package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/minios-linux/lokit/config"
	po "github.com/minios-linux/lokit/internal/format/po"
)

func TestFilterResolvedTargetsByNames(t *testing.T) {
	includeByDefault := false
	disabled := false
	resolved := []config.ResolvedTarget{
		{Target: config.Target{Name: "app"}},
		{Target: config.Target{Name: "matrix/a"}},
		{Target: config.Target{Name: "matrix/b"}},
		{Target: config.Target{Name: "docs", IncludeByDefault: &includeByDefault}},
		{Target: config.Target{Name: "disabled", Enabled: &disabled}},
	}

	t.Run("empty selection omits non-default targets", func(t *testing.T) {
		filtered, err := filterResolvedTargetsByNames(resolved, nil)
		if err != nil {
			t.Fatalf("filterResolvedTargetsByNames error: %v", err)
		}
		if len(filtered) != len(resolved)-2 {
			t.Fatalf("len(filtered) = %d, want %d", len(filtered), len(resolved)-2)
		}
	})

	t.Run("explicit selection rejects disabled target", func(t *testing.T) {
		if _, err := filterResolvedTargetsByNames(resolved, []string{"disabled"}); err == nil {
			t.Fatal("expected disabled target error")
		}
	})

	t.Run("explicit selection includes non-default target", func(t *testing.T) {
		filtered, err := filterResolvedTargetsByNames(resolved, []string{"docs"})
		if err != nil {
			t.Fatalf("filterResolvedTargetsByNames error: %v", err)
		}
		if len(filtered) != 1 || filtered[0].Target.Name != "docs" {
			t.Fatalf("unexpected explicit targets: %#v", filtered)
		}
	})

	t.Run("supports exact and prefix targets with dedupe", func(t *testing.T) {
		filtered, err := filterResolvedTargetsByNames(resolved, []string{"app", "matrix", "matrix/a"})
		if err != nil {
			t.Fatalf("filterResolvedTargetsByNames error: %v", err)
		}
		if len(filtered) != 3 {
			t.Fatalf("len(filtered) = %d, want 3", len(filtered))
		}
		if filtered[0].Target.Name != "app" || filtered[1].Target.Name != "matrix/a" || filtered[2].Target.Name != "matrix/b" {
			t.Fatalf("unexpected target order: %q, %q, %q", filtered[0].Target.Name, filtered[1].Target.Name, filtered[2].Target.Name)
		}
	})

	t.Run("returns error for unknown target", func(t *testing.T) {
		if _, err := filterResolvedTargetsByNames(resolved, []string{"missing"}); err == nil {
			t.Fatal("expected error for unknown target")
		}
	})
}

func TestGettextPackageMetadataFromDebianChangelog(t *testing.T) {
	root := t.TempDir()
	debian := filepath.Join(root, "debian")
	if err := os.Mkdir(debian, 0o755); err != nil {
		t.Fatal(err)
	}
	changelog := "example-app (1.2.3-1) unstable; urgency=medium\n"
	if err := os.WriteFile(filepath.Join(debian, "changelog"), []byte(changelog), 0o644); err != nil {
		t.Fatal(err)
	}

	name, version := gettextPackageMetadata(root, "fallback")
	if name != "example-app" || version != "1.2.3-1" {
		t.Fatalf("metadata = %q, %q", name, version)
	}

	name, version = gettextPackageMetadata(filepath.Join(root, "docs", "manpages"), "fallback")
	if name != "example-app" || version != "1.2.3-1" {
		t.Fatalf("nested metadata = %q, %q", name, version)
	}

	name, version = gettextPackageMetadata(t.TempDir(), "fallback")
	if name != "fallback" || version != "" {
		t.Fatalf("fallback metadata = %q, %q", name, version)
	}

	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "debian"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "debian", "changelog"), []byte(changelog), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(parent, "nested-repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	name, version = gettextPackageMetadata(repo, "nested")
	if name != "nested" || version != "" {
		t.Fatalf("cross-repository metadata = %q, %q", name, version)
	}
}

