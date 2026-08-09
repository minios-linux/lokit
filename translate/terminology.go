package translate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/minios-linux/lokit/terminology"
)

type terminologyPromptTerm struct {
	ID        string   `json:"id"`
	Source    string   `json:"source"`
	Rule      string   `json:"rule"`
	Preferred string   `json:"preferred,omitempty"`
	Accepted  []string `json:"accepted,omitempty"`
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
			return fmt.Errorf("terminology rule %q for %q requires %v", rule.ID, rule.Source, rule.Expected())
		}
	}
	return nil
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
		"Preserve terms marked preserve exactly and prefer the preferred form; accepted forms are also valid.\n" + string(data)
}

func promptTerms(id string, rules []terminology.TermMatch) terminologyPromptEntry {
	entry := terminologyPromptEntry{ID: id}
	for _, rule := range rules {
		item := terminologyPromptTerm{ID: rule.ID, Source: rule.Source}
		if rule.Preserve {
			item.Rule = "preserve"
		} else {
			item.Rule = "preferred"
			item.Preferred = rule.Preferred
			item.Accepted = append([]string(nil), rule.Accepted...)
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
- Preserve terms marked "preserve" exactly.
- Use the preferred form when possible; listed accepted forms are valid alternatives.`
}
