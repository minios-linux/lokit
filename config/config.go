// Package config provides project configuration types and helpers
// for working with lokit.yaml and PO/POT files.
package config

import (
	"os"
	"path/filepath"
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
	ProjectTypeI18Next ProjectType = "i18next" // i18next JSON translations
)

// Project holds project configuration used as an internal bridge structure.
type Project struct {
	// Root is the absolute path to the project (or target) root directory.
	// Used as the base for computing relative source-file paths in POT
	// references. If empty, the current working directory is used.
	Root string
	// Name is the project/package name.
	Name string
	// Version from debian/changelog or fallback.
	Version string
	// PODir is the directory containing .po files (primary, usually code).
	PODir string
	// POTFile is the path to the .pot template file.
	POTFile string
	// SourceDirs are directories to scan for translatable source files.
	SourceDirs []string
	// Keywords are xgettext keyword functions (e.g. "_", "N_", "gettext").
	// If empty, default keywords are used.
	Keywords []string
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
	return resolvePOPath(p.PODir, p.POStructure, p.Name, lang, &p.nestedPOFiles)
}

// resolvePOPath resolves the .po file path for a given language, directory, and structure.
func resolvePOPath(poDir string, structure POStructure, projectName, lang string, cache *map[string]string) string {
	switch structure {
	case POStructureFlat:
		return filepath.Join(poDir, lang+".po")
	case POStructureNested, POStructurePo4a:
		// Check cache first
		if *cache != nil {
			if path, ok := (*cache)[lang]; ok {
				return path
			}
		}
		// Search for .po file in language subdirectory
		langDir := filepath.Join(poDir, lang)
		if entries, err := os.ReadDir(langDir); err == nil {
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".po") && !entry.IsDir() {
					path := filepath.Join(langDir, entry.Name())
					// Cache the result
					if *cache == nil {
						*cache = make(map[string]string)
					}
					(*cache)[lang] = path
					return path
				}
			}
		}
		// Fallback: return expected path using project name
		return filepath.Join(poDir, lang, projectName+".po")
	default:
		return filepath.Join(poDir, lang+".po")
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

// DetectLanguagesNested finds languages from nested structure (po/lang/*.po).
func DetectLanguagesNested(poDir string) []string {
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
