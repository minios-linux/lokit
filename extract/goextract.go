// Go AST-based string extractor for gettext-style wrapper functions.
//
// This extracts translatable strings from Go source files by scanning
// the AST for calls to specified functions (e.g. T("..."), N("...", "...", n)).
// Unlike xgotext, this handles arbitrary wrapper functions, not just direct
// gotext.Get() calls.
//
// Produces a standard PO/POT file compatible with GNU gettext tools.
package extract

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GoKeyword defines a function call to scan for and how to extract arguments.
// Follows xgettext --keyword syntax:
//
//	"T"       — single-argument: T(msgid)
//	"N:1,2"   — positional: N(singular, plural, n) — args 1 and 2 are strings
//	"pgettext:1c,2" — with context: arg 1 is context, arg 2 is msgid
type GoKeyword struct {
	// FuncName is the function name to match (e.g. "T", "N", "Get").
	// Can be a bare name (matches any package) or "pkg.Func" (matches specific selector).
	FuncName string
	// MsgIDArg is the 1-based argument index for msgid (default 1).
	MsgIDArg int
	// PluralArg is the 1-based argument index for plural msgid (0 = none).
	PluralArg int
	// ContextArg is the 1-based argument index for msgctxt (0 = none).
	ContextArg int
}

// ParseGoKeyword parses an xgettext-style keyword spec into a GoKeyword.
// Examples:
//
//	"T"        → GoKeyword{FuncName:"T", MsgIDArg:1}
//	"N:1,2"    → GoKeyword{FuncName:"N", MsgIDArg:1, PluralArg:2}
//	"pgettext:1c,2" → GoKeyword{FuncName:"pgettext", ContextArg:1, MsgIDArg:2}
func ParseGoKeyword(spec string) GoKeyword {
	kw := GoKeyword{MsgIDArg: 1}

	parts := strings.SplitN(spec, ":", 2)
	kw.FuncName = parts[0]

	if len(parts) < 2 {
		return kw
	}

	// Parse argument positions
	argSpecs := strings.Split(parts[1], ",")
	for _, arg := range argSpecs {
		arg = strings.TrimSpace(arg)
		if strings.HasSuffix(arg, "c") {
			// Context argument
			n, err := strconv.Atoi(strings.TrimSuffix(arg, "c"))
			if err == nil {
				kw.ContextArg = n
			}
		} else {
			n, err := strconv.Atoi(arg)
			if err == nil {
				if kw.MsgIDArg == 1 && kw.PluralArg == 0 {
					kw.MsgIDArg = n
				} else {
					kw.PluralArg = n
				}
			}
		}
	}

	return kw
}

// goExtractEntry is a single extracted translatable string.
type goExtractEntry struct {
	MsgID       string
	MsgIDPlural string
	MsgCtxt     string
	Locations   []string // "file:line" references
}

// entryKey returns a unique key for deduplication (msgctxt + msgid).
func (e *goExtractEntry) entryKey() string {
	if e.MsgCtxt != "" {
		return e.MsgCtxt + "\x04" + e.MsgID
	}
	return e.MsgID
}

