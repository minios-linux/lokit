package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/minios-linux/lokit/config"
	mdfile "github.com/minios-linux/lokit/internal/format/markdown"
	"github.com/minios-linux/lokit/lockfile"
	"github.com/minios-linux/lokit/terminology"
	"github.com/minios-linux/lokit/translate"
)

func TestSyncMarkdownKeysMigratesShiftedChecksums(t *testing.T) {
	oldSource, _ := mdfile.Parse([]byte("# Title\n\nIntro.\n\n## Section A\n\nText A.\n\n## Section B\n\nText B.\n"))
	newSource, _ := mdfile.Parse([]byte("# Title\n\nIntro.\n\n## New\n\nNew text.\n\n## Section A\n\nText A.\n\n## Section B\n\nText B.\n"))
	target, _ := mdfile.Parse([]byte("# Titel\n\nEinleitung.\n\n## Abschnitt A\n\nText A übersetzt.\n\n## Abschnitt B\n\nText B übersetzt.\n"))
	lockTarget := lockfile.LockTargetKey("docs", "de")
	lf := &lockfile.LockFile{Version: lockfile.Version, Checksums: map[string]map[string]string{lockTarget: {}}}
	for key, value := range oldSource.SourceValues() {
		lockKey := markdownLockKey("guide.md", key)
		lf.Checksums[lockTarget][lockKey] = lockfile.Hash(lockfile.KVEntryContent(lockKey, value))
	}

	if !syncMarkdownKeys(newSource, target, lf, "docs", "de", "guide.md", true) {
		t.Fatal("expected lock migration")
	}
	if value, _ := target.Get("sec:1"); value != "" {
		t.Fatalf("new section should be untranslated, got %q", value)
	}
	if value, _ := target.Get("sec:2"); !strings.Contains(value, "Abschnitt A") {
		t.Fatalf("Section A translation was not moved: %q", value)
	}
	if lf.Has(lockTarget, "guide.md:sec:1") {
		t.Fatal("new section inherited an old lock checksum")
	}
	for _, key := range []string{"sec:2", "sec:3"} {
		lockKey := markdownLockKey("guide.md", key)
		value, _ := newSource.Get(key)
		if lf.IsChanged(lockTarget, lockKey, lockfile.KVEntryContent(lockKey, value)) {
			t.Fatalf("migrated checksum for %s is stale", key)
		}
	}
}

