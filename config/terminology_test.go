package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minios-linux/lokit/terminology"
)

func TestLoadLokitFileTerminologyRelativePathAndResolve(t *testing.T) {
	dir := t.TempDir()
	terminologyDir := filepath.Join(dir, "l10n")
	if err := os.MkdirAll(terminologyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	terms := `version: 1
exact:
  - id: save
    source: Save
    translations: {de: Speichern}
`
	if err := os.WriteFile(filepath.Join(terminologyDir, "terminology.yaml"), []byte(terms), 0o644); err != nil {
		t.Fatalf("write terminology: %v", err)
	}
	config := `source_lang: en
languages: [de]
terminology:
  from:
    - l10n/terminology.yaml
targets:
  - name: app
    format: i18next
    dir: i18n
    pattern: "{lang}.json"
`
	if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	lf, err := LoadLokitFile(dir)
	if err != nil {
		t.Fatalf("LoadLokitFile: %v", err)
	}
	if lf.Terminology == nil || lf.Terminology.Len() != 1 {
		t.Fatalf("terminology catalog not loaded: %#v", lf.Terminology)
	}
	match, ok := lf.Terminology.Exact("Save", "", "de", terminology.Selector{})
	if !ok || match.Translations[0] != "Speichern" {
		t.Fatalf("unexpected terminology match: %#v, %v", match, ok)
	}
	resolved, err := lf.Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolved) != 1 || resolved[0].Terminology != lf.Terminology {
		t.Fatalf("resolved target did not carry catalog: %#v", resolved)
	}
}

func TestLoadLokitFileTerminologyConfigValidation(t *testing.T) {
	tests := map[string]string{
		"missing from": `terminology: {}
targets: []
`,
		"empty from": `terminology:
  from: []
targets: []
`,
		"unknown field": `terminology:
  from: [terms.yaml]
  optional: true
targets: []
`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, LokitFileName), []byte(content), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			_, err := LoadLokitFile(dir)
			if err == nil || !strings.Contains(err.Error(), "terminology") {
				t.Fatalf("expected terminology validation error, got %v", err)
			}
		})
	}
}
