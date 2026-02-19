# lokit — Localization Kit

**lokit** is a universal localization manager with AI-powered translation. It supports gettext PO, po4a documentation, i18next JSON, Android strings.xml, YAML, Markdown, Java .properties, Flutter ARB, and generic JSON formats — either auto-detected or configured via `lokit.yaml`.

## Features

- **Multi-target config** — `lokit.yaml` for complex projects with multiple translation types
- **Auto-detection** — works without config for simple projects (flat PO, nested PO, po4a, i18next)
- **AI translation** — 7 providers including free GitHub Copilot and Gemini
- **Translation statistics** — per-target progress tracking
- **Smart PO management** — extract, merge, update with `xgettext` and `msgmerge`
- **Native OAuth** — GitHub Copilot (device code) and Gemini (browser)
- **Parallel translation** — concurrent API requests with configurable chunking

## Supported Formats

| Format | Extension | Use case |
|--------|-----------|----------|
| **gettext** | `.po` / `.pot` | Source code strings (shell, Python, C, Go) |
| **po4a** | `.po` + `po4a.cfg` | Documentation / manpages |
| **i18next** | `.json` | Web apps with i18next |
| **json** | `.json` | Simple JSON key-value translations |
| **android** | `strings.xml` | Android resource files |
| **yaml** | `.yaml` / `.yml` | YAML key-value translations |
| **markdown** | `.md` | Markdown document translation |
| **properties** | `.properties` | Java application translations |
| **flutter** | `.arb` | Flutter Application Resource Bundle |

## Supported AI Providers

| Provider | Auth | Free tier |
|----------|------|-----------|
| **GitHub Copilot** | OAuth (device code) | ✅ with GitHub account |
| **Gemini Code Assist** | OAuth (browser) | ✅ 60 req/min |
| **Google AI (Gemini)** | API key | ✅ limited |
| **Groq** | API key | ✅ limited |
| **OpenCode** | API key (optional) | ✅ free models available |
| **Ollama** | none (local) | ✅ |
| **Custom OpenAI** | API key | depends |

## Installation

### From source

```bash
git clone https://github.com/minios-linux/lokit.git
cd lokit
make build
```

### Using go install

```bash
go install github.com/minios-linux/lokit@latest
```

### Check version

```bash
lokit version
```

## Quick Start

### Simple project (auto-detection)

```bash
# Check project structure
lokit status

# Extract strings and create PO files
lokit init

# Authenticate
lokit auth login --provider copilot

# Translate all languages
lokit translate --provider copilot --model gpt-4.1
```

### Complex project (`lokit.yaml`)

Create a `lokit.yaml` in the project root:

```yaml
languages: [de, es, fr, ru]
source_lang: en

targets:
  - name: app
    type: gettext
    dir: po

  - name: docs
    type: po4a
    root: manpages
    po4a_config: po4a.cfg

  - name: website
    type: i18next
    root: submodules/site
    dir: public/translations
    languages: [de, es, fr, pt_BR, ru]

  - name: mobile
    type: flutter
    dir: lib/l10n
```

Then:

```bash
lokit status        # Shows all targets with stats
lokit translate --provider copilot --model gpt-4o  # Translates everything
```

## Configuration File (`lokit.yaml`)

When `lokit.yaml` exists, lokit uses it as the sole source of truth — no auto-detection.

### Schema

A [JSON Schema](lokit.schema.json) is available for editor autocompletion and validation.
To enable it in VS Code, add to the top of your `lokit.yaml`:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/minios-linux/lokit/main/lokit.schema.json
```

Reference:

```yaml
# Default languages for all targets
languages: [de, es, fr, id, it, pt, pt_BR, ru]

# Source language (default: en)
source_lang: en

# Translation targets
targets:
  - name: my-app              # Display name (required)
    type: gettext              # Type (required, see below)
    root: .                    # Working directory relative to config (default: .)

    # --- gettext options ---
    dir: po                    # Base directory for this target (required)
    pot_file: po/messages.pot  # POT template path (default: dir/messages.pot)
    sources: [src/**/*.sh]     # Source globs for xgettext
    keywords: [_, N_, gettext] # xgettext keywords

    # --- po4a options ---
    po4a_config: po4a.cfg      # po4a config path relative to root

    # --- i18next / json options ---
    dir: public/translations           # JSON directory
    recipes_dir: data/recipes              # Per-recipe translations (i18next)
    blog_dir: data/blog                    # Blog post translations (i18next)

    # --- android options ---
    dir: app/src/main/res      # Android res/ directory

    # --- yaml / properties / flutter / markdown options ---
    dir: translations               # Files directory

    # --- overrides ---
    languages: [de, es, fr]    # Override global language list
    source_lang: en            # Override source language
    prompt: "Custom prompt"    # Override system prompt for AI
