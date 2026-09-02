package markdown

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

type FieldID string

type FieldKind string

const (
	FieldProse     FieldKind = "prose"
	FieldLinkLabel FieldKind = "link-label"
	FieldImageAlt  FieldKind = "image-alt"
	FieldHTMLText  FieldKind = "html-text"
)

type TextField struct {
	ID      FieldID   `json:"id"`
	Source  string    `json:"source"`
	Kind    FieldKind `json:"kind"`
	Context string    `json:"context"`
	Start   int       `json:"-"`
	End     int       `json:"-"`
}

type ProviderField struct {
	ID      FieldID   `json:"id"`
	Source  string    `json:"source"`
	Kind    FieldKind `json:"kind"`
	Context string    `json:"context"`
}

type MarkdownPlan struct {
	source      []byte
	fields      []TextField
	references  []referenceRender
	fingerprint string
}

type referenceRender struct {
	labelStart  int
	labelEnd    int
	suffixStart int
	suffixEnd   int
	rawLabel    string
	fieldIDs    []FieldID
}

var providerAuthoredURL = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*://|www\.)`)

func BuildPlan(documentID, segmentKey string, source []byte) (*MarkdownPlan, error) {
	ownedSource := append([]byte(nil), source...)
	parser := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()
	document := parser.Parse(text.NewReader(ownedSource))
	immutableRanges := markdownImmutableTextRanges(document, ownedSource)
	rawHTMLRanges := markdownHTMLRawTextRanges(ownedSource, immutableRanges)
	immutableRanges = append(immutableRanges, rawHTMLRanges...)
	seed := sha256.Sum256(bytes.Join([][]byte{[]byte(documentID), []byte(segmentKey), ownedSource}, []byte{0}))
	plan := &MarkdownPlan{source: ownedSource}
	groups := make(map[ast.Node]int)
	nextGroup := 0
	err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || node.Kind() != ast.KindText {
			return ast.WalkContinue, nil
		}
		textNode := node.(*ast.Text)
		if markdownTextIsImmutable(textNode) {
			return ast.WalkContinue, nil
		}
		start, end := textNode.Segment.Start, textNode.Segment.Stop
		if start < 0 || end < start || end > len(ownedSource) || start == end {
			return ast.WalkContinue, nil
		}
		if markdownSpanOverlaps(start, end, immutableRanges) {
			return ast.WalkContinue, nil
		}
		owner := markdownTextOwner(textNode)
		group, ok := groups[owner]
		if !ok {
			group = nextGroup
			groups[owner] = group
			nextGroup++
		}
		kind := FieldProse
		for parent := textNode.Parent(); parent != nil && parent != owner.Parent(); parent = parent.Parent() {
			switch parent.Kind() {
			case ast.KindLink:
				kind = FieldLinkLabel
			case ast.KindImage:
				kind = FieldImageAlt
			}
		}
		plan.fields = appendMarkdownTextField(plan.fields, ownedSource, start, end, kind, fmt.Sprintf("%s:%d", owner.Kind().String(), group))
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	expressionRanges := markdownVitePressExpressionRanges(ownedSource, immutableRanges)
	immutableRanges = append(immutableRanges, expressionRanges...)
	for _, htmlField := range markdownHTMLTextFields(ownedSource, len(groups), immutableRanges) {
		overlaps := markdownSpanOverlaps(htmlField.Start, htmlField.End, immutableRanges)
		for _, field := range plan.fields {
			if htmlField.Start < field.End && field.Start < htmlField.End {
				overlaps = true
				break
			}
		}
		if !overlaps {
			plan.fields = append(plan.fields, htmlField)
		}
	}
	plan.fields = normalizeVitePressContainerFields(plan.fields, ownedSource)
	plan.fields = subtractMarkdownRanges(plan.fields, expressionRanges)
	sort.Slice(plan.fields, func(i, j int) bool { return plan.fields[i].Start < plan.fields[j].Start })
	merged := make([]TextField, 0, len(plan.fields))
	for _, field := range plan.fields {
		if len(merged) > 0 && merged[len(merged)-1].End == field.Start &&
			merged[len(merged)-1].Kind == field.Kind && merged[len(merged)-1].Context == field.Context {
			merged[len(merged)-1].End = field.End
			merged[len(merged)-1].Source += field.Source
			continue
		}
		merged = append(merged, field)
	}
	plan.fields = merged
	for i := range plan.fields {
		plan.fields[i].ID = FieldID(fmt.Sprintf("md-%x-f%04d", seed[:12], i))
	}
	plan.references = markdownReferenceRenders(document, ownedSource, plan.fields)
	for i := 1; i < len(plan.fields); i++ {
		if plan.fields[i].Start < plan.fields[i-1].End {
			return nil, fmt.Errorf("overlapping Markdown text fields at byte %d", plan.fields[i].Start)
		}
	}
	plan.fingerprint = markdownStructureFingerprint(document, ownedSource)
	return plan, nil
}

