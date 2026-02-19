// lokit — Localization Kit: gettext PO file manager with AI translation support.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/minios-linux/lokit/android"
	"github.com/minios-linux/lokit/arbfile"
	"github.com/minios-linux/lokit/config"
	"github.com/minios-linux/lokit/copilot"
	"github.com/minios-linux/lokit/extract"
	"github.com/minios-linux/lokit/gemini"
	. "github.com/minios-linux/lokit/i18n"
	"github.com/minios-linux/lokit/i18next"
	"github.com/minios-linux/lokit/mdfile"
	"github.com/minios-linux/lokit/merge"
	po "github.com/minios-linux/lokit/pofile"
	"github.com/minios-linux/lokit/propfile"
	"github.com/minios-linux/lokit/settings"
	"github.com/minios-linux/lokit/translate"
	"github.com/minios-linux/lokit/yamlfile"
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

// langFlag returns the flag emoji for a language code, or empty string if unknown.
func langFlag(lang string) string {
	if meta, ok := i18next.LangMeta[lang]; ok {
		return meta.Flag
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
  android     Android strings.xml resource files
  json        Generic JSON translation files (recipes, blog posts)
  yaml        YAML translation files
  markdown    Markdown document translation
  properties  Java .properties translation files
  flutter     Flutter ARB (Application Resource Bundle) files

Configuration:
  Project settings are defined in lokit.yaml at the project root.
  Each project can have multiple translation targets with different types.
  See project examples for lokit.yaml format.

Custom prompts:
  AI translation prompts are stored in ~/.local/share/lokit/prompts.json
  (or $XDG_DATA_HOME/lokit/prompts.json). The file is created automatically
  on first use with built-in defaults. Edit it to customize prompts per
  content type: default, docs, i18next, recipe, blogpost, android, yaml,
  markdown, properties, flutter.
  Use {{targetLang}} as a placeholder for the target language name.

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
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// Load custom prompts from default locations if available
			promptsPath, err := translate.LoadPromptsFromDefaultLocations()
			if err != nil {
				logError(T("Warning: Failed to load prompts: %v"), err)
			} else if promptsPath != "" {
				logInfo(T("Loaded prompts from %s"), promptsPath)
			}
		},
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

func main() {
	Init("")
	root := newRootCmd()
	translateHelpFlags(root)
	if err := root.Execute(); err != nil {
		logError(T("%v"), err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// version (display version information)
// ---------------------------------------------------------------------------

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: T("Show version information"),
		Long:  T(`Display version, commit hash, and build date.`),
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf(T("lokit version %s")+"\n", version)
			fmt.Printf("  %s %s\n", T("commit:"), commit)
			fmt.Printf("  %s %s\n", T("built:"), date)
		},
	}

	return cmd
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// status (read-only: project info + translation stats)
// ---------------------------------------------------------------------------

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: T("Show project info and translation statistics"),
		Long: T(`Show auto-detected project structure and translation statistics.

Displays project type (gettext, po4a, i18next, android), file structure,
detected languages, and per-language translation progress. For projects
configured via lokit.yaml, shows each target separately.

Does not modify any files.`),
		Run: func(cmd *cobra.Command, args []string) {
			runStatus()
		},
	}

	return cmd
}

func runStatus() {
	// Check for lokit.yaml
	lf, err := config.LoadLokitFile(rootDir)
	if err != nil {
		logError(T("Config error: %v"), err)
		os.Exit(1)
	}
	if lf != nil {
		runStatusWithConfig(lf)
		return
	}

	logError(T("No lokit.yaml found in %s"), rootDir)
	logInfo(T("Create a lokit.yaml configuration file. See 'lokit init --help' for format reference."))
	os.Exit(1)
}

// runStatusWithConfig shows multi-target status from lokit.yaml.
func runStatusWithConfig(lf *config.LokitFile) {
	absRoot, _ := filepath.Abs(rootDir)

	sectionHeader(T("Project"))
	keyVal(T("Config"), "lokit.yaml")
	keyVal(T("Root"), absRoot)
	keyVal(T("Source lang"), lf.SourceLang)

	if len(lf.Languages) > 0 {
		keyVal(T("Languages"), strings.Join(lf.Languages, ", "))
	}
	keyVal(T("Targets"), fmt.Sprintf("%d", len(lf.Targets)))

	resolved, err := lf.Resolve(rootDir)
	if err != nil {
		logError(T("Config resolve error: %v"), err)
		os.Exit(1)
	}

	for _, rt := range resolved {
		langs := filterOutLang(rt.Languages, rt.Target.SourceLang)

		targetHeader(rt.Target.Name, rt.Target.Type)

		if rt.Target.Root != "." {
			keyVal(T("Root"), rt.Target.Root)
		}

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
		case config.TargetTypeI18Next, config.TargetTypeJSON:
			showConfigI18NextStats(rt, langs)
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
		}

		fmt.Fprintln(os.Stderr)
	}
}

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

	srcPath := filepath.Join(transDir, rt.Target.SourceLang+".json")
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
		filePath := filepath.Join(transDir, lang+".json")
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

	// Blog post stats
	if rt.Target.BlogDir != "" {
		blogDir := filepath.Join(rt.AbsRoot, rt.Target.BlogDir)
		slugs, err := i18next.BlogPostSlugs(blogDir)
		if err == nil && len(slugs) > 0 {
			fmt.Fprintln(os.Stderr)
			sectionHeader(fmt.Sprintf(T("Blog Posts (%d posts)"), len(slugs)))

			for _, lang := range langs {
				translated := 0
				for _, slug := range slugs {
					transPath := i18next.BlogTranslationPath(blogDir, slug, lang)
					if _, err := os.Stat(transPath); err == nil {
						translated++
					}
				}
				percent := 0
				if len(slugs) > 0 {
					percent = translated * 100 / len(slugs)
				}
				fmt.Fprintf(os.Stderr, "  %s %s %5d/%d\n",
					langCell(lang, langWidth), progressBar(percent, 16), translated, len(slugs))
			}
		}
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

// showConfigYAMLStats shows translation stats for a YAML target.
func showConfigYAMLStats(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	keyVal(T("Translations"), transDir)

	// Try both .yaml and .yml extensions for source file.
	srcLang := rt.Target.SourceLang
	srcPath := ""
	for _, ext := range []string{".yaml", ".yml"} {
		candidate := filepath.Join(transDir, srcLang+ext)
		if _, err := os.Stat(candidate); err == nil {
			srcPath = candidate
			break
		}
	}
	if srcPath == "" {
		keyVal(T("Source"), colorYellow+T("not found")+colorReset+" ("+filepath.Join(transDir, srcLang+".yaml")+")")
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
		// Try both extensions.
		filePath := ""
		for _, ext := range []string{".yaml", ".yml"} {
			candidate := filepath.Join(transDir, lang+ext)
			if _, err := os.Stat(candidate); err == nil {
				filePath = candidate
				break
			}
		}
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

	// Recipe translation stats
	if proj.RecipeTransDir != "" {
		fmt.Fprintln(os.Stderr)
		showRecipeTransStats(proj)
	}

	// Blog post translation stats
	if proj.BlogPostsDir != "" {
		fmt.Fprintln(os.Stderr)
		showBlogTransStats(proj)
	}

	fmt.Fprintln(os.Stderr)
}

func showRecipeTransStats(proj *config.Project) {
	sectionHeader(T("Recipe Translations"))
	langWidth := langColumnWidth(proj.Languages)

	for _, lang := range proj.Languages {
		if lang == proj.SourceLang {
			continue
		}
		langDir := proj.RecipeTransPath(lang)
		if langDir == "" {
			continue
		}
		entries, err := os.ReadDir(langDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s  %s%s%s\n",
				langCell(lang, langWidth), progressBar(0, 16), colorYellow, T("missing"), colorReset)
			continue
		}

		total := 0
		translated := 0
		fullyTranslated := 0

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			total++
			rt, err := i18next.ParseRecipeFile(filepath.Join(langDir, entry.Name()))
			if err != nil {
				continue
			}
			if rt.IsTranslated() {
				translated++
			}
			if rt.IsFullyTranslated() {
				fullyTranslated++
			}
		}

		percent := 0
		if total > 0 {
			percent = fullyTranslated * 100 / total
		}

		fmt.Fprintf(os.Stderr, "  %s %s %5d/%d\n",
			langCell(lang, langWidth), progressBar(percent, 16), fullyTranslated, total)
	}
}

func showBlogTransStats(proj *config.Project) {
	slugs, err := i18next.BlogPostSlugs(proj.BlogPostsDir)
	if err != nil || len(slugs) == 0 {
		return
	}

	sectionHeader(fmt.Sprintf(T("Blog Posts (%d posts)"), len(slugs)))
	langWidth := langColumnWidth(proj.Languages)

	for _, lang := range proj.Languages {
		if lang == proj.SourceLang {
			continue
		}
		translated := 0
		for _, slug := range slugs {
			transPath := i18next.BlogTranslationPath(proj.BlogPostsDir, slug, lang)
			if _, err := os.Stat(transPath); err == nil {
				translated++
			}
		}
		percent := 0
		if len(slugs) > 0 {
			percent = translated * 100 / len(slugs)
		}
		fmt.Fprintf(os.Stderr, "  %s %s %5d/%d\n",
			langCell(lang, langWidth), progressBar(percent, 16), translated, len(slugs))
	}
}

// ---------------------------------------------------------------------------
// init (extract + create/update PO files)
// ---------------------------------------------------------------------------

