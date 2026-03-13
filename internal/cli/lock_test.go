package cli

import (
	"testing"
)

func TestNewLockCmdSubcommands(t *testing.T) {
	cmd := newLockCmd()

	if cmd.Use != "lock" {
		t.Fatalf("Use = %q, want %q", cmd.Use, "lock")
	}

	want := map[string]bool{
		"init":   true,
		"status": true,
		"clean":  true,
		"reset":  true,
	}

	for _, sub := range cmd.Commands() {
		delete(want, sub.Name())
	}

	if len(want) != 0 {
		t.Fatalf("missing subcommands: %v", want)
	}
}

func TestCountStaleKeys(t *testing.T) {
	tracked := []string{"a", "b", "c", "d"}
	current := []string{"a", "c"}

	if got := countStaleKeys(tracked, current); got != 2 {
		t.Fatalf("countStaleKeys() = %d, want %d", got, 2)
	}

	if got := countStaleKeys(nil, current); got != 0 {
		t.Fatalf("countStaleKeys(nil) = %d, want 0", got)
	}
}

func TestLockStatusFlags(t *testing.T) {
	cmd := newLockStatusCmd()

	if cmd.Flags().Lookup("verbose") == nil {
		t.Fatal("missing --verbose flag")
	}
	if cmd.Flags().Lookup("json") == nil {
		t.Fatal("missing --json flag")
	}
}

type testLockKVFile struct {
	keys         []string
	untranslated map[string]struct{}
}

func (f *testLockKVFile) Keys() []string { return append([]string(nil), f.keys...) }

func (f *testLockKVFile) UntranslatedKeys() []string {
	var out []string
	for _, k := range f.keys {
		if _, ok := f.untranslated[k]; ok {
			out = append(out, k)
		}
	}
	return out
}

func (f *testLockKVFile) Set(string, string) bool { return true }

func (f *testLockKVFile) Stats() (int, int, float64) { return 0, 0, 0 }

func (f *testLockKVFile) SourceValues() map[string]string { return map[string]string{} }

func (f *testLockKVFile) WriteFile(string) error { return nil }

func TestFilterSourceEntriesByTranslatedKeys(t *testing.T) {
	sourceEntries := map[string]string{
		"a": "A",
		"b": "B",
		"c": "C",
	}
	translated := map[string]struct{}{
		"a": {},
		"c": {},
		"x": {},
	}

	got := filterSourceEntriesByTranslatedKeys(sourceEntries, translated)
	if len(got) != 2 {
		t.Fatalf("len(filtered) = %d, want 2", len(got))
	}
	if got["a"] != "A" || got["c"] != "C" {
		t.Fatalf("unexpected filtered entries: %+v", got)
	}
	if _, ok := got["b"]; ok {
		t.Fatalf("unexpected key 'b' in filtered entries")
	}
}

func TestTranslatedKeysFromKVFile(t *testing.T) {
	f := &testLockKVFile{
		keys: []string{"k1", "k2", "k3"},
		untranslated: map[string]struct{}{
			"k2": {},
		},
	}

	got := translatedKeysFromKVFile(f)
	if len(got) != 2 {
		t.Fatalf("len(translated) = %d, want 2", len(got))
	}
	if _, ok := got["k1"]; !ok {
		t.Fatalf("k1 should be translated")
	}
	if _, ok := got["k3"]; !ok {
		t.Fatalf("k3 should be translated")
	}
	if _, ok := got["k2"]; ok {
		t.Fatalf("k2 should be untranslated")
	}
}
