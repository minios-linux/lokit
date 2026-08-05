package cli

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/minios-linux/lokit/config"
)

func TestFilterResolvedTargetsByNames(t *testing.T) {
	resolved := []config.ResolvedTarget{
		{Target: config.Target{Name: "app"}},
		{Target: config.Target{Name: "matrix/a"}},
		{Target: config.Target{Name: "matrix/b"}},
		{Target: config.Target{Name: "docs"}},
	}

	t.Run("empty selection returns all", func(t *testing.T) {
		filtered, err := filterResolvedTargetsByNames(resolved, nil)
		if err != nil {
			t.Fatalf("filterResolvedTargetsByNames error: %v", err)
		}
		if len(filtered) != len(resolved) {
			t.Fatalf("len(filtered) = %d, want %d", len(filtered), len(resolved))
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
