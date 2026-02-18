// Package pofile implements reading and writing of PO/POT files
// following the GNU gettext format specification.
package pofile

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// Entry represents a single translatable message in a PO file.
type Entry struct {
	// TranslatorComments are lines starting with "# " (translator comments).
	TranslatorComments []string
	// ExtractedComments are lines starting with "#." (extracted/automatic comments).
	ExtractedComments []string
	// References are source code locations, lines starting with "#:".
	References []string
	// Flags are format flags, lines starting with "#,".
	Flags []string
	// PreviousMsgID stores the previous msgid for fuzzy entries, lines starting with "#|".
	PreviousMsgID string

	// MsgCtxt is the message context (msgctxt).
	MsgCtxt string
	// MsgID is the untranslated string.
	MsgID string
	// MsgIDPlural is the untranslated plural string.
	MsgIDPlural string
	// MsgStr is the translated string (singular or the only form).
	MsgStr string
	// MsgStrPlural maps plural form index to translated string.
	MsgStrPlural map[int]string

	// Obsolete marks entries prefixed with "#~".
	Obsolete bool
}

// IsTranslated returns true if the entry has a non-empty translation.
func (e *Entry) IsTranslated() bool {
	if e.MsgID == "" {
		return false // header entry
	}
	if e.IsFuzzy() {
		return false
	}
	if e.MsgIDPlural != "" {
		for _, v := range e.MsgStrPlural {
			if v == "" {
				return false
			}
		}
		return len(e.MsgStrPlural) > 0
	}
	return e.MsgStr != ""
}

// IsFuzzy returns true if the entry is marked fuzzy.
func (e *Entry) IsFuzzy() bool {
	for _, f := range e.Flags {
		if f == "fuzzy" {
			return true
		}
	}
	return false
}

// SetFuzzy adds or removes the fuzzy flag.
func (e *Entry) SetFuzzy(fuzzy bool) {
	if fuzzy && !e.IsFuzzy() {
		e.Flags = append(e.Flags, "fuzzy")
	} else if !fuzzy {
		filtered := make([]string, 0, len(e.Flags))
		for _, f := range e.Flags {
			if f != "fuzzy" {
				filtered = append(filtered, f)
			}
		}
		e.Flags = filtered
	}
}

// HasFlag checks if a specific flag is present.
func (e *Entry) HasFlag(flag string) bool {
	for _, f := range e.Flags {
		if f == flag {
			return true
		}
	}
	return false
}

// File represents a parsed PO/POT file.
type File struct {
	// Header is the metadata entry (msgid "").
	Header *Entry
	// Entries are the translatable message entries.
	Entries []*Entry
}

// NewFile creates a new empty PO file.
func NewFile() *File {
	return &File{
		Header: &Entry{
			MsgID:  "",
			MsgStr: "",
		},
		Entries: make([]*Entry, 0),
	}
}

// HeaderField returns a header field value by name.
func (f *File) HeaderField(name string) string {
	if f.Header == nil {
		return ""
	}
	for _, line := range strings.Split(f.Header.MsgStr, "\n") {
		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			if strings.EqualFold(key, name) {
				return strings.TrimSpace(line[idx+1:])
			}
		}
	}
	return ""
}

// SetHeaderField sets a header field value.
func (f *File) SetHeaderField(name, value string) {
	if f.Header == nil {
		f.Header = &Entry{MsgID: "", MsgStr: ""}
	}

	lines := strings.Split(f.Header.MsgStr, "\n")
	found := false
	for i, line := range lines {
		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			if strings.EqualFold(key, name) {
				lines[i] = name + ": " + value
				found = true
				break
			}
		}
	}
	if !found {
		// Insert before trailing empty line
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = append(lines[:len(lines)-1], name+": "+value, "")
		} else {
			lines = append(lines, name+": "+value)
		}
	}
	f.Header.MsgStr = strings.Join(lines, "\n")
}

// EntryByMsgID finds an entry by its msgid.
func (f *File) EntryByMsgID(msgid string) *Entry {
	for _, e := range f.Entries {
		if e.MsgID == msgid && !e.Obsolete {
			return e
		}
	}
	return nil
}

// Stats returns translation statistics.
func (f *File) Stats() (total, translated, fuzzy, untranslated int) {
	for _, e := range f.Entries {
		if e.MsgID == "" || e.Obsolete {
			continue
		}
		total++
		if e.IsFuzzy() {
			fuzzy++
		} else if e.IsTranslated() {
			translated++
		} else {
			untranslated++
		}
	}
	return
}

