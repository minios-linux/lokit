package i18next

import (
	"strings"
	"testing"
)

func TestParseAndMarshal_PreservesOrderAndCounts(t *testing.T) {
	data := []byte(`{
  "_meta": {"name": "English", "flag": "US"},
  "translations": {
    "First key": "First value",
    "Second key": "",
    "Third key": "Third value"
  }
}`)

	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	keys := f.Keys()
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	if keys[0] != "First key" || keys[1] != "Second key" || keys[2] != "Third key" {
		t.Fatalf("unexpected key order: %v", keys)
	}

	total, translated, untranslated := f.Stats()
	if total != 3 || translated != 2 || untranslated != 1 {
		t.Fatalf("unexpected stats: total=%d translated=%d untranslated=%d", total, translated, untranslated)
	}

	out, err := f.Marshal()
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	outStr := string(out)
	idxFirst := strings.Index(outStr, `"First key"`)
	idxSecond := strings.Index(outStr, `"Second key"`)
	idxThird := strings.Index(outStr, `"Third key"`)
	if idxFirst < 0 || idxSecond < 0 || idxThird < 0 {
		t.Fatalf("marshaled output missing keys: %s", outStr)
	}
	if !(idxFirst < idxSecond && idxSecond < idxThird) {
		t.Fatalf("marshaled key order changed: %s", outStr)
	}
}

func TestParse_InvalidJSON(t *testing.T) {
	_, err := Parse([]byte(`{"broken":`))
	if err == nil {
		t.Fatal("expected parse error for invalid JSON")
	}
}

func TestResolveMeta_NormalizationAndFallback(t *testing.T) {
	m := ResolveMeta("pt_br")
	if m.Name == "" || m.Flag == "" {
		t.Fatalf("expected normalized metadata for pt_br, got %#v", m)
	}

	unknown := ResolveMeta("zz-ZZ")
	if unknown.Name != "zz-ZZ" || unknown.Flag != "" {
		t.Fatalf("unexpected unknown metadata fallback: %#v", unknown)
	}
}
