// Package yamlfile implements reading and writing of YAML translation files.
//
// The expected file format is a nested YAML map with string leaf values:
//
//	greeting: Hello
//	nav:
//	  home: Home
//	  about: About
//
// Rails i18n style (locale as the top-level key) is also supported:
//
//	en:
//	  greeting: Hello
//	  nav:
//	    home: Home
//
// Leaf values with empty strings are treated as untranslated.
// Non-string leaves (numbers, booleans, arrays) are passed through unchanged.
// The package preserves the source file structure and key order on round-trip.
package yamlfile

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// File model
// ---------------------------------------------------------------------------

// Entry represents a single translatable leaf value.
type Entry struct {
	// Path is the dot-joined key path (e.g. "nav.home").
	Path string
	// Value is the current translation (empty = untranslated).
	Value string
	// Style is the original yaml scalar style for round-trip fidelity.
	Style yaml.Style
}

// File represents a parsed YAML translation file.
type File struct {
	// node is the root yaml.Node, used for round-trip writing.
	node *yaml.Node
	// entries stores all translatable leaf entries in document order.
	entries []Entry
	// index maps path → index in entries for fast lookup during apply.
	index map[string]int
	// rootLocaleKey is set when the file uses Rails i18n style (e.g. "en:").
	// The actual translations live one level deeper.
	rootLocaleKey string
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

// ParseFile reads and parses a YAML translation file.
func ParseFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return Parse(data)
}

// Parse parses YAML data into a File.
func Parse(data []byte) (*File, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	f := &File{
		index: make(map[string]int),
	}

	// yaml.Unmarshal wraps the document in a DocumentNode.
	if doc.Kind == 0 || len(doc.Content) == 0 {
		// Empty file — nothing to do.
		f.node = &doc
		return f, nil
	}
	f.node = &doc

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("YAML root must be a mapping, got kind %d", root.Kind)
	}

	// Detect Rails i18n style: single top-level key whose value is a mapping.
	if len(root.Content) == 2 {
		keyNode := root.Content[0]
		valNode := root.Content[1]
		if keyNode.Kind == yaml.ScalarNode && valNode.Kind == yaml.MappingNode {
			f.rootLocaleKey = keyNode.Value
			collectEntries(valNode, "", f)
			return f, nil
		}
	}

	// Standard flat/nested style.
	collectEntries(root, "", f)
	return f, nil
}

// collectEntries recursively walks a mapping node and appends leaf entries.
func collectEntries(node *yaml.Node, prefix string, f *File) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		key := keyNode.Value
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		switch valNode.Kind {
		case yaml.MappingNode:
			collectEntries(valNode, path, f)
		case yaml.ScalarNode:
			// Only translate string scalars — skip null, bool, int, float.
			if valNode.Tag != "" && valNode.Tag != "!!str" && valNode.Tag != "!str" {
				// Allow untagged (plain strings) and explicit string tags.
				// Skip !!bool, !!int, !!float, !!null.
				switch valNode.Tag {
				case "!!bool", "!!int", "!!float", "!!null":
					continue
				}
			}
			idx := len(f.entries)
			f.entries = append(f.entries, Entry{
				Path:  path,
				Value: valNode.Value,
				Style: valNode.Style,
			})
			f.index[path] = idx
		}
	}
}

// ---------------------------------------------------------------------------
// Querying
// ---------------------------------------------------------------------------

// Keys returns all entry paths in document order.
func (f *File) Keys() []string {
	keys := make([]string, len(f.entries))
	for i, e := range f.entries {
		keys[i] = e.Path
	}
	return keys
}

// UntranslatedKeys returns paths whose value is empty.
func (f *File) UntranslatedKeys() []string {
	var keys []string
	for _, e := range f.entries {
		if e.Value == "" {
			keys = append(keys, e.Path)
		}
	}
	return keys
}

// Get returns the current value for the given path.
func (f *File) Get(path string) (string, bool) {
	idx, ok := f.index[path]
	if !ok {
		return "", false
	}
	return f.entries[idx].Value, true
}

// Set updates the value for the given path.
// Returns false if the path is not in the file.
func (f *File) Set(path, value string) bool {
	idx, ok := f.index[path]
	if !ok {
		return false
	}
	f.entries[idx].Value = value
	return true
}

// Stats returns (total, translated, percent).
func (f *File) Stats() (int, int, float64) {
	total := len(f.entries)
	translated := 0
	for _, e := range f.entries {
		if e.Value != "" {
			translated++
		}
	}
	pct := 0.0
	if total > 0 {
		pct = float64(translated) / float64(total) * 100
	}
	return total, translated, pct
}

