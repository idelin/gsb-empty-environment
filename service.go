package graphsum

import "fmt"

// Stats reports how the service produced its most recent Summary.
type Stats struct {
	// CacheHits counts summaries served from cache without recomputation.
	CacheHits int
	// Reused counts summaries where the previous version's cached result
	// was reused after the independent checker re-verified every optimal
	// numbering against the edited graph.
	Reused int
	// Recomputed counts summaries built by full re-enumeration.
	Recomputed int
	// LastVerifiedNumberings is how many symmetry-equivalent numberings
	// (optimal permutations, i.e. |Aut(g)|) the independent checker
	// re-verified while producing the most recent summary.
	LastVerifiedNumberings int
}

// Service maintains a versioned graph and serves canonical summaries.
// Node IDs are stable identities: they survive edits, and all numberings
// and orbits in summaries refer to them. Every summary served — cached,
// reused, or freshly computed — is checked by the independent permutation
// verifier, which re-verifies all numberings related by symmetry.
type Service struct {
	g       *Graph
	version uint64

	// lastEditNoop records whether the most recent edit left the structure
	// unchanged; only then is the previous cache entry eligible for reuse,
	// and only after re-verification of all symmetry-affected numberings.
	lastEditNoop bool

	cached        *Summary
	cachedVersion uint64

	stats Stats
}

// NewService wraps g (the service takes ownership; mutate via the service).
func NewService(g *Graph) *Service {
	return &Service{g: g}
}

// Version returns the current graph version; every edit bumps it.
func (s *Service) Version() uint64 { return s.version }

// Stats returns service counters.
func (s *Service) Stats() Stats { return s.stats }

// Graph returns a read-only-by-convention snapshot of the current graph.
func (s *Service) Graph() *Graph { return s.g.Clone() }

// Summary returns the canonical summary for the current version.
//
// Cache policy: a summary cached for this exact version is served directly.
// If the last edit was structurally a no-op (e.g. re-setting an identical
// label), the previous version's cached summary is reused — but only after
// the independent checker re-verifies every symmetry-equivalent numbering
// against the current graph. Otherwise the summary is recomputed by exact
// enumeration and then independently verified.
func (s *Service) Summary() (*Summary, error) {
	if s.cached != nil && s.cachedVersion == s.version {
		s.stats.CacheHits++
		return s.cached, nil
	}
	if s.cached != nil && s.cachedVersion == s.version-1 && s.lastEditNoop {
		if err := Verify(s.g, s.cached); err != nil {
			return nil, fmt.Errorf("cached summary failed re-verification at version %d: %v", s.version, err)
		}
		s.stats.Reused++
		s.stats.LastVerifiedNumberings = s.countOptimalNumberings()
		s.cachedVersion = s.version
		return s.cached, nil
	}
	sum, err := Canonical(s.g)
	if err != nil {
		return nil, err
	}
	if err := Verify(s.g, sum); err != nil {
		return nil, fmt.Errorf("fresh summary failed verification at version %d: %v", s.version, err)
	}
	s.stats.Recomputed++
	s.stats.LastVerifiedNumberings = countOptimal(s.g, sum)
	s.cached = sum
	s.cachedVersion = s.version
	return sum, nil
}

// countOptimalNumberings reports how many numberings attain the cached
// encoding — the numberings the verifier effectively re-checks.
func (s *Service) countOptimalNumberings() int {
	if s.cached == nil {
		return 0
	}
	return countOptimal(s.g, s.cached)
}

// countOptimal counts numberings whose encoding equals the summary's.
func countOptimal(g *Graph, sum *Summary) int {
	ids := g.NodeIDs()
	count := 0
	permutations(ids, func(perm []int) bool {
		if g.encode(perm) == sum.Encoding {
			count++
		}
		return true
	})
	return count
}

// bump registers an edit: the version always increments; the cache stays
// eligible for reuse only when the edit did not change the structure.
func (s *Service) bump(changed bool) {
	s.version++
	s.lastEditNoop = !changed
}

// AddNode adds a node with a stable identity.
func (s *Service) AddNode(id int, label string) error {
	if err := s.g.AddNode(id, label); err != nil {
		return err
	}
	s.bump(true)
	return nil
}

// RemoveNode removes a node and its incident edges.
func (s *Service) RemoveNode(id int) error {
	if err := s.g.RemoveNode(id); err != nil {
		return err
	}
	s.bump(true)
	return nil
}

// SetLabel relabels a node; re-setting the same label is a no-op edit.
func (s *Service) SetLabel(id int, label string) error {
	changed, err := s.g.SetLabel(id, label)
	if err != nil {
		return err
	}
	s.bump(changed)
	return nil
}

// SetRoot moves the root; re-setting the same root is a no-op edit.
func (s *Service) SetRoot(id int) error {
	changed, err := s.g.SetRoot(id)
	if err != nil {
		return err
	}
	s.bump(changed)
	return nil
}

// AddEdge adds one parallel edge.
func (s *Service) AddEdge(from, to int, label string) error {
	if err := s.g.AddEdge(from, to, label); err != nil {
		return err
	}
	s.bump(true)
	return nil
}

// RemoveEdge removes one parallel edge; removing a non-existent edge is a
// no-op edit.
func (s *Service) RemoveEdge(from, to int, label string) error {
	s.bump(s.g.RemoveEdge(from, to, label))
	return nil
}
