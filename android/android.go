// Package android implements reading and writing of Android strings.xml translation files.
//
// Supported resource types:
//   - <string>        — simple key/value string
//   - <string-array>  — ordered list of strings
//   - <plurals>       — quantity-keyed plural forms (zero/one/two/few/many/other)
//
// Resources with translatable="false" are parsed but excluded from all
// translation-related accessors (Keys, UntranslatedKeys, SyncKeys, etc.).
// They are still written back verbatim on Marshal.
package android

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Data model
// ---------------------------------------------------------------------------

// EntryKind identifies the type of a resource entry.
type EntryKind int

const (
	// KindString is a plain <string> resource.
	KindString EntryKind = iota
	// KindStringArray is a <string-array> resource.
	KindStringArray
	// KindPlurals is a <plurals> resource.
	KindPlurals
	// KindComment is an XML comment (not a resource).
	KindComment
)

// Entry represents a single item in a strings.xml file.
// It may be a string resource, a string-array, a plurals block, or a comment.
type Entry struct {
	// Kind is the resource type.
	Kind EntryKind

	// --- shared fields (KindString / KindStringArray / KindPlurals) ---

	// Name is the resource name (attribute name="…"). Empty for comments.
	Name string
	// Translatable reflects the translatable="…" attribute. Defaults to true.
	Translatable bool

	// --- KindString ---

	// Value is the translated text. Empty means untranslated.
	// Apostrophes are stored unescaped (\'  →  ') for clean LLM input;
	// they are re-escaped on Marshal.
	Value string
	// UseCDATA indicates the source value was wrapped in <![CDATA[...]]>.
	// When true, Marshal emits CDATA instead of XML-escaping the value.
	UseCDATA bool

	// --- KindStringArray ---

	// Items holds the <item> values in document order (apostrophes unescaped).
	Items []string
	// ItemCDATA mirrors Items: true when the corresponding <item> used CDATA.
	ItemCDATA []bool

	// --- KindPlurals ---

	// Plurals maps quantity keyword (zero/one/two/few/many/other) to text
	// (apostrophes unescaped).
	Plurals map[string]string
	// PluralOrder preserves the order of quantity keywords as they appear in the file.
	PluralOrder []string
	// PluralCDATA mirrors PluralOrder: true when the corresponding <item> used CDATA.
	PluralCDATA map[string]bool

	// --- KindComment ---

	// Comment is the raw comment text (without <!-- -->). Empty for resources.
	Comment string
}

// IsComment reports whether this entry is an XML comment.
func (e *Entry) IsComment() bool { return e.Kind == KindComment }

// IsTranslatable reports whether this resource should be translated.
func (e *Entry) IsTranslatable() bool {
	return e.Kind != KindComment && e.Translatable
}

// IsTranslated reports whether the entry has a complete (non-empty) translation.
func (e *Entry) IsTranslated() bool {
	switch e.Kind {
	case KindString:
		return e.Value != ""
	case KindStringArray:
		if len(e.Items) == 0 {
			return false
		}
		for _, v := range e.Items {
			if v == "" {
				return false
			}
		}
		return true
	case KindPlurals:
		if len(e.Plurals) == 0 {
			return false
		}
		for _, v := range e.Plurals {
			if v == "" {
				return false
			}
		}
		return true
	}
	return false
}

// File represents a parsed Android strings.xml file.
type File struct {
	// Entries in document order (resources + comments).
	Entries []*Entry
	// byName maps resource name to index in Entries for fast lookup.
	byName map[string]int
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

// ParseFile reads and parses an Android strings.xml file.
func ParseFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return Parse(data)
}

// cdataSet holds resource names (and array/plural item paths) that used CDATA
// in the source XML. Built by scanCDATA before XML parsing.
type cdataSet map[string]bool

// cdataKey returns a lookup key for a resource or sub-item.
//
//	string:      "name"
//	string-array item: "name[0]", "name[1]", …
//	plurals item:      "name#one", "name#other", …
func cdataKey(name, suffix string) string {
	if suffix == "" {
		return name
	}
	return name + suffix
}