// UntranslatedEntries returns entries that have no translation and are not fuzzy.
func (f *File) UntranslatedEntries() []*Entry {
	var result []*Entry
	for _, e := range f.Entries {
		if e.MsgID == "" || e.Obsolete {
			continue
		}
		if !e.IsTranslated() && !e.IsFuzzy() {
			result = append(result, e)
		}
	}
	return result
}

// FuzzyEntries returns entries marked as fuzzy.
func (f *File) FuzzyEntries() []*Entry {
	var result []*Entry
	for _, e := range f.Entries {
		if e.MsgID == "" || e.Obsolete {
			continue
		}
		if e.IsFuzzy() {
			result = append(result, e)
		}
	}
	return result
}

// Parse reads a PO/POT file from a reader.
func Parse(r io.Reader) (*File, error) {
	f := NewFile()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	var current *Entry
	var lastField string // tracks the last msgid/msgstr/etc. field for multiline strings
	lineNum := 0

	flush := func() {
		if current == nil {
			return
		}
		if current.MsgID == "" && !current.Obsolete {
			f.Header = current
		} else {
			f.Entries = append(f.Entries, current)
		}
		current = nil
		lastField = ""
	}

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Empty line separates entries
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}

		if current == nil {
			current = &Entry{
				MsgStrPlural: make(map[int]string),
			}
		}

		// Handle obsolete entries
		if strings.HasPrefix(line, "#~ ") {
			current.Obsolete = true
			line = line[3:]
		}

		// Comment lines
		if strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#~") {
			if strings.HasPrefix(line, "#:") {
				// Reference
				refs := strings.TrimSpace(line[2:])
				current.References = append(current.References, refs)
			} else if strings.HasPrefix(line, "#,") {
				// Flags
				flagStr := strings.TrimSpace(line[2:])
				for _, flag := range strings.Split(flagStr, ",") {
					flag = strings.TrimSpace(flag)
					if flag != "" {
						current.Flags = append(current.Flags, flag)
					}
				}
			} else if strings.HasPrefix(line, "#.") {
				// Extracted comment
				current.ExtractedComments = append(current.ExtractedComments, strings.TrimSpace(line[2:]))
			} else if strings.HasPrefix(line, "#|") {
				// Previous msgid
				prev := strings.TrimSpace(line[2:])
				if strings.HasPrefix(prev, "msgid ") {
					current.PreviousMsgID = unquote(strings.TrimPrefix(prev, "msgid "))
				}
			} else {
				// Translator comment
				comment := line[1:]
				if strings.HasPrefix(comment, " ") {
					comment = comment[1:]
				}
				current.TranslatorComments = append(current.TranslatorComments, comment)
			}
			continue
		}

		// msgctxt
		if strings.HasPrefix(line, "msgctxt ") {
			current.MsgCtxt = unquote(strings.TrimPrefix(line, "msgctxt "))
			lastField = "msgctxt"
			continue
		}

		// msgid_plural
		if strings.HasPrefix(line, "msgid_plural ") {
			current.MsgIDPlural = unquote(strings.TrimPrefix(line, "msgid_plural "))
			lastField = "msgid_plural"
			continue
		}

		// msgid
		if strings.HasPrefix(line, "msgid ") {
			current.MsgID = unquote(strings.TrimPrefix(line, "msgid "))
			lastField = "msgid"
			continue
		}

		// msgstr[N]
		if strings.HasPrefix(line, "msgstr[") {
			var idx int
			var quoted string
			n, err := fmt.Sscanf(line, "msgstr[%d]", &idx)
			if err != nil || n != 1 {
				return nil, fmt.Errorf("line %d: invalid msgstr index: %s", lineNum, line)
			}
			// Find the quoted string after "] "
			bracketEnd := strings.Index(line, "] ")
			if bracketEnd < 0 {
				return nil, fmt.Errorf("line %d: invalid msgstr format: %s", lineNum, line)
			}
			quoted = line[bracketEnd+2:]
			current.MsgStrPlural[idx] = unquote(quoted)
			lastField = fmt.Sprintf("msgstr[%d]", idx)
			continue
		}

		// msgstr
		if strings.HasPrefix(line, "msgstr ") {
			current.MsgStr = unquote(strings.TrimPrefix(line, "msgstr "))
			lastField = "msgstr"
			continue
		}

		// Continuation line (starts with ")
		if strings.HasPrefix(line, "\"") {
			val := unquote(line)
			switch {
			case lastField == "msgctxt":
				current.MsgCtxt += val
			case lastField == "msgid":
				current.MsgID += val
			case lastField == "msgid_plural":
				current.MsgIDPlural += val
			case lastField == "msgstr":
				current.MsgStr += val
			case strings.HasPrefix(lastField, "msgstr["):
				var idx int
				fmt.Sscanf(lastField, "msgstr[%d]", &idx)
				current.MsgStrPlural[idx] += val
			}
			continue
		}
	}

	// Flush last entry
	flush()

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading PO file: %w", err)
	}

	return f, nil
}

