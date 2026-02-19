// Package translate contains tests for the translation engine.
package translate

import (
	"encoding/json"
	"testing"

	po "github.com/minios-linux/lokit/pofile"
)

// ---------------------------------------------------------------------------
// npluralsFromFile
// ---------------------------------------------------------------------------

func TestNpluralsFromFile_FromHeader(t *testing.T) {
	f := po.NewFile()
	f.Header.MsgStr = "Plural-Forms: nplurals=3; plural=(n%10==1 ? 0 : n%10>=2 && n%10<=4 ? 1 : 2);\n"

	n := npluralsFromFile(f, "ru")
	if n != 3 {
		t.Errorf("got %d, want 3", n)
	}
}

func TestNpluralsFromFile_FallbackToLang(t *testing.T) {
	f := po.NewFile()
	// No Plural-Forms header — should fall back to language default

	n := npluralsFromFile(f, "ru")
	if n != 3 {
		t.Errorf("got %d, want 3 for Russian", n)
	}

	n2 := npluralsFromFile(f, "en")
	if n2 != 2 {
		t.Errorf("got %d, want 2 for English", n2)
	}

	n3 := npluralsFromFile(f, "ja")
	if n3 != 1 {
		t.Errorf("got %d, want 1 for Japanese", n3)
	}
}

// ---------------------------------------------------------------------------
// parsePluralTranslations
// ---------------------------------------------------------------------------

func TestParsePluralTranslations_SingularEntries(t *testing.T) {
	entries := []*po.Entry{
		{MsgID: "Save"},
		{MsgID: "Cancel"},
	}

	raw := `["Сохранить", "Отмена"]`
	result, err := parsePluralTranslations(raw, entries, 3)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("got %d results, want 2", len(result))
	}
	if result[0].singular != "Сохранить" {
		t.Errorf("result[0].singular = %q, want Сохранить", result[0].singular)
	}
	if result[1].singular != "Отмена" {
		t.Errorf("result[1].singular = %q, want Отмена", result[1].singular)
	}
	if result[0].plural != nil {
		t.Error("result[0].plural should be nil for singular entry")
	}
}

func TestParsePluralTranslations_PluralEntries(t *testing.T) {
	entries := []*po.Entry{
		{MsgID: "%d file", MsgIDPlural: "%d files"},
	}
	// AI returns array of 3 forms for Russian
	raw := `[["файл", "файла", "файлов"]]`

	result, err := parsePluralTranslations(raw, entries, 3)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("got %d results, want 1", len(result))
	}
	if result[0].plural == nil || len(result[0].plural) != 3 {
		t.Fatalf("plural forms: got %v", result[0].plural)
	}
	if result[0].plural[0] != "файл" || result[0].plural[1] != "файла" || result[0].plural[2] != "файлов" {
		t.Errorf("plural forms incorrect: %v", result[0].plural)
	}
}

func TestParsePluralTranslations_MixedEntries(t *testing.T) {
	entries := []*po.Entry{
		{MsgID: "Save"},
		{MsgID: "%d file", MsgIDPlural: "%d files"},
		{MsgID: "Cancel"},
	}
	raw := `["Сохранить", ["%d файл", "%d файла", "%d файлов"], "Отмена"]`

	result, err := parsePluralTranslations(raw, entries, 3)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("got %d results, want 3", len(result))
	}
	if result[0].singular != "Сохранить" {
		t.Errorf("[0] singular = %q", result[0].singular)
	}
	if result[1].plural == nil || len(result[1].plural) != 3 {
		t.Errorf("[1] plural = %v", result[1].plural)
	}
	if result[2].singular != "Отмена" {
		t.Errorf("[2] singular = %q", result[2].singular)
	}
}

func TestParsePluralTranslations_AIReturnedStringForPlural(t *testing.T) {
	// AI sometimes returns a plain string instead of an array for a plural entry.
	// We should duplicate it across all forms.
	entries := []*po.Entry{
		{MsgID: "%d item", MsgIDPlural: "%d items"},
	}
	raw := `["%d предметов"]` // plain string, not array

	result, err := parsePluralTranslations(raw, entries, 2)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result[0].plural == nil || len(result[0].plural) != 2 {
		t.Fatalf("expected 2 plural forms, got %v", result[0].plural)
	}
	for i, f := range result[0].plural {
		if f != "%d предметов" {
			t.Errorf("form[%d] = %q, want %%d предметов", i, f)
		}
	}
}

