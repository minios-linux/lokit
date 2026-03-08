# Configuration Reference

lokit is configured via `lokit.yaml` in your project root. This file is required — lokit will not run without it.

## Schema validation

A [JSON Schema](../lokit.schema.json) is available for editor autocompletion and validation. Add this line to the top of your `lokit.yaml`:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/minios-linux/lokit/refs/heads/master/lokit.schema.json
```

## Minimal config

The only required field is `targets` with at least one entry:

```yaml
source_lang: en
languages: [ru, de]

targets:
  - name: app
    format: i18next
    dir: locales
    pattern: "{lang}.json"
```

## Full reference

```yaml
# Source language (default: "en")
source_lang: en

# Target languages — inherited by all targets unless overridden
languages: [de, es, fr, ru]

# Default AI provider — avoids repeating --provider/--model on every run
provider:
  id: copilot                # Required: copilot | gemini | google | groq | opencode | openai | ollama | custom-openai
  model: gpt-4.1             # Required: model name
  # base_url: http://...     # custom-openai/ollama only
  # prompt: "Custom prompt"  # Global prompt override (supports {{targetLang}})
  # settings:
  #   temperature: 0.3       # 0.0–2.0

# Translation targets (at least one required)
targets:
  - name: my-target           # Display name (required, must be unique)
    format: i18next            # Format (required) — see Formats Guide

    # --- Path resolution ---
    root: .                    # Working directory, relative to lokit.yaml (default: ".")
    dir: locales               # Directory for translation files, relative to root
    pattern: "{lang}.json"     # File pattern with {lang} placeholder

    # --- Gettext-specific ---
    pot: messages.pot           # POT filename in dir
    sources: [src/**/*.py]      # Source globs for xgettext
    keywords: [_, N_]           # xgettext keyword list

    # --- po4a-specific ---
    config: po4a.cfg            # po4a config path relative to root

    # --- Markdown-specific ---
    source: README.md           # Source file (for filename-suffix mode)
    # pattern: "README.{lang}.md"  # Output pattern (for filename-suffix mode)

    # --- Overrides (optional) ---
    source_lang: en             # Override source language for this target
    languages: [de, fr]         # Override language list for this target
    prompt: "Custom prompt"     # Override system prompt for this target

    # --- Key filtering (optional) ---
    ignored_keys: [debug_key]   # Never translated, never sent to AI
    locked_keys: [app_name]     # Preserved as-is (skipped unless --force)
    locked_patterns: ["^brand_"] # Regex patterns treated as locked
```

## Top-level fields

### `source_lang`

Source language code (BCP 47). Default: `"en"`.

### `languages`

Array of target language codes. Inherited by all targets. Can be overridden per target.

```yaml
languages: [ru, de, fr, es, ja, zh-CN]
```

### `provider`

Default AI provider settings. When set, you can run `lokit translate` without `--provider` and `--model` flags.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | yes | Provider identifier |
| `model` | string | yes | Model name |
| `base_url` | string | no | API endpoint (`custom-openai` or `ollama` only) |
| `prompt` | string | no | Global system prompt override |
| `settings.temperature` | number | no | Temperature (0.0–2.0) |

Valid provider IDs: `copilot`, `gemini`, `google`, `groq`, `opencode`, `openai`, `ollama`, `custom-openai`.

### `targets`

Array of translation targets. At least one required. Each target defines a set of files in a specific format to translate.

## Target fields

### Common fields (all formats)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | — | Display name (required, must be unique) |
| `format` | string | — | Translation format (required) |
| `root` | string | `"."` | Base directory relative to `lokit.yaml` |
| `dir` | string | — | Translation file directory relative to `root` |
| `pattern` | string | — | File pattern with `{lang}` placeholder |
| `source_lang` | string | inherited | Override source language |
| `languages` | array | inherited | Override target languages |
| `prompt` | string | — | Custom system prompt for AI |
| `ignored_keys` | array | — | Keys excluded from translation entirely |
| `locked_keys` | array | — | Keys preserved as-is (skipped unless `--force`) |
| `locked_patterns` | array | — | Regex patterns treated as locked |

### Gettext fields

| Field | Type | Description |
|-------|------|-------------|
| `pot` | string | POT template filename (relative to `dir`) |
| `sources` | array | Source file globs for `xgettext` |
| `keywords` | array | `xgettext` keywords (e.g., `["_", "N_:1,2"]`) |

### po4a fields

| Field | Type | Description |
|-------|------|-------------|
| `config` | string | Path to `po4a.cfg` (relative to `root`) |

### Markdown fields

| Field | Type | Description |
|-------|------|-------------|
| `source` | string | Source file path (for filename-suffix layout) |
| `pattern` | string | Output file pattern with `{lang}` (for filename-suffix layout) |

## Path resolution

Paths are resolved in this order:

1. `root` is relative to the directory containing `lokit.yaml`
2. `dir` is relative to `root`
3. `pattern` is relative to `dir`
4. `source` is relative to `root`
5. `config` (po4a) is relative to `root`

Example: if `lokit.yaml` is at `/project/lokit.yaml`:

```yaml
targets:
  - name: app
    format: i18next
    root: frontend          # → /project/frontend
    dir: src/i18n            # → /project/frontend/src/i18n
    pattern: "{lang}.json"   # → /project/frontend/src/i18n/ru.json
```

## The `pattern` field

For file-per-language formats (i18next, vue-i18n, yaml, properties, flutter, js-kv, markdown), `pattern` defines how files are named:

- Must include `{lang}` — replaced with the language code at runtime
- Relative to `dir`

Examples:

| Pattern | Result for `ru` |
|---------|----------------|
| `{lang}.json` | `ru.json` |
| `{lang}/common.json` | `ru/common.json` |
| `messages_{lang}.properties` | `messages_ru.properties` |
| `app_{lang}.arb` | `app_ru.arb` |
| `README.{lang}.md` | `README.ru.md` |

## Per-target overrides

Any target can override `source_lang` and `languages`:

```yaml
languages: [ru, de, fr]  # global default

targets:
  - name: app
    format: i18next
    dir: locales
    pattern: "{lang}.json"
    # uses global languages: ru, de, fr

  - name: docs
    format: markdown
    dir: docs
    languages: [ru, de]    # only ru and de for this target
```

## Multiple targets

A single `lokit.yaml` can define many targets across different formats and directories:

```yaml
languages: [de, es, fr, ru]
source_lang: en

targets:
  - name: cli
    format: gettext
    dir: po
    pot: messages.pot

  - name: docs
    format: po4a
    root: manpages
    config: po4a.cfg

  - name: web
    format: i18next
    root: frontend
    dir: locales
    pattern: "{lang}.json"
    languages: [de, es, fr, pt-BR, ru]

  - name: mobile
    format: flutter
    dir: lib/l10n
    pattern: "app_{lang}.arb"

  - name: readme
    format: markdown
    source: README.md
    dir: .
    pattern: "README.{lang}.md"
```

All targets are processed together by `lokit status`, `lokit init`, and `lokit translate`.
