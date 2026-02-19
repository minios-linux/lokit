// Package lockfile implements lokit.lock — a lock file that tracks
// MD5 checksums of source strings per language. This enables incremental
// translation: only new or changed strings are sent to the AI provider,
// saving tokens and time.
//
// The lock file is stored alongside lokit.yaml as lokit.lock.
package lockfile

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// LockFileName is the default lock file name.
const LockFileName = "lokit.lock"

// Version is the lock file format version.
const Version = 1

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// LockFile represents the lokit.lock file structure.
type LockFile struct {
	Version   int                          `yaml:"version"`
	Checksums map[string]map[string]string `yaml:"checksums"` // target -> key -> md5

	mu   sync.Mutex `yaml:"-"`
	path string     `yaml:"-"`
}

// ---------------------------------------------------------------------------
// Loading and saving
// ---------------------------------------------------------------------------

// Load reads a lock file from the given directory.
// Returns an empty lock file if the file doesn't exist.
func Load(dir string) (*LockFile, error) {
	path := filepath.Join(dir, LockFileName)
	lf := &LockFile{
		Version:   Version,
		Checksums: make(map[string]map[string]string),
		path:      path,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return lf, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, lf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	lf.path = path

	if lf.Checksums == nil {
		lf.Checksums = make(map[string]map[string]string)
	}

	return lf, nil
}

// Save writes the lock file to disk.
func (lf *LockFile) Save() error {
	lf.mu.Lock()
	defer lf.mu.Unlock()

	if lf.path == "" {
		return fmt.Errorf("lock file path not set")
	}

	data, err := yaml.Marshal(lf)
	if err != nil {
		return fmt.Errorf("marshaling lock file: %w", err)
	}

	if err := os.WriteFile(lf.path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", lf.path, err)
	}

	return nil
}

// Path returns the lock file path.
func (lf *LockFile) Path() string {
	return lf.path
}

// ---------------------------------------------------------------------------
// Checksum operations
// ---------------------------------------------------------------------------

// Hash computes the MD5 hex digest of a string.
func Hash(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}

// targetKey builds a unique key for a target entry.
// For PO files: "po/ru.po"
// For i18next: "locales/ru/translation.json"
// For android: "app/src/main/res/values-ru/strings.xml"
func TargetKey(filePath string) string {
	return filepath.ToSlash(filePath)
}

// IsChanged checks if a source string has changed since last translation.
// Returns true if the string is new or its content has changed.
func (lf *LockFile) IsChanged(target, key, sourceContent string) bool {
	lf.mu.Lock()
	defer lf.mu.Unlock()

	keys, ok := lf.Checksums[target]
	if !ok {
		return true
	}
	oldHash, ok := keys[key]
	if !ok {
		return true
	}
	return oldHash != Hash(sourceContent)
}

// Update records the checksum of a source string after successful translation.
func (lf *LockFile) Update(target, key, sourceContent string) {
	lf.mu.Lock()
	defer lf.mu.Unlock()

	if lf.Checksums[target] == nil {
		lf.Checksums[target] = make(map[string]string)
	}
	lf.Checksums[target][key] = Hash(sourceContent)
}

// UpdateBatch records checksums for multiple keys at once.
func (lf *LockFile) UpdateBatch(target string, entries map[string]string) {
	lf.mu.Lock()
	defer lf.mu.Unlock()

	if lf.Checksums[target] == nil {
		lf.Checksums[target] = make(map[string]string)
	}
	for key, sourceContent := range entries {
		lf.Checksums[target][key] = Hash(sourceContent)
	}
}

// FilterChanged returns only the keys whose source content has changed
// since the last translation. The input is a map of key -> sourceContent.
// Returns a map of key -> sourceContent for changed entries only.
func (lf *LockFile) FilterChanged(target string, entries map[string]string) map[string]string {
	lf.mu.Lock()
	defer lf.mu.Unlock()

	existing := lf.Checksums[target]
	changed := make(map[string]string)

	for key, content := range entries {
		hash := Hash(content)
		if existing == nil || existing[key] != hash {
			changed[key] = content
		}
	}

	return changed
}

// Clean removes entries from the lock file that are no longer present in
// the current set of keys. This prevents stale entries from accumulating.
func (lf *LockFile) Clean(target string, currentKeys []string) {
	lf.mu.Lock()
	defer lf.mu.Unlock()

	existing := lf.Checksums[target]
	if existing == nil {
		return
	}

	valid := make(map[string]bool, len(currentKeys))
	for _, k := range currentKeys {
		valid[k] = true
	}

	for k := range existing {
		if !valid[k] {
			delete(existing, k)
		}
	}
}

// RemoveTarget removes all checksums for a target.
func (lf *LockFile) RemoveTarget(target string) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	delete(lf.Checksums, target)
}

// ---------------------------------------------------------------------------
// Stats
// ---------------------------------------------------------------------------

// Stats returns the number of targets and total keys in the lock file.
func (lf *LockFile) Stats() (targets, keys int) {
	lf.mu.Lock()
	defer lf.mu.Unlock()

	targets = len(lf.Checksums)
	for _, m := range lf.Checksums {
		keys += len(m)
	}
	return
}

// Targets returns sorted list of target keys.
func (lf *LockFile) Targets() []string {
	lf.mu.Lock()
	defer lf.mu.Unlock()

	targets := make([]string, 0, len(lf.Checksums))
	for t := range lf.Checksums {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	return targets
}

// ---------------------------------------------------------------------------
// PO-specific helpers
// ---------------------------------------------------------------------------

// POEntryKey builds a lock file key from a PO msgid and msgctxt.
// Format: "msgctxt|msgid" or just "msgid" if no context.
func POEntryKey(msgid, msgctxt string) string {
	if msgctxt != "" {
		return msgctxt + "|" + msgid
	}
	return msgid
}

// POEntryContent builds the source content string for hashing.
// Includes msgid and msgid_plural to detect changes in either.
func POEntryContent(msgid, msgidPlural string) string {
	if msgidPlural != "" {
		return msgid + "\x00" + msgidPlural
	}
	return msgid
}

// ---------------------------------------------------------------------------
// Key-value format helpers (i18next, JSON, YAML, properties, ARB)
// ---------------------------------------------------------------------------

// KVEntryContent builds the source content string for a key-value pair.
// The key is included in the hash so renaming a key triggers re-translation.
func KVEntryContent(key, value string) string {
	return key + "\x00" + value
}

// ---------------------------------------------------------------------------
// Markdown helpers
// ---------------------------------------------------------------------------

// MarkdownEntryKey builds a lock file key for a markdown segment.
// Uses the file path relative to the source dir and a segment index.
func MarkdownEntryKey(relPath string, segmentIndex int) string {
	return fmt.Sprintf("%s#%d", filepath.ToSlash(relPath), segmentIndex)
}

// ---------------------------------------------------------------------------
// Android helpers
// ---------------------------------------------------------------------------

// AndroidEntryKey builds a lock file key from an Android string resource.
func AndroidEntryKey(name string) string {
	return name
}

// ---------------------------------------------------------------------------
// Human-readable summary
// ---------------------------------------------------------------------------

// Summary returns a human-readable summary string.
func (lf *LockFile) Summary() string {
	targets, keys := lf.Stats()
	if targets == 0 {
		return "empty"
	}

	var parts []string
	for _, t := range lf.Targets() {
		n := len(lf.Checksums[t])
		parts = append(parts, fmt.Sprintf("%s: %d keys", t, n))
	}
	return fmt.Sprintf("%d targets, %d keys (%s)", targets, keys, strings.Join(parts, ", "))
}
