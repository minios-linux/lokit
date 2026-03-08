// Package config — lokit.yaml configuration file support.
//
// When a lokit.yaml file exists in the project root, lokit uses it
// as the sole source of truth for translation targets. No auto-detection
// is performed — every target must be explicitly declared.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// YAML schema
// ---------------------------------------------------------------------------

// LokitFile is the top-level lokit.yaml structure.
type LokitFile struct {
	// Languages is the default language list for all targets (can be overridden per target).
	Languages []string `yaml:"languages,omitempty"`
	// SourceLang is the source language code (default "en").
	SourceLang string `yaml:"source_lang,omitempty"`
	// Provider configures default AI provider/model for translate command.
	Provider *ProviderConfig `yaml:"provider,omitempty"`
	// Targets is the list of translation targets.
	Targets []Target `yaml:"targets"`
}

// ProviderConfig defines default provider settings for translation.
type ProviderConfig struct {
	// ID is the provider identifier.
	ID string `yaml:"id"`
	// Model is the model name.
	Model string `yaml:"model"`
	// BaseURL is an optional custom endpoint URL.
	BaseURL string `yaml:"base_url,omitempty"`
	// Prompt is an optional global prompt override.
	Prompt string `yaml:"prompt,omitempty"`
	// Settings contains optional model-specific settings.
	Settings ProviderSettings `yaml:"settings,omitempty"`
}

// ProviderSettings contains optional model-specific tuning values.
type ProviderSettings struct {
	// Temperature controls randomness (0..2).
	Temperature *float64 `yaml:"temperature,omitempty"`
}

// ExtractConfig defines structured extraction settings for matrix-like targets.
type ExtractConfig struct {
	// Source is the extraction source path or path pattern.
	Source string `yaml:"source,omitempty"`
	// IDField is the field name used as stable item identifier.
	IDField string `yaml:"id_field,omitempty"`
	// Fields lists translatable fields to extract.
	Fields []string `yaml:"fields,omitempty"`
}

// Surface describes one translation surface inside a multi-surface target.
type Surface struct {
	Name string `yaml:"name,omitempty"`

	Format     string `yaml:"format,omitempty"`
	Type       string `yaml:"-"`
	Root       string `yaml:"root,omitempty"`
	Dir        string `yaml:"dir,omitempty"`
	Pattern    string `yaml:"pattern,omitempty"`
	Source     string `yaml:"source,omitempty"`
	TargetPath string `yaml:"target,omitempty"`

	POT      string   `yaml:"pot,omitempty"`
	Sources  []string `yaml:"sources,omitempty"`
	Keywords []string `yaml:"keywords,omitempty"`

	Config string `yaml:"config,omitempty"`

	Languages  []string `yaml:"languages,omitempty"`
	SourceLang string   `yaml:"source_lang,omitempty"`
	Prompt     string   `yaml:"prompt,omitempty"`

	LockedKeys     []string `yaml:"locked_keys,omitempty"`
	IgnoredKeys    []string `yaml:"ignored_keys,omitempty"`
	LockedPatterns []string `yaml:"locked_patterns,omitempty"`

	Extract *ExtractConfig `yaml:"extract,omitempty"`
}

