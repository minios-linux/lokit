// Package android implements reading and writing of Android strings.xml files.
package android

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Parse tests
// ---------------------------------------------------------------------------

func TestParse_BasicString(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="app_name">My App</string>
    <string name="hello">Hello World</string>
</resources>`

	f, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(f.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(f.Entries))
	}
	v, ok := f.Get("app_name")
	if !ok || v != "My App" {
		t.Errorf("app_name: got %q ok=%v, want %q", v, ok, "My App")
	}
	v, ok = f.Get("hello")
	if !ok || v != "Hello World" {
		t.Errorf("hello: got %q ok=%v, want %q", v, ok, "Hello World")
	}
}

func TestParse_TranslatableFalse(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="app_name" translatable="false">MyApp</string>
    <string name="greeting">Hello</string>
</resources>`

	f, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	// app_name should be parsed but not translatable
	e := f.GetEntry("app_name")
	if e == nil {
		t.Fatal("app_name entry not found")
	}
	if e.Translatable {
		t.Error("app_name should have Translatable=false")
	}

	// greeting should be translatable
	e2 := f.GetEntry("greeting")
	if e2 == nil {
		t.Fatal("greeting entry not found")
	}
	if !e2.Translatable {
		t.Error("greeting should have Translatable=true")
	}

	// Keys() should not include non-translatable entries
	keys := f.Keys()
	for _, k := range keys {
		if k == "app_name" {
			t.Error("Keys() should not include translatable=false entry")
		}
	}

	// UntranslatedKeys() should also exclude non-translatable
	ukeys := f.UntranslatedKeys()
	for _, k := range ukeys {
		if k == "app_name" {
			t.Error("UntranslatedKeys() should not include translatable=false entry")
		}
	}
}

func TestParse_StringArray(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string-array name="planets">
        <item>Mercury</item>
        <item>Venus</item>
        <item>Earth</item>
    </string-array>
</resources>`

	f, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(f.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(f.Entries))
	}
	e := f.Entries[0]
	if e.Kind != KindStringArray {
		t.Errorf("expected KindStringArray, got %v", e.Kind)
	}
	if e.Name != "planets" {
		t.Errorf("name: got %q, want planets", e.Name)
	}
	want := []string{"Mercury", "Venus", "Earth"}
	if len(e.Items) != len(want) {
		t.Fatalf("items: got %d, want %d", len(e.Items), len(want))
	}
	for i, w := range want {
		if e.Items[i] != w {
			t.Errorf("items[%d]: got %q, want %q", i, e.Items[i], w)
		}
	}
}

func TestParse_Plurals(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <plurals name="songs_found">
        <item quantity="one">%d song found.</item>
        <item quantity="other">%d songs found.</item>
    </plurals>
</resources>`

	f, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(f.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(f.Entries))
	}
	e := f.Entries[0]
	if e.Kind != KindPlurals {
		t.Errorf("expected KindPlurals, got %v", e.Kind)
	}
	if e.Plurals["one"] != "%d song found." {
		t.Errorf("one: got %q", e.Plurals["one"])
	}
	if e.Plurals["other"] != "%d songs found." {
		t.Errorf("other: got %q", e.Plurals["other"])
	}
	// PluralOrder should preserve document order
	if len(e.PluralOrder) != 2 || e.PluralOrder[0] != "one" || e.PluralOrder[1] != "other" {
		t.Errorf("PluralOrder: got %v", e.PluralOrder)
	}
}

func TestParse_Comment(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <!-- Section header -->
    <string name="foo">Foo</string>
</resources>`

	f, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(f.Entries) != 2 {
		t.Fatalf("expected 2 entries (comment + string), got %d", len(f.Entries))
	}
	if !f.Entries[0].IsComment() {
		t.Error("first entry should be a comment")
	}
	if f.Entries[0].Comment != "Section header" {
		t.Errorf("comment: got %q", f.Entries[0].Comment)
	}
}

func TestParse_StringArrayTranslatableFalse(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string-array name="config_values" translatable="false">
        <item>value1</item>
        <item>value2</item>
    </string-array>
    <string-array name="labels">
        <item>Label A</item>
    </string-array>
</resources>`

	f, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	keys := f.Keys()
	for _, k := range keys {
		if k == "config_values" {
			t.Error("Keys() must not include translatable=false string-array")
		}
	}
	if len(keys) != 1 || keys[0] != "labels" {
		t.Errorf("Keys() = %v, want [labels]", keys)
	}
}

