package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/minios-linux/lokit/config"
)

func TestIndexJSONFileGetDistinguishesEmptyAndMissingValues(t *testing.T) {
	f := &indexJSONFile{translations: map[string]string{
		"translated": "value",
		"empty":      "",
	}}

	if got, ok := f.Get("translated"); !ok || got != "value" {
		t.Fatalf("Get(translated) = %q, %v", got, ok)
	}
	if got, ok := f.Get("empty"); !ok || got != "" {
		t.Fatalf("Get(empty) = %q, %v", got, ok)
	}
	if got, ok := f.Get("missing"); ok || got != "" {
		t.Fatalf("Get(missing) = %q, %v", got, ok)
	}
}

func TestShowConfigIndexStatsCountsTerminologyViolations(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "records.json"), []byte(`[{"id":"one","title":"Open app"}]`), 0o644); err != nil {
		t.Fatalf("write source index: %v", err)
	}
	targetPath := filepath.Join(dir, "translations", "de", "one.json")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte(`{"title":"Programm öffnen"}`), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	rt := config.ResolvedTarget{
		Target: config.Target{
			Name:       "records/one",
			Type:       config.TargetTypeVueI18n,
			Format:     config.TargetTypeVueI18n,
			Source:     &config.SourceField{Index: "records.json", RecordsPath: "$", KeyField: "id", Fields: []string{"title"}},
			TargetPath: "translations/{lang}/one.json",
			SourceLang: "en",
		},
		AbsRoot: dir,
		Terminology: testTerminologyCatalog(t, `version: 1
terms:
  - id: app
    source: app
    translations:
      de: Anwendung
`),
	}
	output := captureStderr(t, func() {
		showConfigIndexStats(rt, []string{"de"})
	})

	if !regexp.MustCompile(`(?m)^\s*🇩🇪 de\s+.*\s0%\s+1\s+1\s+0\s*$`).MatchString(output) {
		t.Fatalf("indexed status did not subtract terminology violations from progress:\n%s", output)
	}
}
