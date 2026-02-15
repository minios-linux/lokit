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
//
// Recipe translation files use a simpler format:
//
//	{
//	    "name": "Translated name",
//	    "description": "Translated description",
//	    "longDescription": "Translated long description"
//	}
package i18next

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// UI translation file (public/translations/{lang}.json)
// ---------------------------------------------------------------------------

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

	// Read opening brace
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := t.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("expected {, got %v", t)
	}

	om := &orderedMap{
		values: make(map[string]string),
	}

	for dec.More() {
		// Read key
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("expected string key, got %T", kt)
		}

		// Read value
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
	// Fallback: sorted keys
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

	// _meta
	b.WriteString("    \"_meta\": {\n")
	b.WriteString(fmt.Sprintf("        \"name\": %s,\n", jsonString(f.Meta.Name)))
	b.WriteString(fmt.Sprintf("        \"flag\": %s\n", jsonString(f.Meta.Flag)))
	b.WriteString("    },\n")

	// translations — preserve original key order, or sorted
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

// ---------------------------------------------------------------------------
// Recipe translation file (public/data/recipe-translations/{lang}/{id}.json)
// ---------------------------------------------------------------------------

// RecipeTranslation represents a per-recipe translation file.
type RecipeTranslation struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	LongDescription string `json:"longDescription"`
}

// ParseRecipeFile reads a recipe translation JSON file.
func ParseRecipeFile(path string) (*RecipeTranslation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rt RecipeTranslation
	if err := json.Unmarshal(data, &rt); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &rt, nil
}

// WriteRecipeFile writes a recipe translation JSON file.
func (rt *RecipeTranslation) WriteRecipeFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	data, err := json.MarshalIndent(rt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// IsTranslated returns true if at least name and description are non-empty.
func (rt *RecipeTranslation) IsTranslated() bool {
	return rt.Name != "" && rt.Description != ""
}

// IsFullyTranslated returns true if all three fields are non-empty.
func (rt *RecipeTranslation) IsFullyTranslated() bool {
	return rt.Name != "" && rt.Description != "" && rt.LongDescription != ""
}

// ---------------------------------------------------------------------------
// Blog post translation (data/blog/posts/translations/{slug}.{lang}.md)
// ---------------------------------------------------------------------------

// BlogPost represents a parsed blog post with YAML frontmatter and Markdown body.
// Only translatable fields are exposed; inherited fields are preserved as raw YAML.
type BlogPost struct {
	// Translatable fields
	Title   string
	Excerpt string
	Content string // Markdown body after frontmatter

	// Inherited fields (kept as-is from the source post)
	Author             string
	PublishedAt        string
	UpdatedAt          string
	Tags               []string
	FeaturedImage      string
	Published          bool
	Order              int
	TelegramDiscussion string
	TelegramPostId     int
}

// ParseBlogPost reads a Markdown file with YAML frontmatter.
func ParseBlogPost(path string) (*BlogPost, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBlogPostData(data)
}

// ParseBlogPostData parses blog post data from bytes.
func ParseBlogPostData(data []byte) (*BlogPost, error) {
	content := string(data)

	// Split frontmatter and body
	frontmatter, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, err
	}

	bp := &BlogPost{
		Content:   body,
		Published: true, // default
	}

	// Parse YAML frontmatter line by line (simple key-value parser)
	// We need to handle multi-line values (excerpt with >-) and arrays (tags)
	parseBlogFrontmatter(frontmatter, bp)

	return bp, nil
}

// splitFrontmatter splits a markdown file into YAML frontmatter and body.
func splitFrontmatter(content string) (frontmatter, body string, err error) {
	content = strings.TrimLeft(content, "\n\r")
	if !strings.HasPrefix(content, "---") {
		return "", content, fmt.Errorf("no frontmatter found")
	}

	// Find closing ---
	rest := content[3:]
	rest = strings.TrimLeft(rest, " \t")
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
		rest = rest[2:]
	}

	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return "", content, fmt.Errorf("unclosed frontmatter")
	}

	frontmatter = rest[:idx]
	body = rest[idx+4:] // skip \n---
	// Trim leading newline from body
	body = strings.TrimLeft(body, "\n\r")

	return frontmatter, body, nil
}

