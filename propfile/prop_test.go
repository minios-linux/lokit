package propfile

import (
	"strings"
	"testing"
)

func TestParse_Basic(t *testing.T) {
	data := []byte("greeting=Hello\nfarewell=Goodbye\n")
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := f.Get("greeting"); got != "Hello" {
		t.Errorf("greeting = %q, want %q", got, "Hello")
	}
	if got, _ := f.Get("farewell"); got != "Goodbye" {
		t.Errorf("farewell = %q, want %q", got, "Goodbye")
	}
}

func TestParse_CommentsAndBlanks(t *testing.T) {
	data := []byte("# This is a comment\n\nkey=value\n")
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Keys()) != 1 {
		t.Errorf("expected 1 key, got %d", len(f.Keys()))
	}
	if got, _ := f.Get("key"); got != "value" {
		t.Errorf("key = %q, want %q", got, "value")
	}
}

func TestParse_ColonSeparator(t *testing.T) {
	data := []byte("name: World\n")
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := f.Get("name"); got != "World" {
		t.Errorf("name = %q, want %q", got, "World")
	}
}

func TestParse_ValueWithEquals(t *testing.T) {
	data := []byte("url=http://example.com?a=1&b=2\n")
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := f.Get("url"); got != "http://example.com?a=1&b=2" {
		t.Errorf("url = %q", got)
	}
}

func TestParse_ExclamationComment(t *testing.T) {
	data := []byte("! another comment\nkey=val\n")
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Keys()) != 1 {
		t.Errorf("expected 1 key, got %d", len(f.Keys()))
	}
}

func TestStats(t *testing.T) {
	data := []byte("a=hello\nb=\nc=world\n")
	f, err := Parse(data)
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
	data := []byte("a=hello\nb=\nc=\n")
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	keys := f.UntranslatedKeys()
	if len(keys) != 2 {
		t.Errorf("untranslated = %d, want 2", len(keys))
	}
}

func TestSet_AndMarshal(t *testing.T) {
	data := []byte("a=\nb=\n")
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	f.Set("a", "value_a")
	f.Set("b", "value_b")

	out, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "a=value_a") {
		t.Errorf("marshal missing a=value_a: %s", out)
	}
	if !strings.Contains(string(out), "b=value_b") {
		t.Errorf("marshal missing b=value_b: %s", out)
	}
}

func TestMarshal_PreservesCommentsAndBlanks(t *testing.T) {
	src := "# header\n\nkey=value\n"
	f, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Errorf("round-trip failed:\ngot:  %q\nwant: %q", string(out), src)
	}
}

func TestNewTranslationFile_ClearsValues(t *testing.T) {
	src := "a=hello\nb=world\n"
	srcFile, _ := Parse([]byte(src))
	target := NewTranslationFile(srcFile)
	if v, _ := target.Get("a"); v != "" {
		t.Errorf("expected empty value for a, got %q", v)
	}
	if len(target.Keys()) != 2 {
		t.Errorf("expected 2 keys, got %d", len(target.Keys()))
	}
}

func TestSyncKeys_AddsNewKeys(t *testing.T) {
	src, _ := Parse([]byte("a=hello\nb=world\nc=new\n"))
	target, _ := Parse([]byte("a=translated_a\nb=translated_b\n"))
	SyncKeys(src, target)

	if v, _ := target.Get("a"); v != "translated_a" {
		t.Errorf("a = %q, want translated_a", v)
	}
	if v, _ := target.Get("c"); v != "" {
		t.Errorf("c = %q, want empty (new key)", v)
	}
	if len(target.Keys()) != 3 {
		t.Errorf("expected 3 keys, got %d", len(target.Keys()))
	}
}

func TestSyncKeys_RemovesObsoleteKeys(t *testing.T) {
	src, _ := Parse([]byte("a=hello\n"))
	target, _ := Parse([]byte("a=translated_a\nobsolete=gone\n"))
	SyncKeys(src, target)

	if _, ok := target.Get("obsolete"); ok {
		t.Error("obsolete key should have been removed")
	}
	if len(target.Keys()) != 1 {
		t.Errorf("expected 1 key, got %d", len(target.Keys()))
	}
}
