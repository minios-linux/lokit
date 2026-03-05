package translate

import (
	"context"
	"fmt"
	"strings"
	"time"

	formatfile "github.com/minios-linux/lokit/internal/format"
)

type KVLangTask struct {
	Lang         string
	LangName     string
	FilePath     string
	File         formatfile.KVFile
	SourceValues map[string]string
}

type KVChunkTranslator interface {
	BuildUserPrompt(keys []string, srcVals map[string]string, opts Options) string
	DefaultChunkSize() int
}

type defaultKVChunkTranslator struct{}

func (defaultKVChunkTranslator) BuildUserPrompt(keys []string, srcVals map[string]string, opts Options) string {
	return buildKVUserPrompt(keys, srcVals, opts.LanguageName)
}

func (defaultKVChunkTranslator) DefaultChunkSize() int { return 0 }

type i18nextChunkTranslator struct{}

func (i18nextChunkTranslator) BuildUserPrompt(keys []string, _ map[string]string, opts Options) string {
	return buildI18NextUserPrompt(keys, opts.LanguageName)
}

func (i18nextChunkTranslator) DefaultChunkSize() int { return 0 }

type markdownChunkTranslator struct{}

func (markdownChunkTranslator) BuildUserPrompt(keys []string, srcVals map[string]string, opts Options) string {
	return buildMarkdownUserPrompt(keys, srcVals, opts.LanguageName)
}

func (markdownChunkTranslator) DefaultChunkSize() int { return 1 }

func DefaultKVChunkTranslator() KVChunkTranslator { return defaultKVChunkTranslator{} }

func I18NextChunkTranslator() KVChunkTranslator { return i18nextChunkTranslator{} }

func MarkdownKVChunkTranslator() KVChunkTranslator { return markdownChunkTranslator{} }

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

		var keysToTranslate []string
		if opts.RetranslateExisting {
			keysToTranslate = task.File.Keys()
		} else {
			keysToTranslate = task.File.UntranslatedKeys()
		}

		keysToTranslate = filterExcludedKeys(keysToTranslate, taskOpts)
		keysToTranslate = filterChangedKeys(keysToTranslate, task.SourceValues, taskOpts)

		if len(keysToTranslate) == 0 {
			continue
		}

		opts.log("Translating %s (%s) — %d keys...", task.Lang, task.LangName, len(keysToTranslate))

		if err := translateKVFile(ctx, task.File, task.SourceValues, keysToTranslate, taskOpts, translator); err != nil {
			if ctx.Err() != nil {
				saveKVFile(task.File, task.FilePath, opts)
				return ctx.Err()
			}
			saveKVFile(task.File, task.FilePath, opts)
			opts.logError("Error translating %s: %v", task.Lang, err)
			failedLangs = append(failedLangs, task.Lang)
			continue
		}

		updateLockFileForKV(keysToTranslate, task.SourceValues, taskOpts)
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
		lang         string
		langName     string
		keys         []string
		filePath     string
		file         formatfile.KVFile
		sourceValues map[string]string
	}

	var tasks []flatTask
	for _, lt := range langTasks {
		taskOpts := opts
		taskOpts.Language = lt.Lang
		taskOpts.LanguageName = lt.LangName

		var keys []string
		if opts.RetranslateExisting {
			keys = lt.File.Keys()
		} else {
			keys = lt.File.UntranslatedKeys()
		}

		keys = filterExcludedKeys(keys, taskOpts)
		keys = filterChangedKeys(keys, lt.SourceValues, taskOpts)

		if len(keys) == 0 {
			continue
		}

		tasks = append(tasks, flatTask{
			lang:         lt.Lang,
			langName:     lt.LangName,
			keys:         keys,
			filePath:     lt.FilePath,
			file:         lt.File,
			sourceValues: lt.SourceValues,
		})
	}

	if len(tasks) == 0 {
		return nil
	}

	err := runParallelGeneric(ctx, tasks, opts.effectiveMaxConcurrent(), opts.RequestDelay, func(ctx context.Context, t flatTask) error {
		taskOpts := opts
		taskOpts.Language = t.lang
		taskOpts.LanguageName = t.langName

		opts.log("Translating %s (%s) — %d keys...", t.lang, t.langName, len(t.keys))
		if err := translateKVFileWithRL(ctx, t.file, t.sourceValues, t.keys, taskOpts, translator, rl); err != nil {
			if ctx.Err() == nil {
				saveKVFile(t.file, t.filePath, opts)
			}
			return err
		}

		updateLockFileForKV(t.keys, t.sourceValues, taskOpts)
		saveKVFile(t.file, t.filePath, opts)
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func translateKVFile(ctx context.Context, file formatfile.KVFile, srcVals map[string]string, keys []string, opts Options, translator KVChunkTranslator) error {
	rl := &rateLimitState{}
	return translateKVFileWithRL(ctx, file, srcVals, keys, opts, translator, rl)
}

func translateKVFileWithRL(ctx context.Context, file formatfile.KVFile, srcVals map[string]string, keys []string, opts Options, translator KVChunkTranslator, rl *rateLimitState) error {
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

	for i, chunk := range chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if opts.Verbose {
			opts.log("  Chunk %d/%d (%d keys)", i+1, len(chunks), len(chunk))
		}

		translations, err := translateKVChunk(ctx, chunk, srcVals, systemPrompt, opts, translator, rl)
		if err != nil {
			return fmt.Errorf("translating chunk %d/%d: %w", i+1, len(chunks), err)
		}

		for j, key := range chunk {
			if j < len(translations) && translations[j] != "" {
				file.Set(key, translations[j])
			}
		}

		done += len(chunk)
		if opts.OnProgress != nil {
			opts.OnProgress(opts.Language, done, len(keys))
		}

		if i < len(chunks)-1 && opts.RequestDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opts.RequestDelay):
			}
		}
	}

	return nil
}