// RunGoExtract scans Go source files for calls matching the given keywords
// and produces a .pot file in standard gettext format.
//
// Parameters:
//   - dirs: directories to scan recursively for .go files
//   - potFile: output .pot file path
//   - domain: gettext domain name (for POT header)
//   - keywords: keyword specs (xgettext syntax, e.g. "T", "N:1,2")
//
// Returns an ExtractResult with the list of scanned files and output path.
func RunGoExtract(dirs []string, potFile, domain string, keywords []string) (*ExtractResult, error) {
	if len(keywords) == 0 {
		return nil, fmt.Errorf("no keywords specified for Go extraction")
	}

	// Parse keyword specs
	kws := make([]GoKeyword, len(keywords))
	for i, spec := range keywords {
		kws[i] = ParseGoKeyword(spec)
	}

	// Build a lookup map: funcName → []GoKeyword (multiple keywords can share a name)
	kwMap := make(map[string][]GoKeyword)
	for _, kw := range kws {
		kwMap[kw.FuncName] = append(kwMap[kw.FuncName], kw)
	}

	// Collect .go files from directories
	var goFiles []string
	for _, dir := range dirs {
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				name := info.Name()
				if skipDirs[name] {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) == ".go" && !strings.HasSuffix(path, "_test.go") {
				goFiles = append(goFiles, path)
			}
			return nil
		})
	}

	if len(goFiles) == 0 {
		// No Go files found — create empty POT
		if err := os.MkdirAll(filepath.Dir(potFile), 0755); err != nil {
			return nil, fmt.Errorf("creating output directory: %w", err)
		}
		if err := writePOT(potFile, domain, nil); err != nil {
			return nil, err
		}
		return &ExtractResult{POTFile: potFile, Languages: []string{"Go"}}, nil
	}

	// Extract strings from each file
	entries := make(map[string]*goExtractEntry)
	fset := token.NewFileSet()

	for _, path := range goFiles {
		if err := extractFromFile(fset, path, kwMap, entries); err != nil {
			// Log but continue — one bad file shouldn't stop extraction
			fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", path, err)
		}
	}

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(potFile), 0755); err != nil {
		return nil, fmt.Errorf("creating output directory: %w", err)
	}

	// Write POT file
	if err := writePOT(potFile, domain, entries); err != nil {
		return nil, err
	}

	return &ExtractResult{
		SourceFiles: goFiles,
		Languages:   []string{"Go"},
		POTFile:     potFile,
	}, nil
}

// extractFromFile parses a single Go file and extracts matching calls.
func extractFromFile(fset *token.FileSet, path string, kwMap map[string][]GoKeyword, entries map[string]*goExtractEntry) error {
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return err
	}

	// Walk the AST looking for call expressions
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		var funcName string

		switch fn := call.Fun.(type) {
		case *ast.Ident:
			// Direct call: T("...")
			funcName = fn.Name
		case *ast.SelectorExpr:
			// Selector call: pkg.Get("...") or obj.Get("...")
			funcName = fn.Sel.Name
			// Also try "pkg.Func" form
			if ident, ok := fn.X.(*ast.Ident); ok {
				qualified := ident.Name + "." + fn.Sel.Name
				if _, found := kwMap[qualified]; found {
					funcName = qualified
				}
			}
		default:
			return true
		}

		kws, ok := kwMap[funcName]
		if !ok {
			return true
		}

		pos := fset.Position(call.Lparen)
		location := fmt.Sprintf("%s:%d", path, pos.Line)

		for _, kw := range kws {
			extractCall(call, kw, location, entries)
		}

		return true
	})

	return nil
}

// extractCall extracts strings from a single function call matching a keyword.
func extractCall(call *ast.CallExpr, kw GoKeyword, location string, entries map[string]*goExtractEntry) {
	// Get msgid
	msgID := stringArgAt(call, kw.MsgIDArg)
	if msgID == "" {
		return // Not a string literal — skip
	}

	entry := &goExtractEntry{MsgID: msgID}

	// Get plural if specified
	if kw.PluralArg > 0 {
		plural := stringArgAt(call, kw.PluralArg)
		if plural == "" {
			return // Plural must also be a string literal
		}
		entry.MsgIDPlural = plural
	}

	// Get context if specified
	if kw.ContextArg > 0 {
		ctx := stringArgAt(call, kw.ContextArg)
		if ctx == "" {
			return // Context must also be a string literal
		}
		entry.MsgCtxt = ctx
	}

	// Merge with existing entry (same msgid may appear in multiple locations)
	key := entry.entryKey()
	if existing, ok := entries[key]; ok {
		existing.Locations = append(existing.Locations, location)
		// Adopt plural if we didn't have one
		if existing.MsgIDPlural == "" && entry.MsgIDPlural != "" {
			existing.MsgIDPlural = entry.MsgIDPlural
		}
	} else {
		entry.Locations = []string{location}
		entries[key] = entry
	}
}

// stringArgAt extracts the string literal value at 1-based argument position.
// Returns "" if the argument is not a string literal or doesn't exist.
func stringArgAt(call *ast.CallExpr, pos int) string {
	idx := pos - 1 // convert to 0-based
	if idx < 0 || idx >= len(call.Args) {
		return ""
	}
	return stringFromExpr(call.Args[idx])
}

