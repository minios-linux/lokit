package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/minios-linux/lokit/copilot"
	"github.com/minios-linux/lokit/gemini"
	. "github.com/minios-linux/lokit/i18n"
	"github.com/minios-linux/lokit/settings"
	"github.com/spf13/cobra"
)

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
			if provider == "" {
				sectionHeader(T("Select provider to authenticate"))
				fmt.Fprintln(os.Stderr)
				for i, p := range allProviders {
					if p.auth == "none" {
						continue
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

	_ = accessToken
	logSuccess(T("Gemini authentication successful!"))

	fmt.Fprintln(os.Stderr)
	info := gemini.LoadToken()
	if info == nil {
		logWarning(T("Token was saved but cannot be loaded"))
	} else {
		_, err = gemini.SetupUser(ctx, info)
		if errors.Is(err, gemini.ErrProjectIDRequired) {
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
					_ = gemini.SaveProjectID(projectID)
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

			fmt.Fprintf(os.Stderr, "\n  %s%s%s\n", colorBold+colorYellow, T("OAuth Providers"), colorReset)
			keyVal(T("copilot"), copilot.TokenStatus())
			keyVal(T("gemini"), gemini.TokenStatus())

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
					status := fmt.Sprintf("%s%s%s (%s)", colorGreen, T("✓ configured"), colorReset, T("no key"))
					status += fmt.Sprintf("\n  %14s %s %s", "", T("endpoint:"), entry.BaseURL)
					keyVal(p.id, status)
				} else {
					keyVal(p.id, fmt.Sprintf("%s%s%s", colorRed, T("not configured"), colorReset))
				}
			}

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
