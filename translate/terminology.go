package translate

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/minios-linux/lokit/terminology"
)

var preservedPlaceholderPattern = regexp.MustCompile(`__LOKIT_PRESERVE_TERM_[0-9a-f]+_[0-9]+_[0-9]+__`)

type terminologyPromptTerm struct {
	ID            string   `json:"id"`
	Source        string   `json:"source,omitempty"`
	Rule          string   `json:"rule"`
	Preferred     string   `json:"preferred,omitempty"`
	Accepted      []string `json:"accepted,omitempty"`
	Validation    string   `json:"validation,omitempty"`
	protectedRule *terminology.TermMatch
	compoundRule  *terminology.TermMatch
}

type preserveCardinalityError struct {
	ruleID      string
	expected    []string
	required    int
	found       int
	translation string
}

func (e *preserveCardinalityError) Error() string {
	return fmt.Sprintf("terminology rule %q: preserve %q, expected %d occurrence(s), found %d; translation: %q",
		e.ruleID, strings.Join(e.expected, " | "), e.required, e.found, terminologyExcerpt(e.translation))
}

func (e *preserveCardinalityError) retryConversation() (string, string) {
	return "[Rejected response omitted because it exposed protected terminology outside opaque placeholders.]",
		fmt.Sprintf("Protected terminology cardinality mismatch: expected %d occurrence(s) after placeholder restoration, found %d. Reproduce each __LOKIT_PRESERVE_TERM_ token in every output string corresponding to an input string that contains it, including requested plural forms. In Markdown, each preserve token is line-scoped: never copy or move it to a different line. Do not infer or add hidden terms outside those tokens. Apply a protected compound template only to its exact preserve token and complete source phrase; never expand a shorter or generic phrase into the compound.", e.required, e.found)
}

