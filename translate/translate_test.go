// Package translate contains tests for the translation engine.
package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minios-linux/lokit/internal/format/i18next"
	po "github.com/minios-linux/lokit/internal/format/po"
	"github.com/minios-linux/lokit/lockfile"
	"github.com/minios-linux/lokit/terminology"
)

type testKVFile struct {
	mu        sync.Mutex
	keys      []string
	values    map[string]string
	writtenTo string
}

func newTestKVFile(keys []string, values map[string]string) *testKVFile {
	copyVals := make(map[string]string, len(values))
	for k, v := range values {
		copyVals[k] = v
	}
	return &testKVFile{keys: append([]string(nil), keys...), values: copyVals}
}

func identifiedKVProviderResponse(keys, translations []string) string {
	ids := kvTranslationIDs(keys)
	items := make([]identifiedTranslation, len(keys))
	for i := range keys {
		value, _ := json.Marshal(translations[i])
		items[i] = identifiedTranslation{ID: ids[i], Translation: value}
	}
	content, _ := json.Marshal(items)
	response, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}}},
	})
	return string(response)
}

var preservedTermTokenPattern = regexp.MustCompile(`__LOKIT_PRESERVE_TERM_[0-9a-f]+_[0-9]+_[0-9]+__`)

func preservedTermTokenFromBody(t *testing.T, body []byte) string {
	t.Helper()
	token := preservedTermTokenPattern.Find(body)
	if token == nil {
		t.Fatalf("provider prompt does not contain a preserved terminology token: %s", body)
	}
	return string(token)
}

func preservedTermTokensFromBody(t *testing.T, body []byte) []string {
	t.Helper()
	seen := make(map[string]bool)
	var tokens []string
	for _, match := range preservedTermTokenPattern.FindAll(body, -1) {
		token := string(match)
		if !seen[token] {
			seen[token] = true
			tokens = append(tokens, token)
		}
	}
	if len(tokens) == 0 {
		t.Fatalf("provider prompt does not contain preserved terminology tokens: %s", body)
	}
	return tokens
}

func loadTestTerminology(t *testing.T, content string) *terminology.Catalog {
	t.Helper()
	path := filepath.Join(t.TempDir(), "terminology.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog, err := terminology.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func TestTranslateAppliesExactTerminologyWithoutProvider(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
exact:
  - id: action.save
    source: Save
    translations:
      de: Speichern
`)
	file := po.NewFile()
	entry := &po.Entry{MsgID: "Save"}
	file.Entries = append(file.Entries, entry)

	err := Translate(context.Background(), file, Options{
		Language:    "de",
		TargetName:  "app",
		Format:      "gettext",
		Terminology: catalog,
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if entry.MsgStr != "Speichern" {
		t.Fatalf("MsgStr = %q, want exact terminology translation", entry.MsgStr)
	}
}

func TestTranslateTerminologyConflictDoesNotPartiallyMutatePO(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
exact:
  - id: action.open
    source: Open
    translations: {de: Öffnen}
  - id: action.save.first
    source: Save
    translations: {de: Speichern}
  - id: action.save.second
    source: Save
    translations: {de: Sichern}
`)
	file := po.NewFile()
	open := &po.Entry{MsgID: "Open"}
	save := &po.Entry{MsgID: "Save"}
	file.Entries = append(file.Entries, open, save)
	err := Translate(context.Background(), file, Options{Language: "de", Terminology: catalog})
	if err == nil {
		t.Fatal("expected terminology conflict")
	}
	if open.MsgStr != "" || save.MsgStr != "" {
		t.Fatalf("PO was partially mutated: Open=%q Save=%q", open.MsgStr, save.MsgStr)
	}
}

func TestTerminologyPromptDescribesPromptValidation(t *testing.T) {
	rules := []terminology.TermMatch{{
		ID:         "app.module-manager",
		Source:     "MiniOS Module Manager",
		Preferred:  "Менеджер модулей MiniOS",
		Validation: terminology.ValidationPrompt,
	}}
	prompt := appendTerminologyPrompt("Translate", []terminologyPromptEntry{promptTerms("item-1", rules)})
	for _, expected := range []string{`"validation":"prompt"`, "adapt the preferred term's grammar"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("terminology prompt does not contain %q: %s", expected, prompt)
		}
	}
	if system := terminologySystemPrompt("System"); !strings.Contains(system, `validation is "prompt"`) {
		t.Fatalf("system prompt does not describe prompt validation: %s", system)
	}
}

