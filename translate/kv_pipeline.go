package translate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/minios-linux/lokit/i18n"
	formatfile "github.com/minios-linux/lokit/internal/format"
)

type KVLangTask struct {
	Lang          string
	LangName      string
	FilePath      string
	File          formatfile.KVFile
	SourceValues  map[string]string
	LockKeyPrefix string
	SourcePath    string
}

type KVChunkTranslator interface {
	BuildUserPrompt(keys []string, srcVals map[string]string, opts Options) string
	DefaultChunkSize() int
}

type defaultKVChunkTranslator struct{}

func (defaultKVChunkTranslator) BuildUserPrompt(keys []string, srcVals map[string]string, opts Options) string {
	return buildKVUserPrompt(keys, srcVals, opts.SourceLanguageName, opts.LanguageName)
}

func (defaultKVChunkTranslator) DefaultChunkSize() int { return 0 }

type i18nextChunkTranslator struct{}

func (i18nextChunkTranslator) BuildUserPrompt(keys []string, srcVals map[string]string, opts Options) string {
	return buildI18NextUserPrompt(keys, srcVals, opts.SourceLanguageName, opts.LanguageName)
}

func (i18nextChunkTranslator) DefaultChunkSize() int { return 0 }

type markdownChunkTranslator struct{}

var markdownFencedCode = regexp.MustCompile("(?ms)^```[^\n]*\n.*?^```[ \t]*$|^~~~[^\n]*\n.*?^~~~[ \t]*$")
var markdownInlineCode = regexp.MustCompile("`+[^`\n]+`+")
var markdownCodePlaceholder = regexp.MustCompile(`<!-- lokit:code-block:[0-9]+ -->`)

func (markdownChunkTranslator) BuildUserPrompt(keys []string, srcVals map[string]string, opts Options) string {
	return buildMarkdownUserPrompt(keys, srcVals, opts.SourceLanguageName, opts.LanguageName)
}

func (markdownChunkTranslator) DefaultChunkSize() int { return 1 }

func DefaultKVChunkTranslator() KVChunkTranslator { return defaultKVChunkTranslator{} }

func I18NextChunkTranslator() KVChunkTranslator { return i18nextChunkTranslator{} }

func MarkdownKVChunkTranslator() KVChunkTranslator { return markdownChunkTranslator{} }

func kvTranslationIDs(keys []string) []string {
	ids := make([]string, len(keys))
	for i, key := range keys {
		sum := sha256.Sum256([]byte(key))
		ids[i] = fmt.Sprintf("kv-%x", sum[:8])
	}
	return ids
}

func identifiedKVSystemPrompt(base string) string {
	return base + `

KEY-VALUE RESPONSE CONTRACT:
- The user message assigns an opaque ID to every source value.
- Return ONLY a JSON array of objects with exactly two fields: "id" and "translation".
- Copy every ID exactly. Do not omit, duplicate, rename, or invent IDs.
- "translation" must be a non-empty string.
- This contract replaces any earlier instruction to return a bare array of strings.`
}

func TranslateAllKV(ctx context.Context, langTasks []KVLangTask, opts Options, translator KVChunkTranslator) error {
	if err := PreflightKVTerminology(langTasks, opts, translator); err != nil {
		return err
	}
	if opts.ParallelMode == ParallelFullParallel {
		return translateKVFullParallel(ctx, langTasks, opts, translator)
	}
	return translateKVSequential(ctx, langTasks, opts, translator)
}

// PreflightKVTerminology resolves and validates deterministic terminology
// rules for every language before any file is mutated.
func PreflightKVTerminology(langTasks []KVLangTask, opts Options, translator KVChunkTranslator) error {
	for _, task := range langTasks {
		taskOpts := opts
		taskOpts.Language = task.Lang
		if task.SourcePath != "" {
			taskOpts.SourcePath = task.SourcePath
		}
		if _, _, err := prepareKVWork(task.File, task.SourceValues, task.LockKeyPrefix, taskOpts, translator, false); err != nil {
			return err
		}
	}
	return nil
}

