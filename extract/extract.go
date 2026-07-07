// Package extract wraps GNU xgettext (and xgotext for Go) for extracting
// translatable strings from source files. Supports all languages that xgettext
// supports plus Go via xgotext.
//
// The package auto-detects source files by extension (and shebang for
// extensionless scripts), then delegates extraction to the appropriate tool.
package extract

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	po "github.com/minios-linux/lokit/internal/format/po"
)

// SupportedExtensions maps file extensions to xgettext language names.
// xgettext can also auto-detect from extensions, but explicit mapping
// lets us know which files to collect.
var SupportedExtensions = map[string]string{
	".py":   "Python",
	".c":    "C",
	".h":    "C",
	".cc":   "C++",
	".cpp":  "C++",
	".cxx":  "C++",
	".hh":   "C++",
	".hpp":  "C++",
	".m":    "ObjectiveC",
	".sh":   "Shell",
	".bash": "Shell",
	".js":   "JavaScript",
	".jsx":  "JavaScript",
	".ts":   "JavaScript",
	".tsx":  "JavaScript",
	".pl":   "Perl",
	".pm":   "Perl",
	".php":  "PHP",
	".java": "Java",
	".cs":   "C#",
	".awk":  "awk",
	".tcl":  "Tcl",
	".el":   "EmacsLisp",
	".scm":  "Scheme",
	".lisp": "Lisp",
	".rb":   "Ruby",
	".lua":  "Lua",
	".vala": "Vala",
	".go":   "Go",
	// GUI / desktop formats — require explicit --language= flag in xgettext
	".glade":       "Glade",
	".ui":          "Glade",
	".desktop":     "Desktop",
	".nemo_action": "Desktop",
	// "xml/polkit" is an internal file-type label, NOT an xgettext --language=
	// value.  Polkit policy files are extracted via a dedicated ITS-based pass
	// in RunXgettext, not through the regular language dispatch.
	".policy": "xml/polkit",
}

// polkitExtensions are file types that xgettext handles as XML/ITS-based
// Polkit policies. We prefer .policy.template files over .policy files when
// extracting because the generated .policy files already contain translated
// strings embedded in them, whereas the .policy.template is the clean source.
var polkitExtensions = map[string]bool{
	".policy": true,
}

// shellShebangs are interpreter prefixes that identify a file as a shell script.
var shellShebangs = []string{
	"#!/bin/bash",
	"#!/bin/sh",
	"#!/usr/bin/env bash",
	"#!/usr/bin/env sh",
}

// defaultKeywords are the xgettext keywords used when none are specified.
var defaultKeywords = []string{
	"_",
	"N_",
	"ngettext:1,2",
	"pgettext:1c,2",
	"npgettext:1c,2,3",
	"gettext",
	"eval_gettext",
}

// skipDirs contains directory names to skip during source file scanning.
// Note: "build" and "dist" skip ANY directory named "build" or "dist" in the
// tree, not only Debian build artifacts. Projects that keep translatable sources
// inside a directory named "build" must list those paths explicitly via
// lokit.yaml `sources` instead of relying on automatic discovery.
var skipDirs = map[string]bool{
	".git":         true,
	".hg":          true,
	".svn":         true,
	"node_modules": true,
	"__pycache__":  true,
	".tox":         true,
	".venv":        true,
	"venv":         true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	".eggs":        true,
}

// shouldSkipPath returns true for Debian build artifacts and compiled PO files
// that should not be scanned for translatable strings.
func shouldSkipPath(path string) bool {
	slashPath := filepath.ToSlash(path)
	parts := strings.Split(slashPath, "/")
	for i, part := range parts {
		if skipDirs[part] {
			return true
		}
		if part == "debian" {
			if i+2 < len(parts) {
				switch parts[i+2] {
				case "DEBIAN", "usr":
					return true
				}
			}
			if i+1 < len(parts) && strings.HasSuffix(parts[i+1], ".debhelper") {
				return true
			}
		}
		if part == "po" && strings.HasSuffix(slashPath, ".mo") {
			return true
		}
	}
	return false
}

// ExtractResult holds the outcome of an extraction.
type ExtractResult struct {
	// SourceFiles is the list of source files scanned.
	SourceFiles []string
	// Languages is the set of detected programming languages.
	Languages []string
	// POTFile is the path to the generated .pot file.
	POTFile string
}

