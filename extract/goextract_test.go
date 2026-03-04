package extract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	po "github.com/minios-linux/lokit/internal/format/po"
)

func TestRunGoExtractUsesRelativeReferences(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "internal", "cli")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	goFile := filepath.Join(srcDir, "sample.go")
	code := "package cli\nfunc f(){T(\"hello\")}\n"
	if err := os.WriteFile(goFile, []byte(code), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	potPath := filepath.Join(root, "po", "lokit.pot")
	if _, err := RunGoExtract([]string{srcDir}, potPath, "lokit", []string{"T"}, root); err != nil {
		t.Fatalf("RunGoExtract: %v", err)
	}

	potPO, err := po.ParseFile(potPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if len(potPO.Entries) == 0 || len(potPO.Entries[0].References) == 0 {
		t.Fatal("expected extracted reference")
	}

	ref := potPO.Entries[0].References[0]
	if strings.HasPrefix(ref, root) {
		t.Fatalf("reference is absolute: %q", ref)
	}
	if !strings.HasPrefix(ref, "internal/cli/sample.go:") {
		t.Fatalf("reference is not relative to root: %q", ref)
	}
}
