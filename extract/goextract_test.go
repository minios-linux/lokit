package extract

import (
	"go/ast"
	"go/parser"
	"go/token"
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

func TestRunGoExtractIncludesTranslationEngineLogs(t *testing.T) {
	root := filepath.Clean("..")
	potPath := filepath.Join(t.TempDir(), "lokit.pot")
	if _, err := RunGoExtract([]string{filepath.Join(root, "translate")}, potPath, "lokit", []string{"T"}, root); err != nil {
		t.Fatalf("RunGoExtract: %v", err)
	}

	potPO, err := po.ParseFile(potPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	extracted := make(map[string]bool, len(potPO.Entries))
	for _, entry := range potPO.Entries {
		extracted[entry.MsgID] = true
	}

	for _, msgid := range []string{
		"Translating %s (%s) — %d keys...",
		"  Chunk %d/%d (%d entries)",
		"Saved %s (%d/%d translated)",
		"Error translating %s: %v",
	} {
		if !extracted[msgid] {
			t.Errorf("translation engine log %q was not extracted", msgid)
		}
	}
}

func TestTranslationEngineLogsUseGettext(t *testing.T) {
	translateDir := filepath.Join("..", "translate")
	entries, err := os.ReadDir(translateDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(translateDir, entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("ParseFile(%s): %v", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "log" && sel.Sel.Name != "logError" && sel.Sel.Name != "Printf") {
				return true
			}
			if len(call.Args) == 0 || !isGettextCall(call.Args[0]) {
				t.Errorf("%s: %s format is not wrapped with i18n.T", path, sel.Sel.Name)
			}
			checked++
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no translation engine log calls checked")
	}
}

func isGettextCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "T" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "i18n"
}
