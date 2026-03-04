// lokit — Localization Kit: gettext PO file manager with AI translation support.
package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/minios-linux/lokit/config"
	. "github.com/minios-linux/lokit/i18n"
	"github.com/minios-linux/lokit/internal/format/android"
	"github.com/minios-linux/lokit/internal/format/i18next"
	po "github.com/minios-linux/lokit/internal/format/po"
	"github.com/minios-linux/lokit/translate"
	"github.com/spf13/cobra"
)

// Version information (set via -ldflags during build)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// ANSI colors
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[0;31m"
	colorGreen  = "\033[0;32m"
	colorYellow = "\033[0;33m"
	colorBlue   = "\033[0;34m"
	colorCyan   = "\033[0;36m"
	colorDim    = "\033[2m"
	colorBold   = "\033[1m"
)

func logInfo(format string, args ...any) {
	fmt.Fprintf(os.Stderr, colorCyan+"  → "+colorReset+format+"\n", args...)
}

func logSuccess(format string, args ...any) {
	fmt.Fprintf(os.Stderr, colorGreen+"  ✓ "+colorReset+format+"\n", args...)
}

func logWarning(format string, args ...any) {
	fmt.Fprintf(os.Stderr, colorYellow+"  ⚠ "+colorReset+format+"\n", args...)
}

func logError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, colorRed+"  ✗ "+colorReset+format+"\n", args...)
}

// compileLockedPatterns compiles locked_patterns strings into regexps.
// Invalid patterns are logged as warnings and skipped.
func compileLockedPatterns(patterns []string) []*regexp.Regexp {
	if len(patterns) == 0 {
		return nil
	}
	var compiled []*regexp.Regexp
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			logWarning(T("Invalid locked_pattern %q: %v"), p, err)
			continue
		}
		compiled = append(compiled, re)
	}
	return compiled
}

// setExclusionOpts populates the locked/ignored key fields on translate.Options
// from the given target configuration.
func setExclusionOpts(opts *translate.Options, t *config.Target) {
	opts.LockedKeys = t.LockedKeys
	opts.IgnoredKeys = t.IgnoredKeys
	opts.LockedPatterns = compileLockedPatterns(t.LockedPatterns)
}

// progressBar renders a text progress bar: [████████░░░░] 75%
func progressBar(percent, width int) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	filled := width * percent / 100
	empty := width - filled

	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)

	color := colorGreen
	if percent < 50 {
		color = colorRed
	} else if percent < 100 {
		color = colorYellow
	}

	return fmt.Sprintf("%s%s%s %3d%%", color, bar, colorReset, percent)
}

// sectionHeader prints a styled section header.
func sectionHeader(title string) {
	fmt.Fprintf(os.Stderr, "\n%s%s %s %s%s\n", colorBold, colorBlue, title, colorReset, "")
	fmt.Fprintln(os.Stderr, colorDim+"  "+strings.Repeat("─", 58)+colorReset)
}

// targetHeader prints a styled target header.
func targetHeader(name, targetType string) {
	fmt.Fprintf(os.Stderr, "\n%s%s ● %s%s %s(%s)%s\n",
		colorBold, colorBlue, name, colorReset,
		colorDim, targetType, colorReset)
}

// keyVal prints a key-value pair with consistent alignment.
func keyVal(key, value string) {
	fmt.Fprintf(os.Stderr, "  %s%-14s%s %s\n", colorDim, key, colorReset, value)
}

func flagFromRegion(region string) string {
	if len(region) != 2 {
		return ""
	}
	region = strings.ToUpper(region)
	runes := []rune(region)
	if runes[0] < 'A' || runes[0] > 'Z' || runes[1] < 'A' || runes[1] > 'Z' {
		return ""
	}
	const regionalOffset = 0x1F1E6 - 'A'
	return string([]rune{runes[0] + regionalOffset, runes[1] + regionalOffset})
}