```

### Target types

**gettext** — For code translation (shell, Python, C, Go). Reads `.pot` templates, translates to `.po` files.

**po4a** — For documentation (manpages, AsciiDoc). Works with `po4a.cfg` and its PO directory.

**i18next** — For web applications using i18next. JSON files with `_meta` block.

**json** — For simple JSON translations. JSON files with `translations` block.

**android** — For Android applications. Translates `strings.xml` resource files.

**yaml** — For YAML key-value translation files (`translations/LANG.yaml`).

**markdown** — For Markdown document translation. Files organized as `dir/LANG/file.md`.

**properties** — For Java `.properties` files (`dir/LANG.properties`).

**flutter** — For Flutter ARB files (`dir/app_LANG.arb`).

## Commands

### `lokit status`

Shows project info and translation statistics. With `lokit.yaml`, displays per-target stats.

```bash
lokit status                    # Current directory
lokit status --root ./my-project  # Specific project
```

### `lokit init`

Extracts translatable strings and creates/updates translation files:

```bash
lokit init                     # All languages
lokit init --lang ru,de        # Specific languages
```

- Runs `xgettext` for gettext projects
- Runs `po4a --no-translations` for po4a projects
- Creates missing language files for i18next, json, yaml, properties, flutter
- Idempotent — safe to run repeatedly

### `lokit translate`

Translates using AI:

```bash
lokit translate --provider copilot --model gpt-4o

# All flags:
  --provider string         AI provider (required)
  --model string            Model name (required)
  --lang string             Languages (comma-separated, default: all untranslated)
  --parallel                Enable parallel translation
  --max-concurrent int      Max concurrent tasks (default: 3)
  --chunk-size int          Entries per API request (0 = all at once)
  --retranslate             Re-translate already translated entries
  --fuzzy                   Translate fuzzy entries (default: true)
  --dry-run                 Show what would be translated
  --force                   Ignore lock file, re-translate all entries
  --prompt string           Custom system prompt ({{targetLang}} placeholder)
  --proxy string            HTTP/HTTPS proxy URL
  --api-key string          API key (or provider env var)
  --base-url string         Custom API endpoint
  --timeout duration        Request timeout (0 = provider default)
  --max-retries int         Retries on rate limit (default: 3)
  --request-delay duration  Delay between parallel tasks
  --verbose                 Detailed logging
```

### `lokit auth`

Manage provider credentials:

```bash
# Interactive login
lokit auth login

# Specific provider
lokit auth login --provider copilot
lokit auth login --provider google

# List stored credentials
lokit auth list

# Remove credentials
lokit auth logout --provider copilot
lokit auth logout                      # Remove all
```

### `lokit version`

Show version, commit hash, and build date.

## Examples

### Translate a monorepo with submodules

```yaml
# lokit.yaml
languages: [de, es, fr, id, it, pt, pt_BR, ru]
targets:
  - name: scripts
    type: gettext
    dir: po

  - name: manpages
    type: po4a
    root: manpages
    po4a_config: po4a.cfg

  - name: cli-tool
    type: gettext
    root: submodules/my-tool
    dir: po
```

```bash
lokit translate --provider copilot --model gpt-4o --parallel --max-concurrent 10
```

### Flutter application

```yaml
# lokit.yaml
languages: [de, es, fr, ru, ja, zh]
source_lang: en
targets:
  - name: app
    type: flutter
    dir: lib/l10n
```

```bash
lokit init
lokit translate --provider copilot --model gpt-4o
```

### Java application with .properties

```yaml
# lokit.yaml
languages: [de, es, fr, ru]
source_lang: en
targets:
  - name: app
    type: properties
    dir: src/main/resources
```

### Parallel translation with proxy

```bash
lokit translate \
  --provider copilot --model gpt-4.1 \
  --parallel --max-concurrent 10 --chunk-size 50 \
  --proxy "http://proxy:8080"