type markdownByteRange struct {
	start int
	end   int
}

func markdownImmutableTextRanges(document ast.Node, source []byte) []markdownByteRange {
	ranges := markdownFenceByteRanges(source)
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node.Kind() {
		case ast.KindCodeSpan:
			if start, end, ok := markdownNodeTextBounds(node); ok {
				ranges = append(ranges, markdownInlineCodeRange(source, start, end))
			}
		case ast.KindCodeBlock, ast.KindFencedCodeBlock:
			lines := node.Lines()
			for i := 0; i < lines.Len(); i++ {
				segment := lines.At(i)
				ranges = append(ranges, markdownByteRange{start: segment.Start, end: segment.Stop})
			}
		}
		return ast.WalkContinue, nil
	})
	return ranges
}

func markdownFenceByteRanges(source []byte) []markdownByteRange {
	ranges := markdownFenceRanges(string(source))
	result := make([]markdownByteRange, len(ranges))
	for i, span := range ranges {
		result[i] = markdownByteRange{start: span[0], end: span[1]}
	}
	return result
}

func markdownInlineCodeRange(source []byte, start, end int) markdownByteRange {
	opening := start
	for opening > 0 && source[opening-1] != '\n' && source[opening-1] != '\r' {
		opening--
		if source[opening] == '`' {
			for opening > 0 && source[opening-1] == '`' {
				opening--
			}
			break
		}
	}
	closing := end
	for closing < len(source) && source[closing] != '\n' && source[closing] != '\r' {
		if source[closing] == '`' {
			for closing < len(source) && source[closing] == '`' {
				closing++
			}
			break
		}
		closing++
	}
	return markdownByteRange{start: opening, end: closing}
}

func markdownSpanOverlaps(start, end int, ranges []markdownByteRange) bool {
	for _, value := range ranges {
		if start < value.end && value.start < end {
			return true
		}
	}
	return false
}

func markdownVitePressExpressionRanges(source []byte, immutable []markdownByteRange) []markdownByteRange {
	var ranges []markdownByteRange
	for cursor := 0; cursor+1 < len(source); {
		start := bytes.Index(source[cursor:], []byte("{{"))
		if start < 0 {
			break
		}
		start += cursor
		if containing := markdownContainingRange(start, immutable); containing != nil {
			cursor = containing.end
			continue
		}
		quote := byte(0)
		end := start + 2
		closed := false
		for end+1 < len(source) {
			if quote != 0 {
				if source[end] == '\\' {
					end += 2
					continue
				}
				if source[end] == quote {
					quote = 0
				}
				end++
				continue
			}
			if source[end] == '\'' || source[end] == '"' || source[end] == '`' {
				quote = source[end]
				end++
				continue
			}
			if source[end] == '}' && source[end+1] == '}' {
				end += 2
				closed = true
				break
			}
			end++
		}
		if !closed || end > len(source) {
			end = len(source)
		}
		ranges = append(ranges, markdownByteRange{start: start, end: end})
		cursor = end
	}
	return ranges
}

func markdownContainingRange(position int, ranges []markdownByteRange) *markdownByteRange {
	for i := range ranges {
		if position >= ranges[i].start && position < ranges[i].end {
			return &ranges[i]
		}
	}
	return nil
}