func TestTranslateAllKVPromotesExistingTerminologyViolation(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
terms:
  - id: brand.minios
    source: MiniOS
    preserve: true
    case_sensitive: true
`)
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		token := preservedTermTokenFromBody(t, body)
		if !strings.Contains(string(body), token+" Settings") {
			t.Errorf("provider prompt does not mask preserved term: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(identifiedKVProviderResponse([]string{"title"}, []string{token + "-Einstellungen"})))
	}))
	defer ts.Close()

	file := newTestKVFile([]string{"title"}, map[string]string{"title": "Einstellungen"})
	err := TranslateAllKV(context.Background(), []KVLangTask{{
		Lang:         "de",
		LangName:     "German",
		FilePath:     filepath.Join(t.TempDir(), "de.json"),
		File:         file,
		SourceValues: map[string]string{"title": "MiniOS Settings"},
	}}, Options{
		Provider:     Provider{ID: ProviderCustomOpenAI, BaseURL: ts.URL, Model: "test"},
		TargetName:   "ui",
		Format:       "vue-i18n",
		Terminology:  catalog,
		ParallelMode: ParallelSequential,
	}, DefaultKVChunkTranslator())
	if err != nil {
		t.Fatalf("TranslateAllKV returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("provider requests = %d, want 1", requests)
	}
	if got := file.Value("title"); got != "MiniOS-Einstellungen" {
		t.Fatalf("translated value = %q", got)
	}
}

func TestPreservedTermMaskRoundTrip(t *testing.T) {
	rules := []terminology.TermMatch{{
		ID:            "brand.minios",
		Source:        "MiniOS",
		Match:         terminology.MatchWord,
		CaseSensitive: false,
		Preserve:      true,
	}}
	source := "MiniOS and minios, but not MiniOSX"
	namespace := "__LOKIT_PRESERVE_TERM_test_"
	masked, values := maskPreservedTerms(source, rules, namespace, 3)
	if masked != namespace+"3_0__ and "+namespace+"3_1__, but not MiniOSX" {
		t.Fatalf("masked source = %q", masked)
	}
	restored, err := restorePreservedTerms(masked, values, namespace)
	if err != nil {
		t.Fatalf("restorePreservedTerms: %v", err)
	}
	if restored != source {
		t.Fatalf("restored source = %q, want %q", restored, source)
	}
	direct, err := restorePreservedTerms("MiniOS and minios, but not MiniOSX", values, namespace)
	if err == nil || direct != "" || !strings.Contains(err.Error(), "outside its opaque placeholder") {
		t.Fatalf("direct preserved terms were not rejected safely: direct=%q err=%v", direct, err)
	}
	missing, err := restorePreservedTerms("missing terms", values, namespace)
	if err == nil || missing != "" {
		t.Fatalf("missing preserved terms were not rejected: missing=%q err=%v", missing, err)
	}
	var firstToken string
	for token := range values {
		firstToken = token
		break
	}
	duplicated := strings.Replace(masked, firstToken, firstToken+" "+firstToken, 1)
	if _, err := restorePreservedTerms(duplicated, values, namespace); err == nil {
		t.Fatal("duplicated expected token was accepted")
	}
	if _, err := restorePreservedTerms(namespace+"9_0__", nil, namespace); err == nil {
		t.Fatal("unexpected cross-entry terminology token was accepted")
	}
}

func TestPreservedTermMaskMergesCrossingSpans(t *testing.T) {
	rules := []terminology.TermMatch{
		{Source: "Open Code", Match: terminology.MatchWord, CaseSensitive: true, Preserve: true},
		{Source: "Code Assistant", Match: terminology.MatchWord, CaseSensitive: true, Preserve: true},
	}
	namespace := "__LOKIT_PRESERVE_TERM_crossing_"
	masked, values := maskPreservedTerms("Open Code Assistant", rules, namespace, 0)
	if masked != namespace+"0_0__" {
		t.Fatalf("crossing terms were not merged: %q", masked)
	}
	restored, err := restorePreservedTerms(masked, values, namespace)
	if err != nil {
		t.Fatalf("restorePreservedTerms: %v", err)
	}
	if restored != "Open Code Assistant" {
		t.Fatalf("restored crossing terms = %q", restored)
	}
}

func TestRawPreservedTermsUseRuleMatchingSemantics(t *testing.T) {
	namespace := preservedTermNamespace([]string{"MiniOS and .sb"})
	values := map[string]string{
		namespace + "0_0__": "MiniOS",
		namespace + "0_1__": ".sb",
	}
	rules := []terminology.TermMatch{
		{Source: "MiniOS", Match: terminology.MatchWord, CaseSensitive: false, Preserve: true},
		{Source: ".sb", Match: terminology.MatchSubstring, CaseSensitive: true, Preserve: true},
	}
	for _, response := range []string{
		namespace + "0_0__ " + namespace + "0_1__ minios",
		namespace + "0_0__ " + namespace + "0_1__ file.sb2",
	} {
		if err := validateRawPreservedTerms(response, values, rules); err == nil {
			t.Fatalf("raw protected term was accepted: %q", response)
		}
	}
	forged := namespace + "0_0__ " + namespace + "0_1__ __LOKIT_PRESERVE_TERM_deadbeef_9_9__"
	if _, err := restorePreservedTerms(forged, values, namespace); err == nil {
		t.Fatal("forged preserved placeholder was accepted")
	}
}

func TestProtectedCompoundBindsOnlyItsProtectedBrand(t *testing.T) {
	rules := []terminology.TermMatch{
		{ID: "brand.minios", Source: "MiniOS", Match: terminology.MatchWord, CaseSensitive: true, Preserve: true},
		{ID: "brand.lokit", Source: "Lokit", Match: terminology.MatchWord, CaseSensitive: true, Preserve: true},
		{ID: "app.installer", Source: "MiniOS Installer", Match: terminology.MatchWord, CaseSensitive: true, Preferred: "Instalador de MiniOS"},
	}
	source := "MiniOS Installer and Lokit Installer"
	namespace := preservedTermNamespace([]string{source})
	masked, values := maskPreservedTerms(source, rules, namespace, 0)
	prompt := bindProtectedCompoundTerms(promptTerms("item", rules), []string{masked}, values)
	if len(prompt.Terms) != 1 {
		t.Fatalf("bound terms = %#v, want one MiniOS compound", prompt.Terms)
	}
	if !strings.Contains(prompt.Terms[0].Source, valuesTokenFor(t, values, "MiniOS")) || strings.Contains(prompt.Terms[0].Source, valuesTokenFor(t, values, "Lokit")) {
		t.Fatalf("compound bound to wrong protected brand: %#v", prompt.Terms[0])
	}
}

func TestProtectedCompoundBindingFailsClosedForMergedTerms(t *testing.T) {
	rules := []terminology.TermMatch{
		{Source: "Open Code", Match: terminology.MatchWord, CaseSensitive: true, Preserve: true},
		{Source: "Code Assistant", Match: terminology.MatchWord, CaseSensitive: true, Preserve: true},
		{Source: "Open Code Assistant Plugin", Match: terminology.MatchWord, CaseSensitive: true, Preferred: "Plugin Open Code Assistant"},
	}
	source := "Open Code Assistant Plugin"
	namespace := preservedTermNamespace([]string{source})
	masked, values := maskPreservedTerms(source, rules, namespace, 0)
	prompt := bindProtectedCompoundTerms(promptTerms("item", rules), []string{masked}, values)
	if len(prompt.Terms) != 0 {
		t.Fatalf("ambiguous merged protected compound was exposed: %#v", prompt.Terms)
	}
}

func TestProtectedCompoundBindingPreservesPunctuationBoundaries(t *testing.T) {
	rules := []terminology.TermMatch{
		{Source: ".NET", Match: terminology.MatchWord, CaseSensitive: true, Preserve: true},
		{Source: ".NET Framework", Match: terminology.MatchWord, CaseSensitive: true, Preferred: "Framework .NET"},
	}
	source := "Use .NET Framework"
	namespace := preservedTermNamespace([]string{source})
	masked, values := maskPreservedTerms(source, rules, namespace, 0)
	prompt := bindProtectedCompoundTerms(promptTerms("item", rules), []string{masked}, values)
	if len(prompt.Terms) != 1 {
		t.Fatalf("punctuation-prefixed protected compound was not bound: masked=%q terms=%#v", masked, prompt.Terms)
	}
}

func TestProtectedCompoundMarkerNeverReachesProvider(t *testing.T) {
	rules := []terminology.TermMatch{
		{Source: "MiniOS", Match: terminology.MatchWord, CaseSensitive: true, Preserve: true},
		{Source: "Installer", Match: terminology.MatchWord, CaseSensitive: true, Preferred: "MiniOS Installer"},
	}
	source := "MiniOS and Installer"
	namespace := preservedTermNamespace([]string{source})
	masked, values := maskPreservedTerms(source, rules, namespace, 0)
	prompt := bindProtectedCompoundTerms(promptTerms("item", rules), []string{masked}, values)
	data, _ := json.Marshal(prompt)
	if strings.Contains(string(data), "__LOKIT_PROTECTED_TERM__") {
		t.Fatalf("unbound protected compound marker reached prompt: %s", data)
	}
	if err := validateRawPreservedTerms("__LOKIT_PROTECTED_TERM__", values, rules); err == nil {
		t.Fatal("provider-authored internal compound marker was accepted")
	}
}

func valuesTokenFor(t *testing.T, values map[string]string, want string) string {
	t.Helper()
	for token, value := range values {
		if value == want {
			return token
		}
	}
	t.Fatalf("no token for %q in %#v", want, values)
	return ""
}

func TestPreservedTermMaskIncludesBrandInsideTranslatedTerm(t *testing.T) {
	rules := []terminology.TermMatch{
		{ID: "brand.minios", Source: "MiniOS", Match: terminology.MatchWord, Preserve: true},
		{ID: "app.installer", Source: "MiniOS Installer", Match: terminology.MatchWord, Preferred: "Instalador de MiniOS"},
	}
	namespace := preservedTermNamespace([]string{"MiniOS works with MiniOS Installer"})
	masked, values := maskPreservedTerms("MiniOS works with MiniOS Installer", rules, namespace, 0)
	if len(values) != 2 {
		t.Fatalf("preserved values = %v, want both brand occurrences", values)
	}
	if strings.Contains(masked, "MiniOS") || !strings.Contains(masked, namespace+"0_1__ Installer") {
		t.Fatalf("brand inside translated compound was not masked: %s", masked)
	}
	if strings.Count(masked, namespace) != 2 {
		t.Fatalf("masked text has unexpected tokens: %s", masked)
	}
	prompt := bindProtectedCompoundTerms(promptTerms("item-1", rules), []string{masked}, values)
	if len(prompt.Terms) != 1 || prompt.Terms[0].ID != "protected-compound-0-0" ||
		prompt.Terms[0].Source != namespace+"0_1__ Installer" ||
		prompt.Terms[0].Preferred != "Instalador de "+namespace+"0_1__" {
		t.Fatalf("provider prompt does not safely template translated compound: %#v", prompt.Terms)
	}
}

func TestRejectedResponseBuildsConversationHistory(t *testing.T) {
	messages := []providerMessage{
		{Role: "system", Content: "rules"},
		{Role: "user", Content: "translate"},
	}
	messages = appendRejectedResponse(messages, `[{"id":"kv-1","translation":"MiniOS MiniOS"}]`, fmt.Errorf("expected one MiniOS"))
	body, err := buildOpenAIChatMessagesRequest("gpt-4.1", messages, 0)
	if err != nil {
		t.Fatalf("buildOpenAIChatMessagesRequest: %v", err)
	}
	var request struct {
		Messages []providerMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	roles := []string{"system", "user", "assistant", "user"}
	if len(request.Messages) != len(roles) {
		t.Fatalf("conversation messages = %#v", request.Messages)
	}
	for i, role := range roles {
		if request.Messages[i].Role != role {
			t.Fatalf("message %d role = %q, want %q", i, request.Messages[i].Role, role)
		}
	}
	if !strings.Contains(request.Messages[3].Content, "expected one MiniOS") {
		t.Fatalf("correction message does not include validation feedback: %s", request.Messages[3].Content)
	}
}

func TestPreserveCardinalityRetryHidesProtectedTerm(t *testing.T) {
	rules := []terminology.TermMatch{{
		ID: "brand.minios", Source: "MiniOS", Match: terminology.MatchWord,
		CaseSensitive: true, Preserve: true,
	}}
	err := validateTerminology("MiniOS runs MiniOS", "MiniOS MiniOS MiniOS", rules)
	if err == nil {
		t.Fatal("expected preserve cardinality error")
	}
	messages := appendRejectedResponse([]providerMessage{
		{Role: "system", Content: "rules"},
		{Role: "user", Content: "translate opaque placeholders"},
	}, `[{"id":"kv-1","translation":"MiniOS MiniOS MiniOS"}]`, fmt.Errorf("path: %w", err))
	if strings.Contains(messages[2].Content, "MiniOS") || strings.Contains(messages[3].Content, "MiniOS") {
		t.Fatalf("retry conversation exposed protected term: %#v", messages)
	}
	if !strings.Contains(messages[2].Content, "omitted") || !strings.Contains(messages[3].Content, "corresponding to an input") {
		t.Fatalf("retry conversation lacks safe correction guidance: %#v", messages)
	}
}

func TestKVTerminologyRetryHidesRejectedProtectedTermAndRecovers(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
terms:
  - id: hardware.ram
    source: RAM
    preserve: true
`)
	attempt := 0
	var token string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		attempt++
		if attempt == 1 {
			token = preservedTermTokenFromBody(t, body)
			_, _ = w.Write([]byte(identifiedKVProviderResponse([]string{"memory"}, []string{token + " " + token + " RAM"})))
			return
		}
		if !bytes.Contains(body, []byte("ASSISTANT:")) ||
			!bytes.Contains(body, []byte("Rejected response omitted")) ||
			bytes.Contains(body, []byte("RAM")) {
			t.Fatalf("correction request exposes protected terminology or lacks safe context: %s", body)
		}
		_, _ = w.Write([]byte(identifiedKVProviderResponse([]string{"memory"}, []string{token})))
	}))
	defer ts.Close()

	file := newTestKVFile([]string{"memory"}, map[string]string{"memory": ""})
	err := TranslateAllKV(context.Background(), []KVLangTask{{
		Lang:         "de",
		LangName:     "German",
		FilePath:     "de.json",
		File:         file,
		SourceValues: map[string]string{"memory": "RAM"},
	}}, Options{
		Provider:     Provider{ID: ProviderCustomOpenAI, BaseURL: ts.URL, Model: "test-model"},
		ParallelMode: ParallelSequential,
		Terminology:  catalog,
	}, DefaultKVChunkTranslator())
	if err != nil {
		t.Fatalf("TranslateAllKV: %v", err)
	}
	if attempt != 2 || file.Value("memory") != "RAM" {
		t.Fatalf("attempts=%d translation=%q", attempt, file.Value("memory"))
	}
}

func TestKVRejectsProtectedLiteralMovedAcrossEntries(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
terms:
  - id: brand.minios
    source: MiniOS
    preserve: true
    case_sensitive: true
    when:
      key: brand
`)
	attempt := 0
	var tokens []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		attempt++
		if attempt == 1 {
			if bytes.Contains(body, []byte("MiniOS")) {
				t.Fatalf("selector-scoped source exposed protected literal: %s", body)
			}
			tokens = preservedTermTokensFromBody(t, body)
			if len(tokens) != 2 {
				t.Fatalf("preserved token count = %d, want 2", len(tokens))
			}
			_, _ = w.Write([]byte(identifiedKVProviderResponse([]string{"brand", "generic"}, []string{tokens[0], tokens[1] + " MiniOS"})))
			return
		}
		if bytes.Contains(body, []byte("MiniOS")) {
			t.Fatalf("retry exposed protected literal: %s", body)
		}
		_, _ = w.Write([]byte(identifiedKVProviderResponse([]string{"brand", "generic"}, []string{tokens[0], tokens[1] + " allgemein"})))
	}))
	defer ts.Close()

	file := newTestKVFile([]string{"brand", "generic"}, map[string]string{"brand": "", "generic": ""})
	err := TranslateAllKV(context.Background(), []KVLangTask{{
		Lang: "de", LangName: "German", FilePath: "de.json", File: file,
		SourceValues: map[string]string{"brand": "MiniOS", "generic": "MiniOS general"},
	}}, Options{
		Provider: Provider{ID: ProviderCustomOpenAI, BaseURL: ts.URL, Model: "test"}, ParallelMode: ParallelSequential, Terminology: catalog,
	}, DefaultKVChunkTranslator())
	if err != nil {
		t.Fatal(err)
	}
	if attempt != 2 || file.Value("generic") != "MiniOS allgemein" {
		t.Fatalf("attempts=%d generic=%q", attempt, file.Value("generic"))
	}
}

func TestSourceBackedI18NextMasksProtectedValue(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
terms:
  - id: brand.minios
    source: MiniOS
    preserve: true
    case_sensitive: true
    when:
      key: title
`)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("MiniOS")) {
			t.Fatalf("source-backed I18Next exposed protected value: %s", body)
		}
		token := preservedTermTokenFromBody(t, body)
		_, _ = w.Write([]byte(identifiedKVProviderResponse([]string{"title"}, []string{token + " Einstellungen"})))
	}))
	defer ts.Close()
	file := newTestKVFile([]string{"title"}, map[string]string{"title": ""})
	err := TranslateAllKV(context.Background(), []KVLangTask{{
		Lang: "de", LangName: "German", FilePath: "de.js", File: file,
		SourceValues: map[string]string{"title": "MiniOS settings"},
	}}, Options{
		Provider: Provider{ID: ProviderCustomOpenAI, BaseURL: ts.URL, Model: "test"}, ParallelMode: ParallelSequential, Terminology: catalog,
	}, I18NextChunkTranslator())
	if err != nil {
		t.Fatal(err)
	}
	if got := file.Value("title"); got != "MiniOS Einstellungen" {
		t.Fatalf("translation = %q", got)
	}
}

