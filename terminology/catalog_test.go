package terminology

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeCatalog(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "terminology.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write terminology: %v", err)
	}
	return path
}

func TestCatalogExactSelectorsFallbackAndConflicts(t *testing.T) {
	path := writeCatalog(t, `version: 1
exact:
  - id: generic-save
    source: Save
    translations:
      pt: Salvar
  - id: button-save
    source: Save
    when:
      target: web
      format: vue-i18n
      path: "locales/**"
      key: "buttons.*"
      context: toolbar
    translations:
      pt_BR: Guardar
  - id: files
    source: file
    source_plural: files
    translations:
      ru: [файл, файла, файлов]
`)
	catalog, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	selector := Selector{Target: "web", Format: "vue-i18n", Path: `locales\common.json`, Key: "buttons.save", Context: "toolbar"}
	match, ok := catalog.Exact("Save", "", "pt-BR", selector)
	if !ok {
		t.Fatal("expected exact match")
	}
	if match.ID != "button-save" || match.Locale != "pt-BR" || !reflect.DeepEqual(match.Translations, []string{"Guardar"}) {
		t.Fatalf("unexpected specific match: %#v", match)
	}

	fallback, ok := catalog.MatchExact("Save", "", "pt-PT", Selector{})
	if !ok || fallback.ID != "generic-save" || fallback.Locale != "pt" {
		t.Fatalf("unexpected locale fallback: %#v, %v", fallback, ok)
	}
	plural, ok := catalog.Exact("file", "files", "ru", Selector{})
	if !ok || !reflect.DeepEqual(plural.Translations, []string{"файл", "файла", "файлов"}) {
		t.Fatalf("unexpected plural match: %#v, %v", plural, ok)
	}
	if _, ok := catalog.Exact("file", "", "ru", Selector{}); ok {
		t.Fatal("plural rule unexpectedly matched singular-only request")
	}

	match.Translations[0] = "mutated"
	again, _ := catalog.Exact("Save", "", "pt-BR", selector)
	if again.Translations[0] != "Guardar" {
		t.Fatalf("catalog was mutated through result: %#v", again)
	}
}

func TestCatalogTermMatching(t *testing.T) {
	path := writeCatalog(t, `version: 1
terms:
  - id: app
    source: app
    translations:
      de:
        preferred: Anwendung
        accepted: [App, Applikation]
  - id: api
    source: API
    case_sensitive: true
    preserve: true
  - id: disk
    source: disk
    match: substring
    when:
      format: markdown
    translations:
      de: Datenträger
`)
	catalog, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	rules := catalog.MatchTerms("The app uses API and diskless mode", "de-AT", Selector{Format: "markdown"})
	if len(rules) != 3 {
		t.Fatalf("MatchTerms returned %d rules: %#v", len(rules), rules)
	}
	if rules[0].Preferred != "Anwendung" || rules[0].Locale != "de" || !rules[0].Accepts("Applikation") || rules[0].Accepts("Programm") {
		t.Fatalf("unexpected translated term: %#v", rules[0])
	}
	if !rules[1].Preserve || !rules[1].Accepts("API") || rules[1].Accepts("api") {
		t.Fatalf("unexpected preserve term: %#v", rules[1])
	}
	if !rules[2].MatchesSource("diskless") {
		t.Fatal("substring term did not match")
	}
	if rules[0].MatchesSource("application") {
		t.Fatal("word term matched inside a larger word")
	}
	if rules[1].MatchesSource("api") {
		t.Fatal("case-sensitive source matched different case")
	}

	rules[0].Accepted[0] = "mutated"
	again := catalog.Terms("de", Selector{Format: "markdown"})
	if again[0].Accepted[0] != "App" {
		t.Fatalf("catalog was mutated through term result: %#v", again[0])
	}
}

func TestPromptValidationAllowsGrammaticalForms(t *testing.T) {
	path := writeCatalog(t, `version: 1
terms:
  - id: module-manager
    source: MiniOS Module Manager
    validation: prompt
    translations:
      ru: Менеджер модулей MiniOS
`)
	catalog, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rules := catalog.MatchTerms("Open MiniOS Module Manager", "ru", Selector{})
	if len(rules) != 1 || rules[0].Validation != ValidationPrompt {
		t.Fatalf("unexpected prompt-validated rules: %#v", rules)
	}
	if !rules[0].ValidTranslation("Open MiniOS Module Manager", "Откройте менеджер модулей MiniOS") {
		t.Fatal("prompt validation rejected a grammatical form")
	}
	if rules[0].ValidTranslation("Open MiniOS Module Manager", "Откройте MiniOS Module Manager") {
		t.Fatal("prompt validation accepted an untranslated source term")
	}
}

func TestPromptValidationAcceptsCaseEquivalentPreferredForm(t *testing.T) {
	match := TermMatch{
		Source:     "app",
		Match:      MatchWord,
		Preferred:  "App",
		Validation: ValidationPrompt,
	}
	if !match.ValidTranslation("Open the app", "App öffnen") {
		t.Fatal("prompt validation rejected a case-equivalent preferred form")
	}
}

