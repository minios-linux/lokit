package polkit

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestParseSetMarshal(t *testing.T) {
	input := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<policyconfig>
  <action id="org.test.action">
    <description>Allow doing test action</description>
    <message>Authentication is required</message>
  </action>
</policyconfig>
`)

	f, err := Parse(input, "ru")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(f.Keys()) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(f.Keys()))
	}
	if got, ok := f.Get("org.test.action.description"); !ok || got != "" {
		t.Fatalf("Get(description) before Set = %q, %v", got, ok)
	}
	if got, ok := f.Get("org.test.action.unknown"); ok || got != "" {
		t.Fatalf("Get(unknown) = %q, %v", got, ok)
	}
	if !f.Set("org.test.action.description", "Разрешить тестовое действие") {
		t.Fatalf("Set(description) failed")
	}
	if !f.Set("org.test.action.message", "Требуется аутентификация") {
		t.Fatalf("Set(message) failed")
	}
	if got, ok := f.Get("org.test.action.description"); !ok || got != "Разрешить тестовое действие" {
		t.Fatalf("Get(description) after Set = %q, %v", got, ok)
	}

	out, err := f.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	f2, err := Parse(out, "ru")
	if err != nil {
		t.Fatalf("Parse(Marshal()) error = %v", err)
	}
	if got := f2.values["org.test.action.description"]; got != "Разрешить тестовое действие" {
		t.Fatalf("description mismatch: got %q", got)
	}
	if got := f2.values["org.test.action.message"]; got != "Требуется аутентификация" {
		t.Fatalf("message mismatch: got %q", got)
	}
}

func TestMarshalEscapesXMLText(t *testing.T) {
	input := []byte(`<policyconfig><action id="org.test"><description>Read and write</description></action></policyconfig>`)
	f, err := Parse(input, "de")
	if err != nil {
		t.Fatal(err)
	}
	if !f.Set("org.test.description", "Lese- & Schreibzugriff <admin>") {
		t.Fatal("Set failed")
	}
	out, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Lese- &amp; Schreibzugriff &lt;admin&gt;") {
		t.Fatalf("XML text was not escaped: %s", out)
	}
	var document any
	if err := xml.Unmarshal(out, &document); err != nil {
		t.Fatalf("marshaled policy is not well-formed XML: %v", err)
	}
}

func TestExistingXMLTextEntitiesRoundTrip(t *testing.T) {
	input := []byte(`<policyconfig><action id="org.test"><description>Read</description><description xml:lang="de">A &amp; B &lt;C&gt; &#35;</description></action></policyconfig>`)
	f, err := Parse(input, "de")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := f.Get("org.test.description"); got != "A & B <C> #" {
		t.Fatalf("decoded value = %q", got)
	}
	out, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "&amp;amp;") || strings.Contains(string(out), "&amp;lt;") {
		t.Fatalf("entities were double escaped: %s", out)
	}
	f2, err := Parse(out, "de")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := f2.Get("org.test.description"); got != "A & B <C> #" {
		t.Fatalf("round-trip value = %q", got)
	}
}