func TestTerminologyDiagnosticIncludesPathCountsAndTranslation(t *testing.T) {
	rules := [][]terminology.TermMatch{{{
		ID:            "brand.minios",
		Source:        "MiniOS",
		Match:         terminology.MatchWord,
		CaseSensitive: true,
		Preserve:      true,
	}}}
	err := validateKVChunkTerminology(
		[]string{"sec:0"},
		map[string]string{"sec:0": "MiniOS runs MiniOS"},
		[]string{"Mini OS executa MiniOS"},
		rules,
		"maintenance/Updating-MiniOS.md",
	)
	if err == nil {
		t.Fatal("expected terminology diagnostic")
	}
	for _, want := range []string{
		"maintenance/Updating-MiniOS.md:sec:0",
		`terminology rule "brand.minios"`,
		"expected 2 occurrence(s), found 1",
		"Mini OS executa MiniOS",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnostic %q does not contain %q", err, want)
		}
	}
}

func TestTranslateChunkMasksAndRestoresPreservedPOTerms(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
terms:
  - id: brand.minios
    source: MiniOS
    preserve: true
    case_sensitive: true
`)
	entry := &po.Entry{MsgID: "MiniOS settings"}
	entries := []*po.Entry{entry}
	id := entryTranslationIDs(entries)[0]
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		token := preservedTermTokenFromBody(t, body)
		if !strings.Contains(string(body), token+" settings") {
			t.Errorf("PO provider prompt does not mask preserved term: %s", body)
		}
		if strings.Contains(string(body), "MiniOS") || strings.Contains(string(body), "brand.minios") {
			t.Errorf("PO provider prompt exposes protected terminology metadata: %s", body)
		}
		value, _ := json.Marshal(token + "-Einstellungen")
		content, _ := json.Marshal([]identifiedTranslation{{ID: id, Translation: value}})
		response, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}}},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(response)
	}))
	defer ts.Close()

	translations, err := translateChunk(context.Background(), entries, DefaultSystemPrompt, Options{
		Provider:     Provider{ID: ProviderCustomOpenAI, BaseURL: ts.URL, Model: "test"},
		Language:     "de",
		LanguageName: "German",
		TargetName:   "app",
		Format:       "gettext",
		Terminology:  catalog,
	}, &rateLimitState{})
	if err != nil {
		t.Fatalf("translateChunk: %v", err)
	}
	if len(translations) != 1 || translations[0] != "MiniOS-Einstellungen" {
		t.Fatalf("translations = %v", translations)
	}
}

func TestPORejectsProtectedLiteralMovedAcrossEntries(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
terms:
  - id: brand.minios
    source: MiniOS
    preserve: true
    case_sensitive: true
    when:
      key: "MiniOS settings"
`)
	entries := []*po.Entry{{MsgID: "MiniOS settings"}, {MsgID: "MiniOS general settings"}}
	ids := entryTranslationIDs(entries)
	attempt := 0
	var tokens []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		attempt++
		if attempt == 1 {
			if bytes.Contains(body, []byte("MiniOS")) {
				t.Fatalf("selector-scoped PO source exposed protected literal: %s", body)
			}
			tokens = preservedTermTokensFromBody(t, body)
			if len(tokens) != 2 {
				t.Fatalf("PO preserved token count = %d, want 2", len(tokens))
			}
		}
		values := []string{tokens[0] + "-Einstellungen", tokens[1] + " Allgemein"}
		if attempt == 1 {
			values[1] += " MiniOS"
		} else if bytes.Contains(body, []byte("MiniOS")) {
			t.Fatalf("PO retry exposed protected literal: %s", body)
		}
		items := make([]identifiedTranslation, len(ids))
		for i := range ids {
			encoded, _ := json.Marshal(values[i])
			items[i] = identifiedTranslation{ID: ids[i], Translation: encoded}
		}
		content, _ := json.Marshal(items)
		response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}}}})
		_, _ = w.Write(response)
	}))
	defer ts.Close()

	translations, err := translateChunk(context.Background(), entries, DefaultSystemPrompt, Options{
		Provider: Provider{ID: ProviderCustomOpenAI, BaseURL: ts.URL, Model: "test"}, Language: "de", LanguageName: "German", Terminology: catalog,
	}, &rateLimitState{})
	if err != nil {
		t.Fatal(err)
	}
	if attempt != 2 || translations[1] != "MiniOS Allgemein" {
		t.Fatalf("attempts=%d translations=%#v", attempt, translations)
	}
}

func TestRestorePOPluralRejectsCrossFormToken(t *testing.T) {
	rules := []terminology.TermMatch{{
		Source:        "MiniOS",
		Match:         terminology.MatchWord,
		CaseSensitive: true,
		Preserve:      true,
	}}
	namespace := preservedTermNamespace([]string{"MiniOS file", "MiniOS files"})
	maskedSingular, singular := maskPreservedTerms("MiniOS file", rules, namespace, 0)
	_, plural := maskPreservedTerms("MiniOS files", rules, namespace, 1)
	translations := []pluralTranslation{{plural: []string{maskedSingular, maskedSingular}}}
	err := restorePOPluralPreservedTerms(
		[]*po.Entry{{MsgID: "MiniOS file", MsgIDPlural: "MiniOS files"}},
		translations,
		[]map[string]string{singular},
		[]map[string]string{plural},
		[][]terminology.TermMatch{rules},
		namespace,
	)
	if err == nil {
		t.Fatal("cross-form preserved terminology token was accepted")
	}
}

func TestTranslateAllKVSkipsTerminologyCompliantExistingValue(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
terms:
  - id: brand.minios
    source: MiniOS
    preserve: true
`)
	file := newTestKVFile([]string{"title"}, map[string]string{"title": "MiniOS-Einstellungen"})
	err := TranslateAllKV(context.Background(), []KVLangTask{{
		Lang:         "de",
		LangName:     "German",
		FilePath:     filepath.Join(t.TempDir(), "de.json"),
		File:         file,
		SourceValues: map[string]string{"title": "MiniOS Settings"},
	}}, Options{TargetName: "ui", Format: "vue-i18n", Terminology: catalog}, DefaultKVChunkTranslator())
	if err != nil {
		t.Fatalf("TranslateAllKV returned error: %v", err)
	}
}

func TestTranslateAllKVPassesThroughIgnoredPatterns(t *testing.T) {
	for _, mode := range []string{ParallelSequential, ParallelFullParallel} {
		t.Run(mode, func(t *testing.T) {
			file := newTestKVFile(
				[]string{"fm:updated", "fm:program_commits.app"},
				map[string]string{
					"fm:updated":             "26.08.2026",
					"fm:program_commits.app": "modified-sha",
				},
			)
			task := KVLangTask{
				Lang:     "de",
				FilePath: filepath.Join(t.TempDir(), "de.md"),
				File:     file,
				SourceValues: map[string]string{
					"fm:updated":             "2026-08-26",
					"fm:program_commits.app": "0123456789abcdef0123456789abcdef01234567",
				},
			}
			lf := &lockfile.LockFile{Version: lockfile.Version, Checksums: map[string]map[string]string{}}
			lockTarget := lockfile.LockTargetKey("docs", "de")
			lf.Update(lockTarget, "fm:updated", lockfile.KVEntryContent("fm:updated", "2026-08-26"))
			opts := Options{
				ForceTranslate: true,
				LockFile:       lf,
				LockTarget:     "docs",
				IgnoredPatterns: []*regexp.Regexp{
					regexp.MustCompile(`^fm:updated$`),
					regexp.MustCompile(`^fm:program_commits(?:\.|$)`),
				},
				ParallelMode: mode,
			}
			if err := TranslateAllKV(context.Background(), []KVLangTask{task}, opts, MarkdownKVChunkTranslator()); err != nil {
				t.Fatal(err)
			}
			if got := file.Value("fm:updated"); got != "2026-08-26" {
				t.Fatalf("updated = %q, want source value", got)
			}
			if got := file.Value("fm:program_commits.app"); got != "0123456789abcdef0123456789abcdef01234567" {
				t.Fatalf("program commit = %q, want source value", got)
			}
			if lf.Has(lockTarget, "fm:updated") {
				t.Fatal("passthrough key retained a lock entry")
			}
		})
	}
}

func TestTranslateAllKVPreservesIgnoredKeyValue(t *testing.T) {
	file := newTestKVFile([]string{"debug"}, map[string]string{"debug": "target-only"})
	task := KVLangTask{
		Lang:         "de",
		FilePath:     filepath.Join(t.TempDir(), "de.json"),
		File:         file,
		SourceValues: map[string]string{"debug": "source"},
	}
	opts := Options{
		ForceTranslate:  true,
		IgnoredKeys:     []string{"debug"},
		IgnoredPatterns: []*regexp.Regexp{regexp.MustCompile(`^debug$`)},
	}
	if err := TranslateAllKV(context.Background(), []KVLangTask{task}, opts, DefaultKVChunkTranslator()); err != nil {
		t.Fatal(err)
	}
	if got := file.Value("debug"); got != "target-only" {
		t.Fatalf("ignored key = %q, want existing target value", got)
	}
}

func TestTranslateAllKVAppliesPathScopedExactRule(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
exact:
  - id: docs.open
    source: Open
    when:
      path: docs/guide.md
    translations: {de: Öffnen}
`)
	file := newTestKVFile([]string{"title"}, map[string]string{"title": "Stale translation"})
	err := TranslateAllKV(context.Background(), []KVLangTask{{
		Lang:         "de",
		FilePath:     filepath.Join(t.TempDir(), "de.json"),
		File:         file,
		SourceValues: map[string]string{"title": "Open"},
		SourcePath:   "docs/guide.md",
	}}, Options{TargetName: "docs", Format: "markdown", Terminology: catalog}, MarkdownKVChunkTranslator())
	if err != nil {
		t.Fatal(err)
	}
	if got := file.Value("title"); got != "Öffnen" {
		t.Fatalf("path-scoped exact translation = %q", got)
	}
}