// ParseFile reads a PO/POT file from disk.
func ParseFile(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Write writes the PO file to a writer.
func (f *File) Write(w io.Writer) error {
	bw := bufio.NewWriter(w)

	// Write header
	if f.Header != nil {
		if err := writeEntry(bw, f.Header); err != nil {
			return err
		}
	}

	// Write entries
	for _, e := range f.Entries {
		fmt.Fprintln(bw)
		if err := writeEntry(bw, e); err != nil {
			return err
		}
	}

	return bw.Flush()
}

// WriteFile writes the PO file to disk.
func (f *File) WriteFile(path string) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	return f.Write(out)
}

func writeEntry(w *bufio.Writer, e *Entry) error {
	prefix := ""
	if e.Obsolete {
		prefix = "#~ "
	}

	// Translator comments
	for _, c := range e.TranslatorComments {
		fmt.Fprintf(w, "# %s\n", c)
	}

	// Extracted comments
	for _, c := range e.ExtractedComments {
		fmt.Fprintf(w, "#. %s\n", c)
	}

	// References
	for _, ref := range e.References {
		fmt.Fprintf(w, "#: %s\n", ref)
	}

	// Flags
	if len(e.Flags) > 0 {
		fmt.Fprintf(w, "#, %s\n", strings.Join(e.Flags, ", "))
	}

	// Previous msgid
	if e.PreviousMsgID != "" {
		fmt.Fprintf(w, "#| msgid %s\n", quote(e.PreviousMsgID))
	}

	// msgctxt
	if e.MsgCtxt != "" {
		writeQuotedField(w, prefix+"msgctxt", e.MsgCtxt)
	}

	// msgid
	writeQuotedField(w, prefix+"msgid", e.MsgID)

	// msgid_plural
	if e.MsgIDPlural != "" {
		writeQuotedField(w, prefix+"msgid_plural", e.MsgIDPlural)
	}

	// msgstr / msgstr[N]
	if e.MsgIDPlural != "" && len(e.MsgStrPlural) > 0 {
		// Sort plural indices
		indices := make([]int, 0, len(e.MsgStrPlural))
		for idx := range e.MsgStrPlural {
			indices = append(indices, idx)
		}
		sort.Ints(indices)
		for _, idx := range indices {
			writeQuotedField(w, fmt.Sprintf("%smsgstr[%d]", prefix, idx), e.MsgStrPlural[idx])
		}
	} else {
		writeQuotedField(w, prefix+"msgstr", e.MsgStr)
	}

	return nil
}

// writeQuotedField writes a PO field with proper multiline quoting.
func writeQuotedField(w *bufio.Writer, field, value string) {
	if !strings.Contains(value, "\n") {
		fmt.Fprintf(w, "%s %s\n", field, quote(value))
		return
	}

	// Multiline: use empty string on first line
	fmt.Fprintf(w, "%s \"\"\n", field)
	parts := strings.Split(value, "\n")
	for i, part := range parts {
		if i < len(parts)-1 {
			fmt.Fprintf(w, "%s\n", quote(part+"\n"))
		} else if part != "" {
			fmt.Fprintf(w, "%s\n", quote(part))
		}
	}
}

// quote produces a PO-style quoted string.
func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return `"` + s + `"`
}

// unquote removes PO-style quoting from a string.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	s = s[1 : len(s)-1]

	var result strings.Builder
	result.Grow(len(s))

	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				result.WriteByte('\n')
				i++
			case 't':
				result.WriteByte('\t')
				i++
			case '\\':
				result.WriteByte('\\')
				i++
			case '"':
				result.WriteByte('"')
				i++
			default:
				result.WriteByte(s[i])
			}
		} else {
			result.WriteByte(s[i])
		}
	}
	return result.String()
}

