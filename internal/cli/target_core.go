package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/minios-linux/lokit/config"
	. "github.com/minios-linux/lokit/i18n"
	"github.com/minios-linux/lokit/internal/format/android"
	"github.com/minios-linux/lokit/internal/format/i18next"
	po "github.com/minios-linux/lokit/internal/format/po"
	"github.com/minios-linux/lokit/merge"
	"github.com/minios-linux/lokit/translate"
)

// translateGettextTarget translates a single gettext PO target.
// Automatically runs extraction + PO update (equivalent to `lokit init`)
// before translating, so that new strings are always picked up.
func translateGettextTarget(ctx context.Context, rt config.ResolvedTarget, prov translate.Provider, a translateArgs, langs []string) error {
	poDir := rt.AbsPODir()
	potPath := rt.AbsPOTFile()

	// Build project config (used for both auto-init and POT creation)
	proj := &config.Project{
		Root:        rt.AbsRoot,
		Name:        rt.Target.Name,
		Version:     "0.0.0",
		PODir:       poDir,
		POTFile:     potPath,
		POStructure: config.POStructureFlat,
		Languages:   langs,
		Keywords:    rt.Target.Keywords,
		SourceLang:  rt.Target.SourceLang,
		BugsEmail:   "support@minios.dev",
	}
	if len(rt.Target.Sources) > 0 {
		for _, src := range rt.Target.Sources {
			proj.SourceDirs = append(proj.SourceDirs, filepath.Join(rt.AbsRoot, src))
		}
	} else {
		proj.SourceDirs = []string{rt.AbsRoot}
	}

	// Auto-init: extract strings and update PO files before translating.
	// This ensures new/changed strings are always picked up without
	// requiring a separate `lokit init` run.
	logInfo(T("Extracting strings and updating PO files..."))
	if err := doExtract(proj); err != nil {
		logWarning(T("Extraction failed: %v"), err)
		logInfo(T("Continuing with existing PO files"))
	} else {
		// Merge POT into existing PO files
		potPO, err := po.ParseFile(proj.POTFile)
		if err == nil {
			for _, lang := range langs {
				poPath := rt.POPath(lang)
				if !fileExists(poPath) {
					continue // will be created below
				}
				existingPO, err := po.ParseFile(poPath)
				if err != nil {
					continue
				}
				merged := merge.Merge(existingPO, potPO)
				if err := merged.WriteFile(poPath); err != nil {
					logError(T("Updating %s: %v"), poPath, err)
				}
			}
		}
	}

	logInfo(T("Provider: %s (%s), Model: %s"), prov.Name, prov.ID, prov.Model)
	if a.parallel {
		logInfo(T("Parallel: enabled, max concurrent: %d"), a.maxConcurrent)
	}
	if a.chunkSize > 0 {
		logInfo(T("Chunk size: %d"), a.chunkSize)
	}
	logInfo(T("PO dir: %s"), poDir)
	logInfo(T("Translating: %s"), strings.Join(langs, ", "))

	if a.dryRun {
		for _, lang := range langs {
			poPath := rt.POPath(lang)
			if poFile, err := po.ParseFile(poPath); err == nil {
				untranslated := poFile.UntranslatedEntries()
				langName := po.LangNameNative(lang)
				logInfo(T("%s (%s): %d strings to translate"), lang, langName, len(untranslated))
			} else {
				logInfo(T("%s: PO file not found, will be created"), lang)
			}
		}
		return nil
	}

	// Determine parallel mode
	parallelMode := translate.ParallelSequential
	if a.parallel {
		parallelMode = translate.ParallelFullParallel
	}

	opts := translate.Options{
		Provider:            prov,
		ChunkSize:           a.chunkSize,
		ParallelMode:        parallelMode,
		MaxConcurrent:       a.maxConcurrent,
		RequestDelay:        a.requestDelay,
		Timeout:             a.timeout,
		MaxRetries:          a.maxRetries,
		RetranslateExisting: a.retranslate,
		TranslateFuzzy:      a.fuzzy,
		SystemPrompt:        a.prompt,
		PromptType:          "default", // Use default gettext prompt template
		Verbose:             a.verbose,
		LockFile:            a.lockFile,
		LockTarget:          rt.Target.Name,
		ForceTranslate:      a.force,
		OnProgress: func(lang string, done, total int) {
			logInfo(T("  %s: %d/%d"), lang, done, total)
		},
		OnLog: func(format string, args ...any) {
			logInfo(format, args...)
		},
		OnError: func(format string, args ...any) {
			logError(format, args...)
		},
	}

	setExclusionOpts(&opts, &rt.Target)

	// Override prompt from target config
	if rt.Target.Prompt != "" && opts.SystemPrompt == "" {
		opts.SystemPrompt = rt.Target.Prompt
	}

	// Load PO files, auto-creating from POT if missing
	var langTasks []translate.LangTask
	for _, lang := range langs {
		poPath := rt.POPath(lang)
		poFile, err := po.ParseFile(poPath)
		if err != nil {
			if !fileExists(poPath) {
				poFile = createPOFromPOT(proj, lang, poPath)
				if poFile == nil {
					continue
				}
			} else {
				logError(T("Reading %s: %v"), poPath, err)
				continue
			}
		}

		// Skip if already fully translated (unless retranslate)
		if !a.retranslate {
			untranslated := poFile.UntranslatedEntries()
			fuzzyEntries := poFile.FuzzyEntries()
			if len(untranslated) == 0 && (!a.fuzzy || len(fuzzyEntries) == 0) {
				continue
			}
		}

		langTasks = append(langTasks, translate.LangTask{
			Lang:   lang,
			POFile: poFile,
			POPath: poPath,
		})
	}

	if len(langTasks) == 0 {
		logSuccess(T("[%s] All translations complete!"), rt.Target.Name)
		return nil
	}

	return translate.TranslateAll(ctx, langTasks, opts)
}

