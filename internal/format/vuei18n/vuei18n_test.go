package vuei18n

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseAndSync(t *testing.T) {
	data := []byte(`{
  "buttons": {
    "save": "Save",
    "cancel": ""
  },
  "count": 1,
  "nested": {
    "enabled": true
  }
}`)

	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if got := f.Keys(); !reflect.DeepEqual(got, []string{"buttons.save", "buttons.cancel"}) {
		t.Fatalf("Keys() = %v", got)
	}

	if got := f.UntranslatedKeys(); !reflect.DeepEqual(got, []string{"buttons.cancel"}) {
		t.Fatalf("UntranslatedKeys() = %v", got)
	}

	if ok := f.Set("buttons.cancel", "Cancel"); !ok {
		t.Fatal("Set returned false")
	}

	total, translated, _ := f.Stats()
	if total != 2 || translated != 2 {
		t.Fatalf("Stats() = total=%d translated=%d", total, translated)
	}
}

func TestNewTranslationFileAndWrite(t *testing.T) {
	src, err := Parse([]byte(`{"a":{"b":"Hello"},"x":true}`))
	if err != nil {
		t.Fatalf("Parse src: %v", err)
	}

	tgt := NewTranslationFile(src)
	if got, ok := tgt.Get("a.b"); !ok || got != "" {
		t.Fatalf("Get(a.b) = %q, %v", got, ok)
	}

	if !tgt.Set("a.b", "Hola") {
		t.Fatal("Set(a.b) failed")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "es.json")
	if err := tgt.WriteFile(path); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("written file missing: %v", err)
	}

	reload, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if got, _ := reload.Get("a.b"); got != "Hola" {
		t.Fatalf("reloaded value = %q", got)
	}
}
