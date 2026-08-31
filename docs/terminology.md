# Terminology

Lokit can apply one shared terminology policy across gettext, po4a, Android, JSON, YAML, Markdown, properties, ARB, JS-KV, desktop, and polkit targets.

Terminology data remains project-owned. Lokit provides only the generic loading, matching, prompt, validation, and status mechanisms.

## Configuration

Reference one or more local terminology files from `lokit.yaml`:

```yaml
terminology:
  from:
    - l10n/terminology.yaml
```

Paths are relative to the directory containing `lokit.yaml`. Files are loaded strictly: unknown fields, unsupported versions, duplicate IDs, invalid locale aliases, and conflicting active rules are errors.

## Exact phrases

Exact rules define an authoritative translation for a complete source value:

```yaml
version: 1

exact:
  - id: module-role.core-system
    source: Core system
    when:
      target: [installer, module-manager]
    translations:
      de: Kernsystem
      pt: Sistema principal
      pt-BR: Sistema principal
      ru: Базовая система
```

Exact matches are applied locally without calling the provider. Existing translations that differ are corrected during an ordinary `lokit translate` run; `--force` is not required. Placeholder, plural, format, and term validation still run before the value is applied.

Plural gettext rules declare both source forms and a target-language form list:

```yaml
exact:
  - id: files.count
    source: "%d file"
    source_plural: "%d files"
    translations:
      ru:
        - "%d файл"
        - "%d файла"
        - "%d файлов"
```

The list must contain exactly the number of plural forms required by the target catalog.

## Terms

Term rules constrain words and phrases embedded in larger source values.

Preserve a brand or technical identifier:

```yaml
terms:
  - id: brand.minios
    source: MiniOS
    preserve: true
    case_sensitive: true
```

Specify preferred and accepted translated forms:

```yaml
terms:
  - id: noun.module
    source: module
    translations:
      de:
        preferred: Modul
        accepted: [Module, Moduls, Modulen]
      ru:
        preferred: модуль
        accepted: [модуля, модулю, модулем, модуле, модули, модулей]
```

Only rules matched by a source unit are sent to the provider, scoped by the unit's opaque response ID. Provider output that violates a rule is rejected before files or `lokit.lock` are updated and enters the normal retry flow.

Strict validation is the default. For terms that must follow the target language's grammar and cannot be exhaustively enumerated, use prompt validation:

```yaml
terms:
  - id: app.module-manager
    source: MiniOS Module Manager
    validation: prompt
    translations:
      de: MiniOS-Modulmanager
      ru: Менеджер модулей MiniOS
```

Prompt-validated terms are still sent to the model as preferred terminology, but Lokit does not require a literal preferred or accepted form. It accepts natural inflection and word order while rejecting a target that leaves the source term untranslated. Existing translations containing the untranslated source term are promoted automatically. `preserve: true` always uses strict validation and cannot be combined with `validation: prompt`.

Already translated values are checked on every ordinary translation run. A terminology violation becomes a provider candidate even when its source checksum is unchanged. Compliant preferred or accepted forms are not retranslated.

## Matching

The default term match mode is `word`. Use `substring` for extensions or terms intentionally embedded in identifiers:

```yaml
terms:
  - id: module-extension
    source: .sb
    match: substring
    preserve: true
```

Matching is literal. `case_sensitive` defaults to `false`.

## Selectors

Rules can be scoped with `when`:

```yaml
when:
  target: [installer, module-manager]
  format: gettext
  path: [src/**]
  key: ["Open image"]
  context: menu-label
```

Values within one selector field are ORed. Different fields are ANDed. Selectors use shell-style `*`, `?`, and `**` globs. `context` maps to gettext `msgctxt`; formats without native context use an empty value.

Target selectors allow the same English source to have different approved translations in different products or contexts.

## Locale fallback

Locale names are normalized before lookup. More specific forms win, followed by their base language:

```text
pt_BR      -> pt-BR -> pt
zh-Hant-TW -> zh-Hant-TW -> zh-Hant -> zh
```

Defining locale keys that normalize to the same value, such as both `pt_BR` and `pt-BR`, is an error.

## Status

When terminology is configured, `lokit status` adds a `Terms` column. A target does not report 100% while exact or term violations remain:

```text
Lang     Progress                Done Terms  Left
de       ███████████████░         513     1     0
```

Running `lokit translate` applies exact corrections and translates term violations. No separate checker or post-processing step is required.

## Rule ownership

Keep terminology files under version control with the project that owns the policy. Do not store canonical translations in `lokit.lock`: the lock file remains source-change state and is safe to recreate.
