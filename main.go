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

	"github.com/minios-linux/lokit/config"
	"github.com/minios-linux/lokit/copilot"
	"github.com/minios-linux/lokit/credentials"
	"github.com/minios-linux/lokit/extract"
	"github.com/minios-linux/lokit/gemini"
	"github.com/minios-linux/lokit/i18next"
	"github.com/minios-linux/lokit/merge"
	"github.com/minios-linux/lokit/po"
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
	colorYellow = "\033[1;33m"
	colorBlue   = "\033[0;34m"
)

func logInfo(format string, args ...any) {
	fmt.Fprintf(os.Stderr, colorBlue+"[INFO]"+colorReset+" "+format+"\n", args...)
}

func logSuccess(format string, args ...any) {
	fmt.Fprintf(os.Stderr, colorGreen+"[OK]"+colorReset+" "+format+"\n", args...)
}

func logWarning(format string, args ...any) {
	fmt.Fprintf(os.Stderr, colorYellow+"[WARN]"+colorReset+" "+format+"\n", args...)
}

func logError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, colorRed+"[ERROR]"+colorReset+" "+format+"\n", args...)
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
		Short: "Localization Kit: gettext PO file manager with AI translation",
		Long: `lokit — Localization Kit: gettext PO file manager with AI translation.

Supports flat (po/*.po), nested (po/lang/*.po), and po4a project structures
with auto-detection. Translates using multiple AI providers including native
GitHub Copilot OAuth integration.

Commands:
  status      Show project info and translation statistics
  init        Prepare translation files (extract strings, create/update PO)
  translate   Translate PO files using AI (auto-inits if needed)
  auth        Manage provider authentication

AI Providers:
  google         Google AI (Gemini) — API key
  gemini         Gemini Code Assist — browser OAuth (free)
  groq           Groq — API key required
  opencode       OpenCode (multi-format dispatcher)
  copilot        GitHub Copilot (native OAuth, free)
  ollama         Ollama local server
  custom-openai  Custom OpenAI-compatible endpoint`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global persistent flag — inherited by all subcommands
	root.PersistentFlags().StringVar(&rootDir, "root", ".", "Project root directory")

	root.AddCommand(
		newStatusCmd(),
		newInitCmd(),
		newTranslateCmd(),
		newAuthCmd(),
		newVersionCmd(),
	)

	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		logError("%v", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// version (display version information)
// ---------------------------------------------------------------------------

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Long:  `Display version, commit hash, and build date.`,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("lokit version %s\n", version)
			fmt.Printf("  commit:    %s\n", commit)
			fmt.Printf("  built:     %s\n", date)
		},
	}

	return cmd
}

// ---------------------------------------------------------------------------
// status (read-only: project info + translation stats)
// ---------------------------------------------------------------------------

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show project info and translation statistics",
		Long: `Show auto-detected project structure and translation statistics.

Displays project type (code/docs/po4a), PO file structure, detected languages,
and per-language translation progress. Does not modify any files.`,
		Run: func(cmd *cobra.Command, args []string) {
			runStatus()
		},
	}

	return cmd
}