func TestMarkdownWholeSectionExactRuleFailsPreflight(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
exact:
  - id: docs.structured
    source: Read [MiniOS Installer](/install).
    translations: {de: "Lesen Sie [MiniOS Installer](/install)."}
`)
	file := newTestKVFile([]string{"sec:0"}, map[string]string{"sec:0": ""})
	err := TranslateAllKV(context.Background(), []KVLangTask{{
		Lang:         "de",
		FilePath:     filepath.Join(t.TempDir(), "de.md"),
		File:         file,
		SourceValues: map[string]string{"sec:0": "Read [MiniOS Installer](/install)."},
		SourcePath:   "docs/guide.md",
	}}, Options{TargetName: "docs", Format: "markdown", Terminology: catalog}, MarkdownKVChunkTranslator())
	if err == nil || !strings.Contains(err.Error(), "spans host-owned Markdown structure") {
		t.Fatalf("structured exact rule error = %v", err)
	}
}

func TestTranslateAllKVRetainsOptionsSourcePath(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
exact:
  - id: ui.open
    source: Open
    when:
      path: src/i18n/en.json
    translations: {de: Öffnen}
`)
	for _, mode := range []string{ParallelSequential, ParallelFullParallel} {
		t.Run(mode, func(t *testing.T) {
			file := newTestKVFile([]string{"title"}, map[string]string{"title": ""})
			task := KVLangTask{Lang: "de", FilePath: filepath.Join(t.TempDir(), "de.json"), File: file, SourceValues: map[string]string{"title": "Open"}}
			opts := Options{TargetName: "ui", Format: "vue-i18n", SourcePath: "src/i18n/en.json", Terminology: catalog, ParallelMode: mode}
			if err := TranslateAllKV(context.Background(), []KVLangTask{task}, opts, DefaultKVChunkTranslator()); err != nil {
				t.Fatal(err)
			}
			if got := file.Value("title"); got != "Öffnen" {
				t.Fatalf("translation = %q", got)
			}
		})
	}
}

func TestMarkdownValidationPreservesParserCodePlaceholders(t *testing.T) {
	source := "Text\n\n<!-- lokit:code-block:0 -->\n"
	if isMarkdownTranslationLikelyValid(source, "Text") {
		t.Fatal("missing parser code placeholder was accepted")
	}
	if !isMarkdownTranslationLikelyValid(source, "Übersetzung\n\n<!-- lokit:code-block:0 -->\n") {
		t.Fatal("preserved parser code placeholder was rejected")
	}
}

func (f *testKVFile) Keys() []string {
	return append([]string(nil), f.keys...)
}

func (f *testKVFile) UntranslatedKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, k := range f.keys {
		if f.values[k] == "" {
			out = append(out, k)
		}
	}
	return out
}

func (f *testKVFile) Get(key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.values[key]
	return value, ok
}

func (f *testKVFile) Set(key, value string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.values[key]; ok {
		f.values[key] = value
		return true
	}
	return false
}

func (f *testKVFile) Stats() (int, int, float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := len(f.keys)
	translated := 0
	for _, k := range f.keys {
		if f.values[k] != "" {
			translated++
		}
	}
	pct := 0.0
	if total > 0 {
		pct = float64(translated) / float64(total) * 100
	}
	return total, translated, pct
}

func (f *testKVFile) WriteFile(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writtenTo = path
	return nil
}

func (f *testKVFile) SourceValues() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.values))
	for k := range f.values {
		out[k] = k
	}
	return out
}

func (f *testKVFile) Value(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.values[key]
}

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

func TestApplyTranslationsPreservesSourceTrailingNewline(t *testing.T) {
	entries := []*po.Entry{{MsgID: "nginx +st=server\n"}}

	applyTranslations(entries, []string{`nginx +st=server\n`}, true)

	if got, want := entries[0].MsgStr, "nginx +st=server\n"; got != want {
		t.Fatalf("MsgStr = %q, want %q", got, want)
	}
}

func TestApplyTranslationsRemovesUnexpectedTrailingNewline(t *testing.T) {
	entries := []*po.Entry{{MsgID: "Single Filters"}}

	applyTranslations(entries, []string{"Einzelfilter\n"}, true)

	if got, want := entries[0].MsgStr, "Einzelfilter"; got != want {
		t.Fatalf("MsgStr = %q, want %q", got, want)
	}
}

func TestApplyTranslationsRemovesUnexpectedLeadingNewline(t *testing.T) {
	entries := []*po.Entry{{MsgID: "These options must be provided:"}}

	applyTranslations(entries, []string{"\nEstas opções devem ser fornecidas:"}, true)

	if got, want := entries[0].MsgStr, "Estas opções devem ser fornecidas:"; got != want {
		t.Fatalf("MsgStr = %q, want %q", got, want)
	}
}

func TestApplyTranslationsPreservesSourceLeadingNewline(t *testing.T) {
	entries := []*po.Entry{{MsgID: "\nIndented block"}}

	applyTranslations(entries, []string{`\nBloco indentado`}, true)

	if got, want := entries[0].MsgStr, "\nBloco indentado"; got != want {
		t.Fatalf("MsgStr = %q, want %q", got, want)
	}
}

func TestApplyTranslationsRestoresInternalEscapedNewlines(t *testing.T) {
	entries := []*po.Entry{{MsgID: "\\f[C]\nminios-live -\\fR\n\n"}}

	applyTranslations(entries, []string{`\f[C]\nminios-live -\fR\n` + "\n"}, true)

	want := "\\f[C]\nminios-live -\\fR\n\n"
	if got := entries[0].MsgStr; got != want {
		t.Fatalf("MsgStr = %q, want %q", got, want)
	}
	if strings.Contains(entries[0].MsgStr, `\n`) {
		t.Fatalf("MsgStr = %q, contains literal newline escape", entries[0].MsgStr)
	}
}

func TestApplyTranslationsPreservesSingleUppercaseGroffToken(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		translation string
	}{
		{
			name:        "bold token with underscore",
			source:      `\f[B]SOURCE_IDENTIFIER\fR`,
			translation: `\f[B]IDENTIFICADOR_FUENTE\fR`,
		},
		{
			name:        "bold token without underscore",
			source:      `\f[B]FEATURE\fR`,
			translation: `\f[B]FUNCAO\fR`,
		},
		{
			name:        "code token",
			source:      `\f[C]SOURCE_IDENTIFIER\fR`,
			translation: `\f[C]IDENTIFICADOR_FUENTE\fR`,
		},
		{
			name:        "snake case token",
			source:      `\f[B]source_identifier\fR`,
			translation: `\f[B]identificador_fuente\fR`,
		},
		{
			name:        "mixed snake case token",
			source:      `\f[B]source_ID\fR`,
			translation: `\f[B]identificador_ID\fR`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entries := []*po.Entry{{MsgID: tc.source}}

			applyTranslations(entries, []string{tc.translation}, true)

			if got, want := entries[0].MsgStr, entries[0].MsgID; got != want {
				t.Fatalf("MsgStr = %q, want %q", got, want)
			}
		})
	}
}

func TestApplyTranslationsTranslatesSingleLowercaseGroffWord(t *testing.T) {
	entries := []*po.Entry{{MsgID: `\f[B]feature\fR`}}

	applyTranslations(entries, []string{`\f[B]funcion\fR`}, true)

	if got, want := entries[0].MsgStr, `\f[B]funcion\fR`; got != want {
		t.Fatalf("MsgStr = %q, want %q", got, want)
	}
}

