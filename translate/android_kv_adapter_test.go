package translate

import (
	"testing"

	"github.com/minios-linux/lokit/internal/format/android"
)

func TestAndroidKVFileGetCompoundUnits(t *testing.T) {
	source, err := android.Parse([]byte(`<resources>
    <string name="greeting">Hello</string>
    <string-array name="days"><item>Monday</item><item>Tuesday</item></string-array>
    <plurals name="count"><item quantity="one">One item</item><item quantity="other">Many items</item></plurals>
</resources>`))
	if err != nil {
		t.Fatalf("Parse(source) error = %v", err)
	}
	target, err := android.Parse([]byte(`<resources>
    <string name="greeting">Hola</string>
    <string-array name="days"><item>Lunes</item><item></item></string-array>
    <plurals name="count"><item quantity="one">Un elemento</item><item quantity="other"></item></plurals>
</resources>`))
	if err != nil {
		t.Fatalf("Parse(target) error = %v", err)
	}

	f := newAndroidKVFile(target, source)
	tests := []struct {
		key   string
		value string
		ok    bool
	}{
		{key: "greeting", value: "Hola", ok: true},
		{key: "days[0]", value: "Lunes", ok: true},
		{key: "days[1]", value: "", ok: true},
		{key: "count#one", value: "Un elemento", ok: true},
		{key: "count#other", value: "", ok: true},
		{key: "missing", value: "", ok: false},
	}
	for _, tt := range tests {
		got, ok := f.Get(tt.key)
		if got != tt.value || ok != tt.ok {
			t.Errorf("Get(%q) = %q, %v; want %q, %v", tt.key, got, ok, tt.value, tt.ok)
		}
	}
}

func TestAndroidKVFileGetRejectsMissingTargetUnit(t *testing.T) {
	source, err := android.Parse([]byte(`<resources><string-array name="days"><item>Monday</item></string-array></resources>`))
	if err != nil {
		t.Fatalf("Parse(source) error = %v", err)
	}
	target, err := android.Parse([]byte(`<resources></resources>`))
	if err != nil {
		t.Fatalf("Parse(target) error = %v", err)
	}

	if got, ok := newAndroidKVFile(target, source).Get("days[0]"); ok || got != "" {
		t.Fatalf("Get(days[0]) = %q, %v", got, ok)
	}
}
