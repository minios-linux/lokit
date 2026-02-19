// Package propfile implements reading and writing of Java .properties files.
//
// Format: key=value pairs, one per line. Lines starting with '#' or '!' are
// comments and are preserved verbatim in the output. Blank lines are also
// preserved. Multi-line values (backslash continuation) are not supported —
// each line is treated independently.
//
// File naming convention: each language is stored as a separate file:
//
//	translations_dir/en.properties  (source)
//	translations_dir/ru.properties  (translation)
//
// The File type maintains the original line order so that round-trip
// serialization reproduces the source structure with translated values.
package propfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// File model
// ---------------------------------------------------------------------------

// lineKind classifies each line in the file.
type lineKind int

const (
	lineBlank   lineKind = iota // blank / whitespace-only line
	lineComment                 // comment line (starts with # or !)
	lineEntry                   // key=value pair
)

// line is a single line in the properties file.
type line struct {
	kind  lineKind
	raw   string // original text (comment/blank), or key for entries
	key   string // only for lineEntry
	value string // only for lineEntry; may be replaced by Set
}

// File represents a parsed .properties file.
type File struct {
	// lines stores all lines in document order.
	lines []line
	// index maps key → index in lines for fast lookup.
	index map[string]int
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

// ParseFile reads and parses a .properties file from disk.
func ParseFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return Parse(data)
}

// Parse parses .properties content from a byte slice.
func Parse(data []byte) (*File, error) {
	f := &File{index: make(map[string]int)}

	text := string(data)
	// Normalise Windows line endings.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	rawLines := strings.Split(text, "\n")

	// Drop trailing empty element from a file that ends with \n.
	if len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}

	for _, raw := range rawLines {
		trimmed := strings.TrimSpace(raw)

		switch {
		case trimmed == "":
			f.lines = append(f.lines, line{kind: lineBlank, raw: raw})

		case strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!"):
			f.lines = append(f.lines, line{kind: lineComment, raw: raw})

		default:
			k, v := splitKeyValue(trimmed)
			if k == "" {
				// Malformed line — treat as comment to preserve it.
				f.lines = append(f.lines, line{kind: lineComment, raw: raw})
				continue
			}
			if _, exists := f.index[k]; exists {
				// Duplicate key: overwrite value but keep position.
				f.lines[f.index[k]].value = v
				continue
			}
			idx := len(f.lines)
			f.lines = append(f.lines, line{kind: lineEntry, key: k, value: v})
			f.index[k] = idx
		}
	}

	return f, nil
}

// splitKeyValue splits "key = value" or "key=value" into key and value.
// The separator may be '=' or ':'. Surrounding whitespace is stripped.
func splitKeyValue(s string) (key, value string) {
	for i, ch := range s {
		if ch == '=' || ch == ':' {
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
		}
	}
	// No separator — treat the whole line as a key with empty value.
	return strings.TrimSpace(s), ""
}

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

// Keys returns all translation keys in document order.
func (f *File) Keys() []string {
	keys := make([]string, 0, len(f.index))
	for _, ln := range f.lines {
		if ln.kind == lineEntry {
			keys = append(keys, ln.key)
		}
	}
	return keys
}

// UntranslatedKeys returns keys whose value is empty.
func (f *File) UntranslatedKeys() []string {
	var keys []string
	for _, ln := range f.lines {
		if ln.kind == lineEntry && ln.value == "" {
			keys = append(keys, ln.key)
		}
	}
	return keys
}

// Get returns the value for key and whether it was found.
func (f *File) Get(key string) (string, bool) {
	if idx, ok := f.index[key]; ok {
		return f.lines[idx].value, true
	}
	return "", false
}

// Set sets the value for an existing key. Returns true on success,
// false if the key does not exist.
func (f *File) Set(key, value string) bool {
	idx, ok := f.index[key]
	if !ok {
		return false
	}
	f.lines[idx].value = value
	return true
}

// Stats returns (total, translated, percentTranslated) for this file.
func (f *File) Stats() (int, int, float64) {
	total, translated := 0, 0
	for _, ln := range f.lines {
		if ln.kind == lineEntry {
			total++
			if ln.value != "" {
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
	for _, ln := range f.lines {
		if ln.kind == lineEntry {
			m[ln.key] = ln.value
		}
	}
	return m
}

// ---------------------------------------------------------------------------
// Serialization
// ---------------------------------------------------------------------------

// Marshal serialises the file back to .properties format.
func (f *File) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	for i, ln := range f.lines {
		switch ln.kind {
		case lineBlank:
			buf.WriteByte('\n')
		case lineComment:
			buf.WriteString(ln.raw)
			buf.WriteByte('\n')
		case lineEntry:
			buf.WriteString(ln.key)
			buf.WriteByte('=')
			buf.WriteString(ln.value)
			buf.WriteByte('\n')
		}
		_ = i
	}
	return buf.Bytes(), nil
}

// WriteFile serialises and writes to path, creating parent directories
// with 0755 permissions.
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

// NewTranslationFile creates an empty target File mirroring src's structure
// with all values cleared.
func NewTranslationFile(src *File) *File {
	f := &File{index: make(map[string]int)}
	for _, ln := range src.lines {
		idx := len(f.lines)
		cp := ln
		if ln.kind == lineEntry {
			cp.value = "" // clear value
			f.index[ln.key] = idx
		}
		f.lines = append(f.lines, cp)
	}
	return f
}

// SyncKeys ensures target has the same keys as src, preserving existing
// translations and adding empty entries for new keys. Keys removed from
// src are also removed from target.
func SyncKeys(src, target *File) {
	// Collect existing translations from target.
	existing := make(map[string]string, len(target.index))
	for _, ln := range target.lines {
		if ln.kind == lineEntry {
			existing[ln.key] = ln.value
		}
	}

	// Rebuild from source structure.
	rebuilt := NewTranslationFile(src)
	for k, v := range existing {
		if _, ok := rebuilt.index[k]; ok {
			rebuilt.lines[rebuilt.index[k]].value = v
		}
	}

	target.lines = rebuilt.lines
	target.index = rebuilt.index
}
