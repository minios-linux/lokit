# Commands Reference

## `lokit status`

Shows project info and translation statistics for all targets defined in `lokit.yaml`. Read-only — nothing is modified.

```bash
lokit status
lokit status --root ./my-project
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--root string` | Project root directory (default: `.`) |

**Output includes:**
- Target name and format
- Source language and target languages
- Number of total/translated/untranslated strings per language
- Translation percentage per language

---

## `lokit init`

Extracts translatable strings and creates or updates translation files. Idempotent — safe to run repeatedly.

```bash
lokit init
lokit init --lang ru,de
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--lang, -l string` | Comma-separated languages to initialize (default: all) |

**What it does per format:**

| Format | Action |
|--------|--------|
| gettext | Runs `xgettext` to extract strings into POT, `msgmerge` to update PO files |
| po4a | Runs `po4a --no-translations` to extract translatable content |
| i18next, vue-i18n, yaml, properties, flutter, js-kv | Creates empty language files if they don't exist |
| android | Creates `values-<lang>/strings.xml` directories |
| markdown | Creates language directories or empty files depending on layout |
| desktop, polkit | No init needed (translations are inline in source file) |

---

## `lokit translate`

Translates files using an AI provider. Only sends untranslated or changed strings (tracked via `lokit.lock`).

```bash
# Basic usage
lokit translate --provider copilot --model gpt-4.1

# Translate specific languages
lokit translate --provider copilot --model gpt-4.1 --lang ru,de

# Parallel with 10 workers
lokit translate --provider copilot --model gpt-4.1 --parallel=10

# Preview what would be translated
lokit translate --provider copilot --model gpt-4.1 --dry-run

# Re-translate everything (ignore lock and locked keys)
lokit translate --provider copilot --model gpt-4.1 --force
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--provider string` | from config | AI provider ID |
| `--model string` | from config | Model name |
| `--lang, -l string` | all | Comma-separated target languages |
| `--parallel[=N]` | off (N=3) | Enable parallel translation with N workers |
| `--chunk int` | 0 (all) | Entries per API request (0 = send all at once) |
| `--all, -a` | false | Translate all entries, including already translated |
| `--fuzzy` | true | Translate fuzzy entries (gettext/po4a) |
| `--dry-run` | false | Show what would be translated without making changes |
| `--force, -f` | false | Ignore lock file and locked keys, re-translate everything |
| `--prompt string` | — | Custom system prompt (`{{targetLang}}` placeholder available) |
| `--proxy string` | — | HTTP/HTTPS proxy URL |
| `--api-key string` | — | API key (overrides stored credentials) |
| `--base-url string` | — | Custom API endpoint (`custom-openai` or `ollama`) |
| `--timeout duration` | provider default | Request timeout |
| `--retries int` | 3 | Retries on rate-limit or transient errors |
| `--delay duration` | — | Delay between translation requests |
| `--verbose, -v` | false | Detailed logging |

**Provider/model resolution:** command-line flags take priority over `provider` settings in `lokit.yaml`.

---

## `lokit auth`

Manage provider authentication credentials.

### `lokit auth login`

Authenticate with an AI provider. Interactive mode prompts you to choose a provider.

```bash
# Interactive (shows provider menu)
lokit auth login

# Direct
lokit auth login --provider copilot
lokit auth login --provider google
lokit auth login --provider openai
lokit auth login --provider openai --headless
lokit auth login --provider openai --auth-method oauth
lokit auth login --provider openai --auth-method device
lokit auth login --provider openai --auth-method api-key
lokit auth login --provider custom-openai
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--provider string` | Provider to authenticate with |
| `--auth-method string` | Authentication method for providers with multiple options (`openai`) |
| `--headless` | Shortcut for `--provider openai --auth-method device` |
| `--api-key string` | API key (for key-based providers) |
| `--base-url string` | API endpoint (for `custom-openai`) |

### `lokit auth logout`

Remove stored credentials.

```bash
lokit auth logout --provider copilot   # Remove specific provider
lokit auth logout                      # Remove all credentials
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--provider string` | Specific provider to remove (default: all) |

### `lokit auth list`

Show stored credentials and their status. Alias: `lokit auth ls`.

```bash
lokit auth list
```

---

## `lokit lock`

Manage the incremental translation lock file (`lokit.lock`). The lock file is normally managed automatically — these commands are for manual intervention.

### `lokit lock status`

Show lock file statistics (number of tracked entries per target and language).

```bash
lokit lock status
lokit lock status --target app --verbose
lokit lock status --json
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--target string` | Show status for specific target only |
| `--verbose, -v` | Show per-key details |
| `--json` | Output as JSON |

### `lokit lock init`

Initialize lock from existing translated files without calling AI. Useful when adopting lokit on a project with existing translations.

```bash
lokit lock init
lokit lock init --target app
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--target string` | Initialize for specific target only |
| `--force, -f` | Overwrite existing lock entries |

### `lokit lock clean`

Remove stale lock entries that no longer exist in source files.

```bash
lokit lock clean --dry-run   # Preview what would be removed
lokit lock clean              # Remove stale entries
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--target string` | Clean specific target only |
| `--dry-run` | Preview without making changes |

### `lokit lock reset`

Reset lock entries, forcing re-translation on the next run.

```bash
lokit lock reset                          # Reset entire lock file
lokit lock reset --target app             # Reset one target
lokit lock reset --target app --lang ru   # Reset one target+language
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--target string` | Target to reset |
| `--lang, -l string` | Language to reset (requires `--target`) |
| `--yes, -y` | Skip confirmation prompt |

---

## `lokit version`

Display version, commit hash, and build date.

```bash
lokit version
```

---

## `lokit completion`

Generate shell autocompletion scripts.

```bash
# Bash
lokit completion bash > /etc/bash_completion.d/lokit

# Zsh
lokit completion zsh > "${fpath[1]}/_lokit"

# Fish
lokit completion fish > ~/.config/fish/completions/lokit.fish

# PowerShell
lokit completion powershell > lokit.ps1
```

---

## Global flags

| Flag | Description |
|------|-------------|
| `--root string` | Project root directory (default: current directory) |
