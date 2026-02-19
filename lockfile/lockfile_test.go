package lockfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashDeterministic(t *testing.T) {
	h1 := Hash("hello world")
	h2 := Hash("hello world")
	if h1 != h2 {
		t.Errorf("Hash not deterministic: %s != %s", h1, h2)
	}
	h3 := Hash("different")
	if h1 == h3 {
		t.Errorf("Hash collision: %s == %s", h1, h3)
	}
}

func TestLoadNonExistent(t *testing.T) {
	lf, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load returned error for non-existent file: %v", err)
	}
	if lf.Version != Version {
		t.Errorf("Version = %d, want %d", lf.Version, Version)
	}
	if len(lf.Checksums) != 0 {
		t.Errorf("Checksums not empty: %v", lf.Checksums)
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()

	lf, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	lf.Update("po/ru.po", "Hello", "Hello")
	lf.Update("po/ru.po", "World", "World")
	lf.Update("po/de.po", "Hello", "Hello")

	if err := lf.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file exists
	path := filepath.Join(dir, LockFileName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("Lock file not created at %s", path)
	}

	// Reload and verify
	lf2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}

	targets, keys := lf2.Stats()
	if targets != 2 {
		t.Errorf("targets = %d, want 2", targets)
	}
	if keys != 3 {
		t.Errorf("keys = %d, want 3", keys)
	}
}

func TestIsChanged(t *testing.T) {
	lf := &LockFile{
		Version:   Version,
		Checksums: make(map[string]map[string]string),
	}

	// New entry is always changed
	if !lf.IsChanged("po/ru.po", "Hello", "Hello") {
		t.Error("new entry should be changed")
	}

	// After update, same content is not changed
	lf.Update("po/ru.po", "Hello", "Hello")
	if lf.IsChanged("po/ru.po", "Hello", "Hello") {
		t.Error("unchanged entry should not be changed")
	}

	// Modified content is changed
	if !lf.IsChanged("po/ru.po", "Hello", "Hello!") {
		t.Error("modified entry should be changed")
	}

	// Different target is changed
	if !lf.IsChanged("po/de.po", "Hello", "Hello") {
		t.Error("different target should be changed")
	}
}

func TestFilterChanged(t *testing.T) {
	lf := &LockFile{
		Version:   Version,
		Checksums: make(map[string]map[string]string),
	}

	lf.Update("po/ru.po", "Hello", "Hello")
	lf.Update("po/ru.po", "World", "World")

	entries := map[string]string{
		"Hello": "Hello",      // unchanged
		"World": "World!",     // changed
		"New":   "New string", // new
	}

	changed := lf.FilterChanged("po/ru.po", entries)

	if len(changed) != 2 {
		t.Errorf("changed count = %d, want 2", len(changed))
	}
	if _, ok := changed["Hello"]; ok {
		t.Error("Hello should not be in changed set")
	}
	if _, ok := changed["World"]; !ok {
		t.Error("World should be in changed set")
	}
	if _, ok := changed["New"]; !ok {
		t.Error("New should be in changed set")
	}
}

func TestUpdateBatch(t *testing.T) {
	lf := &LockFile{
		Version:   Version,
		Checksums: make(map[string]map[string]string),
	}

	entries := map[string]string{
		"Hello": "Hello",
		"World": "World",
	}
	lf.UpdateBatch("po/ru.po", entries)

	if lf.IsChanged("po/ru.po", "Hello", "Hello") {
		t.Error("Hello should not be changed after batch update")
	}
	if lf.IsChanged("po/ru.po", "World", "World") {
		t.Error("World should not be changed after batch update")
	}
}

func TestClean(t *testing.T) {
	lf := &LockFile{
		Version:   Version,
		Checksums: make(map[string]map[string]string),
	}

	lf.Update("po/ru.po", "Hello", "Hello")
	lf.Update("po/ru.po", "World", "World")
	lf.Update("po/ru.po", "Deleted", "Deleted")

	// Only Hello and World remain in current set
	lf.Clean("po/ru.po", []string{"Hello", "World"})

	if lf.IsChanged("po/ru.po", "Hello", "Hello") {
		t.Error("Hello should still be tracked")
	}
	if !lf.IsChanged("po/ru.po", "Deleted", "Deleted") {
		t.Error("Deleted should be removed by Clean")
	}
}

