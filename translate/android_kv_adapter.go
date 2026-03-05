package translate

import (
	"fmt"

	"github.com/minios-linux/lokit/internal/format/android"
)

type androidUnitKind int

const (
	androidUnitString androidUnitKind = iota
	androidUnitArrayItem
	androidUnitPlural
)

type androidKVUnit struct {
	key      string
	name     string
	kind     androidUnitKind
	itemIdx  int
	quantity string
}

type androidKVFile struct {
	target *android.File
	source *android.File
	units  []androidKVUnit
	index  map[string]androidKVUnit
}

func newAndroidKVFile(target, source *android.File) *androidKVFile {
	if source == nil {
		source = target
	}
	units := buildAndroidKVUnits(source)
	index := make(map[string]androidKVUnit, len(units))
	for _, unit := range units {
		index[unit.key] = unit
	}
	return &androidKVFile{target: target, source: source, units: units, index: index}
}

func buildAndroidKVUnits(f *android.File) []androidKVUnit {
	if f == nil {
		return nil
	}
	units := make([]androidKVUnit, 0)
	for _, e := range f.Entries {
		if !e.IsTranslatable() || e.IsComment() {
			continue
		}
		switch e.Kind {
		case android.KindString:
			units = append(units, androidKVUnit{key: e.Name, name: e.Name, kind: androidUnitString})
		case android.KindStringArray:
			for idx := range e.Items {
				units = append(units, androidKVUnit{
					key:     fmt.Sprintf("%s[%d]", e.Name, idx),
					name:    e.Name,
					kind:    androidUnitArrayItem,
					itemIdx: idx,
				})
			}
		case android.KindPlurals:
			for _, q := range e.PluralOrder {
				units = append(units, androidKVUnit{
					key:      fmt.Sprintf("%s#%s", e.Name, q),
					name:     e.Name,
					kind:     androidUnitPlural,
					quantity: q,
				})
			}
		}
	}
	return units
}

func (f *androidKVFile) Keys() []string {
	keys := make([]string, 0, len(f.units))
	for _, unit := range f.units {
		keys = append(keys, unit.key)
	}
	return keys
}

func (f *androidKVFile) UntranslatedKeys() []string {
	keys := make([]string, 0)
	for _, unit := range f.units {
		if f.valueForUnit(f.target, unit) == "" {
			keys = append(keys, unit.key)
		}
	}
	return keys
}

func (f *androidKVFile) Set(key, value string) bool {
	unit, ok := f.index[key]
	if !ok {
		return false
	}
	entry := f.target.GetEntry(unit.name)
	if entry == nil {
		return false
	}

	switch unit.kind {
	case androidUnitString:
		return f.target.Set(unit.name, value)
	case androidUnitArrayItem:
		items := append([]string(nil), entry.Items...)
		if unit.itemIdx < 0 || unit.itemIdx >= len(items) {
			return false
		}
		items[unit.itemIdx] = value
		return f.target.SetItems(unit.name, items)
	case androidUnitPlural:
		forms := make(map[string]string, len(entry.Plurals))
		for q, v := range entry.Plurals {
			forms[q] = v
		}
		forms[unit.quantity] = value
		return f.target.SetPlurals(unit.name, forms)
	default:
		return false
	}
}

func (f *androidKVFile) Stats() (total int, translated int, pct float64) {
	total = len(f.units)
	for _, unit := range f.units {
		if f.valueForUnit(f.target, unit) != "" {
			translated++
		}
	}
	if total > 0 {
		pct = float64(translated) / float64(total) * 100
	}
	return
}

func (f *androidKVFile) SourceValues() map[string]string {
	vals := make(map[string]string, len(f.units))
	for _, unit := range f.units {
		vals[unit.key] = f.valueForUnit(f.source, unit)
	}
	return vals
}

func (f *androidKVFile) WriteFile(path string) error {
	return f.target.WriteTargetFile(path)
}

func (f *androidKVFile) valueForUnit(file *android.File, unit androidKVUnit) string {
	if file == nil {
		return ""
	}
	entry := file.GetEntry(unit.name)
	if entry == nil {
		return ""
	}
	switch unit.kind {
	case androidUnitString:
		return entry.Value
	case androidUnitArrayItem:
		if unit.itemIdx >= 0 && unit.itemIdx < len(entry.Items) {
			return entry.Items[unit.itemIdx]
		}
	case androidUnitPlural:
		return entry.Plurals[unit.quantity]
	}
	return ""
}