// SourceValues returns a map of path → value (for use as translation source).
func (f *File) SourceValues() map[string]string {
	m := make(map[string]string, len(f.entries))
	for _, e := range f.entries {
		m[e.Path] = e.Value
	}
	return m
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

// Marshal serialises the file back to YAML, preserving the original structure
// and scalar styles.
func (f *File) Marshal() ([]byte, error) {
	// Apply current entry values back into the node tree.
	if f.node != nil && len(f.node.Content) > 0 {
		root := f.node.Content[0]
		if root.Kind == yaml.MappingNode {
			if f.rootLocaleKey != "" && len(root.Content) == 2 {
				applyEntriesToNode(root.Content[1], "", f)
			} else {
				applyEntriesToNode(root, "", f)
			}
		}
	}
	return yaml.Marshal(f.node)
}

// applyEntriesToNode walks the node tree and updates scalar values from f.entries.
func applyEntriesToNode(node *yaml.Node, prefix string, f *File) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		key := keyNode.Value
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		switch valNode.Kind {
		case yaml.MappingNode:
			applyEntriesToNode(valNode, path, f)
		case yaml.ScalarNode:
			if idx, ok := f.index[path]; ok {
				valNode.Value = f.entries[idx].Value
				// Restore scalar style for round-trip fidelity.
				// If the new value needs quoting (empty string), force quoted style.
				if f.entries[idx].Style != 0 {
					valNode.Style = f.entries[idx].Style
				} else if valNode.Value == "" {
					valNode.Style = yaml.DoubleQuotedStyle
				}
			}
		}
	}
}

// WriteFile serialises the file and writes it to the given path.
func (f *File) WriteFile(path string) error {
	data, err := f.Marshal()
	if err != nil {
		return fmt.Errorf("marshaling YAML: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Sync (create target from source)
// ---------------------------------------------------------------------------

// NewTranslationFile creates an empty target File with the same structure
// as srcFile but with all values cleared (ready for translation).
// The targetLocale is used when the source file uses Rails i18n style.
func NewTranslationFile(srcFile *File, targetLocale string) *File {
	// Deep-copy the source node tree.
	srcData, err := yaml.Marshal(srcFile.node)
	if err != nil {
		return &File{index: make(map[string]int)}
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(srcData, &doc); err != nil {
		return &File{index: make(map[string]int)}
	}

	f := &File{
		node:  &doc,
		index: make(map[string]int),
	}

	if len(doc.Content) == 0 {
		return f
	}

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return f
	}

	// Handle Rails i18n style — replace the root locale key with targetLocale.
	if srcFile.rootLocaleKey != "" && len(root.Content) == 2 {
		root.Content[0].Value = targetLocale
		f.rootLocaleKey = targetLocale
		clearAndCollect(root.Content[1], "", f)
	} else {
		clearAndCollect(root, "", f)
	}

	return f
}

// clearAndCollect walks the node tree, clears all string scalar values,
// and populates f.entries and f.index.
func clearAndCollect(node *yaml.Node, prefix string, f *File) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		key := keyNode.Value
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		switch valNode.Kind {
		case yaml.MappingNode:
			clearAndCollect(valNode, path, f)
		case yaml.ScalarNode:
			switch valNode.Tag {
			case "!!bool", "!!int", "!!float", "!!null":
				continue
			}
			style := valNode.Style
			valNode.Value = ""
			valNode.Style = yaml.DoubleQuotedStyle

			idx := len(f.entries)
			f.entries = append(f.entries, Entry{
				Path:  path,
				Value: "",
				Style: style,
			})
			f.index[path] = idx
		}
	}
}

// SyncKeys ensures the target file has the same keys as the source file.
// New keys from source are added with empty values; keys missing in source
// are removed from the target.
func SyncKeys(src, target *File) {
	// Build a set of source keys.
	srcKeys := make(map[string]bool, len(src.entries))
	for _, e := range src.entries {
		srcKeys[e.Path] = true
	}

	// Remove keys from target that no longer exist in source.
	// (We do this by rebuilding entries/index from the node walk after patching.)
	// Simpler approach: rebuild target from scratch preserving existing values.
	targetVals := make(map[string]string, len(target.entries))
	for _, e := range target.entries {
		if srcKeys[e.Path] {
			targetVals[e.Path] = e.Value
		}
	}

	// Rebuild target node from source structure, then fill in existing values.
	rebuilt := NewTranslationFile(src, target.rootLocaleKey)
	for path, val := range targetVals {
		rebuilt.Set(path, val)
	}

	// Replace target internals.
	target.node = rebuilt.node
	target.entries = rebuilt.entries
	target.index = rebuilt.index
	target.rootLocaleKey = rebuilt.rootLocaleKey
}