func TestApplyPluralTranslationsPreservesPluralSourceTrailingNewline(t *testing.T) {
	entries := []*po.Entry{{MsgID: "%d file", MsgIDPlural: "%d files\n"}}
	translations := []pluralTranslation{{plural: []string{`%d файл\n`, `%d файла\n`, `%d файлов\n`}}}

	applyPluralTranslations(entries, translations, true)

	for idx, got := range entries[0].MsgStrPlural {
		if !strings.HasSuffix(got, "\n") {
			t.Fatalf("MsgStrPlural[%d] = %q, want trailing newline", idx, got)
		}
		if strings.HasSuffix(got, `\n`) {
			t.Fatalf("MsgStrPlural[%d] = %q, contains literal newline escape", idx, got)
		}
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

func TestExtractResponseText_OllamaChatFormat(t *testing.T) {
	body := []byte(`{"message":{"role":"assistant","content":"Привет"},"done":true}`)
	text, err := extractResponseText(body)
	if err != nil {
		t.Fatalf("extractResponseText error: %v", err)
	}
	if text != "Привет" {
		t.Fatalf("text = %q, want %q", text, "Привет")
	}
}

func TestBuildHTTPRequest_OllamaNativeEndpoint(t *testing.T) {
	prov := Provider{
		ID:      ProviderOllama,
		Model:   "test-model",
		BaseURL: "http://localhost:11434",
	}

	endpoint, headers, body, err := buildHTTPRequest(prov, "system", "user", formatOllamaNative)
	if err != nil {
		t.Fatalf("buildHTTPRequest error: %v", err)
	}
	if endpoint != "http://localhost:11434/api/chat" {
		t.Fatalf("endpoint = %q", endpoint)
	}
	if headers["Content-Type"] != "application/json" {
		t.Fatalf("content-type header = %q", headers["Content-Type"])
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if payload["model"] != "test-model" {
		t.Fatalf("model = %v", payload["model"])
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

func TestBuildKVUserPrompt_UsesSourceValuesAndFallbackToKey(t *testing.T) {
	keys := []string{"home.title", "menu.help"}
	srcVals := map[string]string{"home.title": "Home"}
	prompt := buildKVUserPrompt(keys, srcVals, "English", "Russian")

	if !strings.Contains(prompt, "Translate these strings from English to Russian") {
		t.Fatalf("prompt missing language header: %q", prompt)
	}
	if !strings.Contains(prompt, `ID `+kvTranslationIDs(keys)[0]+`: "Home"`) {
		t.Fatalf("prompt missing source value: %q", prompt)
	}
	if !strings.Contains(prompt, `ID `+kvTranslationIDs(keys)[1]+`: "menu.help"`) {
		t.Fatalf("prompt missing fallback key: %q", prompt)
	}
}

func TestBuildI18NextUserPrompt_UsesKeysAsSource(t *testing.T) {
	keys := []string{"Save", "Cancel"}
	prompt := buildI18NextUserPrompt(keys, nil, "English", "German")

	if !strings.Contains(prompt, "Translate these UI strings from English to German") {
		t.Fatalf("prompt missing language header: %q", prompt)
	}
	ids := kvTranslationIDs(keys)
	if !strings.Contains(prompt, `ID `+ids[0]+`: "Save"`) || !strings.Contains(prompt, `ID `+ids[1]+`: "Cancel"`) {
		t.Fatalf("prompt missing key list: %q", prompt)
	}
}

func TestBuildMarkdownUserPrompt_IncludesMarkdownRules(t *testing.T) {
	keys := []string{"intro"}
	srcVals := map[string]string{"intro": "# Welcome\nText"}
	prompt := buildMarkdownUserPrompt(keys, srcVals, "English", "French")

	if !strings.Contains(prompt, "preserve all formatting") {
		t.Fatalf("markdown rules missing from prompt: %q", prompt)
	}
	if !strings.Contains(prompt, `ID `+kvTranslationIDs(keys)[0]+`: "# Welcome\nText"`) {
		t.Fatalf("prompt missing escaped markdown source: %q", prompt)
	}
}

func TestParseTranslations_SingleItemRejectsRawText(t *testing.T) {
	content := "mermaid\\ngraph TD\\nA-->B"
	_, err := parseTranslations(content, 1)
	if err == nil {
		t.Fatal("expected parseTranslations to reject non-JSON raw text")
	}
}

func TestParseTranslations_SingleItemRejectsWrapperText(t *testing.T) {
	content := "Here is the translation: Privet"
	_, err := parseTranslations(content, 1)
	if err == nil {
		t.Fatal("expected parseTranslations to reject wrapper text fallback")
	}
}

func TestParseTranslations_SingleItemAcceptsJSONString(t *testing.T) {
	content := `"Privet"`
	translations, err := parseTranslations(content, 1)
	if err != nil {
		t.Fatalf("parseTranslations returned error: %v", err)
	}
	if len(translations) != 1 || translations[0] != "Privet" {
		t.Fatalf("unexpected parsed translations: %#v", translations)
	}
}

func TestParseTranslations_PreservesGroffFontEscapes(t *testing.T) {
	content := `["\f[B]MENU_LANG\f[R]: \\[lq]multilang\\[rq]\fR"]`
	translations, err := parseTranslations(content, 1)
	if err != nil {
		t.Fatalf("parseTranslations returned error: %v", err)
	}
	want := `\f[B]MENU_LANG\f[R]: \[lq]multilang\[rq]\fR`
	if len(translations) != 1 || translations[0] != want {
		t.Fatalf("unexpected parsed translations: %#v, want %q", translations, want)
	}
}

func TestParseTranslations_UsesFirstCompleteJSONArray(t *testing.T) {
	content := `["# CondinAPT\n\nText with [brackets] and \\\"quotes\\\""] CondinAPT trailing text`
	translations, err := parseTranslations(content, 1)
	if err != nil {
		t.Fatalf("parseTranslations returned error: %v", err)
	}
	if len(translations) != 1 || !strings.Contains(translations[0], "CondinAPT") {
		t.Fatalf("unexpected parsed translations: %#v", translations)
	}
}

func TestParseTranslationsRejectsWrongItemCount(t *testing.T) {
	if _, err := parseTranslations(`["one"]`, 2); err == nil {
		t.Fatal("expected short positional response to be rejected")
	}
	if _, err := parseTranslations(`["one","two","three"]`, 2); err == nil {
		t.Fatal("expected long positional response to be rejected")
	}
}

func TestParseIdentifiedStringTranslationsMapsReorderedResponse(t *testing.T) {
	ids := []string{"msg-first", "msg-second", "msg-third"}
	content := `[
		{"id":"msg-third","translation":"three"},
		{"id":"msg-first","translation":"one"},
		{"id":"msg-second","translation":"two"}
	]`

	translations, err := parseIdentifiedStringTranslations(content, ids)
	if err != nil {
		t.Fatalf("parseIdentifiedStringTranslations returned error: %v", err)
	}
	want := []string{"one", "two", "three"}
	for i := range want {
		if translations[i] != want[i] {
			t.Fatalf("translation[%d] = %q, want %q", i, translations[i], want[i])
		}
	}
}

func TestParseIdentifiedTranslationsRejectsMissingItem(t *testing.T) {
	content := `[{"id":"msg-first","translation":"one"}]`
	if _, err := parseIdentifiedStringTranslations(content, []string{"msg-first", "msg-second"}); err == nil {
		t.Fatal("expected missing identified translation to be rejected")
	}
}

func TestParseIdentifiedTranslationsRejectsDuplicateID(t *testing.T) {
	content := `[
		{"id":"msg-first","translation":"one"},
		{"id":"msg-first","translation":"two"}
	]`
	if _, err := parseIdentifiedStringTranslations(content, []string{"msg-first", "msg-second"}); err == nil {
		t.Fatal("expected duplicate identified translation to be rejected")
	}
}

func TestParseIdentifiedTranslationsRejectsUnknownID(t *testing.T) {
	content := `[
		{"id":"msg-first","translation":"one"},
		{"id":"msg-unknown","translation":"two"}
	]`
	if _, err := parseIdentifiedStringTranslations(content, []string{"msg-first", "msg-second"}); err == nil {
		t.Fatal("expected unknown identified translation to be rejected")
	}
}

func TestValidatePOTranslationsRejectsMissingPythonBracePlaceholder(t *testing.T) {
	entries := []*po.Entry{{
		MsgID: "{operation} data flow direction",
		Flags: []string{"python-brace-format"},
	}}
	if err := validatePOTranslations(entries, []string{"Direction du flux de données"}); err == nil {
		t.Fatal("expected missing Python brace placeholder to be rejected")
	}
}

func TestValidatePOTranslationsRejectsChangedPrintfPlaceholder(t *testing.T) {
	entries := []*po.Entry{{
		MsgID: "%s copied: %d files",
		Flags: []string{"c-format"},
	}}
	if err := validatePOTranslations(entries, []string{"%s kopiert: Dateien"}); err == nil {
		t.Fatal("expected missing printf placeholder to be rejected")
	}
}

func TestValidatePOTranslationsAcceptsPreservedPlaceholders(t *testing.T) {
	entries := []*po.Entry{
		{MsgID: "{operation} data flow direction", Flags: []string{"python-brace-format"}},
		{MsgID: "%s copied: %d files", Flags: []string{"c-format"}},
	}
	translations := []string{
		"Direction du flux de données {operation}",
		"%s kopiert: %d Dateien",
	}
	if err := validatePOTranslations(entries, translations); err != nil {
		t.Fatalf("preserved placeholders rejected: %v", err)
	}
}

func TestTranslationValidationPreservesShellLineContinuations(t *testing.T) {
	source := "lokit translate \\\n  --prompt text"
	changed := "lokit translate\n  --prompt text"

	if err := validatePOTranslations([]*po.Entry{{MsgID: source}}, []string{changed}); err == nil {
		t.Fatal("expected missing PO shell line continuation to be rejected")
	}
	if err := validateKVTranslations([]string{"command"}, map[string]string{"command": source}, []string{changed}); err == nil {
		t.Fatal("expected missing KV shell line continuation to be rejected")
	}
	if err := validatePOTranslations([]*po.Entry{{MsgID: source}}, []string{source}); err != nil {
		t.Fatalf("preserved shell line continuation rejected: %v", err)
	}
}

func TestValidateKVTranslationsRejectsMissingPlaceholders(t *testing.T) {
	keys := []string{"welcome", "progress"}
	sources := map[string]string{
		"welcome":  "Welcome, {{name}}",
		"progress": "%1$d of %2$d files",
	}
	if err := validateKVTranslations(keys, sources, []string{"Willkommen", "%1$d Dateien"}); err == nil {
		t.Fatal("expected missing KV placeholders to be rejected")
	}
}

func TestValidateKVTranslationsAcceptsReorderedTextWithPlaceholders(t *testing.T) {
	keys := []string{"welcome", "progress"}
	sources := map[string]string{
		"welcome":  "Welcome, {{name}}",
		"progress": "%1$d of %2$d files",
	}
	translations := []string{"Willkommen, {{name}}", "%1$d von %2$d Dateien"}
	if err := validateKVTranslations(keys, sources, translations); err != nil {
		t.Fatalf("preserved KV placeholders rejected: %v", err)
	}
}

func TestNormalizePOTranslationNewlines_RestoresGroffFontEscapes(t *testing.T) {
	source := `\f[B]MENU_LANG\f[R]: \[lq]multilang\[rq]\fR`
	translation := "\f[B]MENU_LANG\f[R]: \\[lq]multilang\\[rq]\fR"
	want := source
	if got := normalizePOTranslationNewlines(source, translation); got != want {
		t.Fatalf("normalizePOTranslationNewlines() = %q, want %q", got, want)
	}
}

func TestIsMarkdownTranslationLikelyValid_HeadingMismatch(t *testing.T) {
	src := "## Section\n\nParagraph"
	dst := "Abschnitt\n\nAbsatz"
	if isMarkdownTranslationLikelyValid(src, dst) {
		t.Fatal("expected heading mismatch to be invalid")
	}
}

func TestIsMarkdownTranslationLikelyValid_CodeFenceMissing(t *testing.T) {
	src := "### Example\n\n```bash\necho hi\n```"
	dst := "### Beispiel\n\nCode block omitted"
	if isMarkdownTranslationLikelyValid(src, dst) {
		t.Fatal("expected missing fenced code block to be invalid")
	}
}

func TestIsMarkdownTranslationLikelyValid_ValidStructure(t *testing.T) {
	src := "### Example\n\n```bash\necho hi\n```\n\nText"
	dst := "### Beispiel\n\n```bash\necho hi\n```\n\nText"
	if !isMarkdownTranslationLikelyValid(src, dst) {
		t.Fatal("expected markdown translation with preserved structure to be valid")
	}
}

func TestMarkdownPatchPipelineKeepsResourcesHostOwned(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
terms:
  - id: brand.minios
    source: MiniOS
    preserve: true
    case_sensitive: true
`)
	source := "See [MiniOS Configurator](/docs/Preconfiguring-MiniOS \"Guide\") and <https://example.test/MiniOS>.\n\n[ref]: /private/MiniOS"
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		defer r.Body.Close()
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(request)
		for _, hidden := range []string{"/docs/Preconfiguring-MiniOS", "example.test/MiniOS", "/private/MiniOS", "__LOKIT_LINK_DESTINATION_", "MiniOS"} {
			if bytes.Contains(body, []byte(hidden)) {
				t.Errorf("provider request exposes host-owned data %q: %s", hidden, body)
			}
		}
		if !bytes.Contains(body, []byte("Preserve every __LOKIT_PRESERVE_TERM_")) || !bytes.Contains(body, []byte("same field")) {
			t.Errorf("provider request lacks field-owned terminology contract: %s", body)
		}
		prompt := request.Messages[len(request.Messages)-1].Content
		array, ok := firstJSONArray(prompt)
		if !ok {
			t.Fatalf("Markdown patch prompt has no JSON view: %q", prompt)
		}
		var units []struct {
			ID     string `json:"id"`
			Fields []struct {
				ID     string `json:"id"`
				Source string `json:"source"`
				Kind   string `json:"kind"`
			} `json:"fields"`
		}
		if err := json.Unmarshal([]byte(array), &units); err != nil {
			t.Fatalf("decode provider view: %v\n%s", err, array)
		}
		response := make([]map[string]any, len(units))
		for i, unit := range units {
			values := make(map[string]string)
			ownedToken := ""
			for _, field := range unit.Fields {
				if token := preservedTermTokenPattern.FindString(field.Source); token != "" {
					ownedToken = token
					break
				}
			}
			for _, field := range unit.Fields {
				value := field.Source
				if token := preservedTermTokenPattern.FindString(value); token != "" {
					value = "Конфигураторе " + token
				} else if value == "See " {
					value = "Подробнее см. в "
				} else if strings.HasPrefix(value, " and ") {
					value = " и " + strings.TrimPrefix(value, " and ")
				}
				values[field.ID] = value
			}
			if requests <= 2 && ownedToken != "" && len(unit.Fields) > 0 {
				for _, field := range unit.Fields {
					values[field.ID] = strings.ReplaceAll(values[field.ID], ownedToken, "")
				}
				values[unit.Fields[0].ID] += " " + ownedToken
			}
			if requests >= 3 && len(unit.Fields) != 1 {
				t.Errorf("field repair exposed %d fields, want one", len(unit.Fields))
			}
			response[i] = map[string]any{"id": unit.ID, "values": values}
		}
		content, _ := json.Marshal(response)
		envelope, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}}}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(envelope)
	}))
	defer ts.Close()

	file := newTestKVFile([]string{"sec:3"}, map[string]string{"sec:3": ""})
	tasks := []KVLangTask{{
		Lang:         "ru",
		LangName:     "Russian",
		FilePath:     "ru.md",
		File:         file,
		SourceValues: map[string]string{"sec:3": source},
	}}
	opts := Options{
		Provider:           Provider{ID: ProviderCustomOpenAI, BaseURL: ts.URL, Model: "test"},
		SourceLanguage:     "en",
		SourceLanguageName: "English",
		Language:           "ru",
		LanguageName:       "Russian",
		TargetName:         "docs",
		Format:             "markdown",
		Terminology:        catalog,
		ParallelMode:       ParallelSequential,
	}
	if err := TranslateAllKV(context.Background(), tasks, opts, MarkdownKVChunkTranslator()); err != nil {
		t.Fatalf("TranslateAllKV: %v", err)
	}
	got := file.Value("sec:3")
	if !strings.Contains(got, "[Конфигураторе MiniOS](/docs/Preconfiguring-MiniOS \"Guide\")") ||
		!strings.Contains(got, "<https://example.test/MiniOS>") || !strings.Contains(got, "[ref]: /private/MiniOS") {
		t.Fatalf("host-owned resources or translated label changed: %q", got)
	}
	if requests != 4 {
		t.Fatalf("provider requests = %d, want two invalid plan patches and two isolated field repairs", requests)
	}
}

func TestMarkdownPatchResponseRequiresExactFields(t *testing.T) {
	prepared, err := prepareMarkdownPlans(
		[]string{"sec:0"},
		map[string]string{"sec:0": "Text [label](/safe)."},
		Options{Language: "de", LanguageName: "German", Format: "markdown"},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := &prepared[0]
	if len(plan.fields) == 0 {
		t.Fatal("prepared plan has no fields")
	}
	duplicate := fmt.Sprintf(`[{"id":%q,"values":{%q:"one",%q:"two"}}]`, plan.id, plan.fields[0].id, plan.fields[0].id)
	if _, err := parseMarkdownPatchResponse(duplicate, []*preparedMarkdownPlan{plan}); err == nil {
		t.Fatal("duplicate Markdown patch field was accepted")
	}
	unknown := fmt.Sprintf(`[{"id":%q,"values":{"unknown":"text"}}]`, plan.id)
	if _, err := parseMarkdownPatchResponse(unknown, []*preparedMarkdownPlan{plan}); err == nil {
		t.Fatal("unknown Markdown patch field was accepted")
	}
}

func TestMarkdownPlanGroupsBoundFallbackWithoutSplittingContext(t *testing.T) {
	plan := &preparedMarkdownPlan{id: "unit", fieldByID: make(map[string]preparedMarkdownField)}
	for i := 0; i < 100; i++ {
		field := preparedMarkdownField{id: fmt.Sprintf("f%d", i), context: fmt.Sprintf("c%d", i)}
		plan.fields = append(plan.fields, field)
		plan.fieldByID[field.id] = field
	}
	groups := markdownPlanGroups(plan, 40)
	if len(groups) != 3 {
		t.Fatalf("bounded group count = %d, want 3", len(groups))
	}
	if len(groups[0].fields) != 40 || len(groups[1].fields) != 40 || len(groups[2].fields) != 20 {
		t.Fatalf("bounded groups = %#v", []int{len(groups[0].fields), len(groups[1].fields), len(groups[2].fields)})
	}

	plan = &preparedMarkdownPlan{id: "unit", fieldByID: make(map[string]preparedMarkdownField)}
	for i := 0; i < 45; i++ {
		field := preparedMarkdownField{id: fmt.Sprintf("f%d", i), context: "one-table-cell"}
		plan.fields = append(plan.fields, field)
		plan.fieldByID[field.id] = field
	}
	groups = markdownPlanGroups(plan, 40)
	if len(groups) != 1 || len(groups[0].fields) != 45 {
		t.Fatalf("shared context was split across %d groups", len(groups))
	}
}

func TestInvalidMarkdownPatchFieldsIncludesHostRenderFailures(t *testing.T) {
	prepared, err := prepareMarkdownPlans(
		[]string{"sec:0"},
		map[string]string{"sec:0": "Before [label](/safe) after."},
		Options{Language: "de", LanguageName: "German", Format: "markdown"},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := &prepared[0]
	patches := make(map[string]string, len(plan.fieldByID))
	for id, field := range plan.fieldByID {
		patches[id] = field.source
	}
	var badID string
	for _, field := range plan.fields {
		if field.source == "Before " {
			badID = field.id
			patches[field.id] = "1. Schritt"
			break
		}
	}
	if badID == "" {
		t.Fatal("line-leading field was not found")
	}
	if _, err := renderMarkdownPatch(plan, patches); err == nil {
		t.Fatal("ordered-list structure change passed full render")
	}
	invalid := invalidMarkdownPatchFields(plan, patches)
	if len(invalid) != 1 || invalid[0].id != badID {
		t.Fatalf("invalid render fields = %#v, want %q", invalid, badID)
	}
}

func TestCollectProtectedRulesRejectsCrossEntryLiteral(t *testing.T) {
	rule := terminology.TermMatch{Source: "MiniOS", Match: terminology.MatchWord, CaseSensitive: true, Preserve: true}
	protected := collectProtectedRules([][]terminology.TermMatch{{rule}, nil})
	if err := validateRawPreservedTerms("MiniOS added elsewhere", nil, protected); err == nil {
		t.Fatal("request-wide protected literal was accepted in another KV or PO entry")
	}
}

func TestMarkdownMasksProtectedLiteralOutsideSelectorContext(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
terms:
  - id: scoped.minios
    source: MiniOS
    preserve: true
    case_sensitive: true
    when:
      context: "Paragraph:0"
`)
	prepared, err := prepareMarkdownPlans(
		[]string{"sec:0"},
		map[string]string{"sec:0": "MiniOS first.\n\nMiniOS second."},
		Options{Language: "de", LanguageName: "German", Format: "markdown", Terminology: catalog},
	)
	if err != nil {
		t.Fatal(err)
	}
	tokens := 0
	for _, field := range prepared[0].fields {
		if strings.Contains(field.masked, "MiniOS") {
			t.Fatalf("field %s exposed selector-scoped protected literal: %q", field.id, field.masked)
		}
		tokens += len(field.preserved)
	}
	if tokens != 2 {
		t.Fatalf("owned Markdown token count = %d, want 2", tokens)
	}
}

func TestMarkdownPatchFrontmatterResourcesAreHostOwned(t *testing.T) {
	prepared, err := prepareMarkdownPlans(
		[]string{"fm:hero.text", "fm:hero.image.src", "fm:hero.actions.0.link", "fm:updated"},
		map[string]string{
			"fm:hero.text":           "Fast. Simple. Reliable.",
			"fm:hero.image.src":      "/minios-hero.svg",
			"fm:hero.actions.0.link": "/getting-started/Quick-Start",
			"fm:updated":             "2026-08-31",
		},
		Options{Language: "ru", LanguageName: "Russian", Format: "markdown"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared[0].fields) != 1 {
		t.Fatalf("translatable frontmatter text fields = %#v", prepared[0].fields)
	}
	for i := 1; i < len(prepared); i++ {
		if len(prepared[i].fields) != 0 {
			t.Fatalf("frontmatter resource %q became provider-owned: %#v", prepared[i].key, prepared[i].fields)
		}
	}
	prompt := buildMarkdownPatchPrompt([]*preparedMarkdownPlan{&prepared[0]}, Options{SourceLanguageName: "English", LanguageName: "Russian"})
	if strings.Contains(prompt, "/minios-hero.svg") || strings.Contains(prompt, "Quick-Start") || !strings.Contains(markdownPatchSystemPrompt(""), "Preserve every __LOKIT_PRESERVE_TERM_") {
		t.Fatalf("frontmatter resources exposed or protected-term contract missing: %q", prompt)
	}
}

func TestMarkdownPatchUnknownTextFrontmatterTranslatesByDefault(t *testing.T) {
	prepared, err := prepareMarkdownPlans(
		[]string{"fm:subtitle"},
		map[string]string{"fm:subtitle": "Portable Linux for everyday work"},
		Options{Language: "de", LanguageName: "German", Format: "markdown"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared[0].fieldByID) != 1 {
		t.Fatalf("custom textual frontmatter was not provider-owned: %#v", prepared[0])
	}
}

func TestMarkdownHostOwnedFrontmatterRepairsExistingValue(t *testing.T) {
	file := newTestKVFile([]string{"fm:link"}, map[string]string{"fm:link": "/stale-route"})
	err := TranslateAllKV(context.Background(), []KVLangTask{{
		Lang:         "de",
		FilePath:     filepath.Join(t.TempDir(), "de.md"),
		File:         file,
		SourceValues: map[string]string{"fm:link": "/source-route"},
	}}, Options{Format: "markdown"}, MarkdownKVChunkTranslator())
	if err != nil {
		t.Fatal(err)
	}
	if got := file.Value("fm:link"); got != "/source-route" {
		t.Fatalf("host-owned frontmatter = %q, want source value", got)
	}
}

func TestMarkdownFrontmatterMachineValuesStayHostOwned(t *testing.T) {
	for key, value := range map[string]string{
		"fm:slug":   "quick-start",
		"fm:asset":  "hero.svg",
		"fm:locale": "pt-BR",
		"fm:mode":   "0x1f",
	} {
		if !markdownFrontmatterHostField(key, value) {
			t.Fatalf("machine frontmatter was treated as prose: %s=%q", key, value)
		}
	}
	if markdownFrontmatterHostField("fm:subtitle", "Portable Linux for everyday work") {
		t.Fatal("unknown prose frontmatter was treated as a resource")
	}
	for key, value := range map[string]string{"fm:badge": "new", "fm:cta": "go"} {
		if markdownFrontmatterHostField(key, value) {
			t.Fatalf("short prose frontmatter was treated as a locale: %s=%q", key, value)
		}
	}
	if !markdownFrontmatterHostField("fm:locale", "new") {
		t.Fatal("locale key was not host-owned")
	}
}

func TestMarkdownPatchFrontmatterExcerptUsesResourcePlan(t *testing.T) {
	prepared, err := prepareMarkdownPlans(
		[]string{"fm:excerpt"},
		map[string]string{"fm:excerpt": "Read [the documentation](https://minios.dev/docs) before continuing."},
		Options{Language: "ru", LanguageName: "Russian", Format: "markdown"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared[0].fields) == 0 {
		t.Fatal("frontmatter excerpt has no mutable fields")
	}
	prompt := buildMarkdownPatchPrompt([]*preparedMarkdownPlan{&prepared[0]}, Options{SourceLanguageName: "English", LanguageName: "Russian"})
	if strings.Contains(prompt, "https://minios.dev/docs") {
		t.Fatalf("excerpt URL exposed to provider: %q", prompt)
	}
	patches := make(map[string]string)
	for _, field := range prepared[0].fields {
		patches[field.id] = field.masked
	}
	rendered, err := renderMarkdownPatch(&prepared[0], patches)
	if err != nil {
		t.Fatal(err)
	}
	if rendered != prepared[0].source {
		t.Fatalf("excerpt identity render = %q, want %q", rendered, prepared[0].source)
	}
}

func TestMarkdownPatchExactTranslationUsesOwnedProtectedSlot(t *testing.T) {
	catalog := loadTestTerminology(t, `version: 1
exact:
  - id: app.minios-installer
    source: MiniOS Installer
    translations: {ru: Установщик MiniOS}
terms:
  - id: brand.minios
    source: MiniOS
    preserve: true
    case_sensitive: true
`)
	prepared, err := prepareMarkdownPlans(
		[]string{"sec:0"},
		map[string]string{"sec:0": "# MiniOS Installer"},
		Options{Language: "ru", LanguageName: "Russian", Format: "markdown", Terminology: catalog},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared[0].fieldByID) != 0 {
		t.Fatalf("exact field was sent to provider: %#v", prepared[0].fieldByID)
	}
	rendered, err := renderMarkdownPatch(&prepared[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if rendered != "# Установщик MiniOS" {
		t.Fatalf("exact protected render = %q", rendered)
	}
}

func TestMarkdownPatchRejectsAllEmptyContext(t *testing.T) {
	prepared, err := prepareMarkdownPlans(
		[]string{"sec:0"},
		map[string]string{"sec:0": "Read `code` carefully."},
		Options{Language: "de", LanguageName: "German", Format: "markdown"},
	)
	if err != nil {
		t.Fatal(err)
	}
	patches := make(map[string]string)
	for _, field := range prepared[0].fields {
		patches[field.id] = ""
	}
	if _, err := renderMarkdownPatch(&prepared[0], patches); err == nil {
		t.Fatal("all-empty Markdown context was accepted")
	}
}

func TestMarkdownMalformedBatchFallsBackToSingletonPlans(t *testing.T) {
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		defer r.Body.Close()
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		patchPrompt := ""
		for _, message := range request.Messages {
			if strings.Contains(message.Content, "Translate the mutable Markdown text fields") {
				patchPrompt = message.Content
				break
			}
		}
		var units []struct {
			ID     string `json:"id"`
			Fields []struct {
				ID     string `json:"id"`
				Source string `json:"source"`
			} `json:"fields"`
		}
		array, ok := firstJSONArray(patchPrompt)
		if !ok || json.Unmarshal([]byte(array), &units) != nil {
			t.Fatalf("invalid patch request: %#v", request.Messages)
		}
		var content []byte
		if len(units) > 1 {
			content = []byte(`[{"id":"malformed","values":{}}]`)
		} else {
			values := make(map[string]string)
			for _, field := range units[0].Fields {
				values[field.ID] = "Перевод " + field.Source
			}
			content, _ = json.Marshal([]map[string]any{{"id": units[0].ID, "values": values}})
		}
		envelope, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}}}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(envelope)
	}))
	defer ts.Close()

	translations, err := translateMarkdownPatchChunk(
		context.Background(),
		[]string{"sec:0", "sec:1"},
		map[string]string{"sec:0": "First paragraph.", "sec:1": "Second paragraph."},
		DefaultSystemPrompt,
		Options{
			Provider:           Provider{ID: ProviderCustomOpenAI, BaseURL: ts.URL, Model: "test"},
			SourceLanguageName: "English",
			Language:           "ru",
			LanguageName:       "Russian",
			Format:             "markdown",
			MaxRetries:         1,
		},
		&rateLimitState{},
	)
	if err != nil {
		t.Fatalf("translateMarkdownPatchChunk: %v", err)
	}
	if len(translations) != 2 || !strings.HasPrefix(translations[0], "Перевод ") || !strings.HasPrefix(translations[1], "Перевод ") {
		t.Fatalf("singleton translations = %#v", translations)
	}
	if requests != 4 {
		t.Fatalf("provider requests = %d, want 2 malformed batch attempts and 2 singleton attempts", requests)
	}
}

func TestI18NextFile_SetAndSourceValues(t *testing.T) {
	f := &i18next.File{
		Translations: map[string]string{
			"Save":   "",
			"Cancel": "",
		},
	}

	ok := f.Set("Save", "Сохранить")
	if !ok {
		t.Fatal("expected Set on existing key to return true")
	}
	if got := f.Translations["Save"]; got != "Сохранить" {
		t.Fatalf("translation was not updated: got %q", got)
	}

	if f.Set("Unknown", "X") {
		t.Fatal("Set returned true for unknown key")
	}
	if _, ok := f.Translations["Unknown"]; ok {
		t.Fatal("Set inserted unknown key")
	}

	sourceValues := f.SourceValues()
	if sourceValues["Save"] != "Save" || sourceValues["Cancel"] != "Cancel" {
		t.Fatalf("unexpected source values: %#v", sourceValues)
	}
}

func TestTranslateAllKVSequential_TranslatesAndSaves(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(identifiedKVProviderResponse([]string{"a", "b"}, []string{"Привет", "Пока"})))
	}))
	defer ts.Close()

	f := newTestKVFile([]string{"a", "b"}, map[string]string{"a": "", "b": ""})
	tasks := []KVLangTask{{
		Lang:         "ru",
		LangName:     "Russian",
		FilePath:     "ru.yaml",
		File:         f,
		SourceValues: map[string]string{"a": "Hello", "b": "Bye"},
	}}

	opts := Options{
		Provider: Provider{
			ID:      ProviderCustomOpenAI,
			BaseURL: ts.URL,
			Model:   "test-model",
		},
		ParallelMode: ParallelSequential,
	}

	if err := TranslateAllKV(context.Background(), tasks, opts, DefaultKVChunkTranslator()); err != nil {
		t.Fatalf("TranslateAllKV error: %v", err)
	}

	if got := f.Value("a"); got != "Привет" {
		t.Fatalf("value[a] = %q", got)
	}
	if got := f.Value("b"); got != "Пока" {
		t.Fatalf("value[b] = %q", got)
	}
	if f.writtenTo != "ru.yaml" {
		t.Fatalf("file not saved to expected path: %q", f.writtenTo)
	}
}

