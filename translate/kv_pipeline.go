package translate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	formatfile "github.com/minios-linux/lokit/internal/format"
)

type KVLangTask struct {
	Lang          string
	LangName      string
	FilePath      string
	File          formatfile.KVFile
	SourceValues  map[string]string
	LockKeyPrefix string
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

func (i18nextChunkTranslator) BuildUserPrompt(keys []string, _ map[string]string, opts Options) string {
	return buildI18NextUserPrompt(keys, opts.SourceLanguageName, opts.LanguageName)
}

func (i18nextChunkTranslator) DefaultChunkSize() int { return 0 }

type markdownChunkTranslator struct{}

var markdownFencedCode = regexp.MustCompile("(?ms)^```[^\n]*\n.*?^```[ \t]*$|^~~~[^\n]*\n.*?^~~~[ \t]*$")

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
	if opts.ParallelMode == ParallelFullParallel {
		return translateKVFullParallel(ctx, langTasks, opts, translator)
	}
	return translateKVSequential(ctx, langTasks, opts, translator)
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

		var keysToTranslate []string
		if opts.RetranslateExisting || opts.ForceTranslate {
			keysToTranslate = task.File.Keys()
		} else {
			keysToTranslate = task.File.UntranslatedKeys()
		}

		keysToTranslate = filterExcludedKeys(keysToTranslate, taskOpts)
		keysToTranslate = filterKeysWithSourceValues(keysToTranslate, task.SourceValues, taskOpts)
		keysToTranslate = filterChangedKeys(keysToTranslate, task.SourceValues, task.LockKeyPrefix, taskOpts)

		if len(keysToTranslate) == 0 {
			continue
		}

		opts.log("Translating %s (%s) — %d keys...", task.Lang, task.LangName, len(keysToTranslate))

		translatedKeys, err := translateKVFile(ctx, task.File, task.SourceValues, keysToTranslate, taskOpts, translator)
		if err != nil {
			if ctx.Err() != nil {
				saveKVFile(task.File, task.FilePath, opts)
				return ctx.Err()
			}
			saveKVFile(task.File, task.FilePath, opts)
			opts.logError("Error translating %s: %v", task.Lang, err)
			failedLangs = append(failedLangs, task.Lang)
			continue
		}

		updateLockFileForKV(translatedKeys, task.SourceValues, task.LockKeyPrefix, taskOpts)
		saveKVFile(task.File, task.FilePath, opts)
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
	}

	var tasks []flatTask
	for _, lt := range langTasks {
		taskOpts := opts
		taskOpts.Language = lt.Lang
		taskOpts.LanguageName = lt.LangName
		taskOpts.SourceLanguageName = taskOpts.resolvedSourceLangName()

		var keys []string
		if opts.RetranslateExisting || opts.ForceTranslate {
			keys = lt.File.Keys()
		} else {
			keys = lt.File.UntranslatedKeys()
		}

		keys = filterExcludedKeys(keys, taskOpts)
		keys = filterKeysWithSourceValues(keys, lt.SourceValues, taskOpts)
		keys = filterChangedKeys(keys, lt.SourceValues, lt.LockKeyPrefix, taskOpts)

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

		opts.log("Translating %s (%s) — %d keys...", t.lang, t.langName, len(t.keys))
		translatedKeys, err := translateKVFileWithRL(ctx, t.file, t.sourceValues, t.keys, taskOpts, translator, rl)
		if err != nil {
			if ctx.Err() == nil {
				saveKVFile(t.file, t.filePath, opts)
			}
			return err
		}

		updateLockFileForKV(translatedKeys, t.sourceValues, t.lockKeyPrefix, taskOpts)
		saveKVFile(t.file, t.filePath, opts)
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
			opts.log("  Chunk %d/%d (%d keys)", i+1, len(chunks), len(chunk))
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
					opts.log("  Retrying chunk %d/%d due to markdown structure mismatch (%s)", i+1, len(chunks), badKey)
				}
				attempt++
				if len(chunk) == 1 {
					translations, err = translateMarkdownSingleRetry(ctx, chunk[0], srcVals, systemPrompt, opts, rl)
				} else {
					translations, err = translateKVChunk(ctx, chunk, srcVals, systemPrompt, opts, translator, rl)
				}
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
	promptVals := srcVals
	codeBlocksByKey := map[string][]string(nil)
	if isMarkdownTranslator(translator) {
		masked := make(map[string]string, len(keys))
		codeBlocksByKey = make(map[string][]string, len(keys))
		for _, key := range keys {
			src := key
			if srcVals != nil {
				if v, ok := srcVals[key]; ok && v != "" {
					src = v
				}
			}
			maskedText, blocks := maskMarkdownCodeBlocks(src)
			masked[key] = maskedText
			codeBlocksByKey[key] = blocks
		}
		promptVals = masked
	}

	userPrompt := translator.BuildUserPrompt(keys, promptVals, opts)
	ids := kvTranslationIDs(keys)
	systemPrompt = identifiedKVSystemPrompt(systemPrompt)
	validationVals := promptVals
	if _, ok := translator.(i18nextChunkTranslator); ok {
		validationVals = nil
	}
	maxRetries := opts.effectiveMaxRetries()
	var translations []string
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		prompt := userPrompt
		if lastErr != nil {
			prompt += fmt.Sprintf("\n\nYour previous response was rejected: %v\nReturn a corrected complete response using the required IDs and JSON shape.", lastErr)
		}
		text, err := callProvider(ctx, opts.Provider, systemPrompt, prompt, rl, maxRetries, opts.Verbose)
		if err != nil {
			return nil, err
		}
		translations, err = parseIdentifiedStringTranslations(text, ids)
		if err == nil {
			err = validateKVTranslations(keys, validationVals, translations)
		}
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if attempt < maxRetries {
			opts.log("  Invalid translation response, retrying (%d/%d): %v", attempt+1, maxRetries, err)
			if err := waitBeforeParseRetry(ctx, attempt); err != nil {
				return nil, err
			}
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	if isMarkdownTranslator(translator) {
		for i, key := range keys {
			if i >= len(translations) {
				break
			}
			translations[i] = restoreMarkdownCodeBlocks(translations[i], codeBlocksByKey[key])
		}
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
	}
	return nil
}

