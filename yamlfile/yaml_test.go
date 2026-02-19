// Package yamlfile tests.
package yamlfile

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Parse / Keys / Stats
// ---------------------------------------------------------------------------

func TestParse_Flat(t *testing.T) {
	data := []byte(`greeting: Hello
farewell: Goodbye
`)
	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(f.entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(f.entries))
	}
	assertEntry(t, f, "greeting", "Hello")
	assertEntry(t, f, "farewell", "Goodbye")
}

func TestParse_Nested(t *testing.T) {
	data := []byte(`nav:
  home: Home
  about: About
footer:
  copyright: Copyright
`)
	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(f.entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(f.entries))
	}
	assertEntry(t, f, "nav.home", "Home")
	assertEntry(t, f, "nav.about", "About")
	assertEntry(t, f, "footer.copyright", "Copyright")
}

func TestParse_RailsStyle(t *testing.T) {
	data := []byte(`en:
  greeting: Hello
  nav:
    home: Home
`)
	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if f.rootLocaleKey != "en" {
		t.Fatalf("expected rootLocaleKey=en, got %q", f.rootLocaleKey)
	}
	assertEntry(t, f, "greeting", "Hello")
	assertEntry(t, f, "nav.home", "Home")
}

func TestParse_SkipsNonStringScalars(t *testing.T) {
	data := []byte(`count: 42
enabled: true
ratio: 3.14
nothing: ~
label: Hello
`)
	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	// Only "label" should be translatable.
	if len(f.entries) != 1 {
		t.Fatalf("expected 1 translatable entry, got %d: %v", len(f.entries), f.Keys())
	}
	assertEntry(t, f, "label", "Hello")
}

func TestParse_EmptyFile(t *testing.T) {
	f, err := Parse([]byte(""))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(f.entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(f.entries))
	}
}

func TestStats(t *testing.T) {
	data := []byte(`a: Hello
b: ""
c: World
`)
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	total, translated, pct := f.Stats()
	if total != 3 {
		t.Errorf("total: want 3, got %d", total)
	}
	if translated != 2 {
		t.Errorf("translated: want 2, got %d", translated)
	}
	if pct < 66 || pct > 67 {
		t.Errorf("pct: want ~66.6, got %f", pct)
	}
}

func TestUntranslatedKeys(t *testing.T) {
	data := []byte(`a: Hello
b: ""
c: ""
`)
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	keys := f.UntranslatedKeys()
	if len(keys) != 2 {
		t.Fatalf("want 2 untranslated, got %d: %v", len(keys), keys)
	}
	if keys[0] != "b" || keys[1] != "c" {
		t.Errorf("unexpected keys: %v", keys)
	}
}

// ---------------------------------------------------------------------------
// Set / Marshal round-trip
// ---------------------------------------------------------------------------

func TestSet_AndMarshal(t *testing.T) {
	data := []byte(`greeting: ""
farewell: ""
`)
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	f.Set("greeting", "Привет")
	f.Set("farewell", "Пока")

	out, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse error: %v", err)
	}
	assertEntry(t, f2, "greeting", "Привет")
	assertEntry(t, f2, "farewell", "Пока")
}

func TestMarshal_PreservesStructure(t *testing.T) {
	data := []byte(`nav:
    home: Home
    about: About
footer:
    copyright: Copyright
`)
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "nav:") {
		t.Error("output missing nav key")
	}
	if !strings.Contains(outStr, "footer:") {
		t.Error("output missing footer key")
	}
}

func TestMarshal_RailsStyle(t *testing.T) {
	data := []byte(`en:
  greeting: Hello
  farewell: Goodbye
`)
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	f.Set("greeting", "Привет")
	out, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), "en:") {
		t.Error("expected root locale key 'en:' in output")
	}
	if !strings.Contains(string(out), "Привет") {
		t.Error("expected translated greeting in output")
	}
}

// ---------------------------------------------------------------------------
// NewTranslationFile
// ---------------------------------------------------------------------------

func TestNewTranslationFile_ClearsValues(t *testing.T) {
	src, _ := Parse([]byte(`greeting: Hello
farewell: Goodbye
`))
	target := NewTranslationFile(src, "ru")
	for _, e := range target.entries {
		if e.Value != "" {
			t.Errorf("expected empty value for %q, got %q", e.Path, e.Value)
		}
	}
	if len(target.entries) != 2 {
		t.Fatalf("expected 2 entries in target, got %d", len(target.entries))
	}
}

func TestNewTranslationFile_RailsStyle(t *testing.T) {
	src, _ := Parse([]byte(`en:
  greeting: Hello
  nav:
    home: Home
`))
	target := NewTranslationFile(src, "ru")
	if target.rootLocaleKey != "ru" {
		t.Errorf("expected rootLocaleKey=ru, got %q", target.rootLocaleKey)
	}
	if len(target.entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(target.entries))
	}
}

// ---------------------------------------------------------------------------
// SyncKeys
// ---------------------------------------------------------------------------

func TestSyncKeys_AddsNewKeys(t *testing.T) {
	src, _ := Parse([]byte(`a: Hello
b: World
c: New
`))
	target, _ := Parse([]byte(`a: Привет
b: ""
`))

	SyncKeys(src, target)

	keys := target.Keys()
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys after sync, got %d: %v", len(keys), keys)
	}

	// Existing translations preserved.
	assertEntry(t, target, "a", "Привет")
	assertEntry(t, target, "b", "")
	assertEntry(t, target, "c", "")
}

func TestSyncKeys_RemovesObsoleteKeys(t *testing.T) {
	src, _ := Parse([]byte(`a: Hello
`))
	target, _ := Parse([]byte(`a: Привет
b: Мир
`))

	SyncKeys(src, target)

	keys := target.Keys()
	if len(keys) != 1 {
		t.Fatalf("expected 1 key after sync, got %d: %v", len(keys), keys)
	}
	assertEntry(t, target, "a", "Привет")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func assertEntry(t *testing.T, f *File, path, wantValue string) {
	t.Helper()
	val, ok := f.Get(path)
	if !ok {
		t.Errorf("path %q not found in file", path)
		return
	}
	if val != wantValue {
		t.Errorf("path %q: want %q, got %q", path, wantValue, val)
	}
}