func newInitCmd() *cobra.Command {
	var langs string

	cmd := &cobra.Command{
		Use:   "init",
		Short: T("Prepare translation files (extract strings, create/update PO)"),
		Long: T(`Extract translatable strings and create/update translation files.

Requires a lokit.yaml configuration file in the project root.

For gettext projects: runs xgettext to extract strings into a POT template,
then creates or updates PO files for each language.

For po4a projects: runs 'po4a --no-translations' to update templates.

For i18next/json/yaml/properties/flutter projects: creates missing language
files with empty translations.

For android/markdown projects: no init step needed — use 'lokit translate'
directly.

This command is idempotent — safe to run multiple times. Existing
translations are preserved when updating files.

CONFIG FORMAT (lokit.yaml)

  source_lang: en                    # Source language (default: en)
  languages: [ru, de, fr, es, ...]   # Target languages

  targets:
    - name: myproject                # Display name
      type: gettext                  # Target type (see below)
      root: .                        # Working directory (default: .)
      # ... type-specific options

TARGET TYPES

  gettext — Source code string extraction (shell, python, C, Go, etc.)
    dir: po                          # PO files directory (required)
    pot_file: po/messages.pot        # POT template path
    sources: [src, scripts]          # Directories to scan (default: root)
    keywords: [_, N_, gettext]       # xgettext keywords (default: standard set)

  po4a — Documentation translation (man pages, AsciiDoc, etc.)
    po4a_config: po4a.cfg            # Path to po4a.cfg (default: po4a.cfg)

  i18next — i18next JSON translations
    dir: public/translations         # JSON files directory (required)

  json — Simple JSON translations { "key": "value" }
    dir: translations                # JSON files directory (required)

  android — Android strings.xml
    dir: app/src/main/res            # Android res/ directory (required)

  yaml — YAML key/value translations
    dir: translations                # YAML files directory (required)

  markdown — Markdown document translation
    dir: translations                # Root dir; files at translations/LANG/ (required)

  properties — Java .properties translations
    dir: translations                # .properties files directory (required)

  flutter — Flutter ARB (Application Resource Bundle)
    dir: lib/l10n                    # ARB files directory (required)

COMMON OPTIONS (all target types)

  languages: [ru, de]               # Override global language list
  prompt: "Custom translation..."   # Override AI translation prompt

EXAMPLES

  # Shell scripts with gettext
  source_lang: en
  languages: [ru, de, fr]
  targets:
    - name: myapp
      type: gettext
      sources: [scripts, lib]
      keywords: [gettext, eval_gettext]

  # Go project with wrapper functions
  targets:
    - name: myapp
      type: gettext
      sources: [.]
      keywords: [T, N]

  # Documentation + code
  targets:
    - name: code
      type: gettext
      sources: [src]
    - name: docs
      type: po4a
      po4a_config: docs/po4a.cfg

  # i18next web application
  targets:
    - name: frontend
      type: i18next
      dir: public/translations

  # Flutter application
  targets:
    - name: app
      type: flutter
      dir: lib/l10n

  # Java application with .properties
  targets:
    - name: app
      type: properties
      dir: src/main/resources`),
		Run: func(cmd *cobra.Command, args []string) {
			// Require lokit.yaml
			lf, err := config.LoadLokitFile(rootDir)
			if err != nil {
				logError(T("Config error: %v"), err)
				os.Exit(1)
			}
			if lf == nil {
				logError(T("No lokit.yaml found in %s"), rootDir)
				logInfo(T("Create a lokit.yaml configuration file. See 'lokit init --help' for format reference."))
				os.Exit(1)
			}

			runInitWithConfig(lf, langs)
		},
	}

	cmd.Flags().StringVar(&langs, "lang", "", T("Languages to init (comma-separated, default: all from config)"))

	return cmd
}

