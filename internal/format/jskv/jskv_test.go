package jskv

import "testing"

func TestParseAndMarshal(t *testing.T) {
	input := []byte(`window.translations = {
    "Hello": "Hello",
    "World": ""
};
`)

	f, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(f.Keys()) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(f.Keys()))
	}
	if len(f.UntranslatedKeys()) != 1 || f.UntranslatedKeys()[0] != "World" {
		t.Fatalf("unexpected untranslated keys: %#v", f.UntranslatedKeys())
	}
	if !f.Set("World", "Mir") {
		t.Fatalf("Set(World) failed")
	}

	out, err := f.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse(Marshal()) error = %v", err)
	}
	if got := f2.translations["World"]; got != "Mir" {
		t.Fatalf("round-trip value mismatch: got %q", got)
	}
}

func TestInvalidFormat(t *testing.T) {
	if _, err := Parse([]byte(`{"a":"b"}`)); err == nil {
		t.Fatalf("expected parse error for missing assignment wrapper")
	}
}

func TestSyncKeys(t *testing.T) {
	src, err := Parse([]byte(`window.i18n = {
    "Hello": "Hello",
    "World": "World"
};
`))
	if err != nil {
		t.Fatalf("Parse source: %v", err)
	}
	target, err := Parse([]byte(`window.translations = {
    "Hello": "Hallo",
    "Obsolete": "Alt"
};
`))
	if err != nil {
		t.Fatalf("Parse target: %v", err)
	}

	SyncKeys(src, target)

	keys := target.Keys()
	if len(keys) != 2 || keys[0] != "Hello" || keys[1] != "World" {
		t.Fatalf("Keys() = %#v, want [Hello World]", keys)
	}
	if target.prefix != "window.i18n" {
		t.Fatalf("prefix = %q, want window.i18n", target.prefix)
	}
	if got := target.translations["Hello"]; got != "Hallo" {
		t.Fatalf("Hello translation = %q, want Hallo", got)
	}
	if got := target.translations["World"]; got != "" {
		t.Fatalf("World translation = %q, want empty", got)
	}
	if _, ok := target.translations["Obsolete"]; ok {
		t.Fatal("obsolete key was preserved")
	}
}

func TestSyncKeysNilInputs(t *testing.T) {
	target, err := Parse([]byte(`window.translations = {
    "Hello": "Hallo"
};
`))
	if err != nil {
		t.Fatalf("Parse target: %v", err)
	}

	SyncKeys(nil, target)
	if keys := target.Keys(); len(keys) != 1 || keys[0] != "Hello" {
		t.Fatalf("SyncKeys(nil, target) changed target keys: %#v", keys)
	}

	SyncKeys(target, nil)
	if keys := target.Keys(); len(keys) != 1 || keys[0] != "Hello" {
		t.Fatalf("SyncKeys(target, nil) changed target keys: %#v", keys)
	}
	if target.prefix != "window.translations" {
		t.Fatalf("SyncKeys(target, nil) changed prefix: %q", target.prefix)
	}
}