func TestParsePluralTranslations_PadShortPluralArray(t *testing.T) {
	// AI returns only 1 form but we need 3 — should pad by duplicating last.
	entries := []*po.Entry{
		{MsgID: "%d item", MsgIDPlural: "%d items"},
	}
	raw := `[["%d предмет"]]`

	result, err := parsePluralTranslations(raw, entries, 3)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result[0].plural) != 3 {
		t.Fatalf("expected 3 plural forms after padding, got %d", len(result[0].plural))
	}
	for i, f := range result[0].plural {
		if f != "%d предмет" {
			t.Errorf("form[%d] = %q", i, f)
		}
	}
}

// ---------------------------------------------------------------------------
// applyPluralTranslations
// ---------------------------------------------------------------------------

func TestApplyPluralTranslations_Singular(t *testing.T) {
	e := &po.Entry{MsgID: "Save"}
	translations := []pluralTranslation{{singular: "Сохранить"}}

	applyPluralTranslations([]*po.Entry{e}, translations, false)

	if e.MsgStr != "Сохранить" {
		t.Errorf("MsgStr = %q, want Сохранить", e.MsgStr)
	}
}

func TestApplyPluralTranslations_Plural(t *testing.T) {
	e := &po.Entry{
		MsgID:        "%d file",
		MsgIDPlural:  "%d files",
		MsgStrPlural: make(map[int]string),
	}
	translations := []pluralTranslation{
		{plural: []string{"%d файл", "%d файла", "%d файлов"}},
	}

	applyPluralTranslations([]*po.Entry{e}, translations, false)

	if e.MsgStrPlural[0] != "%d файл" {
		t.Errorf("MsgStrPlural[0] = %q", e.MsgStrPlural[0])
	}
	if e.MsgStrPlural[1] != "%d файла" {
		t.Errorf("MsgStrPlural[1] = %q", e.MsgStrPlural[1])
	}
	if e.MsgStrPlural[2] != "%d файлов" {
		t.Errorf("MsgStrPlural[2] = %q", e.MsgStrPlural[2])
	}
	// MsgStr should not be touched for plural entries
	if e.MsgStr != "" {
		t.Errorf("MsgStr should remain empty for plural entry, got %q", e.MsgStr)
	}
}

func TestApplyPluralTranslations_ClearsFuzzy(t *testing.T) {
	e := &po.Entry{MsgID: "Save", Flags: []string{"fuzzy"}}
	translations := []pluralTranslation{{singular: "Сохранить"}}

	applyPluralTranslations([]*po.Entry{e}, translations, true)

	if e.IsFuzzy() {
		t.Error("fuzzy flag should have been cleared")
	}
}

func TestApplyPluralTranslations_PreservesFuzzyWhenNotClearing(t *testing.T) {
	e := &po.Entry{MsgID: "Save", Flags: []string{"fuzzy"}}
	translations := []pluralTranslation{{singular: "Сохранить"}}

	applyPluralTranslations([]*po.Entry{e}, translations, false)

	if !e.IsFuzzy() {
		t.Error("fuzzy flag should have been preserved")
	}
}

// ---------------------------------------------------------------------------
// hasPluralEntries
// ---------------------------------------------------------------------------

func TestHasPluralEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries []*po.Entry
		want    bool
	}{
		{"empty", []*po.Entry{}, false},
		{"all singular", []*po.Entry{{MsgID: "A"}, {MsgID: "B"}}, false},
		{"one plural", []*po.Entry{{MsgID: "A"}, {MsgID: "%d item", MsgIDPlural: "%d items"}}, true},
		{"all plural", []*po.Entry{{MsgID: "A", MsgIDPlural: "As"}, {MsgID: "B", MsgIDPlural: "Bs"}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := hasPluralEntries(tc.entries)
			if got != tc.want {
				t.Errorf("hasPluralEntries = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// JSON helpers (ensure parsePluralTranslations handles markdown blocks)
// ---------------------------------------------------------------------------

func TestParsePluralTranslations_StripsMarkdownCodeBlock(t *testing.T) {
	entries := []*po.Entry{{MsgID: "Hello"}}
	raw := "```json\n[\"Привет\"]\n```"

	result, err := parsePluralTranslations(raw, entries, 2)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result[0].singular != "Привет" {
		t.Errorf("singular = %q", result[0].singular)
	}
}

// Ensure json.RawMessage can handle both strings and arrays (sanity check).
func TestJSONRawMessage_Mixed(t *testing.T) {
	raw := `["str", ["a", "b", "c"], "another"]`
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items", len(items))
	}
	var s string
	if err := json.Unmarshal(items[0], &s); err != nil || s != "str" {
		t.Errorf("items[0]: %q err=%v", s, err)
	}
	var arr []string
	if err := json.Unmarshal(items[1], &arr); err != nil || len(arr) != 3 {
		t.Errorf("items[1]: %v err=%v", arr, err)
	}
}