// scanCDATA scans raw XML bytes for CDATA sections and records which resource
// elements contained them. Go's encoding/xml decoder transparently unwraps
// CDATA into CharData, so we detect them beforehand using regexes.
//
// Patterns detected:
//
//	<string name="foo"><![CDATA[...
//	<item><![CDATA[...            (inside <string-array name="bar">)
//	<item quantity="q"><![CDATA[...  (inside <plurals name="baz">)
var (
	reStringCDATA     = regexp.MustCompile(`<string\s[^>]*name="([^"]+)"[^>]*>\s*<!\[CDATA\[`)
	reArrayBlock      = regexp.MustCompile(`(?s)<string-array\s[^>]*name="([^"]+)"[^>]*>(.*?)</string-array>`)
	reItemWithCDATA   = regexp.MustCompile(`(?s)<item[^>]*>(\s*<!\[CDATA\[)`)
	rePluralsBlock    = regexp.MustCompile(`(?s)<plurals\s[^>]*name="([^"]+)"[^>]*>(.*?)</plurals>`)
	rePluralItemCDATA = regexp.MustCompile(`(?s)<item\s[^>]*quantity="([^"]+)"[^>]*>\s*<!\[CDATA\[`)
)

func scanCDATA(data []byte) cdataSet {
	result := cdataSet{}
	s := string(data)

	// 1. Plain <string name="…"><![CDATA[
	for _, m := range reStringCDATA.FindAllStringSubmatch(s, -1) {
		result[m[1]] = true
	}

	// 2. <string-array> items — scan the block content for <item> + CDATA
	for _, m := range reArrayBlock.FindAllStringSubmatch(s, -1) {
		name, block := m[1], m[2]
		allItems := reItemWithCDATA.FindAllString(block, -1)
		for i, item := range allItems {
			if strings.Contains(item, "<![CDATA[") {
				result[cdataKey(name, fmt.Sprintf("[%d]", i))] = true
			}
		}
	}

	// 3. <plurals> items — keyed by quantity attribute
	for _, m := range rePluralsBlock.FindAllStringSubmatch(s, -1) {
		name, block := m[1], m[2]
		for _, pm := range rePluralItemCDATA.FindAllStringSubmatch(block, -1) {
			result[cdataKey(name, "#"+pm[1])] = true
		}
	}

	return result
}

// Parse parses Android strings.xml data.
func Parse(data []byte) (*File, error) {
	f := &File{
		byName: make(map[string]int),
	}

	// Pre-scan for CDATA before the XML decoder unwraps them.
	cdata := scanCDATA(data)

	dec := xml.NewDecoder(strings.NewReader(string(data)))
	inResources := false

	for {
		tok, err := dec.Token()
		if err != nil {
			break // EOF or error
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "resources" {
				inResources = true
				continue
			}
			if !inResources {
				continue
			}

			switch t.Name.Local {
			case "string":
				e, err := parseStringElement(dec, t, cdata)
				if err != nil {
					return nil, err
				}
				f.addEntry(e)

			case "string-array":
				e, err := parseStringArrayElement(dec, t, cdata)
				if err != nil {
					return nil, err
				}
				f.addEntry(e)

			case "plurals":
				e, err := parsePluralsElement(dec, t, cdata)
				if err != nil {
					return nil, err
				}
				f.addEntry(e)

			default:
				// Unknown element — skip entirely
				dec.Skip()
			}

		case xml.Comment:
			if inResources {
				comment := strings.TrimSpace(string(t))
				if comment != "" {
					f.Entries = append(f.Entries, &Entry{
						Kind:    KindComment,
						Comment: comment,
					})
				}
			}

		case xml.EndElement:
			if t.Name.Local == "resources" {
				inResources = false
			}
		}
	}

	return f, nil
}

// addEntry appends an entry and registers it in byName if it has a name.
func (f *File) addEntry(e *Entry) {
	idx := len(f.Entries)
	f.Entries = append(f.Entries, e)
	if e.Name != "" {
		f.byName[e.Name] = idx
	}
}

// parseAttrs extracts name and translatable from a start element.
func parseAttrs(elem xml.StartElement) (name string, translatable bool) {
	translatable = true // default
	for _, attr := range elem.Attr {
		switch attr.Name.Local {
		case "name":
			name = attr.Value
		case "translatable":
			if strings.EqualFold(attr.Value, "false") {
				translatable = false
			}
		}
	}
	return
}

