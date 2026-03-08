// Package translate contains tests for the translation engine.
package translate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/minios-linux/lokit/internal/format/i18next"
	po "github.com/minios-linux/lokit/internal/format/po"
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
	prompt := buildKVUserPrompt(keys, srcVals, "Russian")

	if !strings.Contains(prompt, "Translate these strings to Russian") {
		t.Fatalf("prompt missing language header: %q", prompt)
	}
	if !strings.Contains(prompt, `1. "Home"`) {
		t.Fatalf("prompt missing source value: %q", prompt)
	}
	if !strings.Contains(prompt, `2. "menu.help"`) {
		t.Fatalf("prompt missing fallback key: %q", prompt)
	}
}

func TestBuildI18NextUserPrompt_UsesKeysAsSource(t *testing.T) {
	keys := []string{"Save", "Cancel"}
	prompt := buildI18NextUserPrompt(keys, "German")

	if !strings.Contains(prompt, "Translate these UI strings to German") {
		t.Fatalf("prompt missing language header: %q", prompt)
	}
	if !strings.Contains(prompt, `1. "Save"`) || !strings.Contains(prompt, `2. "Cancel"`) {
		t.Fatalf("prompt missing key list: %q", prompt)
	}
}

func TestBuildMarkdownUserPrompt_IncludesMarkdownRules(t *testing.T) {
	keys := []string{"intro"}
	srcVals := map[string]string{"intro": "# Welcome\nText"}
	prompt := buildMarkdownUserPrompt(keys, srcVals, "French")

	if !strings.Contains(prompt, "preserve all formatting") {
		t.Fatalf("markdown rules missing from prompt: %q", prompt)
	}
	if !strings.Contains(prompt, `1. "# Welcome\nText"`) {
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

func TestMaskAndRestoreMarkdownCodeBlocks(t *testing.T) {
	src := "### H\n\n```mermaid\ngraph TD\nA-->B\n```\n\nText"
	masked, blocks := maskMarkdownCodeBlocks(src)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 code block, got %d", len(blocks))
	}
	if !strings.Contains(masked, "__LOKIT_CODE_BLOCK_0__") {
		t.Fatalf("expected placeholder in masked text, got %q", masked)
	}
	restored := restoreMarkdownCodeBlocks(masked, blocks)
	if restored != src {
		t.Fatalf("restored text mismatch\nwant: %q\ngot:  %q", src, restored)
	}
}

func TestMaskAndRestoreMarkdownCodeBlocks_WithInlineBackticks(t *testing.T) {
	src := "### H\n\n```python\nx = \"`hello`\"\n```\n\nText"
	masked, blocks := maskMarkdownCodeBlocks(src)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 code block, got %d", len(blocks))
	}
	if !strings.Contains(masked, "__LOKIT_CODE_BLOCK_0__") {
		t.Fatalf("expected placeholder in masked text, got %q", masked)
	}
	restored := restoreMarkdownCodeBlocks(masked, blocks)
	if restored != src {
		t.Fatalf("restored text mismatch\nwant: %q\ngot:  %q", src, restored)
	}
}

func TestTranslateMarkdownSingleRetry_RestoresMaskedCodeBlocks(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"[\"Perevod __LOKIT_CODE_BLOCK_0__ gotov\"]"}}]}`))
	}))
	defer ts.Close()

	opts := Options{
		Provider: Provider{
			ID:      ProviderCustomOpenAI,
			BaseURL: ts.URL,
			Model:   "test-model",
		},
		LanguageName: "Russian",
	}

	src := "### H\n\n```python\nx = \"`hello`\"\n```\n\nText"
	translations, err := translateMarkdownSingleRetry(
		context.Background(),
		"sec:0",
		map[string]string{"sec:0": src},
		opts.resolvedPrompt(),
		opts,
		&rateLimitState{},
	)
	if err != nil {
		t.Fatalf("translateMarkdownSingleRetry error: %v", err)
	}
	if len(translations) != 1 {
		t.Fatalf("expected 1 translation, got %d", len(translations))
	}
	if strings.Contains(translations[0], "__LOKIT_CODE_BLOCK_0__") {
		t.Fatalf("placeholder was not restored: %q", translations[0])
	}
	if !strings.Contains(translations[0], "```python") || !strings.Contains(translations[0], "`hello`") {
		t.Fatalf("expected restored fenced code block in translation, got %q", translations[0])
	}
}

func TestTranslateMarkdownSingleRetry_AcceptsRawFallbackForMarkdown(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Perevod __LOKIT_CODE_BLOCK_0__ gotov"}}]}`))
	}))
	defer ts.Close()

	opts := Options{
		Provider: Provider{
			ID:      ProviderCustomOpenAI,
			BaseURL: ts.URL,
			Model:   "test-model",
		},
		LanguageName: "Russian",
	}

	src := "### H\n\n```python\nx = \"`hello`\"\n```\n\nText"
	translations, err := translateMarkdownSingleRetry(
		context.Background(),
		"sec:0",
		map[string]string{"sec:0": src},
		opts.resolvedPrompt(),
		opts,
		&rateLimitState{},
	)
	if err != nil {
		t.Fatalf("translateMarkdownSingleRetry error: %v", err)
	}
	if len(translations) != 1 {
		t.Fatalf("expected 1 translation, got %d", len(translations))
	}
	if strings.Contains(translations[0], "__LOKIT_CODE_BLOCK_0__") {
		t.Fatalf("placeholder was not restored: %q", translations[0])
	}
	if !strings.Contains(translations[0], "```python") || !strings.Contains(translations[0], "`hello`") {
		t.Fatalf("expected restored fenced code block in translation, got %q", translations[0])
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
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"[\"Привет\",\"Пока\"]"}}]}`))
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

func TestTranslateAllKVFullParallel_TranslatesAllTasks(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"[\"OK\"]"}}]}`))
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

func TestCallOpenAI_RejectsNonOAuthModel(t *testing.T) {
	prov := Provider{
		ID:    ProviderOpenAI,
		Model: "gpt-4o",
	}

	_, err := callOpenAI(context.Background(), prov, "system", "user", nil, 0, false)
	if err == nil {
		t.Fatal("expected error for non-OAuth model without API key")
	}
	if !strings.Contains(err.Error(), "GPT-5/Codex models") {
		t.Fatalf("unexpected error: %v", err)
	}
}
