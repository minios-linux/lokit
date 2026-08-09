package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minios-linux/lokit/config"
	. "github.com/minios-linux/lokit/i18n"
	formatfile "github.com/minios-linux/lokit/internal/format"
	"github.com/minios-linux/lokit/internal/format/android"
	"github.com/minios-linux/lokit/translate"
)

func showKVStatsHeader(langs []string) int {
	langWidth := langColumnWidth(langs)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s%-*s %-22s %5s %5s %5s%s\n",
		colorDim, langWidth+3, T("Lang"), T("Progress"), T("Done"), T("Terms"), T("Left"), colorReset)
	fmt.Fprintln(os.Stderr, "  "+colorDim+strings.Repeat("─", 52)+colorReset)
	return langWidth
}

func showKVStatsRow(lang string, langWidth, total, translated, termViolations int) {
	left := total - translated
	if left < 0 {
		left = 0
	}
	percent := 0
	if total > 0 {
		percent = (translated - termViolations) * 100 / total
	}
	fmt.Fprintf(os.Stderr, "  %s %s %5d %5d %5d\n",
		langCell(lang, langWidth), progressBar(percent, 16), translated, termViolations, left)
}

func countKVTerminologyViolations(rt config.ResolvedTarget, lang string, file formatfile.KVFile, sourceValues map[string]string, sourcePath string) int {
	opts := translate.Options{Language: lang, SourcePath: sourcePath}
	setTargetOpts(&opts, &rt)
	violations, err := translate.FindKVTerminologyViolations(file, sourceValues, opts)
	if err != nil {
		logWarning(T("[%s] Terminology check failed for %s: %v"), rt.Target.Name, lang, err)
		_, translated, _ := file.Stats()
		return translated
	}
	if len(violations) > 0 {
		logWarning(T("[%s] %s terminology violations: %s"), rt.Target.Name, lang, strings.Join(violations, ", "))
	}
	return len(violations)
}

func relativeSourcePath(rt config.ResolvedTarget, path string) string {
	rel, err := filepath.Rel(rt.AbsRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// androidStatusFile exposes Android strings, array items, and plural forms as
// the same flat KV units used by the Android translation pipeline.
type androidStatusFile struct {
	keys         []string
	values       map[string]string
	sourceValues map[string]string
}

func newAndroidStatusFile(target, source *android.File) *androidStatusFile {
	f := &androidStatusFile{
		values:       make(map[string]string),
		sourceValues: make(map[string]string),
	}
	for _, sourceEntry := range source.Entries {
		if !sourceEntry.IsTranslatable() || sourceEntry.IsComment() {
			continue
		}
		targetEntry := target.GetEntry(sourceEntry.Name)
		switch sourceEntry.Kind {
		case android.KindString:
			f.add(sourceEntry.Name, sourceEntry.Value, androidStringValue(targetEntry))
		case android.KindStringArray:
			for i, value := range sourceEntry.Items {
				key := fmt.Sprintf("%s[%d]", sourceEntry.Name, i)
				f.add(key, value, androidArrayValue(targetEntry, i))
			}
		case android.KindPlurals:
			for _, quantity := range sourceEntry.PluralOrder {
				key := fmt.Sprintf("%s#%s", sourceEntry.Name, quantity)
				f.add(key, sourceEntry.Plurals[quantity], androidPluralValue(targetEntry, quantity))
			}
		}
	}
	return f
}

func (f *androidStatusFile) add(key, source, target string) {
	f.keys = append(f.keys, key)
	f.sourceValues[key] = source
	f.values[key] = target
}

func androidStringValue(entry *android.Entry) string {
	if entry != nil && entry.Kind == android.KindString {
		return entry.Value
	}
	return ""
}

func androidArrayValue(entry *android.Entry, index int) string {
	if entry != nil && entry.Kind == android.KindStringArray && index < len(entry.Items) {
		return entry.Items[index]
	}
	return ""
}

func androidPluralValue(entry *android.Entry, quantity string) string {
	if entry != nil && entry.Kind == android.KindPlurals {
		return entry.Plurals[quantity]
	}
	return ""
}

func (f *androidStatusFile) Keys() []string {
	return append([]string(nil), f.keys...)
}

func (f *androidStatusFile) UntranslatedKeys() []string {
	keys := make([]string, 0)
	for _, key := range f.keys {
		if f.values[key] == "" {
			keys = append(keys, key)
		}
	}
	return keys
}

func (f *androidStatusFile) Get(key string) (string, bool) {
	value, ok := f.values[key]
	return value, ok
}

func (f *androidStatusFile) Set(key, value string) bool {
	if _, ok := f.values[key]; !ok {
		return false
	}
	f.values[key] = value
	return true
}

func (f *androidStatusFile) Stats() (total int, translated int, pct float64) {
	total = len(f.keys)
	for _, value := range f.values {
		if value != "" {
			translated++
		}
	}
	if total > 0 {
		pct = float64(translated) * 100 / float64(total)
	}
	return total, translated, pct
}

func (f *androidStatusFile) SourceValues() map[string]string {
	values := make(map[string]string, len(f.sourceValues))
	for key, value := range f.sourceValues {
		values[key] = value
	}
	return values
}

func (f *androidStatusFile) WriteFile(string) error {
	return fmt.Errorf("android status file is read-only")
}

var _ formatfile.KVFile = (*androidStatusFile)(nil)

func translatedSourceKeys(file formatfile.KVFile, sourceValues map[string]string) int {
	translated := 0
	for key := range sourceValues {
		value, _ := file.Get(key)
		if strings.TrimSpace(value) != "" {
			translated++
		}
	}
	return translated
}