// parseStringElement parses a <string> element already opened.
func parseStringElement(dec *xml.Decoder, elem xml.StartElement, cdata cdataSet) (*Entry, error) {
	name, translatable := parseAttrs(elem)
	var inner strings.Builder
	_, err := readElementContent(dec, &inner)
	if err != nil {
		return nil, fmt.Errorf("reading <string name=%q>: %w", name, err)
	}
	return &Entry{
		Kind:         KindString,
		Name:         name,
		Translatable: translatable,
		Value:        inner.String(),
		UseCDATA:     cdata[name],
	}, nil
}

// parseStringArrayElement parses a <string-array> element already opened.
func parseStringArrayElement(dec *xml.Decoder, elem xml.StartElement, cdata cdataSet) (*Entry, error) {
	name, translatable := parseAttrs(elem)
	e := &Entry{
		Kind:         KindStringArray,
		Name:         name,
		Translatable: translatable,
	}

	depth := 1
	itemIdx := 0
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("reading <string-array name=%q>: %w", name, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "item" && depth == 1 {
				var inner strings.Builder
				_, err := readElementContent(dec, &inner)
				if err != nil {
					return nil, fmt.Errorf("reading <item> in <string-array name=%q>: %w", name, err)
				}
				e.Items = append(e.Items, inner.String())
				e.ItemCDATA = append(e.ItemCDATA, cdata[cdataKey(name, fmt.Sprintf("[%d]", itemIdx))])
				itemIdx++
			} else {
				depth++
			}
		case xml.EndElement:
			depth--
		}
	}
	return e, nil
}

// parsePluralsElement parses a <plurals> element already opened.
func parsePluralsElement(dec *xml.Decoder, elem xml.StartElement, cdata cdataSet) (*Entry, error) {
	name, translatable := parseAttrs(elem)
	e := &Entry{
		Kind:         KindPlurals,
		Name:         name,
		Translatable: translatable,
		Plurals:      make(map[string]string),
		PluralCDATA:  make(map[string]bool),
	}

	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("reading <plurals name=%q>: %w", name, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "item" && depth == 1 {
				var quantity string
				for _, attr := range t.Attr {
					if attr.Name.Local == "quantity" {
						quantity = attr.Value
						break
					}
				}
				var inner strings.Builder
				_, err := readElementContent(dec, &inner)
				if err != nil {
					return nil, fmt.Errorf("reading <item quantity=%q> in <plurals name=%q>: %w", quantity, name, err)
				}
				if quantity != "" {
					e.Plurals[quantity] = inner.String()
					e.PluralOrder = append(e.PluralOrder, quantity)
					e.PluralCDATA[quantity] = cdata[cdataKey(name, "#"+quantity)]
				}
			} else {
				depth++
			}
		case xml.EndElement:
			depth--
		}
	}
	return e, nil
}

// readElementContent reads the full inner content of an XML element until its
// matching close tag, reconstructing inline child elements (e.g., <xliff:g>)
// as raw text. It returns hasCDATA=true when the content contained at least
// one CDATA section (so callers can restore the wrapper on write).
// Apostrophes are unescaped (\' → ') so that LLM receives clean text.
func readElementContent(dec *xml.Decoder, b *strings.Builder) (hasCDATA bool, err error) {
	depth := 1
	for depth > 0 {
		var tok xml.Token
		tok, err = dec.Token()
		if err != nil {
			return
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.WriteString(unescapeAndroidApostrophe(string(t)))
		case xml.Comment:
			// skip XML comments inside elements
		case xml.ProcInst:
			// skip processing instructions
		case xml.Directive:
			// CDATA sections are exposed as xml.Directive by Go's xml package
			// when they start with "[CDATA[". We detect and unwrap them.
			s := string(t)
			if strings.HasPrefix(s, "[CDATA[") && strings.HasSuffix(s, "]]") {
				// Strip "[CDATA[" prefix and "]]" suffix
				inner := s[7 : len(s)-2]
				b.WriteString(unescapeAndroidApostrophe(inner))
				hasCDATA = true
			}
		case xml.StartElement:
			depth++
			b.WriteString("<")
			if t.Name.Space != "" {
				b.WriteString(t.Name.Space)
				b.WriteString(":")
			}
			b.WriteString(t.Name.Local)
			for _, attr := range t.Attr {
				b.WriteString(fmt.Sprintf(` %s="%s"`, attr.Name.Local, attr.Value))
			}
			b.WriteString(">")
		case xml.EndElement:
			depth--
			if depth > 0 {
				b.WriteString("</")
				if t.Name.Space != "" {
					b.WriteString(t.Name.Space)
					b.WriteString(":")
				}
				b.WriteString(t.Name.Local)
				b.WriteString(">")
			}
		}
	}
	return
}