func TestTranslateAllKVSequentialMapsReorderedResponseByID(t *testing.T) {
	keys := []string{"first", "second", "third"}
	ids := kvTranslationIDs(keys)
	items := []identifiedTranslation{
		{ID: ids[2], Translation: json.RawMessage(`"three"`)},
		{ID: ids[0], Translation: json.RawMessage(`"one"`)},
		{ID: ids[1], Translation: json.RawMessage(`"two"`)},
	}
	content, _ := json.Marshal(items)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		_, _ = io.Copy(io.Discard, r.Body)
		response := map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	file := newTestKVFile(keys, map[string]string{"first": "", "second": "", "third": ""})
	tasks := []KVLangTask{{
		Lang:         "de",
		LangName:     "German",
		FilePath:     "de.json",
		File:         file,
		SourceValues: map[string]string{"first": "First", "second": "Second", "third": "Third"},
	}}
	opts := Options{
		Provider:     Provider{ID: ProviderCustomOpenAI, BaseURL: ts.URL, Model: "test-model"},
		ParallelMode: ParallelSequential,
	}

	if err := TranslateAllKV(context.Background(), tasks, opts, DefaultKVChunkTranslator()); err != nil {
		t.Fatalf("TranslateAllKV error: %v", err)
	}
	for key, want := range map[string]string{"first": "one", "second": "two", "third": "three"} {
		if got := file.Value(key); got != want {
			t.Fatalf("value[%s] = %q, want %q", key, got, want)
		}
	}
}