// Target describes a single translation unit.
type Target struct {
	// Name is a human-readable label shown in status/logs.
	Name string `yaml:"name"`
	// Format: "gettext", "po4a", "i18next", "vue-i18n", "android", "yaml",
	// "markdown", "properties", "flutter", "js-kv", "desktop", "polkit".
	Format string `yaml:"format"`
	// Type is an internal normalized format field.
	Type string `yaml:"-"`
	// Root is the working directory relative to lokit.yaml (default ".").
	Root string `yaml:"root,omitempty"`
	// Dir is the unified directory option for targets that need a base directory.
	Dir string `yaml:"dir,omitempty"`
	// Pattern is an optional file path template (relative to Dir) for
	// file-per-language targets. It must include "{lang}", e.g.:
	// - "{lang}.json"
	// - "{lang}/common.json"
	// - "locale_{lang}.properties"
	Pattern string `yaml:"pattern,omitempty"`
	// Source is the source file path template relative to Root.
	Source string `yaml:"source,omitempty"`
	// TargetPath is the target file path template relative to Root.
	TargetPath string `yaml:"target,omitempty"`

	// --- gettext options ---

	// POT is the POT template file name relative to Dir.
	POT string `yaml:"pot,omitempty"`
	// Sources are source files/globs to scan for translatable strings.
	Sources []string `yaml:"sources,omitempty"`
	// Keywords are xgettext keyword options (default "_,N_,gettext,eval_gettext").
	Keywords []string `yaml:"keywords,omitempty"`
	// SourceLang overrides the source language for xgettext.
	SourceLang string `yaml:"source_lang,omitempty"`

	// --- po4a options ---

	// Config is the path to po4a.cfg relative to Root.
	Config string `yaml:"config,omitempty"`

	// --- overrides ---

	// Languages overrides the global language list for this target.
	Languages []string `yaml:"languages,omitempty"`
	// Prompt overrides the system prompt for this target.
	Prompt string `yaml:"prompt,omitempty"`

	// --- key filtering ---

	// LockedKeys lists keys whose existing translations must not be overwritten.
	// Locked keys are skipped during translation even with --retranslate.
	// Use --force to override locked keys.
	LockedKeys []string `yaml:"locked_keys,omitempty"`
	// IgnoredKeys lists keys that are completely excluded from translation.
	// Ignored keys are never sent to the AI provider.
	IgnoredKeys []string `yaml:"ignored_keys,omitempty"`
	// LockedPatterns lists regex patterns; keys matching any pattern are treated as locked.
	LockedPatterns []string `yaml:"locked_patterns,omitempty"`

	// Surfaces defines multiple translation surfaces under one logical target.
	Surfaces []Surface `yaml:"surfaces,omitempty"`

	Extract *ExtractConfig `yaml:"extract,omitempty"`
}

// TargetTypeGettext is used for gettext PO projects (shell, python, C source code).
const TargetTypeGettext = "gettext"

// TargetTypePo4a is used for po4a documentation projects.
const TargetTypePo4a = "po4a"

// TargetTypeI18Next is used for flat JSON key-value translation projects.
const TargetTypeI18Next = "i18next"

// TargetTypeVueI18n is used for nested JSON translation projects.
const TargetTypeVueI18n = "vue-i18n"

// TargetTypeAndroid is used for Android strings.xml translation projects.
const TargetTypeAndroid = "android"

// TargetTypeYAML is used for YAML translation files (nested, flat, Rails i18n style).
const TargetTypeYAML = "yaml"

// TargetTypeMarkdown is used for Markdown document translation.
const TargetTypeMarkdown = "markdown"

// TargetTypeProperties is used for Java .properties translation files.
const TargetTypeProperties = "properties"

// TargetTypeFlutter is used for Flutter ARB (Application Resource Bundle) files.
const TargetTypeFlutter = "flutter"

// TargetTypeJSKV is used for JS assignment key-value translation files.
const TargetTypeJSKV = "js-kv"

// TargetTypeDesktop is used for freedesktop .desktop single-file translations.
const TargetTypeDesktop = "desktop"

// TargetTypePolkit is used for polkit .policy single-file translations.
const TargetTypePolkit = "polkit"

type targetFormatMeta struct {
	requiresDir           bool
	requiresPattern       bool
	patternNeedsLang      bool
	requiresPOT           bool
	requiresConfig        bool
	dirExample            string
	patternExample        string
	potExample            string
	configExample         string
	detectUsesPattern     bool
	detectFunc            func(dir string) []string
	detectCustomLanguages func(t Target, absRoot string) []string
}