// runInitWithConfig initializes translation files using lokit.yaml targets.
func runInitWithConfig(lf *config.LokitFile, langsFlag string) {
	absRoot, _ := filepath.Abs(rootDir)

	resolved, err := lf.Resolve(rootDir)
	if err != nil {
		logError(T("Config resolve error: %v"), err)
		os.Exit(1)
	}

	for _, rt := range resolved {
		langs := filterOutLang(rt.Languages, rt.Target.SourceLang)
		if langsFlag != "" {
			langs = strings.Split(langsFlag, ",")
		}

		targetHeader(rt.Target.Name, rt.Target.Type)

		switch rt.Target.Type {
		case config.TargetTypeGettext:
			proj := &config.Project{
				Root:        rt.AbsRoot,
				Name:        rt.Target.Name,
				Version:     "0.0.0",
				PODir:       rt.AbsPODir(),
				POTFile:     filepath.Join(rt.AbsRoot, rt.Target.POTFile),
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
			runInitCode(proj)

		case config.TargetTypePo4a:
			proj := &config.Project{
				Name:        rt.Target.Name,
				Version:     "0.0.0",
				POStructure: config.POStructurePo4a,
				Po4aConfig:  filepath.Join(rt.AbsRoot, rt.Target.Po4aConfig),
				Languages:   langs,
				SourceLang:  rt.Target.SourceLang,
			}
			proj.PODir = filepath.Join(filepath.Dir(proj.Po4aConfig), "po")
			// Check for docs directory for manpage generation
			for _, candidate := range []string{"docs", "doc"} {
				docsDir := filepath.Join(absRoot, candidate)
				if info, err := os.Stat(docsDir); err == nil && info.IsDir() {
					proj.DocsDir = docsDir
					break
				}
			}
			proj.ManpagesDir = filepath.Dir(proj.Po4aConfig)
			runInitPo4a(proj)

		case config.TargetTypeI18Next, config.TargetTypeJSON:
			proj := &config.Project{
				Name:       rt.Target.Name,
				Version:    "0.0.0",
				Type:       config.ProjectTypeI18Next,
				I18NextDir: rt.AbsTranslationsDir(),
				Languages:  langs,
				SourceLang: rt.Target.SourceLang,
			}
			if rt.Target.RecipesDir != "" {
				proj.RecipeTransDir = filepath.Join(rt.AbsRoot, rt.Target.RecipesDir)
			}
			if rt.Target.BlogDir != "" {
				proj.BlogPostsDir = filepath.Join(rt.AbsRoot, rt.Target.BlogDir)
			}
			runInitI18Next(proj)

		case config.TargetTypeAndroid:
			logInfo(T("Android targets do not require init — use 'lokit translate' directly."))

		case config.TargetTypeYAML:
			runInitYAML(rt, langs)

		case config.TargetTypeMarkdown:
			runInitMarkdown(rt, langs)

		case config.TargetTypeProperties:
			runInitProperties(rt, langs)

		case config.TargetTypeFlutter:
			runInitFlutter(rt, langs)
		}
	}
}

// runInitYAML creates or syncs YAML translation files from the source file.
func runInitYAML(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()

	// Locate source file (try .yaml then .yml).
	srcLang := rt.Target.SourceLang
	srcPath := ""
	for _, ext := range []string{".yaml", ".yml"} {
		candidate := filepath.Join(transDir, srcLang+ext)
		if _, err := os.Stat(candidate); err == nil {
			srcPath = candidate
			break
		}
	}
	if srcPath == "" {
		logError(T("Cannot find source YAML file for language %q in %s"), srcLang, transDir)
		logInfo(T("Expected: %s"), filepath.Join(transDir, srcLang+".yaml"))
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

		// Try to find existing target file.
		targetPath := ""
		for _, ext := range []string{".yaml", ".yml"} {
			candidate := filepath.Join(transDir, lang+ext)
			if _, err := os.Stat(candidate); err == nil {
				targetPath = candidate
				break
			}
		}

		if targetPath == "" {
			// Create new file from source structure.
			targetPath = filepath.Join(transDir, lang+".yaml")
			newFile := yamlfile.NewTranslationFile(srcFile, lang)
			if err := os.MkdirAll(transDir, 0755); err != nil {
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

		// Sync existing file.
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

func runInitI18Next(proj *config.Project) {
	srcPath := proj.I18NextPath(proj.SourceLang)
	srcFile, err := i18next.ParseFile(srcPath)
	if err != nil {
		logError(T("Cannot read source language file %s: %v"), srcPath, err)
		os.Exit(1)
	}

	srcKeys := srcFile.Keys()
	logInfo(T("Source language (%s): %d keys"), proj.SourceLang, len(srcKeys))

	created, updated := 0, 0

	for _, lang := range proj.Languages {
		if lang == proj.SourceLang {
			continue
		}

		filePath := proj.I18NextPath(lang)
		file, err := i18next.ParseFile(filePath)

		if err != nil {
			// Create new file with all keys empty
			meta := i18next.Meta{Name: lang, Flag: ""}
			if lm, ok := i18next.LangMeta[lang]; ok {
				meta = lm
			}
			file = &i18next.File{
				Meta:         meta,
				Translations: make(map[string]string),
			}
			for _, key := range srcKeys {
				file.Translations[key] = ""
			}
			if err := file.WriteFile(filePath); err != nil {
				logError(T("Creating %s: %v"), filePath, err)
				continue
			}
			logSuccess(T("Created: %s (%d keys)"), filePath, len(srcKeys))
			created++
			continue
		}

		// Sync keys: add missing, don't remove extras (they may be intentional)
		added := 0
		for _, key := range srcKeys {
			if _, exists := file.Translations[key]; !exists {
				file.Translations[key] = ""
				added++
			}
		}

		if added > 0 {
			if err := file.WriteFile(filePath); err != nil {
				logError(T("Updating %s: %v"), filePath, err)
				continue
			}
			logSuccess(T("Updated: %s (+%d new keys)"), filePath, added)
			updated++
		} else {
			logInfo(T("%s: up to date"), lang)
		}
	}

	logInfo(T("Summary: %d created, %d updated"), created, updated)
	fmt.Fprintln(os.Stderr)
	showI18NextStats(proj)
	logSuccess(T("Init complete!"))
}

// generateManpagesFromMarkdown generates man pages from markdown files if they don't exist.
// This is needed for po4a projects that reference .1, .7 files in po4a.cfg but only have .md sources.
func generateManpagesFromMarkdown(proj *config.Project) error {
	// Only process if we have both po4a config and docs directory
	if proj.Po4aConfig == "" || proj.DocsDir == "" {
		return nil
	}

	// Check if pandoc is available
	if _, err := exec.LookPath("pandoc"); err != nil {
		logInfo(T("pandoc not found, skipping manpage generation from markdown"))
		return nil
	}

	// Check if docs directory exists
	if _, err := os.Stat(proj.DocsDir); os.IsNotExist(err) {
		return nil // No docs directory, nothing to generate
	}

	manpageDir := filepath.Dir(proj.Po4aConfig)

	// Find all markdown files in docs that look like manpage sources (name.section.md)
	// Example: minios-live.1.md -> minios-live.1
	mdFiles, err := filepath.Glob(filepath.Join(proj.DocsDir, "*.*.md"))
	if err != nil {
		return fmt.Errorf(T("failed to list markdown files: %w"), err)
	}

	generated := 0
	for _, mdPath := range mdFiles {
		mdFile := filepath.Base(mdPath)

		// Extract manpage name (remove .md extension)
		// Example: minios-live.1.md -> minios-live.1
		if !strings.HasSuffix(mdFile, ".md") {
			continue
		}
		manFile := strings.TrimSuffix(mdFile, ".md")

		// Check if this looks like a manpage (has section number)
		// Format should be: name.section where section is a digit
		parts := strings.Split(manFile, ".")
		if len(parts) < 2 {
			continue
		}
		lastPart := parts[len(parts)-1]
		if len(lastPart) != 1 || lastPart[0] < '0' || lastPart[0] > '9' {
			continue
		}

		manPath := filepath.Join(manpageDir, manFile)

		// Skip if manpage already exists
		if _, err := os.Stat(manPath); err == nil {
			continue
		}

		// Generate manpage from markdown
		logInfo(T("Generating %s from %s"), manFile, mdFile)
		cmd := exec.Command("pandoc", "-s", "-t", "man", mdPath, "-o", manPath)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf(T("pandoc failed for %s: %w"), mdFile, err)
		}

		// Post-process: remove \c sequences that cause po4a issues
		if err := removeBackslashCSequences(manPath); err != nil {
			logWarning(T("Failed to post-process %s: %v"), manFile, err)
		}

		generated++
	}

	if generated > 0 {
		logSuccess(T("Generated %d manpage(s) from markdown"), generated)
	}

	return nil
}

// removeBackslashCSequences removes \c escape sequences from manpage files.
// These are generated by pandoc but cause issues with po4a.
func removeBackslashCSequences(filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	// Remove " \c" sequences (space + backslash + c)
	// This is safe because \c is just a line continuation that po4a doesn't like
	modified := strings.ReplaceAll(string(content), " \\c\n", "\n")

	if modified != string(content) {
		return os.WriteFile(filePath, []byte(modified), 0644)
	}

	return nil
}

// scanPo4aLanguages scans a PO directory for language subdirectories containing .po files.
func scanPo4aLanguages(poDir string) []string {
	return config.DetectLanguagesNested(poDir)
}

// doPo4aInit runs po4a --no-translations to generate/update POT and PO files.
// Returns nil on success. Unlike runInitPo4a, this is safe to call from other
// commands (no os.Exit, no stats display).
func doPo4aInit(proj *config.Project) error {
	if _, err := exec.LookPath("po4a"); err != nil {
		return fmt.Errorf(T("po4a is not installed. Install with: sudo apt install po4a"))
	}

	// Check if manpages need to be generated from markdown
	if err := generateManpagesFromMarkdown(proj); err != nil {
		logWarning(T("Failed to generate manpages from markdown: %v"), err)
		logWarning(T("Continuing anyway, po4a might fail if source files don't exist"))
	}

	logInfo(T("Running po4a --no-translations %s ..."), proj.Po4aConfig)
	cmd := exec.Command("po4a", "--no-translations", proj.Po4aConfig)
	cmd.Dir = filepath.Dir(proj.Po4aConfig)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(T("po4a failed: %v"), err)
	}
	logSuccess(T("po4a updated POT and PO files"))

	// Re-scan languages from PO files after po4a ran (it may have created new ones)
	poDir := proj.PODir
	if poDir != "" {
		if scannedLangs := scanPo4aLanguages(poDir); len(scannedLangs) > 0 {
			proj.Languages = scannedLangs
		}
	}

	// Re-resolve POT file (po4a may have generated it)
	proj.POTFile = proj.POTPathResolved()
	return nil
}

func runInitPo4a(proj *config.Project) {
	if err := doPo4aInit(proj); err != nil {
		logError(T("%v"), err)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stderr)
	potPath := proj.POTPathResolved()
	if potPath != "" && fileExists(potPath) {
		showStatsTable(proj, potPath)
	} else {
		showPo4aStats(proj)
	}

	logSuccess(T("Init complete!"))
}

func runInitCode(proj *config.Project) {
	// Step 1: Extract strings using xgettext
	if err := doExtract(proj); err != nil {
		logError(T("%v"), err)
		os.Exit(1)
	}

	// Step 2: Update PO files from POT
	potPO, err := po.ParseFile(proj.POTFile)
	if err != nil {
		logError(T("Reading %s: %v"), proj.POTFile, err)
		os.Exit(1)
	}

	logInfo(T("Updating PO files for: %s"), strings.Join(proj.Languages, ", "))

	created, updated := 0, 0

	for _, lang := range proj.Languages {
		poPath := proj.POPath(lang)

		if err := os.MkdirAll(filepath.Dir(poPath), 0755); err != nil {
			logError(T("Creating directory for %s: %v"), poPath, err)
			continue
		}

		if _, err := os.Stat(poPath); os.IsNotExist(err) {
			newPO := po.NewFile()
			newPO.Header = po.MakeHeader(proj.Name, proj.Version, proj.BugsEmail, proj.CopyrightHolder, lang)
			newPO.SetHeaderField("Plural-Forms", po.PluralFormsForLang(lang))

			for _, e := range potPO.Entries {
				entry := &po.Entry{
					ExtractedComments: e.ExtractedComments,
					References:        e.References,
					Flags:             copyFlags(e.Flags),
					MsgCtxt:           e.MsgCtxt,
					MsgID:             e.MsgID,
					MsgIDPlural:       e.MsgIDPlural,
					MsgStr:            "",
					MsgStrPlural:      make(map[int]string),
				}
				newPO.Entries = append(newPO.Entries, entry)
			}

			if err := newPO.WriteFile(poPath); err != nil {
				logError(T("Creating %s: %v"), poPath, err)
				continue
			}
			logSuccess(T("Created: %s"), poPath)
			created++
		} else {
			existingPO, err := po.ParseFile(poPath)
			if err != nil {
				logError(T("Reading %s: %v"), poPath, err)
				continue
			}

			merged := merge.Merge(existingPO, potPO)
			if err := merged.WriteFile(poPath); err != nil {
				logError(T("Writing %s: %v"), poPath, err)
				continue
			}
			logSuccess(T("Updated: %s"), poPath)
			updated++
		}
	}

	logInfo(T("Summary: %d created, %d updated"), created, updated)

	// Show stats
	fmt.Fprintln(os.Stderr)
	showStatsTable(proj, proj.POTFile)

	logSuccess(T("Init complete!"))
}

// ---------------------------------------------------------------------------
// translate
// ---------------------------------------------------------------------------

func newTranslateCmd() *cobra.Command {
	var (
		// Target selection
		langs string

		// Provider selection
		provider string
		apiKey   string
		model    string
		baseURL  string

		// Translation behavior
		chunkSize   int
		retranslate bool
		fuzzy       bool
		prompt      string
		verbose     bool
		dryRun      bool

		// Parallelization
		parallel      bool
		maxConcurrent int
		requestDelay  time.Duration

		// Network
		timeout    time.Duration
		proxy      string
		maxRetries int
	)

	cmd := &cobra.Command{
		Use:   "translate",
		Short: T("Translate files using AI"),
		Long: T(`Translate files using AI providers.

Supports gettext PO, po4a, i18next JSON, Android strings.xml, generic JSON,
YAML, Markdown, Java .properties, and Flutter ARB formats. Project type is
auto-detected or configured via lokit.yaml.

For gettext/po4a projects, automatically initializes if needed (extracts
strings, creates PO files). Requires --provider and --model flags.

Custom prompts are stored in ~/.local/share/lokit/prompts.json (or
$XDG_DATA_HOME/lokit/prompts.json). The file is created automatically
on first use with built-in defaults. Each project type uses its own
prompt (default, docs, i18next, recipe, blogpost, android, yaml, markdown,
properties, flutter). The --prompt flag overrides the loaded prompt for the
current run.

Examples:
  # Translate a gettext project using GitHub Copilot (free)
  lokit translate --provider copilot --model gpt-4o

  # Translate an Android project using Gemini (free, OAuth)
  lokit translate --provider gemini --model gemini-2.5-flash

  # Translate an i18next project using Google AI (API key)
  lokit translate --provider google --model gemini-2.5-flash

  # Translate specific languages in parallel
  lokit translate --provider copilot --model gpt-4o --lang ru,de --parallel

  # Re-translate everything with a custom prompt
  lokit translate --provider copilot --model gpt-4o --retranslate \
    --prompt "Translate to {{targetLang}}. Use informal tone."

  # Translate a Flutter project
  lokit translate --provider copilot --model gpt-4o

  # Dry run (show what would be translated)
  lokit translate --provider copilot --model gpt-4o --dry-run`),
		Run: func(cmd *cobra.Command, args []string) {
			runTranslate(translateArgs{
				langs:    langs,
				provider: provider, apiKey: apiKey, model: model,
				baseURL:   baseURL,
				chunkSize: chunkSize, retranslate: retranslate,
				fuzzy: fuzzy, prompt: prompt, verbose: verbose,
				dryRun: dryRun, parallel: parallel,
				maxConcurrent: maxConcurrent, requestDelay: requestDelay,
				timeout: timeout, proxy: proxy, maxRetries: maxRetries,
			})
		},
	}

	// Provider selection
	cmd.Flags().StringVar(&provider, "provider", "", T("AI provider (required): google, gemini, groq, opencode, copilot, ollama, custom-openai"))
	cmd.Flags().StringVar(&model, "model", "", T("Model name (required)"))
	cmd.Flags().StringVar(&apiKey, "api-key", "", T("API key (or provider env var: GOOGLE_API_KEY, GROQ_API_KEY, OPENAI_API_KEY, OPENCODE_API_KEY)"))
	cmd.Flags().StringVar(&baseURL, "base-url", "", T("Custom API base URL"))

	// Target selection
	cmd.Flags().StringVar(&langs, "lang", "", T("Languages to translate (comma-separated, default: all with untranslated)"))

	// Translation behavior
	cmd.Flags().IntVar(&chunkSize, "chunk-size", 0, T("Entries per API request (0 = all at once)"))
	cmd.Flags().BoolVar(&retranslate, "retranslate", false, T("Re-translate already translated entries"))
	cmd.Flags().BoolVar(&fuzzy, "fuzzy", true, T("Translate fuzzy entries and clear fuzzy flag"))
	cmd.Flags().StringVar(&prompt, "prompt", "", T("Custom system prompt (use {{targetLang}} placeholder)"))
	cmd.Flags().BoolVar(&verbose, "verbose", false, T("Enable detailed logging"))
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, T("Show what would be translated without calling AI"))

	// Parallelization
	cmd.Flags().BoolVar(&parallel, "parallel", false, T("Enable parallel translation"))
	cmd.Flags().IntVar(&maxConcurrent, "max-concurrent", 3, T("Maximum concurrent tasks (with --parallel)"))
	cmd.Flags().DurationVar(&requestDelay, "request-delay", 0, T("Delay between parallel tasks"))

	// Network
	cmd.Flags().DurationVar(&timeout, "timeout", 0, T("Request timeout (0 = provider default)"))
	cmd.Flags().StringVar(&proxy, "proxy", "", T("HTTP/HTTPS proxy URL"))
	cmd.Flags().IntVar(&maxRetries, "max-retries", 3, T("Maximum retries on rate limit (429)"))

	// Provider completion
	_ = cmd.RegisterFlagCompletionFunc("provider", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{
			"google\t" + T("Google AI (Gemini) — API key required"),
			"gemini\t" + T("Gemini Code Assist — browser OAuth (free)"),
			"groq\t" + T("Groq — API key required"),
			"opencode\t" + T("OpenCode — optional API key"),
			"copilot\t" + T("GitHub Copilot — native OAuth (free)"),
			"ollama\t" + T("Ollama local server"),
			"custom-openai\t" + T("Custom OpenAI-compatible endpoint"),
		}, cobra.ShellCompDirectiveNoFileComp
	})

	// Model completion (provider-aware)
	_ = cmd.RegisterFlagCompletionFunc("model", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		p, _ := cmd.Flags().GetString("provider")
		switch p {
		case "google", "gemini":
			return []string{"gemini-2.5-flash", "gemini-2.0-flash-exp", "gemini-1.5-pro"}, cobra.ShellCompDirectiveNoFileComp
		case "groq":
			return []string{"llama-3.3-70b-versatile", "mixtral-8x7b-32768"}, cobra.ShellCompDirectiveNoFileComp
		case "opencode":
			return []string{"big-pickle", "gemini-2.5-flash", "claude-sonnet-4.5", "gpt-4o"}, cobra.ShellCompDirectiveNoFileComp
		case "copilot":
			return []string{"gpt-4o", "gpt-5", "gpt-5-mini", "claude-sonnet-4", "gemini-2.5-pro"}, cobra.ShellCompDirectiveNoFileComp
		case "ollama":
			return []string{"llama3.2", "qwen2.5", "mistral", "phi3"}, cobra.ShellCompDirectiveNoFileComp
		default:
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	})

	return cmd
}

type translateArgs struct {
	langs                            string
	provider, apiKey, model, baseURL string
	chunkSize                        int
	retranslate, fuzzy               bool
	prompt                           string
	verbose, dryRun, parallel        bool
	maxConcurrent                    int
	requestDelay, timeout            time.Duration
	proxy                            string
	maxRetries                       int
}

func runTranslate(a translateArgs) {
	// Check for lokit.yaml config file
	lf, err := config.LoadLokitFile(rootDir)
	if err != nil {
		logError(T("Config error: %v"), err)
		os.Exit(1)
	}
	if lf != nil {
		runTranslateWithConfig(lf, a)
		return
	}

	logError(T("No lokit.yaml found in %s"), rootDir)
	logInfo(T("Create a lokit.yaml configuration file. See 'lokit init --help' for format reference."))
	os.Exit(1)
}

// ---------------------------------------------------------------------------
// lokit.yaml multi-target translate
// ---------------------------------------------------------------------------

func runTranslateWithConfig(lf *config.LokitFile, a translateArgs) {
	// Resolve API key: --api-key flag → provider env var → stored credential
	key := settings.ResolveAPIKey(a.provider, a.apiKey)

	if a.provider == "" {
		logError(T("No provider specified. Use --provider to choose an AI translation service.\n\n") +
			"Example: lokit translate --provider copilot --model gpt-4o")
		os.Exit(1)
	}

	prov := resolveProvider(a.provider, a.baseURL, key, a.model, a.proxy, a.timeout)
	if err := validateProvider(prov, key); err != nil {
		logError(T("%v"), err)
		os.Exit(1)
	}

	resolved, err := lf.Resolve(rootDir)
	if err != nil {
		logError(T("Config resolve error: %v"), err)
		os.Exit(1)
	}

	if len(resolved) == 0 {
		logError(T("No targets defined in lokit.yaml"))
		os.Exit(1)
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		logWarning(T("Interrupted, saving progress..."))
		cancel()
	}()

	// Filter target languages
	var langFilter []string
	if a.langs != "" {
		langFilter = strings.Split(a.langs, ",")
	}

	hadErrors := false

	for _, rt := range resolved {
		if ctx.Err() != nil {
			break
		}

		// Determine languages for this target
		targetLangs := rt.Languages
		if len(langFilter) > 0 {
			targetLangs = intersectLanguages(targetLangs, langFilter)
		}

		// Filter out source language
		targetLangs = filterOutLang(targetLangs, rt.Target.SourceLang)

		if len(targetLangs) == 0 {
			logInfo(T("[%s] No languages to translate, skipping"), rt.Target.Name)
			continue
		}

		targetHeader(rt.Target.Name, string(rt.Target.Type))

		switch rt.Target.Type {
		case config.TargetTypeGettext:
			if err := translateGettextTarget(ctx, rt, prov, a, targetLangs); err != nil {
				logError(T("[%s] %v"), rt.Target.Name, err)
				hadErrors = true
			}

		case config.TargetTypePo4a:
			if err := translatePo4aTarget(ctx, rt, prov, a, targetLangs); err != nil {
				logError(T("[%s] %v"), rt.Target.Name, err)
				hadErrors = true
			}

		case config.TargetTypeI18Next:
			if err := translateI18NextTarget(ctx, rt, prov, a, targetLangs); err != nil {
				logError(T("[%s] %v"), rt.Target.Name, err)
				hadErrors = true
			}

		case config.TargetTypeJSON:
			if err := translateJSONTarget(ctx, rt, prov, a, targetLangs); err != nil {
				logError(T("[%s] %v"), rt.Target.Name, err)
				hadErrors = true
			}

		case config.TargetTypeAndroid:
			if err := translateAndroidTarget(ctx, rt, prov, a, targetLangs); err != nil {
				logError(T("[%s] %v"), rt.Target.Name, err)
				hadErrors = true
			}

		case config.TargetTypeYAML:
			if err := translateYAMLTarget(ctx, rt, prov, a, targetLangs); err != nil {
				logError(T("[%s] %v"), rt.Target.Name, err)
				hadErrors = true
			}

		case config.TargetTypeMarkdown:
			if err := translateMarkdownTarget(ctx, rt, prov, a, targetLangs); err != nil {
				logError(T("[%s] %v"), rt.Target.Name, err)
				hadErrors = true
			}

		case config.TargetTypeProperties:
			if err := translatePropertiesTarget(ctx, rt, prov, a, targetLangs); err != nil {
				logError(T("[%s] %v"), rt.Target.Name, err)
				hadErrors = true
			}

		case config.TargetTypeFlutter:
			if err := translateFlutterTarget(ctx, rt, prov, a, targetLangs); err != nil {
				logError(T("[%s] %v"), rt.Target.Name, err)
				hadErrors = true
			}

		default:
			logWarning(T("[%s] Unknown target type %q, skipping"), rt.Target.Name, rt.Target.Type)
		}
	}

	if hadErrors {
		logError(T("Translation completed with errors"))
		os.Exit(1)
	}
	logSuccess(T("All targets translated!"))
}

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
		Name:       rt.Target.Name,
		Type:       config.ProjectTypeI18Next,
		I18NextDir: transDir,
		SourceLang: rt.Target.SourceLang,
		Languages:  langs,
	}

	if rt.Target.RecipesDir != "" {
		proj.RecipeTransDir = filepath.Join(rt.AbsRoot, rt.Target.RecipesDir)
	}
	if rt.Target.BlogDir != "" {
		proj.BlogPostsDir = filepath.Join(rt.AbsRoot, rt.Target.BlogDir)
	}

	runTranslateI18Next(proj, prov, a)
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
		Name:       rt.Target.Name,
		Type:       config.ProjectTypeI18Next,
		I18NextDir: transDir,
		SourceLang: rt.Target.SourceLang,
		Languages:  langs,
	}

	runTranslateI18Next(proj, prov, a)
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
				langName := lang
				if meta, ok := i18next.LangMeta[lang]; ok {
					langName = meta.Name
				}
				logInfo(T("%s (%s): %d strings to translate (file will be auto-created)"), lang, langName, srcTotal)
				continue
			}
			untranslated := file.UntranslatedKeys()
			count := len(untranslated)
			if a.retranslate {
				_, count, _ = file.Stats()
				count = srcTotal // retranslate all
			}
			langName := lang
			if meta, ok := i18next.LangMeta[lang]; ok {
				langName = meta.Name
			}
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

		langName := lang
		if meta, ok := i18next.LangMeta[lang]; ok {
			langName = meta.Name
		}

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

