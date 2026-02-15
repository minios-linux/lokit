// Package config implements auto-detection of project settings
// from debian/changelog, existing po/ directory, etc.
package config

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// POStructure indicates how PO files are organized.
type POStructure string

const (
	// POStructureFlat: po/en.po, po/ru.po, po/de.po
	POStructureFlat POStructure = "flat"
	// POStructureNested: po/en/*.po, po/ru/*.po, po/de/*.po
	POStructureNested POStructure = "nested"
	// POStructurePo4a: po4a.cfg + po/lang/*.po (for documentation)
	POStructurePo4a POStructure = "po4a"
	// POStructureUnknown: could not determine
	POStructureUnknown POStructure = "unknown"
)

// ProjectType indicates what kind of translatable content exists.
type ProjectType string

const (
	ProjectTypeCode    ProjectType = "code"    // Source code (gettext)
	ProjectTypeDocs    ProjectType = "docs"    // Documentation (po4a)
	ProjectTypeMixed   ProjectType = "mixed"   // Both code and docs
	ProjectTypeI18Next ProjectType = "i18next" // i18next JSON translations
	ProjectTypeUnknown ProjectType = "unknown"
)

// Project holds auto-detected project configuration.
type Project struct {
	// Name is the project/package name.
	Name string
	// Version from debian/changelog or fallback.
	Version string
	// PODir is the directory containing .po files.
	PODir string
	// POTFile is the path to the .pot template file.
	POTFile string
	// SourceDirs are directories to scan for translatable source files.
	SourceDirs []string
	// Languages detected from existing .po files.
	Languages []string
	// BugsEmail for POT header.
	BugsEmail string
	// CopyrightHolder for POT header.
	CopyrightHolder string
	// POStructure indicates how PO files are organized.
	POStructure POStructure
	// Type indicates what kind of translatable content exists.
	Type ProjectType
	// Po4aConfig is the path to po4a.cfg if found.
	Po4aConfig string
	// ManpagesDir is the directory containing manpages if found.
	ManpagesDir string
	// DocsDir is the directory containing documentation if found.
	DocsDir string

	// I18NextDir is the directory containing i18next JSON translation files.
	// Typically "public/translations" relative to project root.
	I18NextDir string
	// RecipeTransDir is the directory containing per-recipe translation files.
	// Typically "public/data/recipe-translations" relative to project root.
	RecipeTransDir string
	// BlogPostsDir is the directory containing blog posts and translations.
	// Typically "data/blog/posts" relative to project root.
	// Translations live in BlogPostsDir/translations/{slug}.{lang}.md
	BlogPostsDir string
	// SourceLang is the source language code (default "en").
	SourceLang string

	// nestedPOFiles caches resolved paths for nested/po4a structures.
	// key: language code, value: path to .po file
	nestedPOFiles map[string]string
}

// POPath returns the resolved path to the .po file for a given language.
// Works for flat, nested, and po4a structures.
func (p *Project) POPath(lang string) string {
	switch p.POStructure {
	case POStructureFlat:
		return filepath.Join(p.PODir, lang+".po")
	case POStructureNested, POStructurePo4a:
		// Check cache first
		if p.nestedPOFiles != nil {
			if path, ok := p.nestedPOFiles[lang]; ok {
				return path
			}
		}
		// Search for .po file in language subdirectory
		langDir := filepath.Join(p.PODir, lang)
		if entries, err := os.ReadDir(langDir); err == nil {
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".po") && !entry.IsDir() {
					path := filepath.Join(langDir, entry.Name())
					// Cache the result
					if p.nestedPOFiles == nil {
						p.nestedPOFiles = make(map[string]string)
					}
					p.nestedPOFiles[lang] = path
					return path
				}
			}
		}
		// Fallback: return expected path using project name
		return filepath.Join(p.PODir, lang, p.Name+".po")
	default:
		return filepath.Join(p.PODir, lang+".po")
	}
}

// POTPathResolved returns the resolved path to the .pot template file.
// For po4a projects, it searches the pot/ directory.
func (p *Project) POTPathResolved() string {
	// If explicitly set and exists, use it
	if p.POTFile != "" {
		if _, err := os.Stat(p.POTFile); err == nil {
			return p.POTFile
		}
	}

	// For po4a/nested: check pot/ directory next to po/
	if p.POStructure == POStructurePo4a || p.POStructure == POStructureNested {
		potDir := filepath.Join(filepath.Dir(p.PODir), "pot")
		if entries, err := os.ReadDir(potDir); err == nil {
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".pot") && !entry.IsDir() {
					return filepath.Join(potDir, entry.Name())
				}
			}
		}
	}

	return p.POTFile
}

