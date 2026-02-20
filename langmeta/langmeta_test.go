package langmeta

import "testing"

func TestCanonicalize(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "pt_br", want: "pt-BR"},
		{in: " EN-us ", want: "en-US"},
		{in: "ru", want: "ru"},
		{in: "", want: ""},
	}

	for _, tc := range cases {
		got := canonicalize(tc.in)
		if got != tc.want {
			t.Fatalf("canonicalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolve(t *testing.T) {
	t.Run("exact match", func(t *testing.T) {
		got := Resolve("en-GB")
		if got.Name != "English (UK)" || got.Flag == "" {
			t.Fatalf("unexpected result: %#v", got)
		}
	})

	t.Run("normalized match", func(t *testing.T) {
		got := Resolve("pt_br")
		if got.Name != "Português (Brasil)" || got.Flag == "" {
			t.Fatalf("unexpected result: %#v", got)
		}
	})

	t.Run("base fallback", func(t *testing.T) {
		got := Resolve("fr-LU")
		if got.Name != "Français" || got.Flag != "🇫🇷" {
			t.Fatalf("unexpected fallback result: %#v", got)
		}
	})

	t.Run("unknown passthrough", func(t *testing.T) {
		got := Resolve("zz-ZZ")
		if got.Name != "zz-ZZ" || got.Flag != "" {
			t.Fatalf("unexpected unknown result: %#v", got)
		}
	})
}