// ---------------------------------------------------------------------------
// Stats tests
// ---------------------------------------------------------------------------

func TestStats_MixedTypes(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="translated">Hello</string>
    <string name="untranslated"></string>
    <string name="skip" translatable="false">NoTranslate</string>
    <string-array name="arr">
        <item>Item 1</item>
        <item></item>
    </string-array>
</resources>`

	f, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	total, translated, untranslated := f.Stats()
	// skip not counted; arr has empty item so is untranslated
	if total != 3 {
		t.Errorf("total: got %d, want 3", total)
	}
	if translated != 1 {
		t.Errorf("translated: got %d, want 1", translated)
	}
	if untranslated != 2 {
		t.Errorf("untranslated: got %d, want 2", untranslated)
	}
}

// ---------------------------------------------------------------------------
// Marshal tests
// ---------------------------------------------------------------------------

func TestMarshal_BasicString(t *testing.T) {
	f := &File{byName: make(map[string]int)}
	f.addEntry(&Entry{Kind: KindString, Name: "hello", Translatable: true, Value: "Hello"})

	out := string(f.Marshal())
	if !strings.Contains(out, `<string name="hello">Hello</string>`) {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestMarshal_TranslatableFalse(t *testing.T) {
	f := &File{byName: make(map[string]int)}
	f.addEntry(&Entry{Kind: KindString, Name: "app_name", Translatable: false, Value: "MyApp"})

	out := string(f.Marshal())
	if !strings.Contains(out, `translatable="false"`) {
		t.Errorf("expected translatable=false in output:\n%s", out)
	}
	if !strings.Contains(out, `MyApp`) {
		t.Errorf("value should be preserved:\n%s", out)
	}
}

func TestMarshal_StringArray(t *testing.T) {
	f := &File{byName: make(map[string]int)}
	f.addEntry(&Entry{
		Kind:         KindStringArray,
		Name:         "planets",
		Translatable: true,
		Items:        []string{"Меркурий", "Венера"},
	})

	out := string(f.Marshal())
	if !strings.Contains(out, `<string-array name="planets">`) {
		t.Errorf("missing string-array tag:\n%s", out)
	}
	if !strings.Contains(out, `<item>Меркурий</item>`) {
		t.Errorf("missing first item:\n%s", out)
	}
	if !strings.Contains(out, `<item>Венера</item>`) {
		t.Errorf("missing second item:\n%s", out)
	}
}

func TestMarshal_Plurals(t *testing.T) {
	f := &File{byName: make(map[string]int)}
	f.addEntry(&Entry{
		Kind:         KindPlurals,
		Name:         "songs",
		Translatable: true,
		Plurals:      map[string]string{"one": "%d песня", "other": "%d песен"},
		PluralOrder:  []string{"one", "other"},
	})

	out := string(f.Marshal())
	if !strings.Contains(out, `<plurals name="songs">`) {
		t.Errorf("missing plurals tag:\n%s", out)
	}
	if !strings.Contains(out, `quantity="one"`) {
		t.Errorf("missing quantity=one:\n%s", out)
	}
	if !strings.Contains(out, `%d песня`) {
		t.Errorf("missing plural form:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// CDATA tests
// ---------------------------------------------------------------------------

func TestParse_CDATA(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="html_content"><![CDATA[<html><body><h1>Title</h1></body></html>]]></string>
</resources>`

	f, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	e := f.GetEntry("html_content")
	if e == nil {
		t.Fatal("entry not found")
	}
	if !e.UseCDATA {
		t.Error("UseCDATA should be true")
	}
	if e.Value != "<html><body><h1>Title</h1></body></html>" {
		t.Errorf("Value = %q", e.Value)
	}
}