func runTranslateI18Next(proj *config.Project, prov translate.Provider, a translateArgs) {
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

	hasBlog := proj.BlogPostsDir != ""
	hasRecipes := proj.RecipeTransDir != ""

	if len(targetLangs) == 0 && !hasRecipes && !hasBlog {
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
				langName := lang
				if meta, ok := i18next.LangMeta[lang]; ok {
					langName = meta.Name
				}
				logInfo(T("%s (%s): %d strings to translate (file will be auto-created)"), lang, langName, len(srcKeys))
				continue
			}
			untranslated := file.UntranslatedKeys()
			count := len(untranslated)
			if a.retranslate {
				count = len(file.Keys())
			}
			langName := lang
			if meta, ok := i18next.LangMeta[lang]; ok {
				langName = meta.Name
			}
			logInfo(T("%s (%s): %d strings to translate"), lang, langName, count)
		}
		if hasRecipes {
			for _, lang := range targetLangs {
				langDir := proj.RecipeTransPath(lang)
				if langDir == "" {
					continue
				}
				entries, err := os.ReadDir(langDir)
				if err != nil {
					logInfo(T("%s: recipe translations directory not found"), lang)
					continue
				}
				untranslated := 0
				for _, entry := range entries {
					if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
						continue
					}
					rt, err := i18next.ParseRecipeFile(filepath.Join(langDir, entry.Name()))
					if err != nil || !rt.IsFullyTranslated() {
						untranslated++
					}
				}
				logInfo(T("%s: %d recipe files need translation"), lang, untranslated)
			}
		}
		if hasBlog {
			slugs, err := i18next.BlogPostSlugs(proj.BlogPostsDir)
			if err != nil || len(slugs) == 0 {
				logInfo(T("No blog posts found in %s"), proj.BlogPostsDir)
			} else {
				for _, lang := range targetLangs {
					missing := 0
					needsUpdate := 0
					for _, slug := range slugs {
						transPath := i18next.BlogTranslationPath(proj.BlogPostsDir, slug, lang)
						if _, err := os.Stat(transPath); err != nil {
							missing++
						} else if a.retranslate {
							needsUpdate++
						}
					}
					total := missing + needsUpdate
					if total > 0 {
						logInfo(T("%s: %d blog post(s) need translation"), lang, total)
					} else {
						logInfo(T("%s: all blog posts translated"), lang)
					}
				}
			}
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
				meta := i18next.Meta{Name: lang, Flag: ""}
				if lm, ok := i18next.LangMeta[lang]; ok {
					meta = lm
				}
				file = &i18next.File{
					Meta:         meta,
					Translations: make(map[string]string),
				}
				for _, key := range srcKeys {
					file.Translations[key] = ""
				}
				logInfo(T("Auto-creating %s with %d keys"), filePath, len(srcKeys))
			}

			langName := lang
			if meta, ok := i18next.LangMeta[lang]; ok {
				langName = meta.Name
			}

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

	// Translate recipes
	if hasRecipes {
		logInfo(T("Translating recipe metadata..."))

		recipeOpts := opts
		recipeOpts.PromptType = "recipe" // Use recipe-specific prompt template

		for _, lang := range targetLangs {
			if ctx.Err() != nil {
				break
			}

			langDir := proj.RecipeTransPath(lang)
			if langDir == "" {
				continue
			}

			entries, err := os.ReadDir(langDir)
			if err != nil {
				logError(T("Cannot read recipe directory for %s: %v"), lang, err)
				continue
			}

			langName := lang
			if meta, ok := i18next.LangMeta[lang]; ok {
				langName = meta.Name
			}

			// Build recipe tasks — find untranslated recipe files
			var recipeTasks []translate.RecipeTask
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
					continue
				}

				filePath := filepath.Join(langDir, entry.Name())
				rt, err := i18next.ParseRecipeFile(filePath)
				if err != nil {
					rt = &i18next.RecipeTranslation{}
				}

				if rt.IsFullyTranslated() && !a.retranslate {
					continue
				}

				// Load source (English) recipe data for context
				recipeID := strings.TrimSuffix(entry.Name(), ".json")
				srcRecipePath := filepath.Join(proj.RecipeTransPath(proj.SourceLang), entry.Name())
				srcRecipe, srcErr := i18next.ParseRecipeFile(srcRecipePath)

				task := translate.RecipeTask{
					RecipeID: recipeID,
					Lang:     lang,
					FilePath: filePath,
					Recipe:   rt,
				}

				if srcErr == nil {
					task.SourceName = srcRecipe.Name
					task.SourceDescription = srcRecipe.Description
					task.SourceLongDescription = srcRecipe.LongDescription
				}

				recipeTasks = append(recipeTasks, task)
			}

			if len(recipeTasks) == 0 {
				logInfo(T("  %s: all recipe translations complete"), lang)
				continue
			}

			logInfo(T("  %s (%s): %d recipe files to translate"), lang, langName, len(recipeTasks))

			recipeOpts.Language = lang
			recipeOpts.LanguageName = langName

			if err := translate.TranslateRecipes(ctx, recipeTasks, recipeOpts); err != nil {
				if ctx.Err() != nil {
					logWarning(T("Translation interrupted, partial progress saved"))
					os.Exit(0)
				}
				logError(T("Recipe translation failed for %s: %v"), lang, err)
				hadErrors = true
			}
		}
	}

	// Translate blog posts
	if hasBlog {
		logInfo(T("Translating blog posts..."))

		blogOpts := opts
		blogOpts.PromptType = "blogpost" // Use blogpost-specific prompt template

		slugs, err := i18next.BlogPostSlugs(proj.BlogPostsDir)
		if err != nil || len(slugs) == 0 {
			logWarning(T("No blog posts found in %s"), proj.BlogPostsDir)
		} else {
			for _, lang := range targetLangs {
				if ctx.Err() != nil {
					break
				}

				langName := lang
				if meta, ok := i18next.LangMeta[lang]; ok {
					langName = meta.Name
				}

				// Build blog post tasks
				var blogTasks []translate.BlogPostTask
				for _, slug := range slugs {
					// Load source post
					srcPath := filepath.Join(proj.BlogPostsDir, slug+".md")
					srcPost, srcErr := i18next.ParseBlogPost(srcPath)
					if srcErr != nil {
						logError(T("Cannot read source blog post %s: %v"), srcPath, srcErr)
						continue
					}

					// Load or create translation
					transPath := i18next.BlogTranslationPath(proj.BlogPostsDir, slug, lang)
					var transPost *i18next.BlogPost

					if data, err := os.ReadFile(transPath); err == nil {
						// Existing translation
						transPost, err = i18next.ParseBlogPostData(data)
						if err != nil {
							logWarning(T("Error parsing %s, will recreate: %v"), transPath, err)
							transPost = nil
						}
					}

					if transPost == nil {
						// Create new translation post with inherited fields from source
						transPost = &i18next.BlogPost{
							Author:             srcPost.Author,
							PublishedAt:        srcPost.PublishedAt,
							Tags:               srcPost.Tags,
							FeaturedImage:      srcPost.FeaturedImage,
							Published:          srcPost.Published,
							Order:              srcPost.Order,
							TelegramDiscussion: srcPost.TelegramDiscussion,
							TelegramPostId:     srcPost.TelegramPostId,
						}
					}

					// Check if needs translation
					needsTranslation := transPost.Title == "" || transPost.Content == "" || a.retranslate
					if !needsTranslation {
						continue
					}

					blogTasks = append(blogTasks, translate.BlogPostTask{
						Slug:          slug,
						Lang:          lang,
						FilePath:      transPath,
						Post:          transPost,
						SourceTitle:   srcPost.Title,
						SourceExcerpt: srcPost.Excerpt,
						SourceContent: srcPost.Content,
					})
				}

				if len(blogTasks) == 0 {
					logInfo(T("  %s: all blog posts translated"), lang)
					continue
				}

				logInfo(T("  %s (%s): %d blog post(s) to translate"), lang, langName, len(blogTasks))

				blogOpts.Language = lang
				blogOpts.LanguageName = langName

				if err := translate.TranslateBlogPosts(ctx, blogTasks, blogOpts); err != nil {
					if ctx.Err() != nil {
						logWarning(T("Translation interrupted, partial progress saved"))
						os.Exit(0)
					}
					logError(T("Blog post translation failed for %s: %v"), lang, err)
					hadErrors = true
				}
			}
		}
	}

	if hadErrors {
		logError(T("Translation completed with errors"))
		os.Exit(1)
	}
	logSuccess(T("Translation complete!"))
}

