package terminology

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads one version 1 terminology file.
func Load(path string) (*Catalog, error) {
	return LoadFiles([]string{path})
}

// LoadFiles reads terminology files in order and returns one immutable catalog.
func LoadFiles(paths []string) (*Catalog, error) {
	catalog := &Catalog{}
	ids := make(map[string]string)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading terminology %s: %w", path, err)
		}
		var document yaml.Node
		if err := yaml.Unmarshal(data, &document); err != nil {
			return nil, fmt.Errorf("parsing terminology %s: %w", path, err)
		}
		if err := parseFile(path, &document, catalog, ids); err != nil {
			return nil, err
		}
	}
	return catalog, nil
}

func parseFile(path string, document *yaml.Node, catalog *Catalog, ids map[string]string) error {
	if len(document.Content) != 1 {
		return nodeError(path, document, "terminology file must contain one document")
	}
	root := document.Content[0]
	fields, err := mapping(path, root, "terminology file", "version", "exact", "terms")
	if err != nil {
		return err
	}
	version, ok := fields["version"]
	if !ok {
		return nodeError(path, root, "version is required")
	}
	var versionNumber int
	if version.Kind != yaml.ScalarNode || version.Tag != "!!int" || version.Decode(&versionNumber) != nil || versionNumber != 1 {
		return nodeError(path, version, "version must be 1")
	}
	if node := fields["exact"]; node != nil {
		if node.Kind != yaml.SequenceNode {
			return nodeError(path, node, "exact must be a list")
		}
		for _, item := range node.Content {
			rule, err := parseExact(path, item, len(catalog.exact)+len(catalog.terms))
			if err != nil {
				return err
			}
			if first, exists := ids[rule.id]; exists {
				return nodeError(path, item, "duplicate rule id %q (first declared in %s)", rule.id, first)
			}
			ids[rule.id] = path
			catalog.exact = append(catalog.exact, rule)
		}
	}
	if node := fields["terms"]; node != nil {
		if node.Kind != yaml.SequenceNode {
			return nodeError(path, node, "terms must be a list")
		}
		for _, item := range node.Content {
			rule, err := parseTerm(path, item, len(catalog.exact)+len(catalog.terms))
			if err != nil {
				return err
			}
			if first, exists := ids[rule.id]; exists {
				return nodeError(path, item, "duplicate rule id %q (first declared in %s)", rule.id, first)
			}
			ids[rule.id] = path
			catalog.terms = append(catalog.terms, rule)
		}
	}
	return nil
}

func parseExact(path string, node *yaml.Node, order int) (exactRule, error) {
	fields, err := mapping(path, node, "exact entry", "id", "source", "source_plural", "when", "translations")
	if err != nil {
		return exactRule{}, err
	}
	id, err := requiredString(path, node, fields, "id")
	if err != nil {
		return exactRule{}, err
	}
	source, err := requiredString(path, node, fields, "source")
	if err != nil {
		return exactRule{}, err
	}
	sourcePlural, err := optionalString(path, fields["source_plural"], "source_plural")
	if err != nil {
		return exactRule{}, err
	}
	if fields["source_plural"] != nil && sourcePlural == "" {
		return exactRule{}, nodeError(path, fields["source_plural"], "source_plural must not be empty")
	}
	when, err := parseCondition(path, fields["when"])
	if err != nil {
		return exactRule{}, err
	}
	translations, err := parseExactTranslations(path, fields["translations"])
	if err != nil {
		return exactRule{}, err
	}
	return exactRule{id: id, source: source, sourcePlural: sourcePlural, when: when, translations: translations, order: order}, nil
}