// unescapeAndroidApostrophe converts Android-escaped apostrophes (\') to
// plain apostrophes (') so that LLM receives clean, natural text.
func unescapeAndroidApostrophe(s string) string {
	return strings.ReplaceAll(s, `\'`, `'`)
}

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

// Keys returns all translatable resource names in document order.
func (f *File) Keys() []string {
	var keys []string
	for _, e := range f.Entries {
		if e.IsTranslatable() {
			keys = append(keys, e.Name)
		}
	}
	return keys
}

// UntranslatedKeys returns names of translatable entries that have no complete translation.
func (f *File) UntranslatedKeys() []string {
	var result []string
	for _, e := range f.Entries {
		if e.IsTranslatable() && !e.IsTranslated() {
			result = append(result, e.Name)
		}
	}
	return result
}

// UntranslatedEntries returns translatable entries that have no complete translation.
func (f *File) UntranslatedEntries() []*Entry {
	var result []*Entry
	for _, e := range f.Entries {
		if e.IsTranslatable() && !e.IsTranslated() {
			result = append(result, e)
		}
	}
	return result
}

// Stats returns (total, translated, untranslated) counts for translatable resources.
func (f *File) Stats() (total, translated, untranslated int) {
	for _, e := range f.Entries {
		if !e.IsTranslatable() {
			continue
		}
		total++
		if e.IsTranslated() {
			translated++
		} else {
			untranslated++
		}
	}
	return
}

// Get returns the string value for a KindString entry. Returns ("", false) for
// non-string entries or missing keys.
func (f *File) Get(name string) (string, bool) {
	idx, ok := f.byName[name]
	if !ok {
		return "", false
	}
	e := f.Entries[idx]
	if e.Kind != KindString {
		return "", false
	}
	return e.Value, true
}

// GetEntry returns the entry for a given resource name, or nil if not found.
func (f *File) GetEntry(name string) *Entry {
	idx, ok := f.byName[name]
	if !ok {
		return nil
	}
	return f.Entries[idx]
}

// Set sets the string value for a KindString entry. Returns false if the key
// doesn't exist or is not a KindString.
func (f *File) Set(name, value string) bool {
	idx, ok := f.byName[name]
	if !ok {
		return false
	}
	e := f.Entries[idx]
	if e.Kind != KindString {
		return false
	}
	e.Value = value
	return true
}

// SetItems sets the items for a KindStringArray entry. Returns false if the
// key doesn't exist or is not a KindStringArray.
func (f *File) SetItems(name string, items []string) bool {
	idx, ok := f.byName[name]
	if !ok {
		return false
	}
	e := f.Entries[idx]
	if e.Kind != KindStringArray {
		return false
	}
	e.Items = items
	return true
}

