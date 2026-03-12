package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minios-linux/lokit/config"
	. "github.com/minios-linux/lokit/i18n"
	"github.com/minios-linux/lokit/lockfile"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var targets []string

	cmd := &cobra.Command{
		Use:   "status",
		Short: T("Show project info and translation statistics"),
		Long: T(`Show auto-detected project structure and translation statistics.

Displays target format, file structure, detected languages, and per-language
translation progress for gettext, po4a, i18next, vue-i18n, android,
yaml, markdown, properties, flutter, js-kv, desktop, and polkit projects. For projects
configured via lokit.yaml, shows each target separately.

		Does not modify any files.`),
		Run: func(cmd *cobra.Command, args []string) {
			runStatus(targets)
		},
	}

	cmd.Flags().StringSliceVar(&targets, "target", nil, T("Target name from lokit.yaml (repeat flag or use comma-separated list; default: all targets)"))

	return cmd
}

func runStatus(targets []string) {
	lf, err := config.LoadLokitFile(rootDir)
	if err != nil {
		logError(T("Config error: %v"), err)
		os.Exit(1)
	}
	if lf != nil {
		runStatusWithConfig(lf, targets)
		return
	}

	logError(T("No lokit.yaml found in %s"), rootDir)
	logInfo(T("Create a lokit.yaml configuration file. See 'lokit init --help' for format reference."))
	os.Exit(1)
}

func runStatusWithConfig(lf *config.LokitFile, targets []string) {
	absRoot, _ := filepath.Abs(rootDir)

	sectionHeader(T("Project"))
	keyVal(T("Config"), "lokit.yaml")
	keyVal(T("Root"), absRoot)
	keyVal(T("Source lang"), lf.SourceLang)

	if len(lf.Languages) > 0 {
		keyVal(T("Languages"), strings.Join(lf.Languages, ", "))
	}
	keyVal(T("Targets"), fmt.Sprintf("%d", len(lf.Targets)))

	lockF, err := lockfile.Load(rootDir)
	lockPath := filepath.Join(rootDir, lockfile.LockFileName)
	lockExists := true
	if _, statErr := os.Stat(lockPath); os.IsNotExist(statErr) {
		lockExists = false
	}
	if err != nil {
		keyVal(T("Lock file"), colorYellow+fmt.Sprintf(T("error: %v"), err)+colorReset)
		lockF = &lockfile.LockFile{Version: lockfile.Version, Checksums: make(map[string]map[string]string)}
	} else if !lockExists {
		keyVal(T("Lock file"), T("not found"))
	} else {
		lockTargets, keys := lockF.Stats()
		if lockTargets == 0 {
			keyVal(T("Lock file"), T("empty"))
		} else {
			keyVal(T("Lock file"), fmt.Sprintf(T("%d targets, %d keys"), lockTargets, keys))
		}
	}

	resolved, err := lf.Resolve(rootDir)
	if err != nil {
		logError(T("Config resolve error: %v"), err)
		os.Exit(1)
	}
	resolved, err = filterResolvedTargetsByNames(resolved, targets)
	if err != nil {
		logError(T("%v"), err)
		os.Exit(1)
	}

	for _, rt := range resolved {
		langs := filterOutLang(rt.Languages, rt.Target.SourceLang)

		targetHeader(rt.Target.Name, rt.Target.Type)
		keyVal(T("Root"), rt.Target.Root)

		lockKeys := 0
		for _, lang := range langs {
			lockKeys += lockF.TargetKeyCount(lockfile.LockTargetKey(rt.Target.Name, lang))
		}
		keyVal(T("Locked"), fmt.Sprintf(T("%d keys x %d languages"), lockKeys, len(langs)))

		if len(langs) > 0 {
			keyVal(T("Languages"), strings.Join(langs, ", "))
		} else {
			keyVal(T("Languages"), colorYellow+T("none detected")+colorReset)
		}

		switch rt.Target.Type {
		case config.TargetTypeGettext:
			showConfigGettextStats(rt, langs)
		case config.TargetTypePo4a:
			showConfigPo4aStats(rt, langs)
		case config.TargetTypeI18Next:
			showConfigI18NextStats(rt, langs)
		case config.TargetTypeVueI18n:
			if rt.Target.Source != nil && rt.Target.Source.IsIndex() {
				showConfigIndexStats(rt, langs)
			} else {
				showConfigVueI18nStats(rt, langs)
			}
		case config.TargetTypeAndroid:
			showConfigAndroidStats(rt, langs)
		case config.TargetTypeYAML:
			showConfigYAMLStats(rt, langs)
		case config.TargetTypeMarkdown:
			showConfigMarkdownStats(rt, langs)
		case config.TargetTypeProperties:
			showConfigPropertiesStats(rt, langs)
		case config.TargetTypeFlutter:
			showConfigFlutterStats(rt, langs)
		case config.TargetTypeJSKV:
			showConfigJSKVStats(rt, langs)
		case config.TargetTypeDesktop:
			showConfigDesktopStats(rt, langs)
		case config.TargetTypePolkit:
			showConfigPolkitStats(rt, langs)
		default:
			logWarning(T("[%s] Unknown target type %q, skipping"), rt.Target.Name, rt.Target.Type)
		}

		fmt.Fprintln(os.Stderr)
	}
}