func TestApplyPOTProjectID(t *testing.T) {
	potFile := po.NewFile()
	potFile.SetHeaderField("Project-Id-Version", "example-app 1.2.3")
	poFile := po.NewFile()
	poFile.SetHeaderField("Project-Id-Version", "old 1.0")

	applyPOTProjectID(poFile, potFile)
	if got := poFile.HeaderField("Project-Id-Version"); got != "example-app 1.2.3" {
		t.Fatalf("Project-Id-Version = %q", got)
	}

	potFile.SetHeaderField("Project-Id-Version", "PACKAGE VERSION")
	applyPOTProjectID(poFile, potFile)
	if got := poFile.HeaderField("Project-Id-Version"); got != "example-app 1.2.3" {
		t.Fatalf("placeholder replaced Project-Id-Version: %q", got)
	}

	potFile.SetHeaderField("Project-Id-Version", "Main application")
	applyPOTProjectID(poFile, potFile)
	if got := poFile.HeaderField("Project-Id-Version"); got != "example-app 1.2.3" {
		t.Fatalf("unversioned name replaced Project-Id-Version: %q", got)
	}
}

func TestPo4aCommandArgsUsesDebianMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "debian"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "debian", "changelog"), []byte("example-app (2.0) unstable; urgency=medium\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "docs", "po4a.cfg")
	want := []string{"--no-translations", "--package-name", "example-app", "--package-version", "2.0", cfgPath}
	if got := po4aCommandArgs(cfgPath, "fallback", "--no-translations"); !reflect.DeepEqual(got, want) {
		t.Fatalf("po4a args = %#v, want %#v", got, want)
	}
}

func TestResolveTargetsForSelectionSkipsInactiveSources(t *testing.T) {
	dir := t.TempDir()
	configData := []byte(`source_lang: en
languages: [de]
targets:
  - name: default
    format: i18next
    from: [default.en.json]
    to: default.{lang}.json
  - name: manual
    include_by_default: false
    format: vue-i18n
    from:
      index: missing-manual.json
      records: "$"
      key: id
      fields: [name]
    to: manual/{lang}/{id}.json
  - name: disabled
    enabled: false
    format: vue-i18n
    from:
      index: missing-disabled.json
      records: "$"
      key: id
      fields: [name]
    to: disabled/{lang}/{id}.json
`)
	if err := os.WriteFile(filepath.Join(dir, "lokit.yaml"), configData, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.en.json"), []byte(`{"key":"value"}`), 0o644); err != nil {
		t.Fatalf("write default source: %v", err)
	}
	lf, err := config.LoadLokitFile(dir)
	if err != nil {
		t.Fatalf("LoadLokitFile: %v", err)
	}
	resolved, err := resolveTargetsForSelection(lf, dir, nil)
	if err != nil {
		t.Fatalf("default selection resolved inactive source: %v", err)
	}
	if len(resolved) != 1 || resolved[0].Target.Name != "default" {
		t.Fatalf("default resolved targets = %#v", resolved)
	}
	if _, err := resolveTargetsForSelection(lf, dir, []string{"manual"}); err == nil {
		t.Fatal("explicit manual target did not resolve its missing source")
	}
	if _, err := resolveTargetsForSelection(lf, dir, []string{"disabled"}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled target error = %v", err)
	}
}

func TestFilterSourcePaths(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "project")
	files := []string{
		filepath.Join(root, "cli", "bin", "minios-image-compose"),
		filepath.Join(root, "cli", "helper.py"),
		filepath.Join(root, "src", "main.go"),
		filepath.Join(root, "src", "nested", "main.go"),
		filepath.Join(string(filepath.Separator), "external", "input.py"),
	}

	got := filterSourcePaths(files, root, []string{"cli/**/*", "src/*.go"})
	want := []string{
		filepath.Join(root, "src", "nested", "main.go"),
		filepath.Join(string(filepath.Separator), "external", "input.py"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterSourcePaths() = %q, want %q", got, want)
	}

	if unchanged := filterSourcePaths(files, root, nil); !reflect.DeepEqual(unchanged, files) {
		t.Fatalf("empty exclusions changed files: %q", unchanged)
	}
}