func translateKVChunk(ctx context.Context, keys []string, srcVals map[string]string, systemPrompt string, opts Options, translator KVChunkTranslator, rl *rateLimitState) ([]string, error) {
	userPrompt := translator.BuildUserPrompt(keys, srcVals, opts)
	text, err := callProvider(ctx, opts.Provider, systemPrompt, userPrompt, rl, opts.effectiveMaxRetries(), opts.Verbose)
	if err != nil {
		return nil, err
	}
	return parseTranslations(text, len(keys))
}

func saveKVFile(file formatfile.KVFile, path string, opts Options) {
	if err := file.WriteFile(path); err != nil {
		opts.logError("Error saving %s: %v", path, err)
		return
	}
	total, translated, _ := file.Stats()
	opts.log("Saved %s (%d/%d translated)", path, translated, total)
}

func buildKVUserPrompt(keys []string, srcVals map[string]string, langName string) string {
	var userMsg strings.Builder
	userMsg.WriteString(fmt.Sprintf("Translate these strings to %s:\n\n", langName))
	for i, key := range keys {
		src := key
		if srcVals != nil {
			if v, ok := srcVals[key]; ok && v != "" {
				src = v
			}
		}
		userMsg.WriteString(fmt.Sprintf("%d. %s\n", i+1, escapeForPrompt(src)))
	}
	userMsg.WriteString(fmt.Sprintf("\nReturn a JSON array with exactly %d translated strings.", len(keys)))
	return userMsg.String()
}

func buildI18NextUserPrompt(keys []string, langName string) string {
	var userMsg strings.Builder
	userMsg.WriteString(fmt.Sprintf("Translate these UI strings to %s:\n\n", langName))
	for i, key := range keys {
		userMsg.WriteString(fmt.Sprintf("%d. %s\n", i+1, escapeForPrompt(key)))
	}
	userMsg.WriteString(fmt.Sprintf("\nReturn a JSON array with exactly %d translated strings.", len(keys)))
	return userMsg.String()
}

func buildMarkdownUserPrompt(keys []string, srcVals map[string]string, langName string) string {
	var userMsg strings.Builder
	userMsg.WriteString(fmt.Sprintf("Translate these text segments to %s.\n", langName))
	userMsg.WriteString("For Markdown segments, preserve all formatting, headings, code blocks, and inline markup.\n")
	userMsg.WriteString("Return a JSON array with exactly the same number of translated strings.\n\n")
	for i, key := range keys {
		src := key
		if srcVals != nil {
			if v, ok := srcVals[key]; ok && v != "" {
				src = v
			}
		}
		userMsg.WriteString(fmt.Sprintf("%d. %s\n", i+1, escapeForPrompt(src)))
	}
	userMsg.WriteString(fmt.Sprintf("\nReturn a JSON array with exactly %d translated strings.", len(keys)))
	return userMsg.String()
}
