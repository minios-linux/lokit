package cli

import (
	"os"
	"path/filepath"
	"testing"
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
			if got := po4aMarkdownDir(configDir); got != docsDir {
				t.Fatalf("po4aMarkdownDir(%q) = %q, want %q", configDir, got, docsDir)
			}
		})
	}
}
