package readrepair

import "sort"

// ReadResult is what the coordinator returns to the caller.
type ReadResult struct {
	// Versions is the merged sibling set. A single tombstone means the key
	// is deleted; multiple versions are concurrent branches (siblings).
	Versions []Version
}

// Value returns the winning value. ok is false when the key is deleted or
// absent. When several concurrent branches exist, ok is false and the
// caller must resolve Siblings instead — no branch is silently dropped.
func (r ReadResult) Value() (string, bool) {
	if len(r.Versions) == 1 && !r.Versions[0].Tombstone {
		return r.Versions[0].Value, true
	}
	return "", false
}

// Siblings returns the concurrent live (non-tombstone) branches, if any.
func (r ReadResult) Siblings() []string {
	var out []string
	for _, v := range r.Versions {
		if !v.Tombstone {
			out = append(out, v.Value)
		}
	}
	sort.Strings(out)
	return out
}

// Coordinator performs quorum reads across replicas and asynchronously
// repairs lagging ones.
type Coordinator struct {
	Nodes []*Node
	// R is how many responses a read waits for before returning.
	R int
}

type getResponse struct {
	nodeIdx int
	vs      []Version
}

// Read queries all replicas, waits for R responses (simulated latency may
// make some arrive late), merges them into a winning sibling set, then
// asynchronously sends conditional repairs to every replica whose observed
// state does not already cover the merged result.
func (c *Coordinator) Read(key string) ReadResult {
	responses := make(chan getResponse, len(c.Nodes))
	for i, n := range c.Nodes {
		go func(idx int, node *Node) {
			responses <- getResponse{nodeIdx: idx, vs: node.Get(key)}
		}(i, n)
	}

	r := c.R
	if r <= 0 || r > len(c.Nodes) {
		r = len(c.Nodes)
	}

	merged := make([]Version, 0)
	replied := make(map[int]bool)
	for i := 0; i < r; i++ {
		resp := <-responses
		replied[resp.nodeIdx] = true
		merged = mergeVersions(merged, resp.vs)
	}

	// Asynchronously collect late responses, fold them into the final
	// result, and repair any replica that is behind. Repairs are
	// conditional merges, so a replica updated in the meantime keeps its
	// newer/concurrent state.
	final := merged
	go func() {
		for i := r; i < len(c.Nodes); i++ {
			resp := <-responses
			replied[resp.nodeIdx] = true
			final = mergeVersions(final, resp.vs)
		}
		for idx, node := range c.Nodes {
			if !replied[idx] || !covers(node.state(key), final) {
				node.Repair(key, final)
			}
		}
	}()

	return ReadResult{Versions: merged}
}

// covers reports whether held already contains every version in want
// (i.e. nothing in want is newer than or concurrent with held's state).
func covers(held, want []Version) bool {
	for _, w := range want {
		found := false
		for _, h := range held {
			if h.Clock.Equal(w.Clock) || h.Clock.Dominates(w.Clock) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
