package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	mdformat "github.com/minios-linux/lokit/internal/format/markdown"
	"github.com/minios-linux/lokit/terminology"
)

type preparedMarkdownField struct {
	id        string
	scope     int
	source    string
	masked    string
	kind      string
	context   string
	rules     []terminology.TermMatch
	preserved map[string]string
	terms     []terminologyPromptTerm
	fixed     *string
}

type preparedMarkdownPlan struct {
	key            string
	id             string
	source         string
	plan           *mdformat.MarkdownPlan
	fields         []preparedMarkdownField
	fieldByID      map[string]preparedMarkdownField
	protectedRules []terminology.TermMatch
}

type markdownProviderField struct {
	ID      string                  `json:"id"`
	Source  string                  `json:"source"`
	Kind    string                  `json:"kind"`
	Context string                  `json:"context"`
	Terms   []terminologyPromptTerm `json:"terms,omitempty"`
}

type markdownProviderUnit struct {
	ID     string                  `json:"id"`
	Fields []markdownProviderField `json:"fields"`
}

var markdownFrontmatterMachineValue = regexp.MustCompile(`^(?:true|false|null|[-+]?(?:\d+(?:\.\d+)?|0[xX][0-9a-fA-F]+|0[oO][0-7]+|0[bB][01]+)|\d{4}-\d{2}-\d{2}|[0-9a-fA-F]{40,64})$`)
var markdownFrontmatterFilename = regexp.MustCompile(`^[A-Za-z0-9_.-]+\.(?:svg|png|jpe?g|gif|webp|ico|css|js|mjs|json|ya?ml|toml|md|pdf|zip|iso)$`)

func translateMarkdownPatchChunk(ctx context.Context, keys []string, srcVals map[string]string, systemPrompt string, opts Options, rl *rateLimitState) ([]string, error) {
	prepared, err := prepareMarkdownPlans(keys, srcVals, opts)
	if err != nil {
		return nil, err
	}
	results := make([]string, len(prepared))
	var requested []*preparedMarkdownPlan
	for i := range prepared {
		if len(prepared[i].fieldByID) == 0 {
			if len(prepared[i].fields) == 0 {
				results[i] = prepared[i].source
			} else {
				rendered, renderErr := renderMarkdownPatch(&prepared[i], nil)
				if renderErr != nil {
					return nil, fmt.Errorf("key %q: %w", prepared[i].key, renderErr)
				}
				results[i] = rendered
			}
			continue
		}
		requested = append(requested, &prepared[i])
	}
	if len(requested) == 0 {
		return results, nil
	}
	patches, err := requestMarkdownPatches(ctx, requested, systemPrompt, opts, rl, opts.effectiveMaxRetries(), "")
	if err != nil {
		if len(requested) == 1 {
			return nil, err
		}
		for i := range prepared {
			if len(prepared[i].fields) == 0 {
				continue
			}
			rendered, singletonErr := retryMarkdownPlan(ctx, &prepared[i], systemPrompt, opts, rl, err)
			if singletonErr != nil {
				return nil, fmt.Errorf("key %q after malformed batch response: %w", prepared[i].key, singletonErr)
			}
			results[i] = rendered
		}
		if err := validateKVTranslations(keys, srcVals, results); err != nil {
			return nil, err
		}
		return results, nil
	}
	for i := range prepared {
		if len(prepared[i].fieldByID) == 0 {
			continue
		}
		rendered, renderErr := renderMarkdownPatch(&prepared[i], patches[prepared[i].id])
		if renderErr != nil {
			rendered, renderErr = retryMarkdownPlan(ctx, &prepared[i], systemPrompt, opts, rl, renderErr)
		}
		if renderErr != nil {
			return nil, fmt.Errorf("key %q: %w", prepared[i].key, renderErr)
		}
		results[i] = rendered
	}
	if err := validateKVTranslations(keys, srcVals, results); err != nil {
		return nil, err
	}
	return results, nil
}