func runStatus() {
	proj := config.Detect(rootDir)

	// Project info header
	fmt.Fprintf(os.Stderr, "\n%sProject%s\n", colorBlue, colorReset)
	fmt.Fprintln(os.Stderr, strings.Repeat("─", 60))

	fmt.Fprintf(os.Stderr, "  Name:       %s\n", proj.Name)
	fmt.Fprintf(os.Stderr, "  Version:    %s\n", proj.Version)

	absRoot, _ := filepath.Abs(rootDir)
	fmt.Fprintf(os.Stderr, "  Root:       %s\n", absRoot)

	typeDesc := "Unknown"
	switch proj.Type {
	case config.ProjectTypeCode:
		typeDesc = "Source code (gettext)"
	case config.ProjectTypeDocs:
		typeDesc = "Documentation (po4a)"
	case config.ProjectTypeMixed:
		typeDesc = "Mixed (code + docs)"
	case config.ProjectTypeI18Next:
		typeDesc = "i18next JSON translations"
	}
	fmt.Fprintf(os.Stderr, "  Type:       %s\n", typeDesc)

	if proj.Type == config.ProjectTypeI18Next {
		fmt.Fprintf(os.Stderr, "  Trans dir:  %s\n", proj.I18NextDir)
		if proj.RecipeTransDir != "" {
			fmt.Fprintf(os.Stderr, "  Recipes:    %s\n", proj.RecipeTransDir)
		}
		if proj.BlogPostsDir != "" {
			fmt.Fprintf(os.Stderr, "  Blog posts: %s\n", proj.BlogPostsDir)
		}
	} else {
		structDesc := "Unknown"
		switch proj.POStructure {
		case config.POStructureFlat:
			structDesc = "Flat (po/*.po)"
		case config.POStructureNested:
			structDesc = "Nested (po/lang/*.po)"
		case config.POStructurePo4a:
			structDesc = "po4a (documentation)"
		}
		fmt.Fprintf(os.Stderr, "  Structure:  %s\n", structDesc)
		fmt.Fprintf(os.Stderr, "  PO dir:     %s\n", proj.PODir)

		if proj.Po4aConfig != "" {
			fmt.Fprintf(os.Stderr, "  po4a cfg:   %s\n", proj.Po4aConfig)
		}
		if proj.ManpagesDir != "" {
			fmt.Fprintf(os.Stderr, "  Manpages:   %s\n", proj.ManpagesDir)
		}
		if proj.DocsDir != "" {
			fmt.Fprintf(os.Stderr, "  Docs:       %s\n", proj.DocsDir)
		}
	}

	if len(proj.SourceDirs) > 0 {
		fmt.Fprintf(os.Stderr, "  Sources:    %s\n", strings.Join(proj.SourceDirs, ", "))
	}

	fmt.Fprintln(os.Stderr)

	// Languages
	if len(proj.Languages) > 0 {
		fmt.Fprintf(os.Stderr, "  Languages:  %s\n", strings.Join(proj.Languages, ", "))
	} else {
		fmt.Fprintf(os.Stderr, "  Languages:  none detected (will use defaults)\n")
	}

	fmt.Fprintln(os.Stderr)

	// i18next projects have their own stats path
	if proj.Type == config.ProjectTypeI18Next {
		showI18NextStats(proj)
		printSuggestedCommands(proj)
		return
	}

	// Translation statistics
	potPath := proj.POTPathResolved()
	if potPath == "" || !fileExists(potPath) {
		if proj.POStructure == config.POStructurePo4a {
			// po4a projects may have PO files without a central POT
			showPo4aStats(proj)
		} else {
			logInfo("No POT template found. Run 'lokit init' to extract strings.")
		}
		printSuggestedCommands(proj)
		return
	}

	showStatsTable(proj, potPath)
	printSuggestedCommands(proj)
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
		logInfo("No translatable strings found in %s", potPath)
		return
	}

	fmt.Fprintf(os.Stderr, "%sTranslation Statistics%s\n", colorBlue, colorReset)
	fmt.Fprintln(os.Stderr, strings.Repeat("─", 60))
	fmt.Fprintf(os.Stderr, "\n%-10s %-12s %-10s %-10s %-8s\n", "Lang", "Translated", "Fuzzy", "Untrans.", "Percent")
	fmt.Fprintln(os.Stderr, strings.Repeat("─", 52))

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
			fmt.Fprintf(os.Stderr, "%-10s %-12s %-10s %-10s %-8s\n", lang, "missing", "-", "-", "-")
			continue
		}

		_, translated, fuzzy, untranslated := poFile.Stats()
		percent := 0
		if potTotal > 0 {
			percent = translated * 100 / potTotal
		}

		fmt.Fprintf(os.Stderr, "%-10s %-12d %-10d %-10d %d%%\n", lang, translated, fuzzy, untranslated, percent)

		if untranslated > 0 || fuzzy > 0 {
			issues = append(issues, langIssue{lang, untranslated, fuzzy})
		}
	}

	fmt.Fprintln(os.Stderr, strings.Repeat("─", 52))
	fmt.Fprintf(os.Stderr, "Total strings: %d\n", potTotal)

	if len(issues) > 0 {
		fmt.Fprintln(os.Stderr)
		logInfo("Translation gaps:")
		for _, issue := range issues {
			parts := []string{}
			if issue.untranslated > 0 {
				parts = append(parts, fmt.Sprintf("%d untranslated", issue.untranslated))
			}
			if issue.fuzzy > 0 {
				parts = append(parts, fmt.Sprintf("%d fuzzy", issue.fuzzy))
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

	fmt.Fprintf(os.Stderr, "%sTranslation Statistics%s\n", colorBlue, colorReset)
	fmt.Fprintln(os.Stderr, strings.Repeat("─", 60))

	for _, lang := range proj.Languages {
		poPath := proj.POPath(lang)
		catalog, err := po.ParseFile(poPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: %serror reading%s\n", lang, colorRed, colorReset)
			continue
		}

		total, translated, fuzzy, _ := catalog.Stats()
		percent := 0
		if total > 0 {
			percent = (translated * 100) / total
		}

		statusColor := colorGreen
		if percent < 50 {
			statusColor = colorRed
		} else if percent < 100 {
			statusColor = colorYellow
		}

		fmt.Fprintf(os.Stderr, "  %s%s%s: %d%% (%d/%d translated",
			statusColor, lang, colorReset, percent, translated, total)
		if fuzzy > 0 {
			fmt.Fprintf(os.Stderr, ", %d fuzzy", fuzzy)
		}
		fmt.Fprintf(os.Stderr, ")\n")
	}

	fmt.Fprintln(os.Stderr)
}

func showI18NextStats(proj *config.Project) {
	if len(proj.Languages) == 0 {
		logInfo("No language files detected in %s", proj.I18NextDir)
		return
	}

	fmt.Fprintf(os.Stderr, "%sUI Translation Statistics%s\n", colorBlue, colorReset)
	fmt.Fprintln(os.Stderr, strings.Repeat("─", 60))
	fmt.Fprintf(os.Stderr, "\n%-10s %-12s %-12s %-8s\n", "Lang", "Translated", "Untrans.", "Percent")
	fmt.Fprintln(os.Stderr, strings.Repeat("─", 44))

	// Skip source language
	for _, lang := range proj.Languages {
		if lang == proj.SourceLang {
			continue
		}
		filePath := proj.I18NextPath(lang)
		file, err := i18next.ParseFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%-10s %-12s %-12s %-8s\n", lang, "error", "-", "-")
			continue
		}

		total, translated, untranslated := file.Stats()
		percent := 0
		if total > 0 {
			percent = translated * 100 / total
		}

		langName := ""
		if meta, ok := i18next.LangMeta[lang]; ok {
			langName = " " + meta.Flag
		}
		fmt.Fprintf(os.Stderr, "%-10s %-12d %-12d %d%%%s\n", lang, translated, untranslated, percent, langName)
	}

	fmt.Fprintln(os.Stderr, strings.Repeat("─", 44))

	// Show source language key count
	srcFile, err := i18next.ParseFile(proj.I18NextPath(proj.SourceLang))
	if err == nil {
		fmt.Fprintf(os.Stderr, "Source keys (%s): %d\n", proj.SourceLang, len(srcFile.Translations))
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
	fmt.Fprintf(os.Stderr, "%sRecipe Translation Statistics%s\n", colorBlue, colorReset)
	fmt.Fprintln(os.Stderr, strings.Repeat("─", 60))

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
			fmt.Fprintf(os.Stderr, "  %s: directory not found\n", lang)
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

		fmt.Fprintf(os.Stderr, "  %s: %d/%d fully translated (%d%%)\n", lang, fullyTranslated, total, percent)
	}
}

func showBlogTransStats(proj *config.Project) {
	slugs, err := i18next.BlogPostSlugs(proj.BlogPostsDir)
	if err != nil || len(slugs) == 0 {
		return
	}

	fmt.Fprintf(os.Stderr, "%sBlog Post Translation Statistics%s\n", colorBlue, colorReset)
	fmt.Fprintln(os.Stderr, strings.Repeat("─", 60))
	fmt.Fprintf(os.Stderr, "  Source posts: %d\n", len(slugs))

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
		fmt.Fprintf(os.Stderr, "  %s: %d/%d posts translated\n", lang, translated, len(slugs))
	}
}

func printSuggestedCommands(proj *config.Project) {
	fmt.Fprintf(os.Stderr, "%sSuggested Commands%s\n", colorBlue, colorReset)
	fmt.Fprintln(os.Stderr, strings.Repeat("─", 60))
	fmt.Fprintln(os.Stderr)

	switch proj.Type {
	case config.ProjectTypeCode:
		fmt.Fprintf(os.Stderr, "  # Prepare translation files (extract + create/update PO)\n")
		fmt.Fprintf(os.Stderr, "  lokit init\n\n")
		fmt.Fprintf(os.Stderr, "  # Translate using AI (auto-inits if needed)\n")
		fmt.Fprintf(os.Stderr, "  lokit translate --provider copilot --model gpt-4o\n\n")
		fmt.Fprintf(os.Stderr, "  # Translate specific language\n")
		fmt.Fprintf(os.Stderr, "  lokit translate --provider copilot --model gpt-4o --lang ru\n\n")

	case config.ProjectTypeDocs:
		if proj.POStructure == config.POStructurePo4a {
			fmt.Fprintf(os.Stderr, "  # Update POT and PO files (po4a)\n")
			fmt.Fprintf(os.Stderr, "  lokit init\n\n")
		}
		fmt.Fprintf(os.Stderr, "  # Translate using AI\n")
		fmt.Fprintf(os.Stderr, "  lokit translate --provider copilot --model gpt-4o\n\n")
		fmt.Fprintf(os.Stderr, "  # Translate specific language\n")
		fmt.Fprintf(os.Stderr, "  lokit translate --provider copilot --model gpt-4o --lang ru\n\n")

	case config.ProjectTypeMixed:
		fmt.Fprintf(os.Stderr, "  # Prepare translation files\n")
		fmt.Fprintf(os.Stderr, "  lokit init\n\n")
		fmt.Fprintf(os.Stderr, "  # Translate all languages\n")
		fmt.Fprintf(os.Stderr, "  lokit translate --provider copilot --model gpt-4o\n\n")

	case config.ProjectTypeI18Next:
		fmt.Fprintf(os.Stderr, "  # Translate UI strings using AI\n")
		fmt.Fprintf(os.Stderr, "  lokit translate --provider copilot --model gpt-4o\n\n")
		fmt.Fprintf(os.Stderr, "  # Translate specific language\n")
		fmt.Fprintf(os.Stderr, "  lokit translate --provider copilot --model gpt-4o --lang ru\n\n")
		if proj.RecipeTransDir != "" {
			fmt.Fprintf(os.Stderr, "  # Translate with recipes\n")
			fmt.Fprintf(os.Stderr, "  lokit translate --provider copilot --model gpt-4o --recipes\n\n")
		}
		if proj.BlogPostsDir != "" {
			fmt.Fprintf(os.Stderr, "  # Translate blog posts\n")
			fmt.Fprintf(os.Stderr, "  lokit translate --provider copilot --model gpt-4o --blog\n\n")
		}

	default:
		fmt.Fprintf(os.Stderr, "  # No translatable content detected.\n")
		fmt.Fprintf(os.Stderr, "  # Make sure your project has:\n")
		fmt.Fprintf(os.Stderr, "  #   - Source files with _(), N_() calls, or\n")
		fmt.Fprintf(os.Stderr, "  #   - po/ directory with .po files, or\n")
		fmt.Fprintf(os.Stderr, "  #   - po4a.cfg file\n\n")
	}
}

