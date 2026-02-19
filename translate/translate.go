// Package translate implements AI-powered translation of PO file entries
// using multiple HTTP API-based AI providers: Google AI (Gemini), Groq,
// OpenCode (multi-format), GitHub Copilot (native OAuth), Custom OpenAI,
// and Ollama.
package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/minios-linux/lokit/android"
	"github.com/minios-linux/lokit/arbfile"
	"github.com/minios-linux/lokit/copilot"
	"github.com/minios-linux/lokit/gemini"
	"github.com/minios-linux/lokit/i18next"
	"github.com/minios-linux/lokit/mdfile"
	po "github.com/minios-linux/lokit/pofile"
	"github.com/minios-linux/lokit/propfile"
	"github.com/minios-linux/lokit/settings"
	"github.com/minios-linux/lokit/yamlfile"
)

// ---------------------------------------------------------------------------
// Provider IDs (matching the admin panel)
// ---------------------------------------------------------------------------

const (
	ProviderGoogle       = "google"
	ProviderGemini       = "gemini"
	ProviderGroq         = "groq"
	ProviderOpenCode     = "opencode"
	ProviderCopilot      = "copilot"
	ProviderCustomOpenAI = "custom-openai"
	ProviderOllama       = "ollama"
)

// ---------------------------------------------------------------------------
// Parallelization modes
// ---------------------------------------------------------------------------

const (
	ParallelSequential   = "sequential"
	ParallelFullParallel = "full-parallel"
)

// ---------------------------------------------------------------------------
// System Prompts Configuration
// ---------------------------------------------------------------------------

// PromptsConfig holds all system prompts loaded from prompts.json
type PromptsConfig struct {
	Prompts map[string]string `json:"prompts"`
}

// globalPrompts holds the loaded prompts configuration
var globalPrompts *PromptsConfig

// LoadPromptsFromFile loads system prompts from a JSON file.
// If the file doesn't exist or can't be loaded, it returns nil (will use embedded defaults).
func LoadPromptsFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		// File not found is not an error - we'll use embedded defaults
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read prompts file: %w", err)
	}

	var config PromptsConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse prompts file: %w", err)
	}

	globalPrompts = &config
	return nil
}

// defaultPromptsMap returns all built-in system prompts as a map.
func defaultPromptsMap() map[string]string {
	return map[string]string{
		"default":  DefaultSystemPrompt,
		"docs":     DocsSystemPrompt,
		"i18next":  I18NextSystemPrompt,
		"recipe":   RecipeSystemPrompt,
		"blogpost": BlogPostSystemPrompt,
		"android":  AndroidSystemPrompt,
	}
}

// createDefaultPromptsFile writes the built-in prompts to path as a formatted JSON file.
func createDefaultPromptsFile(path string) error {
	config := PromptsConfig{
		Prompts: defaultPromptsMap(),
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling default prompts: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating prompts directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing default prompts file: %w", err)
	}
	return nil
}

// LoadPromptsFromDefaultLocations tries to load prompts from the user data directory.
// Default location: ~/.local/share/lokit/prompts.json (or $XDG_DATA_HOME/lokit/prompts.json)
// This matches the location where auth.json is stored.
// If the file does not exist, it is created with built-in default prompts.
// Returns the path of the loaded prompts file, or empty string on error.
func LoadPromptsFromDefaultLocations() (string, error) {
	path, err := settings.PromptsFilePath()
	if err != nil {
		return "", fmt.Errorf("cannot determine prompts file path: %w", err)
	}

	// If the file doesn't exist, create it with defaults
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := createDefaultPromptsFile(path); err != nil {
			return "", fmt.Errorf("creating default prompts file: %w", err)
		}
	}

	if err := LoadPromptsFromFile(path); err != nil {
		return "", err
	}

	if globalPrompts != nil {
		return path, nil
	}

	return "", nil
}

// getPrompt returns the system prompt for a given content type.
// If custom prompts are loaded, it uses them; otherwise falls back to embedded defaults.
func getPrompt(promptType string) string {
	if globalPrompts != nil {
		if prompt, ok := globalPrompts.Prompts[promptType]; ok && prompt != "" {
			return prompt
		}
	}

	// Fallback to embedded defaults
	switch promptType {
	case "default":
		return DefaultSystemPrompt
	case "docs":
		return DocsSystemPrompt
	case "i18next":
		return I18NextSystemPrompt
	case "recipe":
		return RecipeSystemPrompt
	case "blogpost":
		return BlogPostSystemPrompt
	case "android":
		return AndroidSystemPrompt
	default:
		return DefaultSystemPrompt
	}
}

// ---------------------------------------------------------------------------
// Default system prompt (matches the admin panel's defaultPrompt)
// ---------------------------------------------------------------------------

const DefaultSystemPrompt = `You are a professional translator specializing in software and product localization. You are translating UI strings for a software application.

CONTEXT AWARENESS:
- The audience is software users
- Tone: professional yet approachable, clear and concise
- Use IT/software terminology that is standard in {{targetLang}} tech community
- Adapt to the application's specific domain based on the source text context

IMPORTANT TRANSLATION PRINCIPLES:
- Translate for NATURALNESS and FLUENCY in the target language, not word-for-word
- Use idiomatic expressions natural to {{targetLang}}, not literal translations
- Adapt sentence structure to match {{targetLang}} conventions
- Use established IT terminology in {{targetLang}} (e.g., "edition" in software context, not literary terms)
- Consider cultural context and target audience expectations
- Maintain the original tone and intent, but express it naturally in {{targetLang}}

TECHNICAL REQUIREMENTS:
- Return ONLY a JSON array of translated strings, one for each input entry, in the same order.
- Preserve all format specifiers exactly as-is (%s, %d, %%(name)s, etc.).
- Preserve leading/trailing whitespace, newlines, and punctuation patterns.
- Keep brand names and proper nouns unchanged.
- Do NOT translate technical terms that are standard in English (unless they have established translations).
- Return ONLY the JSON array, no explanations or markdown code blocks.`

// DocsSystemPrompt is the system prompt for translating documentation PO files
// (man pages via po4a) that contain groff/roff markup.
const DocsSystemPrompt = `You are a professional translator specializing in technical documentation for software systems. You are translating documentation that uses groff/roff markup via the po4a (PO for anything) framework.

CONTEXT AWARENESS:
- The source text comes from software documentation and man pages
- The audience is system administrators, developers, and advanced users
- Tone: formal technical documentation, precise and unambiguous
- Use IT/system administration terminology standard in {{targetLang}}

IMPORTANT TRANSLATION PRINCIPLES:
- Translate for NATURALNESS and FLUENCY in {{targetLang}}, not word-for-word
- Use idiomatic expressions natural to {{targetLang}} technical writing
- Adapt sentence structure to match {{targetLang}} documentation conventions
- Use established system administration terminology in {{targetLang}}
- Maintain the formal register typical of technical documentation

CRITICAL MARKUP PRESERVATION RULES:
- Preserve ALL groff/roff inline markup exactly as-is:
  - B<...> (bold) — translate content inside, keep B<> wrapper
  - I<...> (italic) — translate content inside, keep I<> wrapper
  - C<...> (code/constant) — do NOT translate content inside
  - L<...> (link) — do NOT translate
- Preserve ALL groff macros and directives:
  - .SH, .SS, .TP, .IP, .PP, .RS, .RE — keep as-is
  - .B, .I, .BR, .IR — keep as-is
- Do NOT translate:
  - Command names, option flags (--option, -f)
  - File paths (/etc/, /usr/share/, etc.)
  - Environment variable names
  - Package names, program names
  - Configuration directive names
  - Code examples and command invocations
- DO translate:
  - Descriptive text and explanations
  - Section headings (inside .SH "...")
  - Option descriptions (the explanation part, not the option itself)

TECHNICAL REQUIREMENTS:
- Return ONLY a JSON array of translated strings, one for each input entry, in the same order.
- Preserve all format specifiers exactly as-is (%s, %d, etc.).
- Preserve leading/trailing whitespace, newlines, and punctuation patterns.
- Keep brand names and proper nouns unchanged.
- CRITICAL: Properly escape ALL backslashes in JSON strings. Groff sequences like \[dq] MUST be escaped as \\[dq] in JSON.
- Return ONLY the JSON array, no explanations or markdown code blocks.`

// ---------------------------------------------------------------------------
// Provider configuration
// ---------------------------------------------------------------------------

// Provider holds the configuration for an AI translation service.
type Provider struct {
	// ID is the provider identifier (google, groq, opencode, etc.).
	ID string
	// Name is the display name.
	Name string
	// BaseURL is the API base URL.
	BaseURL string
	// APIKey is the authentication key (empty for local services).
	APIKey string
	// Model is the model identifier.
	Model string
	// Proxy is an optional HTTP/HTTPS proxy URL.
	Proxy string
	// Timeout is the request timeout.
	Timeout time.Duration
}

// DefaultProviders returns the pre-configured provider definitions.
func DefaultProviders() map[string]Provider {
	return map[string]Provider{
		ProviderGoogle: {
			ID:      ProviderGoogle,
			Name:    "Google AI (Gemini)",
			BaseURL: "https://generativelanguage.googleapis.com",
			Model:   "",
			Timeout: 120 * time.Second,
		},
		ProviderGemini: {
			ID:      ProviderGemini,
			Name:    "Gemini Code Assist (OAuth)",
			Model:   "",
			Timeout: 120 * time.Second,
		},
		ProviderGroq: {
			ID:      ProviderGroq,
			Name:    "Groq",
			BaseURL: "https://api.groq.com/openai/v1",
			Model:   "",
			Timeout: 60 * time.Second,
		},
		ProviderOpenCode: {
			ID:      ProviderOpenCode,
			Name:    "OpenCode",
			BaseURL: "https://opencode.ai/zen/v1",
			Model:   "",
			Timeout: 120 * time.Second,
		},
		ProviderCopilot: {
			ID:      ProviderCopilot,
			Name:    "GitHub Copilot",
			BaseURL: copilot.CopilotAPIBase,
			Model:   "",
			Timeout: 120 * time.Second,
		},
		ProviderCustomOpenAI: {
			ID:      ProviderCustomOpenAI,
			Name:    "Custom OpenAI",
			Model:   "",
			Timeout: 60 * time.Second,
		},
		ProviderOllama: {
			ID:      ProviderOllama,
			Name:    "Ollama",
			BaseURL: "http://localhost:11434",
			Model:   "",
			Timeout: 120 * time.Second,
		},
	}
}

// ---------------------------------------------------------------------------
// Translation options
// ---------------------------------------------------------------------------

// Options controls the translation behavior.
type Options struct {
	// Provider is the AI provider configuration.
	Provider Provider
	// Language is the target language code (e.g., "ru", "de").
	Language string
	// LanguageName is the human-readable name (e.g., "Russian", "German").
	LanguageName string
	// ChunkSize is how many strings to translate per API call (0 = all at once).
	ChunkSize int
	// ParallelMode controls parallelization (sequential, parallel-langs, parallel-chunks, full-parallel).
	ParallelMode string
	// MaxConcurrent is the maximum number of concurrent tasks for parallel modes.
	MaxConcurrent int
	// RequestDelay is the delay between launching parallel tasks.
	RequestDelay time.Duration
	// Timeout is the per-request timeout (overrides provider timeout if set).
	Timeout time.Duration
	// MaxRetries is the maximum number of retries on rate limit (429). Default: 3.
	MaxRetries int
	// RetranslateExisting if true, re-translates already translated entries.
	RetranslateExisting bool
	// TranslateFuzzy if true, translates fuzzy entries and clears the fuzzy flag.
	TranslateFuzzy bool
	// SystemPrompt overrides the default system prompt.
	SystemPrompt string
	// PromptType specifies which prompt to use: "default", "docs", "i18next", "recipe", "blogpost", "android".
	// If SystemPrompt is set, this is ignored.
	PromptType string
	// OnProgress is called after each batch/chunk is translated.
	OnProgress func(lang string, done, total int)
	// OnLog emits log messages during translation.
	OnLog func(format string, args ...any)
	// OnError emits error messages during translation.
	OnError func(format string, args ...any)
	// Verbose enables detailed logging.
	Verbose bool
}

func (o *Options) log(format string, args ...any) {
	if o.OnLog != nil {
		o.OnLog(format, args...)
	}
}

func (o *Options) logError(format string, args ...any) {
	if o.OnError != nil {
		o.OnError(format, args...)
	} else if o.OnLog != nil {
		o.OnLog(format, args...)
	}
}

func (o *Options) effectiveTimeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	if o.Provider.Timeout > 0 {
		return o.Provider.Timeout
	}
	return 120 * time.Second
}

func (o *Options) effectiveMaxRetries() int {
	if o.MaxRetries > 0 {
		return o.MaxRetries
	}
	return 3
}

func (o *Options) effectiveChunkSize() int {
	if o.ChunkSize > 0 {
		return o.ChunkSize
	}
	return 0 // 0 means all at once
}

func (o *Options) effectiveMaxConcurrent() int {
	if o.MaxConcurrent > 0 {
		return o.MaxConcurrent
	}
	return 3
}

// resolvedPrompt returns the system prompt with {{targetLang}} replaced.
func (o *Options) resolvedPrompt() string {
	prompt := o.SystemPrompt
	if prompt == "" {
		promptType := o.PromptType
		if promptType == "" {
			promptType = "default"
		}
		prompt = getPrompt(promptType)
	}
	langName := o.LanguageName
	if langName == "" {
		langName = po.LangNameNative(o.Language)
	}
	return strings.ReplaceAll(prompt, "{{targetLang}}", langName)
}

// ---------------------------------------------------------------------------
// Rate limit state (global pause for parallel workers)
// ---------------------------------------------------------------------------

type rateLimitState struct {
	mu       sync.Mutex
	paused   int32 // atomic: 1 = paused
	pauseEnd time.Time
}

func (r *rateLimitState) isPaused() bool {
	return atomic.LoadInt32(&r.paused) == 1
}

func (r *rateLimitState) pause(duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pauseEnd = time.Now().Add(duration)
	atomic.StoreInt32(&r.paused, 1)
}

func (r *rateLimitState) unpause() {
	atomic.StoreInt32(&r.paused, 0)
}