// parseBlogFrontmatter parses simple YAML frontmatter into a BlogPost.
// Handles scalar values, multi-line strings (>-), and simple arrays.
func parseBlogFrontmatter(fm string, bp *BlogPost) {
	lines := strings.Split(fm, "\n")
	var currentKey string
	var multiLineValue strings.Builder
	var inMultiLine bool
	var inArray bool
	var arrayKey string

	flushMultiLine := func() {
		if !inMultiLine || currentKey == "" {
			return
		}
		val := multiLineValue.String()
		// Trim trailing paragraph breaks and whitespace
		val = strings.TrimRight(val, "\n ")
		// Normalize multiple consecutive paragraph breaks to single \n\n
		for strings.Contains(val, "\n\n\n") {
			val = strings.ReplaceAll(val, "\n\n\n", "\n\n")
		}
		// Clean up any leading/trailing whitespace
		val = strings.TrimSpace(val)
		setBlogField(bp, currentKey, val)
		inMultiLine = false
		currentKey = ""
		multiLineValue.Reset()
	}

	flushArray := func() {
		inArray = false
		arrayKey = ""
	}

	for _, line := range lines {
		// Check for array item
		trimmed := strings.TrimSpace(line)
		if inArray && strings.HasPrefix(trimmed, "- ") {
			item := strings.TrimPrefix(trimmed, "- ")
			item = unquoteYAML(item)
			if arrayKey == "tags" {
				bp.Tags = append(bp.Tags, item)
			}
			continue
		} else if inArray && (trimmed == "" || (!strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t"))) {
			flushArray()
		}

		// Check for multi-line continuation (>- folded block scalar)
		if inMultiLine {
			// Blank lines within a >- block are paragraph breaks, not end markers.
			// The block only ends when we hit a non-blank line at column 0 (a new key).
			if trimmed == "" {
				// Blank line = paragraph break in >- folded style
				multiLineValue.WriteString("\n\n")
				continue
			}
			if strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") {
				// Indented continuation line — fold with space
				if multiLineValue.Len() > 0 {
					// Don't add space if we just wrote a paragraph break
					s := multiLineValue.String()
					if !strings.HasSuffix(s, "\n\n") {
						multiLineValue.WriteByte(' ')
					}
				}
				multiLineValue.WriteString(strings.TrimSpace(line))
				continue
			}
			// Non-blank, non-indented line — end of block, process as new key
			flushMultiLine()
		}

		// Parse key: value
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}

		key := strings.TrimSpace(line[:colonIdx])
		val := strings.TrimSpace(line[colonIdx+1:])

		// Multi-line value (>- or >)
		if val == ">-" || val == ">" || val == "|" || val == "|-" {
			currentKey = key
			inMultiLine = true
			multiLineValue.Reset()
			continue
		}

		// Array start
		if val == "" {
			// Could be array start — check next line
			arrayKey = key
			inArray = true
			continue
		}

		// Simple value
		val = unquoteYAML(val)
		setBlogField(bp, key, val)
	}

	flushMultiLine()
}

// setBlogField sets a BlogPost field by YAML key name.
func setBlogField(bp *BlogPost, key, val string) {
	switch key {
	case "title":
		bp.Title = val
	case "excerpt":
		bp.Excerpt = val
	case "author":
		bp.Author = val
	case "publishedAt":
		bp.PublishedAt = val
	case "updatedAt":
		bp.UpdatedAt = val
	case "featuredImage":
		bp.FeaturedImage = val
	case "published":
		bp.Published = val == "true"
	case "order":
		if n, err := parseInt(val); err == nil {
			bp.Order = n
		}
	case "telegramDiscussion":
		bp.TelegramDiscussion = val
	case "telegramPostId":
		if n, err := parseInt(val); err == nil {
			bp.TelegramPostId = n
		}
	}
}

