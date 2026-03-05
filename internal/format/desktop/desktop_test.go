package desktop

import "testing"

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
	if !f.Set("Name", "Мое приложение") {
		t.Fatalf("Set(Name) failed")
	}
	if !f.Set("Comment", "Простое приложение") {
		t.Fatalf("Set(Comment) failed")
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
}
