package translate

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/minios-linux/lokit/terminology"
)

type terminologyPromptTerm struct {
	ID         string   `json:"id"`
	Source     string   `json:"source,omitempty"`
	Rule       string   `json:"rule"`
	Preferred  string   `json:"preferred,omitempty"`
	Accepted   []string `json:"accepted,omitempty"`
	Validation string   `json:"validation,omitempty"`
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
		fmt.Sprintf("Protected terminology cardinality mismatch: expected %d occurrence(s) after placeholder restoration, found %d. Reproduce each __LOKIT_PRESERVE_TERM_ token in every output string corresponding to an input string that contains it, including requested plural forms. Do not infer or add hidden terms outside those tokens; continue to apply visible preferred compound terms from the original request.", e.required, e.found)
}

func hasPreservedTermValues(groups ...map[string]string) bool {
	for _, group := range groups {
		if len(group) > 0 {
			return true
		}
	}
	return false
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
			if preserveSpanCoveredByTranslatedTerm(rule, span, text, rules) {
				continue
			}
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

func preserveSpanCoveredByTranslatedTerm(preserve terminology.TermMatch, span terminology.TermSpan, text string, rules []terminology.TermMatch) bool {
	for _, rule := range rules {
		if rule.Preserve {
			continue
		}
		containsPreservedForm := false
		for _, expected := range rule.Expected() {
			if preserve.MatchesSource(expected) {
				containsPreservedForm = true
				break
			}
		}
		if !containsPreservedForm {
			continue
		}
		for _, translatedSpan := range rule.SourceSpans(text) {
			if translatedSpan.Start <= span.Start && translatedSpan.End >= span.End {
				return true
			}
		}
	}
	return false
}

func restorePreservedTerms(text string, values map[string]string, namespace string) (string, error) {
	tokens := make([]string, 0, len(values))
	for token := range values {
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
		"Never infer or add a hidden term outside its token. Prefer the preferred form; accepted forms are also valid. " +
		"When validation is prompt, adapt the preferred term's grammar, inflection, and word order naturally for the translated sentence.\n" + string(data)
}

func promptTerms(id string, rules []terminology.TermMatch) terminologyPromptEntry {
	entry := terminologyPromptEntry{ID: id}
	for _, rule := range rules {
		if rule.Preserve {
			continue
		}
		item := terminologyPromptTerm{ID: rule.ID, Source: rule.Source}
		item.Validation = string(rule.Validation)
		item.Rule = "preferred"
		item.Preferred = rule.Preferred
		item.Accepted = append([]string(nil), rule.Accepted...)
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
- Use the preferred form when possible; listed accepted forms are valid alternatives.
- When validation is "prompt", adapt the preferred term's grammar, inflection, and word order naturally for the target language.`
}