func saveKVFile(file formatfile.KVFile, path string, opts Options) {
	if err := file.WriteFile(path); err != nil {
		opts.logError("Error saving %s: %v", path, err)
		return
	}
	total, translated, _ := file.Stats()
	opts.log("Saved %s (%d/%d translated)", path, translated, total)
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

func buildI18NextUserPrompt(keys []string, sourceLangName, langName string) string {
	var userMsg strings.Builder
	ids := kvTranslationIDs(keys)
	if sourceLangName != "" {
		userMsg.WriteString(fmt.Sprintf("Translate these UI strings from %s to %s:\n\n", sourceLangName, langName))
	} else {
		userMsg.WriteString(fmt.Sprintf("Translate these UI strings to %s:\n\n", langName))
	}
	for i, key := range keys {
		userMsg.WriteString(fmt.Sprintf("ID %s: %s\n", ids[i], escapeForPrompt(key)))
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

	sCode := markdownFencedCode.FindAllStringIndex(s, -1)
	if len(sCode) > 0 {
		dCode := markdownFencedCode.FindAllStringIndex(d, -1)
		if len(dCode) == 0 {
			return false
		}
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

func maskMarkdownCodeBlocks(text string) (string, []string) {
	ranges := markdownFencedCode.FindAllStringIndex(text, -1)
	if len(ranges) == 0 {
		return text, nil
	}

	var out strings.Builder
	blocks := make([]string, 0, len(ranges))
	prev := 0
	for i, r := range ranges {
		out.WriteString(text[prev:r[0]])
		placeholder := fmt.Sprintf("__LOKIT_CODE_BLOCK_%d__", i)
		out.WriteString(placeholder)
		blocks = append(blocks, text[r[0]:r[1]])
		prev = r[1]
	}
	out.WriteString(text[prev:])
	return out.String(), blocks
}

func restoreMarkdownCodeBlocks(text string, blocks []string) string {
	if len(blocks) == 0 {
		return text
	}
	out := text
	for i, block := range blocks {
		placeholder := fmt.Sprintf("__LOKIT_CODE_BLOCK_%d__", i)
		out = strings.ReplaceAll(out, placeholder, block)
	}
	return out
}

func translateMarkdownSingleRetry(ctx context.Context, key string, srcVals map[string]string, systemPrompt string, opts Options, rl *rateLimitState) ([]string, error) {
	src := key
	if srcVals != nil {
		if v, ok := srcVals[key]; ok && v != "" {
			src = v
		}
	}

	maskedSrc, blocks := maskMarkdownCodeBlocks(src)
	id := kvTranslationIDs([]string{key})[0]
	systemPrompt = identifiedKVSystemPrompt(systemPrompt)

	var userMsg strings.Builder
	if opts.SourceLanguageName != "" {
		userMsg.WriteString(fmt.Sprintf("Retry translation from %s to %s for one Markdown segment.\n", opts.SourceLanguageName, opts.LanguageName))
	} else {
		userMsg.WriteString(fmt.Sprintf("Retry translation to %s for one Markdown segment.\n", opts.LanguageName))
	}
	userMsg.WriteString("Previous response was invalid because it changed structure or omitted content.\n")
	userMsg.WriteString("Requirements:\n")
	userMsg.WriteString("- Keep the full segment content (do not summarize or drop lines)\n")
	userMsg.WriteString("- Keep heading markers and heading level exactly\n")
	userMsg.WriteString("- Preserve fenced code blocks exactly as Markdown code blocks\n")
	userMsg.WriteString("- Return ONLY one identified JSON object inside a JSON array\n\n")
	userMsg.WriteString(fmt.Sprintf("ID %s: ", id))
	userMsg.WriteString(escapeForPrompt(maskedSrc))
	userMsg.WriteString(`\n\nReturn [{"id":"` + id + `","translation":"..."}].`)

	text, err := callProvider(ctx, opts.Provider, systemPrompt, userMsg.String(), rl, opts.effectiveMaxRetries(), opts.Verbose)
	if err != nil {
		return nil, err
	}
	translations, err := parseIdentifiedStringTranslations(text, []string{id})
	if err != nil {
		return nil, err
	}
	if len(translations) > 0 {
		translations[0] = restoreMarkdownCodeBlocks(translations[0], blocks)
	}
	return translations, nil
}