// detectShebang reads the first line of a file and returns the language name
// if the shebang line matches a known interpreter. Returns "" if not recognized.
func detectShebang(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return ""
	}
	line := scanner.Text()
	if !strings.HasPrefix(line, "#!") {
		return ""
	}

	for _, prefix := range shellShebangs {
		if strings.HasPrefix(line, prefix) {
			return "Shell"
		}
	}

	// Python shebangs: #!/usr/bin/python, #!/usr/bin/env python3, etc.
	if strings.Contains(line, "python") {
		return "Python"
	}
	// Perl shebangs
	if strings.Contains(line, "perl") {
		return "Perl"
	}
	// Ruby shebangs
	if strings.Contains(line, "ruby") {
		return "Ruby"
	}

	return ""
}

// FindSources recursively finds all source files with known extensions in dirs.
// Also detects extensionless files by shebang (e.g. bash scripts without .sh).
// Skips common non-source directories (node_modules, .git, __pycache__, etc.)
// and nested git repositories (directories containing a .git entry).
//
// Special handling for Polkit .policy files: when a sibling .policy.template
// exists, the template is used instead of the generated .policy file (the
// generated file already has translated strings embedded in it).
func FindSources(dirs []string) ([]string, error) {
	var files []string
	seen := make(map[string]bool)

	for _, dir := range dirs {
		absDir, _ := filepath.Abs(dir)
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip unreadable entries
			}
			relPath, relErr := filepath.Rel(absDir, path)
			if relErr != nil {
				relPath = path
			}
			if shouldSkipPath(relPath) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if info.IsDir() {
				if skipDirs[info.Name()] {
					return filepath.SkipDir
				}
				// Skip nested git repos (not the root scan dir itself).
				// A directory is a nested repo if it contains a .git entry
				// (either a directory for cloned repos, or a file for submodules).
				absPath, _ := filepath.Abs(path)
				if absPath != absDir {
					gitPath := filepath.Join(path, ".git")
					if _, err := os.Stat(gitPath); err == nil {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if seen[path] {
				return nil
			}
			ext := filepath.Ext(path)
			if _, ok := SupportedExtensions[ext]; ok {
				if polkitExtensions[ext] {
					// Prefer .policy.template over .policy when available.
					// The .policy file may contain already-translated strings.
					templatePath := path + ".template"
					if _, terr := os.Stat(templatePath); terr == nil {
						// .policy.template exists — it will be picked up with
						// the ".policy.template" double-extension handling below.
						// Skip the plain .policy file to avoid duplicates.
						return nil
					}
				}
				seen[path] = true
				files = append(files, path)
				return nil
			}
			// Handle .policy.template: treat as Polkit source (clean template).
			if strings.HasSuffix(path, ".policy.template") {
				if !seen[path] {
					seen[path] = true
					files = append(files, path)
				}
				return nil
			}
			// No known extension — try shebang detection for regular files
			if ext == "" && info.Mode().IsRegular() && info.Size() > 0 && info.Size() < 10*1024*1024 {
				if lang := detectShebang(path); lang != "" {
					seen[path] = true
					files = append(files, path)
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", dir, err)
		}
	}

	sort.Strings(files)
	return files, nil
}

// FileLanguage returns the programming language for a source file,
// checking the extension first and falling back to shebang detection.
// .policy.template files are treated as Polkit XML sources (extracted via ITS).
func FileLanguage(path string) string {
	if strings.HasSuffix(path, ".policy.template") {
		return "xml/polkit"
	}
	ext := filepath.Ext(path)
	if lang, ok := SupportedExtensions[ext]; ok {
		return lang
	}
	return detectShebang(path)
}

// SplitGoFiles separates Go files from non-Go files in a file list.
// Returns (goFiles, otherFiles).
func SplitGoFiles(files []string) (goFiles, otherFiles []string) {
	for _, f := range files {
		if FileLanguage(f) == "Go" {
			goFiles = append(goFiles, f)
		} else {
			otherFiles = append(otherFiles, f)
		}
	}
	return
}

// DetectedLanguages returns the set of programming languages found in the file list.
func DetectedLanguages(files []string) []string {
	langSet := make(map[string]bool)
	for _, f := range files {
		if lang := FileLanguage(f); lang != "" {
			langSet[lang] = true
		}
	}
	var langs []string
	for lang := range langSet {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	return langs
}

// xgettextPass describes a single xgettext invocation for one language group.
// language is the explicit --language= value; empty means auto-detection.
type xgettextPass struct {
	language string
	files    []string
}

type xgettextFileGroups struct {
	regular []string
	shell   []string
	glade   []string
	desktop []string
	polkit  []string
}

// groupFilesForXgettext splits files into the pass groups RunXgettext uses.
func groupFilesForXgettext(files []string) xgettextFileGroups {
	var groups xgettextFileGroups
	for _, f := range files {
		if strings.HasSuffix(f, ".policy.template") {
			groups.polkit = append(groups.polkit, f)
			continue
		}
		ext := filepath.Ext(f)
		switch ext {
		case ".ui", ".glade":
			groups.glade = append(groups.glade, f)
		case ".desktop", ".nemo_action":
			groups.desktop = append(groups.desktop, f)
		case ".policy":
			groups.polkit = append(groups.polkit, f)
		case "":
			groups.shell = append(groups.shell, f)
		default:
			groups.regular = append(groups.regular, f)
		}
	}
	return groups
}

// RunXgettext runs xgettext on the given source files and produces a .pot file.
// Files are grouped by their detected xgettext language and processed in
// separate passes, each joined with --join-existing. This is required because
// some formats (Glade, Desktop, Polkit) need an explicit --language= flag that
// cannot be mixed with auto-detected languages in a single invocation.
//
// Pass order:
//  1. Auto-detected languages (Python, C, C++, Shell, etc.) — no --language flag
//  2. Shell scripts without extension (detected via shebang) — --language=Shell
//  3. Glade .ui / .glade — --language=Glade
//  4. Desktop .desktop / .nemo_action — --language=Desktop
//  5. Polkit .policy / .policy.template — best-effort via ITS if available
//
// Parameters:
//   - files: source files to extract from (must NOT contain .go files)
//   - potFile: output .pot file path
//   - pkgName: package name for the POT header
//   - pkgVersion: package version for the POT header
//   - bugsEmail: bug report email for the POT header
//   - keywords: xgettext keyword functions; if empty, defaultKeywords are used
//   - workDir: working directory for xgettext (empty = current process cwd)
//
// Returns an ExtractResult on success.
// xgettext warnings are suppressed; only errors cause failure.
func RunXgettext(files []string, potFile, pkgName, pkgVersion, bugsEmail string, keywords []string, workDir string) (*ExtractResult, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("no source files to extract from")
	}

	// Check that xgettext is available
	xgettextPath, err := exec.LookPath("xgettext")
	if err != nil {
		return nil, fmt.Errorf("xgettext not found; install gettext: sudo apt install gettext")
	}

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(potFile), 0755); err != nil {
		return nil, fmt.Errorf("creating output directory: %w", err)
	}

	// Start from a clean POT file to avoid stale source references accumulating
	// across runs when using --join-existing in subsequent passes.
	_ = os.Remove(potFile)

	// Use custom keywords or defaults
	kws := defaultKeywords
	if len(keywords) > 0 {
		kws = keywords
	}

	groups := groupFilesForXgettext(files)

	potCreated := false

	// baseArgs builds the shared xgettext flags (without --language).
	baseArgs := func(joinExisting bool) []string {
		args := []string{
			"--output=" + potFile,
			"--from-code=UTF-8",
			"--add-comments=TRANSLATORS:",
		}
		if joinExisting && potCreated {
			args = append(args, "--join-existing")
		}
		for _, kw := range kws {
			args = append(args, "--keyword="+kw)
		}
		if pkgName != "" {
			args = append(args, "--package-name="+pkgName)
		}
		if pkgVersion != "" {
			args = append(args, "--package-version="+pkgVersion)
		}
		if bugsEmail != "" {
			args = append(args, "--msgid-bugs-address="+bugsEmail)
		}
		return args
	}

	// runPass executes one xgettext pass and marks potCreated on success.
	runPass := func(args []string, passFiles []string) error {
		tmpFile, err := os.CreateTemp("", "lokit-files-*.txt")
		if err != nil {
			return fmt.Errorf("creating temp file: %w", err)
		}
		tmpPath := tmpFile.Name()
		defer os.Remove(tmpPath)
		for _, f := range passFiles {
			fmt.Fprintln(tmpFile, f)
		}
		tmpFile.Close()

		args = append(args, "--files-from="+tmpPath)
		cmd := exec.Command(xgettextPath, args...)
		if workDir != "" {
			cmd.Dir = workDir
		}
		var stderrBuf strings.Builder
		cmd.Stderr = &stderrBuf
		if err := cmd.Run(); err != nil {
			if stderrBuf.Len() > 0 {
				fmt.Fprint(os.Stderr, stderrBuf.String())
			}
			return fmt.Errorf("xgettext failed: %w", err)
		}
		if _, err := os.Stat(potFile); err == nil {
			potCreated = true
		}
		return nil
	}

	// Passes 1–4: standard language groups processed with a unified loop.
	// potCreated is false initially, so the first non-empty pass will not
	// receive --join-existing even though we always pass joinExisting=true to
	// baseArgs — that flag is gated on potCreated inside baseArgs.
	for _, p := range []xgettextPass{
		{language: "", files: groups.regular},        // Pass 1: auto-detected
		{language: "Shell", files: groups.shell},     // Pass 2: extensionless scripts
		{language: "Glade", files: groups.glade},     // Pass 3: Glade / GTK Builder
		{language: "Desktop", files: groups.desktop}, // Pass 4: desktop entries
	} {
		if len(p.files) == 0 {
			continue
		}
		args := baseArgs(true)
		if p.language != "" {
			args = append(args, "--language="+p.language)
		}
		if err := runPass(args, p.files); err != nil {
			return nil, err
		}
	}

	// Pass 5: Polkit policy files.
	// xgettext supports polkit XML via ITS (Internationalization Tag Set) rules
	// shipped with polkit or gettext >= 0.19.8. Without ITS rules we cannot
	// produce meaningful output — running --language=C on XML would be noise —
	// so we skip the pass entirely and emit a single warning.
	//
	// .policy.template files are preferred over .policy files and are copied
	// to a temp dir as plain .policy files so xgettext can recognise them
	// (xgettext determines the ITS handler from the .policy extension).
	if len(groups.polkit) > 0 {
		itsFile := findPolkitITS()
		if itsFile == "" {
			fmt.Fprintf(os.Stderr,
				"warning: polkit ITS rules not found; polkit strings skipped"+
					" (install polkit or gettext >= 0.19.8)\n")
		} else {
			tmpDir, err := os.MkdirTemp("", "lokit-polkit-*")
			if err != nil {
				return nil, fmt.Errorf("creating temp dir for polkit: %w", err)
			}
			defer os.RemoveAll(tmpDir)

			var resolvedPolkit []string
			for i, f := range groups.polkit {
				if strings.HasSuffix(f, ".policy.template") {
					// Resolve relative paths against workDir so os.ReadFile works
					// regardless of the process working directory.
					absF := f
					if !filepath.IsAbs(f) && workDir != "" {
						absF = filepath.Join(workDir, f)
					}
					// Copy to temp dir as a plain .policy file.
					// Prefix with the loop index to prevent silent overwrites when
					// two .policy.template files share the same basename in different
					// source directories (e.g. share/polkit/org.foo.policy.template
					// and data/org.foo.policy.template both → org.foo.policy).
					base := filepath.Base(strings.TrimSuffix(f, ".template"))
					dst := filepath.Join(tmpDir, fmt.Sprintf("%d_%s", i, base))
					data, err := os.ReadFile(absF)
					if err != nil {
						return nil, fmt.Errorf("reading %s: %w", absF, err)
					}
					if err := os.WriteFile(dst, data, 0644); err != nil {
						return nil, fmt.Errorf("writing temp polkit file: %w", err)
					}
					resolvedPolkit = append(resolvedPolkit, dst)
				} else {
					resolvedPolkit = append(resolvedPolkit, f)
				}
			}

			args := baseArgs(true)
			args = append(args, "--its="+itsFile)
			if err := runPass(args, resolvedPolkit); err != nil {
				// Non-fatal: polkit extraction is best-effort.
				fmt.Fprintf(os.Stderr, "warning: polkit extraction failed (continuing): %v\n", err)
			}
		}
	}

	// xgettext may not create the file if no strings were found in any pass
	if info, err := os.Stat(potFile); os.IsNotExist(err) {
		// Create an empty POT file so downstream tools don't error
		if err := os.WriteFile(potFile, []byte(""), 0644); err != nil {
			return nil, fmt.Errorf("creating empty POT: %w", err)
		}
	} else if err == nil && info.Size() > 0 {
		potPO, err := po.ParseFile(potFile)
		if err != nil {
			return nil, fmt.Errorf("reading generated POT: %w", err)
		}
		potPO.ClearTranslationsForPOT()
		if err := potPO.WriteFile(potFile); err != nil {
			return nil, fmt.Errorf("normalizing generated POT: %w", err)
		}
	}

	return &ExtractResult{
		SourceFiles: files,
		Languages:   DetectedLanguages(files),
		POTFile:     potFile,
	}, nil
}

// RunXgotext runs xgotext on Go source directories to produce a .pot file.
// xgotext is the string extraction tool for Go projects using the gotext library.
//
// xgotext only accepts a single -in directory, so when multiple directories are
// provided we find their common ancestor and use -exclude to skip irrelevant
// subdirectories.
//
// Parameters:
//   - dirs: directories containing Go source files
//   - potFile: output .pot file path
//   - domain: gettext domain name (used as the POT file basename)
//
// Returns an ExtractResult on success.
func RunXgotext(dirs []string, potFile, domain string) (*ExtractResult, error) {
	xgotextPath, err := exec.LookPath("xgotext")
	if err != nil {
		return nil, fmt.Errorf("xgotext not found; install with: go install github.com/leonelquinteros/gotext/cli/xgotext@latest")
	}

	// Ensure output directory exists
	outDir := filepath.Dir(potFile)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output directory: %w", err)
	}

	if domain == "" {
		domain = "default"
	}

	// xgotext only supports a single -in directory. When we have multiple
	// source directories, use their common ancestor as the input directory.
	// The default -exclude already skips .git; we add other common dirs.
	inputDir := dirs[0]
	if len(dirs) > 1 {
		inputDir = commonAncestor(dirs)
	}

	// Build xgotext arguments with correct CLI flags:
	//   -in     input directory
	//   -out    output directory
	//   -default  domain name
	//   -exclude  comma-separated dirs to skip
	args := []string{
		"-in", inputDir,
		"-out", outDir,
		"-default", domain,
		"-exclude", ".git,vendor,node_modules,.venv,venv,dist,build",
	}

	cmd := exec.Command(xgotextPath, args...)
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	cmd.Stdout = &stderrBuf

	if err := cmd.Run(); err != nil {
		if stderrBuf.Len() > 0 {
			fmt.Fprint(os.Stderr, stderrBuf.String())
		}
		return nil, fmt.Errorf("xgotext failed: %w", err)
	}

	// xgotext writes to <outDir>/<domain>.pot — rename if needed
	generatedPot := filepath.Join(outDir, domain+".pot")
	if generatedPot != potFile {
		if _, err := os.Stat(generatedPot); err == nil {
			if err := os.Rename(generatedPot, potFile); err != nil {
				return nil, fmt.Errorf("renaming POT file: %w", err)
			}
		}
	}

	// xgotext may not create the file if no strings were found
	if _, err := os.Stat(potFile); os.IsNotExist(err) {
		if err := os.WriteFile(potFile, []byte(""), 0644); err != nil {
			return nil, fmt.Errorf("creating empty POT: %w", err)
		}
	}

	// Collect Go files for the result
	var goFiles []string
	for _, d := range dirs {
		filepath.Walk(d, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if filepath.Ext(path) == ".go" && !strings.HasSuffix(path, "_test.go") {
				goFiles = append(goFiles, path)
			}
			return nil
		})
	}

	return &ExtractResult{
		SourceFiles: goFiles,
		Languages:   []string{"Go"},
		POTFile:     potFile,
	}, nil
}

