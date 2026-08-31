package desktop

import (
	"bytes"
	"testing"
)

func TestDesktopParseSetMarshal(t *testing.T) {
	input := []byte(`[Desktop Entry]
Name=My App
Comment=Simple app
Name[de]=Meine App
`)

	f, err := Parse(input, "ru")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(f.Keys()) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(f.Keys()))
	}
	if got, ok := f.Get("Name"); !ok || got != "" {
		t.Fatalf("Get(Name) before Set = %q, %v", got, ok)
	}
	if got, ok := f.Get("Unknown"); ok || got != "" {
		t.Fatalf("Get(Unknown) = %q, %v", got, ok)
	}
	if !f.Set("Name", "Мое приложение") {
		t.Fatalf("Set(Name) failed")
	}
	if !f.Set("Comment", "Простое приложение") {
		t.Fatalf("Set(Comment) failed")
	}
	if got, ok := f.Get("Name"); !ok || got != "Мое приложение" {
		t.Fatalf("Get(Name) after Set = %q, %v", got, ok)
	}

	out, err := f.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	f2, err := Parse(out, "ru")
	if err != nil {
		t.Fatalf("Parse(Marshal()) error = %v", err)
	}
	if got := f2.localized["Name"]; got != "Мое приложение" {
		t.Fatalf("name mismatch: got %q", got)
	}
	if got := f2.localized["Comment"]; got != "Простое приложение" {
		t.Fatalf("comment mismatch: got %q", got)
	}
	if bytes.HasSuffix(out, []byte("\n\n")) {
		t.Fatalf("Marshal() added a blank line at EOF: %q", out)
	}
}

func TestDesktopMarshalDoesNotDuplicateExistingLocale(t *testing.T) {
	input := []byte(`[Desktop Entry]
Name=MiniOS Module Manager
Name[pt]=Gestor de módulos MiniOS
Name[de]=Alter Modulmanager
Name[ru]=Менеджер модулей MiniOS
Name[de]=MiniOS-Modulmanager
Comment=Manage modules
Comment[de]=Module verwalten
Comment[de]=MiniOS-Module verwalten
`)

	f, err := Parse(input, "de")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	out, err := f.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, field := range []string{"Name[de]=", "Comment[de]="} {
		if count := bytes.Count(out, []byte(field)); count != 1 {
			t.Fatalf("Marshal() wrote %q %d times:\n%s", field, count, out)
		}
	}
	if !bytes.Contains(out, []byte("Name[de]=MiniOS-Modulmanager\n")) ||
		!bytes.Contains(out, []byte("Comment[de]=MiniOS-Module verwalten\n")) {
		t.Fatalf("Marshal() did not retain the last localized values:\n%s", out)
	}

	f, err = Parse(out, "de")
	if err != nil {
		t.Fatalf("Parse(Marshal()) error = %v", err)
	}
	second, err := f.Marshal()
	if err != nil {
		t.Fatalf("second Marshal() error = %v", err)
	}
	if !bytes.Equal(out, second) {
		t.Fatalf("Marshal() is not idempotent:\nfirst:\n%s\nsecond:\n%s", out, second)
	}
}

func TestDesktopMarshalScopesLocalizedKeysByGroup(t *testing.T) {
	input := []byte(`[Desktop Entry]
Name=Application
Name[de]=Alte Anwendung
Name[de]=Anwendung

[Desktop Action Open]
Name=Open
Name[de]=Öffnen
`)

	f, err := Parse(input, "de")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got := f.Keys(); len(got) != 2 || got[0] != "Name" || got[1] != "[Desktop Action Open].Name" {
		t.Fatalf("Keys() = %v", got)
	}
	out, err := f.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if bytes.Count(out, []byte("Name[de]=")) != 2 ||
		!bytes.Contains(out, []byte("Name[de]=Anwendung\n")) ||
		!bytes.Contains(out, []byte("Name[de]=Öffnen\n")) {
		t.Fatalf("Marshal() did not preserve group-scoped translations:\n%s", out)
	}
}
