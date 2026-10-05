// Package model is the independent reference implementation used to check
// the sync protocol. It ignores the protocol entirely and simply full-merges
// every record ever introduced anywhere; converged tables must equal its
// result.
package model

import (
	"fmt"

	"antientropy/member"
)

// FullMerge merges the complete update history pairwise.
func FullMerge(history []member.Record) map[string]member.Record {
	out := make(map[string]member.Record)
	for _, r := range history {
		if cur, ok := out[r.NodeID]; ok {
			out[r.NodeID] = member.Merge(cur, r)
		} else {
			out[r.NodeID] = r
		}
	}
	return out
}

// CheckTable verifies that t contains exactly the full-merge result.
func CheckTable(t *member.Table, want map[string]member.Record) error {
	if t.Len() != len(want) {
		return fmt.Errorf("model: table has %d records, want %d", t.Len(), len(want))
	}
	for id, w := range want {
		got, ok := t.Get(id)
		if !ok {
			return fmt.Errorf("model: missing record for %s", id)
		}
		if got != w {
			return fmt.Errorf("model: %s = %+v, want %+v", id, got, w)
		}
	}
	return nil
}
