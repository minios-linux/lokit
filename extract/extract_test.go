package extract

import (
	"go/parser"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"os"
)

func TestDetectShebang(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()

	write := func(name, content string) string {
		t.Helper()
		p := filepath.Join(tmp, name)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}

	tests := []struct {
		name    string
		path    string
		expects string
	}{
		{name: "shell bash", path: write("script-sh", "#!/bin/bash\necho hi\n"), expects: "Shell"},
		{name: "python env", path: write("script-py", "#!/usr/bin/env python3\nprint('x')\n"), expects: "Python"},
		{name: "perl", path: write("script-pl", "#!/usr/bin/perl\nprint 'x';\n"), expects: "Perl"},
		{name: "ruby", path: write("script-rb", "#!/usr/bin/env ruby\nputs 'x'\n"), expects: "Ruby"},
		{name: "unknown interpreter", path: write("script-unknown", "#!/usr/bin/env node\n"), expects: ""},
		{name: "no shebang", path: write("plain", "echo hi\n"), expects: ""},
		{name: "empty file", path: write("empty", ""), expects: ""},
		{name: "missing file", path: filepath.Join(tmp, "does-not-exist"), expects: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectShebang(tc.path); got != tc.expects {
				t.Fatalf("detectShebang(%q) = %q, want %q", tc.path, got, tc.expects)
			}
		})
	}
}

