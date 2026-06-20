package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/minios-linux/lokit/config"
	. "github.com/minios-linux/lokit/i18n"
	formatfile "github.com/minios-linux/lokit/internal/format"
	"github.com/minios-linux/lokit/internal/format/android"
	arbfile "github.com/minios-linux/lokit/internal/format/arb"
	"github.com/minios-linux/lokit/internal/format/desktop"
	"github.com/minios-linux/lokit/internal/format/i18next"
	"github.com/minios-linux/lokit/internal/format/jskv"
	mdfile "github.com/minios-linux/lokit/internal/format/markdown"
	po "github.com/minios-linux/lokit/internal/format/po"
	"github.com/minios-linux/lokit/internal/format/polkit"
	propfile "github.com/minios-linux/lokit/internal/format/properties"
	"github.com/minios-linux/lokit/internal/format/vuei18n"
	yamlfile "github.com/minios-linux/lokit/internal/format/yaml"
	"github.com/minios-linux/lokit/lockfile"
	"github.com/spf13/cobra"
)

func newLockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lock",
		Short: T("Manage lokit.lock file"),
		Long: T(`Manage incremental translation lock data in lokit.lock.

Subcommands:
  init    Initialize lock entries from source strings without AI calls
  status  Show lock statistics
  clean   Remove stale entries and orphan lock targets
  reset   Remove all or part of lock data`),
	}

	cmd.AddCommand(
		newLockInitCmd(),
		newLockStatusCmd(),
		newLockCleanCmd(),
		newLockResetCmd(),
	)

	return cmd
}

func newLockInitCmd() *cobra.Command {
	var target string
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: T("Initialize lock from existing sources"),
		Long: T(`Initialize lokit.lock from current source files without calling AI.

For each target/language pair, lokit stores source-content checksums used by
incremental translation.

Examples:
  lokit lock init
  lokit lock init --target ui
  lokit lock init --target ui --force`),
		Run: func(cmd *cobra.Command, args []string) {
			runLockInit(target, force)
		},
	}

	cmd.Flags().StringVar(&target, "target", "", T("Target name from lokit.yaml (default: all targets)"))
	cmd.Flags().BoolVarP(&force, "force", "f", false, T("Overwrite existing lock entries for selected targets"))
	return cmd
}

func newLockStatusCmd() *cobra.Command {
	var target string
	var verbose bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: T("Show lock file statistics"),
		Long: T(`Show statistics for lokit.lock, including tracked targets and keys.

Examples:
  lokit lock status
  lokit lock status --target ui
  lokit lock status --verbose
  lokit lock status --json`),
		Run: func(cmd *cobra.Command, args []string) {
			runLockStatus(target, verbose, jsonOut)
		},
	}

	cmd.Flags().StringVar(&target, "target", "", T("Target name from lokit.yaml (default: all targets)"))
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, T("Show per-language lock breakdown"))
	cmd.Flags().BoolVar(&jsonOut, "json", false, T("Output lock status as JSON"))
	return cmd
}

func newLockCleanCmd() *cobra.Command {
	var targets []string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "clean",
		Short: T("Remove stale and orphan lock entries"),
		Long: T(`Remove lock entries that no longer exist in source files and lock targets
that no longer exist in the current lokit.yaml configuration.

Examples:
  lokit lock clean
  lokit lock clean --target ui
  lokit lock clean --dry-run`),
		Run: func(cmd *cobra.Command, args []string) {
			runLockClean(targets, dryRun)
		},
	}

	cmd.Flags().StringSliceVar(&targets, "target", nil, T("Target name from lokit.yaml (repeat flag or use comma-separated list; default: all targets)"))
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, T("Show stale and orphan entries without modifying lokit.lock"))
	return cmd
}

func newLockResetCmd() *cobra.Command {
	var target string
	var lang string
	var yes bool

	cmd := &cobra.Command{
		Use:   "reset",
		Short: T("Reset lock entries"),
		Long: T(`Reset lock data at different scopes:

  - whole file:          lokit lock reset
  - all languages target: lokit lock reset --target ui
  - single language:      lokit lock reset --target ui --lang ru

Use --yes to skip confirmation.`),
		Run: func(cmd *cobra.Command, args []string) {
			runLockReset(target, lang, yes)
		},
	}

	cmd.Flags().StringVar(&target, "target", "", T("Target name from lokit.yaml"))
	cmd.Flags().StringVarP(&lang, "lang", "l", "", T("Language code (requires --target)"))
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, T("Skip confirmation prompt"))
	return cmd
}