var targetFormatRegistry = map[string]targetFormatMeta{
	TargetTypeGettext: {
		requiresDir: true,
		requiresPOT: true,
		dirExample:  "po",
		potExample:  "messages.pot",
		detectCustomLanguages: func(t Target, absRoot string) []string {
			return detectLanguagesFlat(filepath.Join(absRoot, t.Dir))
		},
	},
	TargetTypePo4a: {
		requiresConfig: true,
		configExample:  "po4a.cfg",
		detectCustomLanguages: func(t Target, absRoot string) []string {
			cfgPath := filepath.Join(absRoot, t.Config)
			if langs := parsePo4aLangs(cfgPath); len(langs) > 0 {
				return langs
			}
			return DetectLanguagesNested(filepath.Join(filepath.Dir(cfgPath), "po"))
		},
	},
	TargetTypeI18Next: {
		requiresDir:       true,
		requiresPattern:   true,
		patternNeedsLang:  true,
		dirExample:        "public/translations",
		patternExample:    "{lang}.json",
		detectUsesPattern: true,
		detectFunc:        detectLanguagesI18Next,
	},
	TargetTypeVueI18n: {
		requiresDir:       true,
		requiresPattern:   true,
		patternNeedsLang:  true,
		dirExample:        "src/locales",
		patternExample:    "{lang}.json",
		detectUsesPattern: true,
		detectFunc:        detectLanguagesI18Next,
	},
	TargetTypeAndroid: {
		requiresDir: true,
		dirExample:  "app/src/main/res",
		detectFunc:  detectLanguagesAndroid,
	},
	TargetTypeYAML: {
		requiresDir:       true,
		requiresPattern:   true,
		patternNeedsLang:  true,
		dirExample:        "translations",
		patternExample:    "{lang}.yaml",
		detectUsesPattern: true,
		detectFunc:        detectLanguagesYAML,
	},
	TargetTypeMarkdown: {
		requiresDir: true,
		dirExample:  "docs",
		detectFunc:  detectLanguagesMarkdown,
	},
	TargetTypeProperties: {
		requiresDir:       true,
		requiresPattern:   true,
		patternNeedsLang:  true,
		dirExample:        "translations",
		patternExample:    "{lang}.properties",
		detectUsesPattern: true,
		detectFunc:        detectLanguagesProperties,
	},
	TargetTypeFlutter: {
		requiresDir:       true,
		requiresPattern:   true,
		patternNeedsLang:  true,
		dirExample:        "lib/l10n",
		patternExample:    "app_{lang}.arb",
		detectUsesPattern: true,
		detectFunc:        detectLanguagesFlutter,
	},
	TargetTypeJSKV: {
		requiresDir:       true,
		requiresPattern:   true,
		patternNeedsLang:  true,
		dirExample:        "translations",
		patternExample:    "{lang}.js",
		detectUsesPattern: true,
		detectFunc:        detectLanguagesJSKV,
	},
	TargetTypeDesktop: {
		requiresDir:      true,
		requiresPattern:  true,
		dirExample:       ".",
		patternExample:   "myapp.desktop",
		patternNeedsLang: false,
	},
	TargetTypePolkit: {
		requiresDir:      true,
		requiresPattern:  true,
		dirExample:       ".",
		patternExample:   "org.example.policy",
		patternNeedsLang: false,
	},
}