func subtractMarkdownRanges(fields []TextField, ranges []markdownByteRange) []TextField {
	result := make([]TextField, 0, len(fields))
	for _, field := range fields {
		cursor := field.Start
		for _, value := range ranges {
			if value.end <= cursor || value.start >= field.End {
				continue
			}
			if value.start > cursor {
				part := field
				part.Start, part.End = cursor, value.start
				part.Source = string([]byte(field.Source)[part.Start-field.Start : part.End-field.Start])
				result = append(result, part)
			}
			if value.end > cursor {
				cursor = value.end
			}
		}
		if cursor < field.End {
			part := field
			part.Start, part.End = cursor, field.End
			part.Source = string([]byte(field.Source)[part.Start-field.Start : part.End-field.Start])
			result = append(result, part)
		}
	}
	return result
}

func markdownReferenceRenders(document ast.Node, source []byte, fields []TextField) []referenceRender {
	var references []referenceRender
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || (node.Kind() != ast.KindLink && node.Kind() != ast.KindImage) {
			return ast.WalkContinue, nil
		}
		textStart, textEnd, ok := markdownNodeTextBounds(node)
		if !ok {
			return ast.WalkContinue, nil
		}
		opening := markdownOpeningBracket(source, textStart)
		closing := markdownClosingBracket(source, textEnd)
		if opening < 0 || closing < 0 {
			return ast.WalkContinue, nil
		}
		suffixEnd := 0
		if bytes.HasPrefix(source[closing:], []byte("][]")) {
			suffixEnd = closing + 3
		} else if closing+1 == len(source) || (source[closing+1] != '(' && source[closing+1] != '[') {
			suffixEnd = closing + 1
		}
		if suffixEnd == 0 {
			return ast.WalkContinue, nil
		}
		reference := referenceRender{
			labelStart:  opening + 1,
			labelEnd:    closing,
			suffixStart: closing,
			suffixEnd:   suffixEnd,
			rawLabel:    string(source[opening+1 : closing]),
		}
		for _, field := range fields {
			if field.Start >= reference.labelStart && field.End <= reference.labelEnd {
				reference.fieldIDs = append(reference.fieldIDs, field.ID)
			}
		}
		if len(reference.fieldIDs) > 0 {
			references = append(references, reference)
		}
		return ast.WalkContinue, nil
	})
	return references
}

func markdownNodeTextBounds(node ast.Node) (int, int, bool) {
	start, end := -1, -1
	_ = ast.Walk(node, func(child ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || child.Kind() != ast.KindText {
			return ast.WalkContinue, nil
		}
		segment := child.(*ast.Text).Segment
		if start < 0 || segment.Start < start {
			start = segment.Start
		}
		if segment.Stop > end {
			end = segment.Stop
		}
		return ast.WalkContinue, nil
	})
	return start, end, start >= 0 && end >= start
}

func markdownOpeningBracket(source []byte, before int) int {
	for index := before - 1; index >= 0 && source[index] != '\n' && source[index] != '\r'; index-- {
		if source[index] == '[' && !markdownSourceByteEscaped(source, index) {
			return index
		}
	}
	return -1
}

func markdownClosingBracket(source []byte, after int) int {
	for index := after; index < len(source) && source[index] != '\n' && source[index] != '\r'; index++ {
		if source[index] == ']' && !markdownSourceByteEscaped(source, index) {
			return index
		}
	}
	return -1
}

func markdownSourceByteEscaped(source []byte, index int) bool {
	backslashes := 0
	for index > 0 && source[index-1] == '\\' {
		backslashes++
		index--
	}
	return backslashes%2 == 1
}

func normalizeVitePressContainerFields(fields []TextField, source []byte) []TextField {
	result := fields[:0]
	for _, field := range fields {
		lineStart := bytes.LastIndexByte(source[:field.Start], '\n') + 1
		lineEnd := bytes.IndexByte(source[field.Start:], '\n')
		if lineEnd < 0 {
			lineEnd = len(source)
		} else {
			lineEnd += field.Start
		}
		line := string(source[lineStart:lineEnd])
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, ":::") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, ":::"))
			parts := strings.SplitN(rest, " ", 2)
			if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
				continue
			}
			title := strings.TrimSpace(parts[1])
			titleStart := lineStart + strings.Index(line, title)
			if field.End <= titleStart {
				continue
			}
			if field.Start < titleStart {
				field.Start = titleStart
				field.Source = string(source[field.Start:field.End])
			}
			field.Context = fmt.Sprintf("vitepress:%d:container-title", lineStart)
		}
		result = append(result, field)
	}
	return result
}

