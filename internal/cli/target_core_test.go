package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minios-linux/lokit/config"
	po "github.com/minios-linux/lokit/internal/format/po"
	"github.com/minios-linux/lokit/translate"
)

func TestPo4aMarkdownDirSupportsRootAndManpagesLayouts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configDir string
		docsDir   string
	}{
		{name: "root config", configDir: ".", docsDir: "docs"},
		{name: "manpages config", configDir: "manpages", docsDir: "docs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			configDir := filepath.Join(root, tc.configDir)
			docsDir := filepath.Join(root, tc.docsDir)
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				t.Fatalf("mkdir config dir: %v", err)
			}
			if err := os.MkdirAll(docsDir, 0o755); err != nil {
				t.Fatalf("mkdir docs dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(docsDir, "tool.1.md"), []byte("# tool\n"), 0o644); err != nil {
				t.Fatalf("write manpage source: %v", err)
			}
			if got := po4aMarkdownDir(configDir, []string{"tool.1"}); got != docsDir {
				t.Fatalf("po4aMarkdownDir(%q) = %q, want %q", configDir, got, docsDir)
			}
		})
	}
}

func TestPo4aMarkdownDirIgnoresUnconfiguredMarkdown(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "manpages")
	wrongDir := filepath.Join(configDir, "docs")
	wantDir := filepath.Join(root, "docs")
	for _, dir := range []string{configDir, wrongDir, wantDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(wrongDir, "unlisted.1.md"), []byte("# unlisted\n"), 0o644); err != nil {
		t.Fatalf("write unrelated source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wantDir, "tool.1.md"), []byte("# tool\n"), 0o644); err != nil {
		t.Fatalf("write configured source: %v", err)
	}
	if got := po4aMarkdownDir(configDir, []string{"tool.1"}); got != wantDir {
		t.Fatalf("po4aMarkdownDir() = %q, want %q", got, wantDir)
	}
}

