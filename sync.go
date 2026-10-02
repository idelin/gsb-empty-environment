package antientropy

import (
	"context"
	"time"
)

// SyncResult summarizes one anti-entropy exchange between two peers.
type SyncResult struct {
	Diff      *DiffResult
	ALearned  []State // states applied to the initiator
	BLearned  []State // states applied to the peer
	Converged bool
}

// SyncStores runs one anti-entropy session between two stores:
// it compares Merkle trees depth by depth, locates the divergent
// leaf buckets, exchanges only those buckets' states and merges
// them last-write-wins in both directions.
func SyncStores(a, b *Store) SyncResult {
	ta := BuildTree(a.Snapshot())
	tb := BuildTree(b.Snapshot())
	diff := Diff(ta, tb)
	res := SyncResult{Diff: diff, Converged: len(diff.Buckets) == 0}
	if res.Converged {
		return res
	}
	aStates := a.StatesInBuckets(diff.Buckets)
	bStates := b.StatesInBuckets(diff.Buckets)
	for _, st := range bStates {
		if a.Apply(st) {
			res.ALearned = append(res.ALearned, st)
		}
	}
	for _, st := range aStates {
		if b.Apply(st) {
			res.BLearned = append(res.BLearned, st)
		}
	}
	return res
}

// AntiEntropyOnce performs a single anti-entropy exchange with a peer.
// Both sides record what they repaired.
func (n *Node) AntiEntropyOnce(peer *Node) SyncResult {
	res := SyncStores(n.Store, peer.Store)
	n.recordRepair(res.ALearned)
	peer.recordRepair(res.BLearned)
	return res
}

// StartAntiEntropy launches the low-frequency background anti-entropy
// goroutine: every interval it picks a random peer and reconciles
// Merkle trees with it, until ctx is cancelled.
func (n *Node) StartAntiEntropy(ctx context.Context, peers []*Node, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if peer := n.pickPeer(peers); peer != nil {
					n.AntiEntropyOnce(peer)
				}
			}
		}
	}()
}
