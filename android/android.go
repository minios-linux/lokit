// Package android implements reading and writing of Android strings.xml translation files.
//
// The expected file format is:
//
//	<?xml version="1.0" encoding="utf-8"?>
//	<resources>
//	    <string name="app_name">My App</string>
//	    <!-- Section comment -->
//	    <string name="hello">Hello</string>
//	</resources>
//
// Each <string> element has a "name" attribute (the key) and text content (the value).
// Empty text content means untranslated. Comments are preserved.
package android

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Data model
// ---------------------------------------------------------------------------

// Entry represents a single item in a strings.xml file.
// It can be either a <string> element or a comment.
type Entry struct {
	// Name is the string resource name (key). Empty for comments.
	Name string
	// Value is the translated text. Empty means untranslated.
	Value string
	// Comment is a comment line (without <!-- -->). Empty for string entries.
	Comment string
	// IsComment marks this entry as a comment (not a string resource).
	IsComment bool
}

// File represents a parsed Android strings.xml file.
type File struct {
	// Entries in document order (strings + comments).
	Entries []Entry
	// byName maps string name to index in Entries for fast lookup.
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

// Parse parses Android strings.xml data.
func Parse(data []byte) (*File, error) {
	f := &File{
		byName: make(map[string]int),
	}

	dec := xml.NewDecoder(strings.NewReader(string(data)))

	// We expect: <?xml?> <resources> ... </resources>
	// Walk through tokens to find <string> elements and comments.
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
			if t.Name.Local == "string" {
				name := ""
				for _, attr := range t.Attr {
					if attr.Name.Local == "name" {
						name = attr.Value
						break
					}
				}

				// Read the inner text content
				var innerContent strings.Builder
				if err := readElementContent(dec, &innerContent); err != nil {
					return nil, fmt.Errorf("reading <string name=%q>: %w", name, err)
				}

				value := innerContent.String()

				idx := len(f.Entries)
				f.Entries = append(f.Entries, Entry{
					Name:  name,
					Value: value,
				})
				if name != "" {
					f.byName[name] = idx
				}
			} else {
				// Skip other elements (string-array, plurals, etc.)
				dec.Skip()
			}

		case xml.Comment:
			if inResources {
				comment := strings.TrimSpace(string(t))
				if comment != "" {
					f.Entries = append(f.Entries, Entry{
						Comment:   comment,
						IsComment: true,
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

// readElementContent reads the full inner content of an XML element
// (text and child elements) until the matching end element, concatenating
// all character data. Handles simple cases (no nested elements expected
// for Android <string>).
func readElementContent(dec *xml.Decoder, b *strings.Builder) error {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			depth++
			// Write opening tag back (e.g., <xliff:g>)
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
	return nil
}

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

// StringEntries returns only the string entries (not comments), in document order.
func (f *File) StringEntries() []Entry {
	var result []Entry
	for _, e := range f.Entries {
		if !e.IsComment {
			result = append(result, e)
		}
	}
	return result
}

// Keys returns all string resource names in document order.
func (f *File) Keys() []string {
	var keys []string
	for _, e := range f.Entries {
		if !e.IsComment {
			keys = append(keys, e.Name)
		}
	}
	return keys
}

// UntranslatedKeys returns names of entries with empty values.
func (f *File) UntranslatedKeys() []string {
	var result []string
	for _, e := range f.Entries {
		if !e.IsComment && e.Value == "" {
			result = append(result, e.Name)
		}
	}
	return result
}

// UntranslatedEntries returns entries with empty values.
func (f *File) UntranslatedEntries() []Entry {
	var result []Entry
	for _, e := range f.Entries {
		if !e.IsComment && e.Value == "" {
			result = append(result, e)
		}
	}
	return result
}

// Stats returns (total, translated, untranslated) counts for string entries.
func (f *File) Stats() (total, translated, untranslated int) {
	for _, e := range f.Entries {
		if e.IsComment {
			continue
		}
		total++
		if e.Value != "" {
			translated++
		} else {
			untranslated++
		}
	}
	return
}

// Get returns the value for a given key name.
func (f *File) Get(name string) (string, bool) {
	idx, ok := f.byName[name]
	if !ok {
		return "", false
	}
	return f.Entries[idx].Value, true
}

// Set sets the value for a given key name. Returns false if the key doesn't exist.
func (f *File) Set(name, value string) bool {
	idx, ok := f.byName[name]
	if !ok {
		return false
	}
	f.Entries[idx].Value = value
	return true
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

// WriteFile writes the strings.xml file to disk, preserving structure and comments.
func (f *File) WriteFile(path string) error {
	data := f.Marshal()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// Marshal produces the XML output matching Android strings.xml format.
func (f *File) Marshal() []byte {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n")
	b.WriteString("<resources>\n")

	for _, e := range f.Entries {
		if e.IsComment {
			b.WriteString(fmt.Sprintf("    <!-- %s -->\n", e.Comment))
			continue
		}
		// Escape the value for XML
		escaped := xmlEscape(e.Value)
		b.WriteString(fmt.Sprintf("    <string name=\"%s\">%s</string>\n", e.Name, escaped))
	}

	b.WriteString("</resources>\n")
	return []byte(b.String())
}

// ---------------------------------------------------------------------------
// Creating translation files from source
// ---------------------------------------------------------------------------

// NewTranslationFile creates a new File with the same keys and comments as the
// source but with all values empty (untranslated).
func NewTranslationFile(source *File) *File {
	f := &File{
		byName: make(map[string]int),
	}
	for _, e := range source.Entries {
		newEntry := Entry{
			Name:      e.Name,
			Value:     "", // empty = untranslated
			Comment:   e.Comment,
			IsComment: e.IsComment,
		}
		idx := len(f.Entries)
		f.Entries = append(f.Entries, newEntry)
		if !e.IsComment && e.Name != "" {
			f.byName[e.Name] = idx
		}
	}
	return f
}

// SyncKeys ensures the file has all keys from source. Missing keys are added
// with empty values. Returns the number of keys added.
func (f *File) SyncKeys(source *File) int {
	added := 0
	for _, e := range source.Entries {
		if e.IsComment {
			continue
		}
		if _, exists := f.byName[e.Name]; !exists {
			idx := len(f.Entries)
			f.Entries = append(f.Entries, Entry{
				Name:  e.Name,
				Value: "",
			})
			f.byName[e.Name] = idx
			added++
		}
	}
	return added
}

// ---------------------------------------------------------------------------
// Language detection from res/ directory
// ---------------------------------------------------------------------------

// DetectLanguages scans an Android res/ directory for values-XX/ directories
// that contain strings.xml, and returns the language codes.
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
		// Check if strings.xml exists in this directory
		stringsPath := filepath.Join(resDir, name, "strings.xml")
		if _, err := os.Stat(stringsPath); err == nil {
			// Convert Android locale format (e.g., "pt-rBR") to standard ("pt-BR")
			lang = androidLocaleToStandard(lang)
			langs = append(langs, lang)
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

// StringsXMLPath returns the path to strings.xml for a given language in the res/ directory.
func StringsXMLPath(resDir, lang string) string {
	dirName := AndroidLocaleDirName(lang)
	return filepath.Join(resDir, dirName, "strings.xml")
}

// SourceStringsXMLPath returns the path to the default (source) strings.xml.
func SourceStringsXMLPath(resDir string) string {
	return filepath.Join(resDir, "values", "strings.xml")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// xmlEscape escapes special XML characters in text content.
// Android strings.xml uses standard XML escaping plus some Android-specific rules.
func xmlEscape(s string) string {
	// Don't escape strings that already contain XML tags (e.g., <xliff:g>)
	if strings.Contains(s, "<") && strings.Contains(s, ">") {
		return s
	}
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	// Android: apostrophes must be escaped.
	// First remove any existing escaping to avoid double-escaping
	// (AI responses or parsed files may already contain \').
	s = strings.ReplaceAll(s, "\\'", "'")
	s = strings.ReplaceAll(s, "'", "\\'")
	return s
}

// androidLocaleToStandard converts Android locale format to standard BCP-47.
// Examples: "pt-rBR" -> "pt-BR", "zh-rCN" -> "zh-CN", "ru" -> "ru"
func androidLocaleToStandard(androidLocale string) string {
	// Handle region codes: "pt-rBR" -> "pt-BR"
	if idx := strings.Index(androidLocale, "-r"); idx >= 0 {
		return androidLocale[:idx] + "-" + androidLocale[idx+2:]
	}
	return androidLocale
}

// standardToAndroidLocale converts standard BCP-47 to Android locale format.
// Examples: "pt-BR" -> "pt-rBR", "zh-CN" -> "zh-rCN", "ru" -> "ru"
func standardToAndroidLocale(lang string) string {
	// Handle region codes: "pt-BR" -> "pt-rBR"
	parts := strings.SplitN(lang, "-", 2)
	if len(parts) == 2 && len(parts[1]) > 0 {
		return parts[0] + "-r" + parts[1]
	}
	return lang
}
