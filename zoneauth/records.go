package zoneauth

import "sort"

// Record is a single resource record attached to a trie node. Delegation
// nodes carry their NS targets as records with Type == "NS".
type Record struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

func sortedRecords(in []Record) []Record {
	if len(in) == 0 {
		return nil
	}
	out := append([]Record(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Value < out[j].Value
	})
	return out
}

func recordsEqual(a, b []Record) bool {
	sa := sortedRecords(a)
	sb := sortedRecords(b)
	if len(sa) != len(sb) {
		return false
	}
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}
