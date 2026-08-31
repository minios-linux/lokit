package translate

import (
	"fmt"
	"strings"

	formatfile "github.com/minios-linux/lokit/internal/format"
	"github.com/minios-linux/lokit/lockfile"
	"github.com/minios-linux/lokit/terminology"
)

func kvSourceValue(key string, sourceValues map[string]string) (string, bool) {
	if sourceValues == nil {
		return key, true
	}
	value, ok := sourceValues[key]
	return value, ok && value != ""
}

func prepareKVWork(file formatfile.KVFile, sourceValues map[string]string, lockKeyPrefix string, opts Options, translator KVChunkTranslator, applyDirect bool) (provider, direct []string, err error) {
	for _, key := range file.Keys() {
		if isKeyIgnoredExact(key, opts) {
			continue
		}
		if isKeyIgnoredPattern(key, opts) {
			if source, ok := sourceValues[key]; ok {
				current, _ := file.Get(key)
				if current != source && applyDirect {
					if !file.Set(key, source) {
						return nil, nil, fmt.Errorf("copying ignored key %q from source", key)
					}
					direct = append(direct, key)
				}
			}
			if applyDirect && opts.LockFile != nil {
				lockTarget := lockfile.LockTargetKey(opts.LockTarget, opts.Language)
				opts.LockFile.Remove(lockTarget, scopedLockKey(lockKeyPrefix, key))
			}
			continue
		}
		if isKeyLocked(key, opts) {
			continue
		}
		source, ok := kvSourceValue(key, sourceValues)
		if !ok {
			continue
		}
		selector := terminologySelector(opts, key, "", opts.SourcePath)
		terms, resolveTermsErr := resolveTerminologyTerms(source, opts, selector)
		if resolveTermsErr != nil {
			return nil, nil, resolveTermsErr
		}
		if opts.Terminology != nil {
			exact, matched, resolveErr := opts.Terminology.ResolveExact(source, "", opts.Language, selector)
			if resolveErr != nil {
				return nil, nil, resolveErr
			}
			if matched {
				if len(exact.Translations) != 1 {
					return nil, nil, fmt.Errorf("exact terminology rule %q for key %q must provide one translation", exact.ID, key)
				}
				value := exact.Translations[0]
				if err := validateKVTranslations([]string{key}, map[string]string{key: source}, []string{value}); err != nil {
					return nil, nil, fmt.Errorf("exact terminology rule %q: %w", exact.ID, err)
				}
				if err := validateTerminology(source, value, terms); err != nil {
					return nil, nil, fmt.Errorf("exact terminology rule %q: %w", exact.ID, err)
				}
				if isMarkdownTranslator(translator) {
					if _, bad := firstInvalidMarkdownTranslation([]string{key}, []string{value}, map[string]string{key: source}); bad {
						return nil, nil, fmt.Errorf("exact terminology rule %q breaks Markdown structure for key %q", exact.ID, key)
					}
				}
				current, _ := file.Get(key)
				if current != value {
					if applyDirect && !file.Set(key, value) {
						return nil, nil, fmt.Errorf("applying exact terminology rule %q to key %q", exact.ID, key)
					}
					direct = append(direct, key)
				}
				continue
			}
		}

		current, exists := file.Get(key)
		translated := exists && strings.TrimSpace(current) != ""
		selected := opts.RetranslateExisting || opts.ForceTranslate || !translated
		terminologyViolation := false
		if translated && opts.Terminology != nil {
			terminologyViolation = validateTerminology(source, current, terms) != nil
			selected = selected || terminologyViolation
		}
		if !selected && opts.LockFile != nil {
			lockKey := scopedLockKey(lockKeyPrefix, key)
			content := source
			if sourceValues != nil {
				content = lockfile.KVEntryContent(lockKey, source)
			}
			lockTarget := lockfile.LockTargetKey(opts.LockTarget, opts.Language)
			selected = opts.LockFile.IsChanged(lockTarget, lockKey, content)
		}
		if selected {
			provider = append(provider, key)
		}
	}
	return provider, direct, nil
}

func kvChunkTerminology(keys []string, sourceValues map[string]string, ids []string, opts Options) ([][]terminology.TermMatch, []terminologyPromptEntry, error) {
	rulesByKey := make([][]terminology.TermMatch, len(keys))
	prompt := make([]terminologyPromptEntry, len(keys))
	for i, key := range keys {
		source, _ := kvSourceValue(key, sourceValues)
		rules, err := resolveTerminologyTerms(source, opts, terminologySelector(opts, key, "", opts.SourcePath))
		if err != nil {
			return nil, nil, err
		}
		rulesByKey[i] = rules
		prompt[i] = promptTerms(ids[i], rules)
	}
	return rulesByKey, prompt, nil
}

func validateKVChunkTerminology(keys []string, sourceValues map[string]string, translations []string, rules [][]terminology.TermMatch, sourcePath string) error {
	for i, key := range keys {
		source, _ := kvSourceValue(key, sourceValues)
		if err := validateTerminology(source, translations[i], rules[i]); err != nil {
			if sourcePath != "" {
				return fmt.Errorf("%s:%s: %w", sourcePath, key, err)
			}
			return fmt.Errorf("key %q: %w", key, err)
		}
	}
	return nil
}

// CountKVTerminologyViolations counts translated keys that violate exact or term rules.
func CountKVTerminologyViolations(file formatfile.KVFile, sourceValues map[string]string, opts Options) (int, error) {
	violations, err := FindKVTerminologyViolations(file, sourceValues, opts)
	return len(violations), err
}

// FindKVTerminologyViolations returns format-native keys that violate policy.
func FindKVTerminologyViolations(file formatfile.KVFile, sourceValues map[string]string, opts Options) ([]string, error) {
	if opts.Terminology == nil {
		return nil, nil
	}
	var violations []string
	for _, key := range file.Keys() {
		if isKeyIgnored(key, opts) {
			continue
		}
		source, ok := kvSourceValue(key, sourceValues)
		if !ok {
			continue
		}
		current, exists := file.Get(key)
		if !exists || strings.TrimSpace(current) == "" {
			continue
		}
		selector := terminologySelector(opts, key, "", opts.SourcePath)
		exact, matched, err := opts.Terminology.ResolveExact(source, "", opts.Language, selector)
		if err != nil {
			return nil, err
		}
		if matched && (len(exact.Translations) != 1 || current != exact.Translations[0]) {
			violations = append(violations, key+" ("+exact.ID+")")
			continue
		}
		terms, err := resolveTerminologyTerms(source, opts, selector)
		if err != nil {
			return nil, err
		}
		for _, rule := range terms {
			if !rule.ValidTranslation(source, current) {
				violations = append(violations, key+" ("+rule.ID+")")
				break
			}
		}
	}
	return violations, nil
}
