package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minios-linux/lokit/config"
	. "github.com/minios-linux/lokit/i18n"
	arbfile "github.com/minios-linux/lokit/internal/format/arb"
	"github.com/minios-linux/lokit/internal/format/i18next"
	mdfile "github.com/minios-linux/lokit/internal/format/markdown"
	propfile "github.com/minios-linux/lokit/internal/format/properties"
	yamlfile "github.com/minios-linux/lokit/internal/format/yaml"
	"github.com/minios-linux/lokit/translate"
)

func showConfigYAMLStats(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	keyVal(T("Translations"), transDir)

	srcLang := rt.Target.SourceLang
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		keyVal(T("Source"), colorYellow+T("not found")+colorReset+" ("+rt.SourcePath()+")")
		return
	}

	srcFile, err := yamlfile.ParseFile(srcPath)
	if err != nil {
		keyVal(T("Source"), colorYellow+fmt.Sprintf(T("parse error: %v"), err)+colorReset)
		return
	}
	srcTotal, _, _ := srcFile.Stats()
	keyVal(T("Source keys"), fmt.Sprintf("%d (%s)", srcTotal, srcLang))
	langWidth := langColumnWidth(langs)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s%-*s %-22s %5s %5s%s\n",
		colorDim, langWidth+3, T("Lang"), T("Progress"), T("Done"), T("Left"), colorReset)
	fmt.Fprintln(os.Stderr, "  "+colorDim+strings.Repeat("─", 46)+colorReset)

	for _, lang := range langs {
		filePath := rt.ExistingTranslationPath(lang)
		if filePath == "" {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("missing"), colorReset)
			continue
		}

		file, err := yamlfile.ParseFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("parse error"), colorReset)
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

func runInitYAML(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()

	srcLang := rt.Target.SourceLang
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		logError(T("Cannot find source YAML file for language %q in %s"), srcLang, transDir)
		logInfo(T("Expected: %s"), rt.SourcePath())
		os.Exit(1)
	}

	srcFile, err := yamlfile.ParseFile(srcPath)
	if err != nil {
		logError(T("Cannot read source YAML file %s: %v"), srcPath, err)
		os.Exit(1)
	}

	logInfo(T("Source language (%s): %d keys"), srcLang, len(srcFile.Keys()))

	created, updated := 0, 0

	for _, lang := range langs {
		if lang == srcLang {
			continue
		}

		targetPath := rt.ExistingTranslationPath(lang)

		if targetPath == "" {
			targetPath = rt.TranslationPath(lang)
			newFile := yamlfile.NewTranslationFile(srcFile, lang)
			if err := os.MkdirAll(transDir, 0o755); err != nil {
				logError(T("Creating directory %s: %v"), transDir, err)
				continue
			}
			if err := newFile.WriteFile(targetPath); err != nil {
				logError(T("Creating %s: %v"), targetPath, err)
				continue
			}
			logSuccess(T("Created: %s (%d keys)"), targetPath, len(srcFile.Keys()))
			created++
			continue
		}

		targetFile, err := yamlfile.ParseFile(targetPath)
		if err != nil {
			logError(T("Reading %s: %v"), targetPath, err)
			continue
		}

		yamlfile.SyncKeys(srcFile, targetFile)
		if err := targetFile.WriteFile(targetPath); err != nil {
			logError(T("Writing %s: %v"), targetPath, err)
			continue
		}
		logSuccess(T("Updated: %s"), targetPath)
		updated++
	}

	logInfo(T("YAML init: %d created, %d updated"), created, updated)
}

