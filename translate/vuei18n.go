package translate

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/minios-linux/lokit/internal/format/vuei18n"
)

// VueI18nLangTask holds a single vue-i18n JSON language file.
type VueI18nLangTask struct {
	Lang       string
	LangName   string
	FilePath   string
	File       *vuei18n.File
	SourceFile *vuei18n.File
}

// TranslateAllVueI18n translates vue-i18n JSON files for all language tasks.
func TranslateAllVueI18n(ctx context.Context, langTasks []VueI18nLangTask, opts Options) error {
	if opts.ParallelMode == ParallelFullParallel {
		return translateVueI18nFullParallel(ctx, langTasks, opts)
	}
	return translateVueI18nSequential(ctx, langTasks, opts)
}

func translateVueI18nSequential(ctx context.Context, langTasks []VueI18nLangTask, opts Options) error {
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
		srcVals := task.SourceFile.SourceValues()
		keysToTranslate = filterChangedKeys(keysToTranslate, srcVals, taskOpts)

		if len(keysToTranslate) == 0 {
			continue
		}

		opts.log("Translating %s (%s) — %d keys...", task.Lang, task.LangName, len(keysToTranslate))
		if err := translateVueI18nFile(ctx, task.File, task.SourceFile, keysToTranslate, taskOpts); err != nil {
			if ctx.Err() != nil {
				saveVueI18nFile(task.File, task.FilePath, opts)
				return ctx.Err()
			}
			opts.logError("Error translating %s: %v", task.Lang, err)
			failedLangs = append(failedLangs, task.Lang)
			continue
		}

		updateLockFileForKV(keysToTranslate, srcVals, taskOpts)
		saveVueI18nFile(task.File, task.FilePath, opts)
	}

	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

func translateVueI18nFullParallel(ctx context.Context, langTasks []VueI18nLangTask, opts Options) error {
	rl := &rateLimitState{}

	type flatTask struct {
		lang     string
		langName string
		filePath string
		file     *vuei18n.File
		srcFile  *vuei18n.File
	}

	var tasks []flatTask
	for _, lt := range langTasks {
		var keys []string
		if opts.RetranslateExisting {
			keys = lt.File.Keys()
		} else {
			keys = lt.File.UntranslatedKeys()
		}

		taskOpts := opts
		taskOpts.Language = lt.Lang
		keys = filterExcludedKeys(keys, taskOpts)
		srcVals := lt.SourceFile.SourceValues()
		keys = filterChangedKeys(keys, srcVals, taskOpts)

		if len(keys) > 0 {
			tasks = append(tasks, flatTask{
				lang:     lt.Lang,
				langName: lt.LangName,
				filePath: lt.FilePath,
				file:     lt.File,
				srcFile:  lt.SourceFile,
			})
		}
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
		var keys []string
		if opts.RetranslateExisting {
			keys = t.file.Keys()
		} else {
			keys = t.file.UntranslatedKeys()
		}
		if len(keys) == 0 {
			continue
		}

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

			opts.log("Translating %s (%s) — %d keys...", t.lang, t.langName, len(keys))
			if err := translateVueI18nFileWithRL(ctx, t.file, t.srcFile, keys, taskOpts, rl); err != nil {
				if ctx.Err() == nil {
					opts.logError("Error translating %s: %v", t.lang, err)
					failedMu.Lock()
					failedLangs = append(failedLangs, t.lang)
					failedMu.Unlock()
				}
			} else {
				srcVals := t.srcFile.SourceValues()
				updateLockFileForKV(keys, srcVals, taskOpts)
				saveVueI18nFile(t.file, t.filePath, opts)
			}
		}()
	}

	wg.Wait()
	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

func translateVueI18nFile(ctx context.Context, file *vuei18n.File, srcFile *vuei18n.File, keys []string, opts Options) error {
	rl := &rateLimitState{}
	return translateVueI18nFileWithRL(ctx, file, srcFile, keys, opts, rl)
}

func translateVueI18nFileWithRL(ctx context.Context, file *vuei18n.File, srcFile *vuei18n.File, keys []string, opts Options, rl *rateLimitState) error {
	chunkSize := opts.effectiveChunkSize()
	if chunkSize <= 0 {
		chunkSize = len(keys)
	}

	srcVals := srcFile.SourceValues()
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

		translations, err := translateYAMLChunk(ctx, chunk, srcVals, systemPrompt, opts, rl)
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

func saveVueI18nFile(file *vuei18n.File, path string, opts Options) {
	if err := file.WriteFile(path); err != nil {
		opts.logError("Error saving %s: %v", path, err)
		return
	}
	total, translated, _ := file.Stats()
	opts.log("Saved %s (%d/%d translated)", path, translated, total)
}
