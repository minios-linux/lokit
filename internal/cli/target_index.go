package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/minios-linux/lokit/config"
	. "github.com/minios-linux/lokit/i18n"
	"github.com/minios-linux/lokit/internal/format/i18next"
	"github.com/minios-linux/lokit/lockfile"
	"github.com/minios-linux/lokit/translate"
)

type indexJSONFile struct {
	translations map[string]string
}

func newIndexJSONFile(source map[string]string) *indexJSONFile {
	t := make(map[string]string, len(source))
	for k := range source {
		t[k] = ""
	}
	return &indexJSONFile{translations: t}
}

func parseIndexJSONFile(path string) (*indexJSONFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	t := make(map[string]string, len(raw))
	for k, v := range raw {
		s, ok := v.(string)
		if !ok {
			continue
		}
		t[k] = s
	}
	return &indexJSONFile{translations: t}, nil
}

func (f *indexJSONFile) Keys() []string {
	keys := make([]string, 0, len(f.translations))
	for k := range f.translations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (f *indexJSONFile) UntranslatedKeys() []string {
	keys := make([]string, 0)
	for _, k := range f.Keys() {
		if strings.TrimSpace(f.translations[k]) == "" {
			keys = append(keys, k)
		}
	}
	return keys
}

func (f *indexJSONFile) Get(key string) string {
	return f.translations[key]
}

func (f *indexJSONFile) Set(key, value string) bool {
	if f.translations == nil {
		f.translations = make(map[string]string)
	}
	if old, ok := f.translations[key]; ok && old == value {
		return false
	}
	f.translations[key] = value
	return true
}

func (f *indexJSONFile) Stats() (total int, translated int, pct float64) {
	total = len(f.translations)
	for _, v := range f.translations {
		if strings.TrimSpace(v) != "" {
			translated++
		}
	}
	if total > 0 {
		pct = float64(translated) * 100 / float64(total)
	}
	return total, translated, pct
}

func (f *indexJSONFile) SourceValues() map[string]string {
	out := make(map[string]string, len(f.translations))
	for k, v := range f.translations {
		out[k] = v
	}
	return out
}

func (f *indexJSONFile) WriteFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f.translations, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func indexTargetID(rt config.ResolvedTarget) (string, bool) {
	_, id, ok := config.SplitExpandedTargetName(rt.Target.Name)
	if !ok {
		return "", false
	}
	return id, true
}

func loadIndexSourceItem(rt config.ResolvedTarget) (*config.IndexItem, error) {
	if rt.Target.Source == nil || !rt.Target.Source.IsIndex() {
		return nil, nil
	}
	id, ok := indexTargetID(rt)
	if !ok {
		return nil, fmt.Errorf(T("index source target must be expanded with {id}: %s"), rt.Target.Name)
	}
	items, err := config.LoadIndexItemsForTarget(rt.Target, rt.AbsRoot)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, fmt.Errorf(T("source record %q not found in %s"), id, rt.Target.Source.Index)
}

func buildIndexSourceValues(item *config.IndexItem) map[string]string {
	vals := make(map[string]string, len(item.Fields))
	for k, v := range item.Fields {
		vals[k] = v
	}
	return vals
}

func showConfigIndexStats(rt config.ResolvedTarget, langs []string) {
	item, err := loadIndexSourceItem(rt)
	if err != nil {
		keyVal(T("Source"), colorYellow+fmt.Sprintf(T("error: %v"), err)+colorReset)
		return
	}
	if item == nil {
		keyVal(T("Source"), colorYellow+T("not found")+colorReset)
		return
	}

	keyVal(T("Source index"), rt.Target.Source.Index)
	keyVal(T("Record ID"), item.ID)
	keyVal(T("Source keys"), fmt.Sprintf("%d (%s)", len(item.Fields), rt.Target.SourceLang))
	langWidth := langColumnWidth(langs)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s%-*s %-22s %5s %5s%s\n",
		colorDim, langWidth+3, T("Lang"), T("Progress"), T("Done"), T("Left"), colorReset)
	fmt.Fprintln(os.Stderr, "  "+colorDim+strings.Repeat("─", 46)+colorReset)

	for _, lang := range langs {
		filePath := rt.TranslationPath(lang)
		f, err := parseIndexJSONFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("missing"), colorReset)
			continue
		}

		for k := range item.Fields {
			if _, ok := f.translations[k]; !ok {
				f.translations[k] = ""
			}
		}
		total, translated, _ := f.Stats()
		if total > len(item.Fields) {
			total = len(item.Fields)
		}
		if translated > total {
			translated = total
		}
		untranslated := total - translated

		percent := 0
		if total > 0 {
			percent = translated * 100 / total
		}
		fmt.Fprintf(os.Stderr, "  %s %s %5d %5d\n",
			langCell(lang, langWidth), progressBar(percent, 16), translated, untranslated)
	}
}

