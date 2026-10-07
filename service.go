package graphcanon

import "sync"

// Service serves canonical forms for a single mutable graph whose version
// increments on every mutation.
//
// Results are cached per graph version, and the most recent exact
// computation is kept as a reuse candidate for later versions (a local
// modification may leave the structure unchanged, e.g. a no-op SetLabel).
// A cached result is never served blindly: before reuse, its proof is
// re-validated against the current graph state. The revalidation checks
// every symmetry-affected numbering claim:
//
//   - the stored Numbering is a root-fixing permutation that still
//     reproduces the stored Code on the current graph (by injectivity of
//     the encoding this also proves the structure is unchanged, so the
//     stored minimum is still the minimum);
//   - every stored automorphism, conjugated back to stable node
//     identities, is re-verified by the independent permutation checker
//     VerifyPermutation;
//   - the stored Orbits equal the orbits recomputed from the re-verified
//     automorphism group.
//
// If any check fails, the service falls back to exact enumeration, which
// is always affordable at this size (at most 7! numberings).
type Service struct {
	mu    sync.Mutex
	g     *Graph
	cache map[uint64]*Result
	last  *Result

	// Instrumentation, also used by tests.
	computations  int // exact enumerations performed
	revalidations int // cached proofs re-validated
	reuses        int // results reused after revalidation
}

// maxCachedVersions bounds the per-version cache. Versions are monotonic,
// so evicted entries are never needed for correctness.
const maxCachedVersions = 16

// NewService creates a canonical-form service for g.
func NewService(g *Graph) *Service {
	return &Service{g: g, cache: make(map[uint64]*Result)}
}

// Canonical returns the canonical form of the graph at its current
// version, reusing a cached result when independent revalidation proves it
// is still exact, and recomputing exactly otherwise.
func (s *Service) Canonical() *Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	v := s.g.version
	if res, ok := s.cache[v]; ok {
		s.revalidations++
		if revalidate(s.g, res) {
			s.reuses++
			return res
		}
		delete(s.cache, v)
	}
	if s.last != nil {
		s.revalidations++
		if revalidate(s.g, s.last) {
			s.reuses++
			s.put(v, s.last)
			return s.last
		}
	}
	res := Canonicalize(s.g)
	s.computations++
	s.last = res
	s.put(v, res)
	return res
}

func (s *Service) put(v uint64, res *Result) {
	if len(s.cache) >= maxCachedVersions {
		s.cache = make(map[uint64]*Result)
	}
	s.cache[v] = res
}

// revalidate independently re-verifies every symmetry-affected claim of a
// cached result against the current graph state.
func revalidate(g *Graph, res *Result) bool {
	n := g.n
	if len(res.Numbering) != n {
		return false
	}
	seen := make([]bool, n)
	for _, c := range res.Numbering {
		if c < 0 || c >= n || seen[c] {
			return false
		}
		seen[c] = true
	}
	if res.Numbering[g.root] != 0 {
		return false
	}
	// The numbering must still reproduce the claimed canonical code. The
	// encoding is injective, so this also proves the labeled multigraph is
	// identical to the one the code was computed from.
	if encode(g, res.Numbering) != res.Code {
		return false
	}
	// Every claimed automorphism must pass the independent permutation
	// checker when conjugated back to stable node identities.
	inv := invertPerm(res.Numbering)
	for _, sigma := range res.Automorphisms {
		if len(sigma) != n {
			return false
		}
		tau := make([]int, n)
		for orig := 0; orig < n; orig++ {
			tau[orig] = inv[sigma[res.Numbering[orig]]]
		}
		if !VerifyPermutation(g, g, tau) {
			return false
		}
	}
	// The orbits must be exactly the orbits of the re-verified group.
	return orbitsEqual(computeOrbits(n, res.Numbering, res.Automorphisms), res.Orbits)
}

func orbitsEqual(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}