func translateKVSequential(ctx context.Context, langTasks []KVLangTask, opts Options, translator KVChunkTranslator) error {
	var failedLangs []string
	for _, task := range langTasks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		taskOpts := opts
		taskOpts.Language = task.Lang
		taskOpts.LanguageName = task.LangName
		taskOpts.SourceLanguageName = taskOpts.resolvedSourceLangName()
		if task.SourcePath != "" {
			taskOpts.SourcePath = task.SourcePath
		}

		keysToTranslate, direct, err := prepareKVWork(task.File, task.SourceValues, task.LockKeyPrefix, taskOpts, translator, true)
		if err != nil {
			opts.logError(i18n.T("Error applying terminology for %s: %v"), task.Lang, err)
			failedLangs = append(failedLangs, task.Lang)
			continue
		}
		if len(direct) > 0 {
			opts.log(i18n.T("  Applied %d exact terminology translations"), len(direct))
		}

		if len(keysToTranslate) == 0 {
			if len(direct) > 0 {
				if err := saveKVFile(task.File, task.FilePath, opts); err != nil {
					failedLangs = append(failedLangs, task.Lang)
					continue
				}
				updateLockFileForKV(direct, task.SourceValues, task.LockKeyPrefix, taskOpts)
			}
			continue
		}

		opts.logEvent(LogEventAction, i18n.T("Translating %s (%s) — %d keys..."), task.Lang, task.LangName, len(keysToTranslate))

		translatedKeys, err := translateKVFile(ctx, task.File, task.SourceValues, keysToTranslate, taskOpts, translator)
		if err != nil {
			if ctx.Err() != nil {
				if saveKVFile(task.File, task.FilePath, opts) == nil {
					updateLockFileForKV(append(direct, translatedKeys...), task.SourceValues, task.LockKeyPrefix, taskOpts)
				}
				return ctx.Err()
			}
			if saveKVFile(task.File, task.FilePath, opts) == nil {
				updateLockFileForKV(append(direct, translatedKeys...), task.SourceValues, task.LockKeyPrefix, taskOpts)
			}
			opts.logError(i18n.T("Error translating %s: %v"), task.Lang, err)
			failedLangs = append(failedLangs, task.Lang)
			continue
		}

		if err := saveKVFile(task.File, task.FilePath, opts); err != nil {
			failedLangs = append(failedLangs, task.Lang)
			continue
		}
		updateLockFileForKV(append(direct, translatedKeys...), task.SourceValues, task.LockKeyPrefix, taskOpts)
	}

	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

