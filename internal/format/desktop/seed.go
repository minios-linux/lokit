package desktop

import (
	"fmt"
	"path/filepath"
	"strings"

	po "github.com/minios-linux/lokit/internal/format/po"
)

// DesktopLocale converts a PO language code to the locale tag used in
// freedesktop .desktop inline translations (e.g. pt-BR → pt_BR).
func DesktopLocale(lang string) string {
	return strings.ReplaceAll(lang, "-", "_")
}

// SeedPO fills PO translations from inline .desktop localized fields.
// Desktop inline translations are the source of truth for Name, Comment,
// GenericName, and Keywords entries referenced from .desktop files.
// Returns the number of entries updated.
//
// Known limitations:
//   - Plural-form entries (MsgIDPlural != "") are not seeded; the freedesktop
//     .desktop spec has no plural syntax and xgettext rarely emits them.
//   - Context-qualified entries (MsgCtxt != "") are not matched against the
//     desktop index; extend pathIndexKeys / the lookup if needed.
//   - Basename fallback matching (see inline comment) may produce false
//     positives when two .desktop files in different directories share the
//     same filename.
//
// If some .desktop files cannot be parsed, a partial error is returned
// alongside the seeded count so callers can log the warning without losing
// the successfully seeded entries.
func SeedPO(poFile *po.File, lang, rootDir string, desktopPaths []string) (int, error) {
	if poFile == nil || len(desktopPaths) == 0 {
		return 0, nil
	}

	desktopLang := DesktopLocale(lang)
	indexes := make(map[string]map[string]string, len(desktopPaths))

	var parseErrs []string
	for _, path := range desktopPaths {
		f, err := ParseFile(path, desktopLang)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		byMsgID := make(map[string]string)
		for _, key := range f.Keys() {
			base := f.base[key]
			if base == "" {
				continue
			}
			if loc := f.localized[key]; loc != "" {
				byMsgID[base] = loc
			}
		}
		if len(byMsgID) == 0 {
			continue
		}
		for _, key := range pathIndexKeys(path, rootDir) {
			indexes[key] = byMsgID
		}
	}

	if len(indexes) == 0 {
		if len(parseErrs) > 0 {
			return 0, fmt.Errorf("parsing .desktop files: %s", strings.Join(parseErrs, "; "))
		}
		return 0, nil
	}

	seeded := 0
	for _, e := range poFile.Entries {
		// MsgIDPlural entries are not seeded: xgettext rarely emits plural forms
		// from .desktop files, and the desktop spec has no plural syntax.
		// MsgCtxt entries are also not matched here — if a project uses contextual
		// desktop strings, extend the index key to include MsgCtxt.
		if e.MsgID == "" || e.Obsolete || e.MsgIDPlural != "" {
			continue
		}
		for _, ref := range e.References {
			refPath := referencePath(ref)
			if !isDesktopReference(refPath) {
				continue
			}
			byMsgID, ok := indexes[refPath]
			if !ok {
				// Basename fallback: PO references often use a path relative to
				// the project root while the index was built from an absolute or
				// differently-rooted path. This handles the common case but can
				// produce false positives when two .desktop files in different
				// directories share the same filename. For minios-live layouts
				// this is acceptable; projects with colliding basenames should
				// ensure xgettext emits unambiguous reference paths.
				if byMsgID, ok = indexes[filepath.Base(refPath)]; !ok {
					continue
				}
			}
			loc, ok := byMsgID[e.MsgID]
			if !ok || loc == "" {
				continue
			}
			if e.MsgStr != loc || e.IsFuzzy() {
				e.MsgStr = loc
				e.SetFuzzy(false)
				seeded++
			}
			break
		}
	}

	if len(parseErrs) > 0 {
		return seeded, fmt.Errorf("parsing .desktop files: %s", strings.Join(parseErrs, "; "))
	}
	return seeded, nil
}

func referencePath(ref string) string {
	if i := strings.Index(ref, ":"); i >= 0 {
		ref = ref[:i]
	}
	return filepath.ToSlash(ref)
}

func isDesktopReference(path string) bool {
	return strings.HasSuffix(path, ".desktop") || strings.HasSuffix(path, ".nemo_action")
}

func pathIndexKeys(path, rootDir string) []string {
	slash := filepath.ToSlash(path)
	keys := []string{slash, filepath.Base(slash)}
	if rootDir != "" {
		if rel, err := filepath.Rel(rootDir, path); err == nil {
			rel = filepath.ToSlash(rel)
			keys = append([]string{rel}, keys...)
		}
	}
	seen := make(map[string]bool, len(keys))
	var out []string
	for _, k := range keys {
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}