// langFlag returns the flag emoji for a language code, or empty string if unknown.
func langFlag(lang string) string {
	meta := i18next.ResolveMeta(lang)
	if meta.Flag != "" {
		return meta.Flag
	}
	normalized := strings.ReplaceAll(lang, "_", "-")
	// Locale fallback: xx-YY -> derive flag from region (YY).
	if parts := strings.SplitN(normalized, "-", 2); len(parts) == 2 {
		if f := flagFromRegion(parts[1]); f != "" {
			return f
		}
		if m := i18next.ResolveMeta(parts[0]); m.Flag != "" {
			return m.Flag
		}
	}

	return ""
}

func langColumnWidth(langs []string) int {
	width := 4
	for _, lang := range langs {
		if w := utf8.RuneCountInString(lang); w > width {
			width = w
		}
	}
	return width
}

func langCell(lang string, width int) string {
	return fmt.Sprintf("%s %-*s", langFlag(lang), width, lang)
}

// ---------------------------------------------------------------------------
// Global flag
// ---------------------------------------------------------------------------

var rootDir string

// ---------------------------------------------------------------------------
// Root command
// ---------------------------------------------------------------------------

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "lokit",
		Short: T("Localization Kit: AI-powered translation for multiple formats"),
		Long: T(`lokit — Localization Kit: AI-powered translation for multiple formats.

Supported project types (auto-detected or configured via lokit.yaml):
  gettext     Flat (po/*.po) and nested (po/lang/*.po) PO files
  po4a        Documentation translation (man pages, groff/roff markup)
  i18next     React/i18next JSON translation files
  vue-i18n    Vue i18n nested JSON translation files
  android     Android strings.xml resource files
  json        Generic JSON translation files
  yaml        YAML translation files
  markdown    Markdown document translation
  properties  Java .properties translation files
  flutter     Flutter ARB (Application Resource Bundle) files

Configuration:
  Project settings are defined in lokit.yaml at the project root.
  Each project can have multiple translation targets with different types.
  See project examples for lokit.yaml format.

AI Providers:
  google         Google AI (Gemini) — API key
  gemini         Gemini Code Assist — browser OAuth (free)
  groq           Groq — API key required
  opencode       OpenCode Zen API (free models available without key)
  copilot        GitHub Copilot (native OAuth, free)
  ollama         Ollama local server
  custom-openai  Custom OpenAI-compatible endpoint`),
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Register template helper functions
	cobra.AddTemplateFunc("localizeFlags", func(s string) string {
		return strings.ReplaceAll(s, "(default ", "("+T("default")+" ")
	})
	cobra.AddTemplateFunc("moreInfo", func(cmdPath string) string {
		return fmt.Sprintf(T("Use \"%s [command] --help\" for more information about a command."), cmdPath)
	})

	// Override usage template with translated section headers.
	// We use T() at template construction time (after Init) so strings are
	// both extractable by the AST extractor and translated at runtime.
	root.SetUsageTemplate(T("Usage:") + `{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

` + T("Aliases:") + `
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

` + T("Examples:") + `
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

` + T("Available Commands:") + `{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

` + T("Additional Commands:") + `{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

` + T("Flags:") + `
{{.LocalFlags.FlagUsages | localizeFlags | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

` + T("Global Flags:") + `
{{.InheritedFlags.FlagUsages | localizeFlags | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

` + T("Additional help topics:") + `{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

{{moreInfo .CommandPath}}{{end}}
`)

	// Override the help subcommand with translated text
	root.SetHelpCommand(&cobra.Command{
		Use:   "help [command]",
		Short: T("Help about any command"),
		Long:  fmt.Sprintf(T("Help provides help for any command in the application.\nSimply type %s help [path to command] for full details."), "lokit"),
		ValidArgsFunction: func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			var completions []string
			cmd, _, e := c.Root().Find(args)
			if e != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			if cmd == nil {
				cmd = c.Root()
			}
			for _, subCmd := range cmd.Commands() {
				if subCmd.IsAvailableCommand() || subCmd.Name() == "help" {
					if strings.HasPrefix(subCmd.Name(), toComplete) {
						completions = append(completions, fmt.Sprintf("%s\t%s", subCmd.Name(), subCmd.Short))
					}
				}
			}
			return completions, cobra.ShellCompDirectiveNoFileComp
		},
		Run: func(c *cobra.Command, args []string) {
			cmd, _, e := c.Root().Find(args)
			if cmd == nil || e != nil {
				c.Printf(T("Unknown help topic %q\n"), args)
				cobra.CheckErr(c.Root().Usage())
			} else {
				cmd.InitDefaultHelpFlag()
				cobra.CheckErr(cmd.Help())
			}
		},
	})

	// Global persistent flag — inherited by all subcommands
	root.PersistentFlags().StringVar(&rootDir, "root", ".", T("Project root directory"))

	root.AddCommand(
		newStatusCmd(),
		newInitCmd(),
		newTranslateCmd(),
		newLockCmd(),
		newAuthCmd(),
		newVersionCmd(),
	)

	return root
}

