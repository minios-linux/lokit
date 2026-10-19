# Providers Guide

lokit supports 9 AI providers for translation. This page covers authentication, environment variables, and usage for each.

## Quick comparison

| Provider | Auth method | Notes | Best for |
|----------|------------|-------|----------|
| GitHub Copilot | OAuth (device code) | Model availability depends on account, plan, and current GitHub rollout | Getting started, general use |
| Gemini CLI (OAuth) | OAuth (browser) | Requires a GCP project ID; quotas depend on plan | OAuth setup; good default |
| Google AI (Gemini) | API key | Paid API (limited free quota may apply) | API-based access to Gemini models |
| Groq | API key | Paid API (free plan limits may apply) | Fast inference |
| OpenCode Console | API key | Pay-as-you-go (some models may be $0) | One key for multiple model families |
| OpenCode Go | API key | Go subscription and separate endpoints | Go models on your subscription |
| OpenAI | Browser OAuth, device code, or API key | ChatGPT auth or official API key | Official OpenAI access |
| Ollama | None (local) | Local compute (free) | Privacy, offline use |
| Custom OpenAI | API key | Depends on endpoint | Any OpenAI-compatible endpoint |

Pricing, quotas, and eligibility can change. Always refer to the provider’s official docs:

- [GitHub Copilot plans](https://docs.github.com/en/copilot/get-started/plans)
- [Gemini Code Assist pricing](https://cloud.google.com/products/gemini/pricing)
- [Gemini Code Assist quotas](https://developers.google.com/gemini-code-assist/resources/quotas)
- [Gemini Code Assist available locations](https://developers.google.com/gemini-code-assist/resources/available-locations)
- [Gemini API pricing (Google AI Studio)](https://ai.google.dev/gemini-api/docs/pricing)
- [Groq rate limits](https://console.groq.com/docs/rate-limits)
- [Groq pricing](https://groq.com/pricing)
- [OpenAI API pricing](https://platform.openai.com/docs/pricing)
- [OpenCode Console models](https://opencode.ai/console/models)
- [OpenCode Go](https://opencode.ai/console/go)

## Credential lookup order

When you run `lokit translate`, credentials are resolved in this order (first match wins):

1. `--api-key` flag on the command line
2. Provider-specific environment variable (see table below)
3. Stored credentials in `~/.local/share/lokit/auth.json`

## Storage

Credentials are stored in `~/.local/share/lokit/auth.json` (permissions: `0600`). The path respects `$XDG_DATA_HOME`.

---

## GitHub Copilot

Uses OAuth device-code flow. Availability and model names depend on your Copilot plan, account eligibility, and GitHub's current rollout.
The provider ID is `github-copilot`, matching OpenCode. The previous `copilot` ID remains accepted for existing configurations and scripts.

**Auth:**
```bash
lokit auth login --provider github-copilot
```

This prints a code and opens your browser. Confirm the code on github.com to complete authentication.

**Usage:**
```bash
lokit translate --provider github-copilot --model MODEL_NAME
```

**Pricing/models:** See GitHub Copilot plans and model comparison. GitHub can change which models are available for a given plan.

**Config shortcut:**
```yaml
provider:
  id: github-copilot
  model: MODEL_NAME
```

---

## Gemini CLI (OAuth)

Uses OAuth browser flow (same authorization used by the Gemini CLI) and calls the Code Assist API under the hood.
This provider requires a GCP project ID to be configured during login.

**Auth:**
```bash
lokit auth login --provider gemini
```

Opens your browser for Google OAuth consent.

**Usage:**
```bash
lokit translate --provider gemini --model MODEL_NAME
```

**Pricing/quotas:** See Gemini Code Assist pricing and quotas.

---

## Google AI (Gemini)

API key access to Google's Gemini models.

**Auth:**
```bash
# Option 1: environment variable
export GOOGLE_API_KEY=your-key-here

# Option 2: store credential
lokit auth login --provider google
```

**Usage:**
```bash
lokit translate --provider google --model MODEL_NAME
```

**Pricing:** Paid API; see Gemini API pricing (Google AI Studio).

**Environment variable:** `GOOGLE_API_KEY`

---

## Groq

Fast inference API. Groq offers paid usage; free plan limits may apply depending on your account.

**Auth:**
```bash
# Option 1: environment variable
export GROQ_API_KEY=your-key-here

# Option 2: store credential
lokit auth login --provider groq
```

**Usage:**
```bash
lokit translate --provider groq --model MODEL_NAME
```

**Pricing/limits:** See Groq pricing and rate limits.

**Environment variable:** `GROQ_API_KEY`

---

## OpenCode Console (`opencode`)

OpenCode Console pay-as-you-go provider (previously called Zen). The existing `opencode` integration uses the legacy Zen endpoint (`https://opencode.ai/zen/v1`); `opencode-go` is a separate provider using the Go endpoints.

**Auth:**
```bash
# Option 1: environment variable
export OPENCODE_API_KEY=your-key-here

# Option 2: store credential
lokit auth login --provider opencode
```

**Usage:**
```bash
lokit translate --provider opencode --model <model>
```

**Pricing/models:** See OpenCode Console models and pricing.

**Environment variable:** `OPENCODE_API_KEY`

---

## OpenCode Go (`opencode-go`)

Uses the separate OpenCode Go subscription endpoints. Sign up for Go or Go Plus and copy your Go API key from the [OpenCode Console](https://opencode.ai/console). In Lokit, `opencode` is Console and `opencode-go` is Go. Credentials are stored separately; no key is reused between them automatically.

```bash
lokit auth login --provider opencode-go
# Or set OPENCODE_GO_API_KEY / pass --api-key.
lokit translate --provider opencode-go --model glm-5.3-flash
```

In `lokit.yaml`:

```yaml
provider:
  id: opencode-go
  model: glm-5.3-flash
```

The provider uses the [documented Go endpoints](https://opencode.ai/console/go#endpoints) for chat, Responses, Anthropic and Gemini models and sends an `x-opencode-session` header. Only model IDs with a verified endpoint mapping are accepted. Update Lokit when Go adds a new model or changes its wire protocol.

OpenCode Go is designed primarily for coding-agent traffic; verify that your translation workload is suitable for your subscription. Without a Go API key, Lokit can check routing locally but cannot verify account access or translation quality.

**Environment variable:** `OPENCODE_GO_API_KEY`

---

## OpenAI

Official OpenAI provider. Supports:

- ChatGPT browser OAuth
- ChatGPT device code flow
- OpenAI API key (`https://api.openai.com/v1`)

**Auth:**
```bash
# Option 1: interactive method selection
lokit auth login --provider openai

# Option 2: browser OAuth
lokit auth login --provider openai --auth-method oauth

# Option 3: device code
lokit auth login --provider openai --auth-method device

# Shortcut for device code
lokit auth login --provider openai --headless

# Option 4: environment variable
export OPENAI_API_KEY=your-key-here

# Option 5: store API key
lokit auth login --provider openai --auth-method api-key
```

**Usage:**
```bash
lokit translate --provider openai --model MODEL_NAME
```

OAuth/device auth uses the ChatGPT Codex Responses endpoint. Discover the models
visible to your current account rather than guessing a model name:

```bash
lokit models --provider openai
lokit models --provider openai --json
```

Lokit obtains the latest stable Codex version from the official
`openai/codex` GitHub release and uses it for the OAuth catalog and Responses
requests, because an older client version can hide newer models. A successful
version lookup is shared across workers in that process. GitHub receives no
OpenAI credentials. If lookup fails, Lokit reports the error instead of guessing
a version or falling back to a review model. API-key authentication
instead lists models from the public OpenAI API. Model availability depends on
the account and endpoint; a listed model is not necessarily suitable for
translation. Select a general text model deliberately. Lokit sends the exact
requested model name, without prefix allowlists or automatic model substitution.
An unavailable model is reported by the server; catalog failure does not silently
select a review model or block an explicitly selected model.

Translation workers never open a browser to log in. If credentials are missing or
cannot be refreshed, run `lokit auth login --provider openai` explicitly. Token
refreshes are serialized so parallel workers do not rotate the same token at once.
The `openai` provider uses fixed official endpoints; use `custom-openai` for custom endpoints.

**Config:**
```yaml
provider:
  id: openai
  model: MODEL_NAME
```

**Environment variable:** `OPENAI_API_KEY`

---

## Ollama

Runs models locally. No API key needed. Requires [Ollama](https://ollama.com/) installed and running.

**Auth:** None required.

**Usage:**
```bash
# Make sure Ollama is running with a model pulled
ollama pull MODEL_NAME

lokit translate --provider ollama --model MODEL_NAME
```

**Custom endpoint:**
```bash
lokit translate --provider ollama --model MODEL_NAME --base-url http://192.168.1.100:11434
```

**Config:**
```yaml
provider:
  id: ollama
  model: MODEL_NAME
  base_url: http://localhost:11434   # default, can be omitted
```

---

## Custom OpenAI

Any non-default OpenAI-compatible API endpoint (for example Azure OpenAI, LM Studio, vLLM, text-generation-webui, or a self-hosted proxy).

**Auth:**
```bash
# Option 1: environment variable
export CUSTOM_OPENAI_API_KEY=your-key-here

# Option 2: store credential and endpoint interactively
lokit auth login --provider custom-openai

# Option 3: store credential and endpoint non-interactively
lokit auth login --provider custom-openai --api-key YOUR_KEY --base-url https://api.example.com/v1
```

**Usage:**
```bash
lokit translate --provider custom-openai --model my-model --base-url https://api.example.com/v1
```

**Config:**
```yaml
provider:
  id: custom-openai
  model: my-model
  base_url: https://api.example.com/v1
```

**Environment variable:** `CUSTOM_OPENAI_API_KEY`

---

## Environment variables summary

| Variable | Provider |
|----------|----------|
| `GOOGLE_API_KEY` | Google AI (Gemini) |
| `GROQ_API_KEY` | Groq |
| `OPENAI_API_KEY` | OpenAI |
| `CUSTOM_OPENAI_API_KEY` | Custom OpenAI |
| `OPENCODE_API_KEY` | OpenCode |
| `OPENCODE_GO_API_KEY` | OpenCode Go |

GitHub Copilot and Gemini CLI use OAuth only. OpenAI supports both OAuth and API keys.