func runLockInit(target string, force bool) {
	resolved, err := loadResolvedTargets(target)
	if err != nil {
		logError(T("%v"), err)
		os.Exit(1)
	}

	lf, err := lockfile.Load(rootDir)
	if err != nil {
		logError(T("Could not load lock file: %v"), err)
		os.Exit(1)
	}

	updatedTargets := 0
	added := 0
	skipped := 0
	hadErrors := false

	for _, rt := range resolved {
		sourceEntries, err := collectSourceEntries(rt)
		if err != nil {
			logWarning(T("[%s] %v"), rt.Target.Name, err)
			hadErrors = true
			continue
		}

		langs := filterOutLang(rt.Languages, rt.Target.SourceLang)
		if len(langs) == 0 {
			continue
		}

		for _, lang := range langs {
			if rt.Target.Type == config.TargetTypePo4a {
				units := collectPo4aLockUnits(rt, lang)
				if len(units) == 0 {
					logWarning(T("[%s/%s] cannot read any po4a PO files"), rt.Target.Name, lang)
					hadErrors = true
					continue
				}
				for _, unit := range units {
					if force {
						lf.RemoveTarget(unit.lockTarget)
					}
					filteredEntries := filterSourceEntriesByTranslatedKeys(unit.sourceEntries, unit.translatedKeys)
					for key, content := range filteredEntries {
						if !force && lf.Has(unit.lockTarget, key) {
							skipped++
							continue
						}
						lf.Update(unit.lockTarget, key, content)
						added++
					}
					updatedTargets++
				}
				continue
			}

			lockTarget := lockfile.LockTargetKey(rt.Target.Name, lang)
			if force {
				lf.RemoveTarget(lockTarget)
			}

			translatedKeys, err := collectTranslatedKeys(rt, lang)
			if err != nil {
				logWarning(T("[%s/%s] %v"), rt.Target.Name, lang, err)
				hadErrors = true
				continue
			}

			filteredEntries := filterSourceEntriesByTranslatedKeys(sourceEntries, translatedKeys)
			for key, content := range filteredEntries {
				if !force && lf.Has(lockTarget, key) {
					skipped++
					continue
				}
				lf.Update(lockTarget, key, content)
				added++
			}

			updatedTargets++
		}
	}

	if err := lf.Save(); err != nil {
		logError(T("Could not save lock file: %v"), err)
		os.Exit(1)
	}

	logSuccess(T("Lock initialized: %d target-language entries updated"), updatedTargets)
	logInfo(T("Added: %d, skipped existing: %d"), added, skipped)
	logInfo(T("Lock file: %s"), lf.Path())

	if hadErrors {
		os.Exit(1)
	}
}

type lockTargetStatus struct {
	Target      string         `json:"target"`
	Languages   int            `json:"languages"`
	Keys        int            `json:"keys"`
	PerLanguage map[string]int `json:"per_language,omitempty"`
}

type lockStatusOutput struct {
	Path      string             `json:"path"`
	Exists    bool               `json:"exists"`
	Targets   int                `json:"targets"`
	Languages int                `json:"languages"`
	Keys      int                `json:"keys"`
	ByTarget  []lockTargetStatus `json:"by_target"`
}

func runLockStatus(target string, verbose bool, jsonOut bool) {
	resolved, err := loadResolvedTargets(target)
	if err != nil {
		logError(T("%v"), err)
		os.Exit(1)
	}

	lf, err := lockfile.Load(rootDir)
	if err != nil {
		logError(T("Could not load lock file: %v"), err)
		os.Exit(1)
	}

	exists := true
	if _, err := os.Stat(lf.Path()); errors.Is(err, os.ErrNotExist) {
		exists = false
	}

	output := lockStatusOutput{
		Path:     lf.Path(),
		Exists:   exists,
		ByTarget: make([]lockTargetStatus, 0, len(resolved)),
	}

	for _, rt := range resolved {
		langs := filterOutLang(rt.Languages, rt.Target.SourceLang)
		if len(langs) == 0 {
			continue
		}

		ts := lockTargetStatus{
			Target:    rt.Target.Name,
			Languages: len(langs),
		}
		if verbose || jsonOut {
			ts.PerLanguage = make(map[string]int, len(langs))
		}

		for _, lang := range langs {
			count := 0
			for _, lockTarget := range lockTargetKeysFor(rt, lang) {
				count += lf.TargetKeyCount(lockTarget)
			}
			ts.Keys += count
			if ts.PerLanguage != nil {
				ts.PerLanguage[lang] = count
			}
		}

		output.Targets++
		output.Languages += ts.Languages
		output.Keys += ts.Keys
		output.ByTarget = append(output.ByTarget, ts)
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(output); err != nil {
			logError(T("JSON output error: %v"), err)
			os.Exit(1)
		}
		return
	}

	sectionHeader(T("Lock File"))
	keyVal(T("Path"), lf.Path())
	if !exists {
		keyVal(T("Status"), T("not found"))
		keyVal(T("Targets"), "0")
		keyVal(T("Languages"), "0")
		keyVal(T("Keys"), "0")
		return
	}

	keyVal(T("Targets"), fmt.Sprintf("%d", output.Targets))
	keyVal(T("Languages"), fmt.Sprintf("%d", output.Languages))
	keyVal(T("Keys"), fmt.Sprintf("%d", output.Keys))

	for _, ts := range output.ByTarget {
		keyVal(ts.Target, fmt.Sprintf(T("%d keys across %d languages"), ts.Keys, ts.Languages))
		if verbose {
			langs := make([]string, 0, len(ts.PerLanguage))
			for lang := range ts.PerLanguage {
				langs = append(langs, lang)
			}
			sort.Strings(langs)
			for _, lang := range langs {
				keyVal("  "+lang, fmt.Sprintf(T("%d keys"), ts.PerLanguage[lang]))
			}
		}
	}
}