func TestTranslateAllKVSequential_SkipsMissingOrEmptySourceValues(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(identifiedKVProviderResponse([]string{"name"}, []string{"Привет"})))
	}))
	defer ts.Close()

	f := newTestKVFile([]string{"name", "longDescription"}, map[string]string{"name": "", "longDescription": ""})
	tasks := []KVLangTask{{
		Lang:     "ru",
		LangName: "Russian",
		FilePath: "ru.json",
		File:     f,
		SourceValues: map[string]string{
			"name":            "Example App",
			"longDescription": "",
		},
	}}

	opts := Options{
		Provider: Provider{
			ID:      ProviderCustomOpenAI,
			BaseURL: ts.URL,
			Model:   "test-model",
		},
		ParallelMode: ParallelSequential,
	}

	if err := TranslateAllKV(context.Background(), tasks, opts, DefaultKVChunkTranslator()); err != nil {
		t.Fatalf("TranslateAllKV error: %v", err)
	}

	if got := f.Value("name"); got != "Привет" {
		t.Fatalf("value[name] = %q, want translated value", got)
	}
	if got := f.Value("longDescription"); got != "" {
		t.Fatalf("value[longDescription] = %q, want empty (skipped)", got)
	}
}

func TestTranslateAllKVFullParallel_TranslatesAllTasks(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		prompt := request.Messages[len(request.Messages)-1].Content
		key := "k1"
		if strings.Contains(prompt, `"Two"`) {
			key = "k2"
		}
		_, _ = w.Write([]byte(identifiedKVProviderResponse([]string{key}, []string{"OK"})))
	}))
	defer ts.Close()

	f1 := newTestKVFile([]string{"k1"}, map[string]string{"k1": ""})
	f2 := newTestKVFile([]string{"k2"}, map[string]string{"k2": ""})
	tasks := []KVLangTask{
		{Lang: "fr", LangName: "French", FilePath: "fr.yaml", File: f1, SourceValues: map[string]string{"k1": "One"}},
		{Lang: "de", LangName: "German", FilePath: "de.yaml", File: f2, SourceValues: map[string]string{"k2": "Two"}},
	}

	opts := Options{
		Provider: Provider{
			ID:      ProviderCustomOpenAI,
			BaseURL: ts.URL,
			Model:   "test-model",
		},
		ParallelMode:  ParallelFullParallel,
		MaxConcurrent: 2,
	}

	if err := TranslateAllKV(context.Background(), tasks, opts, DefaultKVChunkTranslator()); err != nil {
		t.Fatalf("TranslateAllKV error: %v", err)
	}

	if got := f1.Value("k1"); got != "OK" {
		t.Fatalf("f1 value = %q", got)
	}
	if got := f2.Value("k2"); got != "OK" {
		t.Fatalf("f2 value = %q", got)
	}
	if f1.writtenTo != "fr.yaml" || f2.writtenTo != "de.yaml" {
		t.Fatalf("files not saved: fr=%q de=%q", f1.writtenTo, f2.writtenTo)
	}
}

