package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/minios-linux/lokit/config"
	"github.com/minios-linux/lokit/lockfile"
	"github.com/minios-linux/lokit/translate"
)

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
	if !regexp.MustCompile(`(?m)^\s*🇩🇪 de\s+.*\s+1\s+0\s*$`).MatchString(output) {
		t.Fatalf("JSKV stats missing translated count:\n%s", output)
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