func translateKVFullParallel(ctx context.Context, langTasks []KVLangTask, opts Options, translator KVChunkTranslator) error {
	rl := &rateLimitState{}

	type flatTask struct {
		lang          string
		langName      string
		keys          []string
		filePath      string
		file          formatfile.KVFile
		sourceValues  map[string]string
		lockKeyPrefix string
		sourcePath    string
	}

	var tasks []flatTask
	for _, lt := range langTasks {
		taskOpts := opts
		taskOpts.Language = lt.Lang
		taskOpts.LanguageName = lt.LangName
		taskOpts.SourceLanguageName = taskOpts.resolvedSourceLangName()
		if lt.SourcePath != "" {
			taskOpts.SourcePath = lt.SourcePath
		}

		keys, direct, err := prepareKVWork(lt.File, lt.SourceValues, lt.LockKeyPrefix, taskOpts, translator, true)
		if err != nil {
			return err
		}
		if len(direct) > 0 {
			if err := saveKVFile(lt.File, lt.FilePath, opts); err != nil {
				return err
			}
			updateLockFileForKV(direct, lt.SourceValues, lt.LockKeyPrefix, taskOpts)
		}

		if len(keys) == 0 {
			continue
		}

		tasks = append(tasks, flatTask{
			lang:          lt.Lang,
			langName:      lt.LangName,
			keys:          keys,
			filePath:      lt.FilePath,
			file:          lt.File,
			sourceValues:  lt.SourceValues,
			lockKeyPrefix: lt.LockKeyPrefix,
			sourcePath:    lt.SourcePath,
		})
	}

	if len(tasks) == 0 {
		return nil
	}

	err := runParallelGeneric(ctx, tasks, opts.effectiveMaxConcurrent(), opts.RequestDelay, func(ctx context.Context, t flatTask) error {
		taskOpts := opts
		taskOpts.Language = t.lang
		taskOpts.LanguageName = t.langName
		taskOpts.SourceLanguageName = taskOpts.resolvedSourceLangName()
		if t.sourcePath != "" {
			taskOpts.SourcePath = t.sourcePath
		}

		opts.logEvent(LogEventAction, i18n.T("Translating %s (%s) — %d keys..."), t.lang, t.langName, len(t.keys))
		translatedKeys, err := translateKVFileWithRL(ctx, t.file, t.sourceValues, t.keys, taskOpts, translator, rl)
		if err != nil {
			if ctx.Err() == nil {
				if saveKVFile(t.file, t.filePath, opts) == nil {
					updateLockFileForKV(translatedKeys, t.sourceValues, t.lockKeyPrefix, taskOpts)
				}
			}
			return err
		}

		if err := saveKVFile(t.file, t.filePath, opts); err != nil {
			return err
		}
		updateLockFileForKV(translatedKeys, t.sourceValues, t.lockKeyPrefix, taskOpts)
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func translateKVFile(ctx context.Context, file formatfile.KVFile, srcVals map[string]string, keys []string, opts Options, translator KVChunkTranslator) ([]string, error) {
	rl := &rateLimitState{}
	return translateKVFileWithRL(ctx, file, srcVals, keys, opts, translator, rl)
}

func translateKVFileWithRL(ctx context.Context, file formatfile.KVFile, srcVals map[string]string, keys []string, opts Options, translator KVChunkTranslator, rl *rateLimitState) ([]string, error) {
	chunkSize := opts.effectiveChunkSize()
	if chunkSize <= 0 {
		chunkSize = translator.DefaultChunkSize()
		if chunkSize <= 0 {
			chunkSize = len(keys)
		}
	}

	systemPrompt := opts.resolvedPrompt()
	chunks := splitStrings(keys, chunkSize)
	done := 0
	translatedKeys := make([]string, 0, len(keys))
	validateMarkdown := isMarkdownTranslator(translator)

	for i, chunk := range chunks {
		select {
		case <-ctx.Done():
			return translatedKeys, ctx.Err()
		default:
		}

		if opts.Verbose {
			opts.logEvent(LogEventProgress, i18n.T("  Chunk %d/%d (%d keys)"), i+1, len(chunks), len(chunk))
		}

		translations, err := translateKVChunk(ctx, chunk, srcVals, systemPrompt, opts, translator, rl)
		if err != nil {
			return translatedKeys, fmt.Errorf("translating chunk %d/%d: %w", i+1, len(chunks), err)
		}

		if validateMarkdown {
			maxAttempts := 3
			attempt := 1
			for {
				badKey, bad := firstInvalidMarkdownTranslation(chunk, translations, srcVals)
				if !bad {
					break
				}
				if attempt >= maxAttempts {
					return translatedKeys, fmt.Errorf("invalid markdown translation for key %q: structure mismatch", badKey)
				}
				if opts.Verbose {
					opts.logEvent(LogEventRetry, i18n.T("  Retrying chunk %d/%d due to markdown structure mismatch (%s)"), i+1, len(chunks), badKey)
				}
				attempt++
				translations, err = translateKVChunk(ctx, chunk, srcVals, systemPrompt, opts, translator, rl)
				if err != nil {
					return translatedKeys, fmt.Errorf("translating chunk %d/%d: %w", i+1, len(chunks), err)
				}
			}
		}

		for j, key := range chunk {
			if j < len(translations) && translations[j] != "" {
				file.Set(key, translations[j])
				translatedKeys = append(translatedKeys, key)
			}
		}

		done += len(chunk)
		if opts.OnProgress != nil {
			opts.OnProgress(opts.Language, done, len(keys))
		}

		if i < len(chunks)-1 && opts.RequestDelay > 0 {
			select {
			case <-ctx.Done():
				return translatedKeys, ctx.Err()
			case <-time.After(opts.RequestDelay):
			}
		}
	}

	return translatedKeys, nil
}

func translateKVChunk(ctx context.Context, keys []string, srcVals map[string]string, systemPrompt string, opts Options, translator KVChunkTranslator, rl *rateLimitState) ([]string, error) {
	if isMarkdownTranslator(translator) {
		return translateMarkdownPatchChunk(ctx, keys, srcVals, systemPrompt, opts, rl)
	}
	promptVals := srcVals

	ids := kvTranslationIDs(keys)
	systemPrompt = identifiedKVSystemPrompt(systemPrompt)
	validationVals := promptVals
	rulesByKey, terminologyPrompt, err := kvChunkTerminology(keys, validationVals, ids, opts)
	if err != nil {
		return nil, err
	}
	providerVals := promptVals
	preservedTermsByKey := make(map[string]map[string]string, len(keys))
	sourceTexts := make([]string, len(keys))
	for i, key := range keys {
		source := key
		if promptVals != nil {
			if value, ok := promptVals[key]; ok && value != "" {
				source = value
			}
		}
		sourceTexts[i] = source
	}
	preserveNamespace := preservedTermNamespace(sourceTexts)
	protectedRules := collectProtectedRules(rulesByKey)
	for i, key := range keys {
		masked, preserved := maskPreservedTerms(sourceTexts[i], protectedRules, preserveNamespace, i)
		if len(preserved) == 0 {
			continue
		}
		if providerVals == nil || len(preservedTermsByKey) == 0 {
			providerVals = make(map[string]string, len(keys))
			for _, sourceKey := range keys {
				value := sourceKey
				if promptVals != nil {
					if sourceValue, ok := promptVals[sourceKey]; ok && sourceValue != "" {
						value = sourceValue
					}
				}
				providerVals[sourceKey] = value
			}
		}
		providerVals[key] = masked
		preservedTermsByKey[key] = preserved
		terminologyPrompt[i] = bindProtectedCompoundTerms(terminologyPrompt[i], []string{masked}, preserved)
	}
	userPrompt := translator.BuildUserPrompt(keys, providerVals, opts)
	if opts.Terminology != nil {
		systemPrompt = terminologySystemPrompt(systemPrompt)
	}
	userPrompt = appendTerminologyPrompt(userPrompt, terminologyPrompt)
	maxRetries := opts.effectiveMaxRetries()
	var translations []string
	var lastErr error
	conversation := []providerMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
	for attempt := 0; attempt <= maxRetries; attempt++ {
		text, err := callProviderConversation(ctx, opts.Provider, conversation, rl, maxRetries, opts.Verbose)
		if err != nil {
			return nil, err
		}
		translations, err = parseIdentifiedStringTranslations(text, ids)
		if err == nil {
			for i, key := range keys {
				err = validateRawPreservedTerms(translations[i], preservedTermsByKey[key], protectedRules)
				if err != nil {
					err = fmt.Errorf("key %q: %w", key, err)
					break
				}
				translations[i], err = restorePreservedTerms(translations[i], preservedTermsByKey[key], preserveNamespace)
				if err != nil {
					err = fmt.Errorf("key %q: %w", key, err)
					break
				}
			}
		}
		if err == nil {
			err = validateKVTranslations(keys, validationVals, translations)
		}
		if err == nil {
			err = validateKVChunkTerminology(keys, validationVals, translations, rulesByKey, opts.SourcePath)
		}
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if attempt < maxRetries {
			protectedGroups := make([]map[string]string, 0, len(preservedTermsByKey))
			for _, values := range preservedTermsByKey {
				protectedGroups = append(protectedGroups, values)
			}
			conversation = appendRejectedResponseWithProtection(conversation, text, err, preservedTermValues(protectedGroups...))
			opts.logEvent(LogEventRetry, i18n.T("  Invalid translation response, retrying (%d/%d): %v"), attempt+1, maxRetries, err)
			if err := waitBeforeParseRetry(ctx, attempt); err != nil {
				return nil, err
			}
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return translations, nil
}

var kvBracePlaceholder = regexp.MustCompile(`\{\{[A-Za-z_][A-Za-z0-9_]*\}\}|\{[A-Za-z_][A-Za-z0-9_]*(?:![rsa])?(?::[^{}]*)?\}`)

func validateKVTranslations(keys []string, srcVals map[string]string, translations []string) error {
	if len(translations) != len(keys) {
		return fmt.Errorf("got %d translations, expected %d", len(translations), len(keys))
	}
	for i, key := range keys {
		source := key
		if value := srcVals[key]; value != "" {
			source = value
		}
		sourcePlaceholders := append(printfPlaceholder.FindAllString(source, -1), kvBracePlaceholder.FindAllString(source, -1)...)
		translatedPlaceholders := append(printfPlaceholder.FindAllString(translations[i], -1), kvBracePlaceholder.FindAllString(translations[i], -1)...)
		sort.Strings(sourcePlaceholders)
		sort.Strings(translatedPlaceholders)
		if !slicesEqual(sourcePlaceholders, translatedPlaceholders) {
			return fmt.Errorf("key %q placeholders changed: expected %v, got %v", key, sourcePlaceholders, translatedPlaceholders)
		}
		if err := validateShellLineContinuations(source, translations[i]); err != nil {
			return fmt.Errorf("key %q: %w", key, err)
		}
	}
	return nil
}

func saveKVFile(file formatfile.KVFile, path string, opts Options) error {
	if err := file.WriteFile(path); err != nil {
		opts.logError(i18n.T("Error saving %s: %v"), path, err)
		return err
	}
	total, translated, _ := file.Stats()
	opts.logEvent(LogEventWrite, i18n.T("Saved %s (%d/%d translated)"), path, translated, total)
	return nil
}

func buildKVUserPrompt(keys []string, srcVals map[string]string, sourceLangName, langName string) string {
	var userMsg strings.Builder
	ids := kvTranslationIDs(keys)
	if sourceLangName != "" {
		userMsg.WriteString(fmt.Sprintf("Translate these strings from %s to %s:\n\n", sourceLangName, langName))
	} else {
		userMsg.WriteString(fmt.Sprintf("Translate these strings to %s:\n\n", langName))
	}
	for i, key := range keys {
		src := key
		if srcVals != nil {
			if v, ok := srcVals[key]; ok && v != "" {
				src = v
			}
		}
		userMsg.WriteString(fmt.Sprintf("ID %s: %s\n", ids[i], escapeForPrompt(src)))
	}
	userMsg.WriteString(fmt.Sprintf("\nReturn a JSON array with exactly %d objects in this form: ", len(keys)))
	userMsg.WriteString(`{"id":"kv-...","translation":"..."}. Preserve every input ID exactly; the objects may be returned in any order.`)
	return userMsg.String()
}

func buildI18NextUserPrompt(keys []string, srcVals map[string]string, sourceLangName, langName string) string {
	var userMsg strings.Builder
	ids := kvTranslationIDs(keys)
	if sourceLangName != "" {
		userMsg.WriteString(fmt.Sprintf("Translate these UI strings from %s to %s:\n\n", sourceLangName, langName))
	} else {
		userMsg.WriteString(fmt.Sprintf("Translate these UI strings to %s:\n\n", langName))
	}
	for i, key := range keys {
		source := key
		if value, ok := srcVals[key]; ok && value != "" {
			source = value
		}
		userMsg.WriteString(fmt.Sprintf("ID %s: %s\n", ids[i], escapeForPrompt(source)))
	}
	userMsg.WriteString(fmt.Sprintf("\nReturn a JSON array with exactly %d objects in this form: ", len(keys)))
	userMsg.WriteString(`{"id":"kv-...","translation":"..."}. Preserve every input ID exactly; the objects may be returned in any order.`)
	return userMsg.String()
}

func buildMarkdownUserPrompt(keys []string, srcVals map[string]string, sourceLangName, langName string) string {
	var userMsg strings.Builder
	ids := kvTranslationIDs(keys)
	if sourceLangName != "" {
		userMsg.WriteString(fmt.Sprintf("Translate these text segments from %s to %s.\n", sourceLangName, langName))
	} else {
		userMsg.WriteString(fmt.Sprintf("Translate these text segments to %s.\n", langName))
	}
	userMsg.WriteString("For Markdown segments, preserve all formatting, headings, code blocks, and inline markup.\n")
	userMsg.WriteString("Preserve every __LOKIT_INLINE_CODE_N__, __LOKIT_PARSER_CODE_BLOCK_N__, and __LOKIT_LINK_DESTINATION_N__ placeholder exactly, in the same order.\n")
	userMsg.WriteString("Do not omit content, do not summarize, and keep the same heading levels (#, ##, ###) and fenced code blocks.\n")
	userMsg.WriteString("Return one identified JSON object for every segment.\n\n")
	for i, key := range keys {
		src := key
		if srcVals != nil {
			if v, ok := srcVals[key]; ok && v != "" {
				src = v
			}
		}
		userMsg.WriteString(fmt.Sprintf("ID %s: %s\n", ids[i], escapeForPrompt(src)))
	}
	userMsg.WriteString(fmt.Sprintf("\nReturn a JSON array with exactly %d objects in this form: ", len(keys)))
	userMsg.WriteString(`{"id":"kv-...","translation":"..."}. Preserve every input ID exactly; the objects may be returned in any order.`)
	return userMsg.String()
}

func isMarkdownTranslator(translator KVChunkTranslator) bool {
	_, ok := translator.(markdownChunkTranslator)
	return ok
}

func isMarkdownTranslationLikelyValid(src, dst string) bool {
	s := strings.TrimSpace(src)
	d := strings.TrimSpace(dst)
	if d == "" {
		return false
	}

	if level, ok := leadingMarkdownHeadingLevel(s); ok {
		dLevel, dOK := leadingMarkdownHeadingLevel(d)
		if !dOK || dLevel != level {
			return false
		}
	}

	sCode := markdownFencedCode.FindAllString(s, -1)
	dCode := markdownFencedCode.FindAllString(d, -1)
	if !slicesEqual(sCode, dCode) {
		return false
	}
	sInline := markdownInlineCode.FindAllString(s, -1)
	dInline := markdownInlineCode.FindAllString(d, -1)
	if !slicesEqual(sInline, dInline) {
		return false
	}
	sPlaceholders := markdownCodePlaceholder.FindAllString(s, -1)
	dPlaceholders := markdownCodePlaceholder.FindAllString(d, -1)
	if !slicesEqual(sPlaceholders, dPlaceholders) {
		return false
	}

	if strings.Contains(s, "\n") && !strings.Contains(d, "\n") && len(s) > 120 {
		return false
	}

	return true
}

func firstInvalidMarkdownTranslation(keys, translations []string, srcVals map[string]string) (string, bool) {
	for i, key := range keys {
		if i >= len(translations) || translations[i] == "" {
			return key, true
		}
		src := key
		if srcVals != nil {
			if v, ok := srcVals[key]; ok && v != "" {
				src = v
			}
		}
		if !isMarkdownTranslationLikelyValid(src, translations[i]) {
			return key, true
		}
	}
	return "", false
}

func leadingMarkdownHeadingLevel(text string) (int, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || trimmed[0] != '#' {
		return 0, false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, false
	}
	if level >= len(trimmed) || trimmed[level] != ' ' {
		return 0, false
	}
	return level, true
}