func TestTranslateFullParallelMapsReorderedResponsesByID(t *testing.T) {
	idPattern := regexp.MustCompile(`ID (msg-[^:]+):`)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		userPrompt := request.Messages[len(request.Messages)-1].Content
		matches := idPattern.FindAllStringSubmatch(userPrompt, -1)
		items := make([]map[string]string, 0, len(matches))
		for i := len(matches) - 1; i >= 0; i-- {
			id := matches[i][1]
			items = append(items, map[string]string{"id": id, "translation": "translated-" + id})
		}
		content, _ := json.Marshal(items)
		response := map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	makePO := func(msgids ...string) *po.File {
		file := po.NewFile()
		for _, msgid := range msgids {
			file.Entries = append(file.Entries, &po.Entry{MsgID: msgid})
		}
		return file
	}
	fr := makePO("Choose source", "Browse images", "PLAN", "Backup plan")
	ru := makePO("Restore plan", "Source partition", "Partition layout", "Choose disk")
	tmp := t.TempDir()
	tasks := []translationTask{
		{lang: "fr", poFile: fr, poPath: filepath.Join(tmp, "fr.po")},
		{lang: "ru", poFile: ru, poPath: filepath.Join(tmp, "ru.po")},
	}
	opts := Options{
		Provider:      Provider{ID: ProviderCustomOpenAI, BaseURL: ts.URL, Model: "test-model"},
		ParallelMode:  ParallelFullParallel,
		MaxConcurrent: 4,
		ChunkSize:     2,
	}

	if err := TranslateMulti(context.Background(), tasks, opts); err != nil {
		t.Fatalf("TranslateMulti returned error: %v", err)
	}
	for _, file := range []*po.File{fr, ru} {
		for _, entry := range file.Entries {
			id := entryTranslationIDs([]*po.Entry{entry})[0]
			if want := "translated-" + id; entry.MsgStr != want {
				t.Fatalf("MsgStr for %q = %q, want %q", entry.MsgID, entry.MsgStr, want)
			}
		}
	}
}

func TestCallOpenAI_RejectsNonOAuthModel(t *testing.T) {
	prov := Provider{
		ID:    ProviderOpenAI,
		Model: "gpt-4o",
	}

	_, err := callOpenAI(context.Background(), prov, "system", "user", nil, 0, false)
	if err == nil {
		t.Fatal("expected error for non-OAuth model without API key")
	}
	if !strings.Contains(err.Error(), "OAuth-compatible OpenAI model") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCollectEntries_RetranslateIgnoresLock(t *testing.T) {
	f := po.NewFile()
	e := &po.Entry{MsgID: "Hello", MsgStr: "Hallo"}
	f.Entries = append(f.Entries, e)

	lf := &lockfile.LockFile{Version: lockfile.Version, Checksums: map[string]map[string]string{}}
	lockTarget := lockfile.LockTargetKey("ui", "de")
	lf.Update(lockTarget, lockfile.POEntryKey(e.MsgID, e.MsgCtxt), lockfile.POEntryContent(e.MsgID, e.MsgIDPlural))

	opts := Options{
		RetranslateExisting: true,
		LockFile:            lf,
		LockTarget:          "ui",
		Language:            "de",
	}

	entries := collectEntries(f, opts)
	if len(entries) != 1 {
		t.Fatalf("collectEntries len=%d, want 1", len(entries))
	}
}

func TestCollectEntries_ForceSelectsTranslatedEntries(t *testing.T) {
	f := po.NewFile()
	e := &po.Entry{MsgID: "Hello", MsgStr: "Hallo"}
	f.Entries = append(f.Entries, e)

	lf := &lockfile.LockFile{Version: lockfile.Version, Checksums: map[string]map[string]string{}}
	lockTarget := lockfile.LockTargetKey("ui", "de")
	lf.Update(lockTarget, lockfile.POEntryKey(e.MsgID, e.MsgCtxt), lockfile.POEntryContent(e.MsgID, e.MsgIDPlural))

	entries := collectEntries(f, Options{
		ForceTranslate: true,
		LockFile:       lf,
		LockTarget:     "ui",
		Language:       "de",
	})
	if len(entries) != 1 {
		t.Fatalf("collectEntries len=%d, want 1", len(entries))
	}
}

func TestCollectEntries_ForceOverridesLockedKeys(t *testing.T) {
	f := po.NewFile()
	f.Entries = append(f.Entries, &po.Entry{MsgID: "Hello", MsgStr: "Hallo"})

	entries := collectEntries(f, Options{
		ForceTranslate: true,
		LockedKeys:     []string{"Hello"},
	})
	if len(entries) != 1 {
		t.Fatalf("collectEntries len=%d, want 1", len(entries))
	}
}

func TestCollectEntries_ForceDoesNotOverrideIgnoredPatterns(t *testing.T) {
	f := po.NewFile()
	f.Entries = append(f.Entries, &po.Entry{MsgID: "internal.debug", MsgStr: "Debug"})

	entries := collectEntries(f, Options{
		ForceTranslate:  true,
		IgnoredPatterns: []*regexp.Regexp{regexp.MustCompile(`^internal\.`)},
	})
	if len(entries) != 0 {
		t.Fatalf("collectEntries len=%d, want 0", len(entries))
	}
}

func TestTranslateAllSequentialNoOpPreservesPOFile(t *testing.T) {
	raw := []byte("msgid \"\"\n\"hello\"\nmsgstr \"Hallo\"\n")
	poFile, err := po.Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	path := filepath.Join(t.TempDir(), "de.po")
	if err := os.WriteFile(path, raw, 0o664); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(path, 0o664); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	oldTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if err := TranslateAll(context.Background(), []LangTask{{Lang: "de", POFile: poFile, POPath: path}}, Options{}); err != nil {
		t.Fatalf("TranslateAll: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !bytes.Equal(got, raw) || info.Mode().Perm() != 0o664 || !info.ModTime().Equal(oldTime) {
		t.Fatalf("no-op changed file: bytes=%q mode=%o mtime=%v", got, info.Mode().Perm(), info.ModTime())
	}
}

func TestFilterChangedKeys_RetranslateIgnoresLock(t *testing.T) {
	lf := &lockfile.LockFile{Version: lockfile.Version, Checksums: map[string]map[string]string{}}
	lockTarget := lockfile.LockTargetKey("docs", "de")
	lf.Update(lockTarget, "title", lockfile.KVEntryContent("title", "Hello"))

	keys := []string{"title"}
	src := map[string]string{"title": "Hello"}
	opts := Options{
		RetranslateExisting: true,
		LockFile:            lf,
		LockTarget:          "docs",
		Language:            "de",
	}

	got := filterChangedKeys(keys, src, "", opts)
	if len(got) != 1 || got[0] != "title" {
		t.Fatalf("filterChangedKeys returned %v, want [title]", got)
	}
}

func TestFilterChangedKeys_ForceIgnoresLock(t *testing.T) {
	lf := &lockfile.LockFile{Version: lockfile.Version, Checksums: map[string]map[string]string{}}
	lockTarget := lockfile.LockTargetKey("docs", "de")
	lf.Update(lockTarget, "title", lockfile.KVEntryContent("title", "Hello"))

	keys := []string{"title"}
	src := map[string]string{"title": "Hello"}
	got := filterChangedKeys(keys, src, "", Options{
		ForceTranslate: true,
		LockFile:       lf,
		LockTarget:     "docs",
		Language:       "de",
	})
	if len(got) != 1 || got[0] != "title" {
		t.Fatalf("filterChangedKeys returned %v, want [title]", got)
	}
}

func TestUpdateLockFileForPO_SkipsUntranslatedEntries(t *testing.T) {
	lf := &lockfile.LockFile{Version: lockfile.Version, Checksums: map[string]map[string]string{}}

	translated := &po.Entry{MsgID: "A", MsgStr: "AA"}
	untranslated := &po.Entry{MsgID: "B", MsgStr: ""}

	updateLockFileForPO([]*po.Entry{translated, untranslated}, Options{
		LockFile:   lf,
		LockTarget: "pkg",
		Language:   "fr",
	})

	lockTarget := lockfile.LockTargetKey("pkg", "fr")
	if got := lf.TargetKeyCount(lockTarget); got != 1 {
		t.Fatalf("locked keys=%d, want 1", got)
	}
}

// TestCollectEntries_UntranslatedPassesThroughLock verifies that an untranslated
// entry whose source text is unchanged (i.e. locked) is still collected for
// translation. This is the regression case from the desktop-seeding workflow:
// after SeedPO fills in some PO entries and leaves others empty, the lockfile
// must not block the empty entries from being sent to the AI provider.
func TestCollectEntries_UntranslatedPassesThroughLock(t *testing.T) {
	f := po.NewFile()
	// Translated entry — already in lockfile with matching content.
	translated := &po.Entry{MsgID: "Hello", MsgStr: "Hallo"}
	// Untranslated entry — also locked (source unchanged), but has no msgstr.
	untranslated := &po.Entry{MsgID: "Goodbye", MsgStr: ""}
	f.Entries = append(f.Entries, translated, untranslated)

	lf := &lockfile.LockFile{Version: lockfile.Version, Checksums: map[string]map[string]string{}}
	lockTarget := lockfile.LockTargetKey("ui", "de")
	// Record both entries as if they were previously translated (same source).
	lf.Update(lockTarget, lockfile.POEntryKey(translated.MsgID, translated.MsgCtxt),
		lockfile.POEntryContent(translated.MsgID, translated.MsgIDPlural))
	lf.Update(lockTarget, lockfile.POEntryKey(untranslated.MsgID, untranslated.MsgCtxt),
		lockfile.POEntryContent(untranslated.MsgID, untranslated.MsgIDPlural))

	opts := Options{
		LockFile:   lf,
		LockTarget: "ui",
		Language:   "de",
	}

	entries := collectEntries(f, opts)
	// Only the untranslated entry should be collected — the translated one is
	// correctly suppressed by the lockfile.
	if len(entries) != 1 {
		t.Fatalf("collectEntries len=%d, want 1 (only untranslated)", len(entries))
	}
	if entries[0].MsgID != "Goodbye" {
		t.Fatalf("collected entry MsgID=%q, want Goodbye", entries[0].MsgID)
	}
}