func runLockClean(targets []string, dryRun bool) {
	allResolved, err := loadResolvedTargets("")
	if err != nil {
		logError(T("%v"), err)
		os.Exit(1)
	}

	resolved := allResolved
	if len(targets) > 0 {
		resolved, err = filterResolvedTargetsByNames(allResolved, targets)
		if err != nil {
			logError(T("%v"), err)
			os.Exit(1)
		}
	}
	orphanScopes := orphanCleanupScopes(allResolved, resolved, targets)

	lf, err := lockfile.Load(rootDir)
	if err != nil {
		logError(T("Could not load lock file: %v"), err)
		os.Exit(1)
	}

	totalRemoved := 0
	hadErrors := false
	expectedTargets, blockedOrphanScopes := expectedLockTargets(allResolved)

	for _, rt := range resolved {
		sourceEntries, err := collectSourceEntries(rt)
		if err != nil {
			logWarning(T("[%s] %v"), rt.Target.Name, err)
			hadErrors = true
			blockedOrphanScopes[rt.Target.Name] = struct{}{}
			continue
		}

		keys := mapKeys(sourceEntries)
		langs := filterOutLang(rt.Languages, rt.Target.SourceLang)
		for _, lang := range langs {
			if rt.Target.Type == config.TargetTypePo4a {
				units := collectPo4aLockUnits(rt, lang)
				if len(units) == 0 {
					logWarning(T("[%s/%s] cannot read any po4a PO files"), rt.Target.Name, lang)
					hadErrors = true
					blockedOrphanScopes[lockfile.LockTargetKey(rt.Target.Name, lang)] = struct{}{}
					continue
				}
				for _, unit := range units {
					unitKeys := mapKeys(unit.sourceEntries)
					if dryRun {
						beforeKeys := lf.TargetKeyCount(unit.lockTarget)
						removed := countStaleKeys(lf.TargetKeys(unit.lockTarget), unitKeys)
						if removed > 0 || beforeKeys > 0 {
							logInfo(T("[%s] stale entries: %d (tracked: %d)"), unit.lockTarget, removed, beforeKeys)
						}
						totalRemoved += removed
						continue
					}
					removed := lf.Clean(unit.lockTarget, unitKeys)
					if removed > 0 {
						logInfo(T("[%s] removed stale entries: %d"), unit.lockTarget, removed)
					}
					totalRemoved += removed
				}
				continue
			}

			lockTarget := lockfile.LockTargetKey(rt.Target.Name, lang)
			if dryRun {
				beforeKeys := lf.TargetKeyCount(lockTarget)
				removed := countStaleKeys(lf.TargetKeys(lockTarget), keys)
				if removed > 0 || beforeKeys > 0 {
					logInfo(T("[%s/%s] stale entries: %d (tracked: %d)"), rt.Target.Name, lang, removed, beforeKeys)
				}
				totalRemoved += removed
				continue
			}

			removed := lf.Clean(lockTarget, keys)
			if removed > 0 {
				logInfo(T("[%s/%s] removed stale entries: %d"), rt.Target.Name, lang, removed)
			}
			totalRemoved += removed
		}
	}

	if len(blockedOrphanScopes) > 0 {
		logWarning(T("Skipping orphan lock target cleanup for scopes with unresolved sources"))
	}
	for _, orphan := range orphanLockTargets(lf, expectedTargets, orphanScopes, blockedOrphanScopes) {
		tracked := lf.TargetKeyCount(orphan)
		if dryRun {
			logInfo(T("[%s] orphan lock target: %d tracked"), orphan, tracked)
			totalRemoved += tracked
			continue
		}

		lf.RemoveTarget(orphan)
		logInfo(T("[%s] removed orphan lock target: %d"), orphan, tracked)
		totalRemoved += tracked
	}

	if dryRun {
		logInfo(T("Dry run: total stale/orphan entries: %d"), totalRemoved)
		if hadErrors {
			os.Exit(1)
		}
		return
	}

	if err := lf.Save(); err != nil {
		logError(T("Could not save lock file: %v"), err)
		os.Exit(1)
	}
	logSuccess(T("Removed stale/orphan entries: %d"), totalRemoved)

	if hadErrors {
		os.Exit(1)
	}
}