// translatePo4aTarget translates documentation PO files managed by po4a.
func translatePo4aTarget(ctx context.Context, rt config.ResolvedTarget, prov translate.Provider, a translateArgs, langs []string) error {
	cfgPath := rt.AbsPo4aConfig()
	cfgDir := filepath.Dir(cfgPath)
	poDir := filepath.Join(cfgDir, "po")

	logInfo(T("Provider: %s (%s), Model: %s"), prov.Name, prov.ID, prov.Model)
	logInfo(T("po4a config: %s"), cfgPath)
	logInfo(T("PO dir: %s"), poDir)
	logInfo(T("Translating: %s"), strings.Join(langs, ", "))

	if a.dryRun {
		for _, lang := range langs {
			poPath := rt.DocsPOPath(lang)
			if poFile, err := po.ParseFile(poPath); err == nil {
				untranslated := poFile.UntranslatedEntries()
				langName := po.LangNameNative(lang)
				logInfo(T("%s (%s): %d strings to translate"), lang, langName, len(untranslated))
			} else {
				logInfo(T("%s: PO file not found at %s"), lang, poPath)
			}
		}
		return nil
	}

	parallelMode := translate.ParallelSequential
	if a.parallel {
		parallelMode = translate.ParallelFullParallel
	}

	opts := translate.Options{
		Provider:            prov,
		ChunkSize:           a.chunkSize,
		ParallelMode:        parallelMode,
		MaxConcurrent:       a.maxConcurrent,
		RequestDelay:        a.requestDelay,
		Timeout:             a.timeout,
		MaxRetries:          a.maxRetries,
		RetranslateExisting: a.retranslate,
		TranslateFuzzy:      a.fuzzy,
		SystemPrompt:        a.prompt,
		PromptType:          "docs", // Use docs-specific prompt template
		Verbose:             a.verbose,
		LockFile:            a.lockFile,
		LockTarget:          rt.Target.Name,
		ForceTranslate:      a.force,
		OnProgress: func(lang string, done, total int) {
			logInfo(T("  %s: %d/%d"), lang, done, total)
		},
		OnLog: func(format string, args ...any) {
			logInfo(format, args...)
		},
		OnError: func(format string, args ...any) {
			logError(format, args...)
		},
	}

	setExclusionOpts(&opts, &rt.Target)

	// Override prompt from target config
	if rt.Target.Prompt != "" && a.prompt == "" {
		opts.SystemPrompt = rt.Target.Prompt
	}
	if opts.SystemPrompt == "" && opts.PromptType == "docs" {
		logInfo(T("Using documentation-specific translation prompt (groff/man markup preservation)"))
	}

	// Auto-init: if any PO files are missing, run po4a to generate them
	hasMissing := false
	for _, lang := range langs {
		if !fileExists(rt.DocsPOPath(lang)) {
			hasMissing = true
			break
		}
	}
	if hasMissing {
		logInfo(T("PO files missing, running po4a initialization..."))
		proj := &config.Project{
			Name:        rt.Target.Name,
			Version:     "0.0.0",
			POStructure: config.POStructurePo4a,
			Po4aConfig:  cfgPath,
			Languages:   langs,
			SourceLang:  rt.Target.SourceLang,
		}
		proj.PODir = poDir
		proj.ManpagesDir = cfgDir
		// Check for docs directory for manpage generation
		for _, candidate := range []string{"docs", "doc"} {
			docsDir := filepath.Join(rt.AbsRoot, candidate)
			if info, err := os.Stat(docsDir); err == nil && info.IsDir() {
				proj.DocsDir = docsDir
				break
			}
		}
		if err := doPo4aInit(proj); err != nil {
			return fmt.Errorf(T("auto-init failed: %v"), err)
		}
	}

	// Collect PO files for each language
	var langTasks []translate.LangTask
	for _, lang := range langs {
		poPath := rt.DocsPOPath(lang)
		poFile, err := po.ParseFile(poPath)
		if err != nil {
			if !fileExists(poPath) {
				logWarning(T("[%s] No PO file for %s at %s, skipping"), rt.Target.Name, lang, poPath)
				continue
			}
			logError(T("Reading %s: %v"), poPath, err)
			continue
		}

		// Skip if already fully translated (unless retranslate)
		if !a.retranslate {
			untranslated := poFile.UntranslatedEntries()
			fuzzyEntries := poFile.FuzzyEntries()
			if len(untranslated) == 0 && (!a.fuzzy || len(fuzzyEntries) == 0) {
				continue
			}
		}

		langTasks = append(langTasks, translate.LangTask{
			Lang:   lang,
			POFile: poFile,
			POPath: poPath,
		})
	}

	if len(langTasks) == 0 {
		logSuccess(T("[%s] All translations complete!"), rt.Target.Name)
		return nil
	}

	return translate.TranslateAll(ctx, langTasks, opts)
}

