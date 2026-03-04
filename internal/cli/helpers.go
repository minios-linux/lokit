package cli

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minios-linux/lokit/config"
	"github.com/minios-linux/lokit/copilot"
	"github.com/minios-linux/lokit/extract"
	"github.com/minios-linux/lokit/gemini"
	. "github.com/minios-linux/lokit/i18n"
	po "github.com/minios-linux/lokit/internal/format/po"
	"github.com/minios-linux/lokit/settings"
	"github.com/minios-linux/lokit/translate"
)

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

	// Base directory for path normalization and xgettext working directory.
	baseDir := proj.Root
	if baseDir == "" {
		baseDir, _ = os.Getwd()
	}
	if absBase, err := filepath.Abs(baseDir); err == nil {
		baseDir = absBase
	}

	logInfo(T("Found %d source files (%s)"), len(allFiles), extract.DescribeFiles(allFiles))

	// Split into Go files (need xgotext) and everything else (need xgettext)
	goFiles, otherFiles := extract.SplitGoFiles(allFiles)
	otherFilesForXgettext := make([]string, 0, len(otherFiles))
	for _, f := range otherFiles {
		if rel, err := filepath.Rel(baseDir, f); err == nil {
			otherFilesForXgettext = append(otherFilesForXgettext, rel)
		} else {
			otherFilesForXgettext = append(otherFilesForXgettext, f)
		}
	}

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
			return extract.RunGoExtract(goDirs, outPotFile, proj.Name, proj.Keywords, baseDir)
		}
		logInfo(T("Extracting from %d Go files with xgotext..."), len(goFiles))
		return extract.RunXgotext(goDirs, outPotFile, proj.Name)
	}

	switch {
	case len(otherFiles) > 0 && len(goFiles) > 0:
		// Both Go and non-Go files: extract separately, then merge
		logInfo(T("Extracting from %d non-Go files with xgettext..."), len(otherFiles))
		xgettextResult, err := extract.RunXgettext(otherFilesForXgettext, potFile, proj.Name, proj.Version, proj.BugsEmail, proj.Keywords, baseDir)
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
		result, err := extract.RunXgettext(otherFilesForXgettext, potFile, proj.Name, proj.Version, proj.BugsEmail, proj.Keywords, baseDir)
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