func translateYAMLTarget(ctx context.Context, rt config.ResolvedTarget, prov translate.Provider, a translateArgs, langs []string) error {
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

	srcLang := rt.Target.SourceLang
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		return fmt.Errorf(T("cannot find source YAML file for language %q in %s"), srcLang, transDir)
	}

	srcFile, err := yamlfile.ParseFile(srcPath)
	if err != nil {
		return fmt.Errorf(T("cannot read source YAML %s: %w"), srcPath, err)
	}
	srcTotal, _, _ := srcFile.Stats()
	logInfo(T("Source strings: %d"), srcTotal)

	if a.dryRun {
		for _, lang := range langs {
			filePath := rt.ExistingTranslationPath(lang)
			langName := i18next.ResolveMeta(lang).Name

			if filePath == "" {
				logInfo(T("%s (%s): %d strings to translate (file will be auto-created)"), lang, langName, srcTotal)
				continue
			}

			file, err := yamlfile.ParseFile(filePath)
			if err != nil {
				logInfo(T("%s (%s): %d strings to translate"), lang, langName, srcTotal)
				continue
			}

			count := len(file.UntranslatedKeys())
			if a.retranslate {
				count = srcTotal
			}
			logInfo(T("%s (%s): %d strings to translate"), lang, langName, count)
		}
		return nil
	}

	var tasks []translate.YAMLLangTask
	for _, lang := range langs {
		langName := i18next.ResolveMeta(lang).Name

		filePath := rt.ExistingTranslationPath(lang)

		var targetFile *yamlfile.File
		if filePath == "" {
			filePath = rt.TranslationPath(lang)
			targetFile = yamlfile.NewTranslationFile(srcFile, lang)
		} else {
			targetFile, err = yamlfile.ParseFile(filePath)
			if err != nil {
				logError(T("Reading %s: %v"), filePath, err)
				continue
			}
			yamlfile.SyncKeys(srcFile, targetFile)
		}

		tasks = append(tasks, translate.YAMLLangTask{
			Lang:       lang,
			LangName:   langName,
			FilePath:   filePath,
			File:       targetFile,
			SourceFile: srcFile,
		})
	}

	if len(tasks) == 0 {
		logInfo(T("No YAML files to translate"))
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
		OnLog:               func(format string, args ...any) { logInfo(format, args...) },
		OnError:             func(format string, args ...any) { logError(format, args...) },
		OnProgress: func(lang string, done, total int) {
			logInfo(T("  %s: %d/%d strings"), lang, done, total)
		},
	}

	setExclusionOpts(&opts, &rt.Target)

	return translate.TranslateAllYAML(ctx, tasks, opts)
}

func showConfigMarkdownStats(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	keyVal(T("Translations"), transDir)

	srcLang := rt.Target.SourceLang
	srcDir := filepath.Join(transDir, srcLang)
	srcFiles, _ := filepath.Glob(filepath.Join(srcDir, "*.md"))
	if len(srcFiles) == 0 {
		keyVal(T("Source"), colorYellow+T("not found")+colorReset+" ("+srcDir+"/*.md)")
		return
	}

	srcTotal := 0
	for _, p := range srcFiles {
		f, err := mdfile.ParseFile(p)
		if err == nil {
			t, _, _ := f.Stats()
			srcTotal += t
		}
	}
	keyVal(T("Source segments"), fmt.Sprintf("%d (%s, %d files)", srcTotal, srcLang, len(srcFiles)))
	langWidth := langColumnWidth(langs)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s%-*s %-22s %5s %5s%s\n",
		colorDim, langWidth+3, T("Lang"), T("Progress"), T("Done"), T("Left"), colorReset)
	fmt.Fprintln(os.Stderr, "  "+colorDim+strings.Repeat("─", 46)+colorReset)

	for _, lang := range langs {
		langDir := filepath.Join(transDir, lang)
		files, _ := filepath.Glob(filepath.Join(langDir, "*.md"))
		if len(files) == 0 {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("missing"), colorReset)
			continue
		}

		translated := 0
		for _, p := range files {
			f, err := mdfile.ParseFile(p)
			if err != nil {
				continue
			}
			_, tr, _ := f.Stats()
			translated += tr
		}

		untranslated := srcTotal - translated
		percent := 0
		if srcTotal > 0 {
			percent = translated * 100 / srcTotal
		}
		fmt.Fprintf(os.Stderr, "  %s %s %5d %5d\n",
			langCell(lang, langWidth), progressBar(percent, 16), translated, untranslated)
	}
}

