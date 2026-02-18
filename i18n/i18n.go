// Package i18n provides internationalization support for lokit itself.
//
// It wraps the gotext library to provide simple T() and N() functions
// for translating lokit's user-facing strings. Translations are embedded
// in the binary via //go:embed and loaded at startup via Init().
//
// Usage:
//
//	import "github.com/minios-linux/lokit/i18n"
//
//	func main() {
//	    i18n.Init("")  // auto-detect from LANGUAGE/LC_ALL/LC_MESSAGES/LANG
//	    fmt.Println(i18n.T("Hello, world!"))
//	    fmt.Println(i18n.N("Found %d file", "Found %d files", count))
//	}
package i18n

import (
	"embed"
	"os"
	"strings"

	"github.com/leonelquinteros/gotext"
)

// locales embeds the compiled .po/.mo translation files.
// Directory structure: locales/{lang}/LC_MESSAGES/lokit.po
//
//go:embed all:locales
var locales embed.FS

// domain is the gettext domain name for lokit.
const domain = "lokit"

// po is the gotext locale object used for translations.
var po *gotext.Locale

// Init initializes the i18n system. If lang is empty, it auto-detects
// from the environment variables LANGUAGE, LC_ALL, LC_MESSAGES, LANG
// (in that order, matching GNU gettext behavior).
//
// Init should be called once at program startup, before any T() or N() calls.
func Init(lang string) {
	if lang == "" {
		lang = detectLanguage()
	}

	po = gotext.NewLocaleFSWithPath(lang, locales, "locales")
	po.AddDomain(domain)
	po.SetDomain(domain)
}

// T translates a string. If no translation is available, returns the
// original string unchanged (standard gettext passthrough behavior).
func T(msgid string) string {
	if po == nil {
		return msgid
	}
	return po.Get(msgid)
}

// N translates a string with plural forms. The singular form is used
// when n == 1, the plural form otherwise (exact rules depend on the
// target language's plural formula).
func N(singular, plural string, n int) string {
	if po == nil {
		if n == 1 {
			return singular
		}
		return plural
	}
	return po.GetN(singular, plural, n)
}

// detectLanguage reads environment variables to determine the user's
// preferred language, following GNU gettext conventions.
func detectLanguage() string {
	// GNU gettext priority: LANGUAGE > LC_ALL > LC_MESSAGES > LANG
	for _, env := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if val := os.Getenv(env); val != "" {
			// LANGUAGE can be a colon-separated list; take the first
			if env == "LANGUAGE" {
				parts := strings.SplitN(val, ":", 2)
				val = parts[0]
			}
			// Strip encoding suffix (e.g. "ru_RU.UTF-8" -> "ru_RU")
			if idx := strings.IndexByte(val, '.'); idx >= 0 {
				val = val[:idx]
			}
			// Skip "C" and "POSIX" — these mean no translation
			if val == "C" || val == "POSIX" || val == "" {
				continue
			}
			return val
		}
	}
	return "en"
}
