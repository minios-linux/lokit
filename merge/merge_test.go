package merge

import (
	"testing"

	po "github.com/minios-linux/lokit/pofile"
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
