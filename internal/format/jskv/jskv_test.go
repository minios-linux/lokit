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
