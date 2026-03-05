package polkit

import "testing"

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
	if !f.Set("org.test.action.description", "Разрешить тестовое действие") {
		t.Fatalf("Set(description) failed")
	}
	if !f.Set("org.test.action.message", "Требуется аутентификация") {
		t.Fatalf("Set(message) failed")
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
