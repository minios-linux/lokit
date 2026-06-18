// Package merge implements PO file merging logic,
// equivalent to the msgmerge utility.
package merge

import (
	po "github.com/minios-linux/lokit/internal/format/po"
)

// poKey builds a compound map key from msgid and msgctxt so that entries
// with the same msgid but different contexts are never confused.
// Format: "msgctxt\x00msgid" when context is present, otherwise just msgid.
func poKey(msgID, msgCtxt string) string {
	if msgCtxt != "" {
		return msgCtxt + "\x00" + msgID
	}
	return msgID
}

// Merge updates a PO file with entries from a POT template.
// - New entries from the template are added with empty translations.
// - Existing entries that are still in the template are kept.
// - Entries that are no longer in the template are marked obsolete.
// - References and flags are updated from the template.
func Merge(poFile, potFile *po.File) *po.File {
	result := po.NewFile()

	// Keep the PO file's header, update POT-Creation-Date
	result.Header = poFile.Header
	if potFile.Header != nil {
		potCreationDate := potFile.HeaderField("POT-Creation-Date")
		if potCreationDate != "" {
			result.SetHeaderField("POT-Creation-Date", potCreationDate)
		}
	}

	// Build a map of existing translations and obsolete entries to restore.
	// Keys include MsgCtxt to avoid incorrect matches for context-qualified strings.
	existingByKey := make(map[string]*po.Entry)
	obsoleteByKey := make(map[string]*po.Entry)
	for _, e := range poFile.Entries {
		k := poKey(e.MsgID, e.MsgCtxt)
		if e.Obsolete {
			if e.MsgID != "" {
				if _, exists := obsoleteByKey[k]; !exists {
					obsoleteByKey[k] = e
				}
			}
			continue
		}
		existingByKey[k] = e
	}

	// Track which existing entries were matched
	matched := make(map[string]bool)

	// Process template entries in order
	for _, potEntry := range potFile.Entries {
		if potEntry.MsgID == "" {
			continue
		}

		k := poKey(potEntry.MsgID, potEntry.MsgCtxt)

		if existing, ok := existingByKey[k]; ok {
			// Entry exists in both — keep translation, update metadata
			merged := &po.Entry{
				TranslatorComments: existing.TranslatorComments,
				ExtractedComments:  potEntry.ExtractedComments,
				References:         potEntry.References,
				Flags:              mergeFlags(existing.Flags, potEntry.Flags),
				MsgCtxt:            potEntry.MsgCtxt,
				MsgID:              potEntry.MsgID,
				MsgIDPlural:        potEntry.MsgIDPlural,
				MsgStr:             existing.MsgStr,
				MsgStrPlural:       existing.MsgStrPlural,
			}
			result.Entries = append(result.Entries, merged)
			matched[k] = true
		} else if obsolete, ok := obsoleteByKey[k]; ok {
			// Restored entry — reuse translation from a previously obsolete entry.
			// Use mergeFlags so that a fuzzy flag on the obsolete entry is
			// preserved (rather than dropped in favour of bare potEntry.Flags).
			restored := &po.Entry{
				TranslatorComments: obsolete.TranslatorComments,
				ExtractedComments:  potEntry.ExtractedComments,
				References:         potEntry.References,
				Flags:              mergeFlags(obsolete.Flags, potEntry.Flags),
				MsgCtxt:            potEntry.MsgCtxt,
				MsgID:              potEntry.MsgID,
				MsgIDPlural:        potEntry.MsgIDPlural,
				MsgStr:             obsolete.MsgStr,
				MsgStrPlural:       obsolete.MsgStrPlural,
			}
			result.Entries = append(result.Entries, restored)
			matched[k] = true
		} else {
			// New entry from template
			newEntry := &po.Entry{
				ExtractedComments: potEntry.ExtractedComments,
				References:        potEntry.References,
				Flags:             potEntry.Flags,
				MsgCtxt:           potEntry.MsgCtxt,
				MsgID:             potEntry.MsgID,
				MsgIDPlural:       potEntry.MsgIDPlural,
				MsgStr:            "",
				MsgStrPlural:      make(map[int]string),
			}
			result.Entries = append(result.Entries, newEntry)
		}
	}

	// Mark unmatched entries as obsolete
	for _, e := range poFile.Entries {
		if e.MsgID == "" || e.Obsolete {
			continue
		}
		if !matched[poKey(e.MsgID, e.MsgCtxt)] {
			obsolete := *e
			obsolete.Obsolete = true
			// Clear references for obsolete entries
			obsolete.References = nil
			result.Entries = append(result.Entries, &obsolete)
		}
	}

	return result
}

// mergeFlags combines flags from PO and POT, preferring POT format flags
// while keeping PO-specific flags like "fuzzy".
func mergeFlags(poFlags, potFlags []string) []string {
	flagSet := make(map[string]bool)

	// Add PO flags (e.g., fuzzy)
	for _, f := range poFlags {
		flagSet[f] = true
	}

	// Add/override POT flags (format flags)
	for _, f := range potFlags {
		flagSet[f] = true
	}

	var result []string
	// Put fuzzy first if present
	if flagSet["fuzzy"] {
		result = append(result, "fuzzy")
	}
	for f := range flagSet {
		if f != "fuzzy" {
			result = append(result, f)
		}
	}

	return result
}