func validTargetTypes() string {
	return "gettext, po4a, i18next, vue-i18n, android, yaml, markdown, properties, flutter, js-kv, desktop, polkit"
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

// LokitFileName is the default config file name.
const LokitFileName = "lokit.yaml"

func validateNoDeprecatedTargetKeys(data []byte, path string) error {
	var raw struct {
		Targets []map[string]any `yaml:"targets"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil
	}

	for i, t := range raw.Targets {
		deprecated := map[string]string{
			"type":             "format",
			"po_dir":           "dir",
			"translations_dir": "dir",
			"res_dir":          "dir",
			"path_pattern":     "pattern",
			"pot_file":         "pot",
			"po4a_config":      "config",
		}
		for oldKey, replacement := range deprecated {
			if _, ok := t[oldKey]; !ok {
				continue
			}
			if replacement == "remove this field (no replacement)" {
				return fmt.Errorf("%s: target #%d uses unsupported key %q; %s", path, i+1, oldKey, replacement)
			}
			return fmt.Errorf("%s: target #%d uses unsupported key %q; use %q", path, i+1, oldKey, replacement)
		}
	}

	return nil
}

var localeCodeStrictRE = regexp.MustCompile(`^[a-z]{2,3}(?:-[A-Z][a-z]{3}|-[A-Z0-9]{2,8})*$`)

func canonicalLocaleHint(locale string) string {
	trimmed := strings.TrimSpace(locale)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "_", "-")
	parts := strings.Split(trimmed, "-")
	if len(parts) == 0 {
		return ""
	}
	parts[0] = strings.ToLower(parts[0])
	for i := 1; i < len(parts); i++ {
		p := parts[i]
		if len(p) == 4 {
			parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
			continue
		}
		parts[i] = strings.ToUpper(p)
	}
	return strings.Join(parts, "-")
}

func validateLocaleCode(path, field, locale string) error {
	if localeCodeStrictRE.MatchString(locale) {
		return nil
	}
	hint := canonicalLocaleHint(locale)
	if hint != "" && hint != locale && localeCodeStrictRE.MatchString(hint) {
		return fmt.Errorf("%s: invalid locale in %s: %q (try %q)", path, field, locale, hint)
	}
	return fmt.Errorf("%s: invalid locale in %s: %q", path, field, locale)
}

func validateLocaleList(path, field string, locales []string) error {
	for i, locale := range locales {
		if err := validateLocaleCode(path, fmt.Sprintf("%s[%d]", field, i), locale); err != nil {
			return err
		}
	}
	return nil
}

func validateProviderConfig(path string, provider *ProviderConfig) error {
	if provider == nil {
		return nil
	}
	if provider.ID == "" {
		return fmt.Errorf("%s: provider.id is required", path)
	}
	if provider.Model == "" {
		return fmt.Errorf("%s: provider.model is required", path)
	}
	supportedProviders := map[string]struct{}{
		"google":        {},
		"gemini":        {},
		"groq":          {},
		"opencode":      {},
		"copilot":       {},
		"openai":        {},
		"ollama":        {},
		"custom-openai": {},
	}
	if _, ok := supportedProviders[provider.ID]; !ok {
		return fmt.Errorf("%s: provider.id %q is not supported", path, provider.ID)
	}
	if provider.BaseURL != "" {
		switch provider.ID {
		case "custom-openai", "ollama":
		default:
			return fmt.Errorf("%s: provider.base_url is only supported for provider.id custom-openai or ollama", path)
		}
	}
	if provider.Settings.Temperature != nil {
		t := *provider.Settings.Temperature
		if t < 0 || t > 2 {
			return fmt.Errorf("%s: provider.settings.temperature must be between 0 and 2", path)
		}
	}
	return nil
}

// LoadLokitFile loads and validates lokit.yaml from the given directory.
// Returns nil if no lokit.yaml exists.
func LoadLokitFile(rootDir string) (*LokitFile, error) {
	path := filepath.Join(rootDir, LokitFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	if err := validateNoDeprecatedTargetKeys(data, path); err != nil {
		return nil, err
	}

	var lf LokitFile
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	// Defaults
	if lf.SourceLang == "" {
		lf.SourceLang = "en"
	}

	if err := validateLocaleCode(path, "source_lang", lf.SourceLang); err != nil {
		return nil, err
	}
	if err := validateLocaleList(path, "languages", lf.Languages); err != nil {
		return nil, err
	}
	if err := validateProviderConfig(path, lf.Provider); err != nil {
		return nil, err
	}

	targetNames := make(map[string]struct{})

	// Validate & resolve targets
	for i := range lf.Targets {
		t := &lf.Targets[i]

		if t.Name == "" {
			return nil, fmt.Errorf("%s: target #%d has no name", path, i+1)
		}
		if _, exists := targetNames[t.Name]; exists {
			return nil, fmt.Errorf("%s: duplicate target name %q", path, t.Name)
		}
		targetNames[t.Name] = struct{}{}
		if len(t.Surfaces) > 0 {
			if t.Format != "" {
				return nil, fmt.Errorf("%s: target %q uses both target format and surfaces; use one style only", path, t.Name)
			}
			if t.Dir != "" || t.Pattern != "" || t.Source != "" || t.TargetPath != "" || t.POT != "" || t.Config != "" {
				return nil, fmt.Errorf("%s: target %q uses both top-level format fields and surfaces; use one style only", path, t.Name)
			}
			for si := range t.Surfaces {
				s := &t.Surfaces[si]
				if s.Format == "" {
					return nil, fmt.Errorf("%s: target %q surface #%d has no format", path, t.Name, si+1)
				}
				s.Type = s.Format
				meta, ok := targetFormatRegistry[s.Type]
				if !ok {
					return nil, fmt.Errorf("%s: target %q surface #%d has unknown type %q (valid: %s)", path, t.Name, si+1, s.Type, validTargetTypes())
				}
				if meta.requiresDir && s.Dir == "" && s.Source == "" && s.TargetPath == "" {
					return nil, fmt.Errorf("%s: target %q surface #%d (%s) requires \"dir\" (e.g. dir: %s)", path, t.Name, si+1, s.Type, meta.dirExample)
				}
				if meta.requiresPOT && s.POT == "" {
					return nil, fmt.Errorf("%s: target %q surface #%d (%s) requires \"pot\" (e.g. pot: %s)", path, t.Name, si+1, s.Type, meta.potExample)
				}
				if meta.requiresConfig && s.Config == "" {
					return nil, fmt.Errorf("%s: target %q surface #%d (%s) requires \"config\" (e.g. config: %s)", path, t.Name, si+1, s.Type, meta.configExample)
				}
				if meta.requiresPattern && s.Source == "" && s.TargetPath == "" {
					if s.Pattern == "" {
						return nil, fmt.Errorf("%s: target %q surface #%d (%s) requires \"pattern\" (e.g. pattern: %s)", path, t.Name, si+1, s.Type, meta.patternExample)
					}
					if meta.patternNeedsLang && !strings.Contains(s.Pattern, "{lang}") {
						return nil, fmt.Errorf("%s: target %q surface #%d (%s) field \"pattern\" must contain \"{lang}\"", path, t.Name, si+1, s.Type)
					}
				}
				if s.TargetPath != "" && meta.patternNeedsLang && !strings.Contains(s.TargetPath, "{lang}") {
					return nil, fmt.Errorf("%s: target %q surface #%d (%s) field \"target\" must contain \"{lang}\"", path, t.Name, si+1, s.Type)
				}
			}
			continue
		}

		if t.Format == "" {
			return nil, fmt.Errorf("%s: target %q has no format", path, t.Name)
		}
		t.Type = t.Format

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
		if err := validateLocaleCode(path, fmt.Sprintf("targets[%d].source_lang", i), t.SourceLang); err != nil {
			return nil, err
		}
		if err := validateLocaleList(path, fmt.Sprintf("targets[%d].languages", i), t.Languages); err != nil {
			return nil, err
		}

		meta, ok := targetFormatRegistry[t.Type]
		if !ok {
			return nil, fmt.Errorf("%s: target %q has unknown type %q (valid: %s)", path, t.Name, t.Type, validTargetTypes())
		}
		if meta.requiresDir && t.Dir == "" && t.TargetPath == "" && t.Source == "" {
			return nil, fmt.Errorf("%s: target %q (%s) requires \"dir\" (e.g. dir: %s)", path, t.Name, t.Type, meta.dirExample)
		}
		if meta.requiresPOT && t.POT == "" {
			return nil, fmt.Errorf("%s: target %q (%s) requires \"pot\" (e.g. pot: %s)", path, t.Name, t.Type, meta.potExample)
		}
		if meta.requiresConfig && t.Config == "" {
			return nil, fmt.Errorf("%s: target %q (%s) requires \"config\" (e.g. config: %s)", path, t.Name, t.Type, meta.configExample)
		}
		if meta.requiresPattern && t.Source == "" && t.TargetPath == "" {
			if t.Pattern == "" {
				return nil, fmt.Errorf("%s: target %q (%s) requires \"pattern\" (e.g. pattern: %s)", path, t.Name, t.Type, meta.patternExample)
			}
			if meta.patternNeedsLang && !strings.Contains(t.Pattern, "{lang}") {
				return nil, fmt.Errorf("%s: target %q (%s) field \"pattern\" must contain \"{lang}\"", path, t.Name, t.Type)
			}
		}
		if t.TargetPath != "" && meta.patternNeedsLang && !strings.Contains(t.TargetPath, "{lang}") {
			return nil, fmt.Errorf("%s: target %q (%s) field \"target\" must contain \"{lang}\"", path, t.Name, t.Type)
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
		if len(t.Surfaces) == 0 {
			absRoot := filepath.Join(absProjectRoot, t.Root)
			expanded := expandTargetIDs(t, absRoot)
			for _, et := range expanded {
				langs := et.Languages
				if len(langs) == 0 {
					langs = detectTargetLanguages(et, absRoot)
				}
				resolved = append(resolved, ResolvedTarget{Target: et, AbsRoot: absRoot, Languages: langs})
			}
			continue
		}

		for i, s := range t.Surfaces {
			st := Target{
				Name:           t.Name,
				Format:         s.Format,
				Type:           s.Type,
				Root:           coalesceString(s.Root, t.Root),
				Dir:            s.Dir,
				Pattern:        s.Pattern,
				Source:         s.Source,
				TargetPath:     s.TargetPath,
				POT:            s.POT,
				Sources:        s.Sources,
				Keywords:       s.Keywords,
				SourceLang:     coalesceString(s.SourceLang, t.SourceLang),
				Config:         s.Config,
				Languages:      s.Languages,
				Prompt:         coalesceString(s.Prompt, t.Prompt),
				LockedKeys:     mergeStringSlices(t.LockedKeys, s.LockedKeys),
				IgnoredKeys:    mergeStringSlices(t.IgnoredKeys, s.IgnoredKeys),
				LockedPatterns: mergeStringSlices(t.LockedPatterns, s.LockedPatterns),
				Extract:        firstExtract(s.Extract, t.Extract),
			}
			if st.Type == "" {
				st.Type = st.Format
			}
			if st.Name == "" {
				st.Name = fmt.Sprintf("surface-%d", i+1)
			}
			if s.Name != "" {
				st.Name = t.Name + "/" + s.Name
			}
			if st.SourceLang == "" {
				st.SourceLang = lf.SourceLang
			}
			if len(st.Languages) == 0 {
				st.Languages = t.Languages
			}
			if len(st.Sources) == 0 {
				st.Sources = t.Sources
			}

			absRoot := filepath.Join(absProjectRoot, st.Root)
			expanded := expandTargetIDs(st, absRoot)
			for _, et := range expanded {
				langs := et.Languages
				if len(langs) == 0 {
					langs = detectTargetLanguages(et, absRoot)
				}
				resolved = append(resolved, ResolvedTarget{Target: et, AbsRoot: absRoot, Languages: langs})
			}
		}
	}

	return resolved, nil
}

func coalesceString(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func mergeStringSlices(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make([]string, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

func firstExtract(a, b *ExtractConfig) *ExtractConfig {
	if a != nil {
		return a
	}
	return b
}

func expandTargetIDs(t Target, absRoot string) []Target {
	if !strings.Contains(t.Source, "{id}") && !strings.Contains(t.TargetPath, "{id}") && !strings.Contains(t.Pattern, "{id}") {
		return []Target{t}
	}

	sourcePattern := t.Source
	if t.Extract != nil && t.Extract.Source != "" {
		sourcePattern = t.Extract.Source
	}
	if sourcePattern == "" {
		sourcePattern = t.Pattern
	}
	if sourcePattern == "" || !strings.Contains(sourcePattern, "{id}") {
		return []Target{t}
	}

	globPattern := sourcePattern
	globPattern = strings.ReplaceAll(globPattern, "{id}", "*")
	globPattern = strings.ReplaceAll(globPattern, "{lang}", t.SourceLang)
	globPattern = strings.ReplaceAll(globPattern, "{source_lang}", t.SourceLang)

	absGlob := filepath.Join(absRoot, filepath.FromSlash(globPattern))
	paths, err := filepath.Glob(absGlob)
	if err != nil || len(paths) == 0 {
		return []Target{t}
	}

	reStr := regexp.QuoteMeta(filepath.ToSlash(sourcePattern))
	reStr = strings.ReplaceAll(reStr, regexp.QuoteMeta("{id}"), "([^/]+)")
	reStr = strings.ReplaceAll(reStr, regexp.QuoteMeta("{lang}"), regexp.QuoteMeta(t.SourceLang))
	reStr = strings.ReplaceAll(reStr, regexp.QuoteMeta("{source_lang}"), regexp.QuoteMeta(t.SourceLang))
	re := regexp.MustCompile("^" + reStr + "$")

	seen := make(map[string]bool)
	ids := make([]string, 0)
	for _, p := range paths {
		rel, err := filepath.Rel(absRoot, p)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		m := re.FindStringSubmatch(rel)
		if len(m) < 2 {
			continue
		}
		id := m[1]
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}

	if len(ids) == 0 {
		return []Target{t}
	}
	sort.Strings(ids)

	out := make([]Target, 0, len(ids))
	for _, id := range ids {
		cp := t
		cp.Source = strings.ReplaceAll(cp.Source, "{id}", id)
		cp.TargetPath = strings.ReplaceAll(cp.TargetPath, "{id}", id)
		cp.Pattern = strings.ReplaceAll(cp.Pattern, "{id}", id)
		if cp.Extract != nil {
			e := *cp.Extract
			e.Source = strings.ReplaceAll(e.Source, "{id}", id)
			cp.Extract = &e
		}
		cp.Name = fmt.Sprintf("%s/%s", t.Name, id)
		out = append(out, cp)
	}

	return out
}

// detectTargetLanguages auto-detects languages from existing translation files.
func detectTargetLanguages(t Target, absRoot string) []string {
	meta, ok := targetFormatRegistry[t.Type]
	if !ok {
		return nil
	}
	if t.TargetPath != "" && strings.Contains(t.TargetPath, "{lang}") {
		return detectLanguagesByPattern(absRoot, t.TargetPath)
	}
	if meta.detectCustomLanguages != nil {
		return meta.detectCustomLanguages(t, absRoot)
	}
	if meta.detectUsesPattern && t.Pattern != "" {
		return detectLanguagesByPattern(filepath.Join(absRoot, t.Dir), t.Pattern)
	}
	if meta.detectFunc != nil {
		return meta.detectFunc(filepath.Join(absRoot, t.Dir))
	}
	return nil
}

func detectLanguagesByPattern(dir, pattern string) []string {
	if !strings.Contains(pattern, "{lang}") {
		return nil
	}

	globPattern := strings.ReplaceAll(pattern, "{lang}", "*")
	absGlob := filepath.Join(dir, filepath.FromSlash(globPattern))
	paths, err := filepath.Glob(absGlob)
	if err != nil {
		return nil
	}

	reStr := regexp.QuoteMeta(filepath.ToSlash(pattern))
	reStr = strings.ReplaceAll(reStr, regexp.QuoteMeta("{lang}"), "([^/]+)")
	re := regexp.MustCompile("^" + reStr + "$")

	seen := make(map[string]struct{})
	var langs []string
	for _, p := range paths {
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		m := re.FindStringSubmatch(rel)
		if len(m) < 2 {
			continue
		}
		lang := m[1]
		if !(isLangCode(lang) || isI18NextLangCode(lang)) {
			continue
		}
		if _, ok := seen[lang]; ok {
			continue
		}
		seen[lang] = struct{}{}
		langs = append(langs, lang)
	}

	sort.Strings(langs)
	return langs
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

// detectLanguagesJSKV finds language codes from .js translation files.
func detectLanguagesJSKV(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var langs []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".js") {
			continue
		}
		lang := strings.TrimSuffix(name, ".js")
		if isLangCode(lang) || isI18NextLangCode(lang) {
			langs = append(langs, lang)
		}
	}
	sort.Strings(langs)
	return langs
}

// detectLanguagesYAML finds language codes from YAML files in a directory.
func detectLanguagesYAML(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var langs []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		for _, ext := range []string{".yaml", ".yml"} {
			if strings.HasSuffix(name, ext) {
				lang := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
				if isLangCode(lang) || isI18NextLangCode(lang) {
					langs = append(langs, lang)
				}
				break
			}
		}
	}
	sort.Strings(langs)
	return langs
}

// detectLanguagesMarkdown finds language codes from Markdown subdirectories.
// It looks for subdirectories named with language codes (e.g. "ru/", "de/").
func detectLanguagesMarkdown(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var langs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if isLangCode(name) || isI18NextLangCode(name) {
			langs = append(langs, name)
		}
	}
	sort.Strings(langs)
	return langs
}

// detectLanguagesProperties finds language codes from .properties files.
// It looks for files named LANG.properties (e.g. en.properties, ru.properties).
func detectLanguagesProperties(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var langs []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".properties") {
			continue
		}
		lang := strings.TrimSuffix(name, ".properties")
		if isLangCode(lang) || isI18NextLangCode(lang) {
			langs = append(langs, lang)
		}
	}
	sort.Strings(langs)
	return langs
}

// detectLanguagesFlutter finds language codes from ARB files in a directory.
// It looks for files named app_LANG.arb or intl_LANG.arb (e.g. app_en.arb, app_ru.arb).
func detectLanguagesFlutter(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var langs []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".arb") {
			continue
		}
		base := strings.TrimSuffix(name, ".arb")
		// Strip "app_" or "intl_" prefix.
		lang := base
		for _, prefix := range []string{"app_", "intl_"} {
			if strings.HasPrefix(base, prefix) {
				lang = strings.TrimPrefix(base, prefix)
				break
			}
		}
		if isLangCode(lang) || isI18NextLangCode(lang) {
			langs = append(langs, lang)
		}
	}
	sort.Strings(langs)
	return langs
}

// AbsPODir returns the absolute PO directory for a gettext target.
func (rt *ResolvedTarget) AbsPODir() string {
	return filepath.Join(rt.AbsRoot, rt.Target.Dir)
}

// AbsPOTFile returns the absolute POT file path for a gettext target.
func (rt *ResolvedTarget) AbsPOTFile() string {
	return filepath.Join(rt.AbsPODir(), rt.Target.POT)
}

// AbsPo4aConfig returns the absolute po4a.cfg path for a po4a target.
func (rt *ResolvedTarget) AbsPo4aConfig() string {
	return filepath.Join(rt.AbsRoot, rt.Target.Config)
}

// AbsTranslationsDir returns the absolute translations directory for file-based targets.
func (rt *ResolvedTarget) AbsTranslationsDir() string {
	if rt.Target.TargetPath != "" || rt.Target.Source != "" {
		return rt.AbsRoot
	}
	return filepath.Join(rt.AbsRoot, rt.Target.Dir)
}

func (rt *ResolvedTarget) translationPathPatterns() []string {
	if rt.Target.TargetPath != "" {
		return []string{rt.Target.TargetPath}
	}
	if rt.Target.Pattern == "" {
		return nil
	}
	return []string{rt.Target.Pattern}
}

func applyLangPattern(pattern, lang string) string {
	return strings.ReplaceAll(pattern, "{lang}", lang)
}

// TranslationPathCandidates returns absolute path candidates for a language.
func (rt *ResolvedTarget) TranslationPathCandidates(lang string) []string {
	patterns := rt.translationPathPatterns()
	if len(patterns) == 0 {
		return nil
	}

	base := rt.AbsTranslationsDir()
	paths := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		rel := applyLangPattern(pattern, lang)
		paths = append(paths, filepath.Join(base, filepath.FromSlash(rel)))
	}
	return paths
}

// TranslationPath returns the primary absolute path for a language.
func (rt *ResolvedTarget) TranslationPath(lang string) string {
	paths := rt.TranslationPathCandidates(lang)
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

// ExistingTranslationPath returns the first existing path for a language.
func (rt *ResolvedTarget) ExistingTranslationPath(lang string) string {
	for _, p := range rt.TranslationPathCandidates(lang) {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// SourcePathCandidates returns source-language path candidates.
func (rt *ResolvedTarget) SourcePathCandidates() []string {
	if rt.Target.Source != "" {
		base := rt.AbsTranslationsDir()
		p := applyLangPattern(rt.Target.Source, rt.Target.SourceLang)
		return []string{filepath.Join(base, filepath.FromSlash(p))}
	}
	return rt.TranslationPathCandidates(rt.Target.SourceLang)
}

// SourcePath returns the preferred source-language path.
func (rt *ResolvedTarget) SourcePath() string {
	paths := rt.SourcePathCandidates()
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

// ExistingSourcePath returns the first existing source-language path.
func (rt *ResolvedTarget) ExistingSourcePath() string {
	return rt.ExistingTranslationPath(rt.Target.SourceLang)
}

// AbsResDir returns the absolute Android res/ directory for android targets.
func (rt *ResolvedTarget) AbsResDir() string {
	return filepath.Join(rt.AbsRoot, rt.Target.Dir)
}

// POPath returns the .po file path for a language in a gettext target.
func (rt *ResolvedTarget) POPath(lang string) string {
	poDir := rt.AbsPODir()
	for _, candidate := range poLocaleCandidates(lang) {
		path := filepath.Join(poDir, candidate+".po")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return filepath.Join(poDir, poLocalePreferred(lang)+".po")
}

// DocsPOPath returns the .po file path for a language in a po4a target.
func (rt *ResolvedTarget) DocsPOPath(lang string) string {
	cfgDir := filepath.Dir(rt.AbsPo4aConfig())
	poDir := filepath.Join(cfgDir, "po")

	for _, candidate := range poLocaleCandidates(lang) {
		// Search for .po file in language subdirectory
		langDir := filepath.Join(poDir, candidate)
		if entries, err := os.ReadDir(langDir); err == nil {
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".po") && !entry.IsDir() {
					return filepath.Join(langDir, entry.Name())
				}
			}
		}
	}
	// Fallback
	preferred := poLocalePreferred(lang)
	return filepath.Join(poDir, preferred, preferred+".po")
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
// Android res/ language detection
// ---------------------------------------------------------------------------

// detectLanguagesAndroid scans an Android res/ directory for values-XX/ directories
// that contain strings.xml, and returns the language codes.
func detectLanguagesAndroid(resDir string) []string {
	entries, err := os.ReadDir(resDir)
	if err != nil {
		return nil
	}

	var langs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "values-") {
			continue
		}
		lang := strings.TrimPrefix(name, "values-")
		if lang == "" {
			continue
		}
		// Check if strings.xml exists in this directory
		stringsPath := filepath.Join(resDir, name, "strings.xml")
		if _, err := os.Stat(stringsPath); err == nil {
			// Convert Android locale format (e.g., "pt-rBR") to standard ("pt-BR")
			lang = androidLocaleToStandard(lang)
			langs = append(langs, lang)
		}
	}
	sort.Strings(langs)
	return langs
}

// androidLocaleToStandard converts Android locale format to standard BCP-47.
// Examples: "pt-rBR" -> "pt-BR", "zh-rCN" -> "zh-CN", "ru" -> "ru"
func androidLocaleToStandard(androidLocale string) string {
	if idx := strings.Index(androidLocale, "-r"); idx >= 0 {
		return androidLocale[:idx] + "-" + androidLocale[idx+2:]
	}
	return androidLocale
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
