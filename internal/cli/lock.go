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
  clean   Remove stale lock entries not present in source
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
	var target string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "clean",
		Short: T("Remove stale lock entries"),
		Long: T(`Remove lock entries that no longer exist in source files.

Examples:
  lokit lock clean
  lokit lock clean --target ui
  lokit lock clean --dry-run`),
		Run: func(cmd *cobra.Command, args []string) {
			runLockClean(target, dryRun)
		},
	}

	cmd.Flags().StringVar(&target, "target", "", T("Target name from lokit.yaml (default: all targets)"))
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, T("Show stale entries without modifying lokit.lock"))
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
			lockTarget := lockfile.LockTargetKey(rt.Target.Name, lang)
			if force {
				lf.RemoveTarget(lockTarget)
			}
			for key, content := range sourceEntries {
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
			lockTarget := lockfile.LockTargetKey(rt.Target.Name, lang)
			count := lf.TargetKeyCount(lockTarget)
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

func runLockClean(target string, dryRun bool) {
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

	totalRemoved := 0
	hadErrors := false

	for _, rt := range resolved {
		sourceEntries, err := collectSourceEntries(rt)
		if err != nil {
			logWarning(T("[%s] %v"), rt.Target.Name, err)
			hadErrors = true
			continue
		}

		keys := mapKeys(sourceEntries)
		langs := filterOutLang(rt.Languages, rt.Target.SourceLang)
		for _, lang := range langs {
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

	if dryRun {
		logInfo(T("Dry run: total stale entries: %d"), totalRemoved)
		if hadErrors {
			os.Exit(1)
		}
		return
	}

	if err := lf.Save(); err != nil {
		logError(T("Could not save lock file: %v"), err)
		os.Exit(1)
	}
	logSuccess(T("Removed stale entries: %d"), totalRemoved)

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

	if target == "" {
		return resolved, nil
	}

	for _, rt := range resolved {
		if rt.Target.Name == target {
			return []config.ResolvedTarget{rt}, nil
		}
	}

	var matches []config.ResolvedTarget
	for _, rt := range resolved {
		if strings.HasPrefix(rt.Target.Name, target+"/") {
			matches = append(matches, rt)
		}
	}
	if len(matches) > 0 {
		return matches, nil
	}

	return nil, fmt.Errorf(T("Target %q not found in lokit.yaml"), target)
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
		poPath := rt.DocsPOPath(lang)
		f, err := po.ParseFile(poPath)
		if err != nil {
			continue
		}
		parsed++
		for _, e := range f.Entries {
			if e.MsgID == "" || e.Obsolete {
				continue
			}
			key := lockfile.POEntryKey(e.MsgID, e.MsgCtxt)
			entries[key] = lockfile.POEntryContent(e.MsgID, e.MsgIDPlural)
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
	srcDir := filepath.Join(rt.AbsTranslationsDir(), rt.Target.SourceLang)
	srcFiles, err := filepath.Glob(filepath.Join(srcDir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf(T("cannot read markdown files in %s: %v"), srcDir, err)
	}
	if len(srcFiles) == 0 {
		return nil, fmt.Errorf(T("cannot find source Markdown files in %s"), srcDir)
	}

	entries := make(map[string]string)
	for _, srcPath := range srcFiles {
		srcFile, err := mdfile.ParseFile(srcPath)
		if err != nil {
			return nil, fmt.Errorf(T("cannot read source Markdown file %s: %v"), srcPath, err)
		}
		for key, v := range srcFile.SourceValues() {
			entries[key] = lockfile.KVEntryContent(key, v)
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

func confirmAction(prompt string) bool {
	fmt.Fprintf(os.Stderr, "  %s [y/N]: ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return ans == "y" || ans == "yes"
}
