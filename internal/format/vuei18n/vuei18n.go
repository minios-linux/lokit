// Package vuei18n implements reading and writing of vue-i18n JSON files.
//
// Expected format is a nested JSON object with string leaf values:
//
//	{
//	  "buttons": {
//	    "save": "Save",
//	    "cancel": "Cancel"
//	  }
//	}
//
// Leaf values with empty strings are treated as untranslated.
// Non-string leaves are preserved and not translated.
package vuei18n

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type nodeKind int

const (
	nodeObject nodeKind = iota
	nodeArray
	nodeString
	nodeOther
)

type field struct {
	key   string
	value *node
}

type node struct {
	kind   nodeKind
	obj    []field
	arr    []*node
	str    string
	other  any
	quoted bool
}

// Entry represents one translatable string leaf.
type Entry struct {
	Path  string
	Value string
	node  *node
}

// File is a parsed vue-i18n translation file.
type File struct {
	root    *node
	entries []Entry
	index   map[string]int
}

// ParseFile reads and parses a vue-i18n JSON file.
func ParseFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return Parse(data)
}

// Parse parses vue-i18n JSON data.
func Parse(data []byte) (*File, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	first, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	root, err := parseNodeFromToken(dec, first)
	if err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}
	if root.kind != nodeObject {
		return nil, fmt.Errorf("JSON root must be an object")
	}

	f := &File{
		root:  root,
		index: make(map[string]int),
	}
	collectEntries(root, "", f)
	return f, nil
}

func parseNodeFromToken(dec *json.Decoder, tok json.Token) (*node, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			n := &node{kind: nodeObject}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("expected object key string, got %T", kt)
				}

				vt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				child, err := parseNodeFromToken(dec, vt)
				if err != nil {
					return nil, err
				}
				n.obj = append(n.obj, field{key: key, value: child})
			}

			end, err := dec.Token()
			if err != nil {
				return nil, err
			}
			if d, ok := end.(json.Delim); !ok || d != '}' {
				return nil, fmt.Errorf("expected }, got %v", end)
			}
			return n, nil

		case '[':
			n := &node{kind: nodeArray}
			for dec.More() {
				vt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				child, err := parseNodeFromToken(dec, vt)
				if err != nil {
					return nil, err
				}
				n.arr = append(n.arr, child)
			}

			end, err := dec.Token()
			if err != nil {
				return nil, err
			}
			if d, ok := end.(json.Delim); !ok || d != ']' {
				return nil, fmt.Errorf("expected ], got %v", end)
			}
			return n, nil
		}
		return nil, fmt.Errorf("unexpected delimiter %q", rune(t))

	case string:
		return &node{kind: nodeString, str: t}, nil

	default:
		return &node{kind: nodeOther, other: t}, nil
	}
}

func collectEntries(n *node, prefix string, f *File) {
	if n == nil || n.kind != nodeObject {
		return
	}

	for _, fld := range n.obj {
		path := fld.key
		if prefix != "" {
			path = prefix + "." + fld.key
		}

		switch fld.value.kind {
		case nodeObject:
			collectEntries(fld.value, path, f)
		case nodeString:
			idx := len(f.entries)
			f.entries = append(f.entries, Entry{Path: path, Value: fld.value.str, node: fld.value})
			f.index[path] = idx
		}
	}
}

// Keys returns all translatable key paths in document order.
func (f *File) Keys() []string {
	keys := make([]string, len(f.entries))
	for i, e := range f.entries {
		keys[i] = e.Path
	}
	return keys
}

// UntranslatedKeys returns key paths with empty values.
func (f *File) UntranslatedKeys() []string {
	var keys []string
	for _, e := range f.entries {
		if e.Value == "" {
			keys = append(keys, e.Path)
		}
	}
	return keys
}

// Get returns the value for path.
func (f *File) Get(path string) (string, bool) {
	idx, ok := f.index[path]
	if !ok {
		return "", false
	}
	return f.entries[idx].Value, true
}

