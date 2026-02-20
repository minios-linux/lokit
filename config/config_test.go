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
			"    type: gettext\n" +
			"    dir: po\n"
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
		if target.POTFile != filepath.Join("po", "messages.pot") {
			t.Fatalf("target.POTFile = %q, want %q", target.POTFile, filepath.Join("po", "messages.pot"))
		}
	})

	t.Run("rejects deprecated keys", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "targets:\n  - name: app\n    type: gettext\n    po_dir: po\n"
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
		yaml := "targets:\n  - name: app\n    type: gettext\n"
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