// translateHelpFlags recursively sets translated help flag usage on all commands.
// Must be called after all AddCommand() calls but before Execute().
func translateHelpFlags(cmd *cobra.Command) {
	name := cmd.Name()
	if name == "" {
		name = T("this command")
	}
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf(T("help for %s"), name))
	for _, sub := range cmd.Commands() {
		translateHelpFlags(sub)
	}
}

func Execute() {
	Init("")
	root := newRootCmd()
	translateHelpFlags(root)
	root.SetArgs(normalizeCLIArgs(os.Args[1:]))
	if err := root.Execute(); err != nil {
		logError(T("%v"), err)
		os.Exit(1)
	}
}

// normalizeCLIArgs applies small compatibility rewrites before Cobra parsing.
// It allows both `--parallel` (defaults to 3) and `--parallel 10` forms.
func normalizeCLIArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--parallel" {
			if i+1 < len(args) && args[i+1] != "--" && !strings.HasPrefix(args[i+1], "-") {
				out = append(out, arg)
				continue
			}
			out = append(out, "--parallel=3")
			continue
		}
		out = append(out, arg)
	}
	return out
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// status (read-only: project info + translation stats)
// ---------------------------------------------------------------------------

// showConfigGettextStats shows translation stats for a gettext target.
func showConfigGettextStats(rt config.ResolvedTarget, langs []string) {
	potPath := rt.AbsPOTFile()

	potTotal := 0
	if potPO, err := po.ParseFile(potPath); err == nil {
		for _, e := range potPO.Entries {
			if e.MsgID != "" && !e.Obsolete {
				potTotal++
			}
		}
	}

	if potTotal == 0 {
		keyVal(T("POT"), colorYellow+T("not found")+colorReset+" ("+T("run 'lokit init'")+")")
		return
	}

	keyVal(T("Source strings"), fmt.Sprintf("%d", potTotal))
	langWidth := langColumnWidth(langs)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s%-*s %-22s %5s %5s %5s%s\n",
		colorDim, langWidth+3, T("Lang"), T("Progress"), T("Done"), T("Fuzzy"), T("Left"), colorReset)
	fmt.Fprintln(os.Stderr, "  "+colorDim+strings.Repeat("─", 52)+colorReset)

	for _, lang := range langs {
		poPath := rt.POPath(lang)
		poFile, err := po.ParseFile(poPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n", langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("missing"), colorReset)
			continue
		}

		_, translated, fuzzy, untranslated := poFile.Stats()
		percent := 0
		if potTotal > 0 {
			percent = translated * 100 / potTotal
		}

		fmt.Fprintf(os.Stderr, "  %s %s %5d %5d %5d\n",
			langCell(lang, langWidth), progressBar(percent, 16), translated, fuzzy, untranslated)
	}
}

// showConfigPo4aStats shows translation stats for a po4a target.
func showConfigPo4aStats(rt config.ResolvedTarget, langs []string) {
	cfgPath := rt.AbsPo4aConfig()
	keyVal(T("po4a config"), cfgPath)
	langWidth := langColumnWidth(langs)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s%-*s %-22s %5s %5s %5s%s\n",
		colorDim, langWidth+3, T("Lang"), T("Progress"), T("Done"), T("Fuzzy"), T("Total"), colorReset)
	fmt.Fprintln(os.Stderr, "  "+colorDim+strings.Repeat("─", 52)+colorReset)

	for _, lang := range langs {
		poPath := rt.DocsPOPath(lang)
		catalog, err := po.ParseFile(poPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("missing"), colorReset)
			continue
		}

		total, translated, fuzzy, _ := catalog.Stats()
		percent := 0
		if total > 0 {
			percent = (translated * 100) / total
		}

		fmt.Fprintf(os.Stderr, "  %s %s %5d %5d %5d\n",
			langCell(lang, langWidth), progressBar(percent, 16), translated, fuzzy, total)
	}
}

