package cli

import (
	"bufio"
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
	"github.com/minios-linux/lokit/internal/format/i18next"
	mdfile "github.com/minios-linux/lokit/internal/format/markdown"
	po "github.com/minios-linux/lokit/internal/format/po"
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

	cmd := &cobra.Command{
		Use:   "status",
		Short: T("Show lock file statistics"),
		Long: T(`Show statistics for lokit.lock, including tracked targets and keys.

Examples:
  lokit lock status
  lokit lock status --target ui`),
		Run: func(cmd *cobra.Command, args []string) {
			runLockStatus(target)
		},
	}

	cmd.Flags().StringVar(&target, "target", "", T("Target name from lokit.yaml (default: all targets)"))
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

func runLockStatus(target string) {
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

	sectionHeader(T("Lock File"))
	keyVal(T("Path"), lf.Path())
	targets, keys := lf.Stats()
	keyVal(T("Targets"), fmt.Sprintf("%d", targets))
	keyVal(T("Keys"), fmt.Sprintf("%d", keys))

	for _, rt := range resolved {
		langs := filterOutLang(rt.Languages, rt.Target.SourceLang)
		if len(langs) == 0 {
			continue
		}

		total := 0
		for _, lang := range langs {
			lockTarget := lockfile.LockTargetKey(rt.Target.Name, lang)
			total += lf.TargetKeyCount(lockTarget)
		}
		keyVal(rt.Target.Name, fmt.Sprintf(T("%d keys across %d languages"), total, len(langs)))
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

	return nil, fmt.Errorf(T("Target %q not found in lokit.yaml"), target)
}

func collectSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	switch rt.Target.Type {
	case config.TargetTypeGettext:
		return collectGettextSourceEntries(rt)
	case config.TargetTypePo4a:
		return collectPo4aSourceEntries(rt)
	case config.TargetTypeI18Next, config.TargetTypeJSON:
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
		return collectARBSourceEntries(rt)
	default:
		return nil, fmt.Errorf(T("unsupported target type %q"), rt.Target.Type)
	}
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
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}
	srcFile, err := vuei18n.ParseFile(srcPath)
	if err != nil {
		return nil, fmt.Errorf(T("cannot read source file %s: %v"), srcPath, err)
	}

	entries := make(map[string]string)
	for key, v := range srcFile.SourceValues() {
		entries[key] = lockfile.KVEntryContent(key, v)
	}
	return entries, nil
}

func collectAndroidSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	srcPath := android.SourceStringsXMLPath(rt.AbsResDir())
	srcFile, err := android.ParseFile(srcPath)
	if err != nil {
		return nil, fmt.Errorf(T("cannot read source strings.xml %s: %v"), srcPath, err)
	}

	srcVals := make(map[string]string)
	for _, e := range srcFile.Entries {
		if !e.IsTranslatable() || e.IsComment() {
			continue
		}
		switch e.Kind {
		case android.KindString:
			srcVals[e.Name] = e.Value
		case android.KindStringArray:
			srcVals[e.Name] = strings.Join(e.Items, "\x00")
		case android.KindPlurals:
			var parts []string
			for _, q := range e.PluralOrder {
				parts = append(parts, q+"="+e.Plurals[q])
			}
			srcVals[e.Name] = strings.Join(parts, "\x00")
		}
	}

	entries := make(map[string]string)
	for key, v := range srcVals {
		entries[key] = lockfile.KVEntryContent(key, v)
	}
	return entries, nil
}

func collectYAMLSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}
	srcFile, err := yamlfile.ParseFile(srcPath)
	if err != nil {
		return nil, fmt.Errorf(T("cannot read source file %s: %v"), srcPath, err)
	}

	entries := make(map[string]string)
	for key, v := range srcFile.SourceValues() {
		entries[key] = lockfile.KVEntryContent(key, v)
	}
	return entries, nil
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
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}
	srcFile, err := propfile.ParseFile(srcPath)
	if err != nil {
		return nil, fmt.Errorf(T("cannot read source file %s: %v"), srcPath, err)
	}

	entries := make(map[string]string)
	for key, v := range srcFile.SourceValues() {
		entries[key] = lockfile.KVEntryContent(key, v)
	}
	return entries, nil
}

func collectARBSourceEntries(rt config.ResolvedTarget) (map[string]string, error) {
	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}
	srcFile, err := arbfile.ParseFile(srcPath)
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