// commonAncestor finds the deepest common ancestor directory of a list of paths.
func commonAncestor(paths []string) string {
	if len(paths) == 0 {
		return "."
	}
	// Normalize all paths to absolute
	absPaths := make([]string, len(paths))
	for i, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "."
		}
		absPaths[i] = abs
	}

	// Split first path into components
	parts := strings.Split(absPaths[0], string(filepath.Separator))

	for _, p := range absPaths[1:] {
		otherParts := strings.Split(p, string(filepath.Separator))
		// Find common prefix length
		minLen := len(parts)
		if len(otherParts) < minLen {
			minLen = len(otherParts)
		}
		commonLen := 0
		for i := 0; i < minLen; i++ {
			if parts[i] == otherParts[i] {
				commonLen = i + 1
			} else {
				break
			}
		}
		parts = parts[:commonLen]
	}

	if len(parts) == 0 {
		return "."
	}
	result := strings.Join(parts, string(filepath.Separator))
	if result == "" {
		return string(filepath.Separator)
	}
	return result
}

// MergePOTFiles merges two POT files into one using msgcat.
// The result is written to outFile.
func MergePOTFiles(file1, file2, outFile string) error {
	msgcatPath, err := exec.LookPath("msgcat")
	if err != nil {
		return fmt.Errorf("msgcat not found; install gettext: sudo apt install gettext")
	}
	cmd := exec.Command(msgcatPath, "--output="+outFile, "--use-first", file1, file2)
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	if err := cmd.Run(); err != nil {
		if stderrBuf.Len() > 0 {
			fmt.Fprint(os.Stderr, stderrBuf.String())
		}
		return fmt.Errorf("msgcat failed: %w", err)
	}
	return nil
}

