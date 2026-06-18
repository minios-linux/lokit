package desktop

import (
	"os"
	"path/filepath"
	"testing"

	po "github.com/minios-linux/lokit/internal/format/po"
)

func TestDesktopLocale(t *testing.T) {
	cases := map[string]string{
		"de":    "de",
		"pt-BR": "pt_BR",
		"pt_BR": "pt_BR",
		"zh-CN": "zh_CN",
	}
	for in, want := range cases {
		if got := DesktopLocale(in); got != want {
			t.Errorf("DesktopLocale(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSeedPOFromDesktopInline(t *testing.T) {
	dir := t.TempDir()
	desktopPath := filepath.Join(dir, "share", "applications", "myapp.desktop")
	if err := os.MkdirAll(filepath.Dir(desktopPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `[Desktop Entry]
Name=My Application
Name[de]=Meine Anwendung
Comment=Configure application settings
Comment[de]=Anwendungseinstellungen konfigurieren
Keywords=configure;settings;
`
	if err := os.WriteFile(desktopPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	poFile := po.NewFile()
	poFile.Entries = []*po.Entry{
		{
			References: []string{"share/applications/myapp.desktop:4"},
			MsgID:      "My Application",
			MsgStr:     "Anwendung konfigurieren",
			Flags:      []string{"fuzzy"},
		},
		{
			References: []string{"share/applications/myapp.desktop:13"},
			MsgID:      "Configure application settings",
			MsgStr:     "",
		},
		{
			References: []string{"script.sh:10"},
			MsgID:      "Other string",
			MsgStr:     "Andere",
		},
	}

	n, err := SeedPO(poFile, "de", dir, []string{desktopPath})
	if err != nil {
		t.Fatalf("SeedPO() error = %v", err)
	}
	if n != 2 {
		t.Fatalf("seeded = %d, want 2", n)
	}
	if got := poFile.Entries[0].MsgStr; got != "Meine Anwendung" {
		t.Fatalf("Name msgstr = %q, want Meine Anwendung", got)
	}
	if poFile.Entries[0].IsFuzzy() {
		t.Fatal("Name entry should not be fuzzy after seeding")
	}
	if got := poFile.Entries[1].MsgStr; got != "Anwendungseinstellungen konfigurieren" {
		t.Fatalf("Comment msgstr = %q", got)
	}
	if got := poFile.Entries[2].MsgStr; got != "Andere" {
		t.Fatalf("non-desktop entry changed: %q", got)
	}
}