// ---------------------------------------------------------------------------
// auth (login / logout / list)
// ---------------------------------------------------------------------------

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: T("Manage provider authentication"),
		Long: T(`Manage authentication credentials for all AI providers.

OAuth providers (interactive browser/device flow):
  copilot       GitHub Copilot (device code flow, free with GitHub account)
  gemini        Google Gemini Code Assist (browser OAuth, free tier: 60 req/min)

API key providers (paste your key):
  google        Google AI Studio (Gemini API key)
  groq          Groq Cloud (free tier available)
  opencode      OpenCode Zen API (free models available without key)
  custom-openai Custom OpenAI-compatible endpoint

No auth required:
  ollama        Local Ollama server

Examples:
  lokit auth login                         Interactive provider selection
  lokit auth login --provider copilot      OAuth with GitHub Copilot
  lokit auth login --provider google       Store Google AI API key
  lokit auth logout --provider google      Remove Google API key
  lokit auth logout                        Remove all credentials
  lokit auth list                          Show all stored credentials`),
	}

	cmd.AddCommand(
		newAuthLoginCmd(),
		newAuthLogoutCmd(),
		newAuthListCmd(),
	)

	return cmd
}

// allProviders is the ordered list of providers for the interactive menu.
var allProviders = []struct {
	id   string
	name string
	desc string
	auth string // "oauth", "api-key", "none"
}{
	{"copilot", "GitHub Copilot", "free with GitHub account", "oauth"},
	{"gemini", "Google Gemini", "Code Assist, free tier: 60 req/min", "oauth"},
	{"google", "Google AI Studio", "Gemini API key, free tier available", "api-key"},
	{"groq", "Groq Cloud", "fast inference, free tier available", "api-key"},
	{"opencode", "OpenCode", "multi-provider proxy", "api-key"},
	{"custom-openai", "Custom OpenAI", "any OpenAI-compatible endpoint", "api-key"},
	{"ollama", "Ollama", "local server, no auth needed", "none"},
}

