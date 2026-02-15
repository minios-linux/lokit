// Package config — .lokit.yaml configuration file support.
//
// When a .lokit.yaml file exists in the project root, lokit uses it
// as the sole source of truth for translation targets. No auto-detection
// is performed — every target must be explicitly declared.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// YAML schema
// ---------------------------------------------------------------------------

// LokitFile is the top-level .lokit.yaml structure.
type LokitFile struct {
	// Languages is the default language list for all targets (can be overridden per target).
	Languages []string `yaml:"languages,omitempty"`
	// SourceLang is the source language code (default "en").
	SourceLang string `yaml:"source_lang,omitempty"`
	// Targets is the list of translation targets.
	Targets []Target `yaml:"targets"`
}

// Target describes a single translation unit (PO directory, po4a config, i18next dir, etc.).
type Target struct {
	// Name is a human-readable label shown in status/logs.
	Name string `yaml:"name"`
	// Type: "gettext", "po4a", "i18next", "json".
	Type string `yaml:"type"`
	// Root is the working directory relative to .lokit.yaml (default ".").
	Root string `yaml:"root,omitempty"`

	// --- gettext options ---

	// PODir is the PO directory relative to Root (default "po").
	PODir string `yaml:"po_dir,omitempty"`
	// POTFile is the POT template file relative to Root.
	POTFile string `yaml:"pot_file,omitempty"`
	// Sources are source files/globs to scan for translatable strings.
	Sources []string `yaml:"sources,omitempty"`
	// Keywords are xgettext keyword options (default "_,N_,gettext,eval_gettext").
	Keywords []string `yaml:"keywords,omitempty"`
	// SourceLang overrides the source language for xgettext.
	SourceLang string `yaml:"source_lang,omitempty"`

	// --- po4a options ---

	// Po4aConfig is the path to po4a.cfg relative to Root.
	Po4aConfig string `yaml:"po4a_config,omitempty"`

	// --- i18next / json options ---

	// TranslationsDir is the directory containing JSON translation files.
	TranslationsDir string `yaml:"translations_dir,omitempty"`
	// RecipesDir is the directory with per-recipe translation files (i18next).
	RecipesDir string `yaml:"recipes_dir,omitempty"`
	// BlogDir is the directory with blog posts + translations (i18next).
	BlogDir string `yaml:"blog_dir,omitempty"`

	// --- overrides ---

	// Languages overrides the global language list for this target.
	Languages []string `yaml:"languages,omitempty"`
	// Prompt overrides the system prompt for this target.
	Prompt string `yaml:"prompt,omitempty"`
}

// TargetTypeGettext is used for gettext PO projects (shell, python, C source code).
const TargetTypeGettext = "gettext"

// TargetTypePo4a is used for po4a documentation projects.
const TargetTypePo4a = "po4a"

// TargetTypeI18Next is used for i18next JSON translation projects.
const TargetTypeI18Next = "i18next"

// TargetTypeJSON is used for simple JSON translation projects { "translations": {...} }.
const TargetTypeJSON = "json"

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

// LokitFileName is the default config file name.
const LokitFileName = ".lokit.yaml"

// LoadLokitFile loads and validates .lokit.yaml from the given directory.
// Returns nil if no .lokit.yaml exists.
func LoadLokitFile(rootDir string) (*LokitFile, error) {
	path := filepath.Join(rootDir, LokitFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var lf LokitFile
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	// Defaults
	if lf.SourceLang == "" {
		lf.SourceLang = "en"
	}

	// Validate & resolve targets
	for i := range lf.Targets {
		t := &lf.Targets[i]

		if t.Name == "" {
			return nil, fmt.Errorf("%s: target #%d has no name", path, i+1)
		}
		if t.Type == "" {
			return nil, fmt.Errorf("%s: target %q has no type", path, t.Name)
		}

		// Default root
		if t.Root == "" {
			t.Root = "."
		}

		// Inherit global languages if not overridden
		if len(t.Languages) == 0 {
			t.Languages = lf.Languages
		}

		// Inherit source lang
		if t.SourceLang == "" {
			t.SourceLang = lf.SourceLang
		}

		switch t.Type {
		case TargetTypeGettext:
			if t.PODir == "" {
				t.PODir = "po"
			}
			if t.POTFile == "" {
				t.POTFile = filepath.Join(t.PODir, "messages.pot")
			}
		case TargetTypePo4a:
			if t.Po4aConfig == "" {
				// Try to find po4a.cfg relative to root
				t.Po4aConfig = "po4a.cfg"
			}
		case TargetTypeI18Next, TargetTypeJSON:
			if t.TranslationsDir == "" {
				t.TranslationsDir = "public/translations"
			}
		default:
			return nil, fmt.Errorf("%s: target %q has unknown type %q (valid: gettext, po4a, i18next, json)", path, t.Name, t.Type)
		}
	}

	return &lf, nil
}

// ---------------------------------------------------------------------------
// Resolving targets to Projects
// ---------------------------------------------------------------------------

// ResolvedTarget holds a fully resolved target with absolute paths.
type ResolvedTarget struct {
	Target    Target
	AbsRoot   string
	Languages []string
}

// Resolve converts a LokitFile into a list of ResolvedTargets with absolute paths.
// It also auto-detects languages from existing files if not specified.
func (lf *LokitFile) Resolve(projectRoot string) ([]ResolvedTarget, error) {
	absProjectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}

	var resolved []ResolvedTarget
	for _, t := range lf.Targets {
		absRoot := filepath.Join(absProjectRoot, t.Root)

		// Auto-detect languages if not specified
		langs := t.Languages
		if len(langs) == 0 {
			langs = detectTargetLanguages(t, absRoot)
		}

		resolved = append(resolved, ResolvedTarget{
			Target:    t,
			AbsRoot:   absRoot,
			Languages: langs,
		})
	}

	return resolved, nil
}

