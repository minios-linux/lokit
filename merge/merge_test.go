package merge

import (
	"testing"

	po "github.com/minios-linux/lokit/internal/format/po"
)

func TestMergeKeepNewObsoleteAndHeaderUpdate(t *testing.T) {
	poFile := po.NewFile()
	poFile.Header.MsgStr = "Project-Id-Version: lokit 1\nPOT-Creation-Date: old\nLanguage: ru\n"
	poFile.Entries = []*po.Entry{
		{
			MsgID:      "keep",
			MsgStr:     "keep-translation",
			Flags:      []string{"fuzzy", "c-format"},
			References: []string{"old.go:1"},
		},
		{MsgID: "obsolete", MsgStr: "obsolete-translation", References: []string{"unused.go:1"}},
		{MsgID: "already-obsolete", MsgStr: "x", Obsolete: true},
	}

	potFile := po.NewFile()
	potFile.Header.MsgStr = "POT-Creation-Date: new\n"
	potFile.Entries = []*po.Entry{
		{
			MsgID:             "keep",
			MsgIDPlural:       "keep plural",
			ExtractedComments: []string{"auto"},
			References:        []string{"new.go:10"},
			Flags:             []string{"python-format"},
		},
		{MsgID: "new", MsgIDPlural: "new plural", Flags: []string{"java-format"}},
	}

	merged := Merge(poFile, potFile)

	if got := merged.HeaderField("POT-Creation-Date"); got != "new" {
		t.Fatalf("POT-Creation-Date = %q, want new", got)
	}
	if got := merged.HeaderField("Language"); got != "ru" {
		t.Fatalf("Language header lost: got %q", got)
	}

	if len(merged.Entries) != 3 {
		t.Fatalf("entries len = %d, want 3", len(merged.Entries))
	}

	keep := merged.Entries[0]
	if keep.MsgID != "keep" {
		t.Fatalf("first entry msgid = %q, want keep", keep.MsgID)
	}
	if keep.MsgStr != "keep-translation" {
		t.Fatalf("keep translation = %q, want keep-translation", keep.MsgStr)
	}
	if !keep.IsFuzzy() {
		t.Fatal("keep entry should retain fuzzy flag")
	}
	if !keep.HasFlag("python-format") {
		t.Fatal("keep entry should include template format flag")
	}
	if len(keep.ExtractedComments) != 1 || keep.ExtractedComments[0] != "auto" {
		t.Fatalf("keep extracted comments = %v, want [auto]", keep.ExtractedComments)
	}
	if len(keep.References) != 1 || keep.References[0] != "new.go:10" {
		t.Fatalf("keep references = %v, want [new.go:10]", keep.References)
	}

	newEntry := merged.Entries[1]
	if newEntry.MsgID != "new" {
		t.Fatalf("second entry msgid = %q, want new", newEntry.MsgID)
	}
	if newEntry.MsgStr != "" {
		t.Fatalf("new entry msgstr = %q, want empty", newEntry.MsgStr)
	}
	if newEntry.MsgStrPlural == nil {
		t.Fatal("new entry plural map should be initialized")
	}

	obsolete := merged.Entries[2]
	if obsolete.MsgID != "obsolete" || !obsolete.Obsolete {
		t.Fatalf("third entry should be obsolete copy, got msgid=%q obsolete=%v", obsolete.MsgID, obsolete.Obsolete)
	}
	if obsolete.References != nil {
		t.Fatalf("obsolete references should be cleared, got %v", obsolete.References)
	}
}

func TestMergeFlagsKeepsFuzzyFirst(t *testing.T) {
	flags := mergeFlags([]string{"fuzzy", "c-format"}, []string{"python-format"})
	if len(flags) == 0 || flags[0] != "fuzzy" {
		t.Fatalf("flags = %v, want fuzzy first", flags)
	}
}

func TestMergeRestoresObsoleteTranslation(t *testing.T) {
	poFile := po.NewFile()
	poFile.Entries = []*po.Entry{
		{MsgID: "gone", MsgStr: "old-translation", Obsolete: true},
	}

	potFile := po.NewFile()
	potFile.Entries = []*po.Entry{
		{MsgID: "gone", References: []string{"app.sh:1"}},
	}

	merged := Merge(poFile, potFile)
	if len(merged.Entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(merged.Entries))
	}
	entry := merged.Entries[0]
	if entry.Obsolete {
		t.Fatal("restored entry should not be obsolete")
	}
	if entry.MsgStr != "old-translation" {
		t.Fatalf("msgstr = %q, want old-translation", entry.MsgStr)
	}
	if len(entry.References) != 1 || entry.References[0] != "app.sh:1" {
		t.Fatalf("references = %v", entry.References)
	}
}

