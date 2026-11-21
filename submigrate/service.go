package submigrate

import (
	"crypto/ed25519"
	"crypto/sha256"
	"sort"
)

// MigrateRequest is the full, self-contained migration input.
type MigrateRequest struct {
	Reader      ReaderID
	Roots       []*PartitionRoot
	Entries     map[PartitionID][]LogEntry
	Receipts    []*Receipt
	Revocations []*Revocation
	Stream      []*VisibleEvent
}

// MigrateResult is the acceptance verdict. Cursor/Diff are populated only for
// StatusResumable; Notifications are always the visible events of the prefix
// that was provably processed.
type MigrateResult struct {
	Status        string
	Reason        string
	Notifications []Notification
	Diff          []Notification
	Cursor        *Cursor
}

// Service runs migrations and signs cursor bindings.
type Service struct {
	serviceKey ed25519.PrivateKey
}

// NewService derives a deterministic migration-service key from seed material.
func NewService(seedMaterial []byte) *Service {
	key := ed25519.NewKeyFromSeed(deriveSeed("submigrate/v1/service-key", seedMaterial))
	return &Service{serviceKey: key}
}

// PublicKey returns the cursor-signing verification key.
func (s *Service) PublicKey() ed25519.PublicKey {
	return s.serviceKey.Public().(ed25519.PublicKey)
}

func resync(reason string) *MigrateResult {
	return &MigrateResult{Status: StatusResync, Reason: reason}
}

func revoked() *MigrateResult {
	return &MigrateResult{Status: StatusRevoked, Reason: ReasonRevoked}
}

type receiptState struct {
	receipt *Receipt
	root    *PartitionRoot
	revoked bool
}

func rootsHash(roots []*PartitionRoot) Digest {
	ordered := append([]*PartitionRoot(nil), roots...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Partition < ordered[j].Partition })
	h := sha256.New()
	h.Write([]byte("submigrate/v1/roots-hash"))
	for _, r := range ordered {
		h.Write(rootPayload(r))
		h.Write(r.Signature)
	}
	var d Digest
	h.Sum(d[:0])
	return d
}