func TestFilesByLanguageAndDescribeFiles(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()

	goFile := filepath.Join(tmp, "main.go")
	pyFile := filepath.Join(tmp, "tool.py")
	shellFile := filepath.Join(tmp, "script")
	txtFile := filepath.Join(tmp, "readme.txt")

	if err := os.WriteFile(goFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pyFile, []byte("print('ok')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shellFile, []byte("#!/bin/sh\necho ok\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(txtFile, []byte("ignored\n"), 0644); err != nil {
		t.Fatal(err)
	}

	files := []string{goFile, pyFile, shellFile, txtFile}
	byLang := FilesByLanguage(files)

	if len(byLang["Go"]) != 1 || byLang["Go"][0] != goFile {
		t.Fatalf("unexpected Go grouping: %#v", byLang["Go"])
	}
	if len(byLang["Python"]) != 1 || byLang["Python"][0] != pyFile {
		t.Fatalf("unexpected Python grouping: %#v", byLang["Python"])
	}
	if len(byLang["Shell"]) != 1 || byLang["Shell"][0] != shellFile {
		t.Fatalf("unexpected Shell grouping: %#v", byLang["Shell"])
	}
	if _, ok := byLang["Text"]; ok {
		t.Fatalf("unexpected language bucket for text file: %#v", byLang)
	}

	desc := DescribeFiles(files)
	if desc != "1 Go, 1 Python, 1 Shell" {
		t.Fatalf("DescribeFiles() = %q, want %q", desc, "1 Go, 1 Python, 1 Shell")
	}
}

func TestFileLanguageExplicitFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want string
	}{
		{"app.ui", "Glade"},
		{"dialog.glade", "Glade"},
		{"app.desktop", "Desktop"},
		{"action.nemo_action", "Desktop"},
		{"org.example.app.policy", "xml/polkit"},
		{"org.example.app.policy.template", "xml/polkit"},
		{"main.py", "Python"},
		{"main.go", "Go"},
		{"script.sh", "Shell"},
		{"unknown.xyz", ""},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			if got := FileLanguage(tc.path); got != tc.want {
				t.Fatalf("FileLanguage(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestGroupFilesForXgettext(t *testing.T) {
	files := []string{
		"src/main.py",
		"scripts/update",
		"ui/dialog.ui",
		"ui/legacy.glade",
		"share/app.desktop",
		"actions/open.nemo_action",
		"policy/org.example.policy",
		"policy/org.example.policy.template",
	}

	groups := groupFilesForXgettext(files)

	wantRegular := []string{"src/main.py"}
	wantShell := []string{"scripts/update"}
	wantGlade := []string{"ui/dialog.ui", "ui/legacy.glade"}
	wantDesktop := []string{"share/app.desktop", "actions/open.nemo_action"}
	wantPolkit := []string{"policy/org.example.policy", "policy/org.example.policy.template"}

	if !reflect.DeepEqual(groups.regular, wantRegular) {
		t.Fatalf("regular = %#v, want %#v", groups.regular, wantRegular)
	}
	if !reflect.DeepEqual(groups.shell, wantShell) {
		t.Fatalf("shell = %#v, want %#v", groups.shell, wantShell)
	}
	if !reflect.DeepEqual(groups.glade, wantGlade) {
		t.Fatalf("glade = %#v, want %#v", groups.glade, wantGlade)
	}
	if !reflect.DeepEqual(groups.desktop, wantDesktop) {
		t.Fatalf("desktop = %#v, want %#v", groups.desktop, wantDesktop)
	}
	if !reflect.DeepEqual(groups.polkit, wantPolkit) {
		t.Fatalf("polkit = %#v, want %#v", groups.polkit, wantPolkit)
	}
}

func TestFindPolkitITSUsesXDGDataDirs(t *testing.T) {
	tmp := t.TempDir()
	itsPath := filepath.Join(tmp, "polkit-1", "its", "polkit.its")
	if err := os.MkdirAll(filepath.Dir(itsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(itsPath, []byte("<its:rules/>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_DIRS", tmp)

	if got := findPolkitITS(); got != itsPath {
		t.Fatalf("findPolkitITS() = %q, want %q", got, itsPath)
	}
}

func TestFindSourcesPolkitTemplate(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()

	// Create a .policy file and its .policy.template sibling.
	// FindSources should return the template, not the .policy.
	policyFile := filepath.Join(tmp, "org.example.app.policy")
	templateFile := filepath.Join(tmp, "org.example.app.policy.template")
	otherPolicy := filepath.Join(tmp, "org.other.app.policy") // no template

	if err := os.WriteFile(policyFile, []byte("<policyconfig/>"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(templateFile, []byte("<policyconfig/>"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPolicy, []byte("<policyconfig/>"), 0644); err != nil {
		t.Fatal(err)
	}

	sources, err := FindSources([]string{tmp})
	if err != nil {
		t.Fatalf("FindSources: %v", err)
	}

	byBase := make(map[string]bool)
	for _, f := range sources {
		byBase[filepath.Base(f)] = true
	}

	// Template should be present
	if !byBase["org.example.app.policy.template"] {
		t.Errorf("expected .policy.template in sources, got: %v", sources)
	}
	// Plain .policy with a template should NOT be present (replaced by template)
	if byBase["org.example.app.policy"] {
		t.Errorf("plain .policy should be skipped when .policy.template exists, got: %v", sources)
	}
	// .policy without template should be present
	if !byBase["org.other.app.policy"] {
		t.Errorf("expected .policy without template in sources, got: %v", sources)
	}
}

func TestCommonAncestor(t *testing.T) {
	t.Parallel()

	if got := commonAncestor(nil); got != "." {
		t.Fatalf("commonAncestor(nil) = %q, want .", got)
	}

	tmp := t.TempDir()
	a := filepath.Join(tmp, "a", "b", "c")
	b := filepath.Join(tmp, "a", "b", "d", "e")

	got := commonAncestor([]string{a, b})
	want := filepath.Join(tmp, "a", "b")
	if got != want {
		t.Fatalf("commonAncestor mismatch: got %q want %q", got, want)
	}

	single := filepath.Join(tmp, "only")
	got = commonAncestor([]string{single})
	want, err := filepath.Abs(single)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if got != want {
		t.Fatalf("single path ancestor = %q, want %q", got, want)
	}
}

func TestParseGoKeyword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		spec string
		want GoKeyword
	}{
		{spec: "T", want: GoKeyword{FuncName: "T", MsgIDArg: 1}},
		{spec: "N:2,3", want: GoKeyword{FuncName: "N", MsgIDArg: 2, PluralArg: 3}},
		{spec: "pgettext:1c,2", want: GoKeyword{FuncName: "pgettext", MsgIDArg: 2, ContextArg: 1}},
		{spec: "pkg.Tr:2,3", want: GoKeyword{FuncName: "pkg.Tr", MsgIDArg: 2, PluralArg: 3}},
	}

	for _, tc := range tests {
		t.Run(tc.spec, func(t *testing.T) {
			if got := ParseGoKeyword(tc.spec); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseGoKeyword(%q) = %#v, want %#v", tc.spec, got, tc.want)
			}
		})
	}
}

func TestStringFromExprAndPotQuote(t *testing.T) {
	t.Parallel()

	expr, err := parser.ParseExpr(`"hello" + " " + "world"`)
	if err != nil {
		t.Fatalf("ParseExpr: %v", err)
	}
	if got := stringFromExpr(expr); got != "hello world" {
		t.Fatalf("stringFromExpr concat = %q, want %q", got, "hello world")
	}

	notString, err := parser.ParseExpr("someVar")
	if err != nil {
		t.Fatalf("ParseExpr: %v", err)
	}
	if got := stringFromExpr(notString); got != "" {
		t.Fatalf("stringFromExpr(non-literal) = %q, want empty", got)
	}

	if got := potQuote(`a\"b` + "\t" + `c`); got != `"a\\\"b\tc"` {
		t.Fatalf("potQuote escape mismatch: got %q", got)
	}

	multi := potQuote("a\nb\n")
	if !strings.Contains(multi, "\n\"a\\n\"\n\"b\\n\"") {
		t.Fatalf("potQuote multiline output unexpected: %q", multi)
	}
}

func TestShouldSkipPath(t *testing.T) {
	skip := []string{
		"debian/myapp/DEBIAN/control",
		"debian/myapp/usr/bin/app",
		"debian/myapp.debhelper/generated",
		"po/de.mo",
		"build/output.py",
		"node_modules/pkg/index.js",
	}
	for _, p := range skip {
		if !shouldSkipPath(p) {
			t.Errorf("shouldSkipPath(%q) = false, want true", p)
		}
	}

	keep := []string{
		"share/applications/myapp.desktop",
		"debian/rules",
		"po/de.po",
		"src/main.py",
		".",
		"bin/myapp",
	}
	for _, p := range keep {
		if shouldSkipPath(p) {
			t.Errorf("shouldSkipPath(%q) = true, want false", p)
		}
	}
}

func TestFindSourcesSkipsBuildDirButKeepsOtherSources(t *testing.T) {
	dir := t.TempDir()
	py := filepath.Join(dir, "lib", "app.py")
	if err := os.MkdirAll(filepath.Dir(py), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(py, []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buildPy := filepath.Join(dir, "build", "generated.py")
	if err := os.MkdirAll(filepath.Dir(buildPy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(buildPy, []byte("x = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := FindSources([]string{dir})
	if err != nil {
		t.Fatalf("FindSources() error = %v", err)
	}
	if len(files) != 1 || files[0] != py {
		t.Fatalf("FindSources() = %v, want only %s", files, py)
	}
}
