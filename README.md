# lokit — Localization Kit

**lokit** is a modern gettext PO file manager with AI-powered translation support. It simplifies the workflow of extracting, managing, and translating localization files for software projects and documentation.

## Features

- 🚀 **Auto-detection** of project structure (flat, nested, po4a, i18next)
- 🤖 **AI-powered translation** with multiple provider support
- 🔄 **Smart PO file management** — extract, merge, update
- 📊 **Translation statistics** and progress tracking
- 🔐 **Native OAuth support** for GitHub Copilot and Gemini
- 🌍 **Multiple formats** — gettext PO, po4a documentation, i18next JSON

## Supported AI Providers

- **GitHub Copilot** — Native OAuth integration (free for subscribers)
- **Gemini Code Assist** — Browser OAuth (free)
- **Google AI (Gemini)** — API key required
- **Groq** — API key required
- **OpenCode** — Multi-format dispatcher
- **Ollama** — Local server
- **Custom OpenAI** — Any OpenAI-compatible endpoint

## Installation

### From source

```bash
git clone https://github.com/minios-linux/lokit.git
cd lokit
make build
```

This builds the binary with version information embedded. You can also use:

```bash
# Just build (no version info)
go build

# Build and install to $GOPATH/bin
make install

# Build with custom version
VERSION=1.0.0 make build
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

### 1. Check project status

```bash
lokit status
```

Shows detected project structure, languages, and translation progress.

### 2. Initialize translations

```bash
lokit init
```

Extracts translatable strings and creates/updates PO files.

### 3. Authenticate with AI provider

```bash
# GitHub Copilot (recommended)
lokit auth copilot

# Or Gemini Code Assist
lokit auth gemini

# Or use API key
lokit auth google --api-key YOUR_API_KEY
```

### 4. Translate

```bash
# Translate all languages
lokit translate --provider copilot

# Translate specific language
lokit translate --provider copilot --lang ru

# Force retranslation
lokit translate --provider copilot --force
```

## Project Structure Support

### Gettext Projects (Code)

**Flat structure:**
```
po/
  ├── ru.po
  ├── cs.po
  └── de.po
```

**Nested structure:**
```
po/
  ├── ru/
  │   └── messages.po
  ├── cs/
  │   └── messages.po
  └── de/
      └── messages.po
```

### po4a Projects (Documentation)

```
po/
  ├── po4a.conf
  ├── ru.po
  └── cs.po
```

### i18next Projects (JSON)

```
translations/
  ├── en/
  │   └── translation.json
  └── ru/
      └── translation.json
```

## Commands

### `lokit version`

Display version information:
```bash
lokit version
```

Shows version, commit hash, and build date.

### `lokit status`

Display project information and translation statistics.

### `lokit init`

Extract translatable strings and create/update PO files:
- Runs `xgettext` to extract strings from source code
- Creates or updates PO files using `msgmerge`
- Detects project structure automatically

### `lokit translate`

Translate PO files using AI:
```bash
lokit translate [flags]

Flags:
  --provider string   AI provider (copilot, gemini, google, groq, ollama, custom-openai)
  --lang string       Specific language to translate (default: all)
  --force             Force retranslation of already translated strings
  --fuzzy             Include fuzzy translations
  --api-key string    API key (overrides stored credentials)
  --base-url string   Custom endpoint URL (for custom-openai)
  --proxy string      HTTP/HTTPS proxy URL (e.g., http://proxy:8080)
  --timeout duration  Request timeout (default: 60s)
```

**Note on geographic restrictions:** If you're in a region where some AI providers are blocked, you can:
- Use `--proxy` flag to route requests through a proxy server
- Try alternative providers (Gemini, Ollama, custom endpoints)
- Set environment variable: `export HTTPS_PROXY=http://your-proxy:port`

### `lokit auth`

Manage provider authentication:
```bash
# Login (starts OAuth flow)
lokit auth copilot
lokit auth gemini

# Set API key
lokit auth google --api-key YOUR_KEY
lokit auth groq --api-key YOUR_KEY

# Check status
lokit auth status

# Logout
lokit auth logout copilot
```

## Configuration

Credentials are stored securely in:
```
~/.local/share/lokit/auth.json
```

(Respects `$XDG_DATA_HOME` if set)

File permissions: `0600` (owner read/write only)

## Credential Lookup Order

For API keys:
1. `--api-key` flag (highest priority)
2. `LOKIT_API_KEY` environment variable
3. Stored credentials in `auth.json`

## Examples

### Translate with GitHub Copilot

```bash
# First time: authenticate
lokit auth copilot

# Translate all languages
lokit translate --provider copilot

# Translate only Russian
lokit translate --provider copilot --lang ru
```

### Use Ollama locally

```bash
# Make sure Ollama is running locally
# Default endpoint: http://localhost:11434

lokit translate --provider ollama
```

### Custom OpenAI endpoint

```bash
# Authenticate with custom endpoint
lokit auth custom-openai --api-key YOUR_KEY --base-url https://api.example.com/v1

# Translate
lokit translate --provider custom-openai
```

## GitHub Actions & Releases

### Automated Workflows

This project uses GitHub Actions for continuous integration and automated releases:

- **CI Workflow** — Runs on every push and PR
  - Tests code on Linux
  - Builds for multiple platforms (Linux, macOS, Windows)
  - Checks code formatting and runs `go vet`

- **Release Workflow** — Triggered by version tags
  - Builds binaries for all supported platforms
  - Creates GitHub release with changelog
  - Attaches binary archives automatically

### Creating a Release

To create a new release:

```bash
# Tag your commit
git tag v0.2.0

# Push the tag to GitHub
git push origin v0.2.0
```

The Release workflow will automatically:
1. Build binaries for Linux, macOS, and Windows
2. Create release notes from git commits
3. Upload all binaries as release assets

View releases at: https://github.com/minios-linux/lokit/releases

## Development

Built with Go 1.23+, using:
- [cobra](https://github.com/spf13/cobra) — CLI framework
- Native OAuth implementations for GitHub Copilot and Gemini

## License

MIT License — see [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## Author

MiniOS Linux Team
