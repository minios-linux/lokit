package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minios-linux/lokit/config"
	. "github.com/minios-linux/lokit/i18n"
	"github.com/minios-linux/lokit/internal/format/i18next"
	po "github.com/minios-linux/lokit/internal/format/po"
	"github.com/minios-linux/lokit/merge"
	"github.com/spf13/cobra"
)

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

For i18next/vue-i18n/yaml/properties/flutter/js-kv projects: creates missing language
files with empty translations.

For android/markdown/desktop/polkit projects: no init step needed — use 'lokit translate'
directly.

This command is idempotent — safe to run multiple times. Existing
translations are preserved when updating files.

CONFIG FORMAT (lokit.yaml)

  source_lang: en                    # Source language (default: en)
  languages: [ru, de, fr, es, ...]   # Target languages

  targets:
    - name: myproject                # Display name
      format: gettext                # Target format (see below)
      root: .                        # Working directory (default: .)
      # ... format-specific options

TARGET FORMATS

  gettext — Source code string extraction (shell, python, C, Go, etc.)
    dir: po                          # PO files directory (required)
    pot: messages.pot                # POT template filename (inside dir)
    sources: [src, scripts]          # Directories to scan (default: root)
    keywords: [_, N_, gettext]       # xgettext keywords (default: standard set)

  po4a — Documentation translation (man pages, AsciiDoc, etc.)
    config: po4a.cfg                 # Path to po4a.cfg (required)

  i18next — flat JSON translations
    dir: public/translations         # JSON files directory (required)
    pattern: "{lang}.json"           # Language file pattern (required)

  vue-i18n — nested JSON translations
    dir: frontend/src/i18n           # JSON files directory (required)
    pattern: "{lang}.json"           # Language file pattern (required)

  android — Android strings.xml
    dir: app/src/main/res            # Android res/ directory (required)

  yaml — YAML key/value translations
    dir: translations                # YAML files directory (required)
    pattern: "{lang}.yaml"           # Language file pattern (required)

  markdown — Markdown document translation
    dir: translations                # Root dir; files at translations/LANG/ (required)

  properties — Java .properties translations
    dir: translations                # .properties files directory (required)
    pattern: "{lang}.properties"     # Language file pattern (required)

  flutter — Flutter ARB (Application Resource Bundle)
    dir: lib/l10n                    # ARB files directory (required)
    pattern: "app_{lang}.arb"        # Language file pattern (required)

  js-kv — JavaScript assignment key/value translations
    dir: translations                # JS files directory (required)
    pattern: "{lang}.js"             # Language file pattern (required)

  desktop — freedesktop desktop entries (single file)
    dir: .                           # Directory with desktop file
    pattern: "myapp.desktop"         # Desktop file name

  polkit — PolicyKit XML policy (single file)
    dir: .                           # Directory with policy file
    pattern: "org.example.policy"    # Policy file name

COMMON OPTIONS (all target formats)

  languages: [ru, de]               # Override global language list
  prompt: "Custom translation..."   # Override AI translation prompt

EXAMPLES

  # Shell scripts with gettext
  source_lang: en
  languages: [ru, de, fr]
  targets:
    - name: myapp
      format: gettext
      sources: [scripts, lib]
      keywords: [gettext, eval_gettext]

  # Go project with wrapper functions
  targets:
    - name: myapp
      format: gettext
      sources: [.]
      keywords: [T, N]

  # Documentation + code
  targets:
    - name: code
      format: gettext
      sources: [src]
    - name: docs
      format: po4a
      config: docs/po4a.cfg

  # JSON web application
  targets:
    - name: frontend
      format: i18next
      dir: public/translations

  # Flutter application
  targets:
    - name: app
      format: flutter
      dir: lib/l10n

  # Java application with .properties
  targets:
    - name: app
      format: properties
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

	cmd.Flags().StringVarP(&langs, "lang", "l", "", T("Languages to init (comma-separated, default: all from config)"))

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
				POTFile:     rt.AbsPOTFile(),
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
				Po4aConfig:  rt.AbsPo4aConfig(),
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

		case config.TargetTypeI18Next:
			proj := &config.Project{
				Name:               rt.Target.Name,
				Version:            "0.0.0",
				Type:               config.ProjectTypeI18Next,
				I18NextDir:         rt.AbsTranslationsDir(),
				I18NextPathPattern: rt.Target.Pattern,
				Languages:          langs,
				SourceLang:         rt.Target.SourceLang,
			}
			runInitI18Next(proj)

		case config.TargetTypeVueI18n:
			runInitVueI18n(rt, langs)

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

		case config.TargetTypeJSKV:
			runInitJSKV(rt, langs)

		case config.TargetTypeDesktop:
			logInfo(T("Desktop targets do not require init — use 'lokit translate' directly."))

		case config.TargetTypePolkit:
			logInfo(T("Polkit targets do not require init — use 'lokit translate' directly."))
		default:
			logWarning(T("[%s] Unknown target type %q, skipping"), rt.Target.Name, rt.Target.Type)
		}
	}
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
			meta := i18next.ResolveMeta(lang)
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

// ---------------------------------------------------------------------------