func TestLocaleNormalizationAndFallbacks(t *testing.T) {
	for input, want := range map[string]string{
		"pt_br":      "pt-BR",
		"ZH_hans_cn": "zh-Hans-CN",
		"de-de-1996": "de-DE-1996",
	} {
		got, err := NormalizeLocale(input)
		if err != nil || got != want {
			t.Errorf("NormalizeLocale(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	got, err := LocaleFallbacks("zh_Hans_CN")
	if err != nil {
		t.Fatalf("LocaleFallbacks: %v", err)
	}
	want := []string{"zh-Hans-CN", "zh-Hans", "zh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallbacks = %v, want %v", got, want)
	}
	if _, err := NormalizeLocale("not a locale!"); err == nil {
		t.Fatal("expected invalid locale error")
	}
}

func TestLoadStrictValidation(t *testing.T) {
	tests := map[string]string{
		"unknown root field": `version: 1
other: true
`,
		"unknown exact field": `version: 1
exact:
  - id: save
    source: Save
    typo: true
    translations: {de: Speichern}
`,
		"unknown selector": `version: 1
terms:
  - id: app
    source: app
    when: {language: de}
    preserve: true
`,
		"unknown translation field": `version: 1
terms:
  - id: app
    source: app
    translations:
      de: {preferred: Anwendung, typo: App}
`,
		"normalized locale duplicate": `version: 1
exact:
  - id: save
    source: Save
    translations:
      pt-BR: Salvar
      pt_BR: Guardar
`,
		"duplicate id": `version: 1
exact:
  - id: same
    source: A
    translations: {de: A}
terms:
  - id: same
    source: B
    preserve: true
`,
		"preserve and translations": `version: 1
terms:
  - id: app
    source: app
    preserve: true
    translations: {de: Anwendung}
`,
		"invalid match": `version: 1
terms:
  - id: app
    source: app
    match: regex
    preserve: true
`,
		"invalid validation": `version: 1
terms:
  - id: app
    source: app
    validation: advisory
    translations: {de: Anwendung}
`,
		"prompt validation with preserve": `version: 1
terms:
  - id: api
    source: API
    validation: prompt
    preserve: true
`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeCatalog(t, content)
			_, err := Load(path)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), path+":") {
				t.Fatalf("error lacks file diagnostics: %v", err)
			}
		})
	}
}

func TestResolveExactRejectsEqualConflicts(t *testing.T) {
	first := writeCatalog(t, `version: 1
exact:
  - id: first
    source: Save
    translations: {de: Erste}
`)
	second := writeCatalog(t, `version: 1
exact:
  - id: second
    source: Save
    translations: {de: Zweite}
`)
	catalog, err := LoadFiles([]string{first, second})
	if err != nil {
		t.Fatalf("LoadFiles: %v", err)
	}
	if _, _, err := catalog.ResolveExact("Save", "", "de", Selector{}); err == nil {
		t.Fatal("expected equally specific exact rules to conflict")
	}
}

func TestTermValidationDoesNotDoubleCountEquivalentAcceptedForms(t *testing.T) {
	path := writeCatalog(t, `version: 1
terms:
  - id: app
    source: app
    translations:
      de:
        preferred: App
        accepted: [APP]
`)
	catalog, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := catalog.ResolveTerms("de", Selector{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 {
		t.Fatalf("rules = %d", len(rules))
	}
	if rules[0].ValidTranslation("app app", "App") {
		t.Fatal("one target occurrence satisfied two source occurrences")
	}
}

func TestCaseSensitiveTermVariantsRemainIndependent(t *testing.T) {
	path := writeCatalog(t, `version: 1
terms:
  - id: upper
    source: API
    case_sensitive: true
    preserve: true
  - id: lower
    source: api
    case_sensitive: true
    preserve: true
`)
	catalog, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := catalog.ResolveTerms("de", Selector{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(rules))
	}
}

func TestResolveRejectsUnsupportedLocale(t *testing.T) {
	path := writeCatalog(t, `version: 1
terms:
  - id: brand
    source: MiniOS
    preserve: true
`)
	catalog, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.ResolveTerms("sr@latin", Selector{}); err == nil {
		t.Fatal("expected unsupported locale to be rejected")
	}
}

func TestLoadRejectsPreserveFalseWithTranslations(t *testing.T) {
	path := writeCatalog(t, `version: 1
terms:
  - id: app
    source: app
    preserve: false
    translations: {de: Anwendung}
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected preserve: false to be rejected")
	}
}

func TestResolveTermsRejectsOverlappingPolicies(t *testing.T) {
	path := writeCatalog(t, `version: 1
terms:
  - id: api-preserve
    source: API
    case_sensitive: true
    preserve: true
  - id: api-translate
    source: API
    translations: {de: Schnittstelle}
`)
	catalog, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.ResolveTerms("de", Selector{}); err == nil {
		t.Fatal("expected overlapping policies to conflict")
	}
}
