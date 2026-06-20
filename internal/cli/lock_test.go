package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/minios-linux/lokit/config"
	"github.com/minios-linux/lokit/lockfile"
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

func TestOrphanLockTargetsPrefixScope(t *testing.T) {
	lf := &lockfile.LockFile{
		Checksums: map[string]map[string]string{
			"app/de":       {"hello": "hash"},
			"app/es":       {"hello": "hash"},
			"app/old/de":   {"stale": "hash"},
			"app/ui/de":    {"keep": "hash"},
			"app-extra/de": {"keep": "hash"},
		},
	}
	expected := map[string]struct{}{
		"app/de":       {},
		"app/ui/de":    {},
		"app-extra/de": {},
	}
	scopes := []orphanScope{{name: "app", prefix: true}}

	got := orphanLockTargets(lf, expected, scopes, nil)
	want := []string{"app/es", "app/old/de"}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orphanLockTargets = %v, want %v", got, want)
	}
}

func TestOrphanLockTargetsExactScopeDoesNotCleanNestedTarget(t *testing.T) {
	lf := &lockfile.LockFile{
		Checksums: map[string]map[string]string{
			"app/de":    {"hello": "hash"},
			"app/es":    {"hello": "hash"},
			"app/ui/de": {"keep": "hash"},
		},
	}
	expected := map[string]struct{}{
		"app/de": {},
	}
	scopes := []orphanScope{{name: "app", targetType: config.TargetTypeYAML, langs: map[string]struct{}{"de": {}, "es": {}}}}

	got := orphanLockTargets(lf, expected, scopes, nil)
	if len(got) != 1 || got[0] != "app/es" {
		t.Fatalf("orphanLockTargets = %v, want [app/es]", got)
	}
}

func TestOrphanCleanupScopesExactBeatsPrefix(t *testing.T) {
	allResolved := []config.ResolvedTarget{
		{Target: config.Target{Name: "app", Type: config.TargetTypeYAML, Languages: []string{"de"}}, Languages: []string{"de"}},
		{Target: config.Target{Name: "app/ui", Type: config.TargetTypeYAML, Languages: []string{"de"}}, Languages: []string{"de"}},
	}
	selected := []config.ResolvedTarget{allResolved[0]}

	scopes := orphanCleanupScopes(allResolved, selected, []string{"app"})
	if len(scopes) != 1 {
		t.Fatalf("len(scopes) = %d, want 1", len(scopes))
	}
	if scopes[0].prefix {
		t.Fatalf("expected exact scope, got prefix scope: %+v", scopes[0])
	}
	if scopes[0].name != "app" {
		t.Fatalf("scope name = %q, want app", scopes[0].name)
	}
}

func TestOrphanCleanupScopesMultipleTargets(t *testing.T) {
	allResolved := []config.ResolvedTarget{
		{Target: config.Target{Name: "app", Type: config.TargetTypeYAML, Languages: []string{"de"}}, Languages: []string{"de"}},
		{Target: config.Target{Name: "docs", Type: config.TargetTypePo4a, Languages: []string{"de"}}, Languages: []string{"de"}},
		{Target: config.Target{Name: "matrix/a", Type: config.TargetTypeYAML, Languages: []string{"de"}}, Languages: []string{"de"}},
	}
	selected := []config.ResolvedTarget{allResolved[0], allResolved[1], allResolved[2]}

	scopes := orphanCleanupScopes(allResolved, selected, []string{"app,docs", "matrix"})
	if len(scopes) != 3 {
		t.Fatalf("len(scopes) = %d, want 3", len(scopes))
	}
	if scopes[0].name != "app" || scopes[0].prefix {
		t.Fatalf("scope[0] = %+v, want exact app", scopes[0])
	}
	if scopes[1].name != "docs" || scopes[1].prefix {
		t.Fatalf("scope[1] = %+v, want exact docs", scopes[1])
	}
	if scopes[2].name != "matrix" || !scopes[2].prefix {
		t.Fatalf("scope[2] = %+v, want prefix matrix", scopes[2])
	}
}