func TestRemoveTarget(t *testing.T) {
	lf := &LockFile{
		Version:   Version,
		Checksums: make(map[string]map[string]string),
	}

	lf.Update("po/ru.po", "Hello", "Hello")
	lf.RemoveTarget("po/ru.po")

	targets, _ := lf.Stats()
	if targets != 0 {
		t.Errorf("targets after RemoveTarget = %d, want 0", targets)
	}
}

func TestTargets(t *testing.T) {
	lf := &LockFile{
		Version:   Version,
		Checksums: make(map[string]map[string]string),
	}

	lf.Update("po/de.po", "Hello", "Hello")
	lf.Update("po/ru.po", "Hello", "Hello")
	lf.Update("po/ar.po", "Hello", "Hello")

	targets := lf.Targets()
	expected := []string{"po/ar.po", "po/de.po", "po/ru.po"}
	if len(targets) != len(expected) {
		t.Fatalf("targets len = %d, want %d", len(targets), len(expected))
	}
	for i, want := range expected {
		if targets[i] != want {
			t.Errorf("targets[%d] = %q, want %q", i, targets[i], want)
		}
	}
}

func TestPOEntryKey(t *testing.T) {
	tests := []struct {
		msgid, msgctxt, want string
	}{
		{"Hello", "", "Hello"},
		{"Hello", "greeting", "greeting|Hello"},
		{"", "ctx", "ctx|"},
	}
	for _, tt := range tests {
		got := POEntryKey(tt.msgid, tt.msgctxt)
		if got != tt.want {
			t.Errorf("POEntryKey(%q, %q) = %q, want %q", tt.msgid, tt.msgctxt, got, tt.want)
		}
	}
}

func TestPOEntryContent(t *testing.T) {
	singular := POEntryContent("message", "")
	plural := POEntryContent("message", "messages")
	if singular == plural {
		t.Error("singular and plural content should differ")
	}
	if singular != "message" {
		t.Errorf("singular content = %q, want %q", singular, "message")
	}
}

func TestKVEntryContent(t *testing.T) {
	c1 := KVEntryContent("key1", "value1")
	c2 := KVEntryContent("key1", "value2")
	c3 := KVEntryContent("key2", "value1")
	if c1 == c2 {
		t.Error("different values should produce different content")
	}
	if c1 == c3 {
		t.Error("different keys should produce different content")
	}
}

func TestMarkdownEntryKey(t *testing.T) {
	got := MarkdownEntryKey("docs/intro.md", 3)
	want := "docs/intro.md#3"
	if got != want {
		t.Errorf("MarkdownEntryKey = %q, want %q", got, want)
	}
}

func TestSummary(t *testing.T) {
	lf := &LockFile{
		Version:   Version,
		Checksums: make(map[string]map[string]string),
	}

	if lf.Summary() != "empty" {
		t.Errorf("empty summary = %q, want %q", lf.Summary(), "empty")
	}

	lf.Update("po/ru.po", "Hello", "Hello")
	lf.Update("po/de.po", "Hello", "Hello")
	s := lf.Summary()
	if s == "empty" {
		t.Error("summary should not be empty after updates")
	}
}

func TestConcurrentAccess(t *testing.T) {
	lf := &LockFile{
		Version:   Version,
		Checksums: make(map[string]map[string]string),
	}

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			target := "po/ru.po"
			key := "key" + string(rune('0'+n))
			lf.Update(target, key, "value")
			lf.IsChanged(target, key, "value")
			lf.Stats()
			done <- true
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	_, keys := lf.Stats()
	if keys != 10 {
		t.Errorf("keys after concurrent writes = %d, want 10", keys)
	}
}