func runInitIndex(rt config.ResolvedTarget, langs []string) {
	item, err := loadIndexSourceItem(rt)
	if err != nil {
		logError(T("Index init failed: %v"), err)
		os.Exit(1)
	}
	if item == nil {
		logInfo(T("No index source for %s"), rt.Target.Name)
		return
	}

	sourceValues := buildIndexSourceValues(item)
	created := 0
	updated := 0

	for _, lang := range langs {
		if lang == rt.Target.SourceLang {
			continue
		}

		filePath := rt.TranslationPath(lang)
		targetFile, err := parseIndexJSONFile(filePath)
		if err != nil {
			targetFile = newIndexJSONFile(sourceValues)
			if err := targetFile.WriteFile(filePath); err != nil {
				logError(T("Creating %s: %v"), filePath, err)
				continue
			}
			logSuccess(T("Created: %s (%d keys)"), filePath, len(sourceValues))
			created++
			continue
		}

		changed := false
		for k := range sourceValues {
			if _, ok := targetFile.translations[k]; !ok {
				targetFile.translations[k] = ""
				changed = true
			}
		}
		if !changed {
			continue
		}
		if err := targetFile.WriteFile(filePath); err != nil {
			logError(T("Writing %s: %v"), filePath, err)
			continue
		}
		logSuccess(T("Updated: %s"), filePath)
		updated++
	}

	logInfo(T("Index init: %d created, %d updated"), created, updated)
}

func translateIndexTarget(ctx context.Context, rt config.ResolvedTarget, prov translate.Provider, a translateArgs, langs []string, quietNoop bool) error {
	item, err := loadIndexSourceItem(rt)
	if err != nil {
		return err
	}
	if item == nil {
		return fmt.Errorf(T("index source item not found for %s"), rt.Target.Name)
	}
	sourceValues := buildIndexSourceValues(item)

	if a.dryRun {
		for _, lang := range langs {
			langName := i18next.ResolveMeta(lang).Name
			count := len(sourceValues)
			if !a.retranslate {
				if f, err := parseIndexJSONFile(rt.TranslationPath(lang)); err == nil {
					for k := range sourceValues {
						if _, ok := f.translations[k]; !ok {
							f.translations[k] = ""
						}
					}
					count = len(f.UntranslatedKeys())
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

	systemPrompt := a.prompt
	if systemPrompt == "" {
		systemPrompt = rt.Target.Prompt
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
		SystemPrompt:        systemPrompt,
		PromptType:          "default",
		Verbose:             a.verbose,
		LockFile:            a.lockFile,
		LockTarget:          rt.Target.Name,
		ForceTranslate:      a.force,
		OnProgress: func(lang string, done, total int) {
			logInfo(T("  %s: %d/%d strings"), lang, done, total)
		},
		OnLog: func(format string, args ...any) {
			logInfo(format, args...)
		},
		OnError: func(format string, args ...any) {
			logError(format, args...)
		},
	}

	setExclusionOpts(&opts, &rt.Target)

	var tasks []translate.KVLangTask
	for _, lang := range langs {
		filePath := rt.TranslationPath(lang)
		f, err := parseIndexJSONFile(filePath)
		if err != nil {
			f = newIndexJSONFile(sourceValues)
		}
		for key := range sourceValues {
			if _, ok := f.translations[key]; !ok {
				f.translations[key] = ""
			}
		}
		if !a.retranslate && len(f.UntranslatedKeys()) == 0 {
			continue
		}

		tasks = append(tasks, translate.KVLangTask{
			Lang:         lang,
			LangName:     i18next.ResolveMeta(lang).Name,
			FilePath:     filePath,
			File:         f,
			SourceValues: sourceValues,
		})
	}

	if len(tasks) == 0 {
		if !quietNoop {
			logSuccess(T("[%s] All translations complete!"), rt.Target.Name)
		}
		return nil
	}

	return translate.TranslateAllKV(ctx, tasks, opts, translate.DefaultKVChunkTranslator())
}

func collectIndexSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	item, err := loadIndexSourceItem(rt)
	if err != nil {
		return nil, err
	}
	entries := make(map[string]string)
	if item == nil {
		return entries, nil
	}
	for key, value := range item.Fields {
		entries[key] = lockfile.KVEntryContent(key, value)
	}
	return entries, nil
}
