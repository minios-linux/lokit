// Package i18next implements reading and writing of i18next JSON translation files.
//
// The expected file format is:
//
//	{
//	    "_meta": { "name": "Русский", "flag": "🇷🇺" },
//	    "translations": {
//	        "English key text": "Translated value",
//	        "Another key": ""
//	    }
//	}
//
// Keys are natural English text. Empty string values mean untranslated
// (i18next falls back to the key itself as the English text).
package i18next

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/minios-linux/lokit/langmeta"
)

// Meta holds the language metadata from the _meta field.
type Meta struct {
	Name string `json:"name"`
	Flag string `json:"flag"`
}

// File represents a parsed i18next translation file.
type File struct {
	Meta         Meta
	Translations map[string]string // key (English) -> value (translated)
	// keys preserves the original key order from the file.
	keys []string
}

// ParseFile reads and parses an i18next JSON translation file.
func ParseFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return Parse(data)
}

// Parse parses i18next JSON data.
func Parse(data []byte) (*File, error) {
	// First pass: decode into ordered structure to preserve key order.
	var raw struct {
		Meta         Meta            `json:"_meta"`
		Translations json.RawMessage `json:"translations"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	f := &File{
		Meta:         raw.Meta,
		Translations: make(map[string]string),
	}

	// Parse translations preserving key order via json.Decoder.
	if len(raw.Translations) > 0 {
		keys, err := parseOrderedStringMap(raw.Translations)
		if err != nil {
			return nil, fmt.Errorf("parsing translations: %w", err)
		}
		f.keys = keys.keys
		f.Translations = keys.values
	}

	return f, nil
}

// orderedMap preserves insertion order of a string->string JSON object.
type orderedMap struct {
	keys   []string
	values map[string]string
}

func parseOrderedStringMap(data []byte) (*orderedMap, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))

	// Read opening brace.
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := t.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("expected {, got %v", t)
	}

	om := &orderedMap{values: make(map[string]string)}

	for dec.More() {
		// Read key.
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("expected string key, got %T", kt)
		}

		// Read value.
		vt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		value, ok := vt.(string)
		if !ok {
			return nil, fmt.Errorf("expected string value for key %q, got %T", key, vt)
		}

		om.keys = append(om.keys, key)
		om.values[key] = value
	}

	return om, nil
}

// Keys returns the translation keys in their original order.
func (f *File) Keys() []string {
	if len(f.keys) > 0 {
		return f.keys
	}

	// Fallback: sorted keys.
	keys := make([]string, 0, len(f.Translations))
	for k := range f.Translations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// UntranslatedKeys returns keys that have empty translations.
func (f *File) UntranslatedKeys() []string {
	var result []string
	for _, k := range f.Keys() {
		if f.Translations[k] == "" {
			result = append(result, k)
		}
	}
	return result
}

// Stats returns (total, translated, untranslated) counts.
func (f *File) Stats() (total, translated, untranslated int) {
	total = len(f.Translations)
	for _, v := range f.Translations {
		if v != "" {
			translated++
		} else {
			untranslated++
		}
	}
	return
}

// WriteFile writes the translation file back to disk in the original format,
// preserving key order and using 4-space indentation.
func (f *File) WriteFile(path string) error {
	data, err := f.Marshal()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// Marshal produces the JSON output matching the original i18next format
// with 4-space indentation and sorted keys.
func (f *File) Marshal() ([]byte, error) {
	var b strings.Builder
	b.WriteString("{\n")

	// _meta.
	b.WriteString("    \"_meta\": {\n")
	b.WriteString(fmt.Sprintf("        \"name\": %s,\n", jsonString(f.Meta.Name)))
	b.WriteString(fmt.Sprintf("        \"flag\": %s\n", jsonString(f.Meta.Flag)))
	b.WriteString("    },\n")

	// translations — preserve original key order, or sorted.
	b.WriteString("    \"translations\": {\n")
	keys := f.Keys()
	for i, k := range keys {
		v := f.Translations[k]
		b.WriteString(fmt.Sprintf("        %s: %s", jsonString(k), jsonString(v)))
		if i < len(keys)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("    }\n")
	b.WriteString("}\n")

	return []byte(b.String()), nil
}

// jsonString returns a JSON-encoded string value (with proper escaping).
func jsonString(s string) string {
	return strconv.Quote(s)
}

// LangMeta contains known language metadata for auto-fill.
var LangMeta = func() map[string]Meta {
	out := make(map[string]Meta, len(langmeta.Registry))
	for k, v := range langmeta.Registry {
		out[k] = Meta{Name: v.Name, Flag: v.Flag}
	}
	return out
}()

// ResolveMeta returns best-effort language metadata for language codes,
// supporting variants like pt_BR, pt-BR, and region fallbacks.
func ResolveMeta(lang string) Meta {
	m := langmeta.Resolve(lang)
	return Meta{Name: m.Name, Flag: m.Flag}
}
