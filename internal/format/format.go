package format

// KVFile is a generic key-value translation file interface used by
// format-agnostic translation pipelines.
type KVFile interface {
	Keys() []string
	UntranslatedKeys() []string
	Set(key, value string) bool
	Stats() (total int, translated int, pct float64)
	SourceValues() map[string]string
	WriteFile(path string) error
}