func runLockReset(target, lang string, yes bool) {
	lf, err := lockfile.Load(rootDir)
	if err != nil {
		logError(T("Could not load lock file: %v"), err)
		os.Exit(1)
	}

	if lang != "" && target == "" {
		logError(T("--lang requires --target"))
		os.Exit(1)
	}

	if target != "" {
		if _, err := loadResolvedTargets(target); err != nil {
			logError(T("%v"), err)
			os.Exit(1)
		}
	}

	if target == "" {
		if !yes && !confirmAction(T("Reset entire lock file?")) {
			logInfo(T("Cancelled"))
			return
		}
		if err := os.Remove(lf.Path()); err != nil && !errors.Is(err, os.ErrNotExist) {
			logError(T("Removing %s: %v"), lf.Path(), err)
			os.Exit(1)
		}
		logSuccess(T("Reset complete: %s removed"), lf.Path())
		return
	}

	if lang != "" {
		lockTarget := lockfile.LockTargetKey(target, lang)
		lf.RemoveTarget(lockTarget)
	} else {
		prefix := target + "/"
		for _, t := range lf.Targets() {
			if strings.HasPrefix(t, prefix) {
				lf.RemoveTarget(t)
			}
		}
	}

	if err := lf.Save(); err != nil {
		logError(T("Could not save lock file: %v"), err)
		os.Exit(1)
	}

	if lang != "" {
		logSuccess(T("Reset complete: %s/%s"), target, lang)
		return
	}
	logSuccess(T("Reset complete: target %s"), target)
}

func loadResolvedTargets(target string) ([]config.ResolvedTarget, error) {
	lf, err := config.LoadLokitFile(rootDir)
	if err != nil {
		return nil, fmt.Errorf(T("Config error: %v"), err)
	}
	if lf == nil {
		return nil, fmt.Errorf(T("No lokit.yaml found in %s"), rootDir)
	}

	resolved, err := lf.Resolve(rootDir)
	if err != nil {
		return nil, fmt.Errorf(T("Config resolve error: %v"), err)
	}

	return filterResolvedTargetsByNames(resolved, []string{target})
}

func collectSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	switch rt.Target.Type {
	case config.TargetTypeGettext:
		return collectGettextSourceEntries(rt)
	case config.TargetTypePo4a:
		return collectPo4aSourceEntries(rt)
	case config.TargetTypeI18Next:
		return collectI18NextSourceEntries(rt)
	case config.TargetTypeVueI18n:
		if rt.Target.Source != nil && rt.Target.Source.IsIndex() {
			return collectIndexSourceEntries(rt)
		}
		return collectVueI18nSourceEntries(rt)
	case config.TargetTypeAndroid:
		return collectAndroidSourceEntries(rt)
	case config.TargetTypeYAML:
		return collectYAMLSourceEntries(rt)
	case config.TargetTypeMarkdown:
		return collectMarkdownSourceEntries(rt)
	case config.TargetTypeProperties:
		return collectPropertiesSourceEntries(rt)
	case config.TargetTypeFlutter:
		return collectFlutterSourceEntries(rt)
	case config.TargetTypeJSKV:
		return collectJSKVSourceEntries(rt)
	case config.TargetTypeDesktop:
		return collectDesktopSourceEntries(rt)
	case config.TargetTypePolkit:
		return collectPolkitSourceEntries(rt)
	default:
		return nil, fmt.Errorf(T("unsupported target type %q"), rt.Target.Type)
	}
}

type kvSourceFile interface {
	SourceValues() map[string]string
}

func filterSourceEntriesByTranslatedKeys(sourceEntries map[string]string, translatedKeys map[string]struct{}) map[string]string {
	if len(sourceEntries) == 0 || len(translatedKeys) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(translatedKeys))
	for key, content := range sourceEntries {
		if _, ok := translatedKeys[key]; ok {
			out[key] = content
		}
	}
	return out
}

func translatedKeysFromKVFile(file formatfile.KVFile) map[string]struct{} {
	all := file.Keys()
	untranslated := make(map[string]struct{}, len(all))
	for _, key := range file.UntranslatedKeys() {
		untranslated[key] = struct{}{}
	}

	translated := make(map[string]struct{}, len(all))
	for _, key := range all {
		if _, ok := untranslated[key]; ok {
			continue
		}
		translated[key] = struct{}{}
	}
	return translated
}

