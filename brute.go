package traj

import "errors"

// ExhaustiveLimit is the maximum edge count for which exhaustive
// permutation checks are performed (10! is the practical ceiling).
const ExhaustiveLimit = 10

// ErrTooManyEdges is returned when an exhaustive check is requested for
// more than ExhaustiveLimit edges.
var ErrTooManyEdges = errors.New("traj: exhaustive check limited to 10 edges")

// BruteForceMinRoute enumerates every permutation of edges and returns
// the lexicographically smallest edge-ID sequence that forms a valid
// walk from start. ok is false when no permutation is a valid walk.
func BruteForceMinRoute(edges []Edge, start string) (best []string, ok bool) {
	if len(edges) == 0 {
		return nil, true
	}
	used := make([]bool, len(edges))
	path := make([]Edge, 0, len(edges))
	var walk func(current string)
	walk = func(current string) {
		if len(path) == len(edges) {
			ids := RouteIDs(path)
			if !ok || lessIDs(ids, best) {
				best = append([]string(nil), ids...)
				ok = true
			}
			return
		}
		for i, e := range edges {
			if used[i] || e.From != current {
				continue
			}
			used[i] = true
			path = append(path, e)
			walk(e.To)
			path = path[:len(path)-1]
			used[i] = false
		}
	}
	walk(start)
	return best, ok
}

// VerifyExhaustive cross-checks a route produced by FindRoute against a
// full permutation enumeration. It reports whether got is exactly the
// lexicographically smallest valid walk over edges from start. Edge
// sets larger than ExhaustiveLimit yield ErrTooManyEdges.
func VerifyExhaustive(edges []Edge, start string, got []Edge) (bool, error) {
	if len(edges) > ExhaustiveLimit {
		return false, ErrTooManyEdges
	}
	best, ok := BruteForceMinRoute(edges, start)
	if !ok {
		return got == nil, nil
	}
	return equalIDs(RouteIDs(got), best), nil
}

func lessIDs(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