func runInitMarkdown(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	srcLang := rt.Target.SourceLang
	srcDir := filepath.Join(transDir, srcLang)

	srcFiles, _ := filepath.Glob(filepath.Join(srcDir, "*.md"))
	if len(srcFiles) == 0 {
		logError(T("Cannot find source Markdown files in %s"), srcDir)
		logInfo(T("Expected: %s/*.md"), srcDir)
		os.Exit(1)
	}

	logInfo(T("Source language (%s): %d files"), srcLang, len(srcFiles))

	created, updated := 0, 0

	for _, lang := range langs {
		if lang == srcLang {
			continue
		}

		langDir := filepath.Join(transDir, lang)
		if err := os.MkdirAll(langDir, 0o755); err != nil {
			logError(T("Creating directory %s: %v"), langDir, err)
			continue
		}

		for _, srcPath := range srcFiles {
			base := filepath.Base(srcPath)
			targetPath := filepath.Join(langDir, base)

			srcFile, err := mdfile.ParseFile(srcPath)
			if err != nil {
				logError(T("Cannot read source Markdown file %s: %v"), srcPath, err)
				continue
			}

			if _, err := os.Stat(targetPath); os.IsNotExist(err) {
				newFile := mdfile.NewTranslationFile(srcFile)
				if err := newFile.WriteFile(targetPath); err != nil {
					logError(T("Creating %s: %v"), targetPath, err)
					continue
				}
				logSuccess(T("Created: %s (%d segments)"), targetPath, len(srcFile.Keys()))
				created++
			} else {
				targetFile, err := mdfile.ParseFile(targetPath)
				if err != nil {
					logError(T("Reading %s: %v"), targetPath, err)
					continue
				}
				mdfile.SyncKeys(srcFile, targetFile)
				if err := targetFile.WriteFile(targetPath); err != nil {
					logError(T("Writing %s: %v"), targetPath, err)
					continue
				}
				logSuccess(T("Updated: %s"), targetPath)
				updated++
			}
		}
	}

	logInfo(T("Markdown init: %d created, %d updated"), created, updated)
}

func translateMarkdownTarget(ctx context.Context, rt config.ResolvedTarget, prov translate.Provider, a translateArgs, langs []string) error {
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

	srcLang := rt.Target.SourceLang
	srcDir := filepath.Join(transDir, srcLang)
	srcFiles, _ := filepath.Glob(filepath.Join(srcDir, "*.md"))
	if len(srcFiles) == 0 {
		return fmt.Errorf(T("cannot find source Markdown files in %s"), srcDir)
	}

	totalSrcSegs := 0
	for _, p := range srcFiles {
		if f, err := mdfile.ParseFile(p); err == nil {
			t, _, _ := f.Stats()
			totalSrcSegs += t
		}
	}
	logInfo(T("Source segments: %d (%d files)"), totalSrcSegs, len(srcFiles))

	if a.dryRun {
		for _, lang := range langs {
			langName := i18next.ResolveMeta(lang).Name
			langDir := filepath.Join(transDir, lang)
			count := 0
			for _, srcPath := range srcFiles {
				base := filepath.Base(srcPath)
				targetPath := filepath.Join(langDir, base)
				srcFile, err := mdfile.ParseFile(srcPath)
				if err != nil {
					continue
				}
				if a.retranslate {
					count += len(srcFile.Keys())
					continue
				}
				if _, err := os.Stat(targetPath); os.IsNotExist(err) {
					count += len(srcFile.Keys())
					continue
				}
				tf, err := mdfile.ParseFile(targetPath)
				if err != nil {
					count += len(srcFile.Keys())
					continue
				}
				count += len(tf.UntranslatedKeys())
			}
			logInfo(T("%s (%s): %d segments to translate"), lang, langName, count)
		}
		return nil
	}

	var tasks []translate.MarkdownLangTask
	for _, lang := range langs {
		langName := i18next.ResolveMeta(lang).Name
		langDir := filepath.Join(transDir, lang)
		if err := os.MkdirAll(langDir, 0o755); err != nil {
			logError(T("Creating directory %s: %v"), langDir, err)
			continue
		}

		for _, srcPath := range srcFiles {
			base := filepath.Base(srcPath)
			targetPath := filepath.Join(langDir, base)

			srcFile, err := mdfile.ParseFile(srcPath)
			if err != nil {
				logError(T("Reading %s: %v"), srcPath, err)
				continue
			}

			var targetFile *mdfile.File
			if _, err := os.Stat(targetPath); os.IsNotExist(err) {
				targetFile = mdfile.NewTranslationFile(srcFile)
			} else {
				targetFile, err = mdfile.ParseFile(targetPath)
				if err != nil {
					logError(T("Reading %s: %v"), targetPath, err)
					continue
				}
				mdfile.SyncKeys(srcFile, targetFile)
			}

			tasks = append(tasks, translate.MarkdownLangTask{Lang: lang, LangName: langName, FilePath: targetPath, File: targetFile, SourceFile: srcFile})
		}
	}

	if len(tasks) == 0 {
		logInfo(T("No Markdown files to translate"))
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
		OnLog:               func(format string, args ...any) { logInfo(format, args...) },
		OnError:             func(format string, args ...any) { logError(format, args...) },
		OnProgress: func(lang string, done, total int) {
			logInfo(T("  %s: %d/%d segments"), lang, done, total)
		},
	}

	setExclusionOpts(&opts, &rt.Target)

	return translate.TranslateAllMarkdown(ctx, tasks, opts)
}