func collectTranslatedKeys(rt config.ResolvedTarget, lang string) (map[string]struct{}, error) {
	switch rt.Target.Type {
	case config.TargetTypeGettext:
		poPath := rt.POPath(lang)
		catalog, err := po.ParseFile(poPath)
		if err != nil {
			return nil, fmt.Errorf(T("cannot read PO %s: %v"), poPath, err)
		}
		keys := make(map[string]struct{})
		for _, e := range catalog.Entries {
			if e.MsgID == "" || e.Obsolete || e.IsFuzzy() || !e.IsTranslated() {
				continue
			}
			key := lockfile.POEntryKey(e.MsgID, e.MsgCtxt)
			keys[key] = struct{}{}
		}
		return keys, nil

	case config.TargetTypePo4a:
		poPath := rt.DocsPOPath(lang)
		catalog, err := po.ParseFile(poPath)
		if err != nil {
			return nil, fmt.Errorf(T("cannot read PO %s: %v"), poPath, err)
		}
		keys := make(map[string]struct{})
		for _, e := range catalog.Entries {
			if e.MsgID == "" || e.Obsolete || e.IsFuzzy() || !e.IsTranslated() {
				continue
			}
			key := lockfile.POEntryKey(e.MsgID, e.MsgCtxt)
			keys[key] = struct{}{}
		}
		return keys, nil

	case config.TargetTypeI18Next:
		return collectTranslatedSimpleKV(rt.TranslationPath(lang), func(path string) (formatfile.KVFile, error) {
			return i18next.ParseFile(path)
		})

	case config.TargetTypeVueI18n:
		return collectTranslatedSimpleKV(rt.TranslationPath(lang), func(path string) (formatfile.KVFile, error) {
			return vuei18n.ParseFile(path)
		})

	case config.TargetTypeAndroid:
		path := android.StringsXMLPath(rt.AbsResDir(), lang)
		f, err := android.ParseFile(path)
		if err != nil {
			return nil, err
		}
		return collectTranslatedAndroidKeys(f), nil

	case config.TargetTypeYAML:
		return collectTranslatedSimpleKV(rt.TranslationPath(lang), func(path string) (formatfile.KVFile, error) {
			return yamlfile.ParseFile(path)
		})

	case config.TargetTypeMarkdown:
		return collectTranslatedMarkdownKeys(rt, lang)

	case config.TargetTypeProperties:
		return collectTranslatedSimpleKV(rt.TranslationPath(lang), func(path string) (formatfile.KVFile, error) {
			return propfile.ParseFile(path)
		})

	case config.TargetTypeFlutter:
		return collectTranslatedSimpleKV(rt.TranslationPath(lang), func(path string) (formatfile.KVFile, error) {
			return arbfile.ParseFile(path)
		})

	case config.TargetTypeJSKV:
		return collectTranslatedSimpleKV(rt.TranslationPath(lang), func(path string) (formatfile.KVFile, error) {
			return jskv.ParseFile(path)
		})

	case config.TargetTypeDesktop:
		path := rt.SourcePath()
		return collectTranslatedSimpleKV(path, func(p string) (formatfile.KVFile, error) {
			return desktop.ParseFile(p, lang)
		})

	case config.TargetTypePolkit:
		path := rt.SourcePath()
		return collectTranslatedSimpleKV(path, func(p string) (formatfile.KVFile, error) {
			return polkit.ParseFile(p, lang)
		})

	default:
		return nil, fmt.Errorf(T("unsupported target type %q"), rt.Target.Type)
	}
}

type po4aLockUnit struct {
	lockTarget     string
	sourceEntries  map[string]string
	translatedKeys map[string]struct{}
}

func collectPo4aLockUnits(rt config.ResolvedTarget, lang string) []po4aLockUnit {
	files := rt.DocsPOFiles(lang)
	units := make([]po4aLockUnit, 0, len(files))
	for _, file := range files {
		catalog, err := po.ParseFile(file.Path)
		if err != nil {
			continue
		}
		sourceEntries := make(map[string]string)
		translatedKeys := make(map[string]struct{})
		for _, e := range catalog.Entries {
			if e.MsgID == "" || e.Obsolete {
				continue
			}
			key := lockfile.POEntryKey(e.MsgID, e.MsgCtxt)
			sourceEntries[key] = lockfile.POEntryContent(e.MsgID, e.MsgIDPlural)
			if !e.IsFuzzy() && e.IsTranslated() {
				translatedKeys[key] = struct{}{}
			}
		}
		units = append(units, po4aLockUnit{
			lockTarget:     lockfile.LockTargetKey(rt.Target.Name+"/"+file.Master, lang),
			sourceEntries:  sourceEntries,
			translatedKeys: translatedKeys,
		})
	}
	return units
}

func lockTargetKeysFor(rt config.ResolvedTarget, lang string) []string {
	keys, ok := po4aAwareLockTargetKeysFor(rt, lang)
	if !ok {
		return []string{lockfile.LockTargetKey(rt.Target.Name, lang)}
	}
	return keys
}

func collectTranslatedSimpleKV(path string, parse func(path string) (formatfile.KVFile, error)) (map[string]struct{}, error) {
	file, err := parse(path)
	if err != nil {
		return nil, err
	}
	return translatedKeysFromKVFile(file), nil
}