// waitIfPaused blocks until the rate limit pause is over.
func (r *rateLimitState) waitIfPaused(ctx context.Context) error {
	for r.isPaused() {
		r.mu.Lock()
		remaining := time.Until(r.pauseEnd)
		r.mu.Unlock()
		if remaining <= 0 {
			r.unpause()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(remaining, 100*time.Millisecond)):
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// HTTP client with real proxy support
// ---------------------------------------------------------------------------

func makeHTTPClient(proxyURL string, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	// Support both --proxy flag and HTTP_PROXY/HTTPS_PROXY env vars
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err == nil {
			transport.Proxy = http.ProxyURL(parsed)
		}
	} else {
		// Use http.ProxyFromEnvironment to read HTTP_PROXY/HTTPS_PROXY/http_proxy/https_proxy
		transport.Proxy = http.ProxyFromEnvironment
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}

// ---------------------------------------------------------------------------
// API format types
// ---------------------------------------------------------------------------

type apiFormat int

const (
	formatOpenAIChat      apiFormat = iota // OpenAI chat/completions
	formatGeminiNative                     // Google Gemini generateContent
	formatAnthropic                        // Anthropic messages
	formatOpenAIResponses                  // OpenAI responses API
)

// ---------------------------------------------------------------------------
// Request builders for each API format
// ---------------------------------------------------------------------------

func buildOpenAIChatRequest(model, systemPrompt, userPrompt string, temperature float64) ([]byte, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	req := struct {
		Model       string  `json:"model"`
		Messages    []msg   `json:"messages"`
		Temperature float64 `json:"temperature"`
		Stream      bool    `json:"stream"`
	}{
		Model: model,
		Messages: []msg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: temperature,
		Stream:      false,
	}
	return json.Marshal(req)
}

func buildGeminiRequest(systemPrompt, userPrompt string, temperature float64) ([]byte, error) {
	type part struct {
		Text string `json:"text"`
	}
	type content struct {
		Role  string `json:"role,omitempty"`
		Parts []part `json:"parts"`
	}
	type genConfig struct {
		Temperature float64 `json:"temperature"`
	}
	req := struct {
		Contents          []content `json:"contents"`
		GenerationConfig  genConfig `json:"generationConfig"`
		SystemInstruction *content  `json:"systemInstruction,omitempty"`
	}{
		Contents: []content{
			{Role: "user", Parts: []part{{Text: userPrompt}}},
		},
		GenerationConfig: genConfig{Temperature: temperature},
	}
	if systemPrompt != "" {
		req.SystemInstruction = &content{Parts: []part{{Text: systemPrompt}}}
	}
	return json.Marshal(req)
}

func buildAnthropicRequest(model, systemPrompt, userPrompt string) ([]byte, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	req := struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		System    string `json:"system,omitempty"`
		Messages  []msg  `json:"messages"`
	}{
		Model:     model,
		MaxTokens: 8192,
		System:    systemPrompt,
		Messages: []msg{
			{Role: "user", Content: userPrompt},
		},
	}
	return json.Marshal(req)
}

func buildOpenAIResponsesRequest(model, prompt string) ([]byte, error) {
	// The responses API takes a single "input" string
	fullPrompt := prompt
	req := struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}{
		Model: model,
		Input: fullPrompt,
	}
	return json.Marshal(req)
}

// ---------------------------------------------------------------------------
// Response parsers (multi-format)
// ---------------------------------------------------------------------------

