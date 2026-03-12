package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// IndexItem is one source record extracted from source.index.
type IndexItem struct {
	ID     string
	Fields map[string]string
}

func expandIndexSourceTargets(t Target, absRoot string) ([]Target, error) {
	items, err := LoadIndexItemsForTarget(t, absRoot)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return []Target{t}, nil
	}

	out := make([]Target, 0, len(items))
	for _, item := range items {
		cp := t
		cp.Pattern = strings.ReplaceAll(cp.Pattern, "{id}", item.ID)
		cp.TargetPath = strings.ReplaceAll(cp.TargetPath, "{id}", item.ID)
		if cp.Source != nil && cp.Source.Path != "" {
			s := *cp.Source
			s.Path = strings.ReplaceAll(s.Path, "{id}", item.ID)
			cp.Source = &s
		}
		cp.Name = fmt.Sprintf("%s/%s", t.Name, item.ID)
		out = append(out, cp)
	}

	return out, nil
}

// LoadIndexItemsForTarget parses source.index and returns normalized records.
func LoadIndexItemsForTarget(t Target, absRoot string) ([]IndexItem, error) {
	if t.Source == nil || !t.Source.IsIndex() {
		return nil, nil
	}

	indexPath := filepath.Join(absRoot, filepath.FromSlash(t.Source.Index))
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("reading source index %s: %w", indexPath, err)
	}

	recordsPath := t.Source.RecordsPath
	if recordsPath == "" {
		recordsPath = "$"
	}

	recordMaps, err := extractRecordsAtPath(data, recordsPath)
	if err != nil {
		return nil, fmt.Errorf("reading records_path %q in %s: %w", recordsPath, indexPath, err)
	}

	seen := make(map[string]struct{}, len(recordMaps))
	items := make([]IndexItem, 0, len(recordMaps))
	for _, rec := range recordMaps {
		id := scalarToString(rec[t.Source.KeyField])
		if id == "" {
			continue
		}
		if err := validateIndexID(id); err != nil {
			return nil, fmt.Errorf("invalid %q in source index %s: %w", t.Source.KeyField, indexPath, err)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}

		fields := make(map[string]string)
		for _, key := range t.Source.Fields {
			v, ok := rec[key]
			if !ok {
				continue
			}
			s, ok := v.(string)
			if !ok {
				continue
			}
			if strings.TrimSpace(s) == "" {
				continue
			}
			fields[key] = s
		}

		items = append(items, IndexItem{ID: id, Fields: fields})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func validateIndexID(id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return fmt.Errorf("id must not be empty")
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return fmt.Errorf("id %q must not contain path separators", trimmed)
	}
	if trimmed == "." || trimmed == ".." || strings.Contains(trimmed, "..") {
		return fmt.Errorf("id %q must not contain dot path segments", trimmed)
	}
	return nil
}

// SplitExpandedTargetName parses names created by expandTargetIDs.
// Returns base target name and id part for names like "base/id".
func SplitExpandedTargetName(name string) (string, string, bool) {
	i := strings.LastIndex(name, "/")
	if i <= 0 || i+1 >= len(name) {
		return "", "", false
	}
	return name[:i], name[i+1:], true
}

func extractRecordsAtPath(data []byte, recordsPath string) ([]map[string]any, error) {
	if recordsPath == "$" {
		var records []map[string]any
		if err := json.Unmarshal(data, &records); err != nil {
			return nil, err
		}
		return records, nil
	}

	if strings.HasPrefix(recordsPath, "$.") {
		field := strings.TrimPrefix(recordsPath, "$.")
		if field == "" || strings.Contains(field, ".") {
			return nil, fmt.Errorf("unsupported records_path %q (supported: $, $.field)", recordsPath)
		}

		var obj map[string]any
		if err := json.Unmarshal(data, &obj); err != nil {
			return nil, err
		}
		raw, ok := obj[field]
		if !ok {
			return nil, fmt.Errorf("field %q not found", field)
		}
		arr, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("field %q is not an array", field)
		}
		out := make([]map[string]any, 0, len(arr))
		for _, v := range arr {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, m)
		}
		return out, nil
	}

	return nil, fmt.Errorf("unsupported records_path %q (supported: $, $.field)", recordsPath)
}

func scalarToString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%v", x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", x))
	}
}
