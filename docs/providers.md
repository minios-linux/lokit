# Providers Guide

lokit supports 8 AI providers for translation. This page covers authentication, environment variables, and usage for each.

## Quick comparison

| Provider | Auth method | Notes | Best for |
|----------|------------|-------|----------|
| GitHub Copilot | OAuth (device code) | Free tier for eligible accounts; otherwise paid | Getting started, general use |
| Gemini CLI (OAuth) | OAuth (browser) | Requires a GCP project ID; quotas depend on plan | OAuth setup; good default |
| Google AI (Gemini) | API key | Paid API (limited free quota may apply) | API-based access to Gemini models |
| Groq | API key | Paid API (free plan limits may apply) | Fast inference |
| OpenCode (Zen) | API key | Paid API (some models may be $0) | One key for multiple model families |
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
- [OpenCode Zen docs](https://dev.opencode.ai/docs/zen)

## Credential lookup order

When you run `lokit translate`, credentials are resolved in this order (first match wins):

1. `--api-key` flag on the command line
2. Provider-specific environment variable (see table below)
3. Stored credentials in `~/.local/share/lokit/auth.json`

## Storage

Credentials are stored in `~/.local/share/lokit/auth.json` (permissions: `0600`). The path respects `$XDG_DATA_HOME`.

---

## GitHub Copilot

Uses OAuth device-code flow. Availability depends on your Copilot plan and eligibility.

**Auth:**
```bash
lokit auth login --provider copilot
```

This prints a code and opens your browser. Confirm the code on github.com to complete authentication.

**Usage:**
```bash
lokit translate --provider copilot --model gpt-4.1
```

**Pricing:** See GitHub Copilot plans (Copilot Free is only available for eligible accounts).

**Config shortcut:**
```yaml
provider:
  id: copilot
  model: gpt-4.1
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
lokit translate --provider gemini --model gemini-2.5-pro
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
lokit translate --provider google --model gemini-2.5-flash
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
lokit translate --provider groq --model llama-3.3-70b-versatile
```

**Pricing/limits:** See Groq pricing and rate limits.

**Environment variable:** `GROQ_API_KEY`

---

## OpenCode

OpenCode Zen endpoint (`https://opencode.ai/zen/v1`). Zen is a paid API, and OpenCode also lists some models with $0 pricing.

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

**Pricing/models:** See OpenCode Zen docs and pricing.

**Environment variable:** `OPENCODE_API_KEY`

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
lokit translate --provider openai --model gpt-5
```

OAuth/device auth uses the ChatGPT Codex endpoint, so use GPT-5/Codex models there. For `gpt-4o` or `gpt-4.1`, use an API key. The `openai` provider always uses the fixed OpenAI API endpoint; for custom endpoints use `custom-openai`.

**Config:**
```yaml
provider:
  id: openai
  model: gpt-5
```

**Environment variable:** `OPENAI_API_KEY`

---

## Ollama

Runs models locally. No API key needed. Requires [Ollama](https://ollama.com/) installed and running.

**Auth:** None required.

**Usage:**
```bash
# Make sure Ollama is running with a model pulled
ollama pull llama3

lokit translate --provider ollama --model llama3
```

**Custom endpoint:**
```bash
lokit translate --provider ollama --model llama3 --base-url http://192.168.1.100:11434
```

**Config:**
```yaml
provider:
  id: ollama
  model: llama3
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

GitHub Copilot and Gemini CLI use OAuth only. OpenAI supports both OAuth and API keys.
