package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/minios-linux/lokit/config"
	. "github.com/minios-linux/lokit/i18n"
	"github.com/minios-linux/lokit/internal/format/i18next"
	"github.com/minios-linux/lokit/internal/format/vuei18n"
	"github.com/minios-linux/lokit/translate"
)

// showConfigVueI18nStats shows translation stats for a vue-i18n target.
func showConfigVueI18nStats(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	keyVal(T("Translations"), transDir)

	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}

	srcFile, err := vuei18n.ParseFile(srcPath)
	if err != nil {
		keyVal(T("Source"), colorYellow+T("not found")+colorReset+" ("+srcPath+")")
		return
	}

	srcTotal, _, _ := srcFile.Stats()
	keyVal(T("Source keys"), fmt.Sprintf("%d (%s)", srcTotal, rt.Target.SourceLang))
	langWidth := langColumnWidth(langs)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s%-*s %-22s %5s %5s%s\n",
		colorDim, langWidth+3, T("Lang"), T("Progress"), T("Done"), T("Left"), colorReset)
	fmt.Fprintln(os.Stderr, "  "+colorDim+strings.Repeat("─", 46)+colorReset)

	for _, lang := range langs {
		filePath := rt.ExistingTranslationPath(lang)
		if filePath == "" {
			filePath = rt.TranslationPath(lang)
		}

		file, err := vuei18n.ParseFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("missing"), colorReset)
			continue
		}

		total, translated, _ := file.Stats()
		untranslated := total - translated
		percent := 0
		if srcTotal > 0 {
			percent = translated * 100 / srcTotal
		}

		fmt.Fprintf(os.Stderr, "  %s %s %5d %5d\n",
			langCell(lang, langWidth), progressBar(percent, 16), translated, untranslated)
	}
}

// runInitVueI18n creates or syncs vue-i18n translation files from source.
func runInitVueI18n(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	srcLang := rt.Target.SourceLang
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}

	srcFile, err := vuei18n.ParseFile(srcPath)
	if err != nil {
		logError(T("Cannot read source vue-i18n file %s: %v"), srcPath, err)
		os.Exit(1)
	}

	logInfo(T("Source language (%s): %d keys"), srcLang, len(srcFile.Keys()))

	created, updated := 0, 0
	for _, lang := range langs {
		if lang == srcLang {
			continue
		}

		targetPath := rt.TranslationPath(lang)
		targetFile, err := vuei18n.ParseFile(targetPath)
		if err != nil {
			targetFile = vuei18n.NewTranslationFile(srcFile)
			if err := os.MkdirAll(transDir, 0755); err != nil {
				logError(T("Creating directory %s: %v"), transDir, err)
				continue
			}
			if err := targetFile.WriteFile(targetPath); err != nil {
				logError(T("Creating %s: %v"), targetPath, err)
				continue
			}
			logSuccess(T("Created: %s (%d keys)"), targetPath, len(srcFile.Keys()))
			created++
			continue
		}

		vuei18n.SyncKeys(srcFile, targetFile)
		if err := targetFile.WriteFile(targetPath); err != nil {
			logError(T("Writing %s: %v"), targetPath, err)
			continue
		}
		logSuccess(T("Updated: %s"), targetPath)
		updated++
	}

	logInfo(T("Vue i18n init: %d created, %d updated"), created, updated)
}

func translateVueI18nTarget(ctx context.Context, rt config.ResolvedTarget, prov translate.Provider, a translateArgs, langs []string) error {
	transDir := rt.AbsTranslationsDir()

	logInfo(T("Provider: %s (%s), Model: %s"), prov.Name, prov.ID, prov.Model)
	if a.parallel {
		logInfo(T("Parallel: enabled, max concurrent: %d"), a.maxConcurrent)
	}
	if a.chunkSize > 0 {
		logInfo(T("Chunk size: %d"), a.chunkSize)
	}
	logInfo(T("Translations dir: %s"), transDir)
	logInfo(T("Translating: %s"), strings.Join(langs, ", "))

	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}

	srcFile, err := vuei18n.ParseFile(srcPath)
	if err != nil {
		return fmt.Errorf(T("cannot read source vue-i18n file %s: %w"), srcPath, err)
	}

	srcTotal, _, _ := srcFile.Stats()
	logInfo(T("Source strings: %d"), srcTotal)

	if a.dryRun {
		for _, lang := range langs {
			langName := i18next.ResolveMeta(lang).Name
			targetPath := rt.TranslationPath(lang)
			count := srcTotal
			if !a.retranslate {
				if tf, err := vuei18n.ParseFile(targetPath); err == nil {
					count = len(tf.UntranslatedKeys())
				}
			}
			logInfo(T("%s (%s): %d strings to translate"), lang, langName, count)
		}
		return nil
	}

	var tasks []translate.VueI18nLangTask
	for _, lang := range langs {
		langName := i18next.ResolveMeta(lang).Name
		targetPath := rt.TranslationPath(lang)

		targetFile, err := vuei18n.ParseFile(targetPath)
		if err != nil {
			targetFile = vuei18n.NewTranslationFile(srcFile)
		} else {
			vuei18n.SyncKeys(srcFile, targetFile)
		}

		tasks = append(tasks, translate.VueI18nLangTask{
			Lang:       lang,
			LangName:   langName,
			FilePath:   targetPath,
			File:       targetFile,
			SourceFile: srcFile,
		})
	}

	if len(tasks) == 0 {
		logInfo(T("No vue-i18n files to translate"))
		return nil
	}

	systemPrompt := a.prompt
	if systemPrompt == "" {
		systemPrompt = rt.Target.Prompt
	}

	parallelMode := translate.ParallelSequential
	if a.parallel {
		parallelMode = translate.ParallelFullParallel
	}

	opts := translate.Options{
		Provider:            prov,
		SystemPrompt:        systemPrompt,
		PromptType:          "default",
		RetranslateExisting: a.retranslate,
		ChunkSize:           a.chunkSize,
		RequestDelay:        a.requestDelay,
		Timeout:             a.timeout,
		MaxRetries:          a.maxRetries,
		ParallelMode:        parallelMode,
		MaxConcurrent:       a.maxConcurrent,
		Verbose:             a.verbose,
		LockFile:            a.lockFile,
		LockTarget:          rt.Target.Name,
		ForceTranslate:      a.force,
		OnLog: func(format string, args ...any) {
			logInfo(format, args...)
		},
		OnError: func(format string, args ...any) {
			logError(format, args...)
		},
		OnProgress: func(lang string, done, total int) {
			logInfo(T("  %s: %d/%d strings"), lang, done, total)
		},
	}

	setExclusionOpts(&opts, &rt.Target)

	return translate.TranslateAllVueI18n(ctx, tasks, opts)
}