// detectTargetLanguages auto-detects languages from existing translation files.
func detectTargetLanguages(t Target, absRoot string) []string {
	switch t.Type {
	case TargetTypeGettext:
		poDir := filepath.Join(absRoot, t.PODir)
		return detectLanguagesFlat(poDir)

	case TargetTypePo4a:
		cfgPath := filepath.Join(absRoot, t.Po4aConfig)
		if langs := parsePo4aLangs(cfgPath); len(langs) > 0 {
			return langs
		}
		// Fallback: scan po/ subdirectory
		poDir := filepath.Join(filepath.Dir(cfgPath), "po")
		return detectLanguagesNested(poDir)

	case TargetTypeI18Next:
		transDir := filepath.Join(absRoot, t.TranslationsDir)
		return detectLanguagesI18Next(transDir)

	case TargetTypeJSON:
		transDir := filepath.Join(absRoot, t.TranslationsDir)
		return detectLanguagesJSON(transDir)
	}
	return nil
}

// detectLanguagesJSON finds language codes from simple JSON files in a directory.
func detectLanguagesJSON(dir string) []string {
	entries, err := os.ReadDir(dir)
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
		if isLangCode(lang) || isI18NextLangCode(lang) {
			langs = append(langs, lang)
		}
	}
	sort.Strings(langs)
	return langs
}

// AbsPODir returns the absolute PO directory for a gettext target.
func (rt *ResolvedTarget) AbsPODir() string {
	return filepath.Join(rt.AbsRoot, rt.Target.PODir)
}

// AbsPOTFile returns the absolute POT file path for a gettext target.
func (rt *ResolvedTarget) AbsPOTFile() string {
	return filepath.Join(rt.AbsRoot, rt.Target.POTFile)
}

// AbsPo4aConfig returns the absolute po4a.cfg path for a po4a target.
func (rt *ResolvedTarget) AbsPo4aConfig() string {
	return filepath.Join(rt.AbsRoot, rt.Target.Po4aConfig)
}

// AbsTranslationsDir returns the absolute translations dir for i18next/json targets.
func (rt *ResolvedTarget) AbsTranslationsDir() string {
	return filepath.Join(rt.AbsRoot, rt.Target.TranslationsDir)
}

// POPath returns the .po file path for a language in a gettext target.
func (rt *ResolvedTarget) POPath(lang string) string {
	return filepath.Join(rt.AbsPODir(), lang+".po")
}

// DocsPOPath returns the .po file path for a language in a po4a target.
func (rt *ResolvedTarget) DocsPOPath(lang string) string {
	cfgDir := filepath.Dir(rt.AbsPo4aConfig())
	poDir := filepath.Join(cfgDir, "po")

	// Search for .po file in language subdirectory
	langDir := filepath.Join(poDir, lang)
	if entries, err := os.ReadDir(langDir); err == nil {
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".po") && !entry.IsDir() {
				return filepath.Join(langDir, entry.Name())
			}
		}
	}
	// Fallback
	return filepath.Join(poDir, lang, lang+".po")
}

// AllLanguages returns the deduplicated union of all target languages.
func (lf *LokitFile) AllLanguages(projectRoot string) []string {
	seen := make(map[string]bool)
	var all []string

	resolved, err := lf.Resolve(projectRoot)
	if err != nil {
		return lf.Languages
	}

	for _, rt := range resolved {
		for _, lang := range rt.Languages {
			if !seen[lang] {
				seen[lang] = true
				all = append(all, lang)
			}
		}
	}

	sort.Strings(all)
	return all
}

// ---------------------------------------------------------------------------
// po4a.cfg parser
// ---------------------------------------------------------------------------

// parsePo4aLangs extracts the language list from a po4a.cfg [po4a_langs] line.
func parsePo4aLangs(cfgPath string) []string {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[po4a_langs]") {
			langsStr := strings.TrimPrefix(line, "[po4a_langs]")
			langsStr = strings.TrimSpace(langsStr)
			if langsStr == "" {
				continue
			}
			langs := strings.Fields(langsStr)
			sort.Strings(langs)
			return langs
		}
	}
	return nil
}
