package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadIndexItemsForTargetRootArray(t *testing.T) {
	dir := t.TempDir()
	data := `[
  {"id":"a","name":"Alpha","description":"First"},
  {"id":"b","name":"Beta"},
  {"id":"","name":"Nope"}
]`
	if err := os.WriteFile(filepath.Join(dir, "recipes.json"), []byte(data), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	target := Target{
		Name:       "recipes",
		SourceLang: "en",
		Pattern:    "{lang}/{id}.json",
		Source: &SourceField{
			Index:       "recipes.json",
			RecordsPath: "$",
			KeyField:    "id",
			Fields:      []string{"name", "description"},
		},
	}

	items, err := LoadIndexItemsForTarget(target, dir)
	if err != nil {
		t.Fatalf("LoadIndexItemsForTarget() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].ID != "a" || items[1].ID != "b" {
		t.Fatalf("unexpected ids: %+v", items)
	}
	if got := items[1].Fields["description"]; got != "" {
		t.Fatalf("expected missing field to be absent/empty, got %q", got)
	}
}

func TestLoadIndexItemsForTargetNestedArray(t *testing.T) {
	dir := t.TempDir()
	data := `{"recipes":[{"slug":"x","title":"Title X"}]}`
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(data), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	target := Target{
		Source: &SourceField{
			Index:       "index.json",
			RecordsPath: "$.recipes",
			KeyField:    "slug",
			Fields:      []string{"title"},
		},
	}

	items, err := LoadIndexItemsForTarget(target, dir)
	if err != nil {
		t.Fatalf("LoadIndexItemsForTarget() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != "x" {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestSplitExpandedTargetName(t *testing.T) {
	base, id, ok := SplitExpandedTargetName("recipes/firefox-esr")
	if !ok || base != "recipes" || id != "firefox-esr" {
		t.Fatalf("unexpected split: ok=%v base=%q id=%q", ok, base, id)
	}
}

func TestLoadIndexItemsForTargetRejectsUnsafeID(t *testing.T) {
	dir := t.TempDir()
	data := `[{"id":"../escape","name":"Bad"}]`
	if err := os.WriteFile(filepath.Join(dir, "recipes.json"), []byte(data), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	target := Target{
		Name:       "recipes",
		SourceLang: "en",
		Pattern:    "{lang}/{id}.json",
		Source: &SourceField{
			Index:       "recipes.json",
			RecordsPath: "$",
			KeyField:    "id",
			Fields:      []string{"name"},
		},
	}

	_, err := LoadIndexItemsForTarget(target, dir)
	if err == nil {
		t.Fatalf("expected validation error")
	}
}
