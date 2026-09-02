package translate

import (
	"fmt"
	"strconv"
	"strings"

	po "github.com/minios-linux/lokit/internal/format/po"
	"github.com/minios-linux/lokit/terminology"
)

func poSourcePath(entry *po.Entry) string {
	if len(entry.References) == 0 {
		return ""
	}
	path := strings.Fields(entry.References[0])[0]
	for i := 0; i < 2; i++ {
		if colon := strings.LastIndexByte(path, ':'); colon >= 0 {
			if _, err := strconv.Atoi(path[colon+1:]); err == nil {
				path = path[:colon]
			}
		}
	}
	return strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "./")
}

func poSelector(entry *po.Entry, opts Options) terminology.Selector {
	return terminologySelector(opts, entry.MsgID, entry.MsgCtxt, poSourcePath(entry))
}

type poExactUpdate struct {
	entry *po.Entry
	forms []string
}

func planExactPO(poFile *po.File, opts Options, nplurals int) ([]poExactUpdate, error) {
	if opts.Terminology == nil {
		return nil, nil
	}
	var updates []poExactUpdate
	for _, entry := range poFile.Entries {
		if entry.MsgID == "" || entry.Obsolete || isKeyIgnored(entry.MsgID, opts) || isKeyLocked(entry.MsgID, opts) {
			continue
		}
		exact, ok, err := opts.Terminology.ResolveExact(entry.MsgID, entry.MsgIDPlural, opts.Language, poSelector(entry, opts))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		terms, err := resolveTerminologyTerms(entry.MsgID+"\n"+entry.MsgIDPlural, opts, poSelector(entry, opts))
		if err != nil {
			return nil, err
		}
		if entry.MsgIDPlural == "" {
			if len(exact.Translations) != 1 {
				return nil, fmt.Errorf("exact terminology rule %q for %q must provide one translation", exact.ID, entry.MsgID)
			}
			value := exact.Translations[0]
			if err := validatePOTranslations([]*po.Entry{entry}, []string{value}); err != nil {
				return nil, fmt.Errorf("exact terminology rule %q: %w", exact.ID, err)
			}
			if err := validateTerminology(entry.MsgID, value, terms); err != nil {
				return nil, fmt.Errorf("exact terminology rule %q: %w", exact.ID, err)
			}
			if entry.MsgStr != value || entry.IsFuzzy() {
				updates = append(updates, poExactUpdate{entry: entry, forms: []string{value}})
			}
			continue
		}
		if len(exact.Translations) != nplurals {
			return nil, fmt.Errorf("exact terminology rule %q for %q provides %d plural forms, expected %d", exact.ID, entry.MsgID, len(exact.Translations), nplurals)
		}
		translation := pluralTranslation{plural: exact.Translations}
		if err := validatePOPluralTranslations([]*po.Entry{entry}, []pluralTranslation{translation}); err != nil {
			return nil, fmt.Errorf("exact terminology rule %q: %w", exact.ID, err)
		}
		for form, value := range exact.Translations {
			source := entry.MsgIDPlural
			if form == 0 {
				source = entry.MsgID
			}
			if err := validateTerminology(source, value, terms); err != nil {
				return nil, fmt.Errorf("exact terminology rule %q plural form %d: %w", exact.ID, form, err)
			}
		}
		different := entry.IsFuzzy() || len(entry.MsgStrPlural) != len(exact.Translations)
		for form, value := range exact.Translations {
			if entry.MsgStrPlural[form] != value {
				different = true
			}
		}
		if different {
			updates = append(updates, poExactUpdate{entry: entry, forms: append([]string(nil), exact.Translations...)})
		}
	}
	return updates, nil
}

func applyPOExactUpdates(updates []poExactUpdate) []*po.Entry {
	changed := make([]*po.Entry, 0, len(updates))
	for _, update := range updates {
		if update.entry.MsgIDPlural == "" {
			update.entry.MsgStr = update.forms[0]
		} else {
			update.entry.MsgStrPlural = make(map[int]string, len(update.forms))
			for form, value := range update.forms {
				update.entry.MsgStrPlural[form] = value
			}
		}
		update.entry.SetFuzzy(false)
		changed = append(changed, update.entry)
	}
	return changed
}

func applyExactPO(poFile *po.File, opts Options, nplurals int) ([]*po.Entry, error) {
	updates, err := planExactPO(poFile, opts, nplurals)
	if err != nil {
		return nil, err
	}
	return applyPOExactUpdates(updates), nil
}

func collectEntriesWithTerminology(poFile *po.File, opts Options) ([]*po.Entry, error) {
	base := collectEntries(poFile, opts)
	if opts.Terminology == nil {
		return base, nil
	}
	selected := make(map[*po.Entry]bool, len(base))
	for _, entry := range base {
		selected[entry] = true
	}
	var out []*po.Entry
	for _, entry := range poFile.Entries {
		if entry.MsgID == "" || entry.Obsolete || isKeyIgnored(entry.MsgID, opts) || isKeyLocked(entry.MsgID, opts) {
			continue
		}
		selector := poSelector(entry, opts)
		if _, exact, err := opts.Terminology.ResolveExact(entry.MsgID, entry.MsgIDPlural, opts.Language, selector); err != nil {
			return nil, err
		} else if exact {
			continue
		}
		if selected[entry] {
			out = append(out, entry)
			continue
		}
		if entry.IsTranslated() {
			invalid, err := poTerminologyInvalid(entry, opts, selector)
			if err != nil {
				return nil, err
			}
			if invalid {
				out = append(out, entry)
			}
		}
	}
	return out, nil
}

