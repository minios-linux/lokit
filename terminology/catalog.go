// Package terminology loads and matches format-independent terminology rules.
package terminology

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MatchMode controls how a source term is found in text.
type MatchMode string

// ValidationMode controls whether a translated term is enforced literally or
// supplied to the provider as grammatical guidance only.
type ValidationMode string

const (
	MatchWord        MatchMode      = "word"
	MatchSubstring   MatchMode      = "substring"
	ValidationStrict ValidationMode = "strict"
	ValidationPrompt ValidationMode = "prompt"
)

// Selector describes the translation item against which a rule's when block is matched.
type Selector struct {
	Target  string
	Format  string
	Path    string
	Key     string
	Context string
}

// ExactMatch is a resolved exact-string rule. Translations is always a copy.
type ExactMatch struct {
	ID           string
	Source       string
	SourcePlural string
	Locale       string
	Translations []string
}

// TermMatch is a resolved term rule. Accepted is always a copy.
type TermMatch struct {
	ID            string
	Source        string
	Locale        string
	Match         MatchMode
	CaseSensitive bool
	Preserve      bool
	Validation    ValidationMode
	Preferred     string
	Accepted      []string
	order         int
}

// TermSpan identifies a matched term occurrence using rune offsets.
type TermSpan struct {
	Start int
	End   int
}

// MatchesSource reports whether the rule's source term occurs in text.
func (m TermMatch) MatchesSource(text string) bool {
	return containsTerm(text, m.Source, m.Match, m.CaseSensitive)
}

// SourceSpans returns the non-overlapping occurrences of the source term.
func (m TermMatch) SourceSpans(text string) []TermSpan {
	spans := termSpans(text, m.Source, m.Match, m.CaseSensitive)
	result := make([]TermSpan, len(spans))
	for i, span := range spans {
		result[i] = TermSpan{Start: span.start, End: span.end}
	}
	return result
}

// RequiredOccurrences returns the number of source term occurrences.
func (m TermMatch) RequiredOccurrences(source string) int {
	return countTerm(source, m.Source, m.Match, m.CaseSensitive)
}

// AcceptedOccurrences returns the number of approved target term occurrences.
func (m TermMatch) AcceptedOccurrences(target string) int {
	if m.Preserve {
		return countTerm(target, m.Source, m.Match, m.CaseSensitive)
	}
	return countAnyTerm(target, append([]string{m.Preferred}, m.Accepted...), m.Match, m.CaseSensitive)
}

// Accepts reports whether candidate is preferred, accepted, or preserved by the rule.
func (m TermMatch) Accepts(candidate string) bool {
	if m.Preserve {
		return candidate == m.Source
	}
	if candidate == m.Preferred {
		return true
	}
	for _, accepted := range m.Accepted {
		if candidate == accepted {
			return true
		}
	}
	return false
}

// ValidTranslation reports whether target contains enough approved term
// occurrences for the occurrences present in source.
func (m TermMatch) ValidTranslation(source, target string) bool {
	if m.Validation == ValidationPrompt {
		return true
	}
	required := countTerm(source, m.Source, m.Match, m.CaseSensitive)
	if required == 0 {
		return true
	}
	if m.Preserve {
		return countTerm(target, m.Source, m.Match, m.CaseSensitive) >= required
	}
	accepted := append([]string{m.Preferred}, m.Accepted...)
	found := countAnyTerm(target, accepted, m.Match, m.CaseSensitive)
	return found >= required
}

// Expected returns the approved target forms for diagnostics.
func (m TermMatch) Expected() []string {
	if m.Preserve {
		return []string{m.Source}
	}
	return append([]string{m.Preferred}, m.Accepted...)
}

type localizedExact struct {
	locale       string
	translations []string
}

type localizedTerm struct {
	locale    string
	preferred string
	accepted  []string
}

type selectedTerm struct {
	rule        *termRule
	translation *localizedTerm
	localeRank  int
}

type condition struct {
	patterns [5][]*regexp.Regexp
	fields   int
	literals int
}

type exactRule struct {
	id           string
	source       string
	sourcePlural string
	when         condition
	translations []localizedExact
	order        int
}

