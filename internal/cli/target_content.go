package cli

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/minios-linux/lokit/config"
	. "github.com/minios-linux/lokit/i18n"
	arbfile "github.com/minios-linux/lokit/internal/format/arb"
	"github.com/minios-linux/lokit/internal/format/desktop"
	"github.com/minios-linux/lokit/internal/format/i18next"
	"github.com/minios-linux/lokit/internal/format/jskv"
	mdfile "github.com/minios-linux/lokit/internal/format/markdown"
	"github.com/minios-linux/lokit/internal/format/polkit"
	propfile "github.com/minios-linux/lokit/internal/format/properties"
	"github.com/minios-linux/lokit/internal/format/vuei18n"
	yamlfile "github.com/minios-linux/lokit/internal/format/yaml"
	"github.com/minios-linux/lokit/translate"
)

func discoverMarkdownFiles(dir, matchRoot string, excludes []string) ([]string, error) {
	if matchRoot == "" {
		matchRoot = dir
	}
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if shouldExcludeMarkdownPath(matchRoot, path, excludes) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func shouldExcludeMarkdownPath(root, filePath string, excludes []string) bool {
	if len(excludes) == 0 {
		return false
	}
	rel, err := filepath.Rel(root, filePath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	rel = filepath.ToSlash(rel)
	for _, pattern := range excludes {
		pattern = strings.Trim(strings.TrimSpace(filepath.ToSlash(pattern)), "/")
		if pattern == "" {
			continue
		}
		if strings.HasSuffix(pattern, "/**") {
			base := strings.TrimSuffix(pattern, "/**")
			if rel == base || strings.HasPrefix(rel, base+"/") {
				return true
			}
			continue
		}
		if ok, _ := pathpkg.Match(pattern, rel); ok || rel == pattern || strings.HasPrefix(rel, pattern+"/") {
			return true
		}
	}
	return false
}

func markdownSourceExcludes(rt config.ResolvedTarget) []string {
	excludes := append([]string{}, rt.Target.Exclude...)
	for _, lang := range rt.Languages {
		if lang == rt.Target.SourceLang {
			continue
		}
		for _, targetPath := range rt.TranslationPathCandidates(lang) {
			rel, err := filepath.Rel(rt.AbsRoot, targetPath)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			rel = filepath.ToSlash(rel)
			excludes = append(excludes, rel, rel+"/**")
		}
	}
	return excludes
}

func markdownLangDir(rt config.ResolvedTarget, lang string) string {
	if rt.Target.TargetPath != "" && strings.Contains(rt.Target.TargetPath, "{path}") {
		prefix := strings.Split(rt.Target.TargetPath, "{path}")[0]
		prefix = strings.ReplaceAll(prefix, "{lang}", lang)
		prefix = strings.TrimSuffix(prefix, "/")
		return filepath.Join(rt.AbsRoot, filepath.FromSlash(prefix))
	}
	if markdownTargetIsFile(rt) {
		return filepath.Dir(rt.TranslationPath(lang))
	}
	if rt.Target.Pattern == "" {
		return filepath.Join(rt.AbsTranslationsDir(), lang)
	}
	path := rt.TranslationPath(lang)
	if path == "" {
		return filepath.Join(rt.AbsTranslationsDir(), lang)
	}
	return path
}

func markdownTargetPath(rt config.ResolvedTarget, lang, relPath string) string {
	if markdownTargetIsFile(rt) {
		return rt.TranslationPath(lang)
	}
	if rt.Target.TargetPath != "" && strings.Contains(rt.Target.TargetPath, "{path}") {
		out := strings.ReplaceAll(rt.Target.TargetPath, "{lang}", lang)
		out = strings.ReplaceAll(out, "{path}", filepath.ToSlash(relPath))
		return filepath.Join(rt.AbsRoot, filepath.FromSlash(out))
	}
	return filepath.Join(markdownLangDir(rt, lang), relPath)
}

func markdownTargetIsFile(rt config.ResolvedTarget) bool {
	return rt.Target.TargetPath != "" && !strings.Contains(rt.Target.TargetPath, "{path}") && strings.EqualFold(filepath.Ext(rt.Target.TargetPath), ".md")
}

func markdownSourceDir(rt config.ResolvedTarget) string {
	if len(rt.Target.Sources) > 0 {
		return rt.AbsRoot
	}
	if p := rt.ExistingSourcePath(); p != "" {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return filepath.Dir(p)
		}
		return p
	}
	if p := rt.SourcePath(); p != "" {
		if strings.EqualFold(filepath.Ext(p), ".md") {
			return filepath.Dir(p)
		}
		return p
	}
	return markdownLangDir(rt, rt.Target.SourceLang)
}

func discoverMarkdownSourceFiles(rt config.ResolvedTarget) ([]string, error) {
	if len(rt.Target.Sources) == 0 {
		if p := rt.ExistingSourcePath(); p != "" {
			if info, err := os.Stat(p); err == nil && !info.IsDir() && strings.EqualFold(filepath.Ext(p), ".md") {
				return []string{p}, nil
			}
		}
		srcDir := markdownSourceDir(rt)
		return discoverMarkdownFiles(srcDir, rt.AbsRoot, markdownSourceExcludes(rt))
	}

	all, err := discoverMarkdownFiles(rt.AbsRoot, rt.AbsRoot, markdownSourceExcludes(rt))
	if err != nil {
		return nil, err
	}
	var files []string
	for _, p := range all {
		rel, err := filepath.Rel(rt.AbsRoot, p)
		if err != nil {
			continue
		}
		if matchAnyPathPattern(filepath.ToSlash(rel), rt.Target.Sources) {
			files = append(files, p)
		}
	}
	sort.Strings(files)
	return files, nil
}

func matchAnyPathPattern(rel string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = strings.Trim(strings.TrimSpace(filepath.ToSlash(pattern)), "/")
		if pattern == "" {
			continue
		}
		if strings.HasSuffix(pattern, "/**") {
			base := strings.TrimSuffix(pattern, "/**")
			if rel == base || strings.HasPrefix(rel, base+"/") {
				return true
			}
		}
		if strings.Contains(pattern, "**/") {
			prefix := strings.Split(pattern, "**/")[0]
			suffix := strings.Split(pattern, "**/")[1]
			if strings.HasPrefix(rel, prefix) {
				if ok, _ := pathpkg.Match(suffix, strings.TrimPrefix(rel, prefix)); ok {
					return true
				}
				parts := strings.Split(strings.TrimPrefix(rel, prefix), "/")
				for i := range parts {
					if ok, _ := pathpkg.Match(suffix, strings.Join(parts[i:], "/")); ok {
						return true
					}
				}
			}
		}
		if ok, _ := pathpkg.Match(pattern, rel); ok || rel == pattern || strings.HasPrefix(rel, pattern+"/") {
			return true
		}
	}
	return false
}

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
			if a.retranslate || a.force {
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
		SourceLanguage:      rt.Target.SourceLang,
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
	srcDir := markdownSourceDir(rt)
	srcFiles, _ := discoverMarkdownSourceFiles(rt)
	if len(srcFiles) == 0 {
		keyVal(T("Source"), colorYellow+T("not found")+colorReset+" ("+srcDir+")")
		return
	}

	srcTotal := 0
	srcByRelPath := make(map[string]*mdfile.File, len(srcFiles))
	for _, p := range srcFiles {
		f, err := mdfile.ParseFile(p)
		if err == nil {
			t, _, _ := f.Stats()
			srcTotal += t
			relPath, relErr := filepath.Rel(srcDir, p)
			if relErr == nil {
				srcByRelPath[filepath.ToSlash(relPath)] = f
			}
		}
	}
	keyVal(T("Source segments"), fmt.Sprintf("%d (%s, %d files)", srcTotal, srcLang, len(srcFiles)))
	langWidth := langColumnWidth(langs)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s%-*s %-22s %5s %5s%s\n",
		colorDim, langWidth+3, T("Lang"), T("Progress"), T("Done"), T("Left"), colorReset)
	fmt.Fprintln(os.Stderr, "  "+colorDim+strings.Repeat("─", 46)+colorReset)

	for _, lang := range langs {
		langDir := markdownLangDir(rt, lang)
		files, _ := discoverMarkdownTargetFiles(rt, lang)
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
			relPath, relErr := filepath.Rel(langDir, p)
			if relErr == nil && !markdownTargetIsFile(rt) {
				if srcFile, ok := srcByRelPath[filepath.ToSlash(relPath)]; ok {
					mdfile.SyncKeys(srcFile, f)
				}
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

func discoverMarkdownTargetFiles(rt config.ResolvedTarget, lang string) ([]string, error) {
	if markdownTargetIsFile(rt) {
		path := rt.TranslationPath(lang)
		if _, err := os.Stat(path); err == nil {
			return []string{path}, nil
		}
		return nil, nil
	}
	langDir := markdownLangDir(rt, lang)
	return discoverMarkdownFiles(langDir, langDir, nil)
}

func runInitMarkdown(rt config.ResolvedTarget, langs []string) {
	srcLang := rt.Target.SourceLang
	srcDir := markdownSourceDir(rt)

	srcFiles, _ := discoverMarkdownSourceFiles(rt)
	if len(srcFiles) == 0 {
		logError(T("Cannot find source Markdown files in %s"), srcDir)
		logInfo(T("Expected markdown files under: %s"), srcDir)
		os.Exit(1)
	}

	logInfo(T("Source language (%s): %d files"), srcLang, len(srcFiles))

	created, updated := 0, 0

	for _, lang := range langs {
		if lang == srcLang {
			continue
		}

		langDir := markdownLangDir(rt, lang)
		if err := os.MkdirAll(langDir, 0o755); err != nil {
			logError(T("Creating directory %s: %v"), langDir, err)
			continue
		}

		for _, srcPath := range srcFiles {
			relPath, err := filepath.Rel(srcDir, srcPath)
			if err != nil {
				logError(T("Computing relative path for %s: %v"), srcPath, err)
				continue
			}
			targetPath := markdownTargetPath(rt, lang, relPath)

			srcFile, err := mdfile.ParseFile(srcPath)
			if err != nil {
				logError(T("Cannot read source Markdown file %s: %v"), srcPath, err)
				continue
			}

			if _, err := os.Stat(targetPath); os.IsNotExist(err) {
				newFile := mdfile.NewTranslationFile(srcFile, lang)
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

	srcDir := markdownSourceDir(rt)
	srcFiles, _ := discoverMarkdownSourceFiles(rt)
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
			count := 0
			for _, srcPath := range srcFiles {
				relPath, err := filepath.Rel(srcDir, srcPath)
				if err != nil {
					logError(T("Computing relative path for %s: %v"), srcPath, err)
					continue
				}
				targetPath := markdownTargetPath(rt, lang, relPath)
				srcFile, err := mdfile.ParseFile(srcPath)
				if err != nil {
					continue
				}
				if a.retranslate || a.force {
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
				mdfile.SyncKeys(srcFile, tf)
				count += len(tf.UntranslatedKeys())
			}
			logInfo(T("%s (%s): %d segments to translate"), lang, langName, count)
		}
		return nil
	}

	var tasks []translate.MarkdownLangTask
	for _, lang := range langs {
		langName := i18next.ResolveMeta(lang).Name
		langDir := markdownLangDir(rt, lang)
		if err := os.MkdirAll(langDir, 0o755); err != nil {
			logError(T("Creating directory %s: %v"), langDir, err)
			continue
		}

		for _, srcPath := range srcFiles {
			relPath, err := filepath.Rel(srcDir, srcPath)
			if err != nil {
				logError(T("Computing relative path for %s: %v"), srcPath, err)
				continue
			}
			targetPath := markdownTargetPath(rt, lang, relPath)

			srcFile, err := mdfile.ParseFile(srcPath)
			if err != nil {
				logError(T("Reading %s: %v"), srcPath, err)
				continue
			}

			var targetFile *mdfile.File
			if _, err := os.Stat(targetPath); os.IsNotExist(err) {
				targetFile = mdfile.NewTranslationFile(srcFile, lang)
			} else {
				targetFile, err = mdfile.ParseFile(targetPath)
				if err != nil {
					logError(T("Reading %s: %v"), targetPath, err)
					continue
				}
				mdfile.SyncKeys(srcFile, targetFile)
			}

			lockKeyPrefix := filepath.ToSlash(relPath)
			tasks = append(tasks, translate.MarkdownLangTask{Lang: lang, LangName: langName, FilePath: targetPath, File: targetFile, SourceFile: srcFile, LockKeyPrefix: lockKeyPrefix})
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
		SourceLanguage:      rt.Target.SourceLang,
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
			newFile := propfile.NewTranslationFile(srcFile, lang)
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
			if !a.retranslate && !a.force {
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
			targetFile = propfile.NewTranslationFile(srcFile, lang)
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
		SourceLanguage:      rt.Target.SourceLang,
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
			if !a.retranslate && !a.force {
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
		SourceLanguage:      rt.Target.SourceLang,
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

// showConfigVueI18nStats shows translation stats for a nested JSON target.
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

// runInitVueI18n creates or syncs nested JSON translation files from source.
func runInitVueI18n(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	srcLang := rt.Target.SourceLang
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}

	srcFile, err := vuei18n.ParseFile(srcPath)
	if err != nil {
		logError(T("Cannot read source nested JSON file %s: %v"), srcPath, err)
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
			targetFile = vuei18n.NewTranslationFile(srcFile, lang)
			if err := os.MkdirAll(transDir, 0o755); err != nil {
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
		return fmt.Errorf(T("cannot read source nested JSON file %s: %w"), srcPath, err)
	}

	srcTotal, _, _ := srcFile.Stats()
	logInfo(T("Source strings: %d"), srcTotal)

	if a.dryRun {
		for _, lang := range langs {
			langName := i18next.ResolveMeta(lang).Name
			targetPath := rt.TranslationPath(lang)
			count := srcTotal
			if !a.retranslate && !a.force {
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
			targetFile = vuei18n.NewTranslationFile(srcFile, lang)
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
		logInfo(T("No nested JSON files to translate"))
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
		SourceLanguage:      rt.Target.SourceLang,
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

func showConfigJSKVStats(rt config.ResolvedTarget, langs []string) {
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}
	srcFile, err := jskv.ParseFile(srcPath)
	if err != nil {
		label := T("not found")
		if _, statErr := os.Stat(srcPath); statErr == nil {
			label = T("parse error") + ": " + err.Error()
		}
		keyVal(T("Source"), colorYellow+label+colorReset+" ("+srcPath+")")
		return
	}
	srcTotal, _, _ := srcFile.Stats()
	transDir := rt.AbsTranslationsDir()
	keyVal(T("Translations"), transDir)
	keyVal(T("Source keys"), fmt.Sprintf("%d (%s)", srcTotal, rt.Target.SourceLang))
	langWidth := langColumnWidth(langs)

	sectionHeader(T("UI Translation Statistics"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s%-*s %-22s %5s %5s%s\n",
		colorDim, langWidth+3, T("Lang"), T("Progress"), T("Done"), T("Left"), colorReset)
	fmt.Fprintln(os.Stderr, "  "+colorDim+strings.Repeat("─", 46)+colorReset)

	for _, lang := range langs {
		filePath := rt.ExistingTranslationPath(lang)
		exists := filePath != ""
		if filePath == "" {
			filePath = rt.TranslationPath(lang)
		}

		file, err := jskv.ParseFile(filePath)
		if err != nil {
			label := T("missing")
			if exists {
				label = T("parse error")
			}
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, label, colorReset)
			continue
		}
		jskv.SyncKeys(srcFile, file)

		_, translated, _ := file.Stats()
		untranslated := len(file.UntranslatedKeys())
		percent := 0
		if srcTotal > 0 {
			percent = translated * 100 / srcTotal
		}

		fmt.Fprintf(os.Stderr, "  %s %s %5d %5d\n",
			langCell(lang, langWidth), progressBar(percent, 16), translated, untranslated)
	}

	fmt.Fprintln(os.Stderr)
}

func runInitJSKV(rt config.ResolvedTarget, langs []string) {
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}
	srcFile, err := jskv.ParseFile(srcPath)
	if err != nil {
		logError(T("Cannot read source JS file %s: %v"), srcPath, err)
		os.Exit(1)
	}
	for _, lang := range langs {
		if lang == rt.Target.SourceLang {
			continue
		}
		targetPath := rt.TranslationPath(lang)
		existingPath := rt.ExistingTranslationPath(lang)
		if existingPath == "" {
			f := jskv.NewTranslationFile(srcFile)
			if err := f.WriteFile(targetPath); err != nil {
				logError(T("Creating %s: %v"), targetPath, err)
				continue
			}
			logSuccess(T("Created: %s"), targetPath)
			continue
		}

		f, err := jskv.ParseFile(existingPath)
		if err != nil {
			logError(T("Cannot read JS file %s: %v"), existingPath, err)
			continue
		}
		jskv.SyncKeys(srcFile, f)
		if err := f.WriteFile(existingPath); err != nil {
			logError(T("Writing %s: %v"), existingPath, err)
			continue
		}
		logSuccess(T("Updated: %s"), existingPath)
	}
}

func translateJSKVTarget(ctx context.Context, rt config.ResolvedTarget, prov translate.Provider, a translateArgs, langs []string) error {
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}
	srcFile, err := jskv.ParseFile(srcPath)
	if err != nil {
		return fmt.Errorf(T("cannot read source JS file %s: %w"), srcPath, err)
	}
	srcTotal, _, _ := srcFile.Stats()

	if a.dryRun {
		for _, lang := range langs {
			langName := i18next.ResolveMeta(lang).Name
			count := srcTotal
			if !a.retranslate && !a.force {
				filePath := rt.ExistingTranslationPath(lang)
				if filePath == "" {
					logInfo(T("%s (%s): %d strings to translate (file will be auto-created)"), lang, langName, count)
					continue
				}
				if file, err := jskv.ParseFile(filePath); err == nil {
					jskv.SyncKeys(srcFile, file)
					count = len(file.UntranslatedKeys())
				}
			}
			logInfo(T("%s (%s): %d strings to translate"), lang, langName, count)
		}
		return nil
	}

	parallelMode := translate.ParallelSequential
	if a.parallel {
		parallelMode = translate.ParallelFullParallel
	}
	opts := translate.Options{
		Provider:            prov,
		SourceLanguage:      rt.Target.SourceLang,
		ChunkSize:           a.chunkSize,
		ParallelMode:        parallelMode,
		MaxConcurrent:       a.maxConcurrent,
		RequestDelay:        a.requestDelay,
		Timeout:             a.timeout,
		MaxRetries:          a.maxRetries,
		RetranslateExisting: a.retranslate,
		SystemPrompt:        a.prompt,
		PromptType:          "i18next",
		Verbose:             a.verbose,
		LockFile:            a.lockFile,
		LockTarget:          rt.Target.Name,
		ForceTranslate:      a.force,
		OnLog:               func(format string, args ...any) { logInfo(format, args...) },
		OnError:             func(format string, args ...any) { logError(format, args...) },
	}
	setExclusionOpts(&opts, &rt.Target)

	var tasks []translate.KVLangTask
	for _, lang := range langs {
		filePath := rt.ExistingTranslationPath(lang)
		if filePath == "" {
			filePath = rt.TranslationPath(lang)
		}
		file, err := jskv.ParseFile(filePath)
		if err != nil {
			file = jskv.NewTranslationFile(srcFile)
		} else {
			jskv.SyncKeys(srcFile, file)
		}
		tasks = append(tasks, translate.KVLangTask{Lang: lang, LangName: i18next.ResolveMeta(lang).Name, FilePath: filePath, File: file, SourceValues: srcFile.SourceValues()})
	}
	if len(tasks) == 0 {
		return nil
	}
	return translate.TranslateAllKV(ctx, tasks, opts, translate.I18NextChunkTranslator())
}

func showConfigDesktopStats(rt config.ResolvedTarget, langs []string) {
	path := rt.SourcePath()
	src, err := desktop.ParseFile(path, rt.Target.SourceLang)
	if err != nil {
		keyVal(T("Source"), colorYellow+T("not found")+colorReset+" ("+path+")")
		return
	}
	total, _, _ := src.Stats()
	keyVal(T("Source keys"), fmt.Sprintf("%d (%s)", total, rt.Target.SourceLang))
	showSimpleKVSingleFileStats(path, langs, func(lang string) (int, int, error) {
		f, err := desktop.ParseFile(path, lang)
		if err != nil {
			return 0, 0, err
		}
		t, tr, _ := f.Stats()
		return t, tr, nil
	})
}

func showConfigPolkitStats(rt config.ResolvedTarget, langs []string) {
	path := rt.SourcePath()
	src, err := polkit.ParseFile(path, rt.Target.SourceLang)
	if err != nil {
		keyVal(T("Source"), colorYellow+T("not found")+colorReset+" ("+path+")")
		return
	}
	total, _, _ := src.Stats()
	keyVal(T("Source keys"), fmt.Sprintf("%d (%s)", total, rt.Target.SourceLang))
	showSimpleKVSingleFileStats(path, langs, func(lang string) (int, int, error) {
		f, err := polkit.ParseFile(path, lang)
		if err != nil {
			return 0, 0, err
		}
		t, tr, _ := f.Stats()
		return t, tr, nil
	})
}

func showSimpleKVSingleFileStats(path string, langs []string, fn func(lang string) (int, int, error)) {
	langWidth := langColumnWidth(langs)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s%-*s %-22s %5s %5s%s\n", colorDim, langWidth+3, T("Lang"), T("Progress"), T("Done"), T("Left"), colorReset)
	fmt.Fprintln(os.Stderr, "  "+colorDim+strings.Repeat("─", 46)+colorReset)
	for _, lang := range langs {
		total, translated, err := fn(lang)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n", langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("parse error"), colorReset)
			continue
		}
		untranslated := total - translated
		percent := 0
		if total > 0 {
			percent = translated * 100 / total
		}
		fmt.Fprintf(os.Stderr, "  %s %s %5d %5d\n", langCell(lang, langWidth), progressBar(percent, 16), translated, untranslated)
	}
}

func translateDesktopTarget(ctx context.Context, rt config.ResolvedTarget, prov translate.Provider, a translateArgs, langs []string) error {
	path := rt.SourcePath()
	src, err := desktop.ParseFile(path, rt.Target.SourceLang)
	if err != nil {
		return fmt.Errorf(T("cannot read desktop file %s: %w"), path, err)
	}
	opts := translate.Options{Provider: prov, SourceLanguage: rt.Target.SourceLang, ChunkSize: a.chunkSize, ParallelMode: translate.ParallelSequential, RequestDelay: a.requestDelay, Timeout: a.timeout, MaxRetries: a.maxRetries, RetranslateExisting: a.retranslate, SystemPrompt: a.prompt, PromptType: "default", Verbose: a.verbose, LockFile: a.lockFile, LockTarget: rt.Target.Name, ForceTranslate: a.force, OnLog: func(format string, args ...any) { logInfo(format, args...) }, OnError: func(format string, args ...any) { logError(format, args...) }}
	setExclusionOpts(&opts, &rt.Target)
	var tasks []translate.KVLangTask
	for _, lang := range langs {
		f, err := desktop.ParseFile(path, lang)
		if err != nil {
			continue
		}
		tasks = append(tasks, translate.KVLangTask{Lang: lang, LangName: i18next.ResolveMeta(lang).Name, FilePath: path, File: f, SourceValues: src.SourceValues()})
	}
	if len(tasks) == 0 {
		return nil
	}
	return translate.TranslateAllKV(ctx, tasks, opts, translate.DefaultKVChunkTranslator())
}

func translatePolkitTarget(ctx context.Context, rt config.ResolvedTarget, prov translate.Provider, a translateArgs, langs []string) error {
	path := rt.SourcePath()
	src, err := polkit.ParseFile(path, rt.Target.SourceLang)
	if err != nil {
		return fmt.Errorf(T("cannot read policy file %s: %w"), path, err)
	}
	opts := translate.Options{Provider: prov, SourceLanguage: rt.Target.SourceLang, ChunkSize: a.chunkSize, ParallelMode: translate.ParallelSequential, RequestDelay: a.requestDelay, Timeout: a.timeout, MaxRetries: a.maxRetries, RetranslateExisting: a.retranslate, SystemPrompt: a.prompt, PromptType: "default", Verbose: a.verbose, LockFile: a.lockFile, LockTarget: rt.Target.Name, ForceTranslate: a.force, OnLog: func(format string, args ...any) { logInfo(format, args...) }, OnError: func(format string, args ...any) { logError(format, args...) }}
	setExclusionOpts(&opts, &rt.Target)
	var tasks []translate.KVLangTask
	for _, lang := range langs {
		f, err := polkit.ParseFile(path, lang)
		if err != nil {
			continue
		}
		tasks = append(tasks, translate.KVLangTask{Lang: lang, LangName: i18next.ResolveMeta(lang).Name, FilePath: path, File: f, SourceValues: src.SourceValues()})
	}
	if len(tasks) == 0 {
		return nil
	}
	return translate.TranslateAllKV(ctx, tasks, opts, translate.DefaultKVChunkTranslator())
}