// unquoteYAML removes surrounding quotes from a YAML value.
func unquoteYAML(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// parseInt parses a string as an integer.
func parseInt(s string) (int, error) {
	n := 0
	neg := false
	if len(s) > 0 && s[0] == '-' {
		neg = true
		s = s[1:]
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number: %s", s)
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

// WriteBlogPost writes a blog post to disk as Markdown with YAML frontmatter.
func (bp *BlogPost) WriteBlogPost(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	var b strings.Builder
	b.WriteString("---\n")

	// Title
	b.WriteString(fmt.Sprintf("title: %s\n", yamlString(bp.Title)))

	// Excerpt — use >- folded style for multi-line
	if bp.Excerpt != "" {
		b.WriteString("excerpt: >-\n")
		// Split excerpt into paragraphs (separated by \n\n)
		paragraphs := strings.Split(bp.Excerpt, "\n\n")
		for i, para := range paragraphs {
			para = strings.TrimSpace(para)
			if para == "" {
				continue
			}
			// Wrap each paragraph at ~78 chars with 2-space indent
			for _, wline := range wrapText(para, 76) {
				b.WriteString("  " + wline + "\n")
			}
			// Add blank line between paragraphs (YAML >- style)
			if i < len(paragraphs)-1 {
				b.WriteString("\n\n")
			}
		}
	}

	// Author
	if bp.Author != "" {
		b.WriteString(fmt.Sprintf("author: %s\n", bp.Author))
	}

	// Dates
	if bp.PublishedAt != "" {
		b.WriteString(fmt.Sprintf("publishedAt: '%s'\n", bp.PublishedAt))
	}
	if bp.UpdatedAt != "" {
		b.WriteString(fmt.Sprintf("updatedAt: '%s'\n", bp.UpdatedAt))
	}

	// Tags
	if len(bp.Tags) > 0 {
		b.WriteString("tags:\n")
		for _, tag := range bp.Tags {
			b.WriteString(fmt.Sprintf("  - %s\n", tag))
		}
	}

	// Featured image
	if bp.FeaturedImage != "" {
		b.WriteString(fmt.Sprintf("featuredImage: '%s'\n", bp.FeaturedImage))
	}

	// Published
	if bp.Published {
		b.WriteString("published: true\n")
	} else {
		b.WriteString("published: false\n")
	}

	// Telegram fields (only if present in source)
	if bp.TelegramPostId != 0 {
		b.WriteString(fmt.Sprintf("telegramPostId: %d\n", bp.TelegramPostId))
	}
	if bp.TelegramDiscussion != "" {
		b.WriteString(fmt.Sprintf("telegramDiscussion: '%s'\n", bp.TelegramDiscussion))
	}

	b.WriteString("---\n")

	// Body
	b.WriteString(bp.Content)

	// Ensure trailing newline
	result := b.String()
	if len(result) > 0 && result[len(result)-1] != '\n' {
		result += "\n"
	}

	return os.WriteFile(path, []byte(result), 0644)
}

// IsTranslatedBlog returns true if the blog post has translated title and content.
func (bp *BlogPost) IsTranslatedBlog() bool {
	return bp.Title != "" && bp.Content != ""
}

// BlogPostSlugs returns all blog post slugs in the given directory.
func BlogPostSlugs(postsDir string) ([]string, error) {
	entries, err := os.ReadDir(postsDir)
	if err != nil {
		return nil, err
	}
	var slugs []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		slugs = append(slugs, strings.TrimSuffix(name, ".md"))
	}
	sort.Strings(slugs)
	return slugs, nil
}

// BlogTranslationPath returns the path to a blog post translation file.
func BlogTranslationPath(postsDir, slug, lang string) string {
	return filepath.Join(postsDir, "translations", fmt.Sprintf("%s.%s.md", slug, lang))
}

// BlogTranslationLangs returns the list of languages for which a translation exists.
func BlogTranslationLangs(postsDir, slug string) []string {
	transDir := filepath.Join(postsDir, "translations")
	entries, err := os.ReadDir(transDir)
	if err != nil {
		return nil
	}
	prefix := slug + "."
	var langs []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".md") {
			continue
		}
		// Extract lang code: slug.LANG.md
		lang := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".md")
		if lang != "" {
			langs = append(langs, lang)
		}
	}
	sort.Strings(langs)
	return langs
}