// FindSourcesIn is a convenience function that scans a single directory.
func FindSourcesIn(dir string) ([]string, error) {
	return FindSources([]string{dir})
}

// findPolkitITS looks for an ITS rule file that can handle polkit .policy XML
// files. Such files are typically shipped by polkit or gettext on some distros.
// Returns an empty string if no suitable ITS file is found.
func findPolkitITS() string {
	// Only *.its files are valid ITS rule descriptors for xgettext --its=.
	// policyconfig-1.dtd is a schema file, not an ITS file; passing it to
	// xgettext produces silent failures or garbage extraction.
	candidates := []string{
		"/usr/share/polkit-1/its/polkit.its",
		"/usr/share/gettext/its/polkit.its",
	}
	// Honour XDG_DATA_DIRS so non-standard prefixes (NixOS, custom installs)
	// are searched before falling back to the hard-coded system paths.
	if xdgDirs := os.Getenv("XDG_DATA_DIRS"); xdgDirs != "" {
		var extra []string
		for _, dir := range strings.Split(xdgDirs, ":") {
			if dir == "" {
				continue
			}
			extra = append(extra,
				filepath.Join(dir, "polkit-1", "its", "polkit.its"),
				filepath.Join(dir, "gettext", "its", "polkit.its"),
			)
		}
		// Prepend so XDG paths are checked first.
		candidates = append(extra, candidates...)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// SupportedExtensionsList returns a sorted list of supported file extensions.
func SupportedExtensionsList() []string {
	var exts []string
	seen := make(map[string]bool)
	for ext := range SupportedExtensions {
		if !seen[ext] {
			seen[ext] = true
			exts = append(exts, ext)
		}
	}
	sort.Strings(exts)
	return exts
}

// SupportedLanguagesList returns a sorted list of unique xgettext language names.
func SupportedLanguagesList() []string {
	langSet := make(map[string]bool)
	for _, lang := range SupportedExtensions {
		langSet[lang] = true
	}
	var langs []string
	for lang := range langSet {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	return langs
}

// FilesByLanguage groups source files by their detected language.
func FilesByLanguage(files []string) map[string][]string {
	result := make(map[string][]string)
	for _, f := range files {
		if lang := FileLanguage(f); lang != "" {
			result[lang] = append(result[lang], f)
		}
	}
	return result
}

// DescribeFiles returns a human-readable summary of the source files found.
func DescribeFiles(files []string) string {
	byLang := FilesByLanguage(files)
	var parts []string
	// Sort for deterministic output
	var langs []string
	for lang := range byLang {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	for _, lang := range langs {
		parts = append(parts, fmt.Sprintf("%d %s", len(byLang[lang]), lang))
	}
	return strings.Join(parts, ", ")
}