// stringFromExpr extracts a string value from an AST expression.
// Handles string literals and simple concatenation (e.g. "foo" + "bar").
func stringFromExpr(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			s, err := strconv.Unquote(e.Value)
			if err != nil {
				return ""
			}
			return s
		}
	case *ast.BinaryExpr:
		// Handle string concatenation: "foo" + "bar"
		if e.Op == token.ADD {
			left := stringFromExpr(e.X)
			right := stringFromExpr(e.Y)
			if left != "" && right != "" {
				return left + right
			}
		}
	}
	return ""
}

// writePOT writes entries as a standard GNU gettext .pot file.
func writePOT(path, domain string, entries map[string]*goExtractEntry) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating POT file: %w", err)
	}
	defer f.Close()

	// Write POT header
	now := time.Now().UTC().Format("2006-01-02 15:04-0700")
	fmt.Fprintf(f, `# SOME DESCRIPTIVE TITLE.
# Copyright (C) YEAR THE PACKAGE'S COPYRIGHT HOLDER
# This file is distributed under the same license as the %s package.
# FIRST AUTHOR <EMAIL@ADDRESS>, YEAR.
#
#, fuzzy
msgid ""
msgstr ""
"Project-Id-Version: %s\n"
"Report-Msgid-Bugs-To: \n"
"POT-Creation-Date: %s\n"
"PO-Revision-Date: YEAR-MO-DA HO:MI+ZONE\n"
"Last-Translator: FULL NAME <EMAIL@ADDRESS>\n"
"Language-Team: LANGUAGE <LL@li.org>\n"
"Language: \n"
"MIME-Version: 1.0\n"
"Content-Type: text/plain; charset=UTF-8\n"
"Content-Transfer-Encoding: 8bit\n"

`, domain, domain, now)

	if len(entries) == 0 {
		return nil
	}

	// Sort entries by first location for deterministic output
	type sortEntry struct {
		key   string
		entry *goExtractEntry
	}
	sorted := make([]sortEntry, 0, len(entries))
	for k, e := range entries {
		sorted = append(sorted, sortEntry{k, e})
	}
	sort.Slice(sorted, func(i, j int) bool {
		li := sorted[i].entry.Locations[0]
		lj := sorted[j].entry.Locations[0]
		return li < lj
	})

	for _, se := range sorted {
		e := se.entry
		// Write source references
		for _, loc := range e.Locations {
			fmt.Fprintf(f, "#: %s\n", loc)
		}

		// Write context if present
		if e.MsgCtxt != "" {
			fmt.Fprintf(f, "msgctxt %s\n", potQuote(e.MsgCtxt))
		}

		// Write msgid
		fmt.Fprintf(f, "msgid %s\n", potQuote(e.MsgID))

		// Write plural or empty msgstr
		if e.MsgIDPlural != "" {
			fmt.Fprintf(f, "msgid_plural %s\n", potQuote(e.MsgIDPlural))
			fmt.Fprintf(f, "msgstr[0] \"\"\n")
			fmt.Fprintf(f, "msgstr[1] \"\"\n")
		} else {
			fmt.Fprintf(f, "msgstr \"\"\n")
		}
		fmt.Fprintln(f)
	}

	return nil
}

// potQuote formats a string for use in a PO/POT file.
// Handles multi-line strings by splitting on newlines and using
// the standard PO multi-line format (empty first line, then continuation lines).
func potQuote(s string) string {
	// Escape special characters
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\t", `\t`)

	// Handle multi-line strings
	if strings.Contains(s, "\n") {
		lines := strings.Split(s, "\n")
		var parts []string
		parts = append(parts, `""`)
		for i, line := range lines {
			if i < len(lines)-1 {
				parts = append(parts, fmt.Sprintf(`"%s\n"`, line))
			} else if line != "" {
				parts = append(parts, fmt.Sprintf(`"%s"`, line))
			}
		}
		return strings.Join(parts, "\n")
	}

	return fmt.Sprintf(`"%s"`, s)
}