type termRule struct {
	id            string
	source        string
	match         MatchMode
	caseSensitive bool
	when          condition
	preserve      bool
	validation    ValidationMode
	translations  []localizedTerm
	order         int
}

// Catalog is an immutable collection of validated terminology rules.
// Its contents can only be constructed by Load or LoadFiles.
type Catalog struct {
	exact []exactRule
	terms []termRule
}

// Len returns the total number of exact and term rules.
func (c *Catalog) Len() int {
	if c == nil {
		return 0
	}
	return len(c.exact) + len(c.terms)
}

// Exact resolves an exact-string rule for source, plural form, locale, and selector.
// More selective when blocks win; declaration order breaks otherwise equal conflicts.
func (c *Catalog) Exact(source, sourcePlural, locale string, selector Selector) (ExactMatch, bool) {
	return c.MatchExact(source, sourcePlural, locale, selector)
}

// MatchExact resolves an exact-string rule.
func (c *Catalog) MatchExact(source, sourcePlural, locale string, selector Selector) (ExactMatch, bool) {
	match, ok, _ := c.ResolveExact(source, sourcePlural, locale, selector)
	return match, ok
}

// ResolveExact resolves an exact rule and rejects equally specific conflicts.
func (c *Catalog) ResolveExact(source, sourcePlural, locale string, selector Selector) (ExactMatch, bool, error) {
	if c == nil {
		return ExactMatch{}, false, nil
	}
	locales, err := LocaleFallbacks(locale)
	if err != nil {
		return ExactMatch{}, false, err
	}

	var best *exactRule
	var bestTranslation *localizedExact
	bestLocaleRank := len(locales)
	for i := range c.exact {
		rule := &c.exact[i]
		if rule.source != source || rule.sourcePlural != sourcePlural || !rule.when.matches(selector) {
			continue
		}
		translation, localeRank := findExactLocale(rule.translations, locales)
		if translation == nil {
			continue
		}
		if best != nil && rule.when.sameSpecificity(best.when) && localeRank == bestLocaleRank && !stringSlicesEqual(translation.translations, bestTranslation.translations) {
			return ExactMatch{}, false, fmt.Errorf("conflicting exact terminology rules %q and %q for %q (%s)", best.id, rule.id, source, locale)
		}
		if best == nil || rule.when.moreSpecific(best.when) || (rule.when.sameSpecificity(best.when) && localeRank < bestLocaleRank) {
			best = rule
			bestTranslation = translation
			bestLocaleRank = localeRank
		}
	}
	if best == nil {
		return ExactMatch{}, false, nil
	}
	return ExactMatch{
		ID:           best.id,
		Source:       best.source,
		SourcePlural: best.sourcePlural,
		Locale:       bestTranslation.locale,
		Translations: append([]string(nil), bestTranslation.translations...),
	}, true, nil
}

// Terms resolves applicable term rules for a locale and selector. Conflicting
// source terms use the same specificity and declaration-order rules as Exact.
func (c *Catalog) Terms(locale string, selector Selector) []TermMatch {
	terms, _ := c.ResolveTerms(locale, selector)
	return terms
}

