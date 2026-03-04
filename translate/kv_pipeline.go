package translate

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type kvFile interface {
	Keys() []string
	UntranslatedKeys() []string
	Set(key, value string)
	Stats() (total, translated int, pct float64)
	WriteFile(path string) error
}

type kvFileAdapter struct {
	keysFn         func() []string
	untranslatedFn func() []string
	setFn          func(string, string)
	statsFn        func() (int, int, float64)
	writeFn        func(string) error
}

func (f kvFileAdapter) Keys() []string { return f.keysFn() }

func (f kvFileAdapter) UntranslatedKeys() []string { return f.untranslatedFn() }

func (f kvFileAdapter) Set(key, value string) { f.setFn(key, value) }

func (f kvFileAdapter) Stats() (total, translated int, pct float64) { return f.statsFn() }

func (f kvFileAdapter) WriteFile(path string) error { return f.writeFn(path) }

type kvLangTask struct {
	Lang         string
	LangName     string
	FilePath     string
	File         kvFile
	SourceValues map[string]string
}

type kvChunkTranslator interface {
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

func TranslateAllKV(ctx context.Context, langTasks []kvLangTask, opts Options, translator kvChunkTranslator) error {
	if opts.ParallelMode == ParallelFullParallel {
		return translateKVFullParallel(ctx, langTasks, opts, translator)
	}
	return translateKVSequential(ctx, langTasks, opts, translator)
}

func translateKVSequential(ctx context.Context, langTasks []kvLangTask, opts Options, translator kvChunkTranslator) error {
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

func translateKVFullParallel(ctx context.Context, langTasks []kvLangTask, opts Options, translator kvChunkTranslator) error {
	rl := &rateLimitState{}

	type flatTask struct {
		lang         string
		langName     string
		keys         []string
		filePath     string
		file         kvFile
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

	maxConcurrent := opts.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = len(tasks)
	}

	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var failedMu sync.Mutex
	var failedLangs []string

	for _, t := range tasks {
		t := t
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				return
			default:
			}

			taskOpts := opts
			taskOpts.Language = t.lang
			taskOpts.LanguageName = t.langName

			opts.log("Translating %s (%s) — %d keys...", t.lang, t.langName, len(t.keys))
			if err := translateKVFileWithRL(ctx, t.file, t.sourceValues, t.keys, taskOpts, translator, rl); err != nil {
				if ctx.Err() == nil {
					saveKVFile(t.file, t.filePath, opts)
					opts.logError("Error translating %s: %v", t.lang, err)
					failedMu.Lock()
					failedLangs = append(failedLangs, t.lang)
					failedMu.Unlock()
				}
				return
			}

			updateLockFileForKV(t.keys, t.sourceValues, taskOpts)
			saveKVFile(t.file, t.filePath, opts)
		}()
	}

	wg.Wait()
	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

func translateKVFile(ctx context.Context, file kvFile, srcVals map[string]string, keys []string, opts Options, translator kvChunkTranslator) error {
	rl := &rateLimitState{}
	return translateKVFileWithRL(ctx, file, srcVals, keys, opts, translator, rl)
}

func translateKVFileWithRL(ctx context.Context, file kvFile, srcVals map[string]string, keys []string, opts Options, translator kvChunkTranslator, rl *rateLimitState) error {
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

func translateKVChunk(ctx context.Context, keys []string, srcVals map[string]string, systemPrompt string, opts Options, translator kvChunkTranslator, rl *rateLimitState) ([]string, error) {
	userPrompt := translator.BuildUserPrompt(keys, srcVals, opts)
	text, err := callProvider(ctx, opts.Provider, systemPrompt, userPrompt, rl, opts.effectiveMaxRetries(), opts.Verbose)
	if err != nil {
		return nil, err
	}
	return parseTranslations(text, len(keys))
}

func saveKVFile(file kvFile, path string, opts Options) {
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

func toJSONKVTasks(langTasks []JSONLangTask) []kvLangTask {
	tasks := make([]kvLangTask, 0, len(langTasks))
	for _, task := range langTasks {
		f := task.File
		tasks = append(tasks, kvLangTask{
			Lang:     task.Lang,
			LangName: task.LangName,
			FilePath: task.FilePath,
			File: kvFileAdapter{
				keysFn:         f.Keys,
				untranslatedFn: f.UntranslatedKeys,
				setFn: func(key, value string) {
					if _, ok := f.Translations[key]; ok {
						f.Translations[key] = value
					}
				},
				statsFn: func() (total, translated int, pct float64) {
					total, translated, _ = f.Stats()
					if total > 0 {
						pct = float64(translated) / float64(total) * 100
					}
					return
				},
				writeFn: f.WriteFile,
			},
			SourceValues: nil,
		})
	}
	return tasks
}

func toYAMLKVTasks(langTasks []YAMLLangTask) []kvLangTask {
	tasks := make([]kvLangTask, 0, len(langTasks))
	for _, task := range langTasks {
		f := task.File
		tasks = append(tasks, kvLangTask{
			Lang:         task.Lang,
			LangName:     task.LangName,
			FilePath:     task.FilePath,
			File:         kvAdapterFromSettable(f.Keys, f.UntranslatedKeys, f.Set, f.Stats, f.WriteFile),
			SourceValues: task.SourceFile.SourceValues(),
		})
	}
	return tasks
}

func toMarkdownKVTasks(langTasks []MarkdownLangTask) []kvLangTask {
	tasks := make([]kvLangTask, 0, len(langTasks))
	for _, task := range langTasks {
		f := task.File
		tasks = append(tasks, kvLangTask{
			Lang:         task.Lang,
			LangName:     task.LangName,
			FilePath:     task.FilePath,
			File:         kvAdapterFromSettable(f.Keys, f.UntranslatedKeys, f.Set, f.Stats, f.WriteFile),
			SourceValues: task.SourceFile.SourceValues(),
		})
	}
	return tasks
}

func toPropertiesKVTasks(langTasks []PropertiesLangTask) []kvLangTask {
	tasks := make([]kvLangTask, 0, len(langTasks))
	for _, task := range langTasks {
		f := task.File
		tasks = append(tasks, kvLangTask{
			Lang:         task.Lang,
			LangName:     task.LangName,
			FilePath:     task.FilePath,
			File:         kvAdapterFromSettable(f.Keys, f.UntranslatedKeys, f.Set, f.Stats, f.WriteFile),
			SourceValues: task.SourceFile.SourceValues(),
		})
	}
	return tasks
}

func toARBKVTasks(langTasks []ARBLangTask) []kvLangTask {
	tasks := make([]kvLangTask, 0, len(langTasks))
	for _, task := range langTasks {
		f := task.File
		tasks = append(tasks, kvLangTask{
			Lang:         task.Lang,
			LangName:     task.LangName,
			FilePath:     task.FilePath,
			File:         kvAdapterFromSettable(f.Keys, f.UntranslatedKeys, f.Set, f.Stats, f.WriteFile),
			SourceValues: task.SourceFile.SourceValues(),
		})
	}
	return tasks
}

func toVueI18nKVTasks(langTasks []VueI18nLangTask) []kvLangTask {
	tasks := make([]kvLangTask, 0, len(langTasks))
	for _, task := range langTasks {
		f := task.File
		tasks = append(tasks, kvLangTask{
			Lang:         task.Lang,
			LangName:     task.LangName,
			FilePath:     task.FilePath,
			File:         kvAdapterFromSettable(f.Keys, f.UntranslatedKeys, f.Set, f.Stats, f.WriteFile),
			SourceValues: task.SourceFile.SourceValues(),
		})
	}
	return tasks
}

func kvAdapterFromSettable(
	keysFn func() []string,
	untranslatedFn func() []string,
	setFn func(string, string) bool,
	statsFn func() (int, int, float64),
	writeFn func(string) error,
) kvFileAdapter {
	return kvFileAdapter{
		keysFn:         keysFn,
		untranslatedFn: untranslatedFn,
		setFn: func(key, value string) {
			setFn(key, value)
		},
		statsFn: statsFn,
		writeFn: writeFn,
	}
}