func appendMarkdownTextField(fields []TextField, source []byte, start, end int, kind FieldKind, context string) []TextField {
	value := string(source[start:end])
	if start == 0 || source[start-1] == '\n' {
		trimmed := strings.TrimSpace(value)
		if strings.HasPrefix(trimmed, ":::") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, ":::"))
			if rest == "" {
				return fields
			}
			parts := strings.SplitN(rest, " ", 2)
			if len(parts) == 1 {
				return fields
			}
			title := strings.TrimSpace(parts[1])
			if title == "" {
				return fields
			}
			titleStart := start + strings.Index(value, title)
			return append(fields, TextField{Source: title, Kind: kind, Context: context + ":container-title", Start: titleStart, End: titleStart + len(title)})
		}
	}
	return append(fields, TextField{Source: value, Kind: kind, Context: context, Start: start, End: end})
}

func markdownHTMLTextFields(source []byte, groupOffset int, immutable []markdownByteRange) []TextField {
	var fields []TextField
	var stack []string
	cursor := 0
	group := groupOffset
	for cursor < len(source) {
		open := bytes.IndexByte(source[cursor:], '<')
		if open < 0 {
			break
		}
		open += cursor
		if markdownSpanOverlaps(open, open+1, immutable) {
			for _, value := range immutable {
				if open >= value.start && open < value.end {
					cursor = value.end
					break
				}
			}
			continue
		}
		close := markdownHTMLTagEnd(source, open+1)
		if close < 0 {
			break
		}
		tag, closing, selfClosing := markdownHTMLTag(source[open+1 : close])
		if tag == "" {
			cursor = close + 1
			continue
		}
		if closing {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		} else if !selfClosing && !markdownHTMLVoidTag(tag) {
			stack = append(stack, tag)
			if len(stack) == 1 {
				group++
			}
			if markdownHTMLRawTextTag(tag) {
				rawEnd := markdownHTMLRawTextEnd(source, close+1, tag)
				if rawEnd < 0 {
					break
				}
				stack = stack[:len(stack)-1]
				cursor = rawEnd
				continue
			}
		}
		textStart := close + 1
		nextOpen := bytes.IndexByte(source[textStart:], '<')
		if nextOpen < 0 {
			nextOpen = len(source)
		} else {
			nextOpen += textStart
		}
		if len(stack) > 0 && !markdownHTMLRawTextTag(stack[len(stack)-1]) {
			left, right := textStart, nextOpen
			for left < right && (source[left] == ' ' || source[left] == '\t' || source[left] == '\r' || source[left] == '\n') {
				left++
			}
			for right > left && (source[right-1] == ' ' || source[right-1] == '\t' || source[right-1] == '\r' || source[right-1] == '\n') {
				right--
			}
			if left < right {
				fields = append(fields, TextField{Source: string(source[left:right]), Kind: FieldHTMLText, Context: fmt.Sprintf("html:%d:%s", group, stack[len(stack)-1]), Start: left, End: right})
			}
		}
		cursor = close + 1
	}
	return fields
}

func markdownHTMLTagEnd(source []byte, start int) int {
	var quote byte
	for i := start; i < len(source); i++ {
		if quote != 0 {
			if source[i] == quote && (i == 0 || source[i-1] != '\\') {
				quote = 0
			}
			continue
		}
		if source[i] == '\'' || source[i] == '"' {
			quote = source[i]
			continue
		}
		if source[i] == '>' {
			return i
		}
	}
	return -1
}