func parseTerm(path string, node *yaml.Node, order int) (termRule, error) {
	fields, err := mapping(path, node, "term entry", "id", "source", "match", "case_sensitive", "validation", "when", "preserve", "translations")
	if err != nil {
		return termRule{}, err
	}
	id, err := requiredString(path, node, fields, "id")
	if err != nil {
		return termRule{}, err
	}
	source, err := requiredString(path, node, fields, "source")
	if err != nil {
		return termRule{}, err
	}
	mode := MatchWord
	if fields["match"] != nil {
		value, err := optionalString(path, fields["match"], "match")
		if err != nil {
			return termRule{}, err
		}
		mode = MatchMode(value)
		if mode != MatchWord && mode != MatchSubstring {
			return termRule{}, nodeError(path, fields["match"], "match must be word or substring")
		}
	}
	caseSensitive, err := optionalBool(path, fields["case_sensitive"], "case_sensitive")
	if err != nil {
		return termRule{}, err
	}
	preserveNode := fields["preserve"]
	preserve, err := optionalBool(path, preserveNode, "preserve")
	if err != nil {
		return termRule{}, err
	}
	if preserveNode != nil && !preserve {
		return termRule{}, nodeError(path, preserveNode, "preserve, when present, must be true")
	}
	validation := ValidationStrict
	if fields["validation"] != nil {
		value, err := optionalString(path, fields["validation"], "validation")
		if err != nil {
			return termRule{}, err
		}
		validation = ValidationMode(value)
		if validation != ValidationStrict && validation != ValidationPrompt {
			return termRule{}, nodeError(path, fields["validation"], "validation must be strict or prompt")
		}
	}
	if preserve && validation != ValidationStrict {
		return termRule{}, nodeError(path, fields["validation"], "preserve terms require strict validation")
	}
	translationsNode := fields["translations"]
	if (preserveNode != nil) == (translationsNode != nil) {
		return termRule{}, nodeError(path, node, "term entry must define exactly one of preserve: true or translations")
	}
	when, err := parseCondition(path, fields["when"])
	if err != nil {
		return termRule{}, err
	}
	var translations []localizedTerm
	if translationsNode != nil {
		translations, err = parseTermTranslations(path, translationsNode)
		if err != nil {
			return termRule{}, err
		}
	}
	return termRule{id: id, source: source, match: mode, caseSensitive: caseSensitive, when: when, preserve: preserve, validation: validation, translations: translations, order: order}, nil
}

func parseCondition(path string, node *yaml.Node) (condition, error) {
	if node == nil {
		return condition{}, nil
	}
	fields, err := mapping(path, node, "when", "target", "format", "path", "key", "context")
	if err != nil {
		return condition{}, err
	}
	var result condition
	for i, name := range []string{"target", "format", "path", "key", "context"} {
		value := fields[name]
		if value == nil {
			continue
		}
		patterns, err := stringList(path, value, "when."+name, true)
		if err != nil {
			return condition{}, err
		}
		result.fields++
		maxLiterals := 0
		for _, pattern := range patterns {
			compiled, literals, err := compilePattern(pattern)
			if err != nil {
				return condition{}, nodeError(path, value, "invalid when.%s pattern %q: %v", name, pattern, err)
			}
			result.patterns[i] = append(result.patterns[i], compiled)
			if literals > maxLiterals {
				maxLiterals = literals
			}
		}
		result.literals += maxLiterals
	}
	return result, nil
}

func parseExactTranslations(path string, node *yaml.Node) ([]localizedExact, error) {
	if node == nil {
		return nil, nodeError(path, node, "translations is required")
	}
	entries, err := localeMapping(path, node)
	if err != nil {
		return nil, err
	}
	out := make([]localizedExact, 0, len(entries))
	for _, entry := range entries {
		values, err := stringList(path, entry.value, "translation", true)
		if err != nil {
			return nil, err
		}
		out = append(out, localizedExact{locale: entry.locale, translations: values})
	}
	return out, nil
}

func parseTermTranslations(path string, node *yaml.Node) ([]localizedTerm, error) {
	entries, err := localeMapping(path, node)
	if err != nil {
		return nil, err
	}
	out := make([]localizedTerm, 0, len(entries))
	for _, entry := range entries {
		if entry.value.Kind == yaml.ScalarNode {
			preferred, err := scalarString(path, entry.value, "term translation")
			if err != nil || preferred == "" {
				if err != nil {
					return nil, err
				}
				return nil, nodeError(path, entry.value, "term translation must not be empty")
			}
			out = append(out, localizedTerm{locale: entry.locale, preferred: preferred})
			continue
		}
		fields, err := mapping(path, entry.value, "term translation", "preferred", "accepted")
		if err != nil {
			return nil, err
		}
		preferred, err := requiredString(path, entry.value, fields, "preferred")
		if err != nil {
			return nil, err
		}
		var accepted []string
		if fields["accepted"] != nil {
			accepted, err = stringList(path, fields["accepted"], "accepted", false)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, localizedTerm{locale: entry.locale, preferred: preferred, accepted: accepted})
	}
	return out, nil
}

