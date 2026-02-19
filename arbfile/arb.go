// Package arbfile implements reading and writing of Flutter ARB (Application
// Resource Bundle) files.
//
// ARB files are JSON files with a specific structure:
//
//   - "@@locale" holds the BCP-47 language code (e.g. "en", "ru").
//   - Keys starting with "@" (other than "@@locale") are metadata entries
//     (e.g. "@greeting") and are preserved verbatim — never translated.
//   - All other string values are translatable.
//
// File naming convention: app_LANG.arb (e.g. app_en.arb, app_ru.arb) stored
// in a single directory (e.g. lib/l10n/).
//
// Round-trip fidelity: key order from the source file is preserved, and
// metadata keys immediately follow their corresponding translatable key.
package arbfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// File model
// ---------------------------------------------------------------------------

// entry is a single key in the ARB file.
type entry struct {
	key      string
	value    string // raw JSON string value (for metadata: raw JSON)
	isMeta   bool   // true for @-keys (metadata / @@locale)
	rawValue []byte // original JSON value bytes (preserved for meta)
}

// File represents a parsed ARB file.
type File struct {
	// locale is the value of @@locale.
	locale string
	// entries stores all keys in document order.
	entries []entry
	// index maps key → index in entries.
	index map[string]int
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

// ParseFile reads and parses an ARB file from disk.
func ParseFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return Parse(data)
}

// Parse parses ARB content from a byte slice.
func Parse(data []byte) (*File, error) {
	// Decode as ordered key-value using json.Decoder with token streaming
	// to preserve key order.
	f := &File{index: make(map[string]int)}

	dec := json.NewDecoder(bytes.NewReader(data))

	// Expect opening '{'
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("parsing ARB: %w", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("parsing ARB: expected '{', got %v", tok)
	}

	for dec.More() {
		// Read key
		keyTok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("parsing ARB key: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("parsing ARB: expected string key, got %T", keyTok)
		}

		// Read raw value
		var rawVal json.RawMessage
		if err := dec.Decode(&rawVal); err != nil {
			return nil, fmt.Errorf("parsing ARB value for %q: %w", key, err)
		}

		isMeta := strings.HasPrefix(key, "@")

		if key == "@@locale" {
			var s string
			_ = json.Unmarshal(rawVal, &s)
			f.locale = s
		}

		idx := len(f.entries)
		e := entry{
			key:      key,
			isMeta:   isMeta,
			rawValue: rawVal,
		}
		if !isMeta {
			// Translatable string value
			var s string
			if err := json.Unmarshal(rawVal, &s); err == nil {
				e.value = s
			}
		}
		f.entries = append(f.entries, e)
		f.index[key] = idx
	}

	return f, nil
}

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

// Locale returns the @@locale value.
func (f *File) Locale() string { return f.locale }

// Keys returns all translatable (non-metadata) keys in document order.
func (f *File) Keys() []string {
	var keys []string
	for _, e := range f.entries {
		if !e.isMeta {
			keys = append(keys, e.key)
		}
	}
	return keys
}

// UntranslatedKeys returns translatable keys whose value is empty.
func (f *File) UntranslatedKeys() []string {
	var keys []string
	for _, e := range f.entries {
		if !e.isMeta && e.value == "" {
			keys = append(keys, e.key)
		}
	}
	return keys
}

// Get returns the string value for a translatable key.
func (f *File) Get(key string) (string, bool) {
	if idx, ok := f.index[key]; ok && !f.entries[idx].isMeta {
		return f.entries[idx].value, true
	}
	return "", false
}

// Set sets the value of an existing translatable key.
// Returns true on success, false if the key is not found or is metadata.
func (f *File) Set(key, value string) bool {
	idx, ok := f.index[key]
	if !ok || f.entries[idx].isMeta {
		return false
	}
	f.entries[idx].value = value
	// Update raw value too
	raw, _ := json.Marshal(value)
	f.entries[idx].rawValue = raw
	return true
}

// Stats returns (total, translated, percentTranslated).
func (f *File) Stats() (int, int, float64) {
	total, translated := 0, 0
	for _, e := range f.entries {
		if !e.isMeta {
			total++
			if e.value != "" {
				translated++
			}
		}
	}
	pct := 0.0
	if total > 0 {
		pct = float64(translated) / float64(total) * 100
	}
	return total, translated, pct
}

// SourceValues returns a map of key → value for use as translation source.
func (f *File) SourceValues() map[string]string {
	m := make(map[string]string, len(f.index))
	for _, e := range f.entries {
		if !e.isMeta {
			m[e.key] = e.value
		}
	}
	return m
}

// ---------------------------------------------------------------------------
// Serialization
// ---------------------------------------------------------------------------

// Marshal serialises the ARB file to JSON with 2-space indentation.
// The @@locale key is always written first.
func (f *File) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("{\n")

	// Write @@locale first if present.
	localeWritten := false
	if f.locale != "" {
		raw, _ := json.Marshal(f.locale)
		buf.WriteString("  \"@@locale\": ")
		buf.Write(raw)
		localeWritten = true
	}

	for _, e := range f.entries {
		if e.key == "@@locale" {
			continue // already written
		}
		if localeWritten || len(buf.Bytes()) > 2 {
			buf.WriteString(",\n")
		}
		keyBytes, _ := json.Marshal(e.key)
		buf.WriteString("  ")
		buf.Write(keyBytes)
		buf.WriteString(": ")
		if e.isMeta {
			// Pretty-print metadata objects.
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, e.rawValue, "  ", "  "); err != nil {
				buf.Write(e.rawValue)
			} else {
				buf.Write(pretty.Bytes())
			}
		} else {
			raw, _ := json.Marshal(e.value)
			buf.Write(raw)
		}
		localeWritten = true
	}

	buf.WriteString("\n}\n")
	return buf.Bytes(), nil
}

// WriteFile serialises and writes to path.
func (f *File) WriteFile(path string) error {
	data, err := f.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Sync (create target from source)
// ---------------------------------------------------------------------------

// NewTranslationFile creates a new File for targetLocale mirroring src's
// structure but with all translatable values cleared.
func NewTranslationFile(src *File, targetLocale string) *File {
	f := &File{
		locale: targetLocale,
		index:  make(map[string]int),
	}

	for _, e := range src.entries {
		if e.key == "@@locale" {
			continue // will be added from targetLocale
		}
		cp := e
		if !e.isMeta {
			cp.value = ""
			raw, _ := json.Marshal("")
			cp.rawValue = raw
		}
		idx := len(f.entries)
		f.entries = append(f.entries, cp)
		f.index[cp.key] = idx
	}

	return f
}

// SyncKeys ensures target has the same translatable keys as src, preserving
// existing translations and adding empty entries for new keys. Obsolete keys
// (in target but not src) are removed.
func SyncKeys(src, target *File) {
	// Collect existing translations.
	existing := make(map[string]string, len(target.index))
	for _, e := range target.entries {
		if !e.isMeta {
			existing[e.key] = e.value
		}
	}

	rebuilt := NewTranslationFile(src, target.locale)
	for k, v := range existing {
		if idx, ok := rebuilt.index[k]; ok && !rebuilt.entries[idx].isMeta {
			rebuilt.entries[idx].value = v
			raw, _ := json.Marshal(v)
			rebuilt.entries[idx].rawValue = raw
		}
	}

	target.entries = rebuilt.entries
	target.index = rebuilt.index
}