// ---------------------------------------------------------------------------
// init (extract + create/update PO files)
// ---------------------------------------------------------------------------

func newInitCmd() *cobra.Command {
	var (
		srcDirs string
		potFile string
		poDir   string
		langs   string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Prepare translation files (extract strings, create/update PO)",
		Long: `Extract translatable strings and create/update PO files.

For code projects: runs xgettext to extract strings into a POT template,
then creates or updates PO files for each language.

For po4a projects: runs 'po4a --no-translations' to update templates.

This command is idempotent — safe to run multiple times. Existing
translations are preserved when updating PO files.`,
		Run: func(cmd *cobra.Command, args []string) {
			proj := config.Detect(rootDir)

			if potFile != "" {
				proj.POTFile = potFile
			}
			if poDir != "" {
				proj.PODir = poDir
			}
			if srcDirs != "" {
				proj.SourceDirs = strings.Split(srcDirs, ",")
			}
			if langs != "" {
				proj.Languages = strings.Split(langs, ",")
			}

			runInit(proj)
		},
	}

	// Low-level override flags (hidden — auto-detection handles these)
	cmd.Flags().StringVar(&srcDirs, "src", "", "Source directories to scan (comma-separated)")
	cmd.Flags().StringVar(&potFile, "pot", "", "Output .pot file path")
	cmd.Flags().StringVar(&poDir, "po-dir", "", "Directory with .po files")
	cmd.Flags().StringVar(&langs, "lang", "", "Languages (comma-separated)")

	_ = cmd.Flags().MarkHidden("src")
	_ = cmd.Flags().MarkHidden("pot")
	_ = cmd.Flags().MarkHidden("po-dir")

	return cmd
}

func runInit(proj *config.Project) {
	logInfo("Initializing translations for %s (v%s)...", proj.Name, proj.Version)

	if proj.Type == config.ProjectTypeI18Next {
		runInitI18Next(proj)
		return
	}

	if proj.POStructure == config.POStructurePo4a && proj.Po4aConfig != "" {
		runInitPo4a(proj)
		return
	}

	runInitCode(proj)
}

func runInitI18Next(proj *config.Project) {
	srcPath := proj.I18NextPath(proj.SourceLang)
	srcFile, err := i18next.ParseFile(srcPath)
	if err != nil {
		logError("Cannot read source language file %s: %v", srcPath, err)
		os.Exit(1)
	}

	srcKeys := srcFile.Keys()
	logInfo("Source language (%s): %d keys", proj.SourceLang, len(srcKeys))

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
				logError("Creating %s: %v", filePath, err)
				continue
			}
			logSuccess("Created: %s (%d keys)", filePath, len(srcKeys))
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
				logError("Updating %s: %v", filePath, err)
				continue
			}
			logSuccess("Updated: %s (+%d new keys)", filePath, added)
			updated++
		} else {
			logInfo("%s: up to date", lang)
		}
	}

	logInfo("Summary: %d created, %d updated", created, updated)
	fmt.Fprintln(os.Stderr)
	showI18NextStats(proj)
	logSuccess("Init complete!")
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
		logInfo("pandoc not found, skipping manpage generation from markdown")
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
		return fmt.Errorf("failed to list markdown files: %w", err)
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
		logInfo("Generating %s from %s", manFile, mdFile)
		cmd := exec.Command("pandoc", "-s", "-t", "man", mdPath, "-o", manPath)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("pandoc failed for %s: %w", mdFile, err)
		}

		// Post-process: remove \c sequences that cause po4a issues
		if err := removeBackslashCSequences(manPath); err != nil {
			logWarning("Failed to post-process %s: %v", manFile, err)
		}

		generated++
	}

	if generated > 0 {
		logSuccess("Generated %d manpage(s) from markdown", generated)
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

func runInitPo4a(proj *config.Project) {
	if _, err := exec.LookPath("po4a"); err != nil {
		logError("po4a is not installed. Install with: sudo apt install po4a")
		os.Exit(1)
	}

	// Check if manpages need to be generated from markdown
	if err := generateManpagesFromMarkdown(proj); err != nil {
		logWarning("Failed to generate manpages from markdown: %v", err)
		logWarning("Continuing anyway, po4a might fail if source files don't exist")
	}

	logInfo("Running po4a --no-translations %s ...", proj.Po4aConfig)
	cmd := exec.Command("po4a", "--no-translations", proj.Po4aConfig)
	cmd.Dir = filepath.Dir(proj.Po4aConfig)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		logError("po4a failed: %v", err)
		os.Exit(1)
	}
	logSuccess("po4a updated POT and PO files")

	// Re-detect after po4a ran
	redetected := config.Detect(filepath.Dir(filepath.Dir(proj.Po4aConfig)))
	if len(redetected.Languages) > 0 {
		proj.Languages = redetected.Languages
	}
	proj.POTFile = redetected.POTPathResolved()

	fmt.Fprintln(os.Stderr)
	potPath := proj.POTPathResolved()
	if potPath != "" && fileExists(potPath) {
		showStatsTable(proj, potPath)
	} else {
		showPo4aStats(proj)
	}

	logSuccess("Init complete!")
}

func runInitCode(proj *config.Project) {
	// Step 1: Extract strings using xgettext
	if err := doExtract(proj); err != nil {
		logError("%v", err)
		os.Exit(1)
	}

	// Step 2: Update PO files from POT
	potPO, err := po.ParseFile(proj.POTFile)
	if err != nil {
		logError("Reading %s: %v", proj.POTFile, err)
		os.Exit(1)
	}

	logInfo("Updating PO files for: %s", strings.Join(proj.Languages, ", "))

	created, updated := 0, 0

	for _, lang := range proj.Languages {
		poPath := proj.POPath(lang)

		if err := os.MkdirAll(filepath.Dir(poPath), 0755); err != nil {
			logError("Creating directory for %s: %v", poPath, err)
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
				logError("Creating %s: %v", poPath, err)
				continue
			}
			logSuccess("Created: %s", poPath)
			created++
		} else {
			existingPO, err := po.ParseFile(poPath)
			if err != nil {
				logError("Reading %s: %v", poPath, err)
				continue
			}

			merged := merge.Merge(existingPO, potPO)
			if err := merged.WriteFile(poPath); err != nil {
				logError("Writing %s: %v", poPath, err)
				continue
			}
			logSuccess("Updated: %s", poPath)
			updated++
		}
	}

	logInfo("Summary: %d created, %d updated", created, updated)

	// Show stats
	fmt.Fprintln(os.Stderr)
	showStatsTable(proj, proj.POTFile)

	logSuccess("Init complete!")
}