// MakeHeader creates a standard PO/POT file header.
func MakeHeader(packageName, packageVersion, bugsEmail, copyrightHolder, language string) *Entry {
	now := time.Now().UTC().Format("2006-01-02 15:04+0000")

	headerStr := fmt.Sprintf(
		"Project-Id-Version: %s %s\n"+
			"Report-Msgid-Bugs-To: %s\n"+
			"POT-Creation-Date: %s\n"+
			"PO-Revision-Date: %s\n"+
			"Last-Translator: \n"+
			"Language-Team: \n"+
			"Language: %s\n"+
			"MIME-Version: 1.0\n"+
			"Content-Type: text/plain; charset=UTF-8\n"+
			"Content-Transfer-Encoding: 8bit\n",
		packageName, packageVersion, bugsEmail, now, now, language,
	)

	comments := []string{
		fmt.Sprintf("Translations for %s.", packageName),
		fmt.Sprintf("Copyright (C) %d %s", time.Now().Year(), copyrightHolder),
		fmt.Sprintf("This file is distributed under the same license as the %s package.", packageName),
	}

	return &Entry{
		TranslatorComments: comments,
		MsgID:              "",
		MsgStr:             headerStr,
	}
}

// PluralFormsForLang returns the standard Plural-Forms header for a language code.
func PluralFormsForLang(lang string) string {
	// Normalize to base language
	base := lang
	if idx := strings.IndexAny(lang, "_-"); idx > 0 {
		base = lang[:idx]
	}

	switch base {
	case "ja", "ko", "zh", "vi", "th", "id", "ms":
		return "nplurals=1; plural=0;"
	case "fr", "pt":
		if lang == "pt_BR" || lang == "pt-BR" {
			return "nplurals=2; plural=(n > 1);"
		}
		return "nplurals=2; plural=(n > 1);"
	case "en", "de", "nl", "sv", "da", "no", "nb", "nn", "fi", "es", "it", "el", "he", "hu", "tr", "bg", "hi", "ur":
		return "nplurals=2; plural=(n != 1);"
	case "ru", "uk", "be", "hr", "sr", "bs":
		return "nplurals=3; plural=(n%10==1 && n%100!=11 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2);"
	case "pl":
		return "nplurals=3; plural=(n==1 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2);"
	case "cs", "sk":
		return "nplurals=3; plural=(n==1 ? 0 : n>=2 && n<=4 ? 1 : 2);"
	case "ro":
		return "nplurals=3; plural=(n==1 ? 0 : (n==0 || (n%100 > 0 && n%100 < 20)) ? 1 : 2);"
	case "lt":
		return "nplurals=3; plural=(n%10==1 && n%100!=11 ? 0 : n%10>=2 && (n%100<10 || n%100>=20) ? 1 : 2);"
	case "lv":
		return "nplurals=3; plural=(n%10==1 && n%100!=11 ? 0 : n != 0 ? 1 : 2);"
	case "ar":
		return "nplurals=6; plural=(n==0 ? 0 : n==1 ? 1 : n==2 ? 2 : n%100>=3 && n%100<=10 ? 3 : n%100>=11 ? 4 : 5);"
	default:
		return "nplurals=2; plural=(n != 1);"
	}
}

// LangNameNative returns the native name of a language.
func LangNameNative(lang string) string {
	names := map[string]string{
		"ar":    "العربية",
		"bg":    "Български",
		"cs":    "Čeština",
		"da":    "Dansk",
		"de":    "Deutsch",
		"el":    "Ελληνικά",
		"en":    "English",
		"es":    "Español",
		"fi":    "Suomi",
		"fr":    "Français",
		"he":    "עברית",
		"hi":    "हिन्दी",
		"hr":    "Hrvatski",
		"hu":    "Magyar",
		"id":    "Bahasa Indonesia",
		"it":    "Italiano",
		"ja":    "日本語",
		"ko":    "한국어",
		"lt":    "Lietuvių",
		"lv":    "Latviešu",
		"ms":    "Bahasa Melayu",
		"nl":    "Nederlands",
		"no":    "Norsk",
		"nb":    "Norsk bokmål",
		"nn":    "Norsk nynorsk",
		"pl":    "Polski",
		"pt":    "Português",
		"pt_BR": "Português (Brasil)",
		"ro":    "Română",
		"ru":    "Русский",
		"sk":    "Slovenčina",
		"sr":    "Српски",
		"sv":    "Svenska",
		"th":    "ไทย",
		"tr":    "Türkçe",
		"uk":    "Українська",
		"vi":    "Tiếng Việt",
		"zh":    "中文",
	}
	if name, ok := names[lang]; ok {
		return name
	}
	return lang
}
