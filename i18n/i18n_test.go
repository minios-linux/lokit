package i18n

import "testing"

func clearLocaleEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LANGUAGE", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "")
}

func TestDetectLanguagePriorityAndNormalization(t *testing.T) {
	t.Run("LANGUAGE has highest priority", func(t *testing.T) {
		clearLocaleEnv(t)
		t.Setenv("LANGUAGE", "ru_RU.UTF-8:en_US")
		t.Setenv("LC_ALL", "de_DE.UTF-8")

		if got := detectLanguage(); got != "ru_RU" {
			t.Fatalf("detectLanguage() = %q, want %q", got, "ru_RU")
		}
	})

	t.Run("C and POSIX are skipped", func(t *testing.T) {
		clearLocaleEnv(t)
		t.Setenv("LANGUAGE", "C")
		t.Setenv("LC_ALL", "POSIX")
		t.Setenv("LC_MESSAGES", "fr_FR.UTF-8")

		if got := detectLanguage(); got != "fr_FR" {
			t.Fatalf("detectLanguage() = %q, want %q", got, "fr_FR")
		}
	})

	t.Run("falls back to en", func(t *testing.T) {
		clearLocaleEnv(t)
		if got := detectLanguage(); got != "en" {
			t.Fatalf("detectLanguage() = %q, want %q", got, "en")
		}
	})
}

func TestTAndNFallbackWhenUninitialized(t *testing.T) {
	old := po
	po = nil
	t.Cleanup(func() { po = old })

	if got := T("Hello"); got != "Hello" {
		t.Fatalf("T fallback = %q, want %q", got, "Hello")
	}

	if got := N("file", "files", 1); got != "file" {
		t.Fatalf("N singular fallback = %q, want %q", got, "file")
	}

	if got := N("file", "files", 2); got != "files" {
		t.Fatalf("N plural fallback = %q, want %q", got, "files")
	}
}