// ---------------------------------------------------------------------------
// translate
// ---------------------------------------------------------------------------

func newTranslateCmd() *cobra.Command {
	var (
		// Target selection
		poDir string
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
		recipes     bool
		blog        bool

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
		Short: "Translate PO files using AI",
		Long: `Translate PO files using AI providers.

Automatically initializes the project if needed (extracts strings, creates
PO files). Requires --provider and --model flags.

Examples:
  # Translate using GitHub Copilot (free)
  lokit translate --provider copilot --model gpt-4o

  # Translate using Gemini Code Assist (free, OAuth)
  lokit translate --provider gemini --model gemini-2.5-flash

  # Translate using Google AI (API key)
  lokit translate --provider google --model gemini-2.5-flash

  # Translate specific languages in parallel
  lokit translate --provider copilot --model gpt-4o --lang ru,de --parallel

  # Dry run (show what would be translated)
  lokit translate --provider copilot --model gpt-4o --dry-run`,
		Run: func(cmd *cobra.Command, args []string) {
			runTranslate(translateArgs{
				poDir: poDir, langs: langs,
				provider: provider, apiKey: apiKey, model: model,
				baseURL:   baseURL,
				chunkSize: chunkSize, retranslate: retranslate,
				fuzzy: fuzzy, prompt: prompt, verbose: verbose,
				dryRun: dryRun, parallel: parallel, recipes: recipes,
				blog:          blog,
				maxConcurrent: maxConcurrent, requestDelay: requestDelay,
				timeout: timeout, proxy: proxy, maxRetries: maxRetries,
			})
		},
	}

	// Provider selection
	cmd.Flags().StringVar(&provider, "provider", "", "AI provider (required): google, gemini, groq, opencode, copilot, ollama, custom-openai")
	cmd.Flags().StringVar(&model, "model", "", "Model name (required)")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key (or LOKIT_API_KEY env var)")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "Custom API base URL")

	// Target selection
	cmd.Flags().StringVar(&langs, "lang", "", "Languages to translate (comma-separated, default: all with untranslated)")

	// Translation behavior
	cmd.Flags().IntVar(&chunkSize, "chunk-size", 0, "Entries per API request (0 = all at once)")
	cmd.Flags().BoolVar(&retranslate, "retranslate", false, "Re-translate already translated entries")
	cmd.Flags().BoolVar(&fuzzy, "fuzzy", true, "Translate fuzzy entries and clear fuzzy flag")
	cmd.Flags().StringVar(&prompt, "prompt", "", "Custom system prompt (use {{targetLang}} placeholder)")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Enable detailed logging")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be translated without calling AI")
	cmd.Flags().BoolVar(&recipes, "recipes", false, "Also translate per-recipe JSON files (i18next projects)")
	cmd.Flags().BoolVar(&blog, "blog", false, "Also translate blog post Markdown files (i18next projects)")

	// Parallelization
	cmd.Flags().BoolVar(&parallel, "parallel", false, "Enable parallel translation")
	cmd.Flags().IntVar(&maxConcurrent, "max-concurrent", 3, "Maximum concurrent tasks (with --parallel)")
	cmd.Flags().DurationVar(&requestDelay, "request-delay", 0, "Delay between parallel tasks")

	// Network
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Request timeout (0 = provider default)")
	cmd.Flags().StringVar(&proxy, "proxy", "", "HTTP/HTTPS proxy URL")
	cmd.Flags().IntVar(&maxRetries, "max-retries", 3, "Maximum retries on rate limit (429)")

	// Hidden overrides
	cmd.Flags().StringVar(&poDir, "po-dir", "", "Directory with .po files")
	_ = cmd.Flags().MarkHidden("po-dir")

	// Provider completion
	_ = cmd.RegisterFlagCompletionFunc("provider", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{
			"google\tGoogle AI (Gemini) — API key required",
			"gemini\tGemini Code Assist — browser OAuth (free)",
			"groq\tGroq — API key required",
			"opencode\tOpenCode — optional API key",
			"copilot\tGitHub Copilot — native OAuth (free)",
			"ollama\tOllama local server",
			"custom-openai\tCustom OpenAI-compatible endpoint",
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
	poDir, langs                     string
	provider, apiKey, model, baseURL string
	chunkSize                        int
	retranslate, fuzzy               bool
	prompt                           string
	verbose, dryRun, parallel        bool
	recipes                          bool
	blog                             bool
	maxConcurrent                    int
	requestDelay, timeout            time.Duration
	proxy                            string
	maxRetries                       int
}

func runTranslate(a translateArgs) {
	proj := config.Detect(rootDir)

	if a.poDir != "" {
		proj.PODir = a.poDir
	}

	// Resolve API key from flag, environment, or credentials store
	key := a.apiKey
	if key == "" {
		key = os.Getenv("LOKIT_API_KEY")
	}
	if key == "" {
		// Check credentials store for the specific provider
		key = credentials.GetAPIKey(a.provider)
	}

	// Check that provider is specified
	if a.provider == "" {
		logError("No provider specified. Use --provider to choose an AI translation service.\n\n" +
			"Available providers:\n" +
			"  Cloud APIs (require API key):\n" +
			"    google         Google AI (Gemini)\n" +
			"    groq           Groq\n" +
			"    opencode       OpenCode\n\n" +
			"  Cloud (free, requires GitHub account):\n" +
			"    copilot        GitHub Copilot (native OAuth)\n\n" +
			"  Local services (no API key):\n" +
			"    ollama         Ollama local server\n\n" +
			"  Custom:\n" +
			"    custom-openai  Custom OpenAI-compatible endpoint\n\n" +
			"Example: lokit translate --provider copilot --model gpt-4o")
		os.Exit(1)
	}

	// Resolve provider configuration
	prov := resolveProvider(a.provider, a.baseURL, key, a.model, a.proxy, a.timeout)

	// Validate provider requirements
	if err := validateProvider(prov, key); err != nil {
		logError("%v", err)
		os.Exit(1)
	}

	// Branch: i18next projects use a completely different code path
	if proj.Type == config.ProjectTypeI18Next {
		runTranslateI18Next(proj, prov, a)
		return
	}

	// Auto-init if POT template is missing (code projects or unknown type)
	potPath := proj.POTPathResolved()
	if (potPath == "" || !fileExists(potPath)) && proj.POStructure != config.POStructurePo4a {
		logInfo("No POT template found, running init...")
		if err := doExtract(proj); err != nil {
			logError("Auto-extraction failed: %v", err)
			logInfo("If this project uses po4a, run 'lokit init' from the correct directory")
			os.Exit(1)
		}
		// Re-detect project after extraction (POT now exists, po/ may have been created)
		proj = config.Detect(rootDir)
		if a.poDir != "" {
			proj.PODir = a.poDir
		}
	}

	// Determine which languages to translate
	var targetLangs []string
	if a.langs != "" {
		targetLangs = strings.Split(a.langs, ",")
	} else {
		if len(proj.Languages) == 0 {
			logError("No languages detected. Specify languages with --langs, e.g.:")
			fmt.Fprintf(os.Stderr, "  lokit translate --langs ru,de,fr --provider %s --model %s\n", a.provider, a.model)
			os.Exit(1)
		}
		for _, lang := range proj.Languages {
			poPath := proj.POPath(lang)
			poFile, err := po.ParseFile(poPath)
			if err != nil {
				// PO file doesn't exist or is unreadable — it needs translation
				targetLangs = append(targetLangs, lang)
				continue
			}
			untranslated := poFile.UntranslatedEntries()
			fuzzyEntries := poFile.FuzzyEntries()
			if len(untranslated) > 0 || (a.fuzzy && len(fuzzyEntries) > 0) || a.retranslate {
				targetLangs = append(targetLangs, lang)
			}
		}
	}

	if len(targetLangs) == 0 {
		logSuccess("All translations are complete!")
		return
	}

	// Determine parallel mode string for translate package
	parallelMode := translate.ParallelSequential
	if a.parallel {
		parallelMode = translate.ParallelFullParallel
	}

	logInfo("Provider: %s (%s), Model: %s", prov.Name, prov.ID, prov.Model)
	if a.parallel {
		logInfo("Parallel: enabled, max concurrent: %d", a.maxConcurrent)
	} else {
		logInfo("Parallel: disabled (sequential)")
	}
	if a.chunkSize > 0 {
		logInfo("Chunk size: %d", a.chunkSize)
	} else {
		logInfo("Chunk size: all at once")
	}
	logInfo("Translating: %s", strings.Join(targetLangs, ", "))

	if a.dryRun {
		for _, lang := range targetLangs {
			poPath := proj.POPath(lang)
			poFile, err := po.ParseFile(poPath)
			if err != nil {
				if !fileExists(poPath) {
					// Count entries from POT template
					potPath := proj.POTPathResolved()
					if potPO, perr := po.ParseFile(potPath); perr == nil {
						count := 0
						for _, e := range potPO.Entries {
							if e.MsgID != "" && !e.Obsolete {
								count++
							}
						}
						langName := po.LangNameNative(lang)
						logInfo("%s (%s): %d strings to translate (PO file will be auto-created)", lang, langName, count)
					} else {
						logError("%s: PO file missing and no POT template found", lang)
					}
				} else {
					logError("Reading %s: %v", poPath, err)
				}
				continue
			}
			untranslated := poFile.UntranslatedEntries()
			fuzzyEntries := poFile.FuzzyEntries()
			count := len(untranslated)
			if a.fuzzy {
				count += len(fuzzyEntries)
			}
			langName := po.LangNameNative(lang)
			logInfo("%s (%s): %d strings to translate", lang, langName, count)
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
		logWarning("Interrupted, saving progress...")
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
		TranslateFuzzy:      a.fuzzy,
		SystemPrompt:        a.prompt,
		Verbose:             a.verbose,
		OnProgress: func(lang string, done, total int) {
			logInfo("  %s: %d/%d", lang, done, total)
		},
		OnLog: func(format string, args ...any) {
			logInfo(format, args...)
		},
		OnError: func(format string, args ...any) {
			logError(format, args...)
		},
	}

	// Auto-select documentation prompt for po4a/docs projects
	if opts.SystemPrompt == "" && (proj.Type == config.ProjectTypeDocs || proj.POStructure == config.POStructurePo4a) {
		opts.SystemPrompt = translate.DocsSystemPrompt
		logInfo("Using documentation-specific translation prompt (groff/man markup preservation)")
	}

	// Load PO files for all target languages, auto-creating from POT if missing
	var langTasks []translate.LangTask
	for _, lang := range targetLangs {
		poPath := proj.POPath(lang)
		poFile, err := po.ParseFile(poPath)
		if err != nil {
			// Auto-create PO file from POT template if it doesn't exist
			if !fileExists(poPath) {
				poFile = createPOFromPOT(proj, lang, poPath)
				if poFile == nil {
					continue
				}
			} else {
				logError("Reading %s: %v", poPath, err)
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
		logError("No valid PO files to translate")
		os.Exit(1)
	}

	// Run translation
	err := translate.TranslateAll(ctx, langTasks, opts)
	if err != nil {
		if ctx.Err() != nil {
			logWarning("Translation interrupted, partial progress saved")
			os.Exit(0)
		}
		logError("Translation failed: %v", err)
		os.Exit(1)
	}

	logSuccess("Translation complete!")
}

// ---------------------------------------------------------------------------
// i18next translate
// ---------------------------------------------------------------------------

func runTranslateI18Next(proj *config.Project, prov translate.Provider, a translateArgs) {
	// Load source language file for key reference
	srcPath := proj.I18NextPath(proj.SourceLang)
	srcFile, err := i18next.ParseFile(srcPath)
	if err != nil {
		logError("Cannot read source language file %s: %v", srcPath, err)
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

	if len(targetLangs) == 0 && !a.recipes && !a.blog {
		logSuccess("All UI translations are complete!")
		return
	}

	// Parallel mode
	parallelMode := translate.ParallelSequential
	if a.parallel {
		parallelMode = translate.ParallelFullParallel
	}

	logInfo("Provider: %s (%s), Model: %s", prov.Name, prov.ID, prov.Model)
	if a.parallel {
		logInfo("Parallel: enabled, max concurrent: %d", a.maxConcurrent)
	} else {
		logInfo("Parallel: disabled (sequential)")
	}
	logInfo("Source keys (%s): %d", proj.SourceLang, len(srcKeys))

	if len(targetLangs) > 0 {
		logInfo("Translating UI strings: %s", strings.Join(targetLangs, ", "))
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
				logInfo("%s (%s): %d strings to translate (file will be auto-created)", lang, langName, len(srcKeys))
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
			logInfo("%s (%s): %d strings to translate", lang, langName, count)
		}
		if a.recipes && proj.RecipeTransDir != "" {
			for _, lang := range targetLangs {
				langDir := proj.RecipeTransPath(lang)
				if langDir == "" {
					continue
				}
				entries, err := os.ReadDir(langDir)
				if err != nil {
					logInfo("%s: recipe translations directory not found", lang)
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
				logInfo("%s: %d recipe files need translation", lang, untranslated)
			}
		}
		if a.blog && proj.BlogPostsDir != "" {
			slugs, err := i18next.BlogPostSlugs(proj.BlogPostsDir)
			if err != nil || len(slugs) == 0 {
				logInfo("No blog posts found in %s", proj.BlogPostsDir)
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
						logInfo("%s: %d blog post(s) need translation", lang, total)
					} else {
						logInfo("%s: all blog posts translated", lang)
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
		logWarning("Interrupted, saving progress...")
		cancel()
	}()

	// Build translation options
	systemPrompt := a.prompt
	if systemPrompt == "" {
		systemPrompt = translate.I18NextSystemPrompt
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
		Verbose:             a.verbose,
		OnProgress: func(lang string, done, total int) {
			logInfo("  %s: %d/%d", lang, done, total)
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
				logInfo("Auto-creating %s with %d keys", filePath, len(srcKeys))
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
					logWarning("Translation interrupted, partial progress saved")
					os.Exit(0)
				}
				logError("UI translation failed: %v", err)
				hadErrors = true
			}
		}
	}

	// Translate recipes if requested
	if a.recipes && proj.RecipeTransDir != "" {
		logInfo("Translating recipe metadata...")

		recipeOpts := opts
		if a.prompt == "" {
			recipeOpts.SystemPrompt = translate.RecipeSystemPrompt
		}

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
				logError("Cannot read recipe directory for %s: %v", lang, err)
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
				logInfo("  %s: all recipe translations complete", lang)
				continue
			}

			logInfo("  %s (%s): %d recipe files to translate", lang, langName, len(recipeTasks))

			recipeOpts.Language = lang
			recipeOpts.LanguageName = langName

			if err := translate.TranslateRecipes(ctx, recipeTasks, recipeOpts); err != nil {
				if ctx.Err() != nil {
					logWarning("Translation interrupted, partial progress saved")
					os.Exit(0)
				}
				logError("Recipe translation failed for %s: %v", lang, err)
				hadErrors = true
			}
		}
	}

	// Translate blog posts if requested
	if a.blog && proj.BlogPostsDir != "" {
		logInfo("Translating blog posts...")

		blogOpts := opts
		if a.prompt == "" {
			blogOpts.SystemPrompt = translate.BlogPostSystemPrompt
		}

		slugs, err := i18next.BlogPostSlugs(proj.BlogPostsDir)
		if err != nil || len(slugs) == 0 {
			logWarning("No blog posts found in %s", proj.BlogPostsDir)
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
						logError("Cannot read source blog post %s: %v", srcPath, srcErr)
						continue
					}

					// Load or create translation
					transPath := i18next.BlogTranslationPath(proj.BlogPostsDir, slug, lang)
					var transPost *i18next.BlogPost

					if data, err := os.ReadFile(transPath); err == nil {
						// Existing translation
						transPost, err = i18next.ParseBlogPostData(data)
						if err != nil {
							logWarning("Error parsing %s, will recreate: %v", transPath, err)
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
					logInfo("  %s: all blog posts translated", lang)
					continue
				}

				logInfo("  %s (%s): %d blog post(s) to translate", lang, langName, len(blogTasks))

				blogOpts.Language = lang
				blogOpts.LanguageName = langName

				if err := translate.TranslateBlogPosts(ctx, blogTasks, blogOpts); err != nil {
					if ctx.Err() != nil {
						logWarning("Translation interrupted, partial progress saved")
						os.Exit(0)
					}
					logError("Blog post translation failed for %s: %v", lang, err)
					hadErrors = true
				}
			}
		}
	}

	if hadErrors {
		logError("Translation completed with errors")
		os.Exit(1)
	}
	logSuccess("Translation complete!")
}

// ---------------------------------------------------------------------------
// auth (login / logout / list)
// ---------------------------------------------------------------------------

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage provider authentication",
		Long: `Manage authentication credentials for all AI providers.

OAuth providers (interactive browser/device flow):
  copilot       GitHub Copilot (device code flow, free with GitHub account)
  gemini        Google Gemini Code Assist (browser OAuth, free tier: 60 req/min)

API key providers (paste your key):
  google        Google AI Studio (Gemini API key)
  groq          Groq Cloud (free tier available)
  opencode      OpenCode proxy
  custom-openai Custom OpenAI-compatible endpoint

No auth required:
  ollama        Local Ollama server

Examples:
  lokit auth login                         Interactive provider selection
  lokit auth login --provider copilot      OAuth with GitHub Copilot
  lokit auth login --provider google       Store Google AI API key
  lokit auth logout --provider google      Remove Google API key
  lokit auth logout                        Remove all credentials
  lokit auth list                          Show all stored credentials`,
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
		Short: "Authenticate with an AI provider",
		Long: `Authenticate with an AI provider using OAuth or API key.

If --provider is not specified, you will be prompted to choose.

OAuth providers:
  copilot       Device code flow — enter code in browser
  gemini        Browser-based OAuth — sign in with Google

API key providers:
  google        Paste your Google AI Studio API key
  groq          Paste your Groq API key
  opencode      Paste your OpenCode API key
  custom-openai Paste your API key + endpoint URL`,
		Run: func(cmd *cobra.Command, args []string) {
			// If no provider specified, prompt user
			if provider == "" {
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintf(os.Stderr, "%sSelect provider to authenticate:%s\n\n", colorBlue, colorReset)
				for i, p := range allProviders {
					if p.auth == "none" {
						continue // Skip ollama — no auth needed
					}
					authLabel := ""
					switch p.auth {
					case "oauth":
						authLabel = "OAuth"
					case "api-key":
						authLabel = "API key"
					}
					fmt.Fprintf(os.Stderr, "  %d. %s%-13s%s %s (%s)\n",
						i+1, colorYellow, p.id, colorReset, p.desc, authLabel)
				}
				fmt.Fprintln(os.Stderr)
				fmt.Fprintf(os.Stderr, "Enter choice (number or name): ")

				scanner := bufio.NewScanner(os.Stdin)
				if !scanner.Scan() {
					logError("No input received")
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
					logError("Invalid choice. Use: lokit auth login --provider PROVIDER")
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
				logError("Unknown provider '%s'. Run 'lokit auth login' for options.", provider)
				os.Exit(1)
			}
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", "Provider to authenticate")
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
	fmt.Fprintf(os.Stderr, "\n%sGitHub Copilot Authentication%s\n", colorBlue, colorReset)
	fmt.Fprintln(os.Stderr, strings.Repeat("─", 60))
	fmt.Fprintln(os.Stderr)

	_, err := copilot.DeviceCodeFlow(ctx, func(verificationURI, userCode string) {
		fmt.Fprintf(os.Stderr, "  1. Open this URL in your browser:\n")
		fmt.Fprintf(os.Stderr, "     %s%s%s\n\n", colorGreen, verificationURI, colorReset)
		fmt.Fprintf(os.Stderr, "  2. Enter this code:\n")
		fmt.Fprintf(os.Stderr, "     %s%s%s\n\n", colorYellow, userCode, colorReset)
		fmt.Fprintf(os.Stderr, "  Waiting for authorization...\n")
	})
	if err != nil {
		if ctx.Err() != nil {
			logWarning("Authentication cancelled")
			os.Exit(0)
		}
		logError("Authentication failed: %v", err)
		os.Exit(1)
	}

	logSuccess("Copilot authentication successful!")
	fmt.Fprintf(os.Stderr, "\n  You can now use: lokit translate --provider copilot --model gpt-4o\n\n")
}

func authLoginGemini(ctx context.Context) {
	fmt.Fprintf(os.Stderr, "\n%sGoogle Gemini Authentication%s\n", colorBlue, colorReset)
	fmt.Fprintln(os.Stderr, strings.Repeat("─", 60))
	fmt.Fprintln(os.Stderr)

	accessToken, err := gemini.AuthCodeFlow(ctx, func(authURL string) {
		fmt.Fprintf(os.Stderr, "  Opening browser for Google sign-in...\n\n")
		fmt.Fprintf(os.Stderr, "  If the browser doesn't open, visit:\n")
		fmt.Fprintf(os.Stderr, "  %s%s%s\n\n", colorGreen, authURL, colorReset)
		fmt.Fprintf(os.Stderr, "  Waiting for authorization...\n")
	})
	if err != nil {
		if ctx.Err() != nil {
			logWarning("Authentication cancelled")
			os.Exit(0)
		}
		logError("Authentication failed: %v", err)
		os.Exit(1)
	}

	_ = accessToken // Token is saved by AuthCodeFlow; we use the stored Info for setup
	logSuccess("Gemini authentication successful!")

	// Run Code Assist onboarding to get project ID
	fmt.Fprintln(os.Stderr)
	info := gemini.LoadToken()
	if info == nil {
		logWarning("Token was saved but cannot be loaded")
	} else {
		_, err = gemini.SetupUser(ctx, info)
		if errors.Is(err, gemini.ErrProjectIDRequired) {
			// No project ID available — ask user for GCP project ID
			fmt.Fprintln(os.Stderr)
			logWarning("Gemini Code Assist requires a GCP project ID to work in your region.")
			fmt.Fprintf(os.Stderr, "  You can find your project ID at: https://console.cloud.google.com\n")
			fmt.Fprintf(os.Stderr, "  (Create a project if you don't have one, then enable the Gemini API)\n\n")
			fmt.Fprintf(os.Stderr, "  GCP Project ID (or press Enter to skip): ")

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
						logWarning("Code Assist setup failed: %v", err)
						fmt.Fprintf(os.Stderr, "  Project ID saved. You can try again later.\n\n")
					} else {
						logSuccess("Code Assist project configured!")
					}
				} else {
					fmt.Fprintf(os.Stderr, "  Skipped. You can set it later with:\n")
					fmt.Fprintf(os.Stderr, "    lokit auth login --provider gemini\n\n")
				}
			}
		} else if err != nil {
			logWarning("Code Assist setup failed: %v", err)
			fmt.Fprintf(os.Stderr, "  OAuth login succeeded but Code Assist onboarding failed.\n")
			fmt.Fprintf(os.Stderr, "  This will be retried automatically on first translate.\n\n")
		} else {
			logSuccess("Code Assist project configured!")
		}
	}

	fmt.Fprintf(os.Stderr, "\n  You can now use: lokit translate --provider gemini --model gemini-2.5-flash\n")
	fmt.Fprintf(os.Stderr, "  (no API key needed when authenticated via OAuth)\n\n")
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

	fmt.Fprintf(os.Stderr, "\n%s%s — API Key Setup%s\n", colorBlue, info.name, colorReset)
	fmt.Fprintln(os.Stderr, strings.Repeat("─", 60))
	fmt.Fprintln(os.Stderr)

	if info.helpURL != "" {
		fmt.Fprintf(os.Stderr, "  Get your API key from: %s%s%s\n\n", colorGreen, info.helpURL, colorReset)
	}

	// Check if already configured
	existing := credentials.GetAPIKey(providerID)
	if existing != "" {
		fmt.Fprintf(os.Stderr, "  Current key: %s%s%s\n", colorYellow, credentials.MaskKey(existing), colorReset)
		fmt.Fprintf(os.Stderr, "  Enter new key to replace, or press Enter to keep: ")
	} else {
		fmt.Fprintf(os.Stderr, "  Enter API key: ")
	}

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		logError("No input received")
		os.Exit(1)
	}
	key := strings.TrimSpace(scanner.Text())

	if key == "" {
		if existing != "" {
			logInfo("Keeping existing key")
			return
		}
		logError("No API key provided")
		os.Exit(1)
	}

	if err := credentials.SetAPIKey(providerID, key); err != nil {
		logError("Failed to save API key: %v", err)
		os.Exit(1)
	}

	logSuccess("%s API key saved!", info.name)
	fmt.Fprintf(os.Stderr, "\n  You can now use: %s\n\n", info.example)
}

