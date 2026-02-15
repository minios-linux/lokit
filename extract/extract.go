// Package extract wraps the GNU xgettext utility for extracting translatable
// strings from source files. Supports all languages that xgettext supports:
// C, C++, Python, Shell, JavaScript, Perl, PHP, Java, C#, and more.
//
// The package auto-detects source files by extension and delegates extraction
// to xgettext, producing a standard .pot file.
package extract

import (
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

// FindSources recursively finds all source files with known extensions in dirs.
// Skips common non-source directories (node_modules, .git, __pycache__, etc.).
func FindSources(dirs []string) ([]string, error) {
	var files []string
	seen := make(map[string]bool)

	for _, dir := range dirs {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip unreadable entries
			}
			if info.IsDir() {
				if skipDirs[info.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(path)
			if _, ok := SupportedExtensions[ext]; ok {
				if !seen[path] {
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

// DetectedLanguages returns the set of programming languages found in the file list.
func DetectedLanguages(files []string) []string {
	langSet := make(map[string]bool)
	for _, f := range files {
		ext := filepath.Ext(f)
		if lang, ok := SupportedExtensions[ext]; ok {
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
//   - files: source files to extract from
//   - potFile: output .pot file path
//   - pkgName: package name for the POT header
//   - pkgVersion: package version for the POT header
//   - bugsEmail: bug report email for the POT header
//
// Returns an ExtractResult on success.
// xgettext warnings are suppressed; only errors cause failure.
func RunXgettext(files []string, potFile, pkgName, pkgVersion, bugsEmail string) (*ExtractResult, error) {
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

	// Build xgettext arguments
	args := []string{
		"--output=" + potFile,
		"--from-code=UTF-8",
		"--add-comments=TRANSLATORS:",
		"--keyword=_",
		"--keyword=N_",
		"--keyword=ngettext:1,2",
		"--keyword=pgettext:1c,2",
		"--keyword=npgettext:1c,2,3",
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

	// Write file list to a temp file to avoid arg length limits
	tmpFile, err := os.CreateTemp("", "lokit-files-*.txt")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	for _, f := range files {
		fmt.Fprintln(tmpFile, f)
	}
	tmpFile.Close()

	args = append(args, "--files-from="+tmpPath)

	// Run xgettext (suppress warnings, only show on failure)
	cmd := exec.Command(xgettextPath, args...)
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		// Show stderr on failure for diagnostics
		if stderrBuf.Len() > 0 {
			fmt.Fprint(os.Stderr, stderrBuf.String())
		}
		return nil, fmt.Errorf("xgettext failed: %w", err)
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

// FilesByLanguage groups source files by their xgettext language.
func FilesByLanguage(files []string) map[string][]string {
	result := make(map[string][]string)
	for _, f := range files {
		ext := filepath.Ext(f)
		if lang, ok := SupportedExtensions[ext]; ok {
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