// translateI18NextTarget translates i18next JSON files.
func translateI18NextTarget(ctx context.Context, rt config.ResolvedTarget, prov translate.Provider, a translateArgs, langs []string) error {
	transDir := rt.AbsTranslationsDir()

	logInfo(T("Provider: %s (%s), Model: %s"), prov.Name, prov.ID, prov.Model)
	logInfo(T("Translations dir: %s"), transDir)
	logInfo(T("Translating: %s"), strings.Join(langs, ", "))

	// Build a Project-like structure for reuse with existing i18next code
	proj := &config.Project{
		Name:               rt.Target.Name,
		Type:               config.ProjectTypeI18Next,
		I18NextDir:         transDir,
		I18NextPathPattern: rt.Target.Pattern,
		SourceLang:         rt.Target.SourceLang,
		Languages:          langs,
	}

	runTranslateI18Next(proj, &rt.Target, prov, a)
	return nil
}

// translateJSONTarget translates simple JSON translation files { "translations": {...} }.
func translateJSONTarget(ctx context.Context, rt config.ResolvedTarget, prov translate.Provider, a translateArgs, langs []string) error {
	transDir := rt.AbsTranslationsDir()

	logInfo(T("Provider: %s (%s), Model: %s"), prov.Name, prov.ID, prov.Model)
	logInfo(T("Translations dir: %s"), transDir)
	logInfo(T("Translating: %s"), strings.Join(langs, ", "))

	// JSON targets can use the same i18next code path since the format is compatible
	proj := &config.Project{
		Name:               rt.Target.Name,
		Type:               config.ProjectTypeI18Next,
		I18NextDir:         transDir,
		I18NextPathPattern: rt.Target.Pattern,
		SourceLang:         rt.Target.SourceLang,
		Languages:          langs,
	}

	runTranslateI18Next(proj, &rt.Target, prov, a)
	return nil
}

