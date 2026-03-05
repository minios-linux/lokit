package jskv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var assignRE = regexp.MustCompile(`(?s)^\s*([A-Za-z_$][A-Za-z0-9_$.]*)\s*=\s*(\{.*\})\s*;?\s*$`)

type File struct {
	prefix       string
	keys         []string
	translations map[string]string
}

func ParseFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return Parse(data)
}

func Parse(data []byte) (*File, error) {
	m := assignRE.FindSubmatch(data)
	if len(m) != 3 {
		return nil, fmt.Errorf("invalid js-kv format: expected '<expr> = { ... };'")
	}
	prefix := string(m[1])
	jsonObj := m[2]

	keys, vals, err := parseOrderedStringMap(jsonObj)
	if err != nil {
		return nil, err
	}

	return &File{prefix: prefix, keys: keys, translations: vals}, nil
}

func parseOrderedStringMap(data []byte) ([]string, map[string]string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, fmt.Errorf("invalid JSON object: %w", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return nil, nil, fmt.Errorf("expected JSON object")
	}

	keys := make([]string, 0)
	vals := make(map[string]string)
	for dec.More() {
		ktok, err := dec.Token()
		if err != nil {
			return nil, nil, fmt.Errorf("reading key: %w", err)
		}
		k, ok := ktok.(string)
		if !ok {
			return nil, nil, fmt.Errorf("expected string key")
		}

		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, nil, fmt.Errorf("reading value for key %q: %w", k, err)
		}
		s, ok := v.(string)
		if !ok {
			return nil, nil, fmt.Errorf("expected string value for key %q", k)
		}

		if _, exists := vals[k]; !exists {
			keys = append(keys, k)
		}
		vals[k] = s
	}

	tok, err = dec.Token()
	if err != nil {
		return nil, nil, fmt.Errorf("invalid JSON object terminator: %w", err)
	}
	delim, ok = tok.(json.Delim)
	if !ok || delim != '}' {
		return nil, nil, fmt.Errorf("expected JSON object terminator")
	}

	return keys, vals, nil
}

func (f *File) Keys() []string {
	keys := make([]string, len(f.keys))
	copy(keys, f.keys)
	return keys
}

func (f *File) UntranslatedKeys() []string {
	out := make([]string, 0)
	for _, k := range f.keys {
		if f.translations[k] == "" {
			out = append(out, k)
		}
	}
	return out
}

func (f *File) Set(key, value string) bool {
	if _, ok := f.translations[key]; !ok {
		return false
	}
	f.translations[key] = value
	return true
}

func (f *File) Stats() (int, int, float64) {
	total := len(f.keys)
	translated := 0
	for _, k := range f.keys {
		if f.translations[k] != "" {
			translated++
		}
	}
	pct := 0.0
	if total > 0 {
		pct = float64(translated) / float64(total) * 100
	}
	return total, translated, pct
}

func (f *File) SourceValues() map[string]string {
	m := make(map[string]string, len(f.keys))
	for _, k := range f.keys {
		m[k] = k
	}
	return m
}

func (f *File) Marshal() ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(f.prefix)
	out.WriteString(" = {")
	if len(f.keys) > 0 {
		out.WriteByte('\n')
		for i, k := range f.keys {
			keyJSON, _ := json.Marshal(k)
			valJSON, _ := json.Marshal(f.translations[k])
			out.WriteString("    ")
			out.Write(keyJSON)
			out.WriteString(": ")
			out.Write(valJSON)
			if i < len(f.keys)-1 {
				out.WriteByte(',')
			}
			out.WriteByte('\n')
		}
	}
	out.WriteString("};\n")
	return out.Bytes(), nil
}

func (f *File) WriteFile(path string) error {
	data, err := f.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func NewTranslationFile(src *File) *File {
	keys := make([]string, len(src.keys))
	copy(keys, src.keys)
	translations := make(map[string]string, len(keys))
	for _, k := range keys {
		translations[k] = ""
	}
	return &File{prefix: src.prefix, keys: keys, translations: translations}
}