func TestOrphanLockTargetsAllTargets(t *testing.T) {
	lf := &lockfile.LockFile{
		Checksums: map[string]map[string]string{
			"app/de":   {"hello": "hash"},
			"old/de":   {"stale": "hash"},
			"other/de": {"keep": "hash"},
		},
	}
	expected := map[string]struct{}{
		"app/de":   {},
		"other/de": {},
	}

	got := orphanLockTargets(lf, expected, nil, nil)
	if len(got) != 1 || got[0] != "old/de" {
		t.Fatalf("orphanLockTargets = %v, want [old/de]", got)
	}
}

func TestExpectedLockTargetsPo4aFromConfiguredMasters(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "po4a.cfg"), []byte(`[po4a_langs] de
[po4a_paths] pot/$master.pot $lang:po/$lang/$master.po
[type: man] chapter1.1 $lang:translated/$lang/chapter1.1
[type: man] chapter2.1 $lang:translated/$lang/chapter2.1
`), 0o644); err != nil {
		t.Fatalf("write po4a.cfg: %v", err)
	}

	resolved := []config.ResolvedTarget{
		{
			Target: config.Target{
				Name:      "docs",
				Type:      config.TargetTypePo4a,
				Config:    "po4a.cfg",
				Languages: []string{"de"},
			},
			AbsRoot:   dir,
			Languages: []string{"de"},
		},
	}

	got, blocked := expectedLockTargets(resolved)
	if len(blocked) != 0 {
		t.Fatalf("blocked scopes = %v, want none", blocked)
	}
	for _, key := range []string{"docs/chapter1.1/de", "docs/chapter2.1/de"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing expected lock target %q in %v", key, got)
		}
	}

	statusKeys := lockTargetKeysFor(resolved[0], "de")
	if len(statusKeys) != 2 || statusKeys[0] != "docs/chapter1.1/de" || statusKeys[1] != "docs/chapter2.1/de" {
		t.Fatalf("lockTargetKeysFor = %v, want configured po4a masters", statusKeys)
	}
}

func TestExpectedLockTargetsPo4aBlocksUnresolvedConfig(t *testing.T) {
	resolved := []config.ResolvedTarget{
		{
			Target: config.Target{
				Name:      "docs",
				Type:      config.TargetTypePo4a,
				Config:    "po4a.cfg",
				Languages: []string{"de"},
			},
			AbsRoot:   t.TempDir(),
			Languages: []string{"de"},
		},
	}

	got, blocked := expectedLockTargets(resolved)
	if len(got) != 0 {
		t.Fatalf("len(expectedLockTargets) = %d, want 0", len(got))
	}
	if _, ok := blocked["docs/de"]; !ok {
		t.Fatalf("blocked scopes = %v, want docs/de", blocked)
	}
}

func TestLockTargetInScope(t *testing.T) {
	tests := []struct {
		lockTarget   string
		targetFilter string
		want         bool
	}{
		{lockTarget: "app/de", targetFilter: "app", want: true},
		{lockTarget: "app/master/de", targetFilter: "app", want: true},
		{lockTarget: "app", targetFilter: "app", want: true},
		{lockTarget: "app-extra/de", targetFilter: "app", want: false},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.lockTarget, func(t *testing.T) {
			if got := lockTargetInScope(tc.lockTarget, tc.targetFilter); got != tc.want {
				t.Fatalf("lockTargetInScope(%q, %q) = %v, want %v", tc.lockTarget, tc.targetFilter, got, tc.want)
			}
		})
	}
}

func TestLockTargetBlockedLanguageScope(t *testing.T) {
	blocked := map[string]struct{}{"docs/de": {}}

	if !lockTargetBlocked("docs/chapter1.1/de", blocked) {
		t.Fatal("expected docs/chapter1.1/de to be blocked by docs/de")
	}
	if lockTargetBlocked("docs/chapter1.1/fr", blocked) {
		t.Fatal("did not expect docs/chapter1.1/fr to be blocked by docs/de")
	}
	if lockTargetBlocked("docs-extra/chapter1.1/de", blocked) {
		t.Fatal("did not expect docs-extra/chapter1.1/de to be blocked by docs/de")
	}

	blocked = map[string]struct{}{"app/ui/de": {}}
	if !lockTargetBlocked("app/ui/page/de", blocked) {
		t.Fatal("expected slash-named target language scope to block matching lock target")
	}
	if lockTargetBlocked("app/ui/page/fr", blocked) {
		t.Fatal("did not expect slash-named target language scope to block another language")
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