type localeEntry struct {
	locale string
	value  *yaml.Node
}

func localeMapping(path string, node *yaml.Node) ([]localeEntry, error) {
	if node == nil || node.Kind != yaml.MappingNode || len(node.Content) == 0 {
		return nil, nodeError(path, node, "translations must be a non-empty locale map")
	}
	seenRaw := make(map[string]struct{})
	seenNormalized := make(map[string]string)
	out := make([]localeEntry, 0, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		raw, err := scalarString(path, key, "locale key")
		if err != nil {
			return nil, err
		}
		if _, exists := seenRaw[raw]; exists {
			return nil, nodeError(path, key, "duplicate locale key %q", raw)
		}
		seenRaw[raw] = struct{}{}
		normalized, err := NormalizeLocale(raw)
		if err != nil {
			return nil, nodeError(path, key, "%v", err)
		}
		if first, exists := seenNormalized[normalized]; exists {
			return nil, nodeError(path, key, "locale %q duplicates normalized locale key %q", raw, first)
		}
		seenNormalized[normalized] = raw
		out = append(out, localeEntry{locale: normalized, value: value})
	}
	return out, nil
}

func mapping(path string, node *yaml.Node, label string, allowed ...string) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, nodeError(path, node, "%s must be an object", label)
	}
	valid := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		valid[key] = struct{}{}
	}
	result := make(map[string]*yaml.Node, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		name, err := scalarString(path, key, label+" field")
		if err != nil {
			return nil, err
		}
		if _, exists := result[name]; exists {
			return nil, nodeError(path, key, "duplicate field %q in %s", name, label)
		}
		if _, exists := valid[name]; !exists {
			return nil, nodeError(path, key, "unknown field %q in %s", name, label)
		}
		result[name] = value
	}
	return result, nil
}

func requiredString(path string, parent *yaml.Node, fields map[string]*yaml.Node, name string) (string, error) {
	node := fields[name]
	if node == nil {
		return "", nodeError(path, parent, "%s is required", name)
	}
	value, err := scalarString(path, node, name)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", nodeError(path, node, "%s must not be empty", name)
	}
	return value, nil
}

func optionalString(path string, node *yaml.Node, name string) (string, error) {
	if node == nil {
		return "", nil
	}
	return scalarString(path, node, name)
}

func scalarString(path string, node *yaml.Node, label string) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", nodeError(path, node, "%s must be a string", label)
	}
	return node.Value, nil
}

func optionalBool(path string, node *yaml.Node, name string) (bool, error) {
	if node == nil {
		return false, nil
	}
	var value bool
	if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" || node.Decode(&value) != nil {
		return false, nodeError(path, node, "%s must be a boolean", name)
	}
	return value, nil
}

func stringList(path string, node *yaml.Node, label string, scalarAllowed bool) ([]string, error) {
	if scalarAllowed && node != nil && node.Kind == yaml.ScalarNode {
		value, err := scalarString(path, node, label)
		if err != nil {
			return nil, err
		}
		if value == "" {
			return nil, nodeError(path, node, "%s must not be empty", label)
		}
		return []string{value}, nil
	}
	if node == nil || node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
		return nil, nodeError(path, node, "%s must be a non-empty string list", label)
	}
	out := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		value, err := scalarString(path, item, label)
		if err != nil {
			return nil, err
		}
		if value == "" {
			return nil, nodeError(path, item, "%s must not contain empty strings", label)
		}
		out = append(out, value)
	}
	return out, nil
}

func nodeError(path string, node *yaml.Node, format string, args ...any) error {
	line, column := 1, 1
	if node != nil && node.Line > 0 {
		line, column = node.Line, node.Column
	}
	return fmt.Errorf("%s:%d:%d: %s", path, line, column, fmt.Sprintf(format, args...))
}