func prepareMarkdownPlans(keys []string, srcVals map[string]string, opts Options) ([]preparedMarkdownPlan, error) {
	ids := kvTranslationIDs(keys)
	prepared := make([]preparedMarkdownPlan, len(keys))
	fieldScope := 0
	for i, key := range keys {
		source := key
		if value := srcVals[key]; value != "" {
			source = value
		}
		item := preparedMarkdownPlan{key: key, id: ids[i], source: source, fieldByID: make(map[string]preparedMarkdownField)}
		if strings.HasPrefix(key, "fm:") {
			if markdownFrontmatterHostField(key, source) {
				prepared[i] = item
				continue
			}
		}
		plan, err := mdformat.BuildPlan(opts.SourcePath, key, []byte(source))
		if err != nil {
			return nil, fmt.Errorf("key %q: build Markdown plan: %w", key, err)
		}
		item.plan = plan
		for _, field := range plan.Fields() {
			preparedField, err := prepareMarkdownField(key, field, fieldScope, opts)
			if err != nil {
				return nil, fmt.Errorf("key %q field %q: %w", key, field.ID, err)
			}
			item.fields = append(item.fields, preparedField)
			fieldScope++
		}
		for _, field := range item.fields {
			if field.fixed == nil {
				item.fieldByID[field.id] = field
			}
		}
		prepared[i] = item
	}
	var requestProtectedRules []terminology.TermMatch
	seenProtectedRules := make(map[string]bool)
	for _, plan := range prepared {
		for _, field := range plan.fields {
			for _, rule := range field.rules {
				ruleKey := fmt.Sprintf("%s\x00%s\x00%s\x00%t", rule.ID, rule.Source, rule.Match, rule.CaseSensitive)
				if rule.Preserve && !seenProtectedRules[ruleKey] {
					seenProtectedRules[ruleKey] = true
					requestProtectedRules = append(requestProtectedRules, rule)
				}
			}
		}
	}
	for i := range prepared {
		for fieldIndex := range prepared[i].fields {
			field := &prepared[i].fields[fieldIndex]
			namespace := preservedTermNamespace([]string{field.id, field.source})
			field.masked, field.preserved = maskPreservedTerms(field.source, requestProtectedRules, namespace, field.scope)
			field.terms = bindProtectedCompoundTerms(promptTerms(field.id, field.rules), []string{field.masked}, field.preserved).Terms
			if field.fixed != nil {
				fixed := *field.fixed
				for token, protected := range field.preserved {
					fixed = strings.Replace(fixed, protected, token, 1)
				}
				field.fixed = &fixed
			}
		}
		prepared[i].fieldByID = make(map[string]preparedMarkdownField)
		for _, field := range prepared[i].fields {
			if field.fixed == nil {
				prepared[i].fieldByID[field.id] = field
			}
		}
		prepared[i].protectedRules = requestProtectedRules
	}
	return prepared, nil
}

func markdownFrontmatterHostField(key, source string) bool {
	leaf := key
	if index := strings.LastIndex(key, "."); index >= 0 {
		leaf = key[index+1:]
	} else {
		leaf = strings.TrimPrefix(key, "fm:")
	}
	leaf = strings.ToLower(leaf)
	switch leaf {
	case "layout", "updated", "date", "time", "link", "href", "url", "src", "asset", "file", "filename", "path", "permalink", "route", "slug", "locale", "lang", "language", "id", "ref", "target", "theme", "icon", "commit", "sha", "version", "weight", "order", "draft", "published", "telegramdiscussion":
		return true
	}
	value := strings.TrimSpace(source)
	if markdownFrontmatterMachineValue.MatchString(value) || markdownFrontmatterFilename.MatchString(value) ||
		strings.HasPrefix(value, "/") || strings.HasPrefix(value, "./") ||
		strings.HasPrefix(value, "../") || strings.HasPrefix(value, "#") || strings.HasPrefix(value, "http://") ||
		strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "mailto:") {
		return true
	}
	return false
}

