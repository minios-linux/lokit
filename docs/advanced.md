# Advanced Usage

## Incremental translation (`lokit.lock`)

lokit tracks MD5 checksums of source strings in a `lokit.lock` file (stored next to `lokit.yaml`). On subsequent runs, only new or changed strings are sent to the AI provider, saving tokens and time.

### How it works

1. First `lokit translate` run: all strings are translated, checksums are recorded in `lokit.lock`
2. Subsequent runs: lokit compares current source strings with stored checksums
3. Only strings with new or changed checksums are sent to the provider
4. After translation, `lokit.lock` is updated

### Key facts

- **Automatic** — no configuration needed; the lock file is created on the first run
- **Per-target, per-language** — changes in one target don't trigger re-translation of others
- **Safe to delete** — removing `lokit.lock` causes a full translation on the next run
- **Commit to VCS** — recommended, so that CI and teammates benefit from incremental translation

### Manual lock management

```bash
# See what's tracked
lokit lock status

# Initialize lock from existing translations (useful when adopting lokit)
lokit lock init

# Remove stale entries and orphan lock targets
lokit lock clean --dry-run
lokit lock clean

# Force re-translation of specific scope
lokit lock reset --target app --lang ru
```

`lokit lock clean` removes stale source-string checksums and lock target namespaces that are no longer present in the current `lokit.yaml`. With `--target`, orphan cleanup is limited to that target namespace. If a target was removed from `lokit.yaml` entirely, run `lokit lock clean` without `--target` to remove its old lock data.

### Force full re-translation

```bash
# Ignores lock AND locked keys
lokit translate --provider copilot --model MODEL_NAME --force
```

---

## Key filtering

Control which keys are translated per target using three settings in `lokit.yaml`.

### `ignored_keys`

Keys excluded from translation entirely. They are never sent to AI, even with `--force`.

```yaml
ignored_keys:
  - debug_label
  - internal_test_string
```

### `locked_keys`

Existing translations preserved as-is. Skipped during normal runs and `--all` runs. Only `--force` overrides them.

```yaml
locked_keys:
  - app_name
  - copyright_notice
```

### `locked_patterns`

Regex patterns — matching keys are treated as locked.

```yaml
locked_patterns:
  - "^brand_.*"
  - "^legal_"
```

### Behavior summary

| Setting | Effect | Overridden by `--force` |
|---------|--------|------------------------|
| `ignored_keys` | Completely skipped | No |
| `locked_keys` | Existing translation preserved | Yes |
| `locked_patterns` | Same as `locked_keys` (regex match) | Yes |

### Full example

```yaml
targets:
  - name: ui
    format: i18next
    from: [locales/en.json]
    to: locales/{lang}.json
    ignored_keys:
      - debug_label
    locked_keys:
      - app_name
      - copyright_notice
    locked_patterns:
      - "^brand_"
      - "^legal_"
```

These settings work with all 12 formats.

---

## Custom system prompts

Each format has a built-in system prompt optimized for its structure. You can override it at two levels:

### Per target (in `lokit.yaml`)

```yaml
targets:
  - name: app
    format: i18next
    from: [locales/en.json]
    to: locales/{lang}.json
    prompt: "Translate to {{targetLang}}. Use formal register. Keep technical terms in English."
```

### Per run (command-line flag)

```bash
lokit translate --provider copilot --model MODEL_NAME \
  --prompt "Translate to {{targetLang}}. Use informal register."
```

### Priority order

1. `--prompt` flag (highest)
2. Target `prompt` in `lokit.yaml`
3. Built-in format-specific prompt (lowest)

### Placeholder

Use `{{targetLang}}` in your prompt — lokit replaces it with the full language name (e.g., "Russian", "German").

---

## Parallel translation

Send translation requests concurrently to speed up large projects.

```bash
# Enable with default 3 workers
lokit translate --provider copilot --model MODEL_NAME --parallel

# Specify worker count
lokit translate --provider copilot --model MODEL_NAME --parallel=10
```

### Chunking

Control how many entries are sent per API request:

```bash
# Send 50 entries per request, 10 requests in parallel
lokit translate --provider copilot --model MODEL_NAME --parallel=10 --chunk 50
```

When `--chunk` is 0 (default), all entries for a language are sent in a single request. For large files, setting a chunk size can improve reliability and allow better parallelism.

### Rate limiting

If you hit rate limits, add a delay between requests:

```bash
lokit translate --provider copilot --model MODEL_NAME --parallel=5 --delay 500ms
```

---

## Proxy support

Route API requests through an HTTP/HTTPS proxy:

```bash
lokit translate --provider copilot --model MODEL_NAME --proxy "http://proxy.example.com:8080"
```

---

## Monorepo and submodule setups

Use `root` to point targets at different directories within a monorepo:

```yaml
languages: [de, es, fr, ru]

targets:
  - name: main-app
    format: gettext
    from: [src/**/*.py]
    template: po/messages.pot
    to: po/{lang}.po

  - name: docs
    format: po4a
    root: documentation
    from: [po4a.cfg]

  - name: web-frontend
    format: i18next
    root: packages/web
    from: [src/i18n/en.json]
    to: src/i18n/{lang}.json

  - name: mobile-app
    format: flutter
    root: packages/mobile
    from: [lib/l10n/app_en.arb]
    to: lib/l10n/app_{lang}.arb
```

Each target operates independently within its `root` directory. All targets are processed together by `lokit status`, `lokit init`, and `lokit translate`.

---

## Translating specific languages

```bash
# Only Russian and German
lokit translate --provider copilot --model MODEL_NAME --lang ru,de
```

---

## Dry run

Preview what would be translated without making any changes:

```bash
lokit translate --provider copilot --model MODEL_NAME --dry-run
```

Shows the number of strings that would be sent to the AI provider, per target and language.

---

## Translating all entries

By default, lokit only translates untranslated strings. To re-translate everything (but still respect locked keys):

```bash
lokit translate --provider copilot --model MODEL_NAME --all
```

To re-translate everything including locked keys:

```bash
lokit translate --provider copilot --model MODEL_NAME --force
```

---

## User data storage

All user data is stored in `~/.local/share/lokit/` (respects `$XDG_DATA_HOME`):

| File | Contents | Permissions |
|------|----------|-------------|
| `auth.json` | OAuth tokens and API keys | `0600` |

No telemetry or usage data is collected.