func collectTranslatedMarkdownKeys(rt config.ResolvedTarget, lang string) (map[string]struct{}, error) {
	srcDir := markdownSourceDir(rt)
	srcFiles, err := discoverMarkdownFiles(srcDir)
	if err != nil {
		return nil, fmt.Errorf(T("cannot read markdown files in %s: %v"), srcDir, err)
	}

	translated := make(map[string]struct{})
	for _, srcPath := range srcFiles {
		relPath, err := filepath.Rel(srcDir, srcPath)
		if err != nil {
			continue
		}
		relSlash := filepath.ToSlash(relPath)
		targetPath := filepath.Join(markdownLangDir(rt, lang), filepath.FromSlash(relSlash))
		mf, err := mdfile.ParseFile(targetPath)
		if err != nil {
			continue
		}
		for key := range translatedKeysFromKVFile(mf) {
			translated[relSlash+":"+key] = struct{}{}
		}
	}

	return translated, nil
}

func collectTranslatedAndroidKeys(f *android.File) map[string]struct{} {
	translated := make(map[string]struct{})
	for _, e := range f.Entries {
		if !e.IsTranslatable() || e.IsComment() {
			continue
		}
		switch e.Kind {
		case android.KindString:
			if strings.TrimSpace(e.Value) != "" {
				translated[e.Name] = struct{}{}
			}
		case android.KindStringArray:
			for idx, v := range e.Items {
				if strings.TrimSpace(v) == "" {
					continue
				}
				translated[fmt.Sprintf("%s[%d]", e.Name, idx)] = struct{}{}
			}
		case android.KindPlurals:
			for _, q := range e.PluralOrder {
				v := e.Plurals[q]
				if strings.TrimSpace(v) == "" {
					continue
				}
				translated[fmt.Sprintf("%s#%s", e.Name, q)] = struct{}{}
			}
		}
	}
	return translated
}

func collectSimpleKVSourceEntries(rt config.ResolvedTarget, parse func(path string) (kvSourceFile, error)) (map[string]string, error) {
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}
	srcFile, err := parse(srcPath)
	if err != nil {
		return nil, fmt.Errorf(T("cannot read source file %s: %v"), srcPath, err)
	}

	entries := make(map[string]string)
	for key, v := range srcFile.SourceValues() {
		entries[key] = lockfile.KVEntryContent(key, v)
	}
	return entries, nil
}

func collectGettextSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	potPath := rt.AbsPOTFile()
	pot, err := po.ParseFile(potPath)
	if err != nil {
		return nil, fmt.Errorf(T("cannot read POT %s: %v"), potPath, err)
	}

	entries := make(map[string]string)
	for _, e := range pot.Entries {
		if e.MsgID == "" || e.Obsolete {
			continue
		}
		key := lockfile.POEntryKey(e.MsgID, e.MsgCtxt)
		entries[key] = lockfile.POEntryContent(e.MsgID, e.MsgIDPlural)
	}
	return entries, nil
}

func collectPo4aSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	entries := make(map[string]string)
	parsed := 0
	for _, lang := range rt.Languages {
		for _, file := range rt.DocsPOFiles(lang) {
			f, err := po.ParseFile(file.Path)
			if err != nil {
				continue
			}
			parsed++
			for _, e := range f.Entries {
				if e.MsgID == "" || e.Obsolete {
					continue
				}
				key := file.Master + ":" + lockfile.POEntryKey(e.MsgID, e.MsgCtxt)
				entries[key] = lockfile.POEntryContent(e.MsgID, e.MsgIDPlural)
			}
		}
	}

	if parsed == 0 {
		return nil, fmt.Errorf(T("cannot read any po4a PO files for target %s"), rt.Target.Name)
	}
	return entries, nil
}

func collectI18NextSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}
	srcFile, err := i18next.ParseFile(srcPath)
	if err != nil {
		return nil, fmt.Errorf(T("cannot read source file %s: %v"), srcPath, err)
	}

	entries := make(map[string]string)
	for _, key := range srcFile.Keys() {
		entries[key] = lockfile.KVEntryContent(key, srcFile.Translations[key])
	}
	return entries, nil
}

func collectVueI18nSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	return collectSimpleKVSourceEntries(rt, func(path string) (kvSourceFile, error) {
		return vuei18n.ParseFile(path)
	})
}

func collectAndroidSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	srcPath := android.SourceStringsXMLPath(rt.AbsResDir())
	srcFile, err := android.ParseFile(srcPath)
	if err != nil {
		return nil, fmt.Errorf(T("cannot read source strings.xml %s: %v"), srcPath, err)
	}

	srcVals := androidSourceValuesByUnit(srcFile)

	entries := make(map[string]string)
	for key, v := range srcVals {
		entries[key] = lockfile.KVEntryContent(key, v)
	}
	return entries, nil
}

