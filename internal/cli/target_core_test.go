package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minios-linux/lokit/config"
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