// translateAndroidTarget translates Android strings.xml files.
func translateAndroidTarget(ctx context.Context, rt config.ResolvedTarget, prov translate.Provider, a translateArgs, langs []string) error {
	resDir := rt.AbsResDir()

	logInfo(T("Provider: %s (%s), Model: %s"), prov.Name, prov.ID, prov.Model)
	if a.parallel {
		logInfo(T("Parallel: enabled, max concurrent: %d"), a.maxConcurrent)
	}
	if a.chunkSize > 0 {
		logInfo(T("Chunk size: %d"), a.chunkSize)
	}
	logInfo(T("Res dir: %s"), resDir)
	logInfo(T("Translating: %s"), strings.Join(langs, ", "))

	// Load source (English) strings.xml
	srcPath := android.SourceStringsXMLPath(resDir)
	srcFile, err := android.ParseFile(srcPath)
	if err != nil {
		return fmt.Errorf(T("cannot read source strings.xml %s: %w"), srcPath, err)
	}
	srcTotal, _, _ := srcFile.Stats()
	logInfo(T("Source strings: %d"), srcTotal)

	if a.dryRun {
		for _, lang := range langs {
			filePath := android.StringsXMLPath(resDir, lang)
			file, err := android.ParseFile(filePath)
			if err != nil {
				langName := i18next.ResolveMeta(lang).Name
				logInfo(T("%s (%s): %d strings to translate (file will be auto-created)"), lang, langName, srcTotal)
				continue
			}
			untranslated := file.UntranslatedKeys()
			count := len(untranslated)
			if a.retranslate {
				_, count, _ = file.Stats()
				count = srcTotal // retranslate all
			}
			langName := i18next.ResolveMeta(lang).Name
			logInfo(T("%s (%s): %d strings to translate"), lang, langName, count)
		}
		return nil
	}

	// Determine parallel mode
	parallelMode := translate.ParallelSequential
	if a.parallel {
		parallelMode = translate.ParallelFullParallel
	}

	// Build translation options
	systemPrompt := a.prompt
	if rt.Target.Prompt != "" && a.prompt == "" {
		systemPrompt = rt.Target.Prompt
	}

	opts := translate.Options{
		Provider:            prov,
		ChunkSize:           a.chunkSize,
		ParallelMode:        parallelMode,
		MaxConcurrent:       a.maxConcurrent,
		RequestDelay:        a.requestDelay,
		Timeout:             a.timeout,
		MaxRetries:          a.maxRetries,
		RetranslateExisting: a.retranslate,
		SystemPrompt:        systemPrompt,
		PromptType:          "android", // Use Android-specific prompt template
		Verbose:             a.verbose,
		LockFile:            a.lockFile,
		LockTarget:          rt.Target.Name,
		ForceTranslate:      a.force,
		OnProgress: func(lang string, done, total int) {
			logInfo(T("  %s: %d/%d"), lang, done, total)
		},
		OnLog: func(format string, args ...any) {
			logInfo(format, args...)
		},
		OnError: func(format string, args ...any) {
			logError(format, args...)
		},
	}

	setExclusionOpts(&opts, &rt.Target)

	// Build language tasks
	var langTasks []translate.AndroidLangTask
	for _, lang := range langs {
		filePath := android.StringsXMLPath(resDir, lang)
		file, err := android.ParseFile(filePath)
		if err != nil {
			// Auto-create translation file from source with empty values
			file = android.NewTranslationFile(srcFile)
			logInfo(T("Auto-creating %s with %d strings"), filePath, srcTotal)
		} else {
			// Sync keys: add any new keys from source
			added := file.SyncKeys(srcFile)
			if added > 0 {
				logInfo(T("Added %d new strings to %s"), added, filePath)
			}
		}

		// Skip if already fully translated (unless retranslate)
		if !a.retranslate {
			untranslated := file.UntranslatedKeys()
			if len(untranslated) == 0 {
				continue
			}
		}

		langName := i18next.ResolveMeta(lang).Name

		langTasks = append(langTasks, translate.AndroidLangTask{
			Lang:       lang,
			LangName:   langName,
			File:       file,
			FilePath:   filePath,
			SourceFile: srcFile,
		})
	}

	if len(langTasks) == 0 {
		logSuccess(T("[%s] All translations complete!"), rt.Target.Name)
		return nil
	}

	return translate.TranslateAllAndroid(ctx, langTasks, opts)
}

// intersectLanguages returns the intersection of two language lists.
func intersectLanguages(available, filter []string) []string {
	filterMap := make(map[string]bool)
	for _, l := range filter {
		filterMap[strings.TrimSpace(l)] = true
	}
	var result []string
	for _, l := range available {
		if filterMap[l] {
			result = append(result, l)
		}
	}
	return result
}