func prepareMarkdownField(key string, field mdformat.TextField, scope int, opts Options) (preparedMarkdownField, error) {
	selector := terminologySelector(opts, key, field.Context, opts.SourcePath)
	rules, err := resolveTerminologyTerms(field.Source, opts, selector)
	if err != nil {
		return preparedMarkdownField{}, err
	}
	namespace := preservedTermNamespace([]string{string(field.ID), field.Source})
	masked, preserved := maskPreservedTerms(field.Source, rules, namespace, scope)
	terms := bindProtectedCompoundTerms(promptTerms(string(field.ID), rules), []string{masked}, preserved).Terms
	prepared := preparedMarkdownField{
		id:        string(field.ID),
		scope:     scope,
		source:    field.Source,
		masked:    masked,
		kind:      string(field.Kind),
		context:   field.Context,
		rules:     rules,
		preserved: preserved,
		terms:     terms,
	}
	if opts.Terminology != nil {
		exact, matched, err := opts.Terminology.ResolveExact(field.Source, "", opts.Language, selector)
		if err != nil {
			return preparedMarkdownField{}, err
		}
		if matched {
			if len(exact.Translations) != 1 {
				return preparedMarkdownField{}, fmt.Errorf("exact terminology rule %q must provide one translation", exact.ID)
			}
			value := exact.Translations[0]
			prepared.fixed = &value
		}
	}
	return prepared, nil
}

func markdownPatchSystemPrompt(base string) string {
	return base + `

MARKDOWN TEXT PATCH CONTRACT:
- Lokit owns the Markdown syntax tree and all immutable resources. You translate only the declared text fields.
- Return ONLY a JSON array with one object per unit: {"id":"kv-...","values":{"field-id":"translated text"}}.
- Copy every unit ID and field ID exactly. Do not omit, duplicate, rename, or invent IDs or fields.
- Each field value must be a string. An explicitly empty string is allowed when wording moves to another field in the same context.
- Read all fields of a context together and translate them naturally, including link-label grammar, while keeping their order.
- Field values are plain text, not Markdown. Do not add Markdown syntax, URLs, code, links, images, HTML, or opaque IDs.
- Preserve every __LOKIT_PRESERVE_TERM_ token from a source field exactly once in that same field. These required source placeholders are the only opaque IDs allowed in values.
- Never infer, spell, or add the hidden protected term outside its source placeholder, and never move a protected placeholder between fields.
- Terminology records inside a field apply only to the value with that exact field ID.
- For terminology records, prefer "preferred" while listed "accepted" forms are valid alternatives; when validation is "prompt", adapt grammar, inflection, and word order naturally.
- Do not omit or summarize source content. Each non-empty phrasing context must retain translated text, although ordinary wording may move between fields of that same context.
- This contract replaces every earlier instruction to return complete Markdown or an "id"/"translation" object.`
}

func buildMarkdownPatchPrompt(plans []*preparedMarkdownPlan, opts Options) string {
	units := make([]markdownProviderUnit, len(plans))
	for i, plan := range plans {
		unit := markdownProviderUnit{ID: plan.id, Fields: make([]markdownProviderField, 0, len(plan.fieldByID))}
		for _, field := range plan.fields {
			if field.fixed == nil {
				unit.Fields = append(unit.Fields, markdownProviderField{ID: field.id, Source: field.masked, Kind: field.kind, Context: field.context, Terms: field.terms})
			}
		}
		units[i] = unit
	}
	data, _ := json.Marshal(units)
	sourceLanguage := opts.SourceLanguageName
	if sourceLanguage == "" {
		sourceLanguage = opts.resolvedSourceLangName()
	}
	return fmt.Sprintf("Translate the mutable Markdown text fields from %s to %s. Fields sharing a context belong to one phrasing frame and may redistribute ordinary wording while preserving field order. Return the exact JSON patch contract only.\n\n%s", sourceLanguage, opts.LanguageName, data)
}