func TestShowConfigJSKVStatsParsesJavaScriptFiles(t *testing.T) {
	dir := t.TempDir()
	translationsDir := filepath.Join(dir, "translations")
	if err := os.MkdirAll(translationsDir, 0o755); err != nil {
		t.Fatalf("mkdir translations: %v", err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "en.js"), []byte("window.translations = {\n    \"Hello\": \"Hello\"\n};\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "de.js"), []byte("window.translations = {\n    \"Hello\": \"Hallo\"\n};\n"), 0o644); err != nil {
		t.Fatalf("write translation: %v", err)
	}

	rt := config.ResolvedTarget{
		Target: config.Target{
			Name:       "welcome",
			Type:       config.TargetTypeJSKV,
			Format:     config.TargetTypeJSKV,
			Dir:        "translations",
			Pattern:    "{lang}.js",
			SourceLang: "en",
		},
		AbsRoot: dir,
	}

	output := captureStderr(t, func() {
		showConfigJSKVStats(rt, []string{"de"})
	})

	if strings.Contains(output, "missing") {
		t.Fatalf("JSKV stats reported existing translation as missing:\n%s", output)
	}
	if !regexp.MustCompile(`(?m)^\s*🇩🇪 de\s+.*\s+1\s+0\s+0\s*$`).MatchString(output) {
		t.Fatalf("JSKV stats missing translated count:\n%s", output)
	}
}

func TestShowConfigJSKVStatsCountsTerminologyViolations(t *testing.T) {
	dir := t.TempDir()
	translationsDir := filepath.Join(dir, "translations")
	if err := os.MkdirAll(translationsDir, 0o755); err != nil {
		t.Fatalf("mkdir translations: %v", err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "en.js"), []byte("window.translations = {\n    \"Open app\": \"Open app\"\n};\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "de.js"), []byte("window.translations = {\n    \"Open app\": \"Programm öffnen\"\n};\n"), 0o644); err != nil {
		t.Fatalf("write translation: %v", err)
	}

	rt := testJSKVResolvedTarget(dir)
	rt.Terminology = testTerminologyCatalog(t, `version: 1
terms:
  - id: app
    source: app
    translations:
      de: Anwendung
`)
	output := captureStderr(t, func() {
		showConfigJSKVStats(rt, []string{"de"})
	})

	if !strings.Contains(output, "Terms") {
		t.Fatalf("JSKV stats missing Terms column:\n%s", output)
	}
	if !regexp.MustCompile(`(?m)^\s*🇩🇪 de\s+.*\s0%\s+1\s+1\s+0\s*$`).MatchString(output) {
		t.Fatalf("JSKV stats did not subtract terminology violations from progress:\n%s", output)
	}
}

func TestShowConfigMarkdownStatsUsesRelativeSourcePathForTerminology(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "docs", "guide.md")
	targetPath := filepath.Join(dir, "locales", "de", "docs", "guide.md")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("# Open app\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("# Programm öffnen\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	rt := config.ResolvedTarget{
		Target: config.Target{
			Name:       "docs",
			Type:       config.TargetTypeMarkdown,
			Format:     config.TargetTypeMarkdown,
			Sources:    []string{"docs/**/*.md"},
			TargetPath: "locales/{lang}/{path}",
			SourceLang: "en",
		},
		AbsRoot: dir,
		Terminology: testTerminologyCatalog(t, `version: 1
terms:
  - id: docs-app
    source: app
    when:
      path: docs/guide.md
    translations:
      de: Anwendung
`),
	}
	output := captureStderr(t, func() {
		showConfigMarkdownStats(rt, []string{"de"}, nil)
	})

	if !regexp.MustCompile(`(?m)^\s*🇩🇪 de\s+.*\s0%\s+1\s+1\s+0\s*$`).MatchString(output) {
		t.Fatalf("Markdown stats did not apply path-scoped terminology:\n%s", output)
	}
}

func TestShowConfigAndroidStatsCountsFlattenedTerminologyViolations(t *testing.T) {
	dir := t.TempDir()
	resDir := filepath.Join(dir, "res")
	sourcePath := filepath.Join(resDir, "values", "strings.xml")
	targetPath := filepath.Join(resDir, "values-de", "strings.xml")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte(`<resources>
  <string-array name="actions">
    <item>Open app</item>
    <item>Close app</item>
  </string-array>
</resources>
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte(`<resources>
  <string-array name="actions">
    <item>Programm öffnen</item>
    <item>Anwendung schließen</item>
  </string-array>
</resources>
`), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	rt := config.ResolvedTarget{
		Target: config.Target{
			Name:       "android",
			Type:       config.TargetTypeAndroid,
			Format:     config.TargetTypeAndroid,
			TargetPath: "res",
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
		showConfigAndroidStats(rt, []string{"de"})
	})

	if !regexp.MustCompile(`(?m)^\s*🇩🇪 de\s+.*\s50%\s+2\s+1\s+0\s*$`).MatchString(output) {
		t.Fatalf("Android status did not count flattened terminology violations:\n%s", output)
	}
}

func TestShowConfigJSKVStatsReportsSourceParseError(t *testing.T) {
	dir := t.TempDir()
	translationsDir := filepath.Join(dir, "translations")
	if err := os.MkdirAll(translationsDir, 0o755); err != nil {
		t.Fatalf("mkdir translations: %v", err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "en.js"), []byte("not valid js-kv"), 0o644); err != nil {
		t.Fatalf("write invalid source: %v", err)
	}

	output := captureStderr(t, func() {
		showConfigJSKVStats(testJSKVResolvedTarget(dir), []string{"de"})
	})

	if !strings.Contains(output, "parse error") {
		t.Fatalf("JSKV stats did not report source parse error:\n%s", output)
	}
	if strings.Contains(output, "not found") {
		t.Fatalf("JSKV stats reported invalid existing source as not found:\n%s", output)
	}
}

func TestShowConfigJSKVStatsReportsParseError(t *testing.T) {
	dir := t.TempDir()
	translationsDir := filepath.Join(dir, "translations")
	if err := os.MkdirAll(translationsDir, 0o755); err != nil {
		t.Fatalf("mkdir translations: %v", err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "en.js"), []byte("window.translations = {\n    \"Hello\": \"Hello\"\n};\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "de.js"), []byte("not valid js-kv"), 0o644); err != nil {
		t.Fatalf("write invalid translation: %v", err)
	}

	rt := testJSKVResolvedTarget(dir)
	output := captureStderr(t, func() {
		showConfigJSKVStats(rt, []string{"de"})
	})

	if !strings.Contains(output, "parse error") {
		t.Fatalf("JSKV stats did not report parse error:\n%s", output)
	}
	if strings.Contains(output, "missing") {
		t.Fatalf("JSKV stats reported invalid existing file as missing:\n%s", output)
	}
}

func TestTranslateJSKVTargetDryRunDoesNotCreateFilesOrLockEntries(t *testing.T) {
	dir := t.TempDir()
	translationsDir := filepath.Join(dir, "translations")
	if err := os.MkdirAll(translationsDir, 0o755); err != nil {
		t.Fatalf("mkdir translations: %v", err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "en.js"), []byte("window.translations = {\n    \"Hello\": \"Hello\"\n};\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	rt := testJSKVResolvedTarget(dir)
	lf := &lockfile.LockFile{Version: lockfile.Version, Checksums: map[string]map[string]string{}}

	output := captureStderr(t, func() {
		if err := translateJSKVTarget(context.Background(), rt, translate.Provider{}, translateArgs{dryRun: true, lockFile: lf}, []string{"de"}); err != nil {
			t.Fatalf("translateJSKVTarget dry-run error: %v", err)
		}
	})

	if _, err := os.Stat(filepath.Join(translationsDir, "de.js")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created translation file, stat err=%v", err)
	}
	if got := lf.TargetKeyCount(lockfile.LockTargetKey("welcome", "de")); got != 0 {
		t.Fatalf("dry-run added %d lock keys, want 0", got)
	}
	if !strings.Contains(output, "1 strings to translate") {
		t.Fatalf("dry-run output missing count:\n%s", output)
	}
	if !strings.Contains(output, "file will be auto-created") {
		t.Fatalf("dry-run output missing auto-create hint:\n%s", output)
	}
}

func TestTranslateJSKVTargetDryRunCountsMissingSourceKeys(t *testing.T) {
	dir := t.TempDir()
	translationsDir := filepath.Join(dir, "translations")
	if err := os.MkdirAll(translationsDir, 0o755); err != nil {
		t.Fatalf("mkdir translations: %v", err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "en.js"), []byte("window.translations = {\n    \"Hello\": \"Hello\",\n    \"World\": \"World\"\n};\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "de.js"), []byte("window.translations = {\n    \"Hello\": \"Hallo\"\n};\n"), 0o644); err != nil {
		t.Fatalf("write translation: %v", err)
	}

	rt := testJSKVResolvedTarget(dir)
	lf := &lockfile.LockFile{Version: lockfile.Version, Checksums: map[string]map[string]string{}}

	output := captureStderr(t, func() {
		if err := translateJSKVTarget(context.Background(), rt, translate.Provider{}, translateArgs{dryRun: true, lockFile: lf}, []string{"de"}); err != nil {
			t.Fatalf("translateJSKVTarget dry-run error: %v", err)
		}
	})

	if !strings.Contains(output, "1 strings to translate") {
		t.Fatalf("dry-run output missing stale-key count:\n%s", output)
	}
	if got := lf.TargetKeyCount(lockfile.LockTargetKey("welcome", "de")); got != 0 {
		t.Fatalf("dry-run added %d lock keys, want 0", got)
	}

	for _, tc := range []struct {
		name string
		args translateArgs
	}{
		{name: "retranslate", args: translateArgs{dryRun: true, retranslate: true, lockFile: lf}},
		{name: "force", args: translateArgs{dryRun: true, force: true, lockFile: lf}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := captureStderr(t, func() {
				if err := translateJSKVTarget(context.Background(), rt, translate.Provider{}, tc.args, []string{"de"}); err != nil {
					t.Fatalf("translateJSKVTarget dry-run error: %v", err)
				}
			})
			if !strings.Contains(output, "2 strings to translate") {
				t.Fatalf("dry-run output missing full source count:\n%s", output)
			}
		})
	}
}

func TestTranslateI18NextTargetDryRunCountsSynchronizedSourceKeys(t *testing.T) {
	dir := t.TempDir()
	translationsDir := filepath.Join(dir, "translations")
	if err := os.MkdirAll(translationsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "en.json"), []byte(`{"translations":{"existing":"Existing","new":"New"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "de.json"), []byte(`{"translations":{"existing":"Vorhanden"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := config.ResolvedTarget{
		Target: config.Target{
			Name: "web", Type: config.TargetTypeI18Next, Format: config.TargetTypeI18Next,
			Dir: "translations", Pattern: "{lang}.json", SourceLang: "en",
		},
		AbsRoot: dir,
	}
	for _, tc := range []struct {
		name string
		args translateArgs
		want string
	}{
		{name: "missing", args: translateArgs{dryRun: true}, want: "1 strings to translate"},
		{name: "retranslate", args: translateArgs{dryRun: true, retranslate: true}, want: "2 strings to translate"},
		{name: "force", args: translateArgs{dryRun: true, force: true}, want: "2 strings to translate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := captureStderr(t, func() {
				if err := translateI18NextTarget(context.Background(), rt, translate.Provider{}, tc.args, []string{"de"}); err != nil {
					t.Fatal(err)
				}
			})
			if !strings.Contains(output, tc.want) {
				t.Fatalf("dry-run output missing %q:\n%s", tc.want, output)
			}
		})
	}
}

func TestTranslateDesktopTargetDryRunDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.desktop")
	original := []byte("[Desktop Entry]\nName=App\nName[de]=Anwendung\nName[de]=Anwendung\nComment=Description\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("write desktop file: %v", err)
	}
	lf := &lockfile.LockFile{Version: lockfile.Version, Checksums: map[string]map[string]string{}}

	output := captureStderr(t, func() {
		if err := translateDesktopTarget(context.Background(), testDesktopResolvedTarget(dir), translate.Provider{}, translateArgs{dryRun: true, lockFile: lf}, []string{"de"}); err != nil {
			t.Fatalf("translateDesktopTarget dry-run error: %v", err)
		}
	})
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read desktop file: %v", err)
	}
	if string(current) != string(original) {
		t.Fatalf("dry-run mutated desktop file:\n%s", current)
	}
	if len(lf.Checksums) != 0 {
		t.Fatalf("dry-run mutated lock file: %#v", lf.Checksums)
	}
	if !strings.Contains(output, "1 strings to translate") {
		t.Fatalf("dry-run output missing count:\n%s", output)
	}
}

func TestTranslateDesktopTargetNormalizesLocaleAndDuplicates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.desktop")
	if err := os.WriteFile(path, []byte("[Desktop Entry]\nName=App\nName[pt_BR]=Aplicativo antigo\nName[pt_BR]=Aplicativo\nComment=Description\nComment[pt_BR]=Descrição\n"), 0o644); err != nil {
		t.Fatalf("write desktop file: %v", err)
	}

	if err := translateDesktopTarget(context.Background(), testDesktopResolvedTarget(dir), translate.Provider{}, translateArgs{}, []string{"pt-BR"}); err != nil {
		t.Fatalf("translateDesktopTarget error: %v", err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read desktop file: %v", err)
	}
	if count := strings.Count(string(current), "Name[pt_BR]="); count != 1 {
		t.Fatalf("Name[pt_BR] count = %d:\n%s", count, current)
	}
	if strings.Contains(string(current), "[pt-BR]") {
		t.Fatalf("desktop file contains a hyphenated locale:\n%s", current)
	}
}

func TestTranslateCommandDryRunSkipsProviderAndLockWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.desktop")
	original := []byte("[Desktop Entry]\nName=App\nName[de]=Anwendung\nComment=Description\nComment[de]=Beschreibung\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("write desktop file: %v", err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	oldRoot := rootDir
	rootDir = dir
	defer func() { rootDir = oldRoot }()
	lf := &config.LokitFile{
		Languages:  []string{"de"},
		SourceLang: "en",
		Targets: []config.Target{{
			Name:   "desktop",
			Format: config.TargetTypeDesktop,
			To:     "app.desktop",
		}},
	}
	runTranslateWithConfig(lf, translateArgs{provider: "ollama", model: "test", baseURL: server.URL, dryRun: true, targets: []string{"desktop"}})

	if requests != 0 {
		t.Fatalf("dry-run made %d provider requests", requests)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read desktop file: %v", err)
	}
	if string(current) != string(original) {
		t.Fatalf("dry-run mutated desktop file:\n%s", current)
	}
	if _, err := os.Stat(filepath.Join(dir, "lokit.lock")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created lock file, stat err=%v", err)
	}
}

func TestTranslatePolkitTargetDryRunDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.xml")
	original := []byte("<policyconfig><action id=\"org.test\"><description>Allow action</description><message>Authenticate</message></action></policyconfig>\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("write policy file: %v", err)
	}
	rt := config.ResolvedTarget{
		Target: config.Target{
			Name:       "policy",
			Type:       config.TargetTypePolkit,
			Format:     config.TargetTypePolkit,
			Dir:        ".",
			Pattern:    "policy.xml",
			SourceLang: "en",
		},
		AbsRoot: dir,
	}
	output := captureStderr(t, func() {
		if err := translatePolkitTarget(context.Background(), rt, translate.Provider{}, translateArgs{dryRun: true}, []string{"de"}); err != nil {
			t.Fatalf("translatePolkitTarget dry-run error: %v", err)
		}
	})
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read policy file: %v", err)
	}
	if string(current) != string(original) {
		t.Fatalf("dry-run mutated policy file:\n%s", current)
	}
	if !strings.Contains(output, "2 strings to translate") {
		t.Fatalf("dry-run output missing count:\n%s", output)
	}
}

func TestRunInitJSKVSyncsExistingFile(t *testing.T) {
	dir := t.TempDir()
	translationsDir := filepath.Join(dir, "translations")
	if err := os.MkdirAll(translationsDir, 0o755); err != nil {
		t.Fatalf("mkdir translations: %v", err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "en.js"), []byte("window.i18n = {\n    \"Hello\": \"Hello\",\n    \"World\": \"World\"\n};\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "de.js"), []byte("window.translations = {\n    \"Hello\": \"Hallo\",\n    \"Obsolete\": \"Alt\"\n};\n"), 0o644); err != nil {
		t.Fatalf("write translation: %v", err)
	}

	runInitJSKV(testJSKVResolvedTarget(dir), []string{"de"})

	updated, err := os.ReadFile(filepath.Join(translationsDir, "de.js"))
	if err != nil {
		t.Fatalf("read updated translation: %v", err)
	}
	text := string(updated)
	if !strings.Contains(text, "window.i18n = {") {
		t.Fatalf("prefix was not synced:\n%s", text)
	}
	if !strings.Contains(text, `"Hello": "Hallo"`) {
		t.Fatalf("existing translation was not preserved:\n%s", text)
	}
	if !strings.Contains(text, `"World": ""`) {
		t.Fatalf("missing source key was not added empty:\n%s", text)
	}
	if strings.Contains(text, "Obsolete") {
		t.Fatalf("obsolete key was not removed:\n%s", text)
	}
}

func TestRunInitJSKVCreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	translationsDir := filepath.Join(dir, "translations")
	if err := os.MkdirAll(translationsDir, 0o755); err != nil {
		t.Fatalf("mkdir translations: %v", err)
	}
	if err := os.WriteFile(filepath.Join(translationsDir, "en.js"), []byte("window.translations = {\n    \"Hello\": \"Hello\"\n};\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	runInitJSKV(testJSKVResolvedTarget(dir), []string{"de"})

	created, err := os.ReadFile(filepath.Join(translationsDir, "de.js"))
	if err != nil {
		t.Fatalf("read created translation: %v", err)
	}
	if !strings.Contains(string(created), `"Hello": ""`) {
		t.Fatalf("created translation does not contain empty source key:\n%s", string(created))
	}
}

func testJSKVResolvedTarget(dir string) config.ResolvedTarget {
	return config.ResolvedTarget{
		Target: config.Target{
			Name:       "welcome",
			Type:       config.TargetTypeJSKV,
			Format:     config.TargetTypeJSKV,
			Dir:        "translations",
			Pattern:    "{lang}.js",
			SourceLang: "en",
		},
		AbsRoot: dir,
	}
}

func testDesktopResolvedTarget(dir string) config.ResolvedTarget {
	return config.ResolvedTarget{
		Target: config.Target{
			Name:       "desktop",
			Type:       config.TargetTypeDesktop,
			Format:     config.TargetTypeDesktop,
			Dir:        ".",
			Pattern:    "app.desktop",
			SourceLang: "en",
		},
		AbsRoot: dir,
	}
}

func testTerminologyCatalog(t *testing.T, content string) *terminology.Catalog {
	t.Helper()
	path := filepath.Join(t.TempDir(), "terminology.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write terminology: %v", err)
	}
	catalog, err := terminology.Load(path)
	if err != nil {
		t.Fatalf("load terminology: %v", err)
	}
	return catalog
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stderr = w

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}
	os.Stderr = old

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stderr reader: %v", err)
	}
	return string(out)
}