// showConfigI18NextStats shows translation stats for an i18next/json target.
func showConfigI18NextStats(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	keyVal(T("Translations"), transDir)

	srcPath := rt.ExistingSourcePath()
	if srcPath == "" {
		srcPath = rt.SourcePath()
	}
	srcFile, err := i18next.ParseFile(srcPath)
	if err != nil {
		keyVal(T("Source"), colorYellow+T("not found")+colorReset+" ("+srcPath+")")
		return
	}
	srcKeys := len(srcFile.Translations)
	keyVal(T("Source keys"), fmt.Sprintf("%d (%s)", srcKeys, rt.Target.SourceLang))
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
		file, err := i18next.ParseFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("missing"), colorReset)
			continue
		}

		_, translated, untranslated := file.Stats()
		percent := 0
		if srcKeys > 0 {
			percent = translated * 100 / srcKeys
		}

		fmt.Fprintf(os.Stderr, "  %s %s %5d %5d\n",
			langCell(lang, langWidth), progressBar(percent, 16), translated, untranslated)
	}

}

// showConfigAndroidStats shows translation stats for an Android target.
func showConfigAndroidStats(rt config.ResolvedTarget, langs []string) {
	resDir := rt.AbsResDir()
	srcPath := android.SourceStringsXMLPath(resDir)
	keyVal(T("Res dir"), resDir)

	srcFile, err := android.ParseFile(srcPath)
	if err != nil {
		keyVal(T("Source"), colorYellow+T("not found")+colorReset+" ("+srcPath+")")
		return
	}
	srcTotal, _, _ := srcFile.Stats()
	keyVal(T("Source strings"), fmt.Sprintf("%d (%s)", srcTotal, rt.Target.SourceLang))
	langWidth := langColumnWidth(langs)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s%-*s %-22s %5s %5s%s\n",
		colorDim, langWidth+3, T("Lang"), T("Progress"), T("Done"), T("Left"), colorReset)
	fmt.Fprintln(os.Stderr, "  "+colorDim+strings.Repeat("─", 46)+colorReset)

	for _, lang := range langs {
		filePath := android.StringsXMLPath(resDir, lang)
		file, err := android.ParseFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("missing"), colorReset)
			continue
		}

		_, translated, untranslated := file.Stats()
		percent := 0
		if srcTotal > 0 {
			percent = translated * 100 / srcTotal
		}

		fmt.Fprintf(os.Stderr, "  %s %s %5d %5d\n",
			langCell(lang, langWidth), progressBar(percent, 16), translated, untranslated)
	}
}

func showStatsTable(proj *config.Project, potPath string) {
	potTotal := 0
	if potPO, err := po.ParseFile(potPath); err == nil {
		for _, e := range potPO.Entries {
			if e.MsgID != "" && !e.Obsolete {
				potTotal++
			}
		}
	}

	if potTotal == 0 {
		logInfo(T("No translatable strings found in %s"), potPath)
		return
	}

	sectionHeader(T("Translation Statistics"))
	keyVal(T("Source strings"), fmt.Sprintf("%d", potTotal))
	langWidth := langColumnWidth(proj.Languages)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s%-*s %-22s %5s %5s %5s%s\n",
		colorDim, langWidth+3, T("Lang"), T("Progress"), T("Done"), T("Fuzzy"), T("Left"), colorReset)
	fmt.Fprintln(os.Stderr, "  "+colorDim+strings.Repeat("─", 52)+colorReset)

	type langIssue struct {
		lang         string
		untranslated int
		fuzzy        int
	}
	var issues []langIssue

	for _, lang := range proj.Languages {
		poPath := proj.POPath(lang)

		poFile, err := po.ParseFile(poPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("missing"), colorReset)
			continue
		}

		_, translated, fuzzy, untranslated := poFile.Stats()
		percent := 0
		if potTotal > 0 {
			percent = translated * 100 / potTotal
		}

		fmt.Fprintf(os.Stderr, "  %s %s %5d %5d %5d\n",
			langCell(lang, langWidth), progressBar(percent, 16), translated, fuzzy, untranslated)

		if untranslated > 0 || fuzzy > 0 {
			issues = append(issues, langIssue{lang, untranslated, fuzzy})
		}
	}

	if len(issues) > 0 {
		fmt.Fprintln(os.Stderr)
		logInfo(T("Translation gaps:"))
		for _, issue := range issues {
			parts := []string{}
			if issue.untranslated > 0 {
				parts = append(parts, fmt.Sprintf(T("%d untranslated"), issue.untranslated))
			}
			if issue.fuzzy > 0 {
				parts = append(parts, fmt.Sprintf(T("%d fuzzy"), issue.fuzzy))
			}
			fmt.Fprintf(os.Stderr, "  %s: %s\n", issue.lang, strings.Join(parts, ", "))
		}
	}

	fmt.Fprintln(os.Stderr)
}