func authLoginCustomOpenAI() {
	fmt.Fprintf(os.Stderr, "\n%sCustom OpenAI-Compatible Endpoint%s\n", colorBlue, colorReset)
	fmt.Fprintln(os.Stderr, strings.Repeat("─", 60))
	fmt.Fprintln(os.Stderr)

	scanner := bufio.NewScanner(os.Stdin)

	// Base URL
	existing := credentials.Get("custom-openai")
	if existing != nil && existing.BaseURL != "" {
		fmt.Fprintf(os.Stderr, "  Current endpoint: %s%s%s\n", colorYellow, existing.BaseURL, colorReset)
		fmt.Fprintf(os.Stderr, "  Enter new endpoint URL, or press Enter to keep: ")
	} else {
		fmt.Fprintf(os.Stderr, "  Enter endpoint URL (e.g., https://api.example.com/v1): ")
	}

	if !scanner.Scan() {
		logError("No input received")
		os.Exit(1)
	}
	baseURL := strings.TrimSpace(scanner.Text())

	if baseURL == "" && existing != nil && existing.BaseURL != "" {
		baseURL = existing.BaseURL
	}
	if baseURL == "" {
		logError("Endpoint URL is required")
		os.Exit(1)
	}

	// API key (optional for some endpoints)
	if existing != nil && existing.Key != "" {
		fmt.Fprintf(os.Stderr, "  Current key: %s%s%s\n", colorYellow, credentials.MaskKey(existing.Key), colorReset)
		fmt.Fprintf(os.Stderr, "  Enter new API key, or press Enter to keep (leave empty for none): ")
	} else {
		fmt.Fprintf(os.Stderr, "  Enter API key (or press Enter if not required): ")
	}

	if !scanner.Scan() {
		logError("No input received")
		os.Exit(1)
	}
	apiKey := strings.TrimSpace(scanner.Text())

	if apiKey == "" && existing != nil {
		apiKey = existing.Key
	}

	if err := credentials.SetAPIKeyWithBaseURL("custom-openai", apiKey, baseURL); err != nil {
		logError("Failed to save credentials: %v", err)
		os.Exit(1)
	}

	logSuccess("Custom OpenAI endpoint saved!")
	fmt.Fprintf(os.Stderr, "\n  You can now use: lokit translate --provider custom-openai --model MODEL_NAME\n\n")
}

