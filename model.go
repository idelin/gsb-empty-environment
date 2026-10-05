package gsb

import "fmt"

// FullModel is the independent reference model: the join (full merge) of
// every record in every table. Convergence means every table equals it.
func FullModel(tables ...*Table) map[string]Record {
	out := make(map[string]Record)
	for _, t := range tables {
		for id, r := range t.Snapshot() {
			cur, ok := out[id]
			if !ok || r.Compare(cur) > 0 {
				out[id] = r
			}
		}
	}
	return out
}

// CheckModel compares a table against the reference model and returns a
// description of the first mismatch, or "" if they agree.
func CheckModel(t *Table, model map[string]Record) string {
	snap := t.Snapshot()
	for id, want := range model {
		got, ok := snap[id]
		if !ok {
			return fmt.Sprintf("missing %s (want %+v)", id, want)
		}
		if got != want {
			return fmt.Sprintf("diverged %s: got %+v want %+v", id, got, want)
		}
	}
	for id, got := range snap {
		if _, ok := model[id]; !ok {
			return fmt.Sprintf("extra %s (%+v)", id, got)
		}
	}
	return ""
}