func TestMarshal_CDATARoundTrip(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="html_content"><![CDATA[Special <tag> content]]></string>
</resources>
`
	f, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	// Set a translated value
	f.GetEntry("html_content").Value = "Спецальный <tag> контент"

	out := string(f.Marshal())
	if !strings.Contains(out, "<![CDATA[") {
		t.Errorf("CDATA wrapper not preserved in output:\n%s", out)
	}
	if !strings.Contains(out, "Спецальный <tag> контент") {
		t.Errorf("translated value not found:\n%s", out)
	}
	if strings.Contains(out, "&lt;tag&gt;") {
		t.Error("CDATA content should not be XML-escaped")
	}
}

func TestMarshal_CDATAApostropheEscaped(t *testing.T) {
	// Inside CDATA, apostrophes must still be escaped for Android AAPT
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="review"><![CDATA[Hosts can't see your review. <u>Learn more</u>]]></string>
</resources>
`
	f, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	// Value should have unescaped apostrophe after pull
	if !strings.Contains(f.GetEntry("review").Value, "can't") {
		t.Errorf("apostrophe not unescaped in value: %q", f.GetEntry("review").Value)
	}

	out := string(f.Marshal())
	if !strings.Contains(out, `can\'t`) {
		t.Errorf("apostrophe should be re-escaped in CDATA output:\n%s", out)
	}
	// HTML inside CDATA must NOT be escaped
	if strings.Contains(out, "&lt;u&gt;") {
		t.Error("HTML tags inside CDATA should not be XML-escaped")
	}
}

// ---------------------------------------------------------------------------
// Apostrophe normalisation tests
// ---------------------------------------------------------------------------

func TestParse_ApostropheUnescaped(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="msg">Don\'t forget me</string>
</resources>`

	f, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	v, _ := f.Get("msg")
	if v != "Don't forget me" {
		t.Errorf("apostrophe not normalised: got %q", v)
	}
}

func TestMarshal_ApostropheReescaped(t *testing.T) {
	f := &File{byName: make(map[string]int)}
	f.addEntry(&Entry{Kind: KindString, Name: "msg", Translatable: true, Value: "Don't forget"})

	out := string(f.Marshal())
	if !strings.Contains(out, `Don\'t forget`) {
		t.Errorf("apostrophe not re-escaped on Marshal:\n%s", out)
	}
}