// SetPlurals sets the plural forms for a KindPlurals entry. Returns false if
// the key doesn't exist or is not a KindPlurals.
func (f *File) SetPlurals(name string, forms map[string]string) bool {
	idx, ok := f.byName[name]
	if !ok {
		return false
	}
	e := f.Entries[idx]
	if e.Kind != KindPlurals {
		return false
	}
	for q, v := range forms {
		e.Plurals[q] = v
	}
	return true
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

// WriteFile writes the strings.xml file to disk as a source file (includes
// all entries, including translatable="false").
func (f *File) WriteFile(path string) error {
	data := f.Marshal()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// WriteTargetFile writes the strings.xml file for a translated locale.
// Resources marked translatable="false" are omitted — Android inherits them
// from the default values/strings.xml automatically.
func (f *File) WriteTargetFile(path string) error {
	data := f.MarshalTarget()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// Marshal produces the XML output in Android strings.xml format.
// isSource should be true when writing the default values/strings.xml — in
// that case non-translatable resources are included verbatim. For target
// locale files (values-XX/strings.xml) pass isSource=false to omit them.
func (f *File) Marshal() []byte {
	return f.marshal(true)
}

// MarshalTarget produces the XML for a translated locale file, omitting
// resources marked translatable="false" (they live only in the source file).
func (f *File) MarshalTarget() []byte {
	return f.marshal(false)
}

func (f *File) marshal(includeNonTranslatable bool) []byte {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n")
	b.WriteString("<resources>\n")

	for _, e := range f.Entries {
		// In target files, skip non-translatable resources entirely —
		// Android inherits them from values/strings.xml automatically.
		if !includeNonTranslatable && !e.IsTranslatable() && e.Kind != KindComment {
			continue
		}

		switch e.Kind {
		case KindComment:
			b.WriteString(fmt.Sprintf("    <!-- %s -->\n", e.Comment))

		case KindString:
			attrs := fmt.Sprintf(`name="%s"`, e.Name)
			if !e.Translatable {
				attrs += ` translatable="false"`
			}
			content := marshalStringValue(e.Value, e.UseCDATA)
			b.WriteString(fmt.Sprintf("    <string %s>%s</string>\n", attrs, content))

		case KindStringArray:
			attrs := fmt.Sprintf(`name="%s"`, e.Name)
			if !e.Translatable {
				attrs += ` translatable="false"`
			}
			b.WriteString(fmt.Sprintf("    <string-array %s>\n", attrs))
			for i, item := range e.Items {
				useCDATA := i < len(e.ItemCDATA) && e.ItemCDATA[i]
				content := marshalStringValue(item, useCDATA)
				b.WriteString(fmt.Sprintf("        <item>%s</item>\n", content))
			}
			b.WriteString("    </string-array>\n")

		case KindPlurals:
			attrs := fmt.Sprintf(`name="%s"`, e.Name)
			if !e.Translatable {
				attrs += ` translatable="false"`
			}
			b.WriteString(fmt.Sprintf("    <plurals %s>\n", attrs))
			for _, q := range e.PluralOrder {
				v := e.Plurals[q]
				useCDATA := e.PluralCDATA != nil && e.PluralCDATA[q]
				content := marshalStringValue(v, useCDATA)
				b.WriteString(fmt.Sprintf("        <item quantity=\"%s\">%s</item>\n", q, content))
			}
			b.WriteString("    </plurals>\n")
		}
	}

	b.WriteString("</resources>\n")
	return []byte(b.String())
}

// marshalStringValue encodes a string value for XML output.
// If useCDATA is true the value is wrapped in <![CDATA[...]]> and only
// apostrophes are escaped (Android AAPT requirement); otherwise standard
// XML escaping plus Android apostrophe escaping is applied.
func marshalStringValue(s string, useCDATA bool) string {
	if useCDATA {
		// Inside CDATA only apostrophes need escaping for Android AAPT.
		return "<![CDATA[" + escapeAndroidApostrophe(s) + "]]>"
	}
	return xmlEscape(s)
}

// ---------------------------------------------------------------------------
// Creating / syncing translation files
// ---------------------------------------------------------------------------

// NewTranslationFile creates a new File with the same structure as source but
// with all translatable values empty (untranslated). Non-translatable entries
// are copied verbatim; comments are preserved.
func NewTranslationFile(source *File) *File {
	f := &File{byName: make(map[string]int)}
	for _, e := range source.Entries {
		var ne *Entry
		switch e.Kind {
		case KindComment:
			ne = &Entry{Kind: KindComment, Comment: e.Comment}

		case KindString:
			v := ""
			if !e.Translatable {
				v = e.Value // copy verbatim
			}
			ne = &Entry{Kind: KindString, Name: e.Name, Translatable: e.Translatable, Value: v}

		case KindStringArray:
			var items []string
			if !e.Translatable {
				items = append([]string(nil), e.Items...)
			} else {
				items = make([]string, len(e.Items)) // all empty
			}
			ne = &Entry{Kind: KindStringArray, Name: e.Name, Translatable: e.Translatable, Items: items}

		case KindPlurals:
			plurals := make(map[string]string)
			order := append([]string(nil), e.PluralOrder...)
			if !e.Translatable {
				for q, v := range e.Plurals {
					plurals[q] = v
				}
			}
			ne = &Entry{Kind: KindPlurals, Name: e.Name, Translatable: e.Translatable, Plurals: plurals, PluralOrder: order}
		}

		idx := len(f.Entries)
		f.Entries = append(f.Entries, ne)
		if ne.Name != "" {
			f.byName[ne.Name] = idx
		}
	}
	return f
}

// SyncKeys ensures the translation file has all translatable keys from source.
// Missing keys are added with empty values (preserving kind and structure).
// Non-translatable entries and comments are not synced.
// Returns the number of keys added.
func (f *File) SyncKeys(source *File) int {
	added := 0
	for _, e := range source.Entries {
		if !e.IsTranslatable() {
			continue
		}
		if _, exists := f.byName[e.Name]; exists {
			continue
		}
		var ne *Entry
		switch e.Kind {
		case KindString:
			ne = &Entry{Kind: KindString, Name: e.Name, Translatable: true, Value: ""}
		case KindStringArray:
			ne = &Entry{Kind: KindStringArray, Name: e.Name, Translatable: true, Items: make([]string, len(e.Items))}
		case KindPlurals:
			order := append([]string(nil), e.PluralOrder...)
			ne = &Entry{Kind: KindPlurals, Name: e.Name, Translatable: true, Plurals: make(map[string]string), PluralOrder: order}
		default:
			continue
		}
		idx := len(f.Entries)
		f.Entries = append(f.Entries, ne)
		f.byName[e.Name] = idx
		added++
	}
	return added
}

// ---------------------------------------------------------------------------
// Language detection from res/ directory
// ---------------------------------------------------------------------------

// DetectLanguages scans an Android res/ directory for values-XX/ directories
// that contain strings.xml and returns the language codes.
func DetectLanguages(resDir string) []string {
	entries, err := os.ReadDir(resDir)
	if err != nil {
		return nil
	}

	var langs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "values-") {
			continue
		}
		lang := strings.TrimPrefix(name, "values-")
		if lang == "" {
			continue
		}
		stringsPath := filepath.Join(resDir, name, "strings.xml")
		if _, err := os.Stat(stringsPath); err == nil {
			langs = append(langs, androidLocaleToStandard(lang))
		}
	}
	sort.Strings(langs)
	return langs
}

// AndroidLocaleDirName converts a standard language code to an Android
// values directory name (e.g., "pt-BR" -> "values-pt-rBR", "ru" -> "values-ru").
func AndroidLocaleDirName(lang string) string {
	return "values-" + standardToAndroidLocale(lang)
}

// StringsXMLPath returns the path to strings.xml for a given language.
func StringsXMLPath(resDir, lang string) string {
	return filepath.Join(resDir, AndroidLocaleDirName(lang), "strings.xml")
}

// SourceStringsXMLPath returns the path to the default (source) strings.xml.
func SourceStringsXMLPath(resDir string) string {
	return filepath.Join(resDir, "values", "strings.xml")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// xmlEscape escapes special XML characters in text content.
// Strings that already contain XML tags (e.g., <xliff:g>) are returned as-is.
// xmlEscape escapes a plain string value for use inside an XML element.
// Strings that contain both < and > are assumed to carry inline HTML tags
// (e.g. <xliff:g>) and are returned as-is to preserve them.
// Apostrophes are escaped per Android AAPT rules (\').
func xmlEscape(s string) string {
	if strings.Contains(s, "<") && strings.Contains(s, ">") {
		return s
	}
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return escapeAndroidApostrophe(s)
}

// escapeAndroidApostrophe escapes apostrophes for Android AAPT without
// double-escaping (strips any existing \' first, then re-escapes).
func escapeAndroidApostrophe(s string) string {
	s = strings.ReplaceAll(s, `\'`, `'`) // normalise first
	return strings.ReplaceAll(s, `'`, `\'`)
}

// androidLocaleToStandard converts Android locale format to standard BCP-47.
// e.g., "pt-rBR" -> "pt-BR", "zh-rCN" -> "zh-CN", "ru" -> "ru"
func androidLocaleToStandard(androidLocale string) string {
	if idx := strings.Index(androidLocale, "-r"); idx >= 0 {
		return androidLocale[:idx] + "-" + androidLocale[idx+2:]
	}
	return androidLocale
}

// standardToAndroidLocale converts standard BCP-47 to Android locale format.
// e.g., "pt-BR" -> "pt-rBR", "zh-CN" -> "zh-rCN", "ru" -> "ru"
func standardToAndroidLocale(lang string) string {
	parts := strings.SplitN(lang, "-", 2)
	if len(parts) == 2 && len(parts[1]) > 0 {
		return parts[0] + "-r" + parts[1]
	}
	return lang
}
