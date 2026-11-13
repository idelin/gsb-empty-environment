package audit

import "strings"

// ShortestViolation returns the shortest interleaving in which the legacy
// callback successfully accesses a resource with an ended/stale identity or
// another request's identity. The BFS explores every enabled action order.
func ShortestViolation(maxDepth int) (Trace, bool) {
	return search(maxDepth, false, nil)
}

// CrossRequestViolation restricts the search to the specific case where the
// callback runs with another live request's identity.
func CrossRequestViolation(maxDepth int) (Trace, bool) {
	return search(maxDepth, false, func(violation string) bool {
		return strings.Contains(violation, "another request")
	})
}

// CoverageViolation finds the shortest violating trace that also exercises:
// nested impersonation, restoration of the outer frame, connection/thread-pool
// reuse, and a remote Delegation call.
func CoverageViolation(maxDepth int) (Trace, bool) {
	return search(maxDepth, true, nil)
}

func search(
	maxDepth int,
	requireCoverage bool,
	accept func(string) bool,
) (Trace, bool) {
	if maxDepth <= 0 {
		maxDepth = 16
	}
	start := newState()
	queue := []*state{start}
	visited := map[string]struct{}{start.key(): {}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if len(current.steps) >= maxDepth {
			continue
		}
		for _, candidate := range allActions {
			next := current.clone()
			applied, violation := next.apply(candidate)
			if !applied {
				continue
			}
			if violation != "" {
				if requireCoverage && !next.flags.complete() {
					continue
				}
				if accept != nil && !accept(violation) {
					continue
				}
				return next.trace(violation, requireCoverage), true
			}
			key := next.key()
			if _, seen := visited[key]; seen {
				continue
			}
			visited[key] = struct{}{}
			queue = append(queue, next)
		}
	}
	return Trace{}, false
}