func markdownHTMLTag(raw []byte) (string, bool, bool) {
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.HasPrefix(value, "!") || strings.HasPrefix(value, "?") {
		return "", false, false
	}
	closing := strings.HasPrefix(value, "/")
	if closing {
		value = strings.TrimSpace(strings.TrimPrefix(value, "/"))
	}
	end := 0
	for end < len(value) && ((value[end] >= 'A' && value[end] <= 'Z') || (value[end] >= 'a' && value[end] <= 'z') || (end > 0 && (value[end] == '-' || (value[end] >= '0' && value[end] <= '9')))) {
		end++
	}
	if end == 0 {
		return "", false, false
	}
	return strings.ToLower(value[:end]), closing, strings.HasSuffix(strings.TrimSpace(value), "/")
}

func markdownHTMLVoidTag(tag string) bool {
	switch tag {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}

func markdownHTMLRawTextTag(tag string) bool {
	return tag == "script" || tag == "style" || tag == "pre" || tag == "code"
}

func markdownHTMLRawTextEnd(source []byte, start int, tag string) int {
	lower := bytes.ToLower(source[start:])
	needle := []byte("</" + tag)
	cursor := 0
	for cursor < len(lower) {
		closing := bytes.Index(lower[cursor:], needle)
		if closing < 0 {
			return -1
		}
		closing += cursor
		after := closing + len(needle)
		if after < len(lower) && lower[after] != '>' && lower[after] != ' ' && lower[after] != '\t' && lower[after] != '\r' && lower[after] != '\n' {
			cursor = after
			continue
		}
		end := markdownHTMLTagEnd(source, start+after)
		if end < 0 {
			return -1
		}
		return end + 1
	}
	return -1
}

func markdownHTMLRawTextRanges(source []byte, immutable []markdownByteRange) []markdownByteRange {
	var ranges []markdownByteRange
	for cursor := 0; cursor < len(source); {
		open := bytes.IndexByte(source[cursor:], '<')
		if open < 0 {
			break
		}
		open += cursor
		if containing := markdownContainingRange(open, immutable); containing != nil {
			cursor = containing.end
			continue
		}
		close := markdownHTMLTagEnd(source, open+1)
		if close < 0 {
			break
		}
		tag, closing, selfClosing := markdownHTMLTag(source[open+1 : close])
		if tag == "" || closing || selfClosing || !markdownHTMLRawTextTag(tag) {
			cursor = close + 1
			continue
		}
		end := markdownHTMLRawTextEnd(source, close+1, tag)
		if end < 0 {
			end = len(source)
		}
		ranges = append(ranges, markdownByteRange{start: open, end: end})
		cursor = end
	}
	return ranges
}

func markdownTextIsImmutable(node *ast.Text) bool {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Kind() == ast.KindCodeSpan || parent.Kind() == ast.KindRawHTML || parent.Kind() == ast.KindAutoLink {
			return true
		}
	}
	return false
}

func markdownTextOwner(node ast.Node) ast.Node {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Kind() {
		case ast.KindParagraph, ast.KindHeading:
			return parent
		}
		if parent.Type() == ast.TypeBlock {
			return parent
		}
	}
	return node
}

func (p *MarkdownPlan) Fields() []TextField {
	return append([]TextField(nil), p.fields...)
}

func (p *MarkdownPlan) ProviderFields() []ProviderField {
	fields := make([]ProviderField, len(p.fields))
	for i, field := range p.fields {
		fields[i] = ProviderField{ID: field.ID, Source: field.Source, Kind: field.Kind, Context: field.Context}
	}
	return fields
}

func (p *MarkdownPlan) OriginalPatches() map[FieldID]string {
	patches := make(map[FieldID]string, len(p.fields))
	for _, field := range p.fields {
		patches[field.ID] = field.Source
	}
	return patches
}

func (p *MarkdownPlan) ValidateFieldPatch(id FieldID, patch string) error {
	patches := p.OriginalPatches()
	if _, ok := patches[id]; !ok {
		return fmt.Errorf("unknown Markdown field %q", id)
	}
	patches[id] = patch
	_, err := p.RenderExact(patches)
	return err
}