// Set updates value for path.
func (f *File) Set(path, value string) bool {
	idx, ok := f.index[path]
	if !ok {
		return false
	}
	f.entries[idx].Value = value
	if f.entries[idx].node != nil {
		f.entries[idx].node.str = value
	}
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

// SourceValues returns key path -> value map from source file.
func (f *File) SourceValues() map[string]string {
	m := make(map[string]string, len(f.entries))
	for _, e := range f.entries {
		m[e.Path] = e.Value
	}
	return m
}

func cloneNode(n *node) *node {
	if n == nil {
		return nil
	}
	out := &node{kind: n.kind, str: n.str, other: n.other, quoted: n.quoted}
	if len(n.obj) > 0 {
		out.obj = make([]field, 0, len(n.obj))
		for _, fld := range n.obj {
			out.obj = append(out.obj, field{key: fld.key, value: cloneNode(fld.value)})
		}
	}
	if len(n.arr) > 0 {
		out.arr = make([]*node, 0, len(n.arr))
		for _, it := range n.arr {
			out.arr = append(out.arr, cloneNode(it))
		}
	}
	return out
}

// NewTranslationFile creates an empty target file from src structure.
func NewTranslationFile(src *File) *File {
	if src == nil {
		return &File{index: make(map[string]int)}
	}

	f := &File{
		root:  cloneNode(src.root),
		index: make(map[string]int),
	}
	collectEntries(f.root, "", f)
	for i := range f.entries {
		f.entries[i].Value = ""
		if f.entries[i].node != nil {
			f.entries[i].node.str = ""
		}
	}
	return f
}

// SyncKeys syncs target keys to source keys while preserving existing values.
func SyncKeys(src, target *File) {
	targetVals := map[string]string{}
	for _, e := range target.entries {
		targetVals[e.Path] = e.Value
	}

	ref := NewTranslationFile(src)
	for path, val := range targetVals {
		ref.Set(path, val)
	}

	target.root = ref.root
	target.entries = ref.entries
	target.index = ref.index
}

// Marshal serializes file to pretty JSON.
func (f *File) Marshal() ([]byte, error) {
	if f.root == nil {
		return []byte("{}\n"), nil
	}

	var b bytes.Buffer
	if err := writeNode(&b, f.root, 0); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

func writeIndent(b *bytes.Buffer, n int) {
	for i := 0; i < n; i++ {
		b.WriteByte(' ')
	}
}

func writeNode(b *bytes.Buffer, n *node, indent int) error {
	switch n.kind {
	case nodeObject:
		b.WriteByte('{')
		if len(n.obj) == 0 {
			b.WriteByte('}')
			return nil
		}
		b.WriteByte('\n')
		for i, fld := range n.obj {
			writeIndent(b, indent+2)
			k, _ := json.Marshal(fld.key)
			b.Write(k)
			b.WriteString(": ")
			if err := writeNode(b, fld.value, indent+2); err != nil {
				return err
			}
			if i < len(n.obj)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		writeIndent(b, indent)
		b.WriteByte('}')
		return nil

	case nodeArray:
		b.WriteByte('[')
		if len(n.arr) == 0 {
			b.WriteByte(']')
			return nil
		}
		b.WriteByte('\n')
		for i, it := range n.arr {
			writeIndent(b, indent+2)
			if err := writeNode(b, it, indent+2); err != nil {
				return err
			}
			if i < len(n.arr)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		writeIndent(b, indent)
		b.WriteByte(']')
		return nil

	case nodeString:
		s, _ := json.Marshal(n.str)
		b.Write(s)
		return nil

	case nodeOther:
		raw, err := json.Marshal(n.other)
		if err != nil {
			return err
		}
		b.Write(raw)
		return nil
	}

	return fmt.Errorf("unsupported node kind %d", n.kind)
}

// WriteFile writes file to disk.
func (f *File) WriteFile(path string) error {
	data, err := f.Marshal()
	if err != nil {
		return fmt.Errorf("marshaling JSON: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