// ResolveTerms resolves applicable term rules and rejects equally specific conflicts.
func (c *Catalog) ResolveTerms(locale string, selector Selector) ([]TermMatch, error) {
	if c == nil {
		return nil, nil
	}
	locales, err := LocaleFallbacks(locale)
	if err != nil {
		return nil, err
	}

	chosen := make(map[string]selectedTerm)
	for i := range c.terms {
		rule := &c.terms[i]
		if !rule.when.matches(selector) {
			continue
		}
		var translation *localizedTerm
		localeRank := 0
		if !rule.preserve {
			translation, localeRank = findTermLocale(rule.translations, locales)
			if translation == nil {
				continue
			}
		}
		key := string(rule.match) + "\x00" + fmt.Sprint(rule.caseSensitive) + "\x00" + rule.source
		if !rule.caseSensitive {
			key = string(rule.match) + "\x00false\x00" + strings.ToLower(rule.source)
		}
		current, exists := chosen[key]
		if exists && rule.when.sameSpecificity(current.rule.when) && localeRank == current.localeRank && termRulesConflict(current, rule, translation) {
			return nil, fmt.Errorf("conflicting terminology rules %q and %q for term %q (%s)", current.rule.id, rule.id, rule.source, locale)
		}
		if !exists || rule.when.moreSpecific(current.rule.when) || (rule.when.sameSpecificity(current.rule.when) && localeRank < current.localeRank) {
			chosen[key] = selectedTerm{rule: rule, translation: translation, localeRank: localeRank}
		}
	}

	out := make([]TermMatch, 0, len(chosen))
	requested, _ := NormalizeLocale(locale)
	for _, item := range chosen {
		match := TermMatch{
			ID:            item.rule.id,
			Source:        item.rule.source,
			Locale:        requested,
			Match:         item.rule.match,
			CaseSensitive: item.rule.caseSensitive,
			Preserve:      item.rule.preserve,
			Validation:    item.rule.validation,
			order:         item.rule.order,
		}
		if item.translation != nil {
			match.Locale = item.translation.locale
			match.Preferred = item.translation.preferred
			match.Accepted = append([]string(nil), item.translation.accepted...)
		} else {
			match.Preferred = item.rule.source
		}
		out = append(out, match)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].order < out[j].order })
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if termMatchesOverlap(out[i], out[j]) && termPoliciesConflict(out[i], out[j]) {
				return nil, fmt.Errorf("conflicting terminology rules %q and %q for overlapping term %q (%s)", out[i].ID, out[j].ID, out[i].Source, locale)
			}
		}
	}
	return out, nil
}

func termMatchesOverlap(a, b TermMatch) bool {
	if !strings.EqualFold(a.Source, b.Source) {
		return false
	}
	if a.CaseSensitive && b.CaseSensitive && a.Source != b.Source {
		return false
	}
	return true
}

func termPoliciesConflict(a, b TermMatch) bool {
	if a.Preserve != b.Preserve || a.Validation != b.Validation {
		return true
	}
	if a.Preserve {
		return a.Source != b.Source
	}
	return a.Preferred != b.Preferred || !sameStringSet(a.Accepted, b.Accepted)
}

// MatchTerms returns applicable rules whose source term occurs in text.
func (c *Catalog) MatchTerms(text, locale string, selector Selector) []TermMatch {
	rules, _ := c.ResolveTerms(locale, selector)
	out := rules[:0]
	for _, rule := range rules {
		if rule.MatchesSource(text) {
			out = append(out, rule)
		}
	}
	return out
}