// Detect auto-detects project settings from the working directory.
func Detect(rootDir string) *Project {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		absRoot = rootDir
	}

	p := &Project{
		PODir:           filepath.Join(absRoot, "po"),
		POTFile:         filepath.Join(absRoot, "po", "messages.pot"),
		BugsEmail:       "support@minios.dev",
		CopyrightHolder: "MiniOS Linux",
		POStructure:     POStructureUnknown,
		Type:            ProjectTypeUnknown,
		SourceLang:      "en",
	}

	// Check for i18next project first (public/translations/*.json with _meta)
	if detected := detectI18Next(absRoot); detected != nil {
		detected.Name = filepath.Base(absRoot)
		// Try debian/changelog for name
		if name, version, err := parseChangelog(filepath.Join(absRoot, "debian", "changelog")); err == nil {
			detected.Name = name
			detected.Version = version
		}
		if detected.Version == "" {
			detected.Version = "0.0.0"
		}
		return detected
	}

	// Try debian/changelog
	changelogPath := filepath.Join(absRoot, "debian", "changelog")
	if name, version, err := parseChangelog(changelogPath); err == nil {
		p.Name = name
		p.Version = version
	}

	// Fallback to directory name
	if p.Name == "" {
		p.Name = filepath.Base(absRoot)
	}
	if p.Version == "" {
		p.Version = "0.0.0"
	}

	// Detect project type and structure
	p.detectProjectType(absRoot)

	// Auto-detect source directories (for code projects)
	for _, candidate := range []string{"client", "src", "lib"} {
		dir := filepath.Join(absRoot, candidate)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			p.SourceDirs = append(p.SourceDirs, dir)
		}
	}

	// Auto-detect languages from existing .po files
	p.Languages = p.detectLanguagesWithStructure()

	// Auto-detect POT file
	p.POTFile = p.POTPathResolved()

	return p
}

// detectProjectType determines what kind of translatable content exists.
func (p *Project) detectProjectType(rootDir string) {
	hasCode := false
	hasDocs := false

	// Check for code PO directory (standard po/ at root) - PRIORITY for code projects
	codePODir := filepath.Join(rootDir, "po")
	if info, err := os.Stat(codePODir); err == nil && info.IsDir() {
		p.PODir = codePODir
		p.POStructure = detectPOStructure(codePODir)
		hasCode = true
	}

	// Check for po4a.cfg (documentation project)
	po4aCfgPaths := []string{
		filepath.Join(rootDir, "po4a.cfg"),
		filepath.Join(rootDir, "manpages", "po4a.cfg"),
		filepath.Join(rootDir, "doc", "po4a.cfg"),
		filepath.Join(rootDir, "docs", "po4a.cfg"),
	}
	for _, cfgPath := range po4aCfgPaths {
		if _, err := os.Stat(cfgPath); err == nil {
			p.Po4aConfig = cfgPath
			p.ManpagesDir = filepath.Dir(cfgPath)
			hasDocs = true
			// If we don't have code po/ directory, use manpages/po/ instead
			if !hasCode {
				p.PODir = filepath.Join(p.ManpagesDir, "po")
				p.POStructure = POStructurePo4a
			}
			break
		}
	}

	// Check for manpages directory
	manpageDirs := []string{
		filepath.Join(rootDir, "manpages"),
		filepath.Join(rootDir, "man"),
		filepath.Join(rootDir, "doc", "man"),
	}
	for _, dir := range manpageDirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			if p.ManpagesDir == "" {
				p.ManpagesDir = dir
			}
			// Check if it has po/ subdirectory (docs)
			poDir := filepath.Join(dir, "po")
			if info2, err := os.Stat(poDir); err == nil && info2.IsDir() {
				hasDocs = true
				// Only use manpages/po if we don't have code po/ directory
				if !hasCode && p.POStructure == POStructureUnknown {
					p.PODir = poDir
					p.POStructure = detectPOStructure(poDir)
				}
			}
			break
		}
	}

	// Check for docs directory
	docDirs := []string{
		filepath.Join(rootDir, "docs"),
		filepath.Join(rootDir, "doc"),
		filepath.Join(rootDir, "documentation"),
	}
	for _, dir := range docDirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			p.DocsDir = dir
			break
		}
	}

	// Determine project type
	if hasCode && hasDocs {
		p.Type = ProjectTypeMixed
	} else if hasDocs {
		p.Type = ProjectTypeDocs
	} else if hasCode {
		p.Type = ProjectTypeCode
	} else {
		p.Type = ProjectTypeUnknown
	}
}