// Migrate validates every signed artifact and produces an acceptance verdict.
func (s *Service) Migrate(req *MigrateRequest) *MigrateResult {
	if req == nil || req.Reader == "" {
		return resync(ReasonReceiptForeignReader)
	}

	// Phase 1: validate roots against the supplied physical histories.
	rootByID := make(map[PartitionID]*PartitionRoot, len(req.Roots))
	for _, root := range req.Roots {
		if root == nil || !verify(root.HostKey, rootPayload(root), root.Signature) {
			return resync(ReasonRootBadSignature)
		}
		entries, ok := req.Entries[root.Partition]
		if !ok || uint64(len(entries)) != root.LogLength || logRootHash(entries) != root.LogRoot {
			return resync(ReasonRootLogMismatch)
		}
		visible := visibleOf(entries)
		if uint64(len(visible)) != root.VisibleLength || visibleChainHash(visible) != root.VisibleChainHead {
			return resync(ReasonRootVisibleMismatch)
		}
		rootByID[root.Partition] = root
	}

	// Phase 2: revocations must themselves be authentic; an authentic one for
	// this reader/partition is an explicit withdrawal.
	revokedSet := make(map[PartitionID]*Revocation)
	for _, rev := range req.Revocations {
		if rev == nil {
			return resync(ReasonRevocationBadSignature)
		}
		root, ok := rootByID[rev.Partition]
		if !ok {
			return resync(ReasonRevocationBadSignature)
		}
		if !verify(root.HostKey, revocationPayload(rev), rev.Signature) {
			return resync(ReasonRevocationBadSignature)
		}
		if rev.Reader != req.Reader {
			continue
		}
		if rev.Epoch != root.Epoch {
			return resync(ReasonReceiptEpochMismatch)
		}
		revokedSet[rev.Partition] = rev
	}

	// Phase 3: validate old receipts. They are the sole scope authority.
	states := make(map[PartitionID]*receiptState)
	for _, rc := range req.Receipts {
		if rc == nil {
			return resync(ReasonReceiptBadSignature)
		}
		if rc.Reader != req.Reader {
			return resync(ReasonReceiptForeignReader)
		}
		root, ok := rootByID[rc.Partition]
		if !ok {
			return resync(ReasonReceiptUnknownRoot)
		}
		if !verify(root.HostKey, receiptPayload(rc), rc.Signature) {
			return resync(ReasonReceiptBadSignature)
		}
		if rc.Epoch != root.Epoch {
			return resync(ReasonReceiptEpochMismatch)
		}
		if rc.AuthorizedPrefix > root.VisibleLength {
			return resync(ReasonReceiptScopeOutOfRoot)
		}
		if rc.ConfirmedPrefix > rc.AuthorizedPrefix {
			return resync(ReasonReceiptConfirmedRange)
		}
		st := &receiptState{receipt: rc, root: root}
		if _, isRevoked := revokedSet[rc.Partition]; isRevoked {
			st.revoked = true
		}
		states[rc.Partition] = st
	}

	// Phase 4: walk the merged stream in host partial order. Only fully
	// verified visible occurrences inside an old grant may enter the prefix.
	mergedHead := make(map[PartitionID]uint64)
	chainAcc := make(map[PartitionID]Digest)
	notifOrder := make([]EventID, 0)
	notifByID := make(map[EventID]*Notification)
	evidence := make([]*VisibleEvent, 0)

	for _, ev := range req.Stream {
		if ev == nil {
			return resync(ReasonOccurrenceBadSignature)
		}
		root, ok := rootByID[ev.Partition]
		if !ok {
			return resync(ReasonOccurrenceUnknownRoot)
		}
		st, hasReceipt := states[ev.Partition]
		if hasReceipt && st.revoked {
			return revoked()
		}
		if !hasReceipt {
			// No old grant: this partition is outside the old authorization.
			return resync(ReasonOccurrenceOutOfScope)
		}

		// Identity and occurrence signatures must both verify against the root key.
		if !verify(root.HostKey, ev.IdentityPayload, ev.IdentitySig) {
			return resync(ReasonOccurrenceIdentityBad)
		}
		if !verify(root.HostKey, ev.OccurrencePayload, ev.OccurrenceSig) {
			return resync(ReasonOccurrenceBadSignature)
		}
		if !bytesEqual(ev.OccurrencePayload,
			occurrencePayload(ev.Partition, ev.VisSeq, ev.EventID, ev.Summary)) {
			return resync(ReasonOccurrenceBadSignature)
		}
		if !bytesEqual(ev.IdentityPayload, identityPayload(ev.EventID, ev.Summary)) {
			return resync(ReasonOccurrenceIdentityBad)
		}

		// Visible chain must be contiguous and match the host root commitment.
		head := mergedHead[ev.Partition]
		if ev.VisSeq != head+1 {
			return resync(ReasonOccurrenceChainGap)
		}
		var chainBase []byte
		chainBase = putString(chainBase, tagVisibleChain)
		prevAcc := chainAcc[ev.Partition]
		chainBase = putBytes(chainBase, prevAcc[:])
		chainAcc[ev.Partition] = sha(append(chainBase, ev.OccurrencePayload...))
		mergedHead[ev.Partition] = ev.VisSeq

		// Authorization gate: visible sequence must stay inside the old grant.
		if ev.VisSeq > st.receipt.AuthorizedPrefix {
			return resync(ReasonOccurrenceOutOfScope)
		}

		// Dedup strictly by signed stable identity; identical summaries with
		// different identities remain distinct events.
		if existing, dup := notifByID[ev.EventID]; dup {
			if existing.Summary != ev.Summary {
				return resync(ReasonIdentitySummaryClash)
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
		evidence = append(evidence, ev)
	}

	// Every touched partition's replayed visible chain must match its root.
	for p, head := range mergedHead {
		if chainAcc[p] != rootByID[p].visibleChainHeadAt(head) {
			return resync(ReasonRootVisibleMismatch)
		}
	}

	notifications := make([]Notification, 0, len(notifOrder))
	for _, id := range notifOrder {
		notifications = append(notifications, *notifByID[id])
	}

	// Diff = in-scope merged events the old reader had not yet confirmed on
	// any partition. The old confirmed set is the union of confirmed prefixes.
	confirmedIDs := make(map[EventID]bool)
	for p, st := range states {
		for _, ev := range visibleOf(req.Entries[p]) {
			if ev.VisSeq <= st.receipt.ConfirmedPrefix {
				confirmedIDs[ev.EventID] = true
			}
		}
	}
	diff := make([]Notification, 0)
	for _, n := range notifications {
		if !confirmedIDs[n.EventID] {
			diff = append(diff, n)
		}
	}

	cursor := s.buildCursor(req.Reader, states, mergedHead, evidence, notifications, rootByID)
	return &MigrateResult{
		Status:        StatusResumable,
		Reason:        ReasonOK,
		Notifications: notifications,
		Diff:          diff,
		Cursor:        cursor,
	}
}

func (s *Service) buildCursor(
	reader ReaderID,
	states map[PartitionID]*receiptState,
	mergedHead map[PartitionID]uint64,
	evidence []*VisibleEvent,
	notifications []Notification,
	rootByID map[PartitionID]*PartitionRoot,
) *Cursor {
	partitions := make([]PartitionID, 0, len(states))
	for p := range states {
		if mergedHead[p] == 0 {
			continue
		}
		partitions = append(partitions, p)
	}
	sort.Slice(partitions, func(i, j int) bool { return partitions[i] < partitions[j] })

	progress := make([]PartitionProgress, 0, len(partitions))
	receipts := make([]*Receipt, 0, len(partitions))
	roots := make([]*PartitionRoot, 0, len(partitions))
	for _, p := range partitions {
		st := states[p]
		progress = append(progress, PartitionProgress{
			Partition:            p,
			Epoch:                st.root.Epoch,
			AuthorizedPrefix:     st.receipt.AuthorizedPrefix,
			ConfirmedVisibleHead: mergedHead[p],
			MergedVisSeq:         mergedHead[p],
		})
		receipts = append(receipts, st.receipt)
		roots = append(roots, st.root)
	}

	cursor := &Cursor{
		Reader:        reader,
		ServiceKey:    []byte(s.PublicKey()),
		Progress:      progress,
		Evidence:      evidence,
		Notifications: notifications,
		Receipts:      receipts,
		RootsHash:     rootsHash(roots),
	}
	cursor.ServiceSig = ed25519.Sign(s.serviceKey, cursorPayload(cursor))
	return cursor
}

func visibleOf(entries []LogEntry) []*VisibleEvent {
	out := make([]*VisibleEvent, 0, len(entries))
	for _, entry := range entries {
		if entry.Visible != nil {
			out = append(out, entry.Visible)
		}
	}
	return out
}

func bytesEqual(a, b []byte) bool {
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