func newAuthLogoutCmd() *cobra.Command {
	var provider string

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials",
		Long: `Remove stored credentials for one or all providers.

If --provider is not specified, credentials for ALL providers are removed.

Examples:
  lokit auth logout                        Remove all credentials
  lokit auth logout --provider copilot     Remove only Copilot OAuth
  lokit auth logout --provider google      Remove only Google API key
  lokit auth logout --provider gemini      Remove only Gemini OAuth`,
		Run: func(cmd *cobra.Command, args []string) {
			if provider != "" {
				// Remove specific provider
				switch provider {
				case "copilot":
					if err := copilot.DeleteToken(); err != nil {
						logError("Failed to remove Copilot credentials: %v", err)
						os.Exit(1)
					}
					logSuccess("Copilot credentials removed")
				case "gemini":
					if err := gemini.DeleteToken(); err != nil {
						logError("Failed to remove Gemini credentials: %v", err)
						os.Exit(1)
					}
					logSuccess("Gemini credentials removed")
				case "google", "groq", "opencode", "custom-openai":
					if err := credentials.Remove(provider); err != nil {
						logError("Failed to remove %s credentials: %v", provider, err)
						os.Exit(1)
					}
					logSuccess("%s credentials removed", provider)
				default:
					logError("Unknown provider '%s'. Run 'lokit auth list' to see providers.", provider)
					os.Exit(1)
				}
				return
			}

			// Remove all
			errCount := 0
			if err := copilot.DeleteToken(); err != nil {
				logError("Failed to remove Copilot credentials: %v", err)
				errCount++
			}
			if err := gemini.DeleteToken(); err != nil {
				logError("Failed to remove Gemini credentials: %v", err)
				errCount++
			}
			for _, pid := range []string{"google", "groq", "opencode", "custom-openai"} {
				if err := credentials.Remove(pid); err != nil {
					logError("Failed to remove %s credentials: %v", pid, err)
					errCount++
				}
			}
			if errCount == 0 {
				logSuccess("All stored credentials removed")
			}
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", "Provider to logout (default: all)")
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
		Short:   "Show stored credentials and status",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(os.Stderr, "\n%sStored Credentials%s\n", colorBlue, colorReset)
			fmt.Fprintln(os.Stderr, strings.Repeat("─", 60))

			// OAuth providers
			fmt.Fprintf(os.Stderr, "\n  %sOAuth Providers%s\n", colorYellow, colorReset)
			fmt.Fprintf(os.Stderr, "  %-14s %s\n", "copilot", copilot.TokenStatus())
			fmt.Fprintf(os.Stderr, "  %-14s %s\n", "gemini", gemini.TokenStatus())

			// API key providers
			fmt.Fprintf(os.Stderr, "\n  %sAPI Key Providers%s\n", colorYellow, colorReset)
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
				entry := credentials.Get(p.id)
				if entry != nil && entry.Key != "" {
					status := fmt.Sprintf("%sconfigured%s (key: %s)", colorGreen, colorReset, credentials.MaskKey(entry.Key))
					if entry.BaseURL != "" {
						status += fmt.Sprintf("\n  %14s endpoint: %s", "", entry.BaseURL)
					}
					fmt.Fprintf(os.Stderr, "  %-14s %s\n", p.id, status)
				} else if p.id == "custom-openai" && entry != nil && entry.BaseURL != "" {
					// custom-openai may have just a URL, no key
					status := fmt.Sprintf("%sconfigured%s (no key)", colorGreen, colorReset)
					status += fmt.Sprintf("\n  %14s endpoint: %s", "", entry.BaseURL)
					fmt.Fprintf(os.Stderr, "  %-14s %s\n", p.id, status)
				} else {
					fmt.Fprintf(os.Stderr, "  %-14s %snot configured%s\n", p.id, colorRed, colorReset)
				}
			}

			// Environment variables
			fmt.Fprintf(os.Stderr, "\n  %sEnvironment Variables%s\n", colorYellow, colorReset)
			envKey := os.Getenv("LOKIT_API_KEY")
			if envKey != "" {
				fmt.Fprintf(os.Stderr, "  LOKIT_API_KEY: %s%s%s (overrides stored keys)\n", colorGreen, credentials.MaskKey(envKey), colorReset)
			} else {
				fmt.Fprintf(os.Stderr, "  LOKIT_API_KEY: %snot set%s\n", colorRed, colorReset)
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

	logInfo("Scanning for source files in: %s", strings.Join(scanDirs, ", "))

	allFiles, err := extract.FindSources(scanDirs)
	if err != nil {
		return fmt.Errorf("scanning sources: %w", err)
	}

	if len(allFiles) == 0 {
		return fmt.Errorf("no source files found (supported: %s)",
			strings.Join(extract.SupportedExtensionsList(), ", "))
	}

	logInfo("Found %d source files (%s)", len(allFiles), extract.DescribeFiles(allFiles))

	result, err := extract.RunXgettext(allFiles, proj.POTFile, proj.Name, proj.Version, proj.BugsEmail)
	if err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	// Count extracted strings
	potPO, err := po.ParseFile(result.POTFile)
	if err == nil {
		count := 0
		for _, e := range potPO.Entries {
			if e.MsgID != "" && !e.Obsolete {
				count++
			}
		}
		logSuccess("Extracted %d strings to %s", count, result.POTFile)
	} else {
		logSuccess("Extracted strings to %s", result.POTFile)
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
		logInfo("No POT template found, running extraction...")
		if err := doExtract(proj); err != nil {
			logError("Auto-extraction failed: %v", err)
			return nil
		}
		// Re-resolve POT path after extraction
		proj.POTFile = proj.POTPathResolved()
		potPath = proj.POTFile
		if potPath == "" || !fileExists(potPath) {
			logError("Cannot auto-create %s: extraction produced no POT template", poPath)
			logInfo("Check that source files contain translatable strings (_(), N_(), etc.)")
			return nil
		}
	}

	potPO, err := po.ParseFile(potPath)
	if err != nil {
		logError("Cannot read POT template %s: %v", potPath, err)
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
		logError("Creating directory for %s: %v", poPath, err)
		return nil
	}

	if err := newPO.WriteFile(poPath); err != nil {
		logError("Creating %s: %v", poPath, err)
		return nil
	}

	logSuccess("Auto-created %s from %s (%d entries)", poPath, potPath, len(newPO.Entries))
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
		if storedURL := credentials.GetBaseURL(prov.ID); storedURL != "" {
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
			examples = "check provider documentation"
		}

		return fmt.Errorf("--model is required for provider '%s'\n\n"+
			"Example models for %s:\n  %s\n\n"+
			"Usage: --provider %s --model MODEL_NAME",
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
			return fmt.Errorf("provider 'google' requires an API key or Gemini OAuth login\n\n" +
				"Option 1: Store an API key:\n" +
				"  lokit auth login --provider google\n\n" +
				"Option 2: Login with Google OAuth (free tier: 60 req/min):\n" +
				"  lokit auth login --provider gemini\n\n" +
				"Option 3: Pass key directly:\n" +
				"  --api-key YOUR_KEY or export LOKIT_API_KEY=YOUR_KEY\n\n" +
				"Get an API key from: https://aistudio.google.com/apikey")
		}

	case translate.ProviderGemini:
		if gemini.LoadToken() == nil {
			return fmt.Errorf("provider 'gemini' requires Google OAuth login\n\n" +
				"Login with your Google account:\n" +
				"  lokit auth login --provider gemini\n\n" +
				"This uses Gemini Code Assist (free tier: 60 req/min).\n" +
				"For API key access, use --provider google instead.")
		}

	case translate.ProviderGroq:
		if apiKey == "" {
			return fmt.Errorf("provider 'groq' requires an API key\n\n" +
				"Option 1: Store your API key:\n" +
				"  lokit auth login --provider groq\n\n" +
				"Option 2: Pass key directly:\n" +
				"  --api-key YOUR_KEY or export LOKIT_API_KEY=YOUR_KEY\n\n" +
				"Get a free API key from: https://console.groq.com/keys")
		}

	case translate.ProviderOpenCode:
		// OpenCode can work without API key for some models

	case translate.ProviderCopilot:
		if copilot.LoadToken() == nil {
			return fmt.Errorf("provider 'copilot' requires GitHub Copilot authentication\n\n" +
				"Login with your GitHub account:\n" +
				"  lokit auth login --provider copilot\n\n" +
				"This uses GitHub Copilot (requires active Copilot subscription).")
		}

	case translate.ProviderCustomOpenAI:
		if prov.BaseURL == "" {
			return fmt.Errorf("provider 'custom-openai' requires an endpoint URL\n\n" +
				"Option 1: Configure via auth:\n" +
				"  lokit auth login --provider custom-openai\n\n" +
				"Option 2: Pass directly:\n" +
				"  --base-url https://api.example.com/v1")
		}

	case translate.ProviderOllama:
		client := &http.Client{Timeout: 2 * time.Second}
		ollamaURL := prov.BaseURL
		if ollamaURL == "" {
			ollamaURL = "http://localhost:11434"
		}
		resp, err := client.Get(ollamaURL + "/api/tags")
		if err != nil {
			return fmt.Errorf("provider 'ollama' requires Ollama server to be running\n\n" +
				"Start Ollama with: ollama serve\n" +
				"Install from: https://ollama.com\n" +
				"Alternative providers:\n" +
				"  --provider copilot         (GitHub Copilot, free)\n" +
				"  --provider google          (requires API key)")
		}
		resp.Body.Close()
	}

	return nil
}
