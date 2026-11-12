package taskpkg

import (
	"fmt"
	"sort"
)

// Registry supplies every published candidate for a node. Implementations may
// be backed by an index, a mirror snapshot or an in-memory map.
type Registry interface {
	Candidates(node NodeID) ([]Candidate, error)
}

// MapRegistry is an in-memory Registry keyed by node identity. Candidates
// stored under the same path but different sources remain independent.
type MapRegistry struct {
	Data map[NodeID][]Candidate
}

// Candidates implements Registry. It returns a copy so callers cannot mutate
// the registry's ordering while a resolve is in progress.
func (m MapRegistry) Candidates(node NodeID) ([]Candidate, error) {
	found, ok := m.Data[node]
	if !ok {
		return nil, fmt.Errorf("taskpkg: no candidates registered for node %s", node)
	}
	out := append([]Candidate(nil), found...)
	sortCandidatesDesc(out)
	return out, nil
}

// sortCandidatesDesc orders candidates from highest to lowest version; ties
// break on digest for determinism.
func sortCandidatesDesc(cs []Candidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		if c := cs[i].Version.Compare(cs[j].Version); c != 0 {
			return c > 0
		}
		return cs[i].Digest < cs[j].Digest
	})
}
