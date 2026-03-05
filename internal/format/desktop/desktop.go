package desktop

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var translatableKeys = map[string]struct{}{
	"Name":        {},
	"Comment":     {},
	"GenericName": {},
	"Keywords":    {},
}

type line struct {
	raw      string
	key      string
	lang     string
	hasEntry bool
}

type File struct {
	targetLang string
	lines      []line
	base       map[string]string
	localized  map[string]string
	order      []string
}

func ParseFile(path, targetLang string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return Parse(data, targetLang)
}

func Parse(data []byte, targetLang string) (*File, error) {
	f := &File{
		targetLang: targetLang,
		base:       make(map[string]string),
		localized:  make(map[string]string),
	}

	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	rows := strings.Split(text, "\n")
	for _, row := range rows {
		ln := line{raw: row}
		trimmed := strings.TrimSpace(row)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			f.lines = append(f.lines, ln)
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			f.lines = append(f.lines, ln)
			continue
		}

		eq := strings.Index(trimmed, "=")
		if eq < 0 {
			f.lines = append(f.lines, ln)
			continue
		}
		left := strings.TrimSpace(trimmed[:eq])
		val := strings.TrimSpace(trimmed[eq+1:])
		baseKey, lang, ok := parseField(left)
		if !ok {
			f.lines = append(f.lines, ln)
			continue
		}

		ln.hasEntry = true
		ln.key = baseKey
		ln.lang = lang
		if lang == "" {
			if _, exists := f.base[baseKey]; !exists {
				f.order = append(f.order, baseKey)
			}
			f.base[baseKey] = val
		} else if lang == targetLang {
			f.localized[baseKey] = val
		}

		f.lines = append(f.lines, ln)
	}

	return f, nil
}

func parseField(left string) (baseKey, lang string, ok bool) {
	if i := strings.Index(left, "["); i >= 0 && strings.HasSuffix(left, "]") {
		baseKey = left[:i]
		if _, ok = translatableKeys[baseKey]; !ok {
			return "", "", false
		}
		lang = left[i+1 : len(left)-1]
		if lang == "" {
			return "", "", false
		}
		return baseKey, lang, true
	}
	if _, ok = translatableKeys[left]; !ok {
		return "", "", false
	}
	return left, "", true
}

func (f *File) Keys() []string {
	keys := make([]string, len(f.order))
	copy(keys, f.order)
	return keys
}

func (f *File) UntranslatedKeys() []string {
	var out []string
	for _, k := range f.order {
		if f.localized[k] == "" {
			out = append(out, k)
		}
	}
	return out
}

func (f *File) Set(key, value string) bool {
	if _, ok := f.base[key]; !ok {
		return false
	}
	f.localized[key] = value
	return true
}

func (f *File) Stats() (int, int, float64) {
	total := len(f.order)
	translated := 0
	for _, k := range f.order {
		if f.localized[k] != "" {
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
	m := make(map[string]string, len(f.base))
	for _, k := range f.order {
		m[k] = f.base[k]
	}
	return m
}

func (f *File) Marshal() ([]byte, error) {
	var out bytes.Buffer
	written := make(map[string]bool)

	for _, ln := range f.lines {
		if ln.hasEntry && ln.lang == f.targetLang {
			val := f.localized[ln.key]
			out.WriteString(ln.key)
			out.WriteString("[")
			out.WriteString(f.targetLang)
			out.WriteString("]=")
			out.WriteString(val)
			out.WriteByte('\n')
			written[ln.key] = true
			continue
		}

		out.WriteString(ln.raw)
		out.WriteByte('\n')

		if ln.hasEntry && ln.lang == "" {
			if val, ok := f.localized[ln.key]; ok && val != "" && !written[ln.key] {
				out.WriteString(ln.key)
				out.WriteString("[")
				out.WriteString(f.targetLang)
				out.WriteString("]=")
				out.WriteString(val)
				out.WriteByte('\n')
				written[ln.key] = true
			}
		}
	}

	for _, k := range f.order {
		if written[k] {
			continue
		}
		if val := f.localized[k]; val != "" {
			out.WriteString(k)
			out.WriteString("[")
			out.WriteString(f.targetLang)
			out.WriteString("]=")
			out.WriteString(val)
			out.WriteByte('\n')
		}
	}

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

func NewTranslationFile(src *File, targetLang string) *File {
	lines := make([]line, len(src.lines))
	copy(lines, src.lines)
	base := make(map[string]string, len(src.base))
	for k, v := range src.base {
		base[k] = v
	}
	order := make([]string, len(src.order))
	copy(order, src.order)
	loc := make(map[string]string, len(order))
	for _, k := range order {
		loc[k] = ""
	}
	return &File{targetLang: targetLang, lines: lines, base: base, localized: loc, order: order}
}