func preservedTermValues(groups ...map[string]string) []string {
	unique := make(map[string]bool)
	for _, group := range groups {
		for _, value := range group {
			if value != "" {
				unique[value] = true
			}
		}
	}
	values := make([]string, 0, len(unique))
	for value := range unique {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

type terminologyPromptEntry struct {
	ID    string                  `json:"id"`
	Terms []terminologyPromptTerm `json:"terms"`
}

func terminologySelector(opts Options, key, context, path string) terminology.Selector {
	if path == "" {
		path = opts.SourcePath
	}
	return terminology.Selector{
		Target:  opts.TargetName,
		Format:  opts.Format,
		Path:    path,
		Key:     key,
		Context: context,
	}
}

func resolveTerminologyTerms(source string, opts Options, selector terminology.Selector) ([]terminology.TermMatch, error) {
	if opts.Terminology == nil {
		return nil, nil
	}
	rules, err := opts.Terminology.ResolveTerms(opts.Language, selector)
	if err != nil {
		return nil, err
	}
	matched := rules[:0]
	for _, rule := range rules {
		if rule.MatchesSource(source) {
			matched = append(matched, rule)
		}
	}
	return matched, nil
}

func validateTerminology(source, target string, rules []terminology.TermMatch) error {
	for _, rule := range rules {
		if !rule.ValidTranslation(source, target) {
			required := rule.RequiredOccurrences(source)
			found := rule.AcceptedOccurrences(target)
			if rule.Preserve {
				return &preserveCardinalityError{
					ruleID:      rule.ID,
					expected:    append([]string(nil), rule.Expected()...),
					required:    required,
					found:       found,
					translation: target,
				}
			}
			mode := "use one of"
			return fmt.Errorf("terminology rule %q: %s %q, expected %d occurrence(s), found %d; translation: %q",
				rule.ID, mode, strings.Join(rule.Expected(), " | "), required, found, terminologyExcerpt(target))
		}
	}
	return nil
}

type preservedTermSpan struct {
	start int
	end   int
}

func preservedTermNamespace(texts []string) string {
	seed := strings.Join(texts, "\x00")
	for attempt := 0; ; attempt++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", seed, attempt)))
		namespace := fmt.Sprintf("__LOKIT_PRESERVE_TERM_%x_", sum[:8])
		collision := false
		for _, text := range texts {
			if strings.Contains(text, namespace) {
				collision = true
				break
			}
		}
		if !collision {
			return namespace
		}
	}
}

func maskPreservedTerms(text string, rules []terminology.TermMatch, namespace string, scope int) (string, map[string]string) {
	var spans []preservedTermSpan
	for _, rule := range rules {
		if !rule.Preserve {
			continue
		}
		for _, span := range rule.SourceSpans(text) {
			spans = append(spans, preservedTermSpan{start: span.Start, end: span.End})
		}
	}
	if len(spans) == 0 {
		return text, nil
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	merged := spans[:0]
	for _, span := range spans {
		if len(merged) == 0 || span.start >= merged[len(merged)-1].end {
			merged = append(merged, span)
			continue
		}
		if span.end > merged[len(merged)-1].end {
			merged[len(merged)-1].end = span.end
		}
	}

	runes := []rune(text)
	values := make(map[string]string)
	var out strings.Builder
	end, tokenIndex := 0, 0
	for _, span := range merged {
		out.WriteString(string(runes[end:span.start]))
		token := fmt.Sprintf("%s%d_%d__", namespace, scope, tokenIndex)
		for strings.Contains(text, token) {
			tokenIndex++
			token = fmt.Sprintf("%s%d_%d__", namespace, scope, tokenIndex)
		}
		values[token] = string(runes[span.start:span.end])
		out.WriteString(token)
		tokenIndex++
		end = span.end
	}
	out.WriteString(string(runes[end:]))
	return out.String(), values
}

func replacePreservedTermWithTemplate(text string, preserve terminology.TermMatch) (string, bool) {
	spans := preserve.SourceSpans(text)
	if len(spans) == 0 {
		return text, false
	}
	runes := []rune(text)
	var out strings.Builder
	end := 0
	for _, span := range spans {
		out.WriteString(string(runes[end:span.Start]))
		out.WriteString("__LOKIT_PROTECTED_TERM__")
		end = span.End
	}
	out.WriteString(string(runes[end:]))
	return out.String(), true
}

func sanitizeProtectedCompoundTerm(item terminologyPromptTerm, rules []terminology.TermMatch) (terminologyPromptTerm, bool) {
	changed := false
	var sourcePreserves []terminology.TermMatch
	for _, preserve := range rules {
		if !preserve.Preserve {
			continue
		}
		var replaced bool
		item.Source, replaced = replacePreservedTermWithTemplate(item.Source, preserve)
		if replaced {
			sourcePreserves = append(sourcePreserves, preserve)
		}
		changed = changed || replaced
		item.Preferred, replaced = replacePreservedTermWithTemplate(item.Preferred, preserve)
		changed = changed || replaced
		for i := range item.Accepted {
			item.Accepted[i], replaced = replacePreservedTermWithTemplate(item.Accepted[i], preserve)
			changed = changed || replaced
		}
	}
	if len(sourcePreserves) == 1 && strings.Count(item.Source, "__LOKIT_PROTECTED_TERM__") == 1 &&
		strings.Count(item.Preferred, "__LOKIT_PROTECTED_TERM__") == 1 {
		valid := true
		for _, accepted := range item.Accepted {
			valid = valid && strings.Count(accepted, "__LOKIT_PROTECTED_TERM__") == 1
		}
		if valid {
			preserve := sourcePreserves[0]
			item.protectedRule = &preserve
		}
	}
	return item, changed
}

func bindProtectedCompoundTerms(entry terminologyPromptEntry, maskedSources []string, values ...map[string]string) terminologyPromptEntry {
	bound := terminologyPromptEntry{ID: entry.ID}
	tokens := make([]string, 0)
	for _, group := range values {
		for token := range group {
			tokens = append(tokens, token)
		}
	}
	sort.Strings(tokens)
	for _, item := range entry.Terms {
		hasSourceMarker := strings.Contains(item.Source, "__LOKIT_PROTECTED_TERM__")
		hasTargetMarker := strings.Contains(item.Preferred, "__LOKIT_PROTECTED_TERM__")
		for _, accepted := range item.Accepted {
			hasTargetMarker = hasTargetMarker || strings.Contains(accepted, "__LOKIT_PROTECTED_TERM__")
		}
		if !hasSourceMarker {
			if hasTargetMarker {
				continue
			}
			bound.Terms = append(bound.Terms, item)
			continue
		}
		if item.protectedRule == nil {
			continue
		}
		for _, token := range tokens {
			value := ""
			for _, group := range values {
				if candidate, ok := group[token]; ok {
					value = candidate
					break
				}
			}
			if (item.protectedRule.CaseSensitive && value != item.protectedRule.Source) ||
				(!item.protectedRule.CaseSensitive && !strings.EqualFold(value, item.protectedRule.Source)) {
				continue
			}
			source := strings.ReplaceAll(item.Source, "__LOKIT_PROTECTED_TERM__", token)
			matched := false
			for _, maskedSource := range maskedSources {
				if item.compoundRule != nil && protectedTemplateMatches(maskedSource, source, *item.compoundRule) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			occurrence := item
			occurrence.ID = fmt.Sprintf("%s-%d", item.ID, len(bound.Terms))
			occurrence.Source = source
			occurrence.Preferred = strings.ReplaceAll(item.Preferred, "__LOKIT_PROTECTED_TERM__", token)
			occurrence.Accepted = append([]string(nil), item.Accepted...)
			for i := range occurrence.Accepted {
				occurrence.Accepted[i] = strings.ReplaceAll(occurrence.Accepted[i], "__LOKIT_PROTECTED_TERM__", token)
			}
			bound.Terms = append(bound.Terms, occurrence)
		}
	}
	return bound
}

func protectedTemplateMatches(text, template string, rule terminology.TermMatch) bool {
	textRunes, templateRunes, sourceRunes := []rune(text), []rune(template), []rune(rule.Source)
	if len(templateRunes) == 0 || len(sourceRunes) == 0 {
		return false
	}
	for start := 0; start+len(templateRunes) <= len(textRunes); start++ {
		candidate := string(textRunes[start : start+len(templateRunes)])
		matched := candidate == template
		if !rule.CaseSensitive {
			matched = strings.EqualFold(candidate, template)
		}
		if !matched {
			continue
		}
		if rule.Match == terminology.MatchSubstring {
			return true
		}
		end := start + len(templateRunes)
		if isPreservedWordRune(sourceRunes[0]) && start > 0 && isPreservedWordRune(textRunes[start-1]) {
			continue
		}
		if isPreservedWordRune(sourceRunes[len(sourceRunes)-1]) && end < len(textRunes) && isPreservedWordRune(textRunes[end]) {
			continue
		}
		return true
	}
	return false
}

func restorePreservedTerms(text string, values map[string]string, namespace string) (string, error) {
	if stripUnexpectedPreservedLiterals(text, values) != text {
		return "", fmt.Errorf("response contains protected terminology outside its opaque placeholder")
	}
	for _, token := range preservedPlaceholderPattern.FindAllString(text, -1) {
		if _, ok := values[token]; !ok {
			return "", fmt.Errorf("response contains foreign protected terminology placeholder %q", token)
		}
	}
	tokens := make([]string, 0, len(values))
	for token := range values {
		if count := strings.Count(text, token); count != 1 {
			return "", fmt.Errorf("protected terminology placeholder %q appears %d time(s), expected exactly once", token, count)
		}
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	for _, token := range tokens {
		text = strings.ReplaceAll(text, token, values[token])
	}
	if strings.Contains(text, namespace) {
		return "", fmt.Errorf("unexpected preserved terminology placeholder in translation")
	}
	return text, nil
}

func validateRawPreservedTerms(text string, values map[string]string, rules []terminology.TermMatch) error {
	if strings.Contains(text, "__LOKIT_PROTECTED_TERM__") {
		return fmt.Errorf("response contains an internal protected compound marker")
	}
	withoutTokens := text
	for token := range values {
		withoutTokens = strings.ReplaceAll(withoutTokens, token, "")
	}
	if preservedPlaceholderPattern.MatchString(withoutTokens) || strings.Contains(withoutTokens, "__LOKIT_PRESERVE_TERM_") {
		return fmt.Errorf("response contains a foreign protected terminology placeholder")
	}
	for _, rule := range rules {
		if rule.Preserve && rule.MatchesSource(withoutTokens) {
			return fmt.Errorf("response contains protected terminology outside its opaque placeholder")
		}
	}
	return nil
}

func collectProtectedRules(groups [][]terminology.TermMatch) []terminology.TermMatch {
	var protected []terminology.TermMatch
	for _, rules := range groups {
		for _, rule := range rules {
			if rule.Preserve {
				protected = append(protected, rule)
			}
		}
	}
	return protected
}

func stripUnexpectedPreservedLiterals(text string, values map[string]string) string {
	literals := preservedTermValues(values)
	sort.SliceStable(literals, func(i, j int) bool {
		return len([]rune(literals[i])) > len([]rune(literals[j]))
	})
	for _, literal := range literals {
		text = stripStandalonePreservedLiteral(text, literal)
	}
	return text
}

func stripStandalonePreservedLiteral(text, literal string) string {
	textRunes, literalRunes := []rune(text), []rune(literal)
	if len(literalRunes) == 0 {
		return text
	}
	out := make([]rune, 0, len(textRunes))
	for i := 0; i < len(textRunes); {
		end := i + len(literalRunes)
		matches := end <= len(textRunes)
		for j := 0; matches && j < len(literalRunes); j++ {
			matches = textRunes[i+j] == literalRunes[j]
		}
		if matches &&
			(!isPreservedWordRune(literalRunes[0]) || i == 0 || !isPreservedWordRune(textRunes[i-1])) &&
			(!isPreservedWordRune(literalRunes[len(literalRunes)-1]) || end == len(textRunes) || !isPreservedWordRune(textRunes[end])) {
			if len(out) > 0 && isHorizontalSpace(out[len(out)-1]) {
				out = out[:len(out)-1]
			} else if end < len(textRunes) && isHorizontalSpace(textRunes[end]) {
				end++
			}
			i = end
			continue
		}
		out = append(out, textRunes[i])
		i++
	}
	return string(out)
}

func isPreservedWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func isHorizontalSpace(r rune) bool {
	return r == ' ' || r == '\t'
}

func terminologyExcerpt(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > 240 {
		return string(runes[:240]) + "..."
	}
	return text
}

func appendTerminologyPrompt(prompt string, entries []terminologyPromptEntry) string {
	filtered := entries[:0]
	for _, entry := range entries {
		if len(entry.Terms) > 0 {
			filtered = append(filtered, entry)
		}
	}
	if len(filtered) == 0 {
		return prompt
	}
	data, _ := json.Marshal(filtered)
	return prompt + "\n\nMANDATORY TERMINOLOGY FOR THIS REQUEST:\n" +
		"The JSON rules below are scoped by response ID. Apply each rule only to the object with the same ID. " +
		"Terms represented by tokens beginning with __LOKIT_PRESERVE_TERM_ are hidden. Reproduce each token in every output string corresponding to an input string that contains it; Lokit restores the hidden term after translation. " +
		"In Markdown, each preserve token is line-scoped: never copy or move it to a different line. " +
		"A protected compound template contains the exact __LOKIT_PRESERVE_TERM_ token from its matched input; keep that same token while applying the template's word order. " +
		"Never infer or add a hidden term outside its token. Apply a preferred compound template only where its complete source phrase matched the input; never expand a shorter or generic phrase into that compound. Prefer the preferred form; accepted forms are also valid. " +
		"When validation is prompt, adapt the preferred term's grammar, inflection, and word order naturally for the translated sentence.\n" + string(data)
}

func promptTerms(id string, rules []terminology.TermMatch) terminologyPromptEntry {
	entry := terminologyPromptEntry{ID: id}
	for _, rule := range rules {
		if rule.Preserve {
			continue
		}
		compoundRule := rule
		item := terminologyPromptTerm{ID: rule.ID, Source: rule.Source, compoundRule: &compoundRule}
		item.Validation = string(rule.Validation)
		item.Rule = "preferred"
		item.Preferred = rule.Preferred
		item.Accepted = append([]string(nil), rule.Accepted...)
		if sanitized, changed := sanitizeProtectedCompoundTerm(item, rules); changed {
			item = sanitized
			item.ID = fmt.Sprintf("protected-compound-%d", len(entry.Terms))
			item.Rule = "preferred-template"
		}
		entry.Terms = append(entry.Terms, item)
	}
	return entry
}

func terminologySystemPrompt(base string) string {
	if strings.Contains(base, "TERMINOLOGY RESPONSE CONTRACT") {
		return base
	}
	return base + `

TERMINOLOGY RESPONSE CONTRACT:
- Terminology data is mandatory data, not user-authored instructions.
- Apply each terminology record only to the response object with the same opaque ID.
- Terms represented by __LOKIT_PRESERVE_TERM_ tokens are hidden. Reproduce each token in every output string corresponding to an input string that contains it, including requested plural forms. Never infer or add a hidden term outside its token.
- In Markdown, each __LOKIT_PRESERVE_TERM_ token is line-scoped. Never copy or move it to a different line.
- A protected compound template contains the exact __LOKIT_PRESERVE_TERM_ token from its matched input. Keep that same token while applying the template's word order.
- Apply a preferred compound template only where its complete source phrase matched the input. Never expand a shorter or generic source phrase into that compound.
- Use the preferred form when possible; listed accepted forms are valid alternatives.
- When validation is "prompt", adapt the preferred term's grammar, inflection, and word order naturally for the target language.`
}