func TestMarshal_ApostropheNoDoubleEscape(t *testing.T) {
	// If value already has \' it should not become \\'
	f := &File{byName: make(map[string]int)}
	f.addEntry(&Entry{Kind: KindString, Name: "msg", Translatable: true, Value: "Don't"})

	out := string(f.Marshal())
	if strings.Contains(out, `\\'`) {
		t.Errorf("double-escaped apostrophe found:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// MarshalTarget tests
// ---------------------------------------------------------------------------

func TestMarshalTarget_OmitsNonTranslatable(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="app_name" translatable="false">MyApp</string>
    <string name="greeting">Hello</string>
    <string-array name="urls" translatable="false">
        <item>https://example.com</item>
    </string-array>
    <plurals name="items">
        <item quantity="one">%d item</item>
        <item quantity="other">%d items</item>
    </plurals>
</resources>
`
	f, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	out := string(f.MarshalTarget())

	// Non-translatable resources must NOT appear in target file
	if strings.Contains(out, `name="app_name"`) {
		t.Error("MarshalTarget should not include translatable=false string")
	}
	if strings.Contains(out, `name="urls"`) {
		t.Error("MarshalTarget should not include translatable=false string-array")
	}

	// Translatable resources must appear
	if !strings.Contains(out, `name="greeting"`) {
		t.Error("MarshalTarget should include translatable string")
	}
	if !strings.Contains(out, `name="items"`) {
		t.Error("MarshalTarget should include translatable plurals")
	}
}

func TestMarshal_SourceIncludesNonTranslatable(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="app_name" translatable="false">MyApp</string>
    <string name="greeting">Hello</string>
</resources>
`
	f, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	// Marshal (source) must include everything
	out := string(f.Marshal())
	if !strings.Contains(out, `name="app_name"`) {
		t.Error("Marshal (source) should include translatable=false entries")
	}
	if !strings.Contains(out, `name="greeting"`) {
		t.Error("Marshal (source) should include translatable entries")
	}
}

// ---------------------------------------------------------------------------
// Round-trip tests
// ---------------------------------------------------------------------------

func TestRoundTrip_AllTypes(t *testing.T) {
	original := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <!-- App section -->
    <string name="app_name" translatable="false">MyApp</string>
    <string name="greeting">Hello</string>
    <string-array name="planets">
        <item>Mercury</item>
        <item>Venus</item>
    </string-array>
    <plurals name="songs">
        <item quantity="one">%d song</item>
        <item quantity="other">%d songs</item>
    </plurals>
</resources>
`
	f, err := Parse([]byte(original))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	out := string(f.Marshal())

	// Re-parse the marshalled output and verify structure
	f2, err := Parse([]byte(out))
	if err != nil {
		t.Fatalf("Parse of marshalled output error: %v", err)
	}

	if len(f.Entries) != len(f2.Entries) {
		t.Errorf("entry count: original %d, re-parsed %d", len(f.Entries), len(f2.Entries))
	}

	// Verify app_name is still non-translatable
	e := f2.GetEntry("app_name")
	if e == nil || e.Translatable {
		t.Error("app_name should still be translatable=false after round-trip")
	}

	// Verify array items
	ea := f2.GetEntry("planets")
	if ea == nil || ea.Kind != KindStringArray || len(ea.Items) != 2 {
		t.Errorf("planets array not preserved: %+v", ea)
	}

	// Verify plurals
	ep := f2.GetEntry("songs")
	if ep == nil || ep.Kind != KindPlurals {
		t.Errorf("songs plurals not preserved: %+v", ep)
	}
	if ep.Plurals["one"] != "%d song" || ep.Plurals["other"] != "%d songs" {
		t.Errorf("plural forms not preserved: %v", ep.Plurals)
	}
}

// ---------------------------------------------------------------------------
// SyncKeys tests
// ---------------------------------------------------------------------------

func TestSyncKeys_AddsNewKeys(t *testing.T) {
	source := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="a">A</string>
    <string-array name="arr">
        <item>X</item>
    </string-array>
    <plurals name="p">
        <item quantity="other">%d items</item>
    </plurals>
    <string name="skip" translatable="false">Skip</string>
</resources>`

	target := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="a"></string>
</resources>`

	src, _ := Parse([]byte(source))
	tgt, _ := Parse([]byte(target))

	added := tgt.SyncKeys(src)
	if added != 2 { // arr + p (skip excluded)
		t.Errorf("added %d, want 2", added)
	}

	// arr should now exist with correct length
	ea := tgt.GetEntry("arr")
	if ea == nil || ea.Kind != KindStringArray || len(ea.Items) != 1 {
		t.Errorf("arr not synced correctly: %+v", ea)
	}

	// p should exist with correct plural order
	ep := tgt.GetEntry("p")
	if ep == nil || ep.Kind != KindPlurals || len(ep.PluralOrder) != 1 {
		t.Errorf("p not synced correctly: %+v", ep)
	}

	// skip should not have been added
	if _, ok := tgt.byName["skip"]; ok {
		t.Error("non-translatable skip should not be synced")
	}
}

// ---------------------------------------------------------------------------
// NewTranslationFile tests
// ---------------------------------------------------------------------------

func TestNewTranslationFile(t *testing.T) {
	source := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="greeting">Hello</string>
    <string name="app" translatable="false">MyApp</string>
    <string-array name="colors">
        <item>Red</item>
        <item>Blue</item>
    </string-array>
</resources>`

	src, _ := Parse([]byte(source))
	tgt := NewTranslationFile(src)

	// translatable string should be empty
	v, ok := tgt.Get("greeting")
	if !ok || v != "" {
		t.Errorf("greeting should be empty in translation file, got %q", v)
	}

	// non-translatable string should be copied verbatim
	v, ok = tgt.Get("app")
	if !ok || v != "MyApp" {
		t.Errorf("app_name should be copied verbatim, got %q ok=%v", v, ok)
	}

	// string-array items should all be empty
	ea := tgt.GetEntry("colors")
	if ea == nil || len(ea.Items) != 2 {
		t.Fatalf("colors array not found or wrong length")
	}
	for i, item := range ea.Items {
		if item != "" {
			t.Errorf("colors[%d] should be empty, got %q", i, item)
		}
	}
}