func requestMarkdownPatches(ctx context.Context, plans []*preparedMarkdownPlan, systemPrompt string, opts Options, rl *rateLimitState, maxRetries int, feedback string) (map[string]map[string]string, error) {
	conversation := []providerMessage{
		{Role: "system", Content: markdownPatchSystemPrompt(systemPrompt)},
		{Role: "user", Content: buildMarkdownPatchPrompt(plans, opts)},
	}
	if feedback != "" {
		conversation = append(conversation, providerMessage{Role: "user", Content: feedback})
	}
	protected := markdownProtectedValues(plans)
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		text, err := callProviderConversation(ctx, opts.Provider, conversation, rl, maxRetries, opts.Verbose)
		if err != nil {
			return nil, err
		}
		patches, err := parseMarkdownPatchResponse(text, plans)
		if err == nil {
			return patches, nil
		}
		lastErr = err
		if attempt < maxRetries {
			conversation = appendRejectedResponseWithProtection(conversation, text, err, protected)
			if err := waitBeforeParseRetry(ctx, attempt); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

func retryMarkdownPlan(ctx context.Context, plan *preparedMarkdownPlan, systemPrompt string, opts Options, rl *rateLimitState, initialErr error) (string, error) {
	lastErr := initialErr
	maxRetries := opts.effectiveMaxRetries()
	for attempt := 0; attempt <= maxRetries; attempt++ {
		feedback := "A prior patch for this unit failed host validation. Regenerate the complete field map, preserve every required source placeholder in its owning field, retain all source content, and do not add Markdown or resources."
		if len(markdownProtectedValues([]*preparedMarkdownPlan{plan})) == 0 {
			feedback = fmt.Sprintf("A prior patch for this unit failed host validation: %v. Regenerate the complete field map without changing host-owned structure.", lastErr)
		}
		planPatches, err := requestMarkdownPlanGroups(ctx, plan, systemPrompt, opts, rl, feedback)
		if err == nil {
			if rendered, renderErr := renderMarkdownPatch(plan, planPatches); renderErr == nil {
				return rendered, nil
			} else {
				lastErr = renderErr
			}
			invalid := invalidMarkdownPatchFields(plan, planPatches)
			if len(invalid) > 0 {
				for _, field := range invalid {
					focused := &preparedMarkdownPlan{
						key:            plan.key,
						id:             plan.id,
						fields:         []preparedMarkdownField{field},
						fieldByID:      map[string]preparedMarkdownField{field.id: field},
						protectedRules: plan.protectedRules,
					}
					repaired, repairErr := requestMarkdownPatches(ctx, []*preparedMarkdownPlan{focused}, systemPrompt, opts, rl, 0,
						"A prior patch for this field failed host validation. Translate only the supplied field. Keep every placeholder in this field and do not add placeholders or protected names from any other field.")
					if repairErr != nil {
						lastErr = repairErr
						break
					}
					planPatches[field.id] = repaired[focused.id][field.id]
				}
				if rendered, renderErr := renderMarkdownPatch(plan, planPatches); renderErr == nil {
					return rendered, nil
				} else {
					lastErr = renderErr
				}
			}
		} else {
			lastErr = err
		}
		if attempt < maxRetries {
			if err := waitBeforeParseRetry(ctx, attempt); err != nil {
				return "", err
			}
		}
	}
	return "", lastErr
}

func requestMarkdownPlanGroups(ctx context.Context, plan *preparedMarkdownPlan, systemPrompt string, opts Options, rl *rateLimitState, feedback string) (map[string]string, error) {
	groups := markdownPlanGroups(plan, 40)
	patches := make(map[string]string, len(plan.fieldByID))
	for _, focused := range groups {
		response, err := requestMarkdownPatches(ctx, []*preparedMarkdownPlan{focused}, systemPrompt, opts, rl, 0, feedback)
		if err != nil {
			return nil, err
		}
		for id, value := range response[focused.id] {
			patches[id] = value
		}
	}
	return patches, nil
}

func markdownPlanGroups(plan *preparedMarkdownPlan, maxFields int) []*preparedMarkdownPlan {
	var groups [][]preparedMarkdownField
	var group []preparedMarkdownField
	lastContext := ""
	for _, field := range plan.fields {
		if field.fixed != nil {
			continue
		}
		if len(group) >= maxFields && field.context != lastContext {
			groups = append(groups, group)
			group = nil
		}
		group = append(group, field)
		lastContext = field.context
	}
	if len(group) > 0 {
		groups = append(groups, group)
	}
	focused := make([]*preparedMarkdownPlan, 0, len(groups))
	for _, fields := range groups {
		fieldByID := make(map[string]preparedMarkdownField, len(fields))
		for _, field := range fields {
			fieldByID[field.id] = field
		}
		focused = append(focused, &preparedMarkdownPlan{
			key:            plan.key,
			id:             plan.id,
			fields:         fields,
			fieldByID:      fieldByID,
			protectedRules: plan.protectedRules,
		})
	}
	return focused
}

func invalidMarkdownPatchFields(plan *preparedMarkdownPlan, patches map[string]string) []preparedMarkdownField {
	var invalid []preparedMarkdownField
	for _, field := range plan.fields {
		if field.fixed != nil {
			continue
		}
		value, ok := patches[field.id]
		if !ok {
			invalid = append(invalid, field)
			continue
		}
		if _, err := validateMarkdownFieldPatch(plan, field, value); err != nil {
			invalid = append(invalid, field)
		}
	}
	return invalid
}

func renderMarkdownPatch(plan *preparedMarkdownPlan, patches map[string]string) (string, error) {
	if len(patches) != len(plan.fieldByID) {
		return "", fmt.Errorf("got %d field patches, expected %d", len(patches), len(plan.fieldByID))
	}
	restored := make(map[string]string, len(plan.fields))
	contextSource := make(map[string]string)
	contextTarget := make(map[string]string)
	for _, field := range plan.fields {
		value := ""
		if field.fixed != nil {
			value = *field.fixed
		} else {
			var ok bool
			value, ok = patches[field.id]
			if !ok {
				return "", fmt.Errorf("missing Markdown field %q", field.id)
			}
		}
		value, err := validateMarkdownFieldPatch(plan, field, value)
		if err != nil {
			return "", fmt.Errorf("field %q: %w", field.id, err)
		}
		restored[field.id] = value
		contextSource[field.context] += field.source
		contextTarget[field.context] += value
	}
	for context, source := range contextSource {
		if strings.TrimSpace(source) != "" && strings.TrimSpace(contextTarget[context]) == "" {
			return "", fmt.Errorf("Markdown context %q lost all mutable text", context)
		}
	}
	planPatches := make(map[mdformat.FieldID]string, len(restored))
	for id, value := range restored {
		planPatches[mdformat.FieldID(id)] = value
	}
	rendered, err := plan.plan.RenderExact(planPatches)
	if err != nil {
		return "", err
	}
	return string(rendered), nil
}

func validateMarkdownFieldPatch(plan *preparedMarkdownPlan, field preparedMarkdownField, value string) (string, error) {
	if err := validateRawPreservedTerms(value, field.preserved, plan.protectedRules); err != nil {
		return "", err
	}
	restored, err := restorePreservedTerms(value, field.preserved, preservedNamespaceForField(field))
	if err != nil {
		return "", err
	}
	if (field.kind == string(mdformat.FieldLinkLabel) || field.kind == string(mdformat.FieldImageAlt)) &&
		strings.TrimSpace(field.source) != "" && strings.TrimSpace(restored) == "" {
		return "", fmt.Errorf("link or image label is empty")
	}
	if err := validateKVTranslations([]string{field.id}, map[string]string{field.id: field.source}, []string{restored}); err != nil {
		return "", err
	}
	if err := validateTerminology(field.source, restored, field.rules); err != nil {
		return "", err
	}
	if plan.plan != nil {
		if err := plan.plan.ValidateFieldPatch(mdformat.FieldID(field.id), restored); err != nil {
			return "", err
		}
	}
	return restored, nil
}

func preservedNamespaceForField(field preparedMarkdownField) string {
	return preservedTermNamespace([]string{field.id, field.source})
}

func markdownProtectedValues(plans []*preparedMarkdownPlan) []string {
	var groups []map[string]string
	for _, plan := range plans {
		for _, field := range plan.fields {
			groups = append(groups, field.preserved)
		}
	}
	return preservedTermValues(groups...)
}

func parseMarkdownPatchResponse(content string, plans []*preparedMarkdownPlan) (map[string]map[string]string, error) {
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(content)))
	var items []json.RawMessage
	if err := decoder.Decode(&items); err != nil {
		return nil, fmt.Errorf("failed to parse Markdown patch response: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	expected := make(map[string]*preparedMarkdownPlan, len(plans))
	for _, plan := range plans {
		expected[plan.id] = plan
	}
	if len(items) != len(expected) {
		return nil, fmt.Errorf("got %d Markdown patch units, expected %d", len(items), len(expected))
	}
	result := make(map[string]map[string]string, len(items))
	for _, raw := range items {
		id, values, err := decodeMarkdownPatchUnit(raw)
		if err != nil {
			return nil, err
		}
		plan, ok := expected[id]
		if !ok {
			return nil, fmt.Errorf("Markdown patch response contains unknown id %q", id)
		}
		if _, duplicate := result[id]; duplicate {
			return nil, fmt.Errorf("Markdown patch response contains duplicate id %q", id)
		}
		if len(values) != len(plan.fieldByID) {
			return nil, fmt.Errorf("Markdown patch %q has %d fields, expected %d", id, len(values), len(plan.fieldByID))
		}
		for fieldID := range values {
			if _, ok := plan.fieldByID[fieldID]; !ok {
				return nil, fmt.Errorf("Markdown patch %q contains unknown field %q", id, fieldID)
			}
		}
		result[id] = values
	}
	return result, nil
}

func decodeMarkdownPatchUnit(raw json.RawMessage) (string, map[string]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return "", nil, fmt.Errorf("Markdown patch unit is not an object")
	}
	seen := make(map[string]bool)
	var id string
	var values map[string]string
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return "", nil, err
		}
		key, ok := keyToken.(string)
		if !ok || seen[key] {
			return "", nil, fmt.Errorf("Markdown patch unit contains a duplicate or invalid member")
		}
		seen[key] = true
		switch key {
		case "id":
			if err := decoder.Decode(&id); err != nil {
				return "", nil, fmt.Errorf("Markdown patch id is not a string")
			}
		case "values":
			var rawValues json.RawMessage
			if err := decoder.Decode(&rawValues); err != nil {
				return "", nil, err
			}
			values, err = decodeStrictStringMap(rawValues)
			if err != nil {
				return "", nil, err
			}
		default:
			return "", nil, fmt.Errorf("Markdown patch unit contains unknown member %q", key)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return "", nil, err
	}
	if id == "" || values == nil || len(seen) != 2 {
		return "", nil, fmt.Errorf("Markdown patch unit requires exactly id and values")
	}
	return id, values, nil
}

func decodeStrictStringMap(raw json.RawMessage) (map[string]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, fmt.Errorf("Markdown patch values is not an object")
	}
	values := make(map[string]string)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("Markdown patch field id is not a string")
		}
		if _, duplicate := values[key]; duplicate {
			return nil, fmt.Errorf("Markdown patch contains duplicate field %q", key)
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("Markdown patch field %q is not a string", key)
		}
		values[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return values, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("Markdown patch response contains trailing JSON")
		}
		return fmt.Errorf("Markdown patch response contains trailing content: %w", err)
	}
	return nil
}