```

### Translate specific languages

```bash
lokit translate --provider copilot --model gpt-4o --lang ru,de
```

### Use local Ollama

```bash
lokit translate --provider ollama --model llama3
```

### Custom OpenAI endpoint

```bash
lokit auth login --provider custom-openai --api-key YOUR_KEY --base-url https://api.example.com/v1
lokit translate --provider custom-openai --model my-model
```

### Dry run

```bash
lokit translate --provider copilot --model gpt-4o --dry-run
```

## User Data Storage

All user data is stored in `~/.local/share/lokit/` (respects `$XDG_DATA_HOME`):

- **`auth.json`** — OAuth tokens and API keys (permissions: `0600`)

### Credentials

**Lookup order** (highest → lowest):
1. `--api-key` flag
2. Provider-specific environment variable (`GOOGLE_API_KEY`, `GROQ_API_KEY`, `OPENAI_API_KEY`, `OPENCODE_API_KEY`)
3. Stored credentials in `auth.json`

### System Prompts

Each target type has a built-in system prompt optimized for its format (gettext, po4a/docs, i18next, recipe, blogpost, android). Prompts can be customized in two ways:

- **Per target** — set `prompt:` in the target config in `lokit.yaml`
- **Per run** — use the `--prompt` flag on the command line

The `--prompt` flag takes priority over the `lokit.yaml` target prompt, which takes priority over the built-in default. Use `{{targetLang}}` as a placeholder for the target language name.

## Incremental Translation (lokit.lock)

lokit tracks MD5 checksums of source strings in a `lokit.lock` file (stored next to `lokit.yaml`). On subsequent runs, only new or changed strings are sent to the AI provider, saving tokens and time.

- **Automatic** — no configuration needed. The lock file is created on the first `lokit translate` run and updated after each translation.
- **Per-target tracking** — checksums are stored per target and language, so changes in one target don't trigger re-translation of others.
- **Force re-translation** — use `--force` to ignore the lock file and re-translate all entries:
  ```bash
  lokit translate --provider copilot --model gpt-4o --force
  ```
- **Safe to delete** — removing `lokit.lock` simply causes a full translation on the next run.
- **Commit to VCS** — it's recommended to commit `lokit.lock` to version control so that CI and teammates benefit from incremental translation.

## Key Filtering (locked_keys / ignored_keys / locked_patterns)

You can control which keys are translated per target in `lokit.yaml`:

```yaml
targets:
  - name: ui
    format: i18next
    translations_dir: translations
    source_lang: en
    languages: [ru, de, fr]

    # Keys excluded from translation entirely (never sent to AI)
    ignored_keys:
      - debug_label
      - internal_test_string

    # Hand-curated translations preserved as-is (skipped even with --retranslate)
    locked_keys:
      - app_name
      - copyright_notice

    # Regex patterns — matching keys treated as locked
    locked_patterns:
      - "^brand_.*"
      - "^legal_"
```

**Semantics:**

| Field | Effect | Overridden by |
|-------|--------|---------------|
| `ignored_keys` | Key is completely skipped, as if it doesn't exist | — |
| `locked_keys` | Existing translation is preserved, key is not re-translated | `--force` |
| `locked_patterns` | Same as `locked_keys` but matches keys by regex | `--force` |

- `ignored_keys` are always skipped, even with `--force`.
- `locked_keys` and `locked_patterns` are skipped during normal and `--retranslate` runs. Only `--force` overrides them.
- These settings work with all formats: gettext PO, po4a, i18next, Android, YAML, Markdown, .properties, and Flutter ARB.

## Project Structure Support

Without `lokit.yaml`, lokit auto-detects:

**Flat PO** — `po/*.po` (shell/Python gettext)

**Nested PO** — `po/<lang>/*.po` (C/large projects)

**po4a** — `po4a.cfg` + `po/<lang>/*.po` (documentation)

**i18next** — `translations/<lang>.json` or `public/translations/<lang>.json`

## GitHub Actions & Releases

### CI/CD Workflows

- **CI** — Tests, builds, and lints on every push/PR
- **Release** — Builds multi-platform binaries on version tags

### Creating a Release

```bash
git tag v0.2.0
git push origin v0.2.0
```

Automated build for Linux and macOS (amd64, arm64) with binary uploads.

View releases: https://github.com/minios-linux/lokit/releases

## Development

Built with Go 1.23+:

- [cobra](https://github.com/spf13/cobra) — CLI framework
- [yaml.v3](https://gopkg.in/yaml.v3) — YAML config parsing
- Native OAuth for GitHub Copilot and Gemini

## License

MIT License — see [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## Author

MiniOS Linux Team