// detectPOStructure determines how PO files are organized in a directory.
func detectPOStructure(poDir string) POStructure {
	entries, err := os.ReadDir(poDir)
	if err != nil {
		return POStructureUnknown
	}

	hasPOFiles := false
	hasLangDirs := false

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".po") && !entry.IsDir() {
			hasPOFiles = true
		}
		if entry.IsDir() && isLangCode(name) {
			// Check if this language directory contains .po files
			langDir := filepath.Join(poDir, name)
			if subEntries, err := os.ReadDir(langDir); err == nil {
				for _, sub := range subEntries {
					if strings.HasSuffix(sub.Name(), ".po") {
						hasLangDirs = true
						break
					}
				}
			}
		}
	}

	if hasPOFiles && !hasLangDirs {
		return POStructureFlat
	} else if hasLangDirs {
		return POStructureNested
	}
	return POStructureUnknown
}

// isLangCode checks if a string looks like a language code (en, ru, pt_BR, zh_CN, etc).
func isLangCode(s string) bool {
	if len(s) == 2 {
		return s[0] >= 'a' && s[0] <= 'z' && s[1] >= 'a' && s[1] <= 'z'
	}
	if len(s) == 5 && s[2] == '_' {
		return s[0] >= 'a' && s[0] <= 'z' && s[1] >= 'a' && s[1] <= 'z' &&
			s[3] >= 'A' && s[3] <= 'Z' && s[4] >= 'A' && s[4] <= 'Z'
	}
	return false
}

// detectLanguagesWithStructure finds language codes based on PO structure.
func (p *Project) detectLanguagesWithStructure() []string {
	switch p.POStructure {
	case POStructureFlat:
		return detectLanguagesFlat(p.PODir)
	case POStructureNested, POStructurePo4a:
		return detectLanguagesNested(p.PODir)
	default:
		// Try both
		if langs := detectLanguagesFlat(p.PODir); len(langs) > 0 {
			return langs
		}
		return detectLanguagesNested(p.PODir)
	}
}

// detectLanguagesNested finds languages from nested structure (po/lang/*.po).
func detectLanguagesNested(poDir string) []string {
	entries, err := os.ReadDir(poDir)
	if err != nil {
		return nil
	}

	var langs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		lang := entry.Name()
		if !isLangCode(lang) {
			continue
		}
		// Check if directory contains .po files
		langDir := filepath.Join(poDir, lang)
		if subEntries, err := os.ReadDir(langDir); err == nil {
			for _, sub := range subEntries {
				if strings.HasSuffix(sub.Name(), ".po") {
					langs = append(langs, lang)
					break
				}
			}
		}
	}
	sort.Strings(langs)
	return langs
}

// detectLanguagesFlat finds language codes from .po files in a directory.
func detectLanguagesFlat(poDir string) []string {
	entries, err := os.ReadDir(poDir)
	if err != nil {
		return nil
	}

	var langs []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".po") && !entry.IsDir() {
			lang := strings.TrimSuffix(name, ".po")
			langs = append(langs, lang)
		}
	}
	sort.Strings(langs)
	return langs
}

// parseChangelog extracts package name and version from debian/changelog.
var changelogRe = regexp.MustCompile(`^(\S+)\s+\(([^)]+)\)`)

func parseChangelog(path string) (name, version string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		line := scanner.Text()
		matches := changelogRe.FindStringSubmatch(line)
		if len(matches) >= 3 {
			return matches[1], matches[2], nil
		}
	}
	return "", "", os.ErrNotExist
}

// ---------------------------------------------------------------------------
// i18next detection
// ---------------------------------------------------------------------------

