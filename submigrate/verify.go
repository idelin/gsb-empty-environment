package submigrate

import "sort"

// CursorVerifier checks a cursor using only public material: the cursor
// itself and the host roots it claims to be bound to. It needs no service
// private state and no hidden log entries.
type CursorVerifier struct{}

// NewCursorVerifier constructs the stateless verifier.
func NewCursorVerifier() *CursorVerifier { return &CursorVerifier{} }

// VerifyCursor re-derives every binding and returns true only when:
//   - the service signature is valid for the cursor's own service key;
//   - the reader matches every embedded receipt (no other reader's grant);
//   - every receipt, root and occurrence signature verifies against host keys;
//   - replayed evidence is contiguous per partition and matches root visible
//     chain heads at exactly the claimed progress (scope == old authorization);
//   - notifications are the exact dedup-by-signed-identity projection;
//   - the roots hash matches the supplied roots.
func (CursorVerifier) VerifyCursor(c *Cursor, roots []*PartitionRoot) bool {
	if c == nil {
		return false
	}
	if !verify(c.ServiceKey, cursorPayload(c), c.ServiceSig) {
		return false
	}

	rootByID := make(map[PartitionID]*PartitionRoot, len(roots))
	for _, root := range roots {
		if root == nil || !verify(root.HostKey, rootPayload(root), root.Signature) {
			return false
		}
		rootByID[root.Partition] = root
	}
	// The cursor binds only the roots of partitions it actually migrated.
	boundRoots := make([]*PartitionRoot, 0, len(c.Progress))
	for _, pgr := range c.Progress {
		root, ok := rootByID[pgr.Partition]
		if !ok {
			return false
		}
		boundRoots = append(boundRoots, root)
	}
	if rootsHash(boundRoots) != c.RootsHash {
		return false
	}

	progressByID := make(map[PartitionID]PartitionProgress, len(c.Progress))
	receiptByID := make(map[PartitionID]*Receipt, len(c.Receipts))
	for _, p := range c.Progress {
		if _, dup := progressByID[p.Partition]; dup {
			return false
		}
		progressByID[p.Partition] = p
	}
	for _, rc := range c.Receipts {
		if rc == nil || rc.Reader != c.Reader {
			return false
		}
		root, ok := rootByID[rc.Partition]
		if !ok || !verify(root.HostKey, receiptPayload(rc), rc.Signature) {
			return false
		}
		if rc.Epoch != root.Epoch || rc.AuthorizedPrefix > root.VisibleLength ||
			rc.ConfirmedPrefix > rc.AuthorizedPrefix {
			return false
		}
		receiptByID[rc.Partition] = rc
	}
	if len(progressByID) != len(receiptByID) {
		return false
	}
	for p, pgr := range progressByID {
		rc, ok := receiptByID[p]
		root := rootByID[p]
		if !ok || pgr.Epoch != root.Epoch ||
			pgr.AuthorizedPrefix != rc.AuthorizedPrefix ||
			pgr.ConfirmedVisibleHead > pgr.AuthorizedPrefix ||
			pgr.ConfirmedVisibleHead != pgr.MergedVisSeq {
			return false
		}
	}

	// Replay evidence: signatures, contiguous visible chains, scope cutoff.
	head := make(map[PartitionID]uint64)
	acc := make(map[PartitionID]Digest)
	notifOrder := make([]EventID, 0)
	notifByID := make(map[EventID]*Notification)
	for _, ev := range c.Evidence {
		root, ok := rootByID[ev.Partition]
		if !ok {
			return false
		}
		pgr := progressByID[ev.Partition]
		if !verify(root.HostKey, ev.IdentityPayload, ev.IdentitySig) ||
			!verify(root.HostKey, ev.OccurrencePayload, ev.OccurrenceSig) ||
			!bytesEqual(ev.IdentityPayload, identityPayload(ev.EventID, ev.Summary)) ||
			!bytesEqual(ev.OccurrencePayload,
				occurrencePayload(ev.Partition, ev.VisSeq, ev.EventID, ev.Summary)) {
			return false
		}
		if ev.VisSeq != head[ev.Partition]+1 || ev.VisSeq > pgr.AuthorizedPrefix {
			return false
		}
		var base []byte
		base = putString(base, tagVisibleChain)
		prevAcc := acc[ev.Partition]
		base = putBytes(base, prevAcc[:])
		acc[ev.Partition] = sha(append(base, ev.OccurrencePayload...))
		head[ev.Partition] = ev.VisSeq

		if existing, dup := notifByID[ev.EventID]; dup {
			if existing.Summary != ev.Summary {
				return false
			}
			existing.Sources = append(existing.Sources, Source{Partition: ev.Partition, VisSeq: ev.VisSeq})
		} else {
			notifByID[ev.EventID] = &Notification{
				EventID: ev.EventID,
				Summary: ev.Summary,
				Sources: []Source{{Partition: ev.Partition, VisSeq: ev.VisSeq}},
			}
			notifOrder = append(notifOrder, ev.EventID)
		}
	}

	for p, pgr := range progressByID {
		if head[p] != pgr.ConfirmedVisibleHead {
			return false
		}
		root := rootByID[p]
		if acc[p] != root.visibleChainHeadAt(pgr.ConfirmedVisibleHead) {
			return false
		}
	}

	if len(notifOrder) != len(c.Notifications) {
		return false
	}
	for i, id := range notifOrder {
		got := c.Notifications[i]
		want := *notifByID[id]
		if got.EventID != want.EventID || got.Summary != want.Summary ||
			len(got.Sources) != len(want.Sources) {
			return false
		}
		for j := range got.Sources {
			if got.Sources[j] != want.Sources[j] {
				return false
			}
		}
	}

	// Deterministic ordering is part of the binding contract.
	if !sort.SliceIsSorted(c.Progress, func(i, j int) bool {
		return c.Progress[i].Partition < c.Progress[j].Partition
	}) {
		return false
	}
	return true
}