// filterOutLang removes a specific language from a list.
func filterOutLang(langs []string, exclude string) []string {
	var result []string
	for _, l := range langs {
		if l != exclude {
			result = append(result, l)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// i18next translate
// ---------------------------------------------------------------------------

func runTranslateI18Next(proj *config.Project, target *config.Target, prov translate.Provider, a translateArgs) {
	// Load source language file for key reference
	srcPath := proj.I18NextPath(proj.SourceLang)
	srcFile, err := i18next.ParseFile(srcPath)
	if err != nil {
		logError(T("Cannot read source language file %s: %v"), srcPath, err)
		os.Exit(1)
	}
	srcKeys := srcFile.Keys()

	// Determine target languages
	var targetLangs []string
	if a.langs != "" {
		targetLangs = strings.Split(a.langs, ",")
	} else {
		for _, lang := range proj.Languages {
			if lang == proj.SourceLang {
				continue
			}
			filePath := proj.I18NextPath(lang)
			file, err := i18next.ParseFile(filePath)
			if err != nil {
				// File doesn't exist or can't be read — needs translation
				targetLangs = append(targetLangs, lang)
				continue
			}
			untranslated := file.UntranslatedKeys()
			if len(untranslated) > 0 || a.retranslate {
				targetLangs = append(targetLangs, lang)
			}
		}
	}

	// Filter out source language
	filtered := targetLangs[:0]
	for _, lang := range targetLangs {
		if lang != proj.SourceLang {
			filtered = append(filtered, lang)
		}
	}
	targetLangs = filtered

	if len(targetLangs) == 0 {
		logSuccess(T("All UI translations are complete!"))
		return
	}

	// Parallel mode
	parallelMode := translate.ParallelSequential
	if a.parallel {
		parallelMode = translate.ParallelFullParallel
	}

	logInfo(T("Provider: %s (%s), Model: %s"), prov.Name, prov.ID, prov.Model)
	if a.parallel {
		logInfo(T("Parallel: enabled, max concurrent: %d"), a.maxConcurrent)
	} else {
		logInfo(T("Parallel: disabled (sequential)"))
	}
	logInfo(T("Source keys (%s): %d"), proj.SourceLang, len(srcKeys))

	if len(targetLangs) > 0 {
		logInfo(T("Translating UI strings: %s"), strings.Join(targetLangs, ", "))
	}

	// Dry run
	if a.dryRun {
		for _, lang := range targetLangs {
			filePath := proj.I18NextPath(lang)
			file, err := i18next.ParseFile(filePath)
			if err != nil {
				langName := i18next.ResolveMeta(lang).Name
				logInfo(T("%s (%s): %d strings to translate (file will be auto-created)"), lang, langName, len(srcKeys))
				continue
			}
			untranslated := file.UntranslatedKeys()
			count := len(untranslated)
			if a.retranslate {
				count = len(file.Keys())
			}
			langName := i18next.ResolveMeta(lang).Name
			logInfo(T("%s (%s): %d strings to translate"), lang, langName, count)
		}
		return
	}

	// Setup signal handling for graceful cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		logWarning(T("Interrupted, saving progress..."))
		cancel()
	}()

	// Build translation options
	opts := translate.Options{
		Provider:            prov,
		ChunkSize:           a.chunkSize,
		ParallelMode:        parallelMode,
		MaxConcurrent:       a.maxConcurrent,
		RequestDelay:        a.requestDelay,
		Timeout:             a.timeout,
		MaxRetries:          a.maxRetries,
		RetranslateExisting: a.retranslate,
		SystemPrompt:        a.prompt,  // User-provided custom prompt
		PromptType:          "i18next", // Use i18next-specific prompt template
		Verbose:             a.verbose,
		LockFile:            a.lockFile,
		LockTarget:          proj.Name,
		ForceTranslate:      a.force,
		OnProgress: func(lang string, done, total int) {
			logInfo(T("  %s: %d/%d"), lang, done, total)
		},
		OnLog: func(format string, args ...any) {
			logInfo(format, args...)
		},
		OnError: func(format string, args ...any) {
			logError(format, args...)
		},
	}

	if target != nil {
		setExclusionOpts(&opts, target)
	}

	// Translate UI strings
	hadErrors := false
	if len(targetLangs) > 0 {
		// Build language tasks
		var langTasks []translate.JSONLangTask
		for _, lang := range targetLangs {
			filePath := proj.I18NextPath(lang)
			file, err := i18next.ParseFile(filePath)
			if err != nil {
				// Auto-create file with all keys empty
				meta := i18next.ResolveMeta(lang)
				file = &i18next.File{
					Meta:         meta,
					Translations: make(map[string]string),
				}
				for _, key := range srcKeys {
					file.Translations[key] = ""
				}
				logInfo(T("Auto-creating %s with %d keys"), filePath, len(srcKeys))
			}

			langName := i18next.ResolveMeta(lang).Name

			langTasks = append(langTasks, translate.JSONLangTask{
				Lang:     lang,
				LangName: langName,
				File:     file,
				FilePath: filePath,
			})
		}

		if len(langTasks) > 0 {
			err := translate.TranslateAllJSON(ctx, langTasks, opts)
			if err != nil {
				if ctx.Err() != nil {
					logWarning(T("Translation interrupted, partial progress saved"))
					os.Exit(0)
				}
				logError(T("UI translation failed: %v"), err)
				hadErrors = true
			}
		}
	}

	if hadErrors {
		logError(T("Translation completed with errors"))
		os.Exit(1)
	}
	logSuccess(T("Translation complete!"))
}