// TestMergeMsgCtxtCollision verifies that two entries sharing the same msgid
// but with different msgctxt values are treated as distinct entries throughout
// the merge lifecycle (poKey must include msgctxt in its compound key).
func TestMergeMsgCtxtCollision(t *testing.T) {
	// PO has two translations: "Save" with two different contexts.
	poFile := po.NewFile()
	poFile.Entries = []*po.Entry{
		{MsgID: "Save", MsgCtxt: "menu", MsgStr: "Speichern (Menü)"},
		{MsgID: "Save", MsgCtxt: "button", MsgStr: "Speichern (Knopf)"},
	}

	// POT keeps both contexts but adds a new one; also has "Save" without context.
	potFile := po.NewFile()
	potFile.Entries = []*po.Entry{
		{MsgID: "Save", MsgCtxt: "menu"},
		{MsgID: "Save", MsgCtxt: "button"},
		{MsgID: "Save", MsgCtxt: "tooltip"},
		{MsgID: "Save"}, // no context — distinct entry
	}

	merged := Merge(poFile, potFile)

	// Expect 4 entries: menu, button (translated), tooltip + no-context (new, empty).
	if len(merged.Entries) != 4 {
		t.Fatalf("entries len = %d, want 4", len(merged.Entries))
	}

	byCtxt := make(map[string]*po.Entry, len(merged.Entries))
	for _, e := range merged.Entries {
		byCtxt[e.MsgCtxt] = e
	}

	if got := byCtxt["menu"].MsgStr; got != "Speichern (Menü)" {
		t.Errorf("menu MsgStr = %q, want Speichern (Menü)", got)
	}
	if got := byCtxt["button"].MsgStr; got != "Speichern (Knopf)" {
		t.Errorf("button MsgStr = %q, want Speichern (Knopf)", got)
	}
	if got := byCtxt["tooltip"].MsgStr; got != "" {
		t.Errorf("tooltip MsgStr = %q, want empty (new entry)", got)
	}
	if got := byCtxt[""].MsgStr; got != "" {
		t.Errorf("no-context MsgStr = %q, want empty (new entry)", got)
	}
}

// TestMergeMsgCtxtObsoleteRestore verifies that an obsolete entry is restored
// to the correct context — not conflated with an entry of the same msgid but
// a different msgctxt.
func TestMergeMsgCtxtObsoleteRestore(t *testing.T) {
	poFile := po.NewFile()
	poFile.Entries = []*po.Entry{
		// "Open" with context "menu" was removed (obsolete) but had a translation.
		{MsgID: "Open", MsgCtxt: "menu", MsgStr: "Öffnen (Menü)", Obsolete: true},
		// "Open" with context "button" is active and has its own translation.
		{MsgID: "Open", MsgCtxt: "button", MsgStr: "Öffnen (Knopf)"},
	}

	// POT brings back "Open" (menu) and keeps "Open" (button).
	potFile := po.NewFile()
	potFile.Entries = []*po.Entry{
		{MsgID: "Open", MsgCtxt: "menu", References: []string{"menu.go:1"}},
		{MsgID: "Open", MsgCtxt: "button", References: []string{"btn.go:1"}},
	}

	merged := Merge(poFile, potFile)
	if len(merged.Entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(merged.Entries))
	}

	byCtxt := make(map[string]*po.Entry, len(merged.Entries))
	for _, e := range merged.Entries {
		byCtxt[e.MsgCtxt] = e
	}

	if byCtxt["menu"].MsgStr != "Öffnen (Menü)" {
		t.Errorf("menu restore: MsgStr = %q, want Öffnen (Menü)", byCtxt["menu"].MsgStr)
	}
	if byCtxt["button"].MsgStr != "Öffnen (Knopf)" {
		t.Errorf("button active: MsgStr = %q, want Öffnen (Knopf)", byCtxt["button"].MsgStr)
	}
	if byCtxt["menu"].Obsolete || byCtxt["button"].Obsolete {
		t.Error("neither entry should be obsolete after merge")
	}
}
