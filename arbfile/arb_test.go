package arbfile

import (
	"strings"
	"testing"
)

const sampleARB = `{
  "@@locale": "en",
  "greeting": "Hello, {name}!",
  "@greeting": {
    "description": "A greeting message"
  },
  "farewell": "Goodbye!"
}
`

func TestParse_Basic(t *testing.T) {
	f, err := Parse([]byte(sampleARB))
	if err != nil {
		t.Fatal(err)
	}
	if f.Locale() != "en" {
		t.Errorf("locale = %q, want %q", f.Locale(), "en")
	}
	if v, _ := f.Get("greeting"); v != "Hello, {name}!" {
		t.Errorf("greeting = %q", v)
	}
	if v, _ := f.Get("farewell"); v != "Goodbye!" {
		t.Errorf("farewell = %q", v)
	}
}

func TestParse_MetadataNotTranslatable(t *testing.T) {
	f, err := Parse([]byte(sampleARB))
	if err != nil {
		t.Fatal(err)
	}
	keys := f.Keys()
	for _, k := range keys {
		if strings.HasPrefix(k, "@") {
			t.Errorf("metadata key %q should not appear in Keys()", k)
		}
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 translatable keys, got %d: %v", len(keys), keys)
	}
}

func TestStats(t *testing.T) {
	data := `{"@@locale":"en","a":"hello","b":"","c":"world"}`
	f, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	total, translated, _ := f.Stats()
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if translated != 2 {
		t.Errorf("translated = %d, want 2", translated)
	}
}

func TestUntranslatedKeys(t *testing.T) {
	data := `{"@@locale":"en","a":"","b":"","c":"hello"}`
	f, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	keys := f.UntranslatedKeys()
	if len(keys) != 2 {
		t.Errorf("untranslated = %d, want 2", len(keys))
	}
}

func TestSet_UpdatesValue(t *testing.T) {
	f, err := Parse([]byte(`{"@@locale":"en","hello":""}`))
	if err != nil {
		t.Fatal(err)
	}
	ok := f.Set("hello", "Привет")
	if !ok {
		t.Error("Set returned false")
	}
	if v, _ := f.Get("hello"); v != "Привет" {
		t.Errorf("got %q, want Привет", v)
	}
}

func TestMarshal_LocaleFirst(t *testing.T) {
	f, err := Parse([]byte(sampleARB))
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	localePos := strings.Index(s, `"@@locale"`)
	greetingPos := strings.Index(s, `"greeting"`)
	if localePos > greetingPos {
		t.Error("@@locale should appear before greeting in output")
	}
}

func TestMarshal_MetadataPreserved(t *testing.T) {
	f, err := Parse([]byte(sampleARB))
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"@greeting"`) {
		t.Error("@greeting metadata should be preserved in output")
	}
}

func TestNewTranslationFile_ClearsValues(t *testing.T) {
	src, _ := Parse([]byte(sampleARB))
	target := NewTranslationFile(src, "ru")
	if target.Locale() != "ru" {
		t.Errorf("locale = %q, want ru", target.Locale())
	}
	if v, _ := target.Get("greeting"); v != "" {
		t.Errorf("expected empty greeting, got %q", v)
	}
	// Metadata should still be present.
	if idx, ok := target.index["@greeting"]; !ok || !target.entries[idx].isMeta {
		t.Error("@greeting metadata should be preserved in new translation file")
	}
}

func TestSyncKeys_AddsNewKeys(t *testing.T) {
	src, _ := Parse([]byte(`{"@@locale":"en","a":"hello","b":"world","c":"new"}`))
	target, _ := Parse([]byte(`{"@@locale":"ru","a":"привет","b":"мир"}`))
	SyncKeys(src, target)

	if v, _ := target.Get("a"); v != "привет" {
		t.Errorf("a = %q, want привет", v)
	}
	if v, _ := target.Get("c"); v != "" {
		t.Errorf("c = %q, want empty (new key)", v)
	}
	if len(target.Keys()) != 3 {
		t.Errorf("expected 3 keys, got %d", len(target.Keys()))
	}
}

func TestSyncKeys_RemovesObsoleteKeys(t *testing.T) {
	src, _ := Parse([]byte(`{"@@locale":"en","a":"hello"}`))
	target, _ := Parse([]byte(`{"@@locale":"ru","a":"привет","obsolete":"gone"}`))
	SyncKeys(src, target)

	if _, ok := target.Get("obsolete"); ok {
		t.Error("obsolete key should have been removed")
	}
}
