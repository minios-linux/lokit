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
func FindSources(dirs []string) ([]string, error) {
	var files []string
	seen := make(map[string]bool)

	for _, dir := range dirs {
		absDir, _ := filepath.Abs(dir)
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip unreadable entries
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
				seen[path] = true
				files = append(files, path)
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
func FileLanguage(path string) string {
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

// RunXgettext runs xgettext on the given source files and produces a .pot file.
// It auto-detects languages from file extensions (xgettext does this natively).
//
// Parameters:
//   - files: source files to extract from (must NOT contain .go files)
//   - potFile: output .pot file path
//   - pkgName: package name for the POT header
//   - pkgVersion: package version for the POT header
//   - bugsEmail: bug report email for the POT header
//   - keywords: xgettext keyword functions; if empty, defaultKeywords are used
//
// Returns an ExtractResult on success.
// xgettext warnings are suppressed; only errors cause failure.
func RunXgettext(files []string, potFile, pkgName, pkgVersion, bugsEmail string, keywords []string) (*ExtractResult, error) {
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

	// Use custom keywords or defaults
	kws := defaultKeywords
	if len(keywords) > 0 {
		kws = keywords
	}

	// Build xgettext arguments
	args := []string{
		"--output=" + potFile,
		"--from-code=UTF-8",
		"--add-comments=TRANSLATORS:",
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

	// For extensionless files detected via shebang, we need to tell xgettext
	// the language explicitly. Create a temp file list, and for extensionless
	// files prepend with --language on the command line.
	var shellFiles []string
	var regularFiles []string
	for _, f := range files {
		ext := filepath.Ext(f)
		if ext == "" {
			shellFiles = append(shellFiles, f)
		} else {
			regularFiles = append(regularFiles, f)
		}
	}

	// Write regular files to a temp file
	tmpFile, err := os.CreateTemp("", "lokit-files-*.txt")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	for _, f := range regularFiles {
		fmt.Fprintln(tmpFile, f)
	}
	// For extensionless (shebang-detected) files, add them with explicit language flag
	// xgettext supports --language in the files-from, but it's simpler to handle
	// them in a separate pass. Instead, we add them to the file list and use
	// --language=Shell for the extensionless pass.
	tmpFile.Close()

	if len(regularFiles) > 0 {
		args = append(args, "--files-from="+tmpPath)
	}

	// If we have extensionless shell files, we need a second pass or add them
	// directly on the command line with --language override.
	// Approach: run xgettext on regular files first, then on shell files with
	// --language=Shell --join-existing to merge into the same POT.
	if len(regularFiles) > 0 {
		cmd := exec.Command(xgettextPath, args...)
		var stderrBuf strings.Builder
		cmd.Stderr = &stderrBuf
		if err := cmd.Run(); err != nil {
			if stderrBuf.Len() > 0 {
				fmt.Fprint(os.Stderr, stderrBuf.String())
			}
			return nil, fmt.Errorf("xgettext failed: %w", err)
		}
	}

	if len(shellFiles) > 0 {
		shellArgs := []string{
			"--output=" + potFile,
			"--from-code=UTF-8",
			"--add-comments=TRANSLATORS:",
			"--language=Shell",
		}
		if len(regularFiles) > 0 {
			shellArgs = append(shellArgs, "--join-existing")
		}
		for _, kw := range kws {
			shellArgs = append(shellArgs, "--keyword="+kw)
		}
		shellArgs = append(shellArgs, shellFiles...)
		cmd := exec.Command(xgettextPath, shellArgs...)
		var stderrBuf strings.Builder
		cmd.Stderr = &stderrBuf
		if err := cmd.Run(); err != nil {
			if stderrBuf.Len() > 0 {
				fmt.Fprint(os.Stderr, stderrBuf.String())
			}
			return nil, fmt.Errorf("xgettext (shell scripts) failed: %w", err)
		}
	}

	// xgettext may not create the file if no strings were found
	if _, err := os.Stat(potFile); os.IsNotExist(err) {
		// Create an empty POT file so downstream tools don't error
		if err := os.WriteFile(potFile, []byte(""), 0644); err != nil {
			return nil, fmt.Errorf("creating empty POT: %w", err)
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