func newAuthLoginCmd() *cobra.Command {
	var provider string

	cmd := &cobra.Command{
		Use:   "login",
		Short: T("Authenticate with an AI provider"),
		Long: T(`Authenticate with an AI provider using OAuth or API key.

If --provider is not specified, you will be prompted to choose.

OAuth providers:
  copilot       Device code flow — enter code in browser
  gemini        Browser-based OAuth — sign in with Google

API key providers:
  google        Paste your Google AI Studio API key
  groq          Paste your Groq API key
  opencode      Paste your OpenCode API key
  custom-openai Paste your API key + endpoint URL`),
		Run: func(cmd *cobra.Command, args []string) {
			// If no provider specified, prompt user
			if provider == "" {
				sectionHeader(T("Select provider to authenticate"))
				fmt.Fprintln(os.Stderr)
				for i, p := range allProviders {
					if p.auth == "none" {
						continue // Skip ollama — no auth needed
					}
					authLabel := ""
					switch p.auth {
					case "oauth":
						authLabel = T("OAuth")
					case "api-key":
						authLabel = T("API key")
					}
					fmt.Fprintf(os.Stderr, "  %d. %s%-13s%s %s (%s)\n",
						i+1, colorYellow, p.id, colorReset, T(p.desc), authLabel)
				}
				fmt.Fprintln(os.Stderr)
				fmt.Fprintf(os.Stderr, "  %s ", T("Enter choice (number or name):"))

				scanner := bufio.NewScanner(os.Stdin)
				if !scanner.Scan() {
					logError(T("No input received"))
					os.Exit(1)
				}
				choice := strings.TrimSpace(scanner.Text())

				// Try as number first
				found := false
				displayIdx := 0
				for _, p := range allProviders {
					if p.auth == "none" {
						continue
					}
					displayIdx++
					if choice == fmt.Sprintf("%d", displayIdx) || choice == p.id {
						provider = p.id
						found = true
						break
					}
				}
				if !found {
					logError(T("Invalid choice. Use: lokit auth login --provider PROVIDER"))
					os.Exit(1)
				}
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt)
			go func() {
				<-sigCh
				cancel()
			}()

			switch provider {
			case "copilot":
				authLoginCopilot(ctx)
			case "gemini":
				authLoginGemini(ctx)
			case "google", "groq", "opencode":
				authLoginAPIKey(provider)
			case "custom-openai":
				authLoginCustomOpenAI()
			default:
				logError(T("Unknown provider '%s'. Run 'lokit auth login' for options."), provider)
				os.Exit(1)
			}
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", T("Provider to authenticate"))
	_ = cmd.RegisterFlagCompletionFunc("provider", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		completions := make([]string, 0, len(allProviders))
		for _, p := range allProviders {
			if p.auth == "none" {
				continue
			}
			completions = append(completions, fmt.Sprintf("%s\t%s", p.id, p.name))
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

func authLoginCopilot(ctx context.Context) {
	sectionHeader(T("GitHub Copilot Authentication"))

	_, err := copilot.DeviceCodeFlow(ctx, func(verificationURI, userCode string) {
		logInfo(T("Open this URL in your browser:"))
		fmt.Fprintf(os.Stderr, "     %s%s%s\n\n", colorGreen, verificationURI, colorReset)
		logInfo(T("Enter this code:"))
		fmt.Fprintf(os.Stderr, "     %s%s%s\n\n", colorYellow, userCode, colorReset)
		logInfo(T("Waiting for authorization..."))
	})
	if err != nil {
		if ctx.Err() != nil {
			logWarning(T("Authentication cancelled"))
			os.Exit(0)
		}
		logError(T("Authentication failed: %v"), err)
		os.Exit(1)
	}

	logSuccess(T("Copilot authentication successful!"))
	logInfo(T("You can now use: lokit translate --provider copilot --model gpt-4o"))
	fmt.Fprintln(os.Stderr)
}

func authLoginGemini(ctx context.Context) {
	sectionHeader(T("Google Gemini Authentication"))

	accessToken, err := gemini.AuthCodeFlow(ctx, func(authURL string) {
		logInfo(T("Opening browser for Google sign-in..."))
		fmt.Fprintln(os.Stderr)
		logInfo(T("If the browser doesn't open, visit:"))
		fmt.Fprintf(os.Stderr, "     %s%s%s\n\n", colorGreen, authURL, colorReset)
		logInfo(T("Waiting for authorization..."))
	})
	if err != nil {
		if ctx.Err() != nil {
			logWarning(T("Authentication cancelled"))
			os.Exit(0)
		}
		logError(T("Authentication failed: %v"), err)
		os.Exit(1)
	}

	_ = accessToken // Token is saved by AuthCodeFlow; we use the stored Info for setup
	logSuccess(T("Gemini authentication successful!"))

	// Run Code Assist onboarding to get project ID
	fmt.Fprintln(os.Stderr)
	info := gemini.LoadToken()
	if info == nil {
		logWarning(T("Token was saved but cannot be loaded"))
	} else {
		_, err = gemini.SetupUser(ctx, info)
		if errors.Is(err, gemini.ErrProjectIDRequired) {
			// No project ID available — ask user for GCP project ID
			fmt.Fprintln(os.Stderr)
			logWarning(T("Gemini Code Assist requires a GCP project ID to work in your region."))
			logInfo(T("Find your project ID at: https://console.cloud.google.com"))
			logInfo(T("(Create a project if you don't have one, then enable the Gemini API)"))
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "  %s ", T("GCP Project ID (or press Enter to skip):"))

			scanner := bufio.NewScanner(os.Stdin)
			if scanner.Scan() {
				projectID := strings.TrimSpace(scanner.Text())
				if projectID != "" {
					info.ProjectID = projectID
					// Save the project ID immediately so it persists
					_ = gemini.SaveProjectID(projectID)
					// Try onboarding again with the project
					_, err = gemini.SetupUser(ctx, info)
					if err != nil {
						logWarning(T("Code Assist setup failed: %v"), err)
						logInfo(T("Project ID saved. You can try again later."))
					} else {
						logSuccess(T("Code Assist project configured!"))
					}
				} else {
					logInfo(T("Skipped. You can set it later with:"))
					logInfo(T("  lokit auth login --provider gemini"))
					fmt.Fprintln(os.Stderr)
				}
			}
		} else if err != nil {
			logWarning(T("Code Assist setup failed: %v"), err)
			logInfo(T("OAuth login succeeded but Code Assist onboarding failed."))
			logInfo(T("This will be retried automatically on first translate."))
		} else {
			logSuccess(T("Code Assist project configured!"))
		}
	}

	fmt.Fprintln(os.Stderr)
	logInfo(T("You can now use: lokit translate --provider gemini --model gemini-2.5-flash"))
	logInfo(T("(no API key needed when authenticated via OAuth)"))
	fmt.Fprintln(os.Stderr)
}

func authLoginAPIKey(providerID string) {
	// Provider display info
	providerInfo := map[string]struct {
		name    string
		helpURL string
		example string
	}{
		"google": {
			name:    "Google AI Studio",
			helpURL: "https://aistudio.google.com/apikey",
			example: "lokit translate --provider google --model gemini-2.5-flash",
		},
		"groq": {
			name:    "Groq Cloud",
			helpURL: "https://console.groq.com/keys",
			example: "lokit translate --provider groq --model llama-3.3-70b-versatile",
		},
		"opencode": {
			name:    "OpenCode",
			helpURL: "",
			example: "lokit translate --provider opencode --model gemini-2.5-flash",
		},
	}

	info := providerInfo[providerID]

	sectionHeader(fmt.Sprintf(T("%s — API Key Setup"), info.name))

	if info.helpURL != "" {
		logInfo(T("Get your API key from: %s%s%s"), colorGreen, info.helpURL, colorReset)
		fmt.Fprintln(os.Stderr)
	}

	// Check if already configured
	existing := settings.GetAPIKey(providerID)
	if existing != "" {
		keyVal(T("Current key"), fmt.Sprintf("%s%s%s", colorYellow, settings.MaskKey(existing), colorReset))
		fmt.Fprintf(os.Stderr, "  %s ", T("Enter new key to replace, or press Enter to keep:"))
	} else {
		fmt.Fprintf(os.Stderr, "  %s ", T("Enter API key:"))
	}

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		logError(T("No input received"))
		os.Exit(1)
	}
	key := strings.TrimSpace(scanner.Text())

	if key == "" {
		if existing != "" {
			logInfo(T("Keeping existing key"))
			return
		}
		logError(T("No API key provided"))
		os.Exit(1)
	}

	if err := settings.SetAPIKey(providerID, key); err != nil {
		logError(T("Failed to save API key: %v"), err)
		os.Exit(1)
	}

	logSuccess(T("%s API key saved!"), info.name)
	logInfo(T("You can now use: %s"), info.example)
	fmt.Fprintln(os.Stderr)
}

func authLoginCustomOpenAI() {
	sectionHeader(T("Custom OpenAI-Compatible Endpoint"))

	scanner := bufio.NewScanner(os.Stdin)

	// Base URL
	existing := settings.Get("custom-openai")
	if existing != nil && existing.BaseURL != "" {
		keyVal(T("Current endpoint"), fmt.Sprintf("%s%s%s", colorYellow, existing.BaseURL, colorReset))
		fmt.Fprintf(os.Stderr, "  %s ", T("Enter new endpoint URL, or press Enter to keep:"))
	} else {
		fmt.Fprintf(os.Stderr, "  %s ", T("Enter endpoint URL (e.g., https://api.example.com/v1):"))
	}

	if !scanner.Scan() {
		logError(T("No input received"))
		os.Exit(1)
	}
	baseURL := strings.TrimSpace(scanner.Text())

	if baseURL == "" && existing != nil && existing.BaseURL != "" {
		baseURL = existing.BaseURL
	}
	if baseURL == "" {
		logError(T("Endpoint URL is required"))
		os.Exit(1)
	}

	// API key (optional for some endpoints)
	if existing != nil && existing.Key != "" {
		keyVal(T("Current key"), fmt.Sprintf("%s%s%s", colorYellow, settings.MaskKey(existing.Key), colorReset))
		fmt.Fprintf(os.Stderr, "  %s ", T("Enter new API key, or press Enter to keep (leave empty for none):"))
	} else {
		fmt.Fprintf(os.Stderr, "  %s ", T("Enter API key (or press Enter if not required):"))
	}

	if !scanner.Scan() {
		logError(T("No input received"))
		os.Exit(1)
	}
	apiKey := strings.TrimSpace(scanner.Text())

	if apiKey == "" && existing != nil {
		apiKey = existing.Key
	}

	if err := settings.SetAPIKeyWithBaseURL("custom-openai", apiKey, baseURL); err != nil {
		logError(T("Failed to save credentials: %v"), err)
		os.Exit(1)
	}

	logSuccess(T("Custom OpenAI endpoint saved!"))
	logInfo(T("You can now use: lokit translate --provider custom-openai --model MODEL_NAME"))
	fmt.Fprintln(os.Stderr)
}

func newAuthLogoutCmd() *cobra.Command {
	var provider string

	cmd := &cobra.Command{
		Use:   "logout",
		Short: T("Remove stored credentials"),
		Long: T(`Remove stored credentials for one or all providers.

If --provider is not specified, credentials for ALL providers are removed.

Examples:
  lokit auth logout                        Remove all credentials
  lokit auth logout --provider copilot     Remove only Copilot OAuth
  lokit auth logout --provider google      Remove only Google API key
  lokit auth logout --provider gemini      Remove only Gemini OAuth`),
		Run: func(cmd *cobra.Command, args []string) {
			if provider != "" {
				// Remove specific provider
				switch provider {
				case "copilot":
					if err := copilot.DeleteToken(); err != nil {
						logError(T("Failed to remove Copilot credentials: %v"), err)
						os.Exit(1)
					}
					logSuccess(T("Copilot credentials removed"))
				case "gemini":
					if err := gemini.DeleteToken(); err != nil {
						logError(T("Failed to remove Gemini credentials: %v"), err)
						os.Exit(1)
					}
					logSuccess(T("Gemini credentials removed"))
				case "google", "groq", "opencode", "custom-openai":
					if err := settings.Remove(provider); err != nil {
						logError(T("Failed to remove %s credentials: %v"), provider, err)
						os.Exit(1)
					}
					logSuccess(T("%s credentials removed"), provider)
				default:
					logError(T("Unknown provider '%s'. Run 'lokit auth list' to see providers."), provider)
					os.Exit(1)
				}
				return
			}

			// Remove all
			errCount := 0
			if err := copilot.DeleteToken(); err != nil {
				logError(T("Failed to remove Copilot credentials: %v"), err)
				errCount++
			}
			if err := gemini.DeleteToken(); err != nil {
				logError(T("Failed to remove Gemini credentials: %v"), err)
				errCount++
			}
			for _, pid := range []string{"google", "groq", "opencode", "custom-openai"} {
				if err := settings.Remove(pid); err != nil {
					logError(T("Failed to remove %s credentials: %v"), pid, err)
					errCount++
				}
			}
			if errCount == 0 {
				logSuccess(T("All stored credentials removed"))
			}
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", T("Provider to logout (default: all)"))
	_ = cmd.RegisterFlagCompletionFunc("provider", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		completions := make([]string, 0, len(allProviders))
		for _, p := range allProviders {
			if p.auth == "none" {
				continue
			}
			completions = append(completions, fmt.Sprintf("%s\t%s", p.id, p.name))
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

func newAuthListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   T("Show stored credentials and status"),
		Run: func(cmd *cobra.Command, args []string) {
			sectionHeader(T("Stored Credentials"))

			// OAuth providers
			fmt.Fprintf(os.Stderr, "\n  %s%s%s\n", colorBold+colorYellow, T("OAuth Providers"), colorReset)
			keyVal(T("copilot"), copilot.TokenStatus())
			keyVal(T("gemini"), gemini.TokenStatus())

			// API key providers
			fmt.Fprintf(os.Stderr, "\n  %s%s%s\n", colorBold+colorYellow, T("API Key Providers"), colorReset)
			apiKeyProviders := []struct {
				id   string
				name string
			}{
				{"google", "Google AI Studio"},
				{"groq", "Groq Cloud"},
				{"opencode", "OpenCode"},
				{"custom-openai", "Custom OpenAI"},
			}
			for _, p := range apiKeyProviders {
				entry := settings.Get(p.id)
				if entry != nil && entry.Key != "" {
					status := fmt.Sprintf("%s%s%s (key: %s)", colorGreen, T("✓ configured"), colorReset, settings.MaskKey(entry.Key))
					if entry.BaseURL != "" {
						status += fmt.Sprintf("\n  %14s %s %s", "", T("endpoint:"), entry.BaseURL)
					}
					keyVal(p.id, status)
				} else if p.id == "custom-openai" && entry != nil && entry.BaseURL != "" {
					// custom-openai may have just a URL, no key
					status := fmt.Sprintf("%s%s%s (%s)", colorGreen, T("✓ configured"), colorReset, T("no key"))
					status += fmt.Sprintf("\n  %14s %s %s", "", T("endpoint:"), entry.BaseURL)
					keyVal(p.id, status)
				} else {
					keyVal(p.id, fmt.Sprintf("%s%s%s", colorRed, T("not configured"), colorReset))
				}
			}

			// Environment variables
			fmt.Fprintf(os.Stderr, "\n  %s%s%s\n", colorBold+colorYellow, T("Environment Variables"), colorReset)
			envProviders := []struct {
				id string
			}{
				{"google"},
				{"groq"},
				{"opencode"},
				{"custom-openai"},
			}
			for _, p := range envProviders {
				envVar := settings.EnvVarForProvider(p.id)
				if v := os.Getenv(envVar); v != "" {
					keyVal(envVar, fmt.Sprintf("%s%s%s (%s)", colorGreen, settings.MaskKey(v), colorReset, p.id))
				} else {
					keyVal(envVar, fmt.Sprintf("%s%s%s", colorDim, T("not set"), colorReset))
				}
			}
			fmt.Fprintln(os.Stderr)
		},
	}
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// doExtract runs xgettext extraction for the project. Returns nil on success.
// Used by both 'init' and auto-extraction in 'translate'.
func doExtract(proj *config.Project) error {
	// If no source dirs configured, scan the project root
	scanDirs := proj.SourceDirs
	if len(scanDirs) == 0 {
		absRoot, _ := filepath.Abs(filepath.Dir(proj.PODir))
		scanDirs = []string{absRoot}
	}

	logInfo(T("Scanning for source files in: %s"), strings.Join(scanDirs, ", "))

	allFiles, err := extract.FindSources(scanDirs)
	if err != nil {
		return fmt.Errorf(T("scanning sources: %w"), err)
	}

	if len(allFiles) == 0 {
		return fmt.Errorf(T("no source files found (supported: %s)"),
			strings.Join(extract.SupportedExtensionsList(), ", "))
	}

	// Make file paths relative to the project root so xgettext writes short
	// #: references (e.g. "scripts/main.sh:42" instead of
	// "/home/user/project/scripts/main.sh:42").
	// Use proj.Root when available (set from --root flag or target root);
	// fall back to cwd for backward compatibility with auto-detected projects.
	baseDir := proj.Root
	if baseDir == "" {
		baseDir, _ = os.Getwd()
	}
	for i, f := range allFiles {
		if rel, err := filepath.Rel(baseDir, f); err == nil {
			allFiles[i] = rel
		}
	}

	logInfo(T("Found %d source files (%s)"), len(allFiles), extract.DescribeFiles(allFiles))

	// Split into Go files (need xgotext) and everything else (need xgettext)
	goFiles, otherFiles := extract.SplitGoFiles(allFiles)

	potFile := proj.POTFile
	var finalPOT string

	// Helper: extract Go files using the appropriate tool.
	// If project has keywords configured, use AST-based extraction (handles
	// wrapper functions like T(), N()). Otherwise use xgotext (handles direct
	// gotext.Get() calls).
	extractGo := func(goFiles []string, outPotFile string) (*extract.ExtractResult, error) {
		// Collect unique directories containing Go files
		goDirSet := make(map[string]bool)
		for _, f := range goFiles {
			goDirSet[filepath.Dir(f)] = true
		}
		var goDirs []string
		for d := range goDirSet {
			goDirs = append(goDirs, d)
		}

		if len(proj.Keywords) > 0 {
			logInfo(T("Extracting from %d Go files (AST, keywords: %s)..."),
				len(goFiles), strings.Join(proj.Keywords, ", "))
			return extract.RunGoExtract(goDirs, outPotFile, proj.Name, proj.Keywords)
		}
		logInfo(T("Extracting from %d Go files with xgotext..."), len(goFiles))
		return extract.RunXgotext(goDirs, outPotFile, proj.Name)
	}

	switch {
	case len(otherFiles) > 0 && len(goFiles) > 0:
		// Both Go and non-Go files: extract separately, then merge
		logInfo(T("Extracting from %d non-Go files with xgettext..."), len(otherFiles))
		xgettextResult, err := extract.RunXgettext(otherFiles, potFile, proj.Name, proj.Version, proj.BugsEmail, proj.Keywords)
		if err != nil {
			return fmt.Errorf(T("xgettext extraction failed: %w"), err)
		}

		// Extract Go files to a temp POT
		goPotFile := potFile + ".go.tmp"
		defer os.Remove(goPotFile)

		goResult, err := extractGo(goFiles, goPotFile)
		if err != nil {
			logError(T("Go extraction failed: %v"), err)
			logInfo(T("Continuing with xgettext results only"))
			finalPOT = xgettextResult.POTFile
			break
		}
		_ = goResult

		// Merge the two POT files
		logInfo(T("Merging POT files..."))
		if err := extract.MergePOTFiles(xgettextResult.POTFile, goPotFile, potFile); err != nil {
			return fmt.Errorf(T("merging POT files: %w"), err)
		}
		finalPOT = potFile

	case len(otherFiles) > 0:
		// Only non-Go files
		result, err := extract.RunXgettext(otherFiles, potFile, proj.Name, proj.Version, proj.BugsEmail, proj.Keywords)
		if err != nil {
			return fmt.Errorf(T("extraction failed: %w"), err)
		}
		finalPOT = result.POTFile

	case len(goFiles) > 0:
		// Only Go files
		result, err := extractGo(goFiles, potFile)
		if err != nil {
			return fmt.Errorf(T("Go extraction failed: %w"), err)
		}
		finalPOT = result.POTFile
	}

	// Count extracted strings
	potPO, err := po.ParseFile(finalPOT)
	if err == nil {
		count := 0
		for _, e := range potPO.Entries {
			if e.MsgID != "" && !e.Obsolete {
				count++
			}
		}
		logSuccess(T("Extracted %d strings to %s"), count, finalPOT)
	} else {
		logSuccess(T("Extracted strings to %s"), finalPOT)
	}

	return nil
}

// fileExists returns true if the file exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// createPOFromPOT creates a new PO file from the POT template for the given language.
// Returns nil if the POT file can't be found or read.
func createPOFromPOT(proj *config.Project, lang, poPath string) *po.File {
	potPath := proj.POTPathResolved()
	if potPath == "" || !fileExists(potPath) {
		// No POT template — try auto-extracting first
		logInfo(T("No POT template found, running extraction..."))
		if err := doExtract(proj); err != nil {
			logError(T("Auto-extraction failed: %v"), err)
			return nil
		}
		// Re-resolve POT path after extraction
		proj.POTFile = proj.POTPathResolved()
		potPath = proj.POTFile
		if potPath == "" || !fileExists(potPath) {
			logError(T("Cannot auto-create %s: extraction produced no POT template"), poPath)
			logInfo(T("Check that source files contain translatable strings (_(), N_(), etc.)"))
			return nil
		}
	}

	potPO, err := po.ParseFile(potPath)
	if err != nil {
		logError(T("Cannot read POT template %s: %v"), potPath, err)
		return nil
	}

	newPO := po.NewFile()
	newPO.Header = po.MakeHeader(proj.Name, proj.Version, proj.BugsEmail, proj.CopyrightHolder, lang)
	newPO.SetHeaderField("Plural-Forms", po.PluralFormsForLang(lang))

	for _, e := range potPO.Entries {
		entry := &po.Entry{
			ExtractedComments: e.ExtractedComments,
			References:        e.References,
			Flags:             copyFlags(e.Flags),
			MsgCtxt:           e.MsgCtxt,
			MsgID:             e.MsgID,
			MsgIDPlural:       e.MsgIDPlural,
			MsgStr:            "",
			MsgStrPlural:      make(map[int]string),
		}
		newPO.Entries = append(newPO.Entries, entry)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(poPath), 0755); err != nil {
		logError(T("Creating directory for %s: %v"), poPath, err)
		return nil
	}

	if err := newPO.WriteFile(poPath); err != nil {
		logError(T("Creating %s: %v"), poPath, err)
		return nil
	}

	logSuccess(T("Auto-created %s from %s (%d entries)"), poPath, potPath, len(newPO.Entries))
	return newPO
}

func copyFlags(flags []string) []string {
	if len(flags) == 0 {
		return nil
	}
	var result []string
	for _, f := range flags {
		if f != "fuzzy" {
			result = append(result, f)
		}
	}
	return result
}

func resolveProvider(name, baseURL, apiKey, model, proxy string, timeout time.Duration) translate.Provider {
	defaults := translate.DefaultProviders()

	var prov translate.Provider

	if p, ok := defaults[strings.ToLower(name)]; ok {
		prov = p
	} else {
		prov = translate.Provider{
			ID:      translate.ProviderCustomOpenAI,
			Name:    name,
			BaseURL: name,
			Timeout: 60 * time.Second,
		}
	}

	if baseURL != "" {
		prov.BaseURL = baseURL
	} else if prov.ID == translate.ProviderCustomOpenAI {
		// Check credentials store for base URL
		if storedURL := settings.GetBaseURL(prov.ID); storedURL != "" {
			prov.BaseURL = storedURL
		}
	}
	if apiKey != "" {
		prov.APIKey = apiKey
	}
	if model != "" {
		prov.Model = model
	}
	if proxy != "" {
		prov.Proxy = proxy
	}
	if timeout > 0 {
		prov.Timeout = timeout
	}

	return prov
}

func validateProvider(prov translate.Provider, apiKey string) error {
	// Check if model is specified
	if prov.Model == "" {
		modelExamples := map[string]string{
			translate.ProviderGoogle:       "gemini-2.5-flash, gemini-2.0-flash-exp, gemini-1.5-pro",
			translate.ProviderGemini:       "gemini-2.5-flash, gemini-2.0-flash-exp, gemini-1.5-pro",
			translate.ProviderGroq:         "llama-3.3-70b-versatile, mixtral-8x7b-32768",
			translate.ProviderOpenCode:     "big-pickle, gemini-2.5-flash, claude-sonnet-4.5, gpt-4o",
			translate.ProviderCopilot:      "gpt-4o, gpt-5, claude-sonnet-4, gemini-2.5-pro",
			translate.ProviderOllama:       "llama3.2, qwen2.5, mistral",
			translate.ProviderCustomOpenAI: "gpt-4o, gpt-4o-mini (depends on your endpoint)",
		}

		examples := modelExamples[prov.ID]
		if examples == "" {
			examples = T("check provider documentation")
		}

		return fmt.Errorf(T("--model is required for provider '%s'\n\n"+
			"Example models for %s:\n  %s\n\n"+
			"Usage: --provider %s --model MODEL_NAME"),
			prov.ID, prov.Name, examples, prov.ID)
	}

	switch prov.ID {
	case translate.ProviderGoogle:
		if apiKey == "" {
			// Check if Gemini OAuth token is available
			if gemini.LoadToken() != nil {
				// OAuth token available, no API key needed
				break
			}
			return fmt.Errorf(T("provider 'google' requires an API key or Gemini OAuth login\n\n" +
				"Option 1: Store an API key:\n" +
				"  lokit auth login --provider google\n\n" +
				"Option 2: Login with Google OAuth (free tier: 60 req/min):\n" +
				"  lokit auth login --provider gemini\n\n" +
				"Option 3: Pass key directly:\n" +
				"  --api-key YOUR_KEY or export GOOGLE_API_KEY=YOUR_KEY\n\n" +
				"Get an API key from: https://aistudio.google.com/apikey"))
		}

	case translate.ProviderGemini:
		if gemini.LoadToken() == nil {
			return fmt.Errorf(T("provider 'gemini' requires Google OAuth login\n\n" +
				"Login with your Google account:\n" +
				"  lokit auth login --provider gemini\n\n" +
				"This uses Gemini Code Assist (free tier: 60 req/min).\n" +
				"For API key access, use --provider google instead."))
		}

	case translate.ProviderGroq:
		if apiKey == "" {
			return fmt.Errorf(T("provider 'groq' requires an API key\n\n" +
				"Option 1: Store your API key:\n" +
				"  lokit auth login --provider groq\n\n" +
				"Option 2: Pass key directly:\n" +
				"  --api-key YOUR_KEY or export GROQ_API_KEY=YOUR_KEY\n\n" +
				"Get a free API key from: https://console.groq.com/keys"))
		}

	case translate.ProviderOpenCode:
		// OpenCode can work without API key for some models

	case translate.ProviderCopilot:
		if copilot.LoadToken() == nil {
			return fmt.Errorf(T("provider 'copilot' requires GitHub Copilot authentication\n\n" +
				"Login with your GitHub account:\n" +
				"  lokit auth login --provider copilot\n\n" +
				"This uses GitHub Copilot (requires active Copilot subscription)."))
		}

	case translate.ProviderCustomOpenAI:
		if prov.BaseURL == "" {
			return fmt.Errorf(T("provider 'custom-openai' requires an endpoint URL\n\n" +
				"Option 1: Configure via auth:\n" +
				"  lokit auth login --provider custom-openai\n\n" +
				"Option 2: Pass directly:\n" +
				"  --base-url https://api.example.com/v1"))
		}

	case translate.ProviderOllama:
		client := &http.Client{Timeout: 2 * time.Second}
		ollamaURL := prov.BaseURL
		if ollamaURL == "" {
			ollamaURL = "http://localhost:11434"
		}
		resp, err := client.Get(ollamaURL + "/api/tags")
		if err != nil {
			return fmt.Errorf(T("provider 'ollama' requires Ollama server to be running\n\n" +
				"Start Ollama with: ollama serve\n" +
				"Install from: https://ollama.com\n" +
				"Alternative providers:\n" +
				"  --provider copilot         (GitHub Copilot, free)\n" +
				"  --provider google          (requires API key)"))
		}
		resp.Body.Close()
	}

	return nil
}

// translateYAMLTarget translates YAML translation files.
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

	// Locate source file.
	srcLang := rt.Target.SourceLang
	srcPath := ""
	for _, ext := range []string{".yaml", ".yml"} {
		candidate := filepath.Join(transDir, srcLang+ext)
		if _, err := os.Stat(candidate); err == nil {
			srcPath = candidate
			break
		}
	}
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
			filePath := ""
			for _, ext := range []string{".yaml", ".yml"} {
				candidate := filepath.Join(transDir, lang+ext)
				if _, err := os.Stat(candidate); err == nil {
					filePath = candidate
					break
				}
			}

			langName := lang
			if meta, ok := i18next.LangMeta[lang]; ok {
				langName = meta.Name
			}

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

	// Build translate tasks.
	var tasks []translate.YAMLLangTask
	for _, lang := range langs {
		langName := lang
		if meta, ok := i18next.LangMeta[lang]; ok {
			langName = meta.Name
		}

		// Find or init target file.
		filePath := ""
		for _, ext := range []string{".yaml", ".yml"} {
			candidate := filepath.Join(transDir, lang+ext)
			if _, err := os.Stat(candidate); err == nil {
				filePath = candidate
				break
			}
		}

		var targetFile *yamlfile.File
		if filePath == "" {
			filePath = filepath.Join(transDir, lang+".yaml")
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

	systemPrompt := rt.Target.Prompt

	parallelMode := translate.ParallelSequential
	if a.parallel {
		parallelMode = translate.ParallelFullParallel
	}

	opts := translate.Options{
		Provider:            prov,
		SystemPrompt:        systemPrompt,
		RetranslateExisting: a.retranslate,
		ChunkSize:           a.chunkSize,
		RequestDelay:        a.requestDelay,
		Timeout:             a.timeout,
		MaxRetries:          a.maxRetries,
		ParallelMode:        parallelMode,
		MaxConcurrent:       a.maxConcurrent,
		Verbose:             a.verbose,
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

	return translate.TranslateAllYAML(ctx, tasks, opts)
}

// ---------------------------------------------------------------------------
// Markdown helpers
// ---------------------------------------------------------------------------

// showConfigMarkdownStats shows translation stats for a Markdown target.
// Source files live in dir/SOURCE_LANG/*.md and targets in
// dir/LANG/*.md.
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

	// Count total translatable segments across all source files.
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

// runInitMarkdown creates or syncs Markdown translation files from the source directory.
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
		if err := os.MkdirAll(langDir, 0755); err != nil {
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
				// Create new empty translation file.
				newFile := mdfile.NewTranslationFile(srcFile)
				if err := newFile.WriteFile(targetPath); err != nil {
					logError(T("Creating %s: %v"), targetPath, err)
					continue
				}
				logSuccess(T("Created: %s (%d segments)"), targetPath, len(srcFile.Keys()))
				created++
			} else {
				// Sync existing file.
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

// translateMarkdownTarget translates Markdown files for a single target.
// Source: dir/SOURCE_LANG/*.md -> Target: dir/LANG/*.md
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
			langName := lang
			if meta, ok := i18next.LangMeta[lang]; ok {
				langName = meta.Name
			}
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

	// Build tasks — one per (lang, file) pair.
	var tasks []translate.MarkdownLangTask
	for _, lang := range langs {
		langName := lang
		if meta, ok := i18next.LangMeta[lang]; ok {
			langName = meta.Name
		}
		langDir := filepath.Join(transDir, lang)
		if err := os.MkdirAll(langDir, 0755); err != nil {
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

			tasks = append(tasks, translate.MarkdownLangTask{
				Lang:       lang,
				LangName:   langName,
				FilePath:   targetPath,
				File:       targetFile,
				SourceFile: srcFile,
			})
		}
	}

	if len(tasks) == 0 {
		logInfo(T("No Markdown files to translate"))
		return nil
	}

	systemPrompt := rt.Target.Prompt

	parallelMode := translate.ParallelSequential
	if a.parallel {
		parallelMode = translate.ParallelFullParallel
	}

	opts := translate.Options{
		Provider:            prov,
		SystemPrompt:        systemPrompt,
		RetranslateExisting: a.retranslate,
		ChunkSize:           a.chunkSize,
		RequestDelay:        a.requestDelay,
		Timeout:             a.timeout,
		MaxRetries:          a.maxRetries,
		ParallelMode:        parallelMode,
		MaxConcurrent:       a.maxConcurrent,
		Verbose:             a.verbose,
		OnLog: func(format string, args ...any) {
			logInfo(format, args...)
		},
		OnError: func(format string, args ...any) {
			logError(format, args...)
		},
		OnProgress: func(lang string, done, total int) {
			logInfo(T("  %s: %d/%d segments"), lang, done, total)
		},
	}

	return translate.TranslateAllMarkdown(ctx, tasks, opts)
}

// ---------------------------------------------------------------------------
// Properties (.properties) helpers
// ---------------------------------------------------------------------------

// showConfigPropertiesStats shows translation stats for a .properties target.
func showConfigPropertiesStats(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	keyVal(T("Translations"), transDir)

	srcLang := rt.Target.SourceLang
	srcPath := filepath.Join(transDir, srcLang+".properties")
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
		filePath := filepath.Join(transDir, lang+".properties")
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

// runInitProperties creates or syncs .properties translation files from the source file.
func runInitProperties(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	srcLang := rt.Target.SourceLang
	srcPath := filepath.Join(transDir, srcLang+".properties")

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
		targetPath := filepath.Join(transDir, lang+".properties")

		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			newFile := propfile.NewTranslationFile(srcFile)
			if err := os.MkdirAll(transDir, 0755); err != nil {
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

// translatePropertiesTarget translates .properties files for a single target.
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

	srcLang := rt.Target.SourceLang
	srcPath := filepath.Join(transDir, srcLang+".properties")
	srcFile, err := propfile.ParseFile(srcPath)
	if err != nil {
		return fmt.Errorf(T("cannot read source .properties file %s: %w"), srcPath, err)
	}
	srcTotal, _, _ := srcFile.Stats()
	logInfo(T("Source strings: %d"), srcTotal)

	if a.dryRun {
		for _, lang := range langs {
			langName := lang
			if meta, ok := i18next.LangMeta[lang]; ok {
				langName = meta.Name
			}
			targetPath := filepath.Join(transDir, lang+".properties")
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
		langName := lang
		if meta, ok := i18next.LangMeta[lang]; ok {
			langName = meta.Name
		}
		targetPath := filepath.Join(transDir, lang+".properties")

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

		tasks = append(tasks, translate.PropertiesLangTask{
			Lang:       lang,
			LangName:   langName,
			FilePath:   targetPath,
			File:       targetFile,
			SourceFile: srcFile,
		})
	}

	if len(tasks) == 0 {
		logInfo(T("No .properties files to translate"))
		return nil
	}

	parallelMode := translate.ParallelSequential
	if a.parallel {
		parallelMode = translate.ParallelFullParallel
	}

	opts := translate.Options{
		Provider:            prov,
		SystemPrompt:        rt.Target.Prompt,
		RetranslateExisting: a.retranslate,
		ChunkSize:           a.chunkSize,
		RequestDelay:        a.requestDelay,
		Timeout:             a.timeout,
		MaxRetries:          a.maxRetries,
		ParallelMode:        parallelMode,
		MaxConcurrent:       a.maxConcurrent,
		Verbose:             a.verbose,
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

	return translate.TranslateAllProperties(ctx, tasks, opts)
}

// ---------------------------------------------------------------------------
// Flutter ARB helpers
// ---------------------------------------------------------------------------

// showConfigFlutterStats shows translation stats for a Flutter ARB target.
func showConfigFlutterStats(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	keyVal(T("Translations"), transDir)

	srcLang := rt.Target.SourceLang
	srcPath := filepath.Join(transDir, "app_"+srcLang+".arb")
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
		filePath := filepath.Join(transDir, "app_"+lang+".arb")
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

// runInitFlutter creates or syncs Flutter ARB translation files from the source file.
func runInitFlutter(rt config.ResolvedTarget, langs []string) {
	transDir := rt.AbsTranslationsDir()
	srcLang := rt.Target.SourceLang
	srcPath := filepath.Join(transDir, "app_"+srcLang+".arb")

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
		targetPath := filepath.Join(transDir, "app_"+lang+".arb")

		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			newFile := arbfile.NewTranslationFile(srcFile, lang)
			if err := os.MkdirAll(transDir, 0755); err != nil {
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

// translateFlutterTarget translates Flutter ARB files for a single target.
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

	srcLang := rt.Target.SourceLang
	srcPath := filepath.Join(transDir, "app_"+srcLang+".arb")
	srcFile, err := arbfile.ParseFile(srcPath)
	if err != nil {
		return fmt.Errorf(T("cannot read source ARB file %s: %w"), srcPath, err)
	}
	srcTotal, _, _ := srcFile.Stats()
	logInfo(T("Source strings: %d"), srcTotal)

	if a.dryRun {
		for _, lang := range langs {
			langName := lang
			if meta, ok := i18next.LangMeta[lang]; ok {
				langName = meta.Name
			}
			targetPath := filepath.Join(transDir, "app_"+lang+".arb")
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
		langName := lang
		if meta, ok := i18next.LangMeta[lang]; ok {
			langName = meta.Name
		}
		targetPath := filepath.Join(transDir, "app_"+lang+".arb")

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

		tasks = append(tasks, translate.ARBLangTask{
			Lang:       lang,
			LangName:   langName,
			FilePath:   targetPath,
			File:       targetFile,
			SourceFile: srcFile,
		})
	}

	if len(tasks) == 0 {
		logInfo(T("No ARB files to translate"))
		return nil
	}

	parallelMode := translate.ParallelSequential
	if a.parallel {
		parallelMode = translate.ParallelFullParallel
	}

	opts := translate.Options{
		Provider:            prov,
		SystemPrompt:        rt.Target.Prompt,
		RetranslateExisting: a.retranslate,
		ChunkSize:           a.chunkSize,
		RequestDelay:        a.requestDelay,
		Timeout:             a.timeout,
		MaxRetries:          a.maxRetries,
		ParallelMode:        parallelMode,
		MaxConcurrent:       a.maxConcurrent,
		Verbose:             a.verbose,
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

	return translate.TranslateAllARB(ctx, tasks, opts)
}
