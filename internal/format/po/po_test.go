package po

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestClearTranslationsForPOT(t *testing.T) {
	input := `msgid ""
msgstr ""
"Language: en\n"

msgid "hello"
msgstr "translated"
`
	f, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	f.ClearTranslationsForPOT()
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, `msgstr "translated"`) {
		t.Fatalf("POT still has translation: %s", out)
	}
	if !strings.Contains(out, `msgstr ""`) {
		t.Fatalf("expected empty msgstr for entry: %s", out)
	}
}

func TestParseWriteRoundTripAndHeaderFields(t *testing.T) {
	input := `msgid ""
msgstr ""
"Project-Id-Version: lokit 1.0\n"
"Language: ru\n"

#. extracted comment
#: app.go:12
msgid "hello"
msgstr "privet"

#, fuzzy
#| msgid "old count"
msgid "count"
msgid_plural "counts"
msgstr[0] "odin"
msgstr[1] "mnogo"
`

	f, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if got := f.HeaderField("language"); got != "ru" {
		t.Fatalf("HeaderField(language) = %q, want ru", got)
	}
	f.SetHeaderField("Language", "de")
	f.SetHeaderField("Plural-Forms", PluralFormsForLang("de"))
	if got := f.HeaderField("Language"); got != "de" {
		t.Fatalf("Language header after SetHeaderField = %q, want de", got)
	}
	if got := f.HeaderField("Plural-Forms"); got == "" {
		t.Fatal("Plural-Forms should be set")
	}

	if len(f.Entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(f.Entries))
	}
	plural := f.EntryByMsgID("count")
	if plural == nil {
		t.Fatal("count entry not found")
	}
	if plural.PreviousMsgID != "old count" {
		t.Fatalf("PreviousMsgID = %q, want old count", plural.PreviousMsgID)
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	round, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Parse roundtrip error: %v", err)
	}

	if round.HeaderField("Language") != "de" {
		t.Fatalf("roundtrip Language = %q, want de", round.HeaderField("Language"))
	}
	if got := round.EntryByMsgID("hello"); got == nil || got.MsgStr != "privet" {
		t.Fatalf("roundtrip hello entry mismatch: %#v", got)
	}
	roundPlural := round.EntryByMsgID("count")
	if roundPlural == nil {
		t.Fatal("roundtrip plural entry missing")
	}
	if !reflect.DeepEqual(roundPlural.MsgStrPlural, map[int]string{0: "odin", 1: "mnogo"}) {
		t.Fatalf("roundtrip plural forms = %v", roundPlural.MsgStrPlural)
	}
}

func TestStatsFuzzyAndUntranslated(t *testing.T) {
	f := NewFile()
	f.Entries = []*Entry{
		{MsgID: "t1", MsgStr: "translated"},
		{MsgID: "f1", MsgStr: "draft", Flags: []string{"fuzzy"}},
		{MsgID: "u1", MsgStr: ""},
		{MsgID: "p1", MsgIDPlural: "p1s", MsgStrPlural: map[int]string{0: "one", 1: "many"}},
		{MsgID: "p2", MsgIDPlural: "p2s", MsgStrPlural: map[int]string{0: "only one", 1: ""}},
		{MsgID: "old", MsgStr: "x", Obsolete: true},
	}

	total, translated, fuzzy, untranslated := f.Stats()
	if total != 5 || translated != 2 || fuzzy != 1 || untranslated != 2 {
		t.Fatalf("Stats = total=%d translated=%d fuzzy=%d untranslated=%d", total, translated, fuzzy, untranslated)
	}

	if len(f.FuzzyEntries()) != 1 {
		t.Fatalf("FuzzyEntries len = %d, want 1", len(f.FuzzyEntries()))
	}
	if len(f.UntranslatedEntries()) != 2 {
		t.Fatalf("UntranslatedEntries len = %d, want 2", len(f.UntranslatedEntries()))
	}
}

func TestPluralFormsAndLangNameHelpers(t *testing.T) {
	pluralCases := []struct {
		lang string
		want string
	}{
		{lang: "ru", want: "nplurals=3; plural=(n%10==1 && n%100!=11 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2);"},
		{lang: "pt-BR", want: "nplurals=2; plural=(n > 1);"},
		{lang: "ja", want: "nplurals=1; plural=0;"},
		{lang: "zz", want: "nplurals=2; plural=(n != 1);"},
	}
	for _, tc := range pluralCases {
		if got := PluralFormsForLang(tc.lang); got != tc.want {
			t.Fatalf("PluralFormsForLang(%q) = %q, want %q", tc.lang, got, tc.want)
		}
	}

	if got := LangNameNative("pt_br"); got == "pt_br" {
		t.Fatalf("LangNameNative(pt_br) should resolve known language, got %q", got)
	}
	if got := LangNameNative("zz-ZZ"); got != "zz-ZZ" {
		t.Fatalf("LangNameNative(zz-ZZ) = %q, want passthrough", got)
	}
}

func TestWriteObsoleteMultilineMsgstr(t *testing.T) {
	f := NewFile()
	f.Entries = []*Entry{
		{
			Obsolete: true,
			MsgID:    " is not yet installed.\nDo you like to download and install it now?",
			MsgStr:   " ещё не установлен.\nХотите скачать и установить его сейчас?",
		},
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	out := buf.String()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "\"") && !strings.HasPrefix(line, "#~ ") {
			t.Fatalf("continuation line missing #~ prefix: %q", line)
		}
	}

	round, err := Parse(strings.NewReader(out))
	if err != nil {
		t.Fatalf("Parse(Write()) error = %v", err)
	}
	if len(round.Entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(round.Entries))
	}
	if got := round.Entries[0].MsgStr; got != f.Entries[0].MsgStr {
		t.Fatalf("msgstr round-trip = %q, want %q", got, f.Entries[0].MsgStr)
	}
}

func TestWriteEmptyTranslatorCommentWithoutTrailingSpace(t *testing.T) {
	f := NewFile()
	f.Entries = []*Entry{{
		TranslatorComments: []string{""},
		MsgID:              "hello",
		MsgStr:             "hallo",
	}}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if strings.Contains(buf.String(), "# \n") {
		t.Fatalf("empty translator comment has trailing space: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "#\n") {
		t.Fatalf("empty translator comment missing: %q", buf.String())
	}
}