func showConfigPropertiesStats(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	keyVal(T("Translations"), transDir)

	srcLang := rt.Target.SourceLang
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		keyVal(T("Source"), colorYellow+T("not found")+colorReset+" ("+srcPath+")")
		return
	}

	srcFile, err := propfile.ParseFile(srcPath)
	if err != nil {
		keyVal(T("Source"), colorYellow+fmt.Sprintf(T("parse error: %v"), err)+colorReset)
		return
	}
	srcTotal, _, _ := srcFile.Stats()
	keyVal(T("Source keys"), fmt.Sprintf("%d (%s)", srcTotal, srcLang))
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
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("missing"), colorReset)
			continue
		}

		file, err := propfile.ParseFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("parse error"), colorReset)
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

func runInitProperties(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	srcLang := rt.Target.SourceLang
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}

	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		logError(T("Cannot find source .properties file: %s"), srcPath)
		os.Exit(1)
	}

	srcFile, err := propfile.ParseFile(srcPath)
	if err != nil {
		logError(T("Cannot read source .properties file %s: %v"), srcPath, err)
		os.Exit(1)
	}

	logInfo(T("Source language (%s): %d keys"), srcLang, len(srcFile.Keys()))

	created, updated := 0, 0

	for _, lang := range langs {
		if lang == srcLang {
			continue
		}
		targetPath := rt.TranslationPath(lang)

		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			newFile := propfile.NewTranslationFile(srcFile)
			if err := os.MkdirAll(transDir, 0o755); err != nil {
				logError(T("Creating directory %s: %v"), transDir, err)
				continue
			}
			if err := newFile.WriteFile(targetPath); err != nil {
				logError(T("Creating %s: %v"), targetPath, err)
				continue
			}
			logSuccess(T("Created: %s (%d keys)"), targetPath, len(srcFile.Keys()))
			created++
		} else {
			targetFile, err := propfile.ParseFile(targetPath)
			if err != nil {
				logError(T("Reading %s: %v"), targetPath, err)
				continue
			}
			propfile.SyncKeys(srcFile, targetFile)
			if err := targetFile.WriteFile(targetPath); err != nil {
				logError(T("Writing %s: %v"), targetPath, err)
				continue
			}
			logSuccess(T("Updated: %s"), targetPath)
			updated++
		}
	}

	logInfo(T("Properties init: %d created, %d updated"), created, updated)
}

func translatePropertiesTarget(ctx context.Context, rt config.ResolvedTarget, prov translate.Provider, a translateArgs, langs []string) error {
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
	srcFile, err := propfile.ParseFile(srcPath)
	if err != nil {
		return fmt.Errorf(T("cannot read source .properties file %s: %w"), srcPath, err)
	}
	srcTotal, _, _ := srcFile.Stats()
	logInfo(T("Source strings: %d"), srcTotal)

	if a.dryRun {
		for _, lang := range langs {
			langName := i18next.ResolveMeta(lang).Name
			targetPath := rt.TranslationPath(lang)
			count := srcTotal
			if !a.retranslate {
				if _, err := os.Stat(targetPath); err == nil {
					if tf, err := propfile.ParseFile(targetPath); err == nil {
						count = len(tf.UntranslatedKeys())
					}
				}
			}
			logInfo(T("%s (%s): %d strings to translate"), lang, langName, count)
		}
		return nil
	}

	var tasks []translate.PropertiesLangTask
	for _, lang := range langs {
		langName := i18next.ResolveMeta(lang).Name
		targetPath := rt.TranslationPath(lang)

		var targetFile *propfile.File
		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			targetFile = propfile.NewTranslationFile(srcFile)
		} else {
			targetFile, err = propfile.ParseFile(targetPath)
			if err != nil {
				logError(T("Reading %s: %v"), targetPath, err)
				continue
			}
			propfile.SyncKeys(srcFile, targetFile)
		}

		tasks = append(tasks, translate.PropertiesLangTask{Lang: lang, LangName: langName, FilePath: targetPath, File: targetFile, SourceFile: srcFile})
	}

	if len(tasks) == 0 {
		logInfo(T("No .properties files to translate"))
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
		OnLog:               func(format string, args ...any) { logInfo(format, args...) },
		OnError:             func(format string, args ...any) { logError(format, args...) },
		OnProgress: func(lang string, done, total int) {
			logInfo(T("  %s: %d/%d strings"), lang, done, total)
		},
	}

	setExclusionOpts(&opts, &rt.Target)

	return translate.TranslateAllProperties(ctx, tasks, opts)
}

