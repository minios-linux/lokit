// Package mdfile implements reading and writing of Markdown translation files.
//
// Each Markdown file is split into translatable segments:
//
//   - Frontmatter fields (YAML between --- delimiters) are stored as
//     individual segments with keys like "fm:title", "fm:description".
//
//   - The Markdown body is split on headings (# to ######) and horizontal
//     rules (---, ***, ___) into sections, stored as segments with keys
//     like "sec:0", "sec:1", ...
//
//   - Code blocks (``` ... ```) and HTML blocks are NOT split on and are
//     included verbatim in the section that contains them.
//
// The round-trip serialization reconstructs the original file structure.
package mdfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Segment model
// ---------------------------------------------------------------------------

// Segment is a single translatable unit extracted from the Markdown file.
type Segment struct {
	// Key identifies this segment (e.g. "fm:title", "sec:0").
	Key string
	// Value is the current translated text (empty = untranslated).
	Value string
}

// File represents a parsed Markdown translation file.
type File struct {
	// segments holds all translatable segments in document order.
	segments []Segment
	// index maps key → index in segments for fast lookup.
	index map[string]int
	// hasFrontmatter is true if the source has a YAML front matter block.
	hasFrontmatter bool
	// fmKeys is the ordered list of front matter field names.
	fmKeys []string
	// fmNode is the raw YAML node for front matter round-trip.
	fmNode *yaml.Node
	// sectionCount is how many body sections exist.
	sectionCount int
	// sectionSeps holds the separator string (heading/hr) that STARTS each section.
	// sectionSeps[0] is the separator before sections[0] body text (may be empty
	// if there's content before the first heading).
	sectionSeps []string
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

// codeBlockFence matches fenced code blocks (``` or ~~~).
var codeBlockFence = regexp.MustCompile("(?s)```[^`]*?```|~~~[^~]*?~~~")

// sectionSplitter matches headings and horizontal rules that delimit sections.
// It is only applied OUTSIDE of fenced code blocks.
var sectionSplitter = regexp.MustCompile(`(?m)^(#{1,6} .+|[-*_]{3,}\s*)$`)

// frontmatterBlock matches a YAML front matter block at the start of the file.
var frontmatterBlock = regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---\r?\n?`)

// ParseFile reads and parses a Markdown translation file.
func ParseFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return Parse(data)
}

// Parse parses Markdown data into a File.
func Parse(data []byte) (*File, error) {
	text := string(data)
	f := &File{
		index: make(map[string]int),
	}

	// --- Extract front matter ---
	if m := frontmatterBlock.FindStringSubmatchIndex(text); m != nil {
		fmRaw := text[m[2]:m[3]]
		text = text[m[1]:]

		var fmNode yaml.Node
		if err := yaml.Unmarshal([]byte(fmRaw), &fmNode); err == nil && len(fmNode.Content) > 0 {
			root := fmNode.Content[0]
			if root.Kind == yaml.MappingNode {
				f.hasFrontmatter = true
				f.fmNode = &fmNode
				for i := 0; i+1 < len(root.Content); i += 2 {
					keyNode := root.Content[i]
					valNode := root.Content[i+1]
					if valNode.Kind != yaml.ScalarNode {
						continue
					}
					f.fmKeys = append(f.fmKeys, keyNode.Value)
					key := "fm:" + keyNode.Value
					idx := len(f.segments)
					f.segments = append(f.segments, Segment{Key: key, Value: valNode.Value})
					f.index[key] = idx
				}
			}
		}
	}

	// --- Split body into sections ---
	// Find code block ranges to exclude from delimiter search.
	codeRanges := codeBlockFence.FindAllStringIndex(text, -1)

	// Find all delimiter matches OUTSIDE code blocks.
	allDelims := sectionSplitter.FindAllStringIndex(text, -1)
	var delims [][]int
	for _, loc := range allDelims {
		if !insideRanges(loc[0], codeRanges) {
			delims = append(delims, loc)
		}
	}

	// Build list of [start, end] for each section body between delimiters.
	// A section consists of: the delimiter line (heading/hr) + the text after it.
	type sectionSpan struct {
		sep  string // the heading/hr line (empty for the initial pre-heading block)
		body string
	}

	var spans []sectionSpan

	prev := 0
	if len(delims) == 0 {
		// No headings/hrs — the whole body is one section.
		body := strings.TrimSpace(text)
		if body != "" {
			spans = append(spans, sectionSpan{sep: "", body: body})
		}
	} else {
		// Content before the first delimiter.
		pre := strings.TrimSpace(text[:delims[0][0]])
		if pre != "" {
			spans = append(spans, sectionSpan{sep: "", body: pre})
		}
		for i, loc := range delims {
			sep := text[loc[0]:loc[1]]
			var bodyEnd int
			if i+1 < len(delims) {
				bodyEnd = delims[i+1][0]
			} else {
				bodyEnd = len(text)
			}
			body := strings.TrimSpace(text[loc[1]:bodyEnd])
			spans = append(spans, sectionSpan{sep: sep, body: body})
		}
		_ = prev
	}

	for i, sp := range spans {
		key := fmt.Sprintf("sec:%d", i)
		// Combine sep + body as the translatable unit.
		val := sp.sep
		if sp.body != "" {
			if val != "" {
				val += "\n\n" + sp.body
			} else {
				val = sp.body
			}
		}
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		idx := len(f.segments)
		f.segments = append(f.segments, Segment{Key: key, Value: val})
		f.index[key] = idx
		f.sectionSeps = append(f.sectionSeps, sp.sep)
	}
	f.sectionCount = len(spans)

	return f, nil
}

// ---------------------------------------------------------------------------
// Querying
// ---------------------------------------------------------------------------

// Keys returns all segment keys in document order.
func (f *File) Keys() []string {
	keys := make([]string, len(f.segments))
	for i, s := range f.segments {
		keys[i] = s.Key
	}
	return keys
}

// UntranslatedKeys returns keys whose value is empty.
func (f *File) UntranslatedKeys() []string {
	var keys []string
	for _, s := range f.segments {
		if s.Value == "" {
			keys = append(keys, s.Key)
		}
	}
	return keys
}

// Get returns the current value for the given key.
func (f *File) Get(key string) (string, bool) {
	idx, ok := f.index[key]
	if !ok {
		return "", false
	}
	return f.segments[idx].Value, true
}

// Set updates the value for the given key.
// Returns false if the key is not found.
func (f *File) Set(key, value string) bool {
	idx, ok := f.index[key]
	if !ok {
		return false
	}
	f.segments[idx].Value = value
	return true
}

// Stats returns (total, translated, percent).
func (f *File) Stats() (int, int, float64) {
	total := len(f.segments)
	translated := 0
	for _, s := range f.segments {
		if s.Value != "" {
			translated++
		}
	}
	pct := 0.0
	if total > 0 {
		pct = float64(translated) / float64(total) * 100
	}
	return total, translated, pct
}

// SourceValues returns a map of key → value.
func (f *File) SourceValues() map[string]string {
	m := make(map[string]string, len(f.segments))
	for _, s := range f.segments {
		m[s.Key] = s.Value
	}
	return m
}

// ---------------------------------------------------------------------------
// Marshaling
// ---------------------------------------------------------------------------

// Marshal serialises the file back to Markdown.
func (f *File) Marshal() ([]byte, error) {
	var buf bytes.Buffer

	// Front matter.
	if f.hasFrontmatter && f.fmNode != nil && len(f.fmNode.Content) > 0 {
		root := f.fmNode.Content[0]
		if root.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(root.Content); i += 2 {
				keyNode := root.Content[i]
				valNode := root.Content[i+1]
				if valNode.Kind != yaml.ScalarNode {
					continue
				}
				fmKey := "fm:" + keyNode.Value
				if idx, ok := f.index[fmKey]; ok {
					valNode.Value = f.segments[idx].Value
				}
			}
		}
		fmBytes, err := yaml.Marshal(f.fmNode)
		if err != nil {
			return nil, fmt.Errorf("marshaling frontmatter: %w", err)
		}
		// yaml.Marshal of a DocumentNode adds a "---\n" prefix automatically,
		// but let's be explicit.
		fmStr := strings.TrimSpace(string(fmBytes))
		// Remove leading "---" added by the yaml library if present.
		if strings.HasPrefix(fmStr, "---") {
			fmStr = strings.TrimPrefix(fmStr, "---")
			fmStr = strings.TrimSpace(fmStr)
		}
		buf.WriteString("---\n")
		buf.WriteString(fmStr)
		buf.WriteString("\n---\n\n")
	}

	// Body sections.
	secIdx := 0
	for _, seg := range f.segments {
		if strings.HasPrefix(seg.Key, "fm:") {
			continue
		}
		if seg.Value == "" {
			secIdx++
			continue
		}
		if secIdx > 0 || f.hasFrontmatter {
			// Separate sections with a blank line.
		}
		buf.WriteString(strings.TrimSpace(seg.Value))
		buf.WriteString("\n\n")
		secIdx++
	}

	result := bytes.TrimRight(buf.Bytes(), "\n")
	result = append(result, '\n')
	return result, nil
}

// WriteFile serialises the file and writes it to the given path.
func (f *File) WriteFile(path string) error {
	data, err := f.Marshal()
	if err != nil {
		return fmt.Errorf("marshaling Markdown: %w", err)
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
// as srcFile but all values cleared.
func NewTranslationFile(src *File) *File {
	f := &File{
		index:          make(map[string]int),
		hasFrontmatter: src.hasFrontmatter,
		fmKeys:         append([]string{}, src.fmKeys...),
		sectionCount:   src.sectionCount,
		sectionSeps:    append([]string{}, src.sectionSeps...),
	}

	// Deep-copy fmNode.
	if src.fmNode != nil {
		if data, err := yaml.Marshal(src.fmNode); err == nil {
			var node yaml.Node
			if err := yaml.Unmarshal(data, &node); err == nil {
				f.fmNode = &node
			}
		}
	}

	for _, seg := range src.segments {
		idx := len(f.segments)
		f.segments = append(f.segments, Segment{Key: seg.Key, Value: ""})
		f.index[seg.Key] = idx
	}

	return f
}

// SyncKeys ensures target has the same segment keys as src, preserving
// existing translations and adding empty segments for new keys.
func SyncKeys(src, target *File) {
	srcKeys := make(map[string]bool, len(src.segments))
	for _, s := range src.segments {
		srcKeys[s.Key] = true
	}

	targetVals := make(map[string]string, len(target.segments))
	for _, s := range target.segments {
		if srcKeys[s.Key] {
			targetVals[s.Key] = s.Value
		}
	}

	rebuilt := NewTranslationFile(src)
	for k, v := range targetVals {
		rebuilt.Set(k, v)
	}

	target.segments = rebuilt.segments
	target.index = rebuilt.index
	target.fmKeys = rebuilt.fmKeys
	target.fmNode = rebuilt.fmNode
	target.hasFrontmatter = rebuilt.hasFrontmatter
	target.sectionCount = rebuilt.sectionCount
	target.sectionSeps = rebuilt.sectionSeps
}

// insideRanges returns true if pos falls within any of the given [start,end) ranges.
func insideRanges(pos int, ranges [][]int) bool {
	for _, r := range ranges {
		if pos >= r[0] && pos < r[1] {
			return true
		}
	}
	return false
}