func showPo4aStats(proj *config.Project) {
	if len(proj.Languages) == 0 {
		return
	}

	sectionHeader(T("Documentation Statistics"))
	langWidth := langColumnWidth(proj.Languages)

	fmt.Fprintf(os.Stderr, "  %s%-*s %-22s %5s %5s %5s%s\n",
		colorDim, langWidth+3, T("Lang"), T("Progress"), T("Done"), T("Fuzzy"), T("Total"), colorReset)
	fmt.Fprintln(os.Stderr, "  "+colorDim+strings.Repeat("─", 52)+colorReset)

	for _, lang := range proj.Languages {
		poPath := proj.POPath(lang)
		catalog, err := po.ParseFile(poPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("missing"), colorReset)
			continue
		}

		total, translated, fuzzy, _ := catalog.Stats()
		percent := 0
		if total > 0 {
			percent = (translated * 100) / total
		}

		fmt.Fprintf(os.Stderr, "  %s %s %5d %5d %5d\n",
			langCell(lang, langWidth), progressBar(percent, 16), translated, fuzzy, total)
	}

	fmt.Fprintln(os.Stderr)
}

func showI18NextStats(proj *config.Project) {
	if len(proj.Languages) == 0 {
		logInfo(T("No language files detected in %s"), proj.I18NextDir)
		return
	}

	// Show source language key count first
	srcFile, err := i18next.ParseFile(proj.I18NextPath(proj.SourceLang))
	srcKeys := 0
	if err == nil {
		srcKeys = len(srcFile.Translations)
	}

	sectionHeader(T("UI Translation Statistics"))
	if srcKeys > 0 {
		keyVal(T("Source keys"), fmt.Sprintf("%d (%s)", srcKeys, proj.SourceLang))
	}
	langWidth := langColumnWidth(proj.Languages)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s%-*s %-22s %5s %5s%s\n",
		colorDim, langWidth+3, T("Lang"), T("Progress"), T("Done"), T("Left"), colorReset)
	fmt.Fprintln(os.Stderr, "  "+colorDim+strings.Repeat("─", 46)+colorReset)

	// Skip source language
	for _, lang := range proj.Languages {
		if lang == proj.SourceLang {
			continue
		}
		filePath := proj.I18NextPath(lang)
		file, err := i18next.ParseFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("missing"), colorReset)
			continue
		}

		_, translated, untranslated := file.Stats()
		percent := 0
		if srcKeys > 0 {
			percent = translated * 100 / srcKeys
		}

		fmt.Fprintf(os.Stderr, "  %s %s %5d %5d\n",
			langCell(lang, langWidth), progressBar(percent, 16), translated, untranslated)
	}

	fmt.Fprintln(os.Stderr)
}

// ---------------------------------------------------------------------------
// init (extract + create/update PO files)
// ---------------------------------------------------------------------------

// Shared helpers
// ---------------------------------------------------------------------------

// doExtract runs xgettext extraction for the project. Returns nil on success.
// Used by both 'init' and auto-extraction in 'translate'.
