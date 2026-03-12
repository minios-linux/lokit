package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProjectPOPathAndPOTResolution(t *testing.T) {
	t.Run("flat structure", func(t *testing.T) {
		p := &Project{PODir: "/tmp/po", POStructure: POStructureFlat}
		got := p.POPath("ru")
		want := filepath.Join("/tmp/po", "ru.po")
		if got != want {
			t.Fatalf("POPath(flat) = %q, want %q", got, want)
		}
	})

	t.Run("flat structure resolves bcp47 to underscore po filename", func(t *testing.T) {
		dir := t.TempDir()
		poDir := filepath.Join(dir, "po")
		if err := os.MkdirAll(poDir, 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		existing := filepath.Join(poDir, "pt_BR.po")
		if err := os.WriteFile(existing, []byte(""), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		p := &Project{PODir: poDir, POStructure: POStructureFlat}
		if got := p.POPath("pt-BR"); got != existing {
			t.Fatalf("POPath(flat, pt-BR) = %q, want %q", got, existing)
		}
	})

	t.Run("nested structure picks existing file and caches", func(t *testing.T) {
		dir := t.TempDir()
		poDir := filepath.Join(dir, "po")
		langDir := filepath.Join(poDir, "ru")
		if err := os.MkdirAll(langDir, 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		existing := filepath.Join(langDir, "docs.po")
		if err := os.WriteFile(existing, []byte(""), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		p := &Project{Name: "lokit", PODir: poDir, POStructure: POStructureNested}
		first := p.POPath("ru")
		if first != existing {
			t.Fatalf("first POPath(nested) = %q, want %q", first, existing)
		}

		if err := os.Remove(existing); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		second := p.POPath("ru")
		if second != existing {
			t.Fatalf("second POPath(nested, cached) = %q, want %q", second, existing)
		}
	})

	t.Run("nested fallback path", func(t *testing.T) {
		dir := t.TempDir()
		p := &Project{Name: "lokit", PODir: filepath.Join(dir, "po"), POStructure: POStructureNested}
		got := p.POPath("de")
		want := filepath.Join(dir, "po", "de", "lokit.po")
		if got != want {
			t.Fatalf("POPath(nested fallback) = %q, want %q", got, want)
		}
	})

	t.Run("nested fallback path prefers underscore variant for region locales", func(t *testing.T) {
		dir := t.TempDir()
		p := &Project{Name: "lokit", PODir: filepath.Join(dir, "po"), POStructure: POStructureNested}
		got := p.POPath("pt-BR")
		want := filepath.Join(dir, "po", "pt_BR", "lokit.po")
		if got != want {
			t.Fatalf("POPath(nested fallback, pt-BR) = %q, want %q", got, want)
		}
	})

	t.Run("pot path resolved from pot directory", func(t *testing.T) {
		dir := t.TempDir()
		poDir := filepath.Join(dir, "po")
		potDir := filepath.Join(dir, "pot")
		if err := os.MkdirAll(poDir, 0755); err != nil {
			t.Fatalf("MkdirAll po: %v", err)
		}
		if err := os.MkdirAll(potDir, 0755); err != nil {
			t.Fatalf("MkdirAll pot: %v", err)
		}
		pot := filepath.Join(potDir, "docs.pot")
		if err := os.WriteFile(pot, []byte(""), 0644); err != nil {
			t.Fatalf("WriteFile pot: %v", err)
		}

		p := &Project{PODir: poDir, POStructure: POStructurePo4a}
		if got := p.POTPathResolved(); got != pot {
			t.Fatalf("POTPathResolved() = %q, want %q", got, pot)
		}
	})
}

func TestResolvedTargetPOPathLocaleVariants(t *testing.T) {
	t.Run("gettext target resolves pt-BR to existing pt_BR.po", func(t *testing.T) {
		dir := t.TempDir()
		poDir := filepath.Join(dir, "po")
		if err := os.MkdirAll(poDir, 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		existing := filepath.Join(poDir, "pt_BR.po")
		if err := os.WriteFile(existing, []byte(""), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		rt := &ResolvedTarget{AbsRoot: dir, Target: Target{Dir: "po"}}
		if got := rt.POPath("pt-BR"); got != existing {
			t.Fatalf("ResolvedTarget.POPath(pt-BR) = %q, want %q", got, existing)
		}
	})

	t.Run("po4a docs path resolves pt-BR to existing pt_BR directory", func(t *testing.T) {
		dir := t.TempDir()
		cfgDir := filepath.Join(dir, "manpages")
		if err := os.MkdirAll(cfgDir, 0755); err != nil {
			t.Fatalf("MkdirAll cfgDir: %v", err)
		}
		cfgPath := filepath.Join(cfgDir, "po4a.cfg")
		if err := os.WriteFile(cfgPath, []byte("[po4a_langs] pt-BR\n"), 0644); err != nil {
			t.Fatalf("WriteFile cfg: %v", err)
		}

		langDir := filepath.Join(cfgDir, "po", "pt_BR")
		if err := os.MkdirAll(langDir, 0755); err != nil {
			t.Fatalf("MkdirAll langDir: %v", err)
		}
		existing := filepath.Join(langDir, "docs.po")
		if err := os.WriteFile(existing, []byte(""), 0644); err != nil {
			t.Fatalf("WriteFile po: %v", err)
		}

		rt := &ResolvedTarget{AbsRoot: dir, Target: Target{Config: "manpages/po4a.cfg"}}
		if got := rt.DocsPOPath("pt-BR"); got != existing {
			t.Fatalf("ResolvedTarget.DocsPOPath(pt-BR) = %q, want %q", got, existing)
		}
	})
}

func TestLoadLokitFileDefaultsAndValidation(t *testing.T) {
	t.Run("missing file returns nil", func(t *testing.T) {
		dir := t.TempDir()
		lf, err := LoadLokitFile(dir)
		if err != nil {
			t.Fatalf("LoadLokitFile error: %v", err)
		}
		if lf != nil {
			t.Fatalf("LoadLokitFile expected nil, got %#v", lf)
		}
	})

	t.Run("applies defaults and inheritance", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "languages: [ru, de]\n" +
			"targets:\n" +
			"  - name: app\n" +
			"    format: gettext\n" +
			"    dir: po\n" +
			"    pot: messages.pot\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		lf, err := LoadLokitFile(dir)
		if err != nil {
			t.Fatalf("LoadLokitFile error: %v", err)
		}
		if lf.SourceLang != "en" {
			t.Fatalf("SourceLang = %q, want en", lf.SourceLang)
		}
		if len(lf.Targets) != 1 {
			t.Fatalf("expected 1 target, got %d", len(lf.Targets))
		}
		target := lf.Targets[0]
		if target.Root != "." {
			t.Fatalf("target.Root = %q, want .", target.Root)
		}
		if !reflect.DeepEqual(target.Languages, []string{"ru", "de"}) {
			t.Fatalf("target.Languages = %v, want [ru de]", target.Languages)
		}
		if target.SourceLang != "en" {
			t.Fatalf("target.SourceLang = %q, want en", target.SourceLang)
		}
		if target.POT != "messages.pot" {
			t.Fatalf("target.POT = %q, want %q", target.POT, "messages.pot")
		}
	})

	t.Run("rejects deprecated keys", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "targets:\n  - name: app\n    format: gettext\n    po_dir: po\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := LoadLokitFile(dir)
		if err == nil {
			t.Fatal("expected error for deprecated key")
		}
		if !strings.Contains(err.Error(), "unsupported key") {
			t.Fatalf("error %q does not contain unsupported key message", err)
		}
	})

	t.Run("validates required dir", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "targets:\n  - name: app\n    format: gettext\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := LoadLokitFile(dir)
		if err == nil {
			t.Fatal("expected validation error")
		}
		if !strings.Contains(err.Error(), "requires \"dir\"") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("validates required pot for gettext", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "targets:\n  - name: app\n    format: gettext\n    dir: po\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := LoadLokitFile(dir)
		if err == nil {
			t.Fatal("expected validation error")
		}
		if !strings.Contains(err.Error(), "requires \"pot\"") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("validates required config for po4a", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "targets:\n  - name: docs\n    format: po4a\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := LoadLokitFile(dir)
		if err == nil {
			t.Fatal("expected validation error")
		}
		if !strings.Contains(err.Error(), "requires \"config\"") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestLokitFileResolveAutoDetectAndAllLanguages(t *testing.T) {
	dir := t.TempDir()
	poDir := filepath.Join(dir, "po")
	if err := os.MkdirAll(poDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(poDir, "de.po"), []byte(""), 0644); err != nil {
		t.Fatalf("WriteFile de.po: %v", err)
	}
	if err := os.WriteFile(filepath.Join(poDir, "ru.po"), []byte(""), 0644); err != nil {
		t.Fatalf("WriteFile ru.po: %v", err)
	}

	lf := &LokitFile{
		Targets: []Target{{
			Name: "app",
			Type: TargetTypeGettext,
			Root: ".",
			Dir:  "po",
		}},
	}

	resolved, err := lf.Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved target, got %d", len(resolved))
	}
	if !filepath.IsAbs(resolved[0].AbsRoot) {
		t.Fatalf("AbsRoot is not absolute: %q", resolved[0].AbsRoot)
	}
	if !reflect.DeepEqual(resolved[0].Languages, []string{"de", "ru"}) {
		t.Fatalf("resolved languages = %v, want [de ru]", resolved[0].Languages)
	}

	all := lf.AllLanguages(dir)
	if !reflect.DeepEqual(all, []string{"de", "ru"}) {
		t.Fatalf("AllLanguages = %v, want [de ru]", all)
	}
}

func TestLoadLokitFilePatternValidation(t *testing.T) {
	t.Run("defaults markdown pattern to {lang}", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "targets:\n  - name: docs\n    format: markdown\n    dir: docs\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		lf, err := LoadLokitFile(dir)
		if err != nil {
			t.Fatalf("LoadLokitFile error: %v", err)
		}
		if got := lf.Targets[0].Pattern; got != "{lang}" {
			t.Fatalf("target pattern = %q, want {lang}", got)
		}
	})

	t.Run("requires pattern for file-per-language targets", func(t *testing.T) {
		types := []string{TargetTypeI18Next, TargetTypeVueI18n, TargetTypeYAML, TargetTypeProperties, TargetTypeFlutter, TargetTypeJSKV}
		for _, targetType := range types {
			t.Run(targetType, func(t *testing.T) {
				dir := t.TempDir()
				yaml := "targets:\n  - name: app\n    format: " + targetType + "\n    dir: i18n\n"
				if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}

				_, err := LoadLokitFile(dir)
				if err == nil {
					t.Fatal("expected validation error")
				}
				if !strings.Contains(err.Error(), "requires \"pattern\"") {
					t.Fatalf("unexpected error: %v", err)
				}
			})
		}
	})

	t.Run("rejects pattern without lang placeholder", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "targets:\n  - name: app\n    format: i18next\n    dir: i18n\n    pattern: common.json\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		_, err := LoadLokitFile(dir)
		if err == nil {
			t.Fatal("expected validation error")
		}
		if !strings.Contains(err.Error(), "pattern") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("rejects unknown format values", func(t *testing.T) {
		formats := []string{"unsupported-a", "unsupported-b", "unsupported-c", "unsupported-d"}
		for _, format := range formats {
			t.Run(format, func(t *testing.T) {
				dir := t.TempDir()
				yaml := "targets:\n  - name: app\n    format: " + format + "\n    dir: i18n\n    pattern: '{lang}.json'\n"
				if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}

				_, err := LoadLokitFile(dir)
				if err == nil {
					t.Fatal("expected validation error")
				}
				if !strings.Contains(err.Error(), "unknown type") {
					t.Fatalf("unexpected error: %v", err)
				}
			})
		}
	})
}

func TestResolveWithPatternAndHelpers(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "i18n", "en"), 0755); err != nil {
		t.Fatalf("MkdirAll en: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "i18n", "ru"), 0755); err != nil {
		t.Fatalf("MkdirAll ru: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "i18n", "en", "common.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("WriteFile en: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "i18n", "ru", "common.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("WriteFile ru: %v", err)
	}

	lf := &LokitFile{
		SourceLang: "en",
		Targets: []Target{{
			Name:       "app",
			Type:       TargetTypeI18Next,
			Root:       ".",
			Dir:        "i18n",
			Pattern:    "{lang}/common.json",
			SourceLang: "en",
		}},
	}

	resolved, err := lf.Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved target, got %d", len(resolved))
	}
	rt := resolved[0]

	if !reflect.DeepEqual(rt.Languages, []string{"en", "ru"}) {
		t.Fatalf("resolved languages = %v, want [en ru]", rt.Languages)
	}

	wantRu := filepath.Join(dir, "i18n", "ru", "common.json")
	if got := rt.TranslationPath("ru"); got != wantRu {
		t.Fatalf("TranslationPath(ru) = %q, want %q", got, wantRu)
	}

	if got := rt.ExistingSourcePath(); got != filepath.Join(dir, "i18n", "en", "common.json") {
		t.Fatalf("ExistingSourcePath = %q", got)
	}
}

func TestLoadLokitFileProviderAndLocaleValidation(t *testing.T) {
	t.Run("accepts provider object with base_url for ollama", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "provider:\n" +
			"  id: ollama\n" +
			"  model: llama3.2\n" +
			"  base_url: http://localhost:11434\n" +
			"targets:\n" +
			"  - name: app\n" +
			"    format: i18next\n" +
			"    dir: i18n\n" +
			"    pattern: '{lang}.json'\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		lf, err := LoadLokitFile(dir)
		if err != nil {
			t.Fatalf("LoadLokitFile error: %v", err)
		}
		if lf.Provider == nil || lf.Provider.ID != "ollama" {
			t.Fatalf("provider not loaded correctly: %#v", lf.Provider)
		}
	})

	t.Run("rejects provider base_url for openai", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "provider:\n" +
			"  id: openai\n" +
			"  model: gpt-4o\n" +
			"  base_url: https://api.openai.com/v1\n" +
			"targets:\n" +
			"  - name: app\n" +
			"    format: i18next\n" +
			"    dir: i18n\n" +
			"    pattern: '{lang}.json'\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		_, err := LoadLokitFile(dir)
		if err == nil {
			t.Fatal("expected error for base_url with openai provider")
		}
		if !strings.Contains(err.Error(), "base_url") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("rejects provider base_url for unsupported provider", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "provider:\n" +
			"  id: copilot\n" +
			"  model: gpt-4o\n" +
			"  base_url: https://example.com/v1\n" +
			"targets:\n" +
			"  - name: app\n" +
			"    format: i18next\n" +
			"    dir: i18n\n" +
			"    pattern: '{lang}.json'\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		_, err := LoadLokitFile(dir)
		if err == nil {
			t.Fatal("expected validation error")
		}
		if !strings.Contains(err.Error(), "provider.base_url") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("rejects non-canonical locale with hint", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "source_lang: en\n" +
			"languages: [pt-br]\n" +
			"targets:\n" +
			"  - name: app\n" +
			"    format: i18next\n" +
			"    dir: i18n\n" +
			"    pattern: '{lang}.json'\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		_, err := LoadLokitFile(dir)
		if err == nil {
			t.Fatal("expected validation error")
		}
		if !strings.Contains(err.Error(), "try \"pt-BR\"") {
			t.Fatalf("expected hint in error, got: %v", err)
		}
	})

	t.Run("accepts target-local languages without locale validation", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "source_lang: en\n" +
			"languages: [ru]\n" +
			"targets:\n" +
			"  - name: app\n" +
			"    format: gettext\n" +
			"    dir: po\n" +
			"    pot: messages.pot\n" +
			"    languages: [pt_BR, pt-br]\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		lf, err := LoadLokitFile(dir)
		if err != nil {
			t.Fatalf("LoadLokitFile error: %v", err)
		}
		got := lf.Targets[0].Languages
		want := []string{"pt_BR", "pt-br"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("target languages = %v, want %v", got, want)
		}
	})

	t.Run("accepts target-local source_lang without locale validation", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "source_lang: en\n" +
			"languages: [ru]\n" +
			"targets:\n" +
			"  - name: app\n" +
			"    format: gettext\n" +
			"    dir: po\n" +
			"    pot: messages.pot\n" +
			"    source_lang: en_US\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		lf, err := LoadLokitFile(dir)
		if err != nil {
			t.Fatalf("LoadLokitFile error: %v", err)
		}
		if got := lf.Targets[0].SourceLang; got != "en_US" {
			t.Fatalf("target source_lang = %q, want en_US", got)
		}
	})
}

func TestLoadLokitFileRejectsLegacyKeysAndDuplicateNames(t *testing.T) {
	t.Run("rejects legacy path_pattern key", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "targets:\n" +
			"  - name: app\n" +
			"    format: i18next\n" +
			"    dir: i18n\n" +
			"    path_pattern: '{lang}.json'\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := LoadLokitFile(dir)
		if err == nil {
			t.Fatal("expected validation error")
		}
		if !strings.Contains(err.Error(), "unsupported key") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("rejects duplicate target names", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "targets:\n" +
			"  - name: app\n" +
			"    format: i18next\n" +
			"    dir: i18n\n" +
			"    pattern: '{lang}.json'\n" +
			"  - name: app\n" +
			"    format: yaml\n" +
			"    dir: locales\n" +
			"    pattern: '{lang}.yaml'\n"
		if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		_, err := LoadLokitFile(dir)
		if err == nil {
			t.Fatal("expected validation error")
		}
		if !strings.Contains(err.Error(), "duplicate target name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestLoadLokitFileDoesNotAutoLoadNestedConfigs(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "apps", "web")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	nestedYAML := "targets:\n" +
		"  - name: web\n" +
		"    format: i18next\n" +
		"    dir: i18n\n" +
		"    pattern: '{lang}.json'\n"
	if err := os.WriteFile(filepath.Join(nested, LokitFileName), []byte(nestedYAML), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	lf, err := LoadLokitFile(root)
	if err != nil {
		t.Fatalf("LoadLokitFile error: %v", err)
	}
	if lf != nil {
		t.Fatalf("expected nil for root without lokit.yaml, got %#v", lf)
	}
}

func TestLoadLokitFileAcceptsFormatField(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lokit.yaml"), []byte("source_lang: en\ntargets:\n  - name: ui\n    format: i18next\n    dir: i18n\n    pattern: '{lang}.json'\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	lf, err := LoadLokitFile(dir)
	if err != nil {
		t.Fatalf("LoadLokitFile() error = %v", err)
	}
	if len(lf.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(lf.Targets))
	}
	if lf.Targets[0].Type != TargetTypeI18Next {
		t.Fatalf("expected type %q, got %q", TargetTypeI18Next, lf.Targets[0].Type)
	}
}

func TestResolveSurfaces(t *testing.T) {
	dir := t.TempDir()
	yaml := "source_lang: en\nlanguages: [ru]\ntargets:\n  - name: app\n    root: .\n    surfaces:\n      - name: ui\n        format: i18next\n        dir: i18n\n        pattern: '{lang}.json'\n"
	if err := os.WriteFile(filepath.Join(dir, "lokit.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "i18n"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "i18n", "en.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	lf, err := LoadLokitFile(dir)
	if err != nil {
		t.Fatalf("LoadLokitFile() error = %v", err)
	}
	resolved, err := lf.Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved surface, got %d", len(resolved))
	}
	if resolved[0].Target.Name != "app/ui" {
		t.Fatalf("unexpected resolved name: %q", resolved[0].Target.Name)
	}
}

func TestResolveExtractIDExpansion(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "alpha.txt"), []byte("a"), 0644); err != nil {
		t.Fatalf("write alpha: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "beta.txt"), []byte("b"), 0644); err != nil {
		t.Fatalf("write beta: %v", err)
	}

	yaml := "source_lang: en\nlanguages: [ru]\ntargets:\n  - name: matrix\n    format: markdown\n    source: data/{id}.txt\n    target: data/{id}.{lang}.txt\n"
	if err := os.WriteFile(filepath.Join(dir, "lokit.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	lf, err := LoadLokitFile(dir)
	if err != nil {
		t.Fatalf("LoadLokitFile() error = %v", err)
	}
	resolved, err := lf.Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("expected 2 expanded targets, got %d", len(resolved))
	}
}

func TestLoadLokitFileSourceObjectIndexMode(t *testing.T) {
	dir := t.TempDir()
	yaml := "source_lang: en\ntargets:\n  - name: recipes\n    format: vue-i18n\n    root: data\n    dir: recipe-translations\n    pattern: \"{lang}/{id}.json\"\n    languages: [de]\n    source:\n      index: recipes.json\n      records_path: \"$\"\n      key_field: id\n      fields: [name, description]\n"
	if err := os.WriteFile(filepath.Join(dir, "lokit.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	lf, err := LoadLokitFile(dir)
	if err != nil {
		t.Fatalf("LoadLokitFile() error = %v", err)
	}
	if lf == nil || len(lf.Targets) != 1 {
		t.Fatalf("expected one target")
	}
	s := lf.Targets[0].Source
	if s == nil || !s.IsIndex() {
		t.Fatalf("expected source index config")
	}
	if s.RecordsPath != "$" {
		t.Fatalf("unexpected records_path: %q", s.RecordsPath)
	}
}

func TestLoadLokitFileSourceObjectValidation(t *testing.T) {
	dir := t.TempDir()
	yaml := "source_lang: en\ntargets:\n  - name: recipes\n    format: vue-i18n\n    root: data\n    dir: recipe-translations\n    pattern: \"{lang}.json\"\n    source:\n      index: recipes.json\n      fields: [name]\n"
	if err := os.WriteFile(filepath.Join(dir, "lokit.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := LoadLokitFile(dir)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(err.Error(), "key_field") {
		t.Fatalf("expected key_field validation error, got: %v", err)
	}
}

func TestLoadLokitFileSourceObjectRequiresIDPlaceholder(t *testing.T) {
	dir := t.TempDir()
	yaml := "source_lang: en\ntargets:\n  - name: recipes\n    format: vue-i18n\n    root: data\n    dir: recipe-translations\n    pattern: \"{lang}.json\"\n    source:\n      index: recipes.json\n      key_field: id\n      fields: [name]\n"
	if err := os.WriteFile(filepath.Join(dir, "lokit.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := LoadLokitFile(dir)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(err.Error(), "{id}") {
		t.Fatalf("expected {id} validation error, got: %v", err)
	}
}

func TestLoadLokitFileSourceObjectDefaultsRecordsPath(t *testing.T) {
	dir := t.TempDir()
	yaml := "source_lang: en\ntargets:\n  - name: recipes\n    format: vue-i18n\n    root: data\n    dir: recipe-translations\n    pattern: \"{lang}/{id}.json\"\n    source:\n      index: recipes.json\n      key_field: id\n      fields: [name]\n"
	if err := os.WriteFile(filepath.Join(dir, "lokit.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	lf, err := LoadLokitFile(dir)
	if err != nil {
		t.Fatalf("LoadLokitFile() error = %v", err)
	}
	if got := lf.Targets[0].Source.RecordsPath; got != "$" {
		t.Fatalf("expected default records_path '$', got %q", got)
	}
}

func TestResolveIndexSourcePropagatesLoadError(t *testing.T) {
	dir := t.TempDir()
	yaml := "source_lang: en\ntargets:\n  - name: recipes\n    format: vue-i18n\n    root: data\n    dir: recipe-translations\n    pattern: \"{lang}/{id}.json\"\n    source:\n      index: missing.json\n      key_field: id\n      fields: [name]\n"
	if err := os.WriteFile(filepath.Join(dir, "lokit.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	lf, err := LoadLokitFile(dir)
	if err != nil {
		t.Fatalf("LoadLokitFile() error = %v", err)
	}

	_, err = lf.Resolve(dir)
	if err == nil {
		t.Fatalf("expected resolve error")
	}
	if !strings.Contains(err.Error(), "reading source index") {
		t.Fatalf("expected source index error, got: %v", err)
	}
}
