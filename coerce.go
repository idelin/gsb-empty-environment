package oci

import "sort"

func coerceEntries(value any) []Entry {
	switch entries := value.(type) {
	case nil:
		return nil
	case []Entry:
		return append([]Entry(nil), entries...)
	case []*Entry:
		out := make([]Entry, 0, len(entries))
		for _, entry := range entries {
			if entry != nil {
				out = append(out, *entry)
			}
		}
		return out
	case []any:
		out := make([]Entry, 0, len(entries))
		for _, item := range entries {
			switch entry := item.(type) {
			case Entry:
				out = append(out, entry)
			case *Entry:
				if entry != nil {
					out = append(out, *entry)
				}
			}
		}
		return out
	case map[string]Entry:
		keys := make([]string, 0, len(entries))
		for key := range entries {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make([]Entry, 0, len(keys))
		for _, key := range keys {
			entry := entries[key]
			if firstNonEmpty(entry.Path, entry.Name, entry.Filename) == "" {
				entry.Path = key
			}
			out = append(out, entry)
		}
		return out
	case map[string]*Entry:
		keys := make([]string, 0, len(entries))
		for key := range entries {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make([]Entry, 0, len(keys))
		for _, key := range keys {
			entry := entries[key]
			if entry == nil {
				continue
			}
			copied := *entry
			if firstNonEmpty(copied.Path, copied.Name, copied.Filename) == "" {
				copied.Path = key
			}
			out = append(out, copied)
		}
		return out
	default:
		return nil
	}
}
