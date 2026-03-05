package polkit

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	actionRE      = regexp.MustCompile(`(?s)<action\s+id="([^"]+)"[^>]*>(.*?)</action>`)
	descriptionRE = regexp.MustCompile(`(?s)<description(?:\s+xml:lang="([^"]+)")?>(.*?)</description>`)
	messageRE     = regexp.MustCompile(`(?s)<message(?:\s+xml:lang="([^"]+)")?>(.*?)</message>`)
)

type File struct {
	targetLang   string
	raw          string
	keys         []string
	sourceValues map[string]string
	values       map[string]string
}

func ParseFile(path, targetLang string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return Parse(data, targetLang)
}

func Parse(data []byte, targetLang string) (*File, error) {
	raw := string(data)
	f := &File{
		targetLang:   targetLang,
		raw:          raw,
		sourceValues: make(map[string]string),
		values:       make(map[string]string),
	}

	for _, actionMatch := range actionRE.FindAllStringSubmatch(raw, -1) {
		actionID := actionMatch[1]
		body := actionMatch[2]

		collectTag(f, actionID, body, "description", descriptionRE)
		collectTag(f, actionID, body, "message", messageRE)
	}

	sort.Strings(f.keys)
	return f, nil
}

func collectTag(f *File, actionID, body, tag string, re *regexp.Regexp) {
	baseKey := actionID + "." + tag
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		lang := m[1]
		text := strings.TrimSpace(m[2])
		if lang == "" {
			if _, ok := f.sourceValues[baseKey]; !ok {
				f.keys = append(f.keys, baseKey)
			}
			f.sourceValues[baseKey] = text
			continue
		}
		if lang == f.targetLang {
			f.values[baseKey] = text
		}
	}
}

func (f *File) Keys() []string {
	keys := make([]string, len(f.keys))
	copy(keys, f.keys)
	return keys
}

func (f *File) UntranslatedKeys() []string {
	var out []string
	for _, k := range f.keys {
		if f.values[k] == "" {
			out = append(out, k)
		}
	}
	return out
}

func (f *File) Set(key, value string) bool {
	if _, ok := f.sourceValues[key]; !ok {
		return false
	}
	f.values[key] = value
	return true
}

func (f *File) Stats() (int, int, float64) {
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

func (f *File) SourceValues() map[string]string {
	m := make(map[string]string, len(f.sourceValues))
	for k, v := range f.sourceValues {
		m[k] = v
	}
	return m
}

func (f *File) Marshal() ([]byte, error) {
	out := f.raw
	for _, actionMatch := range actionRE.FindAllStringSubmatch(f.raw, -1) {
		actionID := actionMatch[1]
		fullBlock := actionMatch[0]
		body := actionMatch[2]

		updatedBody := applyTag(body, actionID, "description", f.targetLang, f.values, descriptionRE)
		updatedBody = applyTag(updatedBody, actionID, "message", f.targetLang, f.values, messageRE)

		updatedBlock := strings.Replace(fullBlock, body, updatedBody, 1)
		out = strings.Replace(out, fullBlock, updatedBlock, 1)
	}
	return []byte(out), nil
}

func applyTag(body, actionID, tag, lang string, values map[string]string, re *regexp.Regexp) string {
	key := actionID + "." + tag
	val := values[key]
	if val == "" {
		return body
	}

	for _, m := range re.FindAllStringSubmatch(body, -1) {
		if m[1] == lang {
			repl := "<" + tag + ` xml:lang="` + lang + `">` + val + "</" + tag + ">"
			return strings.Replace(body, m[0], repl, 1)
		}
	}

	// Insert localized tag after the base (non-lang) tag.
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		if m[1] == "" {
			insert := m[0] + "\n    <" + tag + ` xml:lang="` + lang + `">` + val + "</" + tag + ">"
			return strings.Replace(body, m[0], insert, 1)
		}
	}
	return body
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
