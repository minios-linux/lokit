package po

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClearTranslationsForPOTPreservesPluralSlots(t *testing.T) {
	f := NewFile()
	f.Entries = []*Entry{{MsgID: "one panel", MsgIDPlural: "many panels",
		MsgStrPlural: map[int]string{0: "ein Panel", 1: "mehrere Panels"}}}
	f.ClearTranslationsForPOT()
	for index := 0; index < 2; index++ {
		value, present := f.Entries[0].MsgStrPlural[index]
		if !present || value != "" {
			t.Fatalf("plural form %d lost or not cleared: %#v", index, f.Entries[0])
		}
	}
	path := filepath.Join(t.TempDir(), "messages.pot")
	if err := f.WriteFile(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `msgstr[0] ""`) || !strings.Contains(string(data), `msgstr[1] ""`) {
		t.Fatalf("invalid plural POT: %s", data)
	}
	if msgcat, err := exec.LookPath("msgcat"); err == nil {
		if output, err := exec.Command(msgcat, "-o", os.DevNull, path).CombinedOutput(); err != nil {
			t.Fatalf("gettext rejected POT: %v: %s", err, output)
		}
	}
}