func androidSourceValuesByUnit(srcFile *android.File) map[string]string {
	vals := make(map[string]string)
	for _, e := range srcFile.Entries {
		if !e.IsTranslatable() || e.IsComment() {
			continue
		}
		switch e.Kind {
		case android.KindString:
			vals[e.Name] = e.Value
		case android.KindStringArray:
			for idx, v := range e.Items {
				vals[fmt.Sprintf("%s[%d]", e.Name, idx)] = v
			}
		case android.KindPlurals:
			for _, q := range e.PluralOrder {
				vals[fmt.Sprintf("%s#%s", e.Name, q)] = e.Plurals[q]
			}
		}
	}
	return vals
}

func collectYAMLSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	return collectSimpleKVSourceEntries(rt, func(path string) (kvSourceFile, error) {
		return yamlfile.ParseFile(path)
	})
}

func collectMarkdownSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	srcDir := markdownSourceDir(rt)
	srcFiles, err := discoverMarkdownFiles(srcDir)
	if err != nil {
		return nil, fmt.Errorf(T("cannot read markdown files in %s: %v"), srcDir, err)
	}
	if len(srcFiles) == 0 {
		return nil, fmt.Errorf(T("cannot find source Markdown files in %s"), srcDir)
	}

	entries := make(map[string]string)
	for _, srcPath := range srcFiles {
		relPath, err := filepath.Rel(srcDir, srcPath)
		if err != nil {
			return nil, fmt.Errorf(T("cannot compute relative path for %s: %v"), srcPath, err)
		}
		lockPrefix := filepath.ToSlash(relPath)
		srcFile, err := mdfile.ParseFile(srcPath)
		if err != nil {
			return nil, fmt.Errorf(T("cannot read source Markdown file %s: %v"), srcPath, err)
		}
		for key, v := range srcFile.SourceValues() {
			lockKey := lockPrefix + ":" + key
			entries[lockKey] = lockfile.KVEntryContent(lockKey, v)
		}
	}
	return entries, nil
}

func collectPropertiesSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	return collectSimpleKVSourceEntries(rt, func(path string) (kvSourceFile, error) {
		return propfile.ParseFile(path)
	})
}

func collectFlutterSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	return collectSimpleKVSourceEntries(rt, func(path string) (kvSourceFile, error) {
		return arbfile.ParseFile(path)
	})
}

func collectJSKVSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}
	srcFile, err := jskv.ParseFile(srcPath)
	if err != nil {
		return nil, fmt.Errorf(T("cannot read source file %s: %v"), srcPath, err)
	}

	entries := make(map[string]string)
	for key, v := range srcFile.SourceValues() {
		entries[key] = lockfile.KVEntryContent(key, v)
	}
	return entries, nil
}

func collectDesktopSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	srcPath := rt.SourcePath()
	srcFile, err := desktop.ParseFile(srcPath, rt.Target.SourceLang)
	if err != nil {
		return nil, fmt.Errorf(T("cannot read source file %s: %v"), srcPath, err)
	}
	entries := make(map[string]string)
	for key, v := range srcFile.SourceValues() {
		entries[key] = lockfile.KVEntryContent(key, v)
	}
	return entries, nil
}

func collectPolkitSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	srcPath := rt.SourcePath()
	srcFile, err := polkit.ParseFile(srcPath, rt.Target.SourceLang)
	if err != nil {
		return nil, fmt.Errorf(T("cannot read source file %s: %v"), srcPath, err)
	}
	entries := make(map[string]string)
	for key, v := range srcFile.SourceValues() {
		entries[key] = lockfile.KVEntryContent(key, v)
	}
	return entries, nil
}

func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func countStaleKeys(tracked []string, current []string) int {
	if len(tracked) == 0 {
		return 0
	}
	currentSet := make(map[string]struct{}, len(current))
	for _, k := range current {
		currentSet[k] = struct{}{}
	}
	stale := 0
	for _, k := range tracked {
		if _, ok := currentSet[k]; !ok {
			stale++
		}
	}
	return stale
}

func expectedLockTargets(resolved []config.ResolvedTarget) (map[string]struct{}, map[string]struct{}) {
	expected := make(map[string]struct{})
	blockedScopes := make(map[string]struct{})
	for _, rt := range resolved {
		langs := filterOutLang(rt.Languages, rt.Target.SourceLang)
		for _, lang := range langs {
			keys, ok := po4aAwareLockTargetKeysFor(rt, lang)
			if !ok {
				blockedScopes[lockfile.LockTargetKey(rt.Target.Name, lang)] = struct{}{}
				continue
			}
			for _, key := range keys {
				expected[key] = struct{}{}
			}
		}
	}
	return expected, blockedScopes
}

