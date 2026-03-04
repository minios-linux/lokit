package cli

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/minios-linux/lokit/config"
	. "github.com/minios-linux/lokit/i18n"
	"github.com/minios-linux/lokit/lockfile"
	"github.com/minios-linux/lokit/settings"
	"github.com/spf13/cobra"
)

func newTranslateCmd() *cobra.Command {
	var (
		langs string

		provider string
		apiKey   string
		model    string
		baseURL  string

		chunkSize   int
		retranslate bool
		fuzzy       bool
		prompt      string
		verbose     bool
		dryRun      bool
		force       bool

		parallel     int
		requestDelay time.Duration

		timeout time.Duration
		proxy   string
		retries int
	)

	cmd := &cobra.Command{
		Use:   "translate",
		Short: T("Translate files using AI"),
		Long: T(`Translate files using AI providers.

Supports gettext PO, po4a, i18next JSON, Android strings.xml, generic JSON,
YAML, Markdown, Java .properties, and Flutter ARB formats. Project type is
auto-detected or configured via lokit.yaml.

For gettext/po4a projects, automatically initializes if needed (extracts
strings, creates PO files).

Incremental translation: lokit tracks source string checksums in lokit.lock.
Only new or changed strings are sent to the AI provider, saving tokens and
time. Use --force to ignore the lock file and re-translate everything.

Key filtering: configure per-target in lokit.yaml:
  ignored_keys  — keys excluded from translation entirely (never sent to AI)
  locked_keys   — keys whose translations are preserved (skipped even with
                  --all; only --force overrides)
  locked_patterns — regex patterns matching keys treated as locked
                  (e.g. "^brand_.*" locks all brand_ prefixed keys)

Each target type has a built-in system prompt optimized for its format.
Use the --prompt flag to override it for the current run, or set prompt:
in lokit.yaml target config for a permanent override.
Use {{targetLang}} as a placeholder for the target language name.

Examples:
  # Translate a gettext project using GitHub Copilot (free)
  lokit translate --provider copilot --model gpt-4o

  # Translate an Android project using Gemini (free, OAuth)
  lokit translate --provider gemini --model gemini-2.5-flash

  # Translate an i18next project using Google AI (API key)
  lokit translate --provider google --model gemini-2.5-flash

  # Translate specific languages in parallel
  lokit translate --provider copilot --model gpt-4o --lang ru,de --parallel

  # Force full re-translation (ignore lock file)
  lokit translate --provider copilot --model gpt-4o --force

  # Translate all entries with a custom prompt
  lokit translate --provider copilot --model gpt-4o --all \
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
				dryRun: dryRun, force: force, parallel: parallel > 0,
				maxConcurrent: parallel, requestDelay: requestDelay,
				timeout: timeout, proxy: proxy, maxRetries: retries,
			})
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", T("AI provider: google, gemini, groq, opencode, copilot, ollama, custom-openai (or use lokit.yaml provider.id)"))
	cmd.Flags().StringVar(&model, "model", "", T("Model name (or use lokit.yaml provider.model)"))
	cmd.Flags().StringVar(&apiKey, "api-key", "", T("API key (or provider env var: GOOGLE_API_KEY, GROQ_API_KEY, OPENAI_API_KEY, OPENCODE_API_KEY)"))
	cmd.Flags().StringVar(&baseURL, "base-url", "", T("Custom API base URL"))

	cmd.Flags().StringVarP(&langs, "lang", "l", "", T("Languages to translate (comma-separated, default: all with untranslated)"))

	cmd.Flags().IntVar(&chunkSize, "chunk", 0, T("Entries per API request (0 = all at once)"))
	cmd.Flags().BoolVarP(&retranslate, "all", "a", false, T("Translate all entries, including already translated ones"))
	cmd.Flags().BoolVar(&fuzzy, "fuzzy", true, T("Translate fuzzy entries and clear fuzzy flag"))
	cmd.Flags().StringVar(&prompt, "prompt", "", T("Custom system prompt (use {{targetLang}} placeholder)"))
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, T("Enable detailed logging"))
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, T("Show what would be translated without calling AI"))
	cmd.Flags().BoolVarP(&force, "force", "f", false, T("Ignore lock file and re-translate all changed entries"))

	cmd.Flags().IntVar(&parallel, "parallel", 0, T("Enable parallel translation with optional worker count (e.g. --parallel or --parallel=8)"))
	cmd.Flags().DurationVar(&requestDelay, "delay", 0, T("Delay between translation requests"))

	cmd.Flags().DurationVar(&timeout, "timeout", 0, T("Request timeout (0 = provider default)"))
	cmd.Flags().StringVar(&proxy, "proxy", "", T("HTTP/HTTPS proxy URL"))
	cmd.Flags().IntVar(&retries, "retries", 3, T("Maximum retries on rate limit (429)"))

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
	verbose, dryRun, force, parallel bool
	maxConcurrent                    int
	requestDelay, timeout            time.Duration
	proxy                            string
	maxRetries                       int
	lockFile                         *lockfile.LockFile
}

func runTranslate(a translateArgs) {
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

func runTranslateWithConfig(lf *config.LokitFile, a translateArgs) {
	providerName := a.provider
	modelName := a.model
	baseURL := a.baseURL
	if lf.Provider != nil {
		if providerName == "" {
			providerName = lf.Provider.ID
		}
		if modelName == "" {
			modelName = lf.Provider.Model
		}
		if baseURL == "" {
			baseURL = lf.Provider.BaseURL
		}
		if a.prompt == "" {
			a.prompt = lf.Provider.Prompt
		}
	}

	key := settings.ResolveAPIKey(providerName, a.apiKey)

	if providerName == "" {
		logError(T("No provider specified. Use --provider to choose an AI translation service.\n\n") +
			"Example: lokit translate --provider copilot --model gpt-4o")
		os.Exit(1)
	}

	prov := resolveProvider(providerName, baseURL, key, modelName, a.proxy, a.timeout)
	if lf.Provider != nil && lf.Provider.Settings.Temperature != nil {
		prov.Temperature = *lf.Provider.Settings.Temperature
	}
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

	lockF, err := lockfile.Load(rootDir)
	if err != nil {
		logWarning(T("Could not load lock file: %v"), err)
		lockF = &lockfile.LockFile{Version: lockfile.Version, Checksums: make(map[string]map[string]string)}
	}
	a.lockFile = lockF

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		logWarning(T("Interrupted, saving progress..."))
		cancel()
	}()

	var langFilter []string
	if a.langs != "" {
		langFilter = strings.Split(a.langs, ",")
	}

	hadErrors := false

	for _, rt := range resolved {
		if ctx.Err() != nil {
			break
		}

		targetLangs := rt.Languages
		if len(langFilter) > 0 {
			targetLangs = intersectLanguages(targetLangs, langFilter)
		}

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
		case config.TargetTypeVueI18n:
			if err := translateVueI18nTarget(ctx, rt, prov, a, targetLangs); err != nil {
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

		if err := a.lockFile.Save(); err != nil {
			logWarning(T("Could not save lock file after target %s: %v"), rt.Target.Name, err)
		}
	}

	if err := a.lockFile.Save(); err != nil {
		logWarning(T("Could not save lock file: %v"), err)
	}

	if hadErrors {
		logError(T("Translation completed with errors"))
		os.Exit(1)
	}
	logSuccess(T("All targets translated!"))
}