func (p *MarkdownPlan) RenderExact(patches map[FieldID]string) ([]byte, error) {
	if len(patches) != len(p.fields) {
		return nil, fmt.Errorf("got %d Markdown field patches, expected %d", len(patches), len(p.fields))
	}
	expected := make(map[FieldID]bool, len(p.fields))
	for _, field := range p.fields {
		expected[field.ID] = true
	}
	for id := range patches {
		if !expected[id] {
			return nil, fmt.Errorf("unknown Markdown field %q", id)
		}
	}
	type renderEdit struct {
		start int
		end   int
		value string
	}
	var edits []renderEdit
	changed := make(map[FieldID]bool)
	for _, field := range p.fields {
		patch, ok := patches[field.ID]
		if !ok {
			return nil, fmt.Errorf("missing Markdown field %q", field.ID)
		}
		value := patch
		if patch == field.Source {
			value = string(p.source[field.Start:field.End])
		} else {
			escaped, err := escapeMarkdownField(patch, field, p.source)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", field.ID, err)
			}
			value = escaped
			changed[field.ID] = true
		}
		edits = append(edits, renderEdit{start: field.Start, end: field.End, value: value})
	}
	for _, reference := range p.references {
		referenceChanged := false
		for _, id := range reference.fieldIDs {
			referenceChanged = referenceChanged || changed[id]
		}
		if referenceChanged {
			edits = append(edits, renderEdit{start: reference.suffixStart, end: reference.suffixEnd, value: "][" + reference.rawLabel + "]"})
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var rendered bytes.Buffer
	cursor := 0
	for _, edit := range edits {
		if edit.start < cursor {
			return nil, fmt.Errorf("overlapping Markdown render edits at byte %d", edit.start)
		}
		rendered.Write(p.source[cursor:edit.start])
		rendered.WriteString(edit.value)
		cursor = edit.end
	}
	rendered.Write(p.source[cursor:])
	result := rendered.Bytes()
	document := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(result))
	if fingerprint := markdownStructureFingerprint(document, result); fingerprint != p.fingerprint {
		return nil, fmt.Errorf("Markdown immutable structure changed after rendering")
	}
	return append([]byte(nil), result...), nil
}

func escapeMarkdownField(value string, field TextField, source []byte) (string, error) {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("patch is not valid UTF-8 text")
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("patch contains a line break")
	}
	if strings.Contains(value, "{{") || strings.Contains(value, "}}") || strings.Contains(value, ":::") {
		return "", fmt.Errorf("patch contains VitePress template or container syntax")
	}
	if providerAuthoredURL.MatchString(value) {
		return "", fmt.Errorf("patch contains a provider-authored URL")
	}
	if field.Kind == FieldHTMLText {
		return html.EscapeString(value), nil
	}
	replacer := strings.NewReplacer(
		`\`, `\\`,
		"`", "\\`",
		"*", "\\*",
		"_", "\\_",
		"[", "\\[",
		"]", "\\]",
		"<", "\\<",
		">", "\\>",
		"|", "\\|",
	)
	escaped := replacer.Replace(value)
	if field.Start == 0 || source[field.Start-1] == '\n' {
		trimmed := strings.TrimLeft(escaped, " \t")
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ">") ||
			strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "=") {
			prefixLen := len(escaped) - len(trimmed)
			escaped = escaped[:prefixLen] + `\` + escaped[prefixLen:]
		}
	}
	return escaped, nil
}

func markdownStructureFingerprint(document ast.Node, source []byte) string {
	var parts []string
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || node.Kind() == ast.KindText || node.Kind() == ast.KindString {
			return ast.WalkContinue, nil
		}
		part := node.Kind().String()
		switch value := node.(type) {
		case *ast.Heading:
			part += fmt.Sprintf(":%d", value.Level)
		case *ast.Link:
			part += fmt.Sprintf(":%x:%x", value.Destination, value.Title)
		case *ast.Image:
			part += fmt.Sprintf(":%x:%x", value.Destination, value.Title)
		case *ast.AutoLink:
			part += fmt.Sprintf(":%x:%x", value.URL(source), value.Label(source))
		case *ast.Emphasis:
			part += fmt.Sprintf(":%d", value.Level)
		}
		parts = append(parts, part)
		return ast.WalkContinue, nil
	})
	return strings.Join(parts, "\x00")
}
