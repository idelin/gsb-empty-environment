package migrate

import (
	"encoding/json"
	"strings"
)

// journalName is the root-level file that records an in-flight migration so
// an interrupted two-phase rename can be resumed from the tree state alone.
const journalName = ".migrate-journal"

// journal is the crash-recovery record persisted inside the tree.
//
// The Phase1Done checkpoint matters: during phase 1 no final names exist
// yet, so a node found at its original path is unambiguously the planned
// node. Once phase 2 starts, a finalized node may sit on another entry's
// original path (name swaps), so phase 1 must never be re-entered.
type journal struct {
	Phase1Done bool           `json:"phase1_done"`
	Entries    []journalEntry `json:"entries"`
}

type journalEntry struct {
	Orig  string `json:"orig"`
	Temp  string `json:"temp"`
	Final string `json:"final"`
}

// Apply computes the full mapping for the tree and executes it as a
// crash-safe two-phase rename: every affected node first moves to a unique
// temporary name (recorded in an in-tree journal), then moves to its final
// name. If Apply is interrupted, calling Recover (or Apply again) on the
// tree finishes the migration.
func Apply(t *Tree, norm Normalizer) error {
	p, err := ComputePlan(t, norm)
	if err != nil {
		return err
	}
	if len(p.Renames) == 0 {
		return nil
	}
	if err := p.WriteJournal(t); err != nil {
		return err
	}
	return Recover(t)
}

// WriteJournal records the plan inside the tree so a later Recover call can
// finish or restart the two-phase rename after an interruption.
func (p *Plan) WriteJournal(t *Tree) error {
	j := &journal{Entries: make([]journalEntry, len(p.Renames))}
	for i, r := range p.Renames {
		j.Entries[i] = journalEntry{Orig: r.Orig, Temp: r.Temp, Final: r.Final}
	}
	return writeJournal(t, j)
}

func writeJournal(t *Tree, j *journal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	n := t.Add("/"+journalName, false)
	n.Content = data
	return nil
}

func readJournal(t *Tree) (*journal, error) {
	n, ok := t.Root.Children[journalName]
	if !ok {
		return nil, nil
	}
	var j journal
	if err := json.Unmarshal(n.Content, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

// Recover finishes an interrupted migration recorded in the tree's journal.
// It is idempotent and a no-op when no migration is in flight.
func Recover(t *Tree) error {
	j, err := readJournal(t)
	if err != nil || j == nil {
		return err
	}
	if !j.Phase1Done {
		if err := runPhase1(t, j); err != nil {
			return err
		}
	}
	if err := recoverPhase2(t, j.Entries); err != nil {
		return err
	}
	delete(t.Root.Children, journalName)
	return nil
}

// runPhase1 makes sure every planned node sits at its temporary name, then
// persists the phase-1 checkpoint. Nodes are located by original path,
// falling back to the temporary names of ancestors that were already moved,
// so it tolerates a crash at any point of phase 1.
func runPhase1(t *Tree, j *journal) error {
	origToTemp := map[string]string{}
	for _, e := range j.Entries {
		origToTemp[e.Orig] = e.Temp
	}
	for _, e := range j.Entries {
		n := findNode(t, e.Orig, origToTemp)
		if n == nil || n.Name == e.Temp {
			continue // already moved
		}
		if err := n.Rename(e.Temp); err != nil {
			return err
		}
	}
	j.Phase1Done = true
	return writeJournal(t, j)
}

// recoverPhase2 moves every node from its temporary name to its final name,
// parents before children. Temporary names are unique per parent and
// reserved for planned nodes, so a node found at an entry's temporary path
// is unambiguously that entry's node; missing entries are already done.
func recoverPhase2(t *Tree, entries []journalEntry) error {
	byOrig := map[string]journalEntry{}
	for _, e := range entries {
		byOrig[e.Orig] = e
	}
	tempToFinal := map[string]string{}
	tempPaths := make([]string, len(entries))
	for i, e := range entries {
		tp := tempPath(e, byOrig)
		tempPaths[i] = tp
		tempToFinal[tp] = e.Final
	}
	for i, e := range entries {
		n := findNode(t, tempPaths[i], tempToFinal)
		if n == nil || n.Name == e.Final {
			continue // already finalized
		}
		if err := n.Rename(e.Final); err != nil {
			return err
		}
	}
	return nil
}

// tempPath computes where a planned node lives once phase 1 is complete:
// planned ancestors appear under their temporary names.
func tempPath(e journalEntry, byOrig map[string]journalEntry) string {
	comps := splitPath(e.Orig)
	var b strings.Builder
	prefix := ""
	for _, c := range comps[:len(comps)-1] {
		prefix += "/" + c
		if ae, ok := byOrig[prefix]; ok {
			b.WriteString("/" + ae.Temp)
		} else {
			b.WriteString("/" + c)
		}
	}
	return b.String() + "/" + e.Temp
}

// findNode walks path from the root; when a component is missing it retries
// with the alternate name recorded for that path prefix (temporary names
// during phase 1, final names during phase 2).
func findNode(t *Tree, path string, alt map[string]string) *Node {
	cur := t.Root
	prefix := ""
	for _, comp := range splitPath(path) {
		prefix += "/" + comp
		next := cur.Children[comp]
		if next == nil {
			if a, ok := alt[prefix]; ok {
				next = cur.Children[a]
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}
