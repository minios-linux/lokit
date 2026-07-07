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
    from: [locales/en.json]
    to: locales/{lang}.json
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
  # prompt: "Custom prompt"  # Global prompt override (supports {{targetLang}} and {{sourceLang}})
  # settings:
  #   temperature: 0.3       # 0.0–2.0

# Translation targets (at least one required)
targets:
  - name: my-target           # Display name (required, must be unique)
    format: i18next            # Format (required) — see Formats Guide

    # --- Path resolution ---
    root: .                    # Working directory, relative to lokit.yaml (default: ".")
    from: [locales/en.json]    # Source files/globs, relative to root
    except: [dist/**]          # Optional exclude globs for source discovery
    to: locales/{lang}.json    # Output path template, relative to root

    # --- Gettext-specific ---
    # For gettext, use source globs plus a POT template and PO output path:
    # from: [src/**/*.py]
    # template: po/messages.pot
    # to: po/{lang}.po
    # keywords: [_, N_]         # xgettext keyword list

    # --- po4a-specific ---
    # For po4a, point from at the po4a config file:
    # from: [po4a.cfg]

    # --- Markdown-specific ---
    # For Markdown, from can be a file or glob and to controls the output layout:
    # from: [README.md]
    # to: README.{lang}.md

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
| `from` | array/object | — | Source files/globs, or indexed record source |
| `except` | array | — | Source file globs excluded from `from` discovery |
| `to` | string | — | Output path template with placeholders |
| `template` | string | — | Optional generated template/catalog path, e.g. gettext POT |
| `source_lang` | string | inherited | Override source language |
| `languages` | array | inherited | Override target languages |
| `prompt` | string | — | Custom system prompt for AI |
| `ignored_keys` | array | — | Keys excluded from translation entirely |
| `locked_keys` | array | — | Keys preserved as-is (skipped unless `--force`) |
| `locked_patterns` | array | — | Regex patterns treated as locked |

### Gettext fields

| Field | Type | Description |
|-------|------|-------------|
| `template` | string | POT template path relative to `root` |
| `from` | array | Source file globs for `xgettext` |
| `to` | string | PO output path template, usually `po/{lang}.po` |
| `keywords` | array | `xgettext` keywords (e.g., `["_", "N_:1,2"]`) |

### po4a fields

| Field | Type | Description |
|-------|------|-------------|
| `from` | array | Path to `po4a.cfg` relative to `root`, e.g. `[po4a.cfg]` |

### Markdown fields

| Field | Type | Description |
|-------|------|-------------|
| `from` | array | Source Markdown files/globs |
| `to` | string | Output path template with `{lang}`, `{path}`, or `{id}` |

## Path resolution

Paths are resolved in this order:

1. `root` is relative to the directory containing `lokit.yaml`
2. `from`, `except`, `to`, and `template` are relative to `root`
3. `to` is expanded per target language
4. `from` globs are expanded before `except` filters are applied

Example: if `lokit.yaml` is at `/project/lokit.yaml`:

```yaml
targets:
  - name: app
    format: i18next
    root: frontend          # → /project/frontend
    from: [src/i18n/en.json] # → /project/frontend/src/i18n/en.json
    to: src/i18n/{lang}.json # → /project/frontend/src/i18n/ru.json
```

## The `to` field

For file-per-language formats (i18next, vue-i18n, yaml, properties, flutter, js-kv, markdown), `to` defines how translated files are named:

- Must include `{lang}` — replaced with the language code at runtime
- Relative to `root`
- May also use `{source_lang}`, `{path}`, `{folder}`, `{name}`, `{ext}`, and `{id}` where supported

Examples:

| Template | Result for `ru` |
|----------|-----------------|
| `locales/{lang}.json` | `locales/ru.json` |
| `locales/{lang}/common.json` | `locales/ru/common.json` |
| `i18n/messages_{lang}.properties` | `i18n/messages_ru.properties` |
| `lib/l10n/app_{lang}.arb` | `lib/l10n/app_ru.arb` |
| `README.{lang}.md` | `README.ru.md` |
| `translations/{lang}/{path}` | `translations/ru/guide/intro.md` |

## Per-target overrides

Any target can override `source_lang` and `languages`:

```yaml
languages: [ru, de, fr]  # global default

targets:
  - name: app
    format: i18next
    from: [locales/en.json]
    to: locales/{lang}.json
    # uses global languages: ru, de, fr

  - name: docs
    format: markdown
    from: [docs/**/*.md]
    to: translations/{lang}/{path}
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
    from: [src/**/*.sh]
    template: po/messages.pot
    to: po/{lang}.po

  - name: docs
    format: po4a
    root: manpages
    from: [po4a.cfg]

  - name: web
    format: i18next
    root: frontend
    from: [locales/en.json]
    to: locales/{lang}.json
    languages: [de, es, fr, pt-BR, ru]

  - name: mobile
    format: flutter
    from: [lib/l10n/app_en.arb]
    to: lib/l10n/app_{lang}.arb

  - name: readme
    format: markdown
    from: [README.md]
    to: README.{lang}.md
```

All targets are processed together by `lokit status`, `lokit init`, and `lokit translate`.