// yamlString returns a YAML-safe string representation.
// Uses quoting only when necessary.
func yamlString(s string) string {
	// Check if quoting is needed
	needsQuote := false
	for _, c := range s {
		if c == ':' || c == '#' || c == '\'' || c == '"' || c == '\n' {
			needsQuote = true
			break
		}
	}
	if len(s) > 0 && (s[0] == ' ' || s[0] == '{' || s[0] == '[' || s[0] == '>' || s[0] == '|') {
		needsQuote = true
	}
	if !needsQuote {
		return s
	}
	// Use single quotes with '' escaping for internal single quotes
	escaped := strings.ReplaceAll(s, "'", "''")
	return "'" + escaped + "'"
}

// wrapText wraps text to the given width, breaking at word boundaries.
func wrapText(text string, width int) []string {
	if len(text) <= width {
		return []string{text}
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}

	var lines []string
	var current strings.Builder

	for _, word := range words {
		if current.Len() > 0 && current.Len()+1+len(word) > width {
			lines = append(lines, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteByte(' ')
		}
		current.WriteString(word)
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}

	return lines
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// jsonString returns a JSON-encoded string value (with proper escaping).
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// LangMeta contains known language metadata for auto-fill.
var LangMeta = map[string]Meta{
	"en":    {Name: "English", Flag: "🇺🇸"},
	"ru":    {Name: "Русский", Flag: "🇷🇺"},
	"de":    {Name: "Deutsch", Flag: "🇩🇪"},
	"es":    {Name: "Español", Flag: "🇪🇸"},
	"fr":    {Name: "Français", Flag: "🇫🇷"},
	"it":    {Name: "Italiano", Flag: "🇮🇹"},
	"pt-BR": {Name: "Português (Brasil)", Flag: "🇧🇷"},
	"id":    {Name: "Bahasa Indonesia", Flag: "🇮🇩"},
	"ja":    {Name: "日本語", Flag: "🇯🇵"},
	"ko":    {Name: "한국어", Flag: "🇰🇷"},
	"zh-CN": {Name: "简体中文", Flag: "🇨🇳"},
	"zh-TW": {Name: "繁體中文", Flag: "🇹🇼"},
	"ar":    {Name: "العربية", Flag: "🇸🇦"},
	"tr":    {Name: "Türkçe", Flag: "🇹🇷"},
	"pl":    {Name: "Polski", Flag: "🇵🇱"},
	"uk":    {Name: "Українська", Flag: "🇺🇦"},
	"nl":    {Name: "Nederlands", Flag: "🇳🇱"},
	"sv":    {Name: "Svenska", Flag: "🇸🇪"},
	"cs":    {Name: "Čeština", Flag: "🇨🇿"},
	"ro":    {Name: "Română", Flag: "🇷🇴"},
	"hu":    {Name: "Magyar", Flag: "🇭🇺"},
	"el":    {Name: "Ελληνικά", Flag: "🇬🇷"},
	"da":    {Name: "Dansk", Flag: "🇩🇰"},
	"fi":    {Name: "Suomi", Flag: "🇫🇮"},
	"no":    {Name: "Norsk", Flag: "🇳🇴"},
	"th":    {Name: "ไทย", Flag: "🇹🇭"},
	"vi":    {Name: "Tiếng Việt", Flag: "🇻🇳"},
	"hi":    {Name: "हिन्दी", Flag: "🇮🇳"},
}