func termRulesConflict(current selectedTerm, next *termRule, translation *localizedTerm) bool {
	if current.rule.preserve != next.preserve || current.rule.validation != next.validation {
		return true
	}
	if next.preserve {
		return false
	}
	return current.translation.preferred != translation.preferred || !sameStringSet(current.translation.accepted, translation.accepted)
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa, bb := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	return stringSlicesEqual(aa, bb)
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func findExactLocale(values []localizedExact, fallbacks []string) (*localizedExact, int) {
	for rank, locale := range fallbacks {
		for i := range values {
			if values[i].locale == locale {
				return &values[i], rank
			}
		}
	}
	return nil, len(fallbacks)
}

func findTermLocale(values []localizedTerm, fallbacks []string) (*localizedTerm, int) {
	for rank, locale := range fallbacks {
		for i := range values {
			if values[i].locale == locale {
				return &values[i], rank
			}
		}
	}
	return nil, len(fallbacks)
}

func (c condition) matches(selector Selector) bool {
	values := [5]string{selector.Target, selector.Format, selector.Path, selector.Key, selector.Context}
	values[2] = strings.ReplaceAll(values[2], "\\", "/")
	for i, patterns := range c.patterns {
		if len(patterns) == 0 {
			continue
		}
		matched := false
		for _, pattern := range patterns {
			if pattern.MatchString(values[i]) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func (c condition) moreSpecific(other condition) bool {
	return c.fields > other.fields || (c.fields == other.fields && c.literals > other.literals)
}

func (c condition) sameSpecificity(other condition) bool {
	return c.fields == other.fields && c.literals == other.literals
}

func compilePattern(pattern string) (*regexp.Regexp, int, error) {
	if pattern == "" {
		return nil, 0, fmt.Errorf("selector pattern must not be empty")
	}
	pattern = strings.ReplaceAll(pattern, "\\", "/")
	var b strings.Builder
	b.WriteString("^")
	literals := 0
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 3
				} else {
					b.WriteString(".*")
					i += 2
				}
			} else {
				b.WriteString("[^/]*")
				i++
			}
		case '?':
			b.WriteString("[^/]")
			i++
		default:
			r, size := utf8.DecodeRuneInString(pattern[i:])
			b.WriteString(regexp.QuoteMeta(string(r)))
			literals++
			i += size
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	return re, literals, err
}

// NormalizeLocale canonicalizes common BCP 47 and underscore locale forms.
func NormalizeLocale(locale string) (string, error) {
	locale = strings.TrimSpace(strings.ReplaceAll(locale, "_", "-"))
	if locale == "" {
		return "", fmt.Errorf("locale must not be empty")
	}
	parts := strings.Split(locale, "-")
	if len(parts[0]) < 2 || len(parts[0]) > 8 || !asciiLetters(parts[0]) {
		return "", fmt.Errorf("invalid locale %q", locale)
	}
	parts[0] = strings.ToLower(parts[0])
	for i := 1; i < len(parts); i++ {
		part := parts[i]
		if len(part) < 1 || len(part) > 8 || !asciiAlnum(part) {
			return "", fmt.Errorf("invalid locale %q", locale)
		}
		switch {
		case len(part) == 4 && asciiLetters(part):
			parts[i] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
		case len(part) == 2 && asciiLetters(part), len(part) == 3 && asciiDigits(part):
			parts[i] = strings.ToUpper(part)
		default:
			parts[i] = strings.ToLower(part)
		}
	}
	return strings.Join(parts, "-"), nil
}

// LocaleFallbacks returns a locale from most to least specific.
func LocaleFallbacks(locale string) ([]string, error) {
	normalized, err := NormalizeLocale(locale)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(normalized, "-")
	out := make([]string, 0, len(parts))
	for len(parts) > 0 {
		out = append(out, strings.Join(parts, "-"))
		parts = parts[:len(parts)-1]
	}
	return out, nil
}

func localeFallbacks(locale string) []string {
	locales, _ := LocaleFallbacks(locale)
	return locales
}

func asciiLetters(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func asciiDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func asciiAlnum(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func containsTerm(text, term string, mode MatchMode, caseSensitive bool) bool {
	return countTerm(text, term, mode, caseSensitive) > 0
}

func countTerm(text, term string, mode MatchMode, caseSensitive bool) int {
	return len(termSpans(text, term, mode, caseSensitive))
}

type textSpan struct{ start, end int }

func termSpans(text, term string, mode MatchMode, caseSensitive bool) []textSpan {
	if term == "" {
		return nil
	}
	textRunes, termRunes := []rune(text), []rune(term)
	if len(termRunes) > len(textRunes) {
		return nil
	}
	var spans []textSpan
	for i := 0; i+len(termRunes) <= len(textRunes); i++ {
		candidate := string(textRunes[i : i+len(termRunes)])
		matches := candidate == term
		if !caseSensitive {
			matches = strings.EqualFold(candidate, term)
		}
		if !matches {
			continue
		}
		if mode == MatchSubstring || termWordBoundaries(textRunes, termRunes, i) {
			spans = append(spans, textSpan{i, i + len(termRunes)})
			i += len(termRunes) - 1
		}
	}
	return spans
}

func countAnyTerm(text string, terms []string, mode MatchMode, caseSensitive bool) int {
	seenTerms := make(map[string]bool)
	var spans []textSpan
	for _, term := range terms {
		key := term
		if !caseSensitive {
			key = strings.ToLower(term)
		}
		if term == "" || seenTerms[key] {
			continue
		}
		seenTerms[key] = true
		spans = append(spans, termSpans(text, term, mode, caseSensitive)...)
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	count, end := 0, -1
	for _, span := range spans {
		if span.start < end {
			continue
		}
		count++
		end = span.end
	}
	return count
}

func termWordBoundaries(text, term []rune, start int) bool {
	if isWordRune(term[0]) && start > 0 && isWordRune(text[start-1]) {
		return false
	}
	end := start + len(term)
	return !isWordRune(term[len(term)-1]) || end == len(text) || !isWordRune(text[end])
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
