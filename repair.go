package antientropy

import (
	"context"
	"sort"
	"time"
)

// SyncResult describes one Merkle-tree anti-entropy round.
type SyncResult struct {
	Applied       []NodeState // entries copied from the remote replica
	ComparedNodes int         // Merkle nodes visited during the traversal
	TotalNodes    int         // total nodes in the tree
	DifferBuckets int         // leaf buckets that actually differed
}

// SyncFrom pulls newer state from remote into s using a depth-limited
// Merkle tree traversal: hashes are compared top-down, equal subtrees are
// pruned, and only differing leaf buckets exchange their entries.
func (s *Store) SyncFrom(remote *Store, depth int) SyncResult {
	localSnap := s.Snapshot()
	remoteSnap := remote.Snapshot()
	localLevels := buildLevels(localSnap, depth)
	remoteLevels := buildLevels(remoteSnap, depth)

	total := 0
	for _, lv := range localLevels {
		total += len(lv)
	}

	type cursor struct{ level, idx int }
	differBuckets := map[int]struct{}{}
	compared := 0
	queue := []cursor{{0, 0}}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		compared++
		if localLevels[c.level][c.idx] == remoteLevels[c.level][c.idx] {
			continue // identical subtree: prune
		}
		if c.level == depth {
			differBuckets[c.idx] = struct{}{}
			continue
		}
		queue = append(queue,
			cursor{c.level + 1, c.idx * 2},
			cursor{c.level + 1, c.idx*2 + 1})
	}

	res := SyncResult{ComparedNodes: compared, TotalNodes: total, DifferBuckets: len(differBuckets)}
	buckets := make([]int, 0, len(differBuckets))
	for b := range differBuckets {
		buckets = append(buckets, b)
	}
	sort.Ints(buckets)
	for _, b := range buckets {
		localEntries := entriesInBucket(localSnap, b, depth)
		remoteEntries := entriesInBucket(remoteSnap, b, depth)
		keys := map[string]struct{}{}
		for k := range localEntries {
			keys[k] = struct{}{}
		}
		for k := range remoteEntries {
			keys[k] = struct{}{}
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			rv, rok := remoteEntries[k]
			lv, lok := localEntries[k]
			if rok && (!lok || rv.Version > lv.Version) {
				if s.Apply(rv) {
					res.Applied = append(res.Applied, rv)
				}
			}
		}
	}
	return res
}

// Repairer is the background anti-entropy coroutine: it periodically
// reconciles the local replica with its peers in both directions.
type Repairer struct {
	Local    *Store
	Peers    []*Store
	Depth    int
	Interval time.Duration
	Logf     func(format string, args ...any)
}

func (r *Repairer) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// SyncOnce runs one full anti-entropy round against every peer.
func (r *Repairer) SyncOnce() {
	for _, peer := range r.Peers {
		res := r.Local.SyncFrom(peer, r.Depth)
		if len(res.Applied) > 0 {
			r.logf("[anti-entropy] pulled %d entr(ies) from peer after comparing %d/%d merkle nodes (%d differing leaf buckets)",
				len(res.Applied), res.ComparedNodes, res.TotalNodes, res.DifferBuckets)
			for _, st := range res.Applied {
				r.logf("[anti-entropy] repaired: %s -> %s (v%d)", st.ID, st.Status, st.Version)
			}
		}
		// Push back so both replicas converge after one round.
		peer.SyncFrom(r.Local, r.Depth)
	}
}

// Run starts the periodic background repair loop until ctx is cancelled.
func (r *Repairer) Run(ctx context.Context) {
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.SyncOnce()
		}
	}
}