// extractResponseText tries all known response formats and returns the text.
func extractResponseText(body []byte) (string, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", fmt.Errorf("invalid JSON response: %w", err)
	}

	// Check for API error
	if errObj, ok := raw["error"]; ok {
		if errMap, ok := errObj.(map[string]any); ok {
			if msg, ok := errMap["message"].(string); ok {
				return "", fmt.Errorf("API error: %s", msg)
			}
		}
		return "", fmt.Errorf("API error: %v", errObj)
	}

	// 1. OpenAI chat format: choices[0].message.content
	if choices, ok := raw["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if message, ok := choice["message"].(map[string]any); ok {
				if content, ok := message["content"].(string); ok {
					return content, nil
				}
			}
		}
	}

	// 2. Gemini format: candidates[0].content.parts[0].text
	if candidates, ok := raw["candidates"].([]any); ok && len(candidates) > 0 {
		if candidate, ok := candidates[0].(map[string]any); ok {
			if content, ok := candidate["content"].(map[string]any); ok {
				if parts, ok := content["parts"].([]any); ok && len(parts) > 0 {
					if part, ok := parts[0].(map[string]any); ok {
						if text, ok := part["text"].(string); ok {
							return text, nil
						}
					}
				}
			}
		}
	}

	// 3. Anthropic format: content[].type=="text" -> .text
	if contentArr, ok := raw["content"].([]any); ok {
		for _, c := range contentArr {
			if block, ok := c.(map[string]any); ok {
				if block["type"] == "text" {
					if text, ok := block["text"].(string); ok {
						return text, nil
					}
				}
			}
		}
	}

	// 4. OpenAI responses format: output[].type=="message" -> .content[].type=="output_text" -> .text
	if output, ok := raw["output"].([]any); ok {
		for _, o := range output {
			if item, ok := o.(map[string]any); ok {
				if item["type"] == "message" {
					if contentArr, ok := item["content"].([]any); ok {
						for _, c := range contentArr {
							if block, ok := c.(map[string]any); ok {
								if block["type"] == "output_text" {
									if text, ok := block["text"].(string); ok {
										return text, nil
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// 5. Simple response field (gemini-cli normalized)
	if resp, ok := raw["response"].(string); ok {
		return resp, nil
	}

	return "", fmt.Errorf("could not extract text from response: %s", truncate(string(body), 500))
}

// ---------------------------------------------------------------------------
// Rate limit: parse 429 response for retry delay
// ---------------------------------------------------------------------------

// parseRetryDelay extracts the retry delay from a 429 response body.
// Looks for Google's RetryInfo detail with retryDelay field.
// Returns the delay to wait, defaulting to 60s + 5s buffer.
func parseRetryDelay(body []byte) time.Duration {
	const defaultDelay = 65 * time.Second // 60s + 5s buffer

	var errResp struct {
		Error struct {
			Details []struct {
				Type       string `json:"@type"`
				RetryDelay string `json:"retryDelay"`
			} `json:"details"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &errResp); err != nil {
		return defaultDelay
	}

	for _, detail := range errResp.Error.Details {
		if strings.Contains(detail.Type, "RetryInfo") && detail.RetryDelay != "" {
			// Parse duration like "30s", "1.5m", "45.123s"
			d := detail.RetryDelay
			d = strings.TrimSuffix(d, "s")
			if secs, err := strconv.ParseFloat(d, 64); err == nil {
				return time.Duration(secs*1000)*time.Millisecond + 5*time.Second
			}
		}
	}

	return defaultDelay
}

// ---------------------------------------------------------------------------
// Provider-specific API call dispatch
// ---------------------------------------------------------------------------

// callProvider sends a prompt to the configured provider and returns the response text.
func callProvider(ctx context.Context, prov Provider, systemPrompt, userPrompt string, rl *rateLimitState, maxRetries int, verbose bool) (string, error) {
	switch prov.ID {
	case ProviderGoogle:
		// Use OAuth if no API key but Gemini token is available
		if prov.APIKey == "" && gemini.LoadToken() != nil {
			return callGeminiOAuth(ctx, prov, systemPrompt, userPrompt, rl, maxRetries, verbose)
		}
		return callHTTPProvider(ctx, prov, systemPrompt, userPrompt, formatGeminiNative, rl, maxRetries, verbose)
	case ProviderGemini:
		// Gemini OAuth (Code Assist) — always uses OAuth
		return callGeminiOAuth(ctx, prov, systemPrompt, userPrompt, rl, maxRetries, verbose)
	case ProviderGroq:
		return callHTTPProvider(ctx, prov, systemPrompt, userPrompt, formatOpenAIChat, rl, maxRetries, verbose)
	case ProviderOpenCode:
		return callOpenCode(ctx, prov, systemPrompt, userPrompt, rl, maxRetries, verbose)
	case ProviderCopilot:
		return callCopilot(ctx, prov, systemPrompt, userPrompt, rl, maxRetries, verbose)
	case ProviderCustomOpenAI:
		return callHTTPProvider(ctx, prov, systemPrompt, userPrompt, formatOpenAIChat, rl, maxRetries, verbose)
	case ProviderOllama:
		return callHTTPProvider(ctx, prov, systemPrompt, userPrompt, formatOpenAIChat, rl, maxRetries, verbose)
	default:
		// Fallback: treat as OpenAI-compatible
		return callHTTPProvider(ctx, prov, systemPrompt, userPrompt, formatOpenAIChat, rl, maxRetries, verbose)
	}
}

// ---------------------------------------------------------------------------
// HTTP-based provider call (Google, Groq, Custom OpenAI, Ollama, generic)
// ---------------------------------------------------------------------------

func callHTTPProvider(ctx context.Context, prov Provider, systemPrompt, userPrompt string, format apiFormat, rl *rateLimitState, maxRetries int, verbose bool) (string, error) {
	endpoint, headers, body, err := buildHTTPRequest(prov, systemPrompt, userPrompt, format)
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}

	client := makeHTTPClient(prov.Proxy, prov.Timeout)

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Wait if globally paused (rate limit from another worker)
		if rl != nil {
			if err := rl.waitIfPaused(ctx); err != nil {
				return "", err
			}
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("creating request: %w", err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		if verbose {
			log.Printf("[DEBUG] %s attempt %d: POST %s", prov.Name, attempt+1, endpoint)
		}

		resp, err := client.Do(req)
		if err != nil {
			if attempt < maxRetries {
				wait := time.Duration(math.Pow(2, float64(attempt))) * time.Second
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(wait):
				}
				continue
			}
			return "", fmt.Errorf("API request failed: %w", err)
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			retryDelay := parseRetryDelay(respBody)
			if verbose {
				log.Printf("[WARN] 429 rate limited, waiting %v before retry (attempt %d/%d)", retryDelay, attempt+1, maxRetries)
			}
			// Globally pause all workers
			if rl != nil {
				rl.pause(retryDelay)
			}
			if attempt < maxRetries {
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(retryDelay):
				}
				if rl != nil {
					rl.unpause()
				}
				continue
			}
			return "", fmt.Errorf("rate limited after %d retries: %s", maxRetries, string(respBody))
		}

		if resp.StatusCode != http.StatusOK {
			if attempt < maxRetries && resp.StatusCode >= 500 {
				wait := time.Duration(math.Pow(2, float64(attempt))) * time.Second
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(wait):
				}
				continue
			}
			return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, truncate(string(respBody), 500))
		}

		text, err := extractResponseText(respBody)
		if err != nil {
			return "", err
		}
		return text, nil
	}

	return "", fmt.Errorf("exhausted all %d retries", maxRetries)
}

// buildHTTPRequest constructs the endpoint, headers, and body for an HTTP provider.
func buildHTTPRequest(prov Provider, systemPrompt, userPrompt string, format apiFormat) (string, map[string]string, []byte, error) {
	headers := map[string]string{
		"Content-Type": "application/json",
	}

	var endpoint string
	var body []byte
	var err error

	switch format {
	case formatGeminiNative:
		// Google AI: POST /v1beta/models/{model}:generateContent
		endpoint = fmt.Sprintf("%s/v1beta/models/%s:generateContent",
			strings.TrimRight(prov.BaseURL, "/"), prov.Model)
		if prov.APIKey != "" {
			headers["x-goog-api-key"] = prov.APIKey
		}
		body, err = buildGeminiRequest(systemPrompt, userPrompt, 0.3)

	case formatAnthropic:
		endpoint = strings.TrimRight(prov.BaseURL, "/") + "/messages"
		if prov.APIKey != "" {
			headers["x-api-key"] = prov.APIKey
		}
		headers["anthropic-version"] = "2023-06-01"
		body, err = buildAnthropicRequest(prov.Model, systemPrompt, userPrompt)

	case formatOpenAIResponses:
		endpoint = strings.TrimRight(prov.BaseURL, "/") + "/responses"
		if prov.APIKey != "" {
			headers["Authorization"] = "Bearer " + prov.APIKey
		}
		fullPrompt := systemPrompt + "\n\n" + userPrompt
		body, err = buildOpenAIResponsesRequest(prov.Model, fullPrompt)

	default: // formatOpenAIChat
		baseURL := strings.TrimRight(prov.BaseURL, "/")
		if !strings.HasSuffix(baseURL, "/chat/completions") {
			endpoint = baseURL + "/chat/completions"
		} else {
			endpoint = baseURL
		}
		if prov.APIKey != "" {
			headers["Authorization"] = "Bearer " + prov.APIKey
		}
		body, err = buildOpenAIChatRequest(prov.Model, systemPrompt, userPrompt, 0.3)
	}

	if err != nil {
		return "", nil, nil, err
	}
	return endpoint, headers, body, nil
}

// ---------------------------------------------------------------------------
// OpenCode provider (multi-format dispatch based on model prefix)
// ---------------------------------------------------------------------------

func callOpenCode(ctx context.Context, prov Provider, systemPrompt, userPrompt string, rl *rateLimitState, maxRetries int, verbose bool) (string, error) {
	// Determine API format based on model prefix
	model := prov.Model
	var format apiFormat
	adjustedProv := prov

	switch {
	case strings.HasPrefix(model, "gemini-"):
		format = formatGeminiNative
		adjustedProv.BaseURL = strings.TrimRight(prov.BaseURL, "/")
		// For Gemini format, the endpoint is built differently: /models/{model}
		// We override BaseURL to include the full path
		endpoint := fmt.Sprintf("%s/models/%s", adjustedProv.BaseURL, model)
		adjustedProv.BaseURL = endpoint
		// Use Gemini auth header
		return callGeminiViaOpenCode(ctx, adjustedProv, systemPrompt, userPrompt, rl, maxRetries, verbose)

	case strings.HasPrefix(model, "claude-"):
		format = formatAnthropic

	case strings.HasPrefix(model, "gpt-"):
		format = formatOpenAIResponses

	default:
		format = formatOpenAIChat
	}

	return callHTTPProvider(ctx, adjustedProv, systemPrompt, userPrompt, format, rl, maxRetries, verbose)
}

// callGeminiViaOpenCode handles the Gemini-format call through OpenCode.
func callGeminiViaOpenCode(ctx context.Context, prov Provider, systemPrompt, userPrompt string, rl *rateLimitState, maxRetries int, verbose bool) (string, error) {
	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if prov.APIKey != "" {
		headers["x-goog-api-key"] = prov.APIKey
	}

	body, err := buildGeminiRequest(systemPrompt, userPrompt, 0.3)
	if err != nil {
		return "", err
	}

	// prov.BaseURL already contains the full endpoint (e.g., .../models/gemini-2.5-flash)
	endpoint := prov.BaseURL

	client := makeHTTPClient(prov.Proxy, prov.Timeout)

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if rl != nil {
			if err := rl.waitIfPaused(ctx); err != nil {
				return "", err
			}
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("creating request: %w", err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			if attempt < maxRetries {
				wait := time.Duration(math.Pow(2, float64(attempt))) * time.Second
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(wait):
				}
				continue
			}
			return "", fmt.Errorf("API request failed: %w", err)
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			retryDelay := parseRetryDelay(respBody)
			if rl != nil {
				rl.pause(retryDelay)
			}
			if attempt < maxRetries {
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(retryDelay):
				}
				if rl != nil {
					rl.unpause()
				}
				continue
			}
			return "", fmt.Errorf("rate limited after %d retries", maxRetries)
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, truncate(string(respBody), 500))
		}

		return extractResponseText(respBody)
	}

	return "", fmt.Errorf("exhausted all %d retries", maxRetries)
}

// ---------------------------------------------------------------------------
// GitHub Copilot provider (native OAuth, OpenAI-compatible API)
// ---------------------------------------------------------------------------

// callCopilot authenticates with GitHub Copilot and calls the API.
// Uses the OpenAI chat completions format against api.githubcopilot.com.
func callCopilot(ctx context.Context, prov Provider, systemPrompt, userPrompt string, rl *rateLimitState, maxRetries int, verbose bool) (string, error) {
	// Ensure we have a valid token (will prompt for auth if needed)
	accessToken, err := copilot.EnsureAuth(ctx)
	if err != nil {
		return "", fmt.Errorf("Copilot authentication failed: %w", err)
	}

	// Build OpenAI chat completions request body
	body, err := buildOpenAIChatRequest(prov.Model, systemPrompt, userPrompt, 0.3)
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}

	endpoint := strings.TrimRight(prov.BaseURL, "/") + "/chat/completions"
	client := makeHTTPClient(prov.Proxy, prov.Timeout)

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Wait if globally paused (rate limit from another worker)
		if rl != nil {
			if err := rl.waitIfPaused(ctx); err != nil {
				return "", err
			}
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("creating request: %w", err)
		}

		// Set Copilot-specific headers
		req.Header.Set("Content-Type", "application/json")
		copilot.SetAuthHeaders(req, accessToken)

		if verbose {
			log.Printf("[DEBUG] copilot attempt %d: POST %s (model: %s)", attempt+1, endpoint, prov.Model)
		}

		resp, err := client.Do(req)
		if err != nil {
			if attempt < maxRetries {
				wait := time.Duration(math.Pow(2, float64(attempt))) * time.Second
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(wait):
				}
				continue
			}
			return "", fmt.Errorf("Copilot API request failed: %w", err)
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized {
			// Token may be expired/invalid -- delete and retry auth once
			if attempt == 0 {
				if verbose {
					log.Printf("[WARN] Copilot returned 401, re-authenticating...")
				}
				_ = copilot.DeleteToken()
				newToken, err := copilot.EnsureAuth(ctx)
				if err != nil {
					return "", fmt.Errorf("Copilot re-authentication failed: %w", err)
				}
				accessToken = newToken
				continue
			}
			return "", fmt.Errorf("Copilot authentication failed (401): %s", truncate(string(respBody), 300))
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			retryDelay := parseRetryDelay(respBody)
			if verbose {
				log.Printf("[WARN] Copilot 429 rate limited, waiting %v (attempt %d/%d)", retryDelay, attempt+1, maxRetries)
			}
			if rl != nil {
				rl.pause(retryDelay)
			}
			if attempt < maxRetries {
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(retryDelay):
				}
				if rl != nil {
					rl.unpause()
				}
				continue
			}
			return "", fmt.Errorf("Copilot rate limited after %d retries: %s", maxRetries, string(respBody))
		}

		if resp.StatusCode != http.StatusOK {
			if attempt < maxRetries && resp.StatusCode >= 500 {
				wait := time.Duration(math.Pow(2, float64(attempt))) * time.Second
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(wait):
				}
				continue
			}

			// Special handling for 403 Forbidden - usually geographic restrictions or no subscription
			if resp.StatusCode == http.StatusForbidden {
				return "", fmt.Errorf("Copilot API returned 403 Forbidden: access denied\n\n" +
					"Common causes:\n" +
					"  1. Geographic restrictions - GitHub Copilot may be blocked in your region\n" +
					"  2. No active Copilot subscription ($10/month or free for students/OSS maintainers)\n" +
					"  3. Invalid or expired authentication token\n\n" +
					"Solutions:\n" +
					"  - Re-authenticate: lokit auth logout --provider copilot && lokit auth login --provider copilot\n" +
					"  - Use proxy/VPN if geographic restrictions apply\n" +
					"  - Try alternative providers that may work in your region:\n" +
					"      lokit auth login --provider gemini      (Google, 60 req/min free)\n" +
					"      lokit auth login --provider google      (Google AI Studio)\n" +
					"      lokit translate --provider ollama --model llama3.2  (local, no restrictions)")
			}

			return "", fmt.Errorf("Copilot API returned status %d: %s", resp.StatusCode, truncate(string(respBody), 500))
		}

		text, err := extractResponseText(respBody)
		if err != nil {
			return "", err
		}
		return text, nil
	}

	return "", fmt.Errorf("Copilot: exhausted all %d retries", maxRetries)
}

// ---------------------------------------------------------------------------
// Google Gemini OAuth provider (Code Assist API)
//
// The gemini-cli OAuth client ID is registered for the Code Assist API
// (cloudcode-pa.googleapis.com), NOT the public Generative Language API.
// The Code Assist API wraps Vertex AI requests in a two-level envelope:
//
//   Request:  { "model": "...", "request": { <vertex-format> } }
//   Response: { "response": { "candidates": [...] }, "traceId": "..." }
// ---------------------------------------------------------------------------

// caGenerateContentRequest is the Code Assist API request wrapper.
type caGenerateContentRequest struct {
	Model        string      `json:"model"`
	Project      string      `json:"project,omitempty"`
	UserPromptID string      `json:"user_prompt_id,omitempty"`
	Request      interface{} `json:"request"`
}

// caGenerateContentResponse is the Code Assist API response wrapper.
type caGenerateContentResponse struct {
	Response json.RawMessage `json:"response"`
	TraceID  string          `json:"traceId,omitempty"`
}

func callGeminiOAuth(ctx context.Context, prov Provider, systemPrompt, userPrompt string, rl *rateLimitState, maxRetries int, verbose bool) (string, error) {
	// Ensure we have a valid token with Code Assist project ID
	token, err := gemini.EnsureAuthWithSetup(ctx)
	if err != nil {
		return "", fmt.Errorf("Gemini authentication failed: %w", err)
	}
	accessToken := token.Access

	// Build the inner Gemini-native request body (Vertex format)
	innerBody, err := buildGeminiRequest(systemPrompt, userPrompt, 0.3)
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}

	// Parse inner body to embed it in the Code Assist wrapper
	var innerReq interface{}
	if err := json.Unmarshal(innerBody, &innerReq); err != nil {
		return "", fmt.Errorf("parsing inner request: %w", err)
	}

	// Model name: Code Assist expects bare model name (e.g. "gemini-2.5-flash"),
	// NOT "models/gemini-2.5-flash" (unlike the public Generative Language API).
	modelName := prov.Model
	modelName = strings.TrimPrefix(modelName, "models/")

	// Wrap in Code Assist envelope with project ID
	caReq := caGenerateContentRequest{
		Model:   modelName,
		Project: token.ProjectID,
		Request: innerReq,
	}
	body, err := json.Marshal(caReq)
	if err != nil {
		return "", fmt.Errorf("marshaling Code Assist request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/%s:generateContent",
		gemini.CodeAssistBase, gemini.CodeAssistVersion)
	client := makeHTTPClient(prov.Proxy, prov.Timeout)

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Wait if globally paused (rate limit from another worker)
		if rl != nil {
			if err := rl.waitIfPaused(ctx); err != nil {
				return "", err
			}
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("creating request: %w", err)
		}

		// Set OAuth headers
		req.Header.Set("Content-Type", "application/json")
		gemini.SetAuthHeaders(req, accessToken)

		if verbose {
			log.Printf("[DEBUG] gemini-oauth attempt %d: POST %s (model: %s)", attempt+1, endpoint, prov.Model)
		}

		resp, err := client.Do(req)
		if err != nil {
			if attempt < maxRetries {
				wait := time.Duration(math.Pow(2, float64(attempt))) * time.Second
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(wait):
				}
				continue
			}
			return "", fmt.Errorf("Gemini API request failed: %w", err)
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized {
			// Token may be expired/revoked -- try refreshing once
			if attempt == 0 {
				if verbose {
					log.Printf("[WARN] Gemini returned 401, refreshing token...")
				}
				tok := gemini.LoadToken()
				if tok != nil && tok.Refresh != "" {
					if err := gemini.RefreshAccessToken(ctx, tok); err != nil {
						if verbose {
							log.Printf("[WARN] Token refresh failed: %v, re-authenticating...", err)
						}
						_ = gemini.DeleteToken()
						newTok, err := gemini.EnsureAuthWithSetup(ctx)
						if err != nil {
							return "", fmt.Errorf("Gemini re-authentication failed: %w", err)
						}
						accessToken = newTok.Access
					} else {
						accessToken = tok.Access
					}
				} else {
					_ = gemini.DeleteToken()
					newTok, err := gemini.EnsureAuthWithSetup(ctx)
					if err != nil {
						return "", fmt.Errorf("Gemini re-authentication failed: %w", err)
					}
					accessToken = newTok.Access
				}
				continue
			}
			return "", fmt.Errorf("Gemini authentication failed (401): %s", truncate(string(respBody), 300))
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			retryDelay := parseRetryDelay(respBody)
			if verbose {
				log.Printf("[WARN] Gemini 429 rate limited, waiting %v (attempt %d/%d)", retryDelay, attempt+1, maxRetries)
			}
			if rl != nil {
				rl.pause(retryDelay)
			}
			if attempt < maxRetries {
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(retryDelay):
				}
				if rl != nil {
					rl.unpause()
				}
				continue
			}
			return "", fmt.Errorf("Gemini rate limited after %d retries: %s", maxRetries, string(respBody))
		}

		if resp.StatusCode != http.StatusOK {
			if attempt < maxRetries && resp.StatusCode >= 500 {
				wait := time.Duration(math.Pow(2, float64(attempt))) * time.Second
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(wait):
				}
				continue
			}
			return "", fmt.Errorf("Gemini API returned status %d: %s", resp.StatusCode, truncate(string(respBody), 500))
		}

		// Unwrap Code Assist response envelope
		var caResp caGenerateContentResponse
		if err := json.Unmarshal(respBody, &caResp); err != nil {
			// Fallback: try parsing as standard Vertex/Gemini response
			text, err2 := extractResponseText(respBody)
			if err2 != nil {
				return "", fmt.Errorf("parsing Code Assist response: %w (raw: %s)", err, truncate(string(respBody), 300))
			}
			return text, nil
		}

		// The inner "response" field contains standard Vertex AI format
		// which extractResponseText already knows how to parse
		if len(caResp.Response) > 0 {
			text, err := extractResponseText(caResp.Response)
			if err != nil {
				return "", fmt.Errorf("extracting text from Code Assist response: %w", err)
			}
			return text, nil
		}

		// If no "response" wrapper, try full body (backwards compat)
		text, err := extractResponseText(respBody)
		if err != nil {
			return "", err
		}
		return text, nil
	}

	return "", fmt.Errorf("Gemini OAuth: exhausted all %d retries", maxRetries)
}

// ---------------------------------------------------------------------------
// Translation response parsing
// ---------------------------------------------------------------------------

var markdownCodeBlock = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")
var groffEscapePattern = regexp.MustCompile(`(\\\[(?:dq|aq|co|lq|rq|oq|cq|em|en|ha|ti|bu|de|ps|ts|Fo|Fc|Po|Pc|rs)\])`)

// fixGroffEscapesInJSON properly escapes backslashes in groff escape sequences
// and other invalid escape sequences within JSON strings. AI models sometimes
// return sequences like \[dq], \&, \m without properly escaping the backslash.
//
// We need to find invalid escape sequences inside JSON string values and ensure
// the backslashes are properly escaped: \& -> \\&, \[dq] -> \\[dq]
func fixGroffEscapesInJSON(jsonContent string) string {
	var fixed strings.Builder
	inQuote := false
	escaped := false

	for i := 0; i < len(jsonContent); i++ {
		c := jsonContent[i]

		if c == '"' && !escaped {
			inQuote = !inQuote
			fixed.WriteByte(c)
			escaped = false
			continue
		}

		if inQuote && c == '\\' && !escaped {
			// Check if next character forms a valid JSON escape sequence
			if i+1 < len(jsonContent) {
				next := jsonContent[i+1]
				// Valid JSON escapes: \" \\ \/ \b \f \n \r \t \uXXXX
				if next == '"' || next == '\\' || next == '/' ||
					next == 'b' || next == 'f' || next == 'n' ||
					next == 'r' || next == 't' || next == 'u' {
					// Valid JSON escape sequence, keep as-is
					fixed.WriteByte(c)
					escaped = true
					continue
				}
				// Invalid escape sequence - backslash needs to be doubled
				// Examples: \&, \m, \[dq], etc.
				fixed.WriteString("\\\\")
				escaped = false
				continue
			}
			// Backslash at end of string - escape it
			fixed.WriteString("\\\\")
			escaped = false
			continue
		}

		fixed.WriteByte(c)
		escaped = (c == '\\' && !escaped)
	}

	return fixed.String()
}

// npluralsFromFile returns the number of plural forms for a PO file by reading
// the Plural-Forms header, falling back to the per-language default.
func npluralsFromFile(poFile *po.File, lang string) int {
	pluralForms := poFile.HeaderField("Plural-Forms")
	if pluralForms == "" {
		pluralForms = po.PluralFormsForLang(lang)
	}
	// Parse "nplurals=N; ..."
	for _, part := range strings.Split(pluralForms, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "nplurals=") {
			n, err := strconv.Atoi(strings.TrimPrefix(part, "nplurals="))
			if err == nil && n > 0 {
				return n
			}
		}
	}
	return 2 // safe default
}

// pluralTranslation holds the result for one entry: either a single string
// (singular) or multiple strings (plural forms).
type pluralTranslation struct {
	singular string
	plural   []string // non-nil only for entries with MsgIDPlural
}

// translateChunkWithPlurals translates a chunk of entries, correctly handling
// plural forms. For entries that have a MsgIDPlural the AI is asked to return
// all nplurals forms; singular entries produce a single string as before.
func translateChunkWithPlurals(ctx context.Context, entries []*po.Entry, systemPrompt string, opts Options, rl *rateLimitState, nplurals int) ([]pluralTranslation, error) {
	var userMsg strings.Builder
	userMsg.WriteString("Translate these entries:\n\n")

	for i, e := range entries {
		if e.MsgIDPlural != "" {
			userMsg.WriteString(fmt.Sprintf("%d. singular: %s | plural: %s\n",
				i+1, escapeForPrompt(e.MsgID), escapeForPrompt(e.MsgIDPlural)))
			userMsg.WriteString(fmt.Sprintf("   (return an array of exactly %d plural forms for the target language)\n", nplurals))
		} else {
			userMsg.WriteString(fmt.Sprintf("%d. %s\n", i+1, escapeForPrompt(e.MsgID)))
		}
		if len(e.References) > 0 {
			userMsg.WriteString(fmt.Sprintf("   (context: %s)\n", strings.Join(e.References, ", ")))
		}
	}

	userMsg.WriteString(fmt.Sprintf("\nReturn a JSON array with exactly %d elements. ", len(entries)))
	userMsg.WriteString("For singular entries return a string. For plural entries (marked with 'singular: ... | plural: ...') return an array of strings (one per plural form).")

	text, err := callProvider(ctx, opts.Provider, systemPrompt, userMsg.String(), rl, opts.effectiveMaxRetries(), opts.Verbose)
	if err != nil {
		return nil, err
	}

	return parsePluralTranslations(text, entries, nplurals)
}

// parsePluralTranslations parses the AI response into a slice of pluralTranslation.
// Each element corresponds to the entry at the same index.
func parsePluralTranslations(content string, entries []*po.Entry, nplurals int) ([]pluralTranslation, error) {
	content = strings.TrimSpace(content)

	// Strip markdown code blocks if present
	if m := markdownCodeBlock.FindStringSubmatch(content); len(m) > 1 {
		content = m[1]
	}

	// Find outer JSON array
	startIdx := strings.Index(content, "[")
	endIdx := strings.LastIndex(content, "]")
	if startIdx >= 0 && endIdx > startIdx {
		content = content[startIdx : endIdx+1]
	}

	content = fixGroffEscapesInJSON(content)

	// Decode as []json.RawMessage so we can handle mixed string/array elements
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse translation response as JSON array: %w\nResponse: %s", err, truncate(content, 300))
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("got 0 translations, expected %d", len(entries))
	}

	result := make([]pluralTranslation, len(entries))
	for i, entry := range entries {
		if i >= len(raw) {
			break
		}
		elem := raw[i]

		if entry.MsgIDPlural != "" {
			// Expect a JSON array of strings
			var forms []string
			if err := json.Unmarshal(elem, &forms); err != nil {
				// AI might have returned a plain string — use it as form[0] and duplicate
				var s string
				if err2 := json.Unmarshal(elem, &s); err2 == nil {
					forms = make([]string, nplurals)
					for j := range forms {
						forms[j] = s
					}
				}
			}
			// Ensure exactly nplurals forms (pad with last form if short)
			for len(forms) < nplurals {
				if len(forms) > 0 {
					forms = append(forms, forms[len(forms)-1])
				} else {
					forms = append(forms, "")
				}
			}
			result[i] = pluralTranslation{plural: forms[:nplurals]}
		} else {
			// Expect a plain string; if AI returned array, take first element
			var s string
			if err := json.Unmarshal(elem, &s); err != nil {
				var arr []string
				if err2 := json.Unmarshal(elem, &arr); err2 == nil && len(arr) > 0 {
					s = arr[0]
				}
			}
			result[i] = pluralTranslation{singular: s}
		}
	}
	return result, nil
}

// parseTranslations extracts a JSON array of strings from the AI response text.
func parseTranslations(content string, expected int) ([]string, error) {
	content = strings.TrimSpace(content)

	// Strip markdown code blocks if present
	if m := markdownCodeBlock.FindStringSubmatch(content); len(m) > 1 {
		content = m[1]
	}

	// Try to find a JSON array in the response
	startIdx := strings.Index(content, "[")
	endIdx := strings.LastIndex(content, "]")
	if startIdx >= 0 && endIdx > startIdx {
		content = content[startIdx : endIdx+1]
	}

	// Fix common groff/man escape sequences that break JSON parsing
	// AI models sometimes don't properly escape backslashes in JSON strings
	// containing groff sequences like \[dq], \[aq], \[co], etc.
	content = fixGroffEscapesInJSON(content)

	var translations []string
	if err := json.Unmarshal([]byte(content), &translations); err != nil {
		return nil, fmt.Errorf("failed to parse translation response as JSON array: %w\nResponse: %s", err, truncate(content, 300))
	}

	if len(translations) == 0 {
		return nil, fmt.Errorf("got 0 translations, expected %d", expected)
	}

	return translations, nil
}

// ---------------------------------------------------------------------------
// Core translation logic
// ---------------------------------------------------------------------------

// translationTask represents a single chunk of entries to translate for a language.
type translationTask struct {
	lang    string
	entries []*po.Entry
	poFile  *po.File
	poPath  string
}

// Translate translates untranslated entries in a PO file using an AI provider.
// This is the single-language entry point used by the sequential path and by
// parallel workers.
func Translate(ctx context.Context, poFile *po.File, opts Options) error {
	// Collect entries to translate
	toTranslate := collectEntries(poFile, opts)
	if len(toTranslate) == 0 {
		return nil
	}

	chunkSize := opts.effectiveChunkSize()
	if chunkSize == 0 {
		chunkSize = len(toTranslate) // All at once
	}

	rl := &rateLimitState{}
	total := len(toTranslate)

	// Split into chunks
	chunks := splitEntries(toTranslate, chunkSize)

	systemPrompt := opts.resolvedPrompt()
	done := 0

	// Determine number of plural forms for this language once
	nplurals := npluralsFromFile(poFile, opts.Language)

	for i, chunk := range chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if opts.Verbose {
			opts.log("  Chunk %d/%d (%d entries)", i+1, len(chunks), len(chunk))
		}

		if hasPluralEntries(chunk) {
			// Use plural-aware path when any entry in the chunk has a plural form
			translations, err := translateChunkWithPlurals(ctx, chunk, systemPrompt, opts, rl, nplurals)
			if err != nil {
				return fmt.Errorf("translating chunk %d/%d: %w", i+1, len(chunks), err)
			}
			applyPluralTranslations(chunk, translations, opts.TranslateFuzzy)
		} else {
			translations, err := translateChunk(ctx, chunk, systemPrompt, opts, rl)
			if err != nil {
				return fmt.Errorf("translating chunk %d/%d: %w", i+1, len(chunks), err)
			}
			applyTranslations(chunk, translations, opts.TranslateFuzzy)
		}

		done += len(chunk)
		if opts.OnProgress != nil {
			opts.OnProgress(opts.Language, done, total)
		}

		// Delay between chunks
		if i < len(chunks)-1 && opts.RequestDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opts.RequestDelay):
			}
		}
	}

	return nil
}

// TranslateMulti translates multiple PO files: sequential or full-parallel.
func TranslateMulti(ctx context.Context, tasks []translationTask, opts Options) error {
	if opts.ParallelMode == ParallelFullParallel {
		return translateFullParallel(ctx, tasks, opts)
	}
	return translateSequential(ctx, tasks, opts)
}

// collectEntries gathers entries that need translation from a PO file.
func collectEntries(poFile *po.File, opts Options) []*po.Entry {
	var toTranslate []*po.Entry
	for _, e := range poFile.Entries {
		if e.MsgID == "" || e.Obsolete {
			continue
		}
		if opts.RetranslateExisting {
			toTranslate = append(toTranslate, e)
		} else if opts.TranslateFuzzy && e.IsFuzzy() {
			toTranslate = append(toTranslate, e)
		} else if !e.IsTranslated() && !e.IsFuzzy() {
			toTranslate = append(toTranslate, e)
		}
	}
	return toTranslate
}

// splitEntries divides entries into chunks of the given size.
func splitEntries(entries []*po.Entry, chunkSize int) [][]*po.Entry {
	if chunkSize <= 0 || chunkSize >= len(entries) {
		return [][]*po.Entry{entries}
	}
	var chunks [][]*po.Entry
	for i := 0; i < len(entries); i += chunkSize {
		end := i + chunkSize
		if end > len(entries) {
			end = len(entries)
		}
		chunks = append(chunks, entries[i:end])
	}
	return chunks
}

// translateChunk translates a single chunk of entries (singular only, no plural).
// Kept for backward compatibility with non-PO callers.
func translateChunk(ctx context.Context, entries []*po.Entry, systemPrompt string, opts Options, rl *rateLimitState) ([]string, error) {
	// Build the user prompt
	var userMsg strings.Builder
	userMsg.WriteString("Translate these entries:\n\n")
	for i, e := range entries {
		userMsg.WriteString(fmt.Sprintf("%d. %s\n", i+1, escapeForPrompt(e.MsgID)))
		if len(e.References) > 0 {
			userMsg.WriteString(fmt.Sprintf("   (context: %s)\n", strings.Join(e.References, ", ")))
		}
	}
	userMsg.WriteString(fmt.Sprintf("\nReturn a JSON array with exactly %d translated strings.", len(entries)))

	text, err := callProvider(ctx, opts.Provider, systemPrompt, userMsg.String(), rl, opts.effectiveMaxRetries(), opts.Verbose)
	if err != nil {
		return nil, err
	}

	return parseTranslations(text, len(entries))
}

// hasPluralEntries reports whether any entry in the slice has a MsgIDPlural.
func hasPluralEntries(entries []*po.Entry) bool {
	for _, e := range entries {
		if e.MsgIDPlural != "" {
			return true
		}
	}
	return false
}

// applyTranslations applies translated strings to PO entries.
func applyTranslations(entries []*po.Entry, translations []string, clearFuzzy bool) {
	for i, entry := range entries {
		if i < len(translations) && translations[i] != "" {
			entry.MsgStr = translations[i]
			if entry.IsFuzzy() && clearFuzzy {
				entry.SetFuzzy(false)
			}
		}
	}
}

// applyPluralTranslations applies plural-aware translations to PO entries.
// For plural entries it fills MsgStrPlural[0..N-1]; for singular entries it
// sets MsgStr as before.
func applyPluralTranslations(entries []*po.Entry, translations []pluralTranslation, clearFuzzy bool) {
	for i, entry := range entries {
		if i >= len(translations) {
			break
		}
		t := translations[i]
		if entry.MsgIDPlural != "" && len(t.plural) > 0 {
			if entry.MsgStrPlural == nil {
				entry.MsgStrPlural = make(map[int]string)
			}
			for j, form := range t.plural {
				if form != "" {
					entry.MsgStrPlural[j] = form
				}
			}
			if entry.IsFuzzy() && clearFuzzy {
				entry.SetFuzzy(false)
			}
		} else if entry.MsgIDPlural == "" && t.singular != "" {
			entry.MsgStr = t.singular
			if entry.IsFuzzy() && clearFuzzy {
				entry.SetFuzzy(false)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Parallelization modes
// ---------------------------------------------------------------------------

// translateSequential processes languages one at a time, chunks sequentially.
func translateSequential(ctx context.Context, tasks []translationTask, opts Options) error {
	var failedLangs []string
	for _, task := range tasks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		taskOpts := opts
		taskOpts.Language = task.lang
		taskOpts.LanguageName = po.LangNameNative(task.lang)

		toTranslate := collectEntries(task.poFile, opts)
		if len(toTranslate) == 0 {
			continue
		}

		opts.log("Translating %s (%s) — %d entries...", task.lang, taskOpts.LanguageName, len(toTranslate))

		if err := Translate(ctx, task.poFile, taskOpts); err != nil {
			if ctx.Err() != nil {
				savePOFile(task.poFile, task.poPath, opts)
				return ctx.Err()
			}
			opts.logError("Error translating %s: %v", task.lang, err)
			failedLangs = append(failedLangs, task.lang)
			continue
		}

		savePOFile(task.poFile, task.poPath, opts)
	}
	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

// translateFullParallel flattens all lang/chunk combinations and runs them all
// up to maxConcurrent simultaneously.
func translateFullParallel(ctx context.Context, tasks []translationTask, opts Options) error {
	rl := &rateLimitState{}

	type flatTask struct {
		lang         string
		chunk        []*po.Entry
		poFile       *po.File
		poPath       string
		systemPrompt string
		total        *int64
		done         *int64
	}

	var flatTasks []flatTask

	for _, task := range tasks {
		taskOpts := opts
		taskOpts.Language = task.lang
		taskOpts.LanguageName = po.LangNameNative(task.lang)

		toTranslate := collectEntries(task.poFile, opts)
		if len(toTranslate) == 0 {
			continue
		}

		chunkSize := opts.effectiveChunkSize()
		if chunkSize == 0 {
			chunkSize = len(toTranslate)
		}
		chunks := splitEntries(toTranslate, chunkSize)

		total := int64(len(toTranslate))
		done := int64(0)
		systemPrompt := taskOpts.resolvedPrompt()

		for _, chunk := range chunks {
			flatTasks = append(flatTasks, flatTask{
				lang:         task.lang,
				chunk:        chunk,
				poFile:       task.poFile,
				poPath:       task.poPath,
				systemPrompt: systemPrompt,
				total:        &total,
				done:         &done,
			})
		}
	}

	if len(flatTasks) == 0 {
		return nil
	}

	// Use a mutex per PO file to protect concurrent writes
	fileMu := make(map[string]*sync.Mutex)
	for _, ft := range flatTasks {
		if _, ok := fileMu[ft.poPath]; !ok {
			fileMu[ft.poPath] = &sync.Mutex{}
		}
	}

	err := runParallelGeneric(ctx, flatTasks, opts.effectiveMaxConcurrent(), opts.RequestDelay, func(ctx context.Context, ft flatTask) error {
		taskOpts := opts
		taskOpts.Language = ft.lang
		taskOpts.LanguageName = po.LangNameNative(ft.lang)

		translations, err := translateChunk(ctx, ft.chunk, ft.systemPrompt, taskOpts, rl)
		if err != nil {
			return err
		}

		mu := fileMu[ft.poPath]
		mu.Lock()
		applyTranslations(ft.chunk, translations, opts.TranslateFuzzy)
		mu.Unlock()

		newDone := atomic.AddInt64(ft.done, int64(len(ft.chunk)))
		if opts.OnProgress != nil {
			opts.OnProgress(ft.lang, int(newDone), int(atomic.LoadInt64(ft.total)))
		}
		return nil
	})

	// Save all PO files
	saved := make(map[string]bool)
	for _, ft := range flatTasks {
		if !saved[ft.poPath] {
			savePOFile(ft.poFile, ft.poPath, opts)
			saved[ft.poPath] = true
		}
	}

	return err
}

// ---------------------------------------------------------------------------
// Generic parallel runner
// ---------------------------------------------------------------------------

// runParallelGeneric runs any typed tasks in parallel with concurrency limit and delay.
func runParallelGeneric[T any](ctx context.Context, tasks []T, maxConcurrent int, delay time.Duration, fn func(context.Context, T) error) error {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}

	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once

	for i, task := range tasks {
		if ctx.Err() != nil {
			break
		}

		// Delay between launching tasks (skip first)
		if i > 0 && delay > 0 {
			select {
			case <-ctx.Done():
				break
			case <-time.After(delay):
			}
		}

		sem <- struct{}{}
		wg.Add(1)

		go func(t T) {
			defer func() {
				<-sem
				wg.Done()
			}()

			if err := fn(ctx, t); err != nil {
				errOnce.Do(func() {
					firstErr = err
				})
			}
		}(task)
	}

	wg.Wait()
	return firstErr
}

// ---------------------------------------------------------------------------
// Public API for multi-language translation from main.go
// ---------------------------------------------------------------------------

// LangTask is a language translation task exposed to main.go.
type LangTask struct {
	Lang   string
	POFile *po.File
	POPath string
}

// TranslateAll translates multiple languages according to opts.ParallelMode.
func TranslateAll(ctx context.Context, langTasks []LangTask, opts Options) error {
	tasks := make([]translationTask, len(langTasks))
	for i, lt := range langTasks {
		tasks[i] = translationTask{
			lang:   lt.Lang,
			poFile: lt.POFile,
			poPath: lt.POPath,
		}

		entries := collectEntries(lt.POFile, opts)
		tasks[i].entries = entries
	}

	return TranslateMulti(ctx, tasks, opts)
}

// ---------------------------------------------------------------------------
// i18next JSON translation support
// ---------------------------------------------------------------------------

// I18NextSystemPrompt is the system prompt for translating i18next JSON UI strings.
const I18NextSystemPrompt = `You are a professional translator specializing in software and product localization. You are translating UI strings for a web application built with React and i18next.

CONTEXT AWARENESS:
- The application is a web application using React and i18next for internationalization
- The audience is web application users
- Keys are natural English text; you translate them to {{targetLang}}
- Tone: professional yet approachable, clear and concise
- Use IT/software terminology that is standard in {{targetLang}} tech community

IMPORTANT TRANSLATION PRINCIPLES:
- Translate for NATURALNESS and FLUENCY in the target language, not word-for-word
- Use idiomatic expressions natural to {{targetLang}}, not literal translations
- Adapt sentence structure to match {{targetLang}} conventions
- Use established IT terminology in {{targetLang}}
- Consider cultural context and target audience expectations
- Maintain the original tone and intent, but express it naturally in {{targetLang}}

TECHNICAL REQUIREMENTS:
- Return ONLY a JSON array of translated strings, one for each input entry, in the same order.
- Preserve all interpolation variables exactly as-is (e.g. {{count}}, {{name}}, etc.).
- Preserve leading/trailing whitespace, newlines, and punctuation patterns.
- Keep brand names and proper nouns unchanged.
- Do NOT translate technical terms that are standard in English (unless they have established translations).
- Return ONLY the JSON array, no explanations or markdown code blocks.`

// RecipeSystemPrompt is the system prompt for translating recipe metadata
// (app names, descriptions, long descriptions).
const RecipeSystemPrompt = `You are a professional translator specializing in software and product localization. You are translating application metadata (names, descriptions) for a software catalog or app store.

CONTEXT AWARENESS:
- These are software application descriptions for an application catalog
- Each entry has a name, short description, and optionally a long HTML description
- The audience is users browsing for software to install
- Tone: clear, informative, matching standard app store descriptions

IMPORTANT TRANSLATION PRINCIPLES:
- Translate for NATURALNESS and FLUENCY in {{targetLang}}, not word-for-word
- Use established IT terminology standard in {{targetLang}}
- Keep the same level of technical detail as the original
- Application names that are proper nouns should NOT be translated
- Short descriptions should remain concise (one line)

TECHNICAL REQUIREMENTS:
- Return ONLY a JSON array of translated strings, one for each input entry, in the same order.
- Preserve ALL HTML tags exactly as-is (<p>, <ul>, <li>, <code>, etc.)
- Preserve all technical terms, command names, and file paths
- Keep brand names and application names unchanged
- Return ONLY the JSON array, no explanations or markdown code blocks.`

// JSONLangTask is a language translation task for i18next JSON files.
type JSONLangTask struct {
	Lang     string
	LangName string
	File     *i18next.File
	FilePath string
}

// RecipeTask is a single recipe translation task.
type RecipeTask struct {
	RecipeID string
	Lang     string
	FilePath string
	Recipe   *i18next.RecipeTranslation
	// Source fields from English (for context)
	SourceName            string
	SourceDescription     string
	SourceLongDescription string
}

// TranslateAllJSON translates multiple i18next JSON files according to opts.ParallelMode.
func TranslateAllJSON(ctx context.Context, langTasks []JSONLangTask, opts Options) error {
	if opts.ParallelMode == ParallelFullParallel {
		return translateJSONFullParallel(ctx, langTasks, opts)
	}
	return translateJSONSequential(ctx, langTasks, opts)
}

// translateJSONSequential translates i18next JSON files one language at a time.
func translateJSONSequential(ctx context.Context, langTasks []JSONLangTask, opts Options) error {
	var failedLangs []string
	for _, task := range langTasks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		taskOpts := opts
		taskOpts.Language = task.Lang
		taskOpts.LanguageName = task.LangName

		// Determine keys to translate
		var keysToTranslate []string
		if opts.RetranslateExisting {
			keysToTranslate = task.File.Keys()
		} else {
			keysToTranslate = task.File.UntranslatedKeys()
		}

		if len(keysToTranslate) == 0 {
			continue
		}

		opts.log("Translating %s (%s) — %d keys...", task.Lang, task.LangName, len(keysToTranslate))

		if err := translateJSONFile(ctx, task.File, keysToTranslate, taskOpts); err != nil {
			if ctx.Err() != nil {
				saveJSONFile(task.File, task.FilePath, opts)
				return ctx.Err()
			}
			opts.logError("Error translating %s: %v", task.Lang, err)
			failedLangs = append(failedLangs, task.Lang)
			continue
		}

		saveJSONFile(task.File, task.FilePath, opts)
	}
	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

// translateJSONFullParallel translates all i18next JSON files in parallel.
func translateJSONFullParallel(ctx context.Context, langTasks []JSONLangTask, opts Options) error {
	rl := &rateLimitState{}

	type flatTask struct {
		lang         string
		langName     string
		keys         []string
		file         *i18next.File
		filePath     string
		systemPrompt string
		total        *int64
		done         *int64
	}

	var flatTasks []flatTask

	for _, task := range langTasks {
		var keysToTranslate []string
		if opts.RetranslateExisting {
			keysToTranslate = task.File.Keys()
		} else {
			keysToTranslate = task.File.UntranslatedKeys()
		}
		if len(keysToTranslate) == 0 {
			continue
		}

		chunkSize := opts.effectiveChunkSize()
		if chunkSize == 0 {
			chunkSize = len(keysToTranslate)
		}
		chunks := splitStrings(keysToTranslate, chunkSize)

		total := int64(len(keysToTranslate))
		done := int64(0)

		taskOpts := opts
		taskOpts.Language = task.Lang
		taskOpts.LanguageName = task.LangName
		systemPrompt := taskOpts.resolvedPrompt()

		for _, chunk := range chunks {
			flatTasks = append(flatTasks, flatTask{
				lang:         task.Lang,
				langName:     task.LangName,
				keys:         chunk,
				file:         task.File,
				filePath:     task.FilePath,
				systemPrompt: systemPrompt,
				total:        &total,
				done:         &done,
			})
		}
	}

	if len(flatTasks) == 0 {
		return nil
	}

	// Mutex per file to protect concurrent writes
	fileMu := make(map[string]*sync.Mutex)
	for _, ft := range flatTasks {
		if _, ok := fileMu[ft.filePath]; !ok {
			fileMu[ft.filePath] = &sync.Mutex{}
		}
	}

	err := runParallelGeneric(ctx, flatTasks, opts.effectiveMaxConcurrent(), opts.RequestDelay, func(ctx context.Context, ft flatTask) error {
		translations, err := translateJSONChunk(ctx, ft.keys, ft.systemPrompt, opts, rl)
		if err != nil {
			return err
		}

		mu := fileMu[ft.filePath]
		mu.Lock()
		applyJSONTranslations(ft.file, ft.keys, translations)
		mu.Unlock()

		newDone := atomic.AddInt64(ft.done, int64(len(ft.keys)))
		if opts.OnProgress != nil {
			opts.OnProgress(ft.lang, int(newDone), int(atomic.LoadInt64(ft.total)))
		}
		return nil
	})

	// Save all files
	saved := make(map[string]bool)
	for _, ft := range flatTasks {
		if !saved[ft.filePath] {
			saveJSONFile(ft.file, ft.filePath, opts)
			saved[ft.filePath] = true
		}
	}

	return err
}

// translateJSONFile translates specific keys in an i18next JSON file.
func translateJSONFile(ctx context.Context, file *i18next.File, keys []string, opts Options) error {
	chunkSize := opts.effectiveChunkSize()
	if chunkSize == 0 {
		chunkSize = len(keys)
	}

	rl := &rateLimitState{}
	chunks := splitStrings(keys, chunkSize)
	systemPrompt := opts.resolvedPrompt()
	done := 0

	for i, chunk := range chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if opts.Verbose {
			opts.log("  Chunk %d/%d (%d keys)", i+1, len(chunks), len(chunk))
		}

		translations, err := translateJSONChunk(ctx, chunk, systemPrompt, opts, rl)
		if err != nil {
			return fmt.Errorf("translating chunk %d/%d: %w", i+1, len(chunks), err)
		}

		applyJSONTranslations(file, chunk, translations)

		done += len(chunk)
		if opts.OnProgress != nil {
			opts.OnProgress(opts.Language, done, len(keys))
		}

		if i < len(chunks)-1 && opts.RequestDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opts.RequestDelay):
			}
		}
	}

	return nil
}

// translateJSONChunk sends a batch of English keys to the AI and gets translations back.
func translateJSONChunk(ctx context.Context, keys []string, systemPrompt string, opts Options, rl *rateLimitState) ([]string, error) {
	var userMsg strings.Builder
	userMsg.WriteString(fmt.Sprintf("Translate these UI strings to %s:\n\n", opts.LanguageName))
	for i, key := range keys {
		userMsg.WriteString(fmt.Sprintf("%d. %s\n", i+1, escapeForPrompt(key)))
	}
	userMsg.WriteString(fmt.Sprintf("\nReturn a JSON array with exactly %d translated strings.", len(keys)))

	text, err := callProvider(ctx, opts.Provider, systemPrompt, userMsg.String(), rl, opts.effectiveMaxRetries(), opts.Verbose)
	if err != nil {
		return nil, err
	}

	return parseTranslations(text, len(keys))
}

// applyJSONTranslations applies translated strings to the i18next file.
func applyJSONTranslations(file *i18next.File, keys []string, translations []string) {
	for i, key := range keys {
		if i < len(translations) && translations[i] != "" {
			file.Translations[key] = translations[i]
		}
	}
}

// saveJSONFile saves an i18next JSON file and logs the result.
func saveJSONFile(file *i18next.File, path string, opts Options) {
	if err := file.WriteFile(path); err != nil {
		opts.logError("Error saving %s: %v", path, err)
	} else {
		total, translated, _ := file.Stats()
		opts.log("Saved %s (%d/%d translated)", path, translated, total)
	}
}

// splitStrings divides a string slice into chunks of the given size.
func splitStrings(items []string, chunkSize int) [][]string {
	if chunkSize <= 0 || chunkSize >= len(items) {
		return [][]string{items}
	}
	var chunks [][]string
	for i := 0; i < len(items); i += chunkSize {
		end := i + chunkSize
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[i:end])
	}
	return chunks
}

// recipeFieldRef identifies a specific field in a specific recipe task.
type recipeFieldRef struct {
	taskIdx int
	field   string // "name", "description", "longDescription"
}

// TranslateRecipes translates recipe metadata files in batch.
// It groups recipe fields into chunks and translates them efficiently.
func TranslateRecipes(ctx context.Context, tasks []RecipeTask, opts Options) error {
	if len(tasks) == 0 {
		return nil
	}

	rl := &rateLimitState{}
	systemPrompt := opts.resolvedPrompt()

	// Flatten all fields that need translation into a single list
	var fields []recipeFieldRef
	var texts []string

	for i, task := range tasks {
		if task.Recipe.Name == "" && task.SourceName != "" {
			fields = append(fields, recipeFieldRef{i, "name"})
			texts = append(texts, task.SourceName)
		}
		if task.Recipe.Description == "" && task.SourceDescription != "" {
			fields = append(fields, recipeFieldRef{i, "description"})
			texts = append(texts, task.SourceDescription)
		}
		if task.Recipe.LongDescription == "" && task.SourceLongDescription != "" {
			fields = append(fields, recipeFieldRef{i, "longDescription"})
			texts = append(texts, task.SourceLongDescription)
		}
	}

	if len(texts) == 0 {
		return nil
	}

	chunkSize := opts.effectiveChunkSize()
	if chunkSize == 0 {
		chunkSize = 50 // Default chunk size for recipes
	}

	chunks := splitStrings(texts, chunkSize)
	fieldChunks := splitFieldRefs(fields, chunkSize)

	done := 0
	var failedChunks int

	for i, chunk := range chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if opts.Verbose {
			opts.log("  Recipe chunk %d/%d (%d fields)", i+1, len(chunks), len(chunk))
		}

		var userMsg strings.Builder
		userMsg.WriteString(fmt.Sprintf("Translate these application descriptions to %s:\n\n", opts.LanguageName))
		for j, text := range chunk {
			userMsg.WriteString(fmt.Sprintf("%d. %s\n", j+1, escapeForPrompt(text)))
		}
		userMsg.WriteString(fmt.Sprintf("\nReturn a JSON array with exactly %d translated strings.", len(chunk)))

		text, err := callProvider(ctx, opts.Provider, systemPrompt, userMsg.String(), rl, opts.effectiveMaxRetries(), opts.Verbose)
		if err != nil {
			opts.logError("Recipe translation chunk %d/%d failed: %v", i+1, len(chunks), err)
			failedChunks++
			continue
		}

		translations, err := parseTranslations(text, len(chunk))
		if err != nil {
			opts.logError("Recipe translation chunk %d/%d: parse error: %v", i+1, len(chunks), err)
			failedChunks++
			continue
		}

		// Apply translations to the correct recipe fields
		for j, ref := range fieldChunks[i] {
			if j < len(translations) && translations[j] != "" {
				switch ref.field {
				case "name":
					tasks[ref.taskIdx].Recipe.Name = translations[j]
				case "description":
					tasks[ref.taskIdx].Recipe.Description = translations[j]
				case "longDescription":
					tasks[ref.taskIdx].Recipe.LongDescription = translations[j]
				}
			}
		}

		done += len(chunk)
		if opts.OnProgress != nil {
			opts.OnProgress(opts.Language, done, len(texts))
		}

		if i < len(chunks)-1 && opts.RequestDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opts.RequestDelay):
			}
		}
	}

	// Save all modified recipe files
	saved := 0
	for _, task := range tasks {
		if task.Recipe.Name != "" || task.Recipe.Description != "" {
			if err := task.Recipe.WriteRecipeFile(task.FilePath); err != nil {
				opts.logError("Error saving %s: %v", task.FilePath, err)
			} else {
				saved++
			}
		}
	}

	if saved > 0 {
		opts.log("Saved %d recipe translation files", saved)
	}

	if failedChunks > 0 {
		return fmt.Errorf("%d recipe translation chunks failed", failedChunks)
	}
	return nil
}

// splitFieldRefs splits a slice of recipeFieldRef into chunks.
func splitFieldRefs(items []recipeFieldRef, chunkSize int) [][]recipeFieldRef {
	if chunkSize <= 0 || chunkSize >= len(items) {
		return [][]recipeFieldRef{items}
	}
	var chunks [][]recipeFieldRef
	for i := 0; i < len(items); i += chunkSize {
		end := i + chunkSize
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[i:end])
	}
	return chunks
}

// ---------------------------------------------------------------------------
// Blog post translation support
// ---------------------------------------------------------------------------

// BlogPostSystemPrompt is the system prompt for translating blog post content.
const BlogPostSystemPrompt = `You are a professional translator specializing in technical blog posts and articles. You are translating blog posts for a software project website.

CONTEXT AWARENESS:
- The audience is technical users interested in software, technology, and project updates
- Blog posts may discuss features, releases, community updates, and technical topics
- Tone: professional yet friendly, matching the original post's voice
- Use IT/software terminology standard in {{targetLang}}

IMPORTANT TRANSLATION PRINCIPLES:
- Translate for NATURALNESS and FLUENCY in {{targetLang}}, not word-for-word
- Use idiomatic expressions natural to {{targetLang}}
- Adapt sentence structure to match {{targetLang}} conventions
- Maintain the original tone, energy, and intent
- Preserve the blog post's personality and style

TECHNICAL REQUIREMENTS:
- Return ONLY a JSON array of translated strings, one for each input entry, in the same order.
- Preserve ALL Markdown formatting exactly as-is: links [text](url), **bold**, *italic*, headers, lists, code blocks
- Preserve all URLs unchanged
- Keep brand names and proper nouns unchanged
- Do NOT translate technical terms that are standard in English (unless they have established translations)
- Return ONLY the JSON array, no explanations or markdown code blocks.`

// BlogPostTask is a single blog post translation task.
type BlogPostTask struct {
	Slug     string
	Lang     string
	FilePath string
	Post     *i18next.BlogPost
	// Source fields from English
	SourceTitle   string
	SourceExcerpt string
	SourceContent string
}

// TranslateBlogPosts translates blog post files.
// Each blog post has 3 translatable fields: title, excerpt, content.
// We send all fields of a single post in one API request for context coherence.
func TranslateBlogPosts(ctx context.Context, tasks []BlogPostTask, opts Options) error {
	if len(tasks) == 0 {
		return nil
	}

	rl := &rateLimitState{}
	var failedPosts int

	for i, task := range tasks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Determine which fields need translation
		var sourceStrings []string
		var fieldNames []string

		if (task.Post.Title == "" || opts.RetranslateExisting) && task.SourceTitle != "" {
			sourceStrings = append(sourceStrings, task.SourceTitle)
			fieldNames = append(fieldNames, "title")
		}
		if (task.Post.Excerpt == "" || opts.RetranslateExisting) && task.SourceExcerpt != "" {
			sourceStrings = append(sourceStrings, task.SourceExcerpt)
			fieldNames = append(fieldNames, "excerpt")
		}
		if (task.Post.Content == "" || opts.RetranslateExisting) && task.SourceContent != "" {
			sourceStrings = append(sourceStrings, task.SourceContent)
			fieldNames = append(fieldNames, "content")
		}

		if len(sourceStrings) == 0 {
			continue
		}

		opts.log("  Blog post %d/%d: %s (%d fields)", i+1, len(tasks), task.Slug, len(sourceStrings))

		// Translate
		translations, err := translateJSONChunk(ctx, sourceStrings, opts.resolvedPrompt(), opts, rl)
		if err != nil {
			opts.logError("Blog post %s translation failed: %v", task.Slug, err)
			failedPosts++
			continue
		}

		if len(translations) != len(sourceStrings) {
			opts.logError("Blog post %s: expected %d translations, got %d", task.Slug, len(sourceStrings), len(translations))
			failedPosts++
			continue
		}

		// Apply translations
		for j, field := range fieldNames {
			switch field {
			case "title":
				task.Post.Title = translations[j]
			case "excerpt":
				task.Post.Excerpt = translations[j]
			case "content":
				task.Post.Content = translations[j]
			}
		}

		// Update timestamp
		task.Post.UpdatedAt = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")

		// Save
		if err := task.Post.WriteBlogPost(task.FilePath); err != nil {
			opts.logError("Error saving blog post %s: %v", task.FilePath, err)
			failedPosts++
		}
	}

	if failedPosts > 0 {
		return fmt.Errorf("%d blog post(s) failed to translate", failedPosts)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// savePOFile saves a PO file and logs the result.
func savePOFile(poFile *po.File, poPath string, opts Options) {
	poFile.SetHeaderField("PO-Revision-Date", time.Now().UTC().Format("2006-01-02 15:04+0000"))
	if err := poFile.WriteFile(poPath); err != nil {
		opts.logError("Error saving %s: %v", poPath, err)
	} else {
		total, translated, _, _ := poFile.Stats()
		opts.log("Saved %s (%d/%d translated)", poPath, translated, total)
	}
}

// escapeForPrompt prepares a string for inclusion in the AI prompt.
func escapeForPrompt(s string) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return fmt.Sprintf(`"%s"`, s)
}

// truncate truncates a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// min returns the smaller of two durations.
func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Android strings.xml translation support
// ---------------------------------------------------------------------------

// AndroidSystemPrompt is the system prompt for translating Android strings.xml UI strings.
const AndroidSystemPrompt = `You are a professional translator specializing in mobile app localization. You are translating UI strings for an Android application.

CONTEXT AWARENESS:
- The audience is mobile app users
- Tone: professional yet approachable, clear and concise
- Use mobile app terminology that is standard in {{targetLang}} tech community
- Adapt to the app's specific domain and target audience based on the source text context

IMPORTANT TRANSLATION PRINCIPLES:
- Translate for NATURALNESS and FLUENCY in the target language, not word-for-word
- Use idiomatic expressions natural to {{targetLang}}, not literal translations
- Adapt sentence structure to match {{targetLang}} conventions
- Use established terminology in {{targetLang}} appropriate for mobile apps
- Consider cultural context and target audience expectations
- Maintain the original tone and intent, but express it naturally in {{targetLang}}
- Follow platform-specific conventions for {{targetLang}} on Android (button labels, menu items, etc.)

TECHNICAL REQUIREMENTS:
- Return ONLY a JSON array of translated strings, one for each input entry, in the same order.
- Preserve all Android format specifiers exactly as-is (%s, %d, %1$s, %1$d, %2$d, %2$s, etc.).
- Preserve leading/trailing whitespace, newlines, and punctuation patterns.
- Keep brand names and proper nouns unchanged.
- Do NOT translate technical terms that are standard in English (unless they have established translations).
- Return ONLY the JSON array, no explanations or markdown code blocks.`

// AndroidLangTask is a language translation task for Android strings.xml files.
type AndroidLangTask struct {
	Lang       string
	LangName   string
	File       *android.File
	FilePath   string
	SourceFile *android.File // Source (English) file for looking up original values
}

// TranslateAllAndroid translates multiple Android strings.xml files according to opts.ParallelMode.
func TranslateAllAndroid(ctx context.Context, langTasks []AndroidLangTask, opts Options) error {
	if opts.ParallelMode == ParallelFullParallel {
		return translateAndroidFullParallel(ctx, langTasks, opts)
	}
	return translateAndroidSequential(ctx, langTasks, opts)
}

// translateAndroidSequential translates Android strings.xml files one language at a time.
func translateAndroidSequential(ctx context.Context, langTasks []AndroidLangTask, opts Options) error {
	var failedLangs []string
	for _, task := range langTasks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		taskOpts := opts
		taskOpts.Language = task.Lang
		taskOpts.LanguageName = task.LangName

		// Determine keys to translate
		var keysToTranslate []string
		if opts.RetranslateExisting {
			keysToTranslate = task.File.Keys()
		} else {
			keysToTranslate = task.File.UntranslatedKeys()
		}

		if len(keysToTranslate) == 0 {
			continue
		}

		opts.log("Translating %s (%s) — %d strings...", task.Lang, task.LangName, len(keysToTranslate))

		if err := translateAndroidFile(ctx, task.File, task.SourceFile, keysToTranslate, taskOpts); err != nil {
			if ctx.Err() != nil {
				saveAndroidFile(task.File, task.FilePath, opts)
				return ctx.Err()
			}
			opts.logError("Error translating %s: %v", task.Lang, err)
			failedLangs = append(failedLangs, task.Lang)
			continue
		}

		saveAndroidFile(task.File, task.FilePath, opts)
	}
	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

// translateAndroidFullParallel translates all Android strings.xml files in parallel.
func translateAndroidFullParallel(ctx context.Context, langTasks []AndroidLangTask, opts Options) error {
	rl := &rateLimitState{}

	type flatTask struct {
		lang         string
		langName     string
		keys         []string
		file         *android.File
		sourceFile   *android.File
		filePath     string
		systemPrompt string
		total        *int64
		done         *int64
	}

	var flatTasks []flatTask

	for _, task := range langTasks {
		var keysToTranslate []string
		if opts.RetranslateExisting {
			keysToTranslate = task.File.Keys()
		} else {
			keysToTranslate = task.File.UntranslatedKeys()
		}
		if len(keysToTranslate) == 0 {
			continue
		}

		chunkSize := opts.effectiveChunkSize()
		if chunkSize == 0 {
			chunkSize = len(keysToTranslate)
		}
		chunks := splitStrings(keysToTranslate, chunkSize)

		total := int64(len(keysToTranslate))
		done := int64(0)

		taskOpts := opts
		taskOpts.Language = task.Lang
		taskOpts.LanguageName = task.LangName
		systemPrompt := taskOpts.resolvedPrompt()

		for _, chunk := range chunks {
			flatTasks = append(flatTasks, flatTask{
				lang:         task.Lang,
				langName:     task.LangName,
				keys:         chunk,
				file:         task.File,
				sourceFile:   task.SourceFile,
				filePath:     task.FilePath,
				systemPrompt: systemPrompt,
				total:        &total,
				done:         &done,
			})
		}
	}

	if len(flatTasks) == 0 {
		return nil
	}

	// Mutex per file to protect concurrent writes
	fileMu := make(map[string]*sync.Mutex)
	for _, ft := range flatTasks {
		if _, ok := fileMu[ft.filePath]; !ok {
			fileMu[ft.filePath] = &sync.Mutex{}
		}
	}

	err := runParallelGeneric(ctx, flatTasks, opts.effectiveMaxConcurrent(), opts.RequestDelay, func(ctx context.Context, ft flatTask) error {
		translations, err := translateAndroidChunk(ctx, ft.keys, ft.sourceFile, ft.systemPrompt, opts, rl)
		if err != nil {
			return err
		}

		mu := fileMu[ft.filePath]
		mu.Lock()
		applyAndroidTranslations(ft.file, ft.keys, translations)
		mu.Unlock()

		newDone := atomic.AddInt64(ft.done, int64(len(ft.keys)))
		if opts.OnProgress != nil {
			opts.OnProgress(ft.lang, int(newDone), int(atomic.LoadInt64(ft.total)))
		}
		return nil
	})

	// Save all files
	saved := make(map[string]bool)
	for _, ft := range flatTasks {
		if !saved[ft.filePath] {
			saveAndroidFile(ft.file, ft.filePath, opts)
			saved[ft.filePath] = true
		}
	}

	return err
}

// translateAndroidFile translates specific keys in an Android strings.xml file.
func translateAndroidFile(ctx context.Context, file *android.File, sourceFile *android.File, keys []string, opts Options) error {
	chunkSize := opts.effectiveChunkSize()
	if chunkSize == 0 {
		chunkSize = len(keys)
	}

	rl := &rateLimitState{}
	chunks := splitStrings(keys, chunkSize)
	systemPrompt := opts.resolvedPrompt()
	done := 0

	for i, chunk := range chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if opts.Verbose {
			opts.log("  Chunk %d/%d (%d strings)", i+1, len(chunks), len(chunk))
		}

		translations, err := translateAndroidChunk(ctx, chunk, sourceFile, systemPrompt, opts, rl)
		if err != nil {
			return fmt.Errorf("translating chunk %d/%d: %w", i+1, len(chunks), err)
		}

		applyAndroidTranslations(file, chunk, translations)

		done += len(chunk)
		if opts.OnProgress != nil {
			opts.OnProgress(opts.Language, done, len(keys))
		}

		if i < len(chunks)-1 && opts.RequestDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opts.RequestDelay):
			}
		}
	}

	return nil
}

// androidTranslationUnit represents a single translatable atom sent to the AI.
// For KindString:      value is the string text.
// For KindStringArray: value is one <item> text; itemIdx is its index in the array.
// For KindPlurals:     value is one quantity form; quantity is its keyword.
type androidTranslationUnit struct {
	key      string // resource name
	kind     android.EntryKind
	value    string // source text
	itemIdx  int    // KindStringArray: index into Items
	quantity string // KindPlurals: quantity keyword
}

// translateAndroidChunk sends a batch of Android resource keys to the AI and
// returns translations. Each key may expand to multiple units (array items,
// plural forms), all sent in a single request.
func translateAndroidChunk(ctx context.Context, keys []string, sourceFile *android.File, systemPrompt string, opts Options, rl *rateLimitState) ([]string, error) {
	// Expand keys → translation units
	units := buildAndroidUnits(keys, sourceFile)
	if len(units) == 0 {
		return nil, nil
	}

	var userMsg strings.Builder
	userMsg.WriteString(fmt.Sprintf("Translate these Android app UI strings to %s:\n\n", opts.LanguageName))
	for i, u := range units {
		if u.value != "" {
			userMsg.WriteString(fmt.Sprintf("%d. %s\n", i+1, escapeForPrompt(u.value)))
		} else {
			userMsg.WriteString(fmt.Sprintf("%d. [%s] (translate based on string resource name)\n", i+1, u.key))
		}
	}
	userMsg.WriteString(fmt.Sprintf("\nReturn a JSON array with exactly %d translated strings.", len(units)))

	text, err := callProvider(ctx, opts.Provider, systemPrompt, userMsg.String(), rl, opts.effectiveMaxRetries(), opts.Verbose)
	if err != nil {
		return nil, err
	}

	return parseTranslations(text, len(units))
}

// buildAndroidUnits expands a list of resource keys into individual translation
// units, one per translatable string atom.
func buildAndroidUnits(keys []string, sourceFile *android.File) []androidTranslationUnit {
	var units []androidTranslationUnit
	for _, key := range keys {
		e := sourceFile.GetEntry(key)
		if e == nil {
			// Key not in source — add a single unit with empty value
			units = append(units, androidTranslationUnit{key: key, kind: android.KindString})
			continue
		}
		switch e.Kind {
		case android.KindString:
			units = append(units, androidTranslationUnit{
				key:   key,
				kind:  android.KindString,
				value: e.Value,
			})
		case android.KindStringArray:
			for idx, item := range e.Items {
				units = append(units, androidTranslationUnit{
					key:     key,
					kind:    android.KindStringArray,
					value:   item,
					itemIdx: idx,
				})
			}
		case android.KindPlurals:
			for _, q := range e.PluralOrder {
				units = append(units, androidTranslationUnit{
					key:      key,
					kind:     android.KindPlurals,
					value:    e.Plurals[q],
					quantity: q,
				})
			}
		}
	}
	return units
}

// applyAndroidTranslations applies a flat slice of translations (one per unit)
// back to the target Android file. Units must be in the same order as returned
// by buildAndroidUnits for the given keys.
func applyAndroidTranslations(file *android.File, keys []string, translations []string) {
	units := buildAndroidUnits(keys, file) // use target file to know structure

	// Build a temporary accumulator for array/plural results
	type arrayAcc struct {
		items []string
	}
	type pluralAcc struct {
		forms map[string]string
	}
	arrays := map[string]*arrayAcc{}
	plurals := map[string]*pluralAcc{}

	// Pre-populate accumulators with current values so partial translations
	// don't wipe already-translated forms.
	for _, key := range keys {
		e := file.GetEntry(key)
		if e == nil {
			continue
		}
		switch e.Kind {
		case android.KindStringArray:
			acc := &arrayAcc{items: make([]string, len(e.Items))}
			copy(acc.items, e.Items)
			arrays[key] = acc
		case android.KindPlurals:
			acc := &pluralAcc{forms: make(map[string]string)}
			for q, v := range e.Plurals {
				acc.forms[q] = v
			}
			plurals[key] = acc
		}
	}

	for i, u := range units {
		if i >= len(translations) || translations[i] == "" {
			continue
		}
		tr := translations[i]
		switch u.kind {
		case android.KindString:
			file.Set(u.key, tr)
		case android.KindStringArray:
			acc, ok := arrays[u.key]
			if !ok {
				continue
			}
			if u.itemIdx < len(acc.items) {
				acc.items[u.itemIdx] = tr
			}
		case android.KindPlurals:
			acc, ok := plurals[u.key]
			if !ok {
				continue
			}
			acc.forms[u.quantity] = tr
		}
	}

	// Flush accumulators back to file
	for key, acc := range arrays {
		file.SetItems(key, acc.items)
	}
	for key, acc := range plurals {
		file.SetPlurals(key, acc.forms)
	}
}

// saveAndroidFile saves an Android strings.xml file and logs the result.
func saveAndroidFile(file *android.File, path string, opts Options) {
	// Translated locale files omit translatable="false" resources — Android
	// inherits them from the default values/strings.xml automatically.
	if err := file.WriteTargetFile(path); err != nil {
		opts.logError("Error saving %s: %v", path, err)
	} else {
		total, translated, _ := file.Stats()
		opts.log("Saved %s (%d/%d translated)", path, translated, total)
	}
}

// ---------------------------------------------------------------------------
// YAML translation
// ---------------------------------------------------------------------------

// YAMLLangTask holds a single YAML language file ready for translation.
type YAMLLangTask struct {
	// Lang is the BCP-47 language code (e.g. "ru", "de").
	Lang string
	// LangName is the human-readable language name (e.g. "Russian").
	LangName string
	// FilePath is the absolute path to write the translated file.
	FilePath string
	// File is the target YAML file (already synced from source).
	File *yamlfile.File
	// SourceFile is the source (English) YAML file.
	SourceFile *yamlfile.File
}

// TranslateAllYAML translates YAML files for all language tasks.
func TranslateAllYAML(ctx context.Context, langTasks []YAMLLangTask, opts Options) error {
	if opts.ParallelMode == ParallelFullParallel {
		return translateYAMLFullParallel(ctx, langTasks, opts)
	}
	return translateYAMLSequential(ctx, langTasks, opts)
}

// translateYAMLSequential translates YAML files one language at a time.
func translateYAMLSequential(ctx context.Context, langTasks []YAMLLangTask, opts Options) error {
	var failedLangs []string
	for _, task := range langTasks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		taskOpts := opts
		taskOpts.Language = task.Lang
		taskOpts.LanguageName = task.LangName

		var keysToTranslate []string
		if opts.RetranslateExisting {
			keysToTranslate = task.File.Keys()
		} else {
			keysToTranslate = task.File.UntranslatedKeys()
		}

		if len(keysToTranslate) == 0 {
			continue
		}

		opts.log("Translating %s (%s) — %d keys...", task.Lang, task.LangName, len(keysToTranslate))

		if err := translateYAMLFile(ctx, task.File, task.SourceFile, keysToTranslate, taskOpts); err != nil {
			if ctx.Err() != nil {
				saveYAMLFile(task.File, task.FilePath, opts)
				return ctx.Err()
			}
			opts.logError("Error translating %s: %v", task.Lang, err)
			failedLangs = append(failedLangs, task.Lang)
			continue
		}

		saveYAMLFile(task.File, task.FilePath, opts)
	}
	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

// translateYAMLFullParallel translates all YAML files in parallel.
func translateYAMLFullParallel(ctx context.Context, langTasks []YAMLLangTask, opts Options) error {
	rl := &rateLimitState{}

	type flatTask struct {
		lang     string
		langName string
		key      string
		filePath string
		file     *yamlfile.File
		srcFile  *yamlfile.File
	}

	// Build flat list of per-language tasks.
	var tasks []flatTask
	for _, lt := range langTasks {
		var keys []string
		if opts.RetranslateExisting {
			keys = lt.File.Keys()
		} else {
			keys = lt.File.UntranslatedKeys()
		}
		if len(keys) > 0 {
			tasks = append(tasks, flatTask{
				lang:     lt.Lang,
				langName: lt.LangName,
				filePath: lt.FilePath,
				file:     lt.File,
				srcFile:  lt.SourceFile,
			})
		}
	}

	if len(tasks) == 0 {
		return nil
	}

	maxConcurrent := opts.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = len(tasks)
	}

	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var failedMu sync.Mutex
	var failedLangs []string

	for _, t := range tasks {
		t := t
		var keys []string
		if opts.RetranslateExisting {
			keys = t.file.Keys()
		} else {
			keys = t.file.UntranslatedKeys()
		}
		if len(keys) == 0 {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				return
			default:
			}

			taskOpts := opts
			taskOpts.Language = t.lang
			taskOpts.LanguageName = t.langName

			opts.log("Translating %s (%s) — %d keys...", t.lang, t.langName, len(keys))
			if err := translateYAMLFileWithRL(ctx, t.file, t.srcFile, keys, taskOpts, rl); err != nil {
				if ctx.Err() == nil {
					opts.logError("Error translating %s: %v", t.lang, err)
					failedMu.Lock()
					failedLangs = append(failedLangs, t.lang)
					failedMu.Unlock()
				}
			} else {
				saveYAMLFile(t.file, t.filePath, opts)
			}
		}()
	}
	wg.Wait()

	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

// translateYAMLFile translates a YAML file using a new rate-limit state.
func translateYAMLFile(ctx context.Context, file *yamlfile.File, srcFile *yamlfile.File, keys []string, opts Options) error {
	rl := &rateLimitState{}
	return translateYAMLFileWithRL(ctx, file, srcFile, keys, opts, rl)
}

// translateYAMLFileWithRL translates a YAML file using a shared rate-limit state.
func translateYAMLFileWithRL(ctx context.Context, file *yamlfile.File, srcFile *yamlfile.File, keys []string, opts Options, rl *rateLimitState) error {
	chunkSize := opts.effectiveChunkSize()
	if chunkSize <= 0 {
		chunkSize = len(keys)
	}

	srcVals := srcFile.SourceValues()
	systemPrompt := opts.resolvedPrompt()
	chunks := splitStrings(keys, chunkSize)
	done := 0

	for i, chunk := range chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if opts.Verbose {
			opts.log("  Chunk %d/%d (%d keys)", i+1, len(chunks), len(chunk))
		}

		translations, err := translateYAMLChunk(ctx, chunk, srcVals, systemPrompt, opts, rl)
		if err != nil {
			return fmt.Errorf("translating chunk %d/%d: %w", i+1, len(chunks), err)
		}

		applyYAMLTranslations(file, chunk, translations)

		done += len(chunk)
		if opts.OnProgress != nil {
			opts.OnProgress(opts.Language, done, len(keys))
		}

		if i < len(chunks)-1 && opts.RequestDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opts.RequestDelay):
			}
		}
	}

	return nil
}

// translateYAMLChunk sends a batch of YAML keys with source values to the AI.
func translateYAMLChunk(ctx context.Context, keys []string, srcVals map[string]string, systemPrompt string, opts Options, rl *rateLimitState) ([]string, error) {
	var userMsg strings.Builder
	userMsg.WriteString(fmt.Sprintf("Translate these strings to %s:\n\n", opts.LanguageName))
	for i, key := range keys {
		src := srcVals[key]
		if src == "" {
			src = key
		}
		userMsg.WriteString(fmt.Sprintf("%d. %s\n", i+1, escapeForPrompt(src)))
	}
	userMsg.WriteString(fmt.Sprintf("\nReturn a JSON array with exactly %d translated strings.", len(keys)))

	text, err := callProvider(ctx, opts.Provider, systemPrompt, userMsg.String(), rl, opts.effectiveMaxRetries(), opts.Verbose)
	if err != nil {
		return nil, err
	}

	return parseTranslations(text, len(keys))
}

// applyYAMLTranslations writes translated values back to the YAML file.
func applyYAMLTranslations(file *yamlfile.File, keys []string, translations []string) {
	for i, key := range keys {
		if i < len(translations) && translations[i] != "" {
			file.Set(key, translations[i])
		}
	}
}

// saveYAMLFile saves a YAML file and logs the result.
func saveYAMLFile(file *yamlfile.File, path string, opts Options) {
	if err := file.WriteFile(path); err != nil {
		opts.logError("Error saving %s: %v", path, err)
	} else {
		total, translated, _ := file.Stats()
		opts.log("Saved %s (%d/%d translated)", path, translated, total)
	}
}

// ---------------------------------------------------------------------------
// Markdown translation
// ---------------------------------------------------------------------------

// MarkdownLangTask holds a single Markdown language task ready for translation.
type MarkdownLangTask struct {
	// Lang is the BCP-47 language code.
	Lang string
	// LangName is the human-readable language name.
	LangName string
	// FilePath is the absolute path to write the translated file.
	FilePath string
	// File is the target Markdown file (synced from source).
	File *mdfile.File
	// SourceFile is the source Markdown file.
	SourceFile *mdfile.File
}

// TranslateAllMarkdown translates Markdown files for all language tasks.
func TranslateAllMarkdown(ctx context.Context, langTasks []MarkdownLangTask, opts Options) error {
	if opts.ParallelMode == ParallelFullParallel {
		return translateMarkdownFullParallel(ctx, langTasks, opts)
	}
	return translateMarkdownSequential(ctx, langTasks, opts)
}

// translateMarkdownSequential translates Markdown files one language at a time.
func translateMarkdownSequential(ctx context.Context, langTasks []MarkdownLangTask, opts Options) error {
	var failedLangs []string
	for _, task := range langTasks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		taskOpts := opts
		taskOpts.Language = task.Lang
		taskOpts.LanguageName = task.LangName

		var keysToTranslate []string
		if opts.RetranslateExisting {
			keysToTranslate = task.File.Keys()
		} else {
			keysToTranslate = task.File.UntranslatedKeys()
		}

		if len(keysToTranslate) == 0 {
			continue
		}

		opts.log("Translating %s (%s) — %d segments...", task.Lang, task.LangName, len(keysToTranslate))

		if err := translateMarkdownFile(ctx, task.File, task.SourceFile, keysToTranslate, taskOpts); err != nil {
			if ctx.Err() != nil {
				saveMarkdownFile(task.File, task.FilePath, opts)
				return ctx.Err()
			}
			opts.logError("Error translating %s: %v", task.Lang, err)
			failedLangs = append(failedLangs, task.Lang)
			continue
		}

		saveMarkdownFile(task.File, task.FilePath, opts)
	}
	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

// translateMarkdownFullParallel translates all Markdown files in parallel.
func translateMarkdownFullParallel(ctx context.Context, langTasks []MarkdownLangTask, opts Options) error {
	type flatTask struct {
		lang     string
		langName string
		filePath string
		file     *mdfile.File
		srcFile  *mdfile.File
	}

	var tasks []flatTask
	for _, lt := range langTasks {
		var keys []string
		if opts.RetranslateExisting {
			keys = lt.File.Keys()
		} else {
			keys = lt.File.UntranslatedKeys()
		}
		if len(keys) > 0 {
			tasks = append(tasks, flatTask{
				lang:     lt.Lang,
				langName: lt.LangName,
				filePath: lt.FilePath,
				file:     lt.File,
				srcFile:  lt.SourceFile,
			})
		}
	}

	if len(tasks) == 0 {
		return nil
	}

	maxConcurrent := opts.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = len(tasks)
	}

	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var failedMu sync.Mutex
	var failedLangs []string
	rl := &rateLimitState{}

	for _, t := range tasks {
		t := t
		var keys []string
		if opts.RetranslateExisting {
			keys = t.file.Keys()
		} else {
			keys = t.file.UntranslatedKeys()
		}
		if len(keys) == 0 {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				return
			default:
			}

			taskOpts := opts
			taskOpts.Language = t.lang
			taskOpts.LanguageName = t.langName

			opts.log("Translating %s (%s) — %d segments...", t.lang, t.langName, len(keys))
			if err := translateMarkdownFileWithRL(ctx, t.file, t.srcFile, keys, taskOpts, rl); err != nil {
				if ctx.Err() == nil {
					opts.logError("Error translating %s: %v", t.lang, err)
					failedMu.Lock()
					failedLangs = append(failedLangs, t.lang)
					failedMu.Unlock()
				}
			} else {
				saveMarkdownFile(t.file, t.filePath, opts)
			}
		}()
	}
	wg.Wait()

	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

// translateMarkdownFile translates a Markdown file.
func translateMarkdownFile(ctx context.Context, file *mdfile.File, srcFile *mdfile.File, keys []string, opts Options) error {
	rl := &rateLimitState{}
	return translateMarkdownFileWithRL(ctx, file, srcFile, keys, opts, rl)
}

// translateMarkdownFileWithRL translates a Markdown file with a shared rate-limit state.
func translateMarkdownFileWithRL(ctx context.Context, file *mdfile.File, srcFile *mdfile.File, keys []string, opts Options, rl *rateLimitState) error {
	// Markdown segments can be large, so translate one at a time by default.
	// Each segment is a complete heading+body block that should be translated atomically.
	chunkSize := opts.effectiveChunkSize()
	if chunkSize <= 0 {
		chunkSize = 1
	}

	srcVals := srcFile.SourceValues()
	systemPrompt := opts.resolvedPrompt()
	chunks := splitStrings(keys, chunkSize)
	done := 0

	for i, chunk := range chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if opts.Verbose {
			opts.log("  Chunk %d/%d (%d segments)", i+1, len(chunks), len(chunk))
		}

		translations, err := translateMarkdownChunk(ctx, chunk, srcVals, systemPrompt, opts, rl)
		if err != nil {
			return fmt.Errorf("translating chunk %d/%d: %w", i+1, len(chunks), err)
		}

		applyMarkdownTranslations(file, chunk, translations)

		done += len(chunk)
		if opts.OnProgress != nil {
			opts.OnProgress(opts.Language, done, len(keys))
		}

		if i < len(chunks)-1 && opts.RequestDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opts.RequestDelay):
			}
		}
	}

	return nil
}

// translateMarkdownChunk sends Markdown segments to the AI for translation.
// Each segment is a complete section (heading + body) or a frontmatter field.
func translateMarkdownChunk(ctx context.Context, keys []string, srcVals map[string]string, systemPrompt string, opts Options, rl *rateLimitState) ([]string, error) {
	var userMsg strings.Builder
	userMsg.WriteString(fmt.Sprintf("Translate these text segments to %s.\n", opts.LanguageName))
	userMsg.WriteString("For Markdown segments, preserve all formatting, headings, code blocks, and inline markup.\n")
	userMsg.WriteString("Return a JSON array with exactly the same number of translated strings.\n\n")
	for i, key := range keys {
		src := srcVals[key]
		if src == "" {
			src = key
		}
		userMsg.WriteString(fmt.Sprintf("%d. %s\n", i+1, escapeForPrompt(src)))
	}
	userMsg.WriteString(fmt.Sprintf("\nReturn a JSON array with exactly %d translated strings.", len(keys)))

	text, err := callProvider(ctx, opts.Provider, systemPrompt, userMsg.String(), rl, opts.effectiveMaxRetries(), opts.Verbose)
	if err != nil {
		return nil, err
	}

	return parseTranslations(text, len(keys))
}

// applyMarkdownTranslations writes translated segments back to the Markdown file.
func applyMarkdownTranslations(file *mdfile.File, keys []string, translations []string) {
	for i, key := range keys {
		if i < len(translations) && translations[i] != "" {
			file.Set(key, translations[i])
		}
	}
}

// saveMarkdownFile saves a Markdown file and logs the result.
func saveMarkdownFile(file *mdfile.File, path string, opts Options) {
	if err := file.WriteFile(path); err != nil {
		opts.logError("Error saving %s: %v", path, err)
	} else {
		total, translated, _ := file.Stats()
		opts.log("Saved %s (%d/%d translated)", path, translated, total)
	}
}

// ---------------------------------------------------------------------------
// Properties (.properties) translation
// ---------------------------------------------------------------------------

// PropertiesLangTask holds a single .properties language file ready for translation.
type PropertiesLangTask struct {
	// Lang is the BCP-47 language code.
	Lang string
	// LangName is the human-readable language name.
	LangName string
	// FilePath is the absolute path to write the translated file.
	FilePath string
	// File is the target .properties file (synced from source).
	File *propfile.File
	// SourceFile is the source .properties file.
	SourceFile *propfile.File
}

// TranslateAllProperties translates .properties files for all language tasks.
func TranslateAllProperties(ctx context.Context, langTasks []PropertiesLangTask, opts Options) error {
	if opts.ParallelMode == ParallelFullParallel {
		return translatePropertiesFullParallel(ctx, langTasks, opts)
	}
	return translatePropertiesSequential(ctx, langTasks, opts)
}

// translatePropertiesSequential translates .properties files one language at a time.
func translatePropertiesSequential(ctx context.Context, langTasks []PropertiesLangTask, opts Options) error {
	var failedLangs []string
	for _, task := range langTasks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		taskOpts := opts
		taskOpts.Language = task.Lang
		taskOpts.LanguageName = task.LangName

		var keysToTranslate []string
		if opts.RetranslateExisting {
			keysToTranslate = task.File.Keys()
		} else {
			keysToTranslate = task.File.UntranslatedKeys()
		}

		if len(keysToTranslate) == 0 {
			continue
		}

		opts.log("Translating %s (%s) — %d keys...", task.Lang, task.LangName, len(keysToTranslate))

		if err := translatePropertiesFile(ctx, task.File, task.SourceFile, keysToTranslate, taskOpts); err != nil {
			if ctx.Err() != nil {
				savePropertiesFile(task.File, task.FilePath, opts)
				return ctx.Err()
			}
			opts.logError("Error translating %s: %v", task.Lang, err)
			failedLangs = append(failedLangs, task.Lang)
			continue
		}

		savePropertiesFile(task.File, task.FilePath, opts)
	}
	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

// translatePropertiesFullParallel translates all .properties files in parallel.
func translatePropertiesFullParallel(ctx context.Context, langTasks []PropertiesLangTask, opts Options) error {
	rl := &rateLimitState{}

	type flatTask struct {
		lang     string
		langName string
		filePath string
		file     *propfile.File
		srcFile  *propfile.File
	}

	var tasks []flatTask
	for _, lt := range langTasks {
		var keys []string
		if opts.RetranslateExisting {
			keys = lt.File.Keys()
		} else {
			keys = lt.File.UntranslatedKeys()
		}
		if len(keys) > 0 {
			tasks = append(tasks, flatTask{
				lang:     lt.Lang,
				langName: lt.LangName,
				filePath: lt.FilePath,
				file:     lt.File,
				srcFile:  lt.SourceFile,
			})
		}
	}

	if len(tasks) == 0 {
		return nil
	}

	maxConcurrent := opts.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = len(tasks)
	}

	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var failedMu sync.Mutex
	var failedLangs []string

	for _, t := range tasks {
		t := t
		var keys []string
		if opts.RetranslateExisting {
			keys = t.file.Keys()
		} else {
			keys = t.file.UntranslatedKeys()
		}
		if len(keys) == 0 {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				return
			default:
			}

			taskOpts := opts
			taskOpts.Language = t.lang
			taskOpts.LanguageName = t.langName

			opts.log("Translating %s (%s) — %d keys...", t.lang, t.langName, len(keys))
			if err := translatePropertiesFileWithRL(ctx, t.file, t.srcFile, keys, taskOpts, rl); err != nil {
				if ctx.Err() == nil {
					opts.logError("Error translating %s: %v", t.lang, err)
					failedMu.Lock()
					failedLangs = append(failedLangs, t.lang)
					failedMu.Unlock()
				}
			} else {
				savePropertiesFile(t.file, t.filePath, opts)
			}
		}()
	}
	wg.Wait()

	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

// translatePropertiesFile translates a .properties file using a new rate-limit state.
func translatePropertiesFile(ctx context.Context, file *propfile.File, srcFile *propfile.File, keys []string, opts Options) error {
	rl := &rateLimitState{}
	return translatePropertiesFileWithRL(ctx, file, srcFile, keys, opts, rl)
}

// translatePropertiesFileWithRL translates a .properties file using a shared rate-limit state.
func translatePropertiesFileWithRL(ctx context.Context, file *propfile.File, srcFile *propfile.File, keys []string, opts Options, rl *rateLimitState) error {
	chunkSize := opts.effectiveChunkSize()
	if chunkSize <= 0 {
		chunkSize = len(keys)
	}

	srcVals := srcFile.SourceValues()
	systemPrompt := opts.resolvedPrompt()
	chunks := splitStrings(keys, chunkSize)
	done := 0

	for i, chunk := range chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if opts.Verbose {
			opts.log("  Chunk %d/%d (%d keys)", i+1, len(chunks), len(chunk))
		}

		translations, err := translateYAMLChunk(ctx, chunk, srcVals, systemPrompt, opts, rl)
		if err != nil {
			return fmt.Errorf("translating chunk %d/%d: %w", i+1, len(chunks), err)
		}

		applyPropertiesTranslations(file, chunk, translations)

		done += len(chunk)
		if opts.OnProgress != nil {
			opts.OnProgress(opts.Language, done, len(keys))
		}

		if i < len(chunks)-1 && opts.RequestDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opts.RequestDelay):
			}
		}
	}

	return nil
}

// applyPropertiesTranslations writes translated values back to the .properties file.
func applyPropertiesTranslations(file *propfile.File, keys []string, translations []string) {
	for i, key := range keys {
		if i < len(translations) && translations[i] != "" {
			file.Set(key, translations[i])
		}
	}
}

// savePropertiesFile saves a .properties file and logs the result.
func savePropertiesFile(file *propfile.File, path string, opts Options) {
	if err := file.WriteFile(path); err != nil {
		opts.logError("Error saving %s: %v", path, err)
	} else {
		total, translated, _ := file.Stats()
		opts.log("Saved %s (%d/%d translated)", path, translated, total)
	}
}

// ---------------------------------------------------------------------------
// Flutter ARB translation
// ---------------------------------------------------------------------------

// ARBLangTask holds a single ARB language file ready for translation.
type ARBLangTask struct {
	// Lang is the BCP-47 language code.
	Lang string
	// LangName is the human-readable language name.
	LangName string
	// FilePath is the absolute path to write the translated file.
	FilePath string
	// File is the target ARB file (synced from source).
	File *arbfile.File
	// SourceFile is the source ARB file.
	SourceFile *arbfile.File
}

// TranslateAllARB translates ARB files for all language tasks.
func TranslateAllARB(ctx context.Context, langTasks []ARBLangTask, opts Options) error {
	if opts.ParallelMode == ParallelFullParallel {
		return translateARBFullParallel(ctx, langTasks, opts)
	}
	return translateARBSequential(ctx, langTasks, opts)
}

// translateARBSequential translates ARB files one language at a time.
func translateARBSequential(ctx context.Context, langTasks []ARBLangTask, opts Options) error {
	var failedLangs []string
	for _, task := range langTasks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		taskOpts := opts
		taskOpts.Language = task.Lang
		taskOpts.LanguageName = task.LangName

		var keysToTranslate []string
		if opts.RetranslateExisting {
			keysToTranslate = task.File.Keys()
		} else {
			keysToTranslate = task.File.UntranslatedKeys()
		}

		if len(keysToTranslate) == 0 {
			continue
		}

		opts.log("Translating %s (%s) — %d keys...", task.Lang, task.LangName, len(keysToTranslate))

		if err := translateARBFile(ctx, task.File, task.SourceFile, keysToTranslate, taskOpts); err != nil {
			if ctx.Err() != nil {
				saveARBFile(task.File, task.FilePath, opts)
				return ctx.Err()
			}
			opts.logError("Error translating %s: %v", task.Lang, err)
			failedLangs = append(failedLangs, task.Lang)
			continue
		}

		saveARBFile(task.File, task.FilePath, opts)
	}
	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

// translateARBFullParallel translates all ARB files in parallel.
func translateARBFullParallel(ctx context.Context, langTasks []ARBLangTask, opts Options) error {
	rl := &rateLimitState{}

	type flatTask struct {
		lang     string
		langName string
		filePath string
		file     *arbfile.File
		srcFile  *arbfile.File
	}

	var tasks []flatTask
	for _, lt := range langTasks {
		var keys []string
		if opts.RetranslateExisting {
			keys = lt.File.Keys()
		} else {
			keys = lt.File.UntranslatedKeys()
		}
		if len(keys) > 0 {
			tasks = append(tasks, flatTask{
				lang:     lt.Lang,
				langName: lt.LangName,
				filePath: lt.FilePath,
				file:     lt.File,
				srcFile:  lt.SourceFile,
			})
		}
	}

	if len(tasks) == 0 {
		return nil
	}

	maxConcurrent := opts.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = len(tasks)
	}

	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var failedMu sync.Mutex
	var failedLangs []string

	for _, t := range tasks {
		t := t
		var keys []string
		if opts.RetranslateExisting {
			keys = t.file.Keys()
		} else {
			keys = t.file.UntranslatedKeys()
		}
		if len(keys) == 0 {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				return
			default:
			}

			taskOpts := opts
			taskOpts.Language = t.lang
			taskOpts.LanguageName = t.langName

			opts.log("Translating %s (%s) — %d keys...", t.lang, t.langName, len(keys))
			if err := translateARBFileWithRL(ctx, t.file, t.srcFile, keys, taskOpts, rl); err != nil {
				if ctx.Err() == nil {
					opts.logError("Error translating %s: %v", t.lang, err)
					failedMu.Lock()
					failedLangs = append(failedLangs, t.lang)
					failedMu.Unlock()
				}
			} else {
				saveARBFile(t.file, t.filePath, opts)
			}
		}()
	}
	wg.Wait()

	if len(failedLangs) > 0 {
		return fmt.Errorf("%d language(s) failed: %s", len(failedLangs), strings.Join(failedLangs, ", "))
	}
	return nil
}

// translateARBFile translates an ARB file using a new rate-limit state.
func translateARBFile(ctx context.Context, file *arbfile.File, srcFile *arbfile.File, keys []string, opts Options) error {
	rl := &rateLimitState{}
	return translateARBFileWithRL(ctx, file, srcFile, keys, opts, rl)
}

// translateARBFileWithRL translates an ARB file using a shared rate-limit state.
func translateARBFileWithRL(ctx context.Context, file *arbfile.File, srcFile *arbfile.File, keys []string, opts Options, rl *rateLimitState) error {
	chunkSize := opts.effectiveChunkSize()
	if chunkSize <= 0 {
		chunkSize = len(keys)
	}

	srcVals := srcFile.SourceValues()
	systemPrompt := opts.resolvedPrompt()
	chunks := splitStrings(keys, chunkSize)
	done := 0

	for i, chunk := range chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if opts.Verbose {
			opts.log("  Chunk %d/%d (%d keys)", i+1, len(chunks), len(chunk))
		}

		translations, err := translateYAMLChunk(ctx, chunk, srcVals, systemPrompt, opts, rl)
		if err != nil {
			return fmt.Errorf("translating chunk %d/%d: %w", i+1, len(chunks), err)
		}

		applyARBTranslations(file, chunk, translations)

		done += len(chunk)
		if opts.OnProgress != nil {
			opts.OnProgress(opts.Language, done, len(keys))
		}

		if i < len(chunks)-1 && opts.RequestDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opts.RequestDelay):
			}
		}
	}

	return nil
}

// applyARBTranslations writes translated values back to the ARB file.
func applyARBTranslations(file *arbfile.File, keys []string, translations []string) {
	for i, key := range keys {
		if i < len(translations) && translations[i] != "" {
			file.Set(key, translations[i])
		}
	}
}

// saveARBFile saves an ARB file and logs the result.
func saveARBFile(file *arbfile.File, path string, opts Options) {
	if err := file.WriteFile(path); err != nil {
		opts.logError("Error saving %s: %v", path, err)
	} else {
		total, translated, _ := file.Stats()
		opts.log("Saved %s (%d/%d translated)", path, translated, total)
	}
}