func TestGenerateManpagesFromMarkdownOnlyUsesConfiguredMasters(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	docsDir := filepath.Join(root, "docs")
	manDir := filepath.Join(root, "manpages")
	for _, dir := range []string{binDir, docsDir, manDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	pandoc := filepath.Join(binDir, "pandoc")
	if err := os.WriteFile(pandoc, []byte("#!/bin/sh\nwhile [ \"$1\" != \"-o\" ]; do shift; done\nprintf generated > \"$2\"\n"), 0o755); err != nil {
		t.Fatalf("write fake pandoc: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	configPath := filepath.Join(manDir, "po4a.cfg")
	if err := os.WriteFile(configPath, []byte("[type: man] wanted.1 $lang:$lang/wanted.1\n"), 0o644); err != nil {
		t.Fatalf("write po4a config: %v", err)
	}
	for _, name := range []string{"wanted.1.md", "unwanted.1.md"} {
		if err := os.WriteFile(filepath.Join(docsDir, name), []byte("# test\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	proj := &config.Project{Po4aConfig: configPath, DocsDir: docsDir}
	if err := generateManpagesFromMarkdown(proj); err != nil {
		t.Fatalf("generateManpagesFromMarkdown: %v", err)
	}
	if _, err := os.Stat(filepath.Join(manDir, "wanted.1")); err != nil {
		t.Fatalf("configured master was not generated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(manDir, "unwanted.1")); !os.IsNotExist(err) {
		t.Fatalf("unconfigured master was generated, stat err=%v", err)
	}
}

func TestSynchronizePo4aSharedTranslationsResolvesFuzzyConflict(t *testing.T) {
	obsolete, err := po.Parse(strings.NewReader(`#~ msgid "MiniOS Development Team"
#~ msgstr "Obsolete translation"
`))
	if err != nil {
		t.Fatalf("parse obsolete PO: %v", err)
	}
	canonical, err := po.Parse(strings.NewReader(`msgid ""
msgstr ""

msgid "MiniOS Development Team"
msgstr "MiniOS Development Team"
`))
	if err != nil {
		t.Fatalf("parse canonical PO: %v", err)
	}
	localized, err := po.Parse(strings.NewReader(`msgid ""
msgstr ""

msgid "MiniOS Development Team"
msgstr "Equipe de Desenvolvimento do MiniOS"
`))
	if err != nil {
		t.Fatalf("parse localized PO: %v", err)
	}
	conflicting, err := po.Parse(strings.NewReader(`msgid ""
msgstr ""

#, fuzzy
msgid "MiniOS Development Team"
msgstr "#-#-#-#-# conflict #-#-#-#-#"
`))
	if err != nil {
		t.Fatalf("parse conflicting PO: %v", err)
	}
	dir := t.TempDir()
	tasks := []translate.LangTask{
		{Lang: "pt_BR", POFile: obsolete, POPath: filepath.Join(dir, "obsolete.po")},
		{Lang: "pt_BR", POFile: canonical, POPath: filepath.Join(dir, "first.po")},
		{Lang: "pt_BR", POFile: localized, POPath: filepath.Join(dir, "second.po")},
		{Lang: "pt_BR", POFile: conflicting, POPath: filepath.Join(dir, "third.po")},
	}
	if err := synchronizePo4aSharedTranslations(tasks, translate.Options{}); err != nil {
		t.Fatalf("synchronizePo4aSharedTranslations: %v", err)
	}
	entry := conflicting.Entries[0]
	if entry.IsFuzzy() || entry.MsgStr != "Equipe de Desenvolvimento do MiniOS" {
		t.Fatalf("conflicting entry was not synchronized: %#v", entry)
	}
	if _, err := os.Stat(tasks[3].POPath); err != nil {
		t.Fatalf("synchronized PO was not written: %v", err)
	}
}

func TestSynchronizePo4aSharedTranslationsHonorsFilters(t *testing.T) {
	for _, tc := range []struct {
		name         string
		opts         translate.Options
		synchronized bool
	}{
		{name: "ignored", opts: translate.Options{IgnoredKeys: []string{"Shared"}}},
		{name: "ignored with force", opts: translate.Options{IgnoredKeys: []string{"Shared"}, ForceTranslate: true}},
		{name: "locked", opts: translate.Options{LockedKeys: []string{"Shared"}}},
		{name: "locked with force", opts: translate.Options{LockedKeys: []string{"Shared"}, ForceTranslate: true}, synchronized: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canonical, err := po.Parse(strings.NewReader("msgid \"Shared\"\nmsgstr \"Localized\"\n"))
			if err != nil {
				t.Fatalf("parse canonical PO: %v", err)
			}
			conflicting, err := po.Parse(strings.NewReader("#, fuzzy\nmsgid \"Shared\"\nmsgstr \"Conflict\"\n"))
			if err != nil {
				t.Fatalf("parse conflicting PO: %v", err)
			}
			tasks := []translate.LangTask{
				{Lang: "pt_BR", POFile: canonical, POPath: filepath.Join(t.TempDir(), "canonical.po")},
				{Lang: "pt_BR", POFile: conflicting, POPath: filepath.Join(t.TempDir(), "conflicting.po")},
			}
			if err := synchronizePo4aSharedTranslations(tasks, tc.opts); err != nil {
				t.Fatalf("synchronizePo4aSharedTranslations: %v", err)
			}
			entry := conflicting.Entries[0]
			if tc.synchronized {
				if entry.IsFuzzy() || entry.MsgStr != "Localized" {
					t.Fatalf("locked entry was not synchronized with force: %#v", entry)
				}
			} else if !entry.IsFuzzy() || entry.MsgStr != "Conflict" {
				t.Fatalf("excluded entry changed: %#v", entry)
			}
		})
	}
}

func TestPOTComparisonIgnoresOnlyCreationDate(t *testing.T) {
	a := "msgid \"\"\nmsgstr \"\"\n\"POT-Creation-Date: old\\n\"\n\nmsgid \"Name\"\nmsgstr \"\"\n"
	b := strings.Replace(a, "old", "new", 1)
	if !potEqualIgnoringCreationDate(a, b) {
		t.Fatal("POT creation date was treated as a semantic change")
	}
	if potEqualIgnoringCreationDate(a, strings.Replace(b, "Name", "Changed", 1)) {
		t.Fatal("POT msgid change was ignored")
	}
	messageA := a + "\nmsgid \"POT-Creation-Date: old\"\nmsgstr \"\"\n"
	messageB := strings.Replace(messageA, "Date: old\"", "Date: new\"", 1)
	if potEqualIgnoringCreationDate(messageA, messageB) {
		t.Fatal("POT-Creation-Date text outside the header was ignored")
	}
}

func TestRestoreFileSnapshotPreservesMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.pot")
	if err := os.WriteFile(path, []byte("original"), 0o664); err != nil {
		t.Fatalf("write original: %v", err)
	}
	if err := os.Chmod(path, 0o664); err != nil {
		t.Fatalf("chmod original: %v", err)
	}
	oldTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	snapshot, err := snapshotFile(path)
	if err != nil {
		t.Fatalf("snapshotFile: %v", err)
	}
	if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod partial: %v", err)
	}
	if err := restoreFileSnapshot(path, snapshot); err != nil {
		t.Fatalf("restoreFileSnapshot: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if string(data) != "original" || info.Mode().Perm() != 0o664 || !info.ModTime().Equal(oldTime) {
		t.Fatalf("restored data=%q mode=%o mtime=%v", data, info.Mode().Perm(), info.ModTime())
	}
}

func TestSnapshotFileReportsReadErrors(t *testing.T) {
	if _, err := snapshotFile(t.TempDir()); err == nil {
		t.Fatal("snapshotFile accepted an unreadable file path")
	}
}

func TestMixedExtractionFailureCanRestoreOriginalPOT(t *testing.T) {
	xgettext, err := exec.LookPath("xgettext")
	if err != nil {
		t.Skip("xgettext is not installed")
	}
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.Symlink(xgettext, filepath.Join(binDir, "xgettext")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	t.Setenv("PATH", binDir)
	if err := os.WriteFile(filepath.Join(root, "messages.py"), []byte("_(\"Python message\")\n"), 0o644); err != nil {
		t.Fatalf("write Python source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "messages.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write Go source: %v", err)
	}
	potPath := filepath.Join(root, "po", "messages.pot")
	if err := os.Mkdir(filepath.Dir(potPath), 0o755); err != nil {
		t.Fatalf("Mkdir PO: %v", err)
	}
	original := []byte("msgid \"Original message\"\nmsgstr \"\"\n")
	if err := os.WriteFile(potPath, original, 0o644); err != nil {
		t.Fatalf("write original POT: %v", err)
	}
	proj := &config.Project{Root: root, Name: "mixed", SourceDirs: []string{root}, POTFile: potPath}
	if _, err := doExtract(proj); err == nil {
		t.Fatal("mixed extraction succeeded without xgotext")
	}
	restored, err := os.ReadFile(potPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(restored) != string(original) {
		t.Fatalf("POT was not restored: %q", restored)
	}
}

func TestASTExtractionHonorsExactFilesAndPreservesPOTMode(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "messages.go"), []byte("package sample\nfunc messages() { T(\"Included message\") }\n"), 0o644); err != nil {
		t.Fatalf("write included source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "excluded.go"), []byte("package sample\nfunc broken(\n"), 0o644); err != nil {
		t.Fatalf("write excluded source: %v", err)
	}
	potPath := filepath.Join(root, "po", "messages.pot")
	if err := os.Mkdir(filepath.Dir(potPath), 0o755); err != nil {
		t.Fatalf("Mkdir PO: %v", err)
	}
	if err := os.WriteFile(potPath, []byte("msgid \"Old message\"\nmsgstr \"\"\n"), 0o664); err != nil {
		t.Fatalf("write original POT: %v", err)
	}
	if err := os.Chmod(potPath, 0o664); err != nil {
		t.Fatalf("chmod original POT: %v", err)
	}
	oldTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(potPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	proj := &config.Project{
		Root: root, Name: "exact-go", SourceDirs: []string{root}, POTFile: potPath,
		Keywords: []string{"T"}, Exclude: []string{"excluded.go"},
	}
	if _, err := doExtract(proj); err != nil {
		t.Fatalf("doExtract: %v", err)
	}
	data, err := os.ReadFile(potPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	info, err := os.Stat(potPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !strings.Contains(string(data), "Included message") || strings.Contains(string(data), "Old message") {
		t.Fatalf("unexpected POT content: %s", data)
	}
	if info.Mode().Perm() != 0o664 || info.ModTime().Equal(oldTime) {
		t.Fatalf("semantic update mode=%o mtime=%v", info.Mode().Perm(), info.ModTime())
	}
}