// detectI18Next checks if the project uses i18next JSON translation files.
// Looks for public/translations/*.json files containing { "_meta", "translations" }.
// Returns a configured Project or nil if not an i18next project.
func detectI18Next(rootDir string) *Project {
	// Try common locations for i18next translation directories
	candidates := []string{
		filepath.Join(rootDir, "public", "translations"),
		filepath.Join(rootDir, "src", "translations"),
		filepath.Join(rootDir, "translations"),
	}

	var transDir string
	for _, dir := range candidates {
		if isI18NextDir(dir) {
			transDir = dir
			break
		}
	}
	if transDir == "" {
		return nil
	}

	p := &Project{
		PODir:           filepath.Join(rootDir, "po"),
		POTFile:         filepath.Join(rootDir, "po", "messages.pot"),
		BugsEmail:       "support@minios.dev",
		CopyrightHolder: "MiniOS Linux",
		POStructure:     POStructureUnknown,
		Type:            ProjectTypeI18Next,
		I18NextDir:      transDir,
		SourceLang:      "en",
	}

	// Detect languages from JSON files
	p.Languages = detectLanguagesI18Next(transDir)

	// Detect recipe translations directory
	recipeDir := filepath.Join(rootDir, "public", "data", "recipe-translations")
	if info, err := os.Stat(recipeDir); err == nil && info.IsDir() {
		p.RecipeTransDir = recipeDir
	}

	// Detect blog posts directory (data/blog/posts/ with translations/ subdir)
	blogPostsDirs := []string{
		filepath.Join(rootDir, "data", "blog", "posts"),
		filepath.Join(rootDir, "content", "blog"),
		filepath.Join(rootDir, "blog", "posts"),
	}
	for _, dir := range blogPostsDirs {
		transDir := filepath.Join(dir, "translations")
		if info, err := os.Stat(transDir); err == nil && info.IsDir() {
			p.BlogPostsDir = dir
			break
		}
	}

	return p
}

// isI18NextDir checks if a directory contains i18next-format JSON files.
// A valid i18next file has { "_meta": { ... }, "translations": { ... } }.
func isI18NextDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if isI18NextFile(path) {
			return true
		}
	}
	return false
}

// isI18NextFile checks if a JSON file has the i18next { _meta, translations } format.
func isI18NextFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return false
	}
	_, hasMeta := obj["_meta"]
	_, hasTrans := obj["translations"]
	return hasMeta && hasTrans
}

// detectLanguagesI18Next finds language codes from i18next JSON files.
// File names are language codes: en.json, ru.json, pt-BR.json.
func detectLanguagesI18Next(transDir string) []string {
	entries, err := os.ReadDir(transDir)
	if err != nil {
		return nil
	}

	var langs []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		lang := strings.TrimSuffix(name, ".json")
		if isI18NextLangCode(lang) {
			langs = append(langs, lang)
		}
	}
	sort.Strings(langs)
	return langs
}

// isI18NextLangCode checks if a string looks like a language code.
// Supports: en, ru, de, pt-BR, zh-CN, etc. (BCP 47 with hyphens).
func isI18NextLangCode(s string) bool {
	// Simple 2-letter code
	if len(s) == 2 {
		return s[0] >= 'a' && s[0] <= 'z' && s[1] >= 'a' && s[1] <= 'z'
	}
	// 2-letter + region: pt-BR, zh-CN
	parts := strings.SplitN(s, "-", 2)
	if len(parts) == 2 && len(parts[0]) == 2 && len(parts[1]) >= 2 {
		return parts[0][0] >= 'a' && parts[0][0] <= 'z' &&
			parts[0][1] >= 'a' && parts[0][1] <= 'z'
	}
	return false
}

// I18NextPath returns the path to the i18next JSON file for a given language.
func (p *Project) I18NextPath(lang string) string {
	return filepath.Join(p.I18NextDir, lang+".json")
}

// RecipeTransPath returns the directory containing recipe translations for a language.
func (p *Project) RecipeTransPath(lang string) string {
	if p.RecipeTransDir == "" {
		return ""
	}
	return filepath.Join(p.RecipeTransDir, lang)
}

// BlogPostsTransDir returns the translations subdirectory for blog posts.
func (p *Project) BlogPostsTransDir() string {
	if p.BlogPostsDir == "" {
		return ""
	}
	return filepath.Join(p.BlogPostsDir, "translations")
}