func showConfigFlutterStats(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	keyVal(T("Translations"), transDir)

	srcLang := rt.Target.SourceLang
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		keyVal(T("Source"), colorYellow+T("not found")+colorReset+" ("+srcPath+")")
		return
	}

	srcFile, err := arbfile.ParseFile(srcPath)
	if err != nil {
		keyVal(T("Source"), colorYellow+fmt.Sprintf(T("parse error: %v"), err)+colorReset)
		return
	}
	srcTotal, _, _ := srcFile.Stats()
	keyVal(T("Source keys"), fmt.Sprintf("%d (%s)", srcTotal, srcLang))
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
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("missing"), colorReset)
			continue
		}

		file, err := arbfile.ParseFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("parse error"), colorReset)
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

func runInitFlutter(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	srcLang := rt.Target.SourceLang
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}

	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		logError(T("Cannot find source ARB file: %s"), srcPath)
		os.Exit(1)
	}

	srcFile, err := arbfile.ParseFile(srcPath)
	if err != nil {
		logError(T("Cannot read source ARB file %s: %v"), srcPath, err)
		os.Exit(1)
	}

	logInfo(T("Source language (%s): %d keys"), srcLang, len(srcFile.Keys()))

	created, updated := 0, 0

	for _, lang := range langs {
		if lang == srcLang {
			continue
		}
		targetPath := rt.TranslationPath(lang)

		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			newFile := arbfile.NewTranslationFile(srcFile, lang)
			if err := os.MkdirAll(transDir, 0o755); err != nil {
				logError(T("Creating directory %s: %v"), transDir, err)
				continue
			}
			if err := newFile.WriteFile(targetPath); err != nil {
				logError(T("Creating %s: %v"), targetPath, err)
				continue
			}
			logSuccess(T("Created: %s (%d keys)"), targetPath, len(srcFile.Keys()))
			created++
		} else {
			targetFile, err := arbfile.ParseFile(targetPath)
			if err != nil {
				logError(T("Reading %s: %v"), targetPath, err)
				continue
			}
			arbfile.SyncKeys(srcFile, targetFile)
			if err := targetFile.WriteFile(targetPath); err != nil {
				logError(T("Writing %s: %v"), targetPath, err)
				continue
			}
			logSuccess(T("Updated: %s"), targetPath)
			updated++
		}
	}

	logInfo(T("Flutter ARB init: %d created, %d updated"), created, updated)
}

func translateFlutterTarget(ctx context.Context, rt config.ResolvedTarget, prov translate.Provider, a translateArgs, langs []string) error {
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
	srcFile, err := arbfile.ParseFile(srcPath)
	if err != nil {
		return fmt.Errorf(T("cannot read source ARB file %s: %w"), srcPath, err)
	}
	srcTotal, _, _ := srcFile.Stats()
	logInfo(T("Source strings: %d"), srcTotal)

	if a.dryRun {
		for _, lang := range langs {
			langName := i18next.ResolveMeta(lang).Name
			targetPath := rt.TranslationPath(lang)
			count := srcTotal
			if !a.retranslate {
				if _, err := os.Stat(targetPath); err == nil {
					if tf, err := arbfile.ParseFile(targetPath); err == nil {
						count = len(tf.UntranslatedKeys())
					}
				}
			}
			logInfo(T("%s (%s): %d strings to translate"), lang, langName, count)
		}
		return nil
	}

	var tasks []translate.ARBLangTask
	for _, lang := range langs {
		langName := i18next.ResolveMeta(lang).Name
		targetPath := rt.TranslationPath(lang)

		var targetFile *arbfile.File
		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			targetFile = arbfile.NewTranslationFile(srcFile, lang)
		} else {
			targetFile, err = arbfile.ParseFile(targetPath)
			if err != nil {
				logError(T("Reading %s: %v"), targetPath, err)
				continue
			}
			arbfile.SyncKeys(srcFile, targetFile)
		}

		tasks = append(tasks, translate.ARBLangTask{Lang: lang, LangName: langName, FilePath: targetPath, File: targetFile, SourceFile: srcFile})
	}

	if len(tasks) == 0 {
		logInfo(T("No ARB files to translate"))
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
		OnLog:               func(format string, args ...any) { logInfo(format, args...) },
		OnError:             func(format string, args ...any) { logError(format, args...) },
		OnProgress: func(lang string, done, total int) {
			logInfo(T("  %s: %d/%d strings"), lang, done, total)
		},
	}

	setExclusionOpts(&opts, &rt.Target)

	return translate.TranslateAllARB(ctx, tasks, opts)
}