func preflightPOTerminology(poFile *po.File, opts Options, nplurals int) error {
	if _, err := planExactPO(poFile, opts, nplurals); err != nil {
		return err
	}
	if opts.Terminology == nil {
		return nil
	}
	for _, entry := range poFile.Entries {
		if entry.MsgID == "" || entry.Obsolete || isKeyIgnored(entry.MsgID, opts) || isKeyLocked(entry.MsgID, opts) {
			continue
		}
		source := entry.MsgID
		if entry.MsgIDPlural != "" {
			source += "\n" + entry.MsgIDPlural
		}
		if _, err := resolveTerminologyTerms(source, opts, poSelector(entry, opts)); err != nil {
			return err
		}
	}
	return nil
}

func poTerminologyInvalid(entry *po.Entry, opts Options, selector terminology.Selector) (bool, error) {
	terms, err := resolveTerminologyTerms(entry.MsgID+"\n"+entry.MsgIDPlural, opts, selector)
	if err != nil {
		return false, err
	}
	if entry.MsgIDPlural == "" {
		return validateTerminology(entry.MsgID, entry.MsgStr, terms) != nil, nil
	}
	for form, value := range entry.MsgStrPlural {
		source := entry.MsgIDPlural
		if form == 0 {
			source = entry.MsgID
		}
		if validateTerminology(source, value, terms) != nil {
			return true, nil
		}
	}
	return false, nil
}

func poChunkTerminology(entries []*po.Entry, ids []string, opts Options) ([][]terminology.TermMatch, []terminologyPromptEntry, error) {
	rulesByEntry := make([][]terminology.TermMatch, len(entries))
	prompt := make([]terminologyPromptEntry, len(entries))
	for i, entry := range entries {
		source := entry.MsgID
		if entry.MsgIDPlural != "" {
			source += "\n" + entry.MsgIDPlural
		}
		rules, err := resolveTerminologyTerms(source, opts, poSelector(entry, opts))
		if err != nil {
			return nil, nil, err
		}
		rulesByEntry[i] = rules
		prompt[i] = promptTerms(ids[i], rules)
	}
	return rulesByEntry, prompt, nil
}

func validatePOChunkTerminology(entries []*po.Entry, translations []string, rules [][]terminology.TermMatch) error {
	for i, entry := range entries {
		if err := validateTerminology(entry.MsgID, translations[i], rules[i]); err != nil {
			return fmt.Errorf("entry %q: %w", entry.MsgID, err)
		}
	}
	return nil
}

func validatePOPluralChunkTerminology(entries []*po.Entry, translations []pluralTranslation, rules [][]terminology.TermMatch) error {
	for i, entry := range entries {
		if entry.MsgIDPlural == "" {
			if err := validateTerminology(entry.MsgID, translations[i].singular, rules[i]); err != nil {
				return fmt.Errorf("entry %q: %w", entry.MsgID, err)
			}
			continue
		}
		for form, value := range translations[i].plural {
			source := entry.MsgIDPlural
			if form == 0 {
				source = entry.MsgID
			}
			if err := validateTerminology(source, value, rules[i]); err != nil {
				return fmt.Errorf("entry %q plural form %d: %w", entry.MsgID, form, err)
			}
		}
	}
	return nil
}

func restorePOPluralPreservedTerms(entries []*po.Entry, translations []pluralTranslation, singular, plural []map[string]string, rules [][]terminology.TermMatch, namespace string) error {
	protectedRules := collectProtectedRules(rules)
	for i, entry := range entries {
		if entry.MsgIDPlural == "" {
			if err := validateRawPreservedTerms(translations[i].singular, singular[i], protectedRules); err != nil {
				return fmt.Errorf("entry %q: %w", entry.MsgID, err)
			}
			value, err := restorePreservedTerms(translations[i].singular, singular[i], namespace)
			if err != nil {
				return fmt.Errorf("entry %q: %w", entry.MsgID, err)
			}
			translations[i].singular = value
			continue
		}
		for form, value := range translations[i].plural {
			preserved := plural[i]
			if form == 0 {
				preserved = singular[i]
			}
			if err := validateRawPreservedTerms(value, preserved, protectedRules); err != nil {
				return fmt.Errorf("entry %q plural form %d: %w", entry.MsgID, form, err)
			}
			restored, err := restorePreservedTerms(value, preserved, namespace)
			if err != nil {
				return fmt.Errorf("entry %q plural form %d: %w", entry.MsgID, form, err)
			}
			translations[i].plural[form] = restored
		}
	}
	return nil
}

// CountPOTerminologyViolations counts translated entries that do not satisfy
// an exact or term rule. Empty, fuzzy, obsolete, and ignored entries are not
// double-counted as terminology failures.
func CountPOTerminologyViolations(poFile *po.File, opts Options) (int, error) {
	if opts.Terminology == nil {
		return 0, nil
	}
	violations := 0
	for _, entry := range poFile.Entries {
		if entry.MsgID == "" || entry.Obsolete || entry.IsFuzzy() || !entry.IsTranslated() || isKeyIgnored(entry.MsgID, opts) {
			continue
		}
		selector := poSelector(entry, opts)
		exact, matched, err := opts.Terminology.ResolveExact(entry.MsgID, entry.MsgIDPlural, opts.Language, selector)
		if err != nil {
			return 0, err
		}
		if matched {
			current := []string{entry.MsgStr}
			if entry.MsgIDPlural != "" {
				current = current[:0]
				for i := 0; i < len(entry.MsgStrPlural); i++ {
					current = append(current, entry.MsgStrPlural[i])
				}
			}
			if !slicesEqual(current, exact.Translations) {
				violations++
				continue
			}
		}
		invalid, err := poTerminologyInvalid(entry, opts, selector)
		if err != nil {
			return 0, err
		}
		if invalid {
			violations++
		}
	}
	return violations, nil
}