func po4aAwareLockTargetKeysFor(rt config.ResolvedTarget, lang string) ([]string, bool) {
	if rt.Target.Type != config.TargetTypePo4a {
		return []string{lockfile.LockTargetKey(rt.Target.Name, lang)}, true
	}

	masters, err := rt.DocsPOMasters()
	if err == nil && len(masters) > 0 {
		keys := make([]string, 0, len(masters))
		for _, master := range masters {
			keys = append(keys, lockfile.LockTargetKey(rt.Target.Name+"/"+master, lang))
		}
		return keys, true
	}

	files := rt.DocsPOFiles(lang)
	if len(files) == 0 {
		return nil, false
	}

	keys := make([]string, 0, len(files))
	for _, file := range files {
		keys = append(keys, lockfile.LockTargetKey(rt.Target.Name+"/"+file.Master, lang))
	}
	return keys, true
}

type orphanScope struct {
	name       string
	targetType string
	langs      map[string]struct{}
	prefix     bool
}

func orphanCleanupScopes(allResolved, selected []config.ResolvedTarget, targets []string) []orphanScope {
	if len(targets) == 0 {
		return nil
	}

	selectedByName := make(map[string]config.ResolvedTarget, len(selected))
	for _, rt := range selected {
		selectedByName[rt.Target.Name] = rt
	}

	var scopes []orphanScope
	seen := make(map[string]struct{})
	for _, raw := range targets {
		for _, part := range strings.Split(raw, ",") {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}
			if rt, ok := selectedByName[name]; ok {
				if _, exists := seen["exact:"+name]; exists {
					continue
				}
				scopes = append(scopes, orphanScopeForTarget(rt))
				seen["exact:"+name] = struct{}{}
				continue
			}

			matchedPrefix := false
			for _, rt := range allResolved {
				if strings.HasPrefix(rt.Target.Name, name+"/") {
					matchedPrefix = true
					break
				}
			}
			if matchedPrefix {
				if _, exists := seen["prefix:"+name]; exists {
					continue
				}
				scopes = append(scopes, orphanScope{name: name, prefix: true})
				seen["prefix:"+name] = struct{}{}
			}
		}
	}
	return scopes
}

func orphanScopeForTarget(rt config.ResolvedTarget) orphanScope {
	langs := filterOutLang(rt.Languages, rt.Target.SourceLang)
	langSet := make(map[string]struct{}, len(langs))
	for _, lang := range langs {
		langSet[lang] = struct{}{}
	}
	return orphanScope{name: rt.Target.Name, targetType: rt.Target.Type, langs: langSet}
}

func orphanLockTargets(lf *lockfile.LockFile, expected map[string]struct{}, scopes []orphanScope, blockedScopes map[string]struct{}) []string {
	var orphans []string
	for _, target := range lf.Targets() {
		if _, ok := expected[target]; ok {
			continue
		}
		if !lockTargetInOrphanScopes(target, scopes) {
			continue
		}
		if lockTargetBlocked(target, blockedScopes) {
			continue
		}
		orphans = append(orphans, target)
	}
	return orphans
}

func lockTargetInOrphanScopes(lockTarget string, scopes []orphanScope) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, scope := range scopes {
		if scope.prefix {
			if lockTargetInScope(lockTarget, scope.name) {
				return true
			}
			continue
		}
		if lockTargetInExactTargetScope(lockTarget, scope) {
			return true
		}
	}
	return false
}

func lockTargetInExactTargetScope(lockTarget string, scope orphanScope) bool {
	for lang := range scope.langs {
		if lockTarget == lockfile.LockTargetKey(scope.name, lang) {
			return true
		}
		if scope.targetType == config.TargetTypePo4a && lockTargetMatchesLanguageScope(lockTarget, lockfile.LockTargetKey(scope.name, lang)) {
			return true
		}
	}
	return false
}

func lockTargetBlocked(lockTarget string, blockedScopes map[string]struct{}) bool {
	for scope := range blockedScopes {
		if lockTargetInScope(lockTarget, scope) || lockTargetMatchesLanguageScope(lockTarget, scope) {
			return true
		}
	}
	return false
}

func lockTargetMatchesLanguageScope(lockTarget, scope string) bool {
	parts := strings.Split(scope, "/")
	if len(parts) < 2 {
		return false
	}
	targetName := strings.Join(parts[:len(parts)-1], "/")
	lang := parts[len(parts)-1]
	return strings.HasPrefix(lockTarget, targetName+"/") && strings.HasSuffix(lockTarget, "/"+lang)
}

func lockTargetInScope(lockTarget, targetFilter string) bool {
	return lockTarget == targetFilter || strings.HasPrefix(lockTarget, targetFilter+"/")
}

func confirmAction(prompt string) bool {
	fmt.Fprintf(os.Stderr, "  %s [y/N]: ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return ans == "y" || ans == "yes"
}
