package extract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopTemplates(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"settings.desktop.in", "helper.desktop.in.in"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("[Desktop Entry]\nName=Display\nComment=Configure display\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := FindSources([]string{dir})
	if err != nil || len(files) != 2 {
		t.Fatalf("templates not discovered: %v: %v", files, err)
	}
	files, err = FindSources([]string{dir, dir})
	if err != nil || len(files) != 2 {
		t.Fatalf("templates not deduplicated: %v: %v", files, err)
	}
	for _, path := range files {
		if FileLanguage(path) != "Desktop" {
			t.Fatalf("wrong template language: %s", path)
		}
	}
	groups := groupFilesForXgettext(files, dir)
	if len(groups.desktop) != 2 || len(groups.regular) != 0 {
		t.Fatalf("templates not in Desktop pass: %#v", groups)
	}
	if _, err := exec.LookPath("xgettext"); err != nil {
		t.Skip("xgettext unavailable")
	}
	pot := filepath.Join(dir, "messages.pot")
	if _, err := RunXgettext(files, pot, "test", "1.0", "", nil, dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(pot)
	if err != nil || !strings.Contains(string(data), `msgid "Configure display"`) {
		t.Fatalf("template strings not extracted: %s: %v", data, err)
	}
}
