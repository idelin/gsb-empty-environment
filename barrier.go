package netsec

import (
	"fmt"
	"sync"
)

// Barrier enforces one window's sealing rules. It accepts partition seals
// only as continuous, hash-chained prefixes that stay inside the manifest,
// and refuses to produce a window root until every partition is complete.
// A Barrier is safe for concurrent use by multiple partition submitters.
type Barrier struct {
	mu sync.Mutex

	manifest Manifest
	snap     *snapshot

	// accepted[partition][seq-1] holds the verified seal, or a zero Seal
	// while that slot is still missing.
	accepted map[PartitionID][]Seal

	finalized bool
	result    *Attestation
}

// NewBarrier opens a barrier for the given window manifest. The manifest
// must name at least one partition with no duplicate or empty identifiers.
func NewBarrier(manifest Manifest) (*Barrier, error) {
	snap, err := manifest.compile()
	if err != nil {
		return nil, err
	}
	accepted := make(map[PartitionID][]Seal, len(snap.order))
	for _, id := range snap.order {
		accepted[id] = make([]Seal, snap.specs[id].UpperBound)
	}
	return &Barrier{
		manifest: manifest,
		snap:     snap,
		accepted: accepted,
	}, nil
}

// Window returns the barrier's window ID.
func (b *Barrier) Window() WindowID {
	return b.snap.window
}

// ManifestDigest returns the canonical digest of the window manifest.
func (b *Barrier) ManifestDigest() Digest {
	return b.snap.digest()
}

// Submit validates one seal and admits it into the partition prefix.
//
// It rejects foreign windows and unknown partitions, seals above the
// manifest upper bound, out-of-order seals that would leave a prefix gap,
// seals whose chain linkage is broken, self-digest mismatches and
// conflicting re-seals. Re-submitting the exact same seal is idempotent and
// returns false without an error, which lets at-least-once producers retry
// safely.
func (b *Barrier) Submit(seal Seal) (added bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err := b.validate(seal); err != nil {
		return false, err
	}

	slot := b.accepted[seal.Partition]
	idx := int(seal.Seq - 1)

	if existing := slot[idx]; existing.Seq != 0 {
		if existing.Equal(seal) {
			// Duplicate seal: same generation, same digest. Idempotent no-op.
			return false, nil
		}
		return false, fmt.Errorf("%w: partition %q seq %d",
			ErrDuplicateConflict, seal.Partition, seal.Seq)
	}

	slot[idx] = seal
	return true, nil
}

// validate runs every check that does not depend on slot occupancy. Callers
// hold b.mu.
func (b *Barrier) validate(seal Seal) error {
	if seal.Window != b.snap.window {
		return fmt.Errorf("%w: seal window %d, barrier window %d",
			ErrForeignWindow, seal.Window, b.snap.window)
	}
	spec, ok := b.snap.specs[seal.Partition]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownPartition, seal.Partition)
	}
	if seal.Seq == 0 || seal.Seq > spec.UpperBound {
		return fmt.Errorf("%w: partition %q seq %d (bound %d)",
			ErrSequenceAboveBound, seal.Partition, seal.Seq, spec.UpperBound)
	}
	if !seal.hasValidDigest() {
		return fmt.Errorf("%w: partition %q seq %d",
			ErrBadDigest, seal.Partition, seal.Seq)
	}

	// A slot already occupied by a different seal is a generation conflict;
	// check this before chain linkage so retries get the precise error.
	idx := int(seal.Seq - 1)
	if existing := b.accepted[seal.Partition][idx]; existing.Seq != 0 && !existing.Equal(seal) {
		return fmt.Errorf("%w: partition %q seq %d",
			ErrDuplicateConflict, seal.Partition, seal.Seq)
	}

	// Chain continuity: seq 1 must anchor on the manifest anchor, later
	// seals must link to the immediately preceding seal's digest.
	var wantPrev Digest
	switch {
	case seal.Seq == 1:
		wantPrev = spec.Anchor
	default:
		prev := b.accepted[seal.Partition][int(seal.Seq)-2]
		if prev.Seq == 0 {
			return fmt.Errorf("%w: partition %q next seq %d",
				ErrSequenceGap, seal.Partition, seal.Seq)
		}
		wantPrev = prev.Digest
	}
	if seal.PrevDigest != wantPrev {
		return fmt.Errorf("%w: partition %q seq %d",
			ErrChainBroken, seal.Partition, seal.Seq)
	}
	return nil
}

// SubmitPrefix admits an entire continuous prefix for one partition. Seals
// are validated and stored in order; on failure the barrier retains only the
// seals already accepted before the bad one, so the producer must fill the
// gap and retry. The proof must match the manifest: starting at seq 1 and
// ending exactly at the partition's upper bound.
func (b *Barrier) SubmitPrefix(proof PrefixProof) error {
	b.mu.Lock()
	spec, ok := b.snap.specs[proof.Partition]
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownPartition, proof.Partition)
	}
	if uint64(len(proof.Seals)) != uint64(spec.UpperBound) {
		return fmt.Errorf("%w: partition %q has %d seals, upper bound %d",
			ErrIncomplete, proof.Partition, len(proof.Seals), spec.UpperBound)
	}
	for idx, seal := range proof.Seals {
		if seal.Partition != proof.Partition {
			return fmt.Errorf("%w: seal %d carries partition %q, proof partition %q",
				ErrUnknownPartition, idx+1, seal.Partition, proof.Partition)
		}
		if _, err := b.Submit(seal); err != nil {
			return err
		}
	}
	return nil
}

// Missing reports the partition prefixes that are not yet fully proven,
// each with the smallest sequence number still absent. An empty result means
// every manifest partition reached its upper bound.
func (b *Barrier) Missing() map[PartitionID]Seq {
	b.mu.Lock()
	defer b.mu.Unlock()

	missing := make(map[PartitionID]Seq)
	for _, id := range b.snap.order {
		slot := b.accepted[id]
		for idx, seal := range slot {
			if seal.Seq == 0 {
				missing[id] = Seq(idx + 1)
				break
			}
		}
	}
	return missing
}

// Ready reports whether every partition has proven its full prefix.
func (b *Barrier) Ready() bool {
	return len(b.Missing()) == 0
}

// Progress reports the number of accepted seals and the manifest upper bound
// for one partition.
func (b *Barrier) Progress(partition PartitionID) (accepted, upperBound Seq, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	spec, ok := b.snap.specs[partition]
	if !ok {
		return 0, 0, fmt.Errorf("%w: %q", ErrUnknownPartition, partition)
	}
	var count Seq
	for _, seal := range b.accepted[partition] {
		if seal.Seq == 0 {
			break
		}
		count++
	}
	return count, spec.UpperBound, nil
}

// Roots returns the terminal digest of each partition's prefix, in the
// canonical sorted partition order used for the aggregate root. It returns
// ErrIncomplete while any prefix has a gap.
func (b *Barrier) Roots() ([]PartitionRoot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.readyLocked() {
		return nil, ErrIncomplete
	}
	return b.rootsLocked(), nil
}

func (b *Barrier) readyLocked() bool {
	for _, id := range b.snap.order {
		for _, seal := range b.accepted[id] {
			if seal.Seq == 0 {
				return false
			}
		}
	}
	return true
}

// terminalRoot returns the final prefix digest for a partition. A partition
// with an upper bound of zero contributes its manifest anchor, since it has
// no records inside the window.
func (b *Barrier) terminalRoot(id PartitionID) PartitionRoot {
	slot := b.accepted[id]
	if len(slot) == 0 {
		return PartitionRoot{Partition: id, LastDigest: b.snap.specs[id].Anchor}
	}
	return PartitionRoot{Partition: id, LastDigest: slot[len(slot)-1].Digest}
}

func (b *Barrier) rootsLocked() []PartitionRoot {
	roots := make([]PartitionRoot, 0, len(b.snap.order))
	for _, id := range b.snap.order {
		roots = append(roots, b.terminalRoot(id))
	}
	return roots
}

// Finalize signs the window root. It succeeds only when every partition has
// contributed its full continuous prefix; otherwise it returns ErrIncomplete
// and names the still-missing partitions, so a missing partition can never be
// papered over with another partition's newer root.
//
// When signer is nil the attestation is returned unsigned. Finalize is
// idempotent: once sealed, repeated calls return the same attestation.
func (b *Barrier) Finalize(signer Signer) (*Attestation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.finalized {
		return b.result, nil
	}
	if !b.readyLocked() {
		return nil, fmt.Errorf("%w: missing %v", ErrIncomplete, b.missingLocked())
	}

	roots := b.rootsLocked()

	manifestDigest := b.snap.digest()
	root := AggregateDigest(b.snap.window, manifestDigest, b.snap.prev, roots)

	att := &Attestation{
		Window:         b.snap.window,
		ManifestDigest: manifestDigest,
		PrevRoot:       b.snap.prev,
		Roots:          roots,
		Root:           root,
	}
	if signer != nil {
		sig, err := signer.Sign(b.rootMessageLocked(att))
		if err != nil {
			return nil, fmt.Errorf("barrier: sign window root: %w", err)
		}
		att.Signature = append([]byte(nil), sig...)
		att.KeyID = signer.KeyID()
	}

	b.finalized = true
	b.result = att
	return att, nil
}

func (b *Barrier) missingLocked() []PartitionID {
	var missing []PartitionID
	for _, id := range b.snap.order {
		for _, seal := range b.accepted[id] {
			if seal.Seq == 0 {
				missing = append(missing, id)
				break
			}
		}
	}
	return missing
}

func (b *Barrier) rootMessageLocked(att *Attestation) []byte {
	return rootMessage(att.Window, att.ManifestDigest, att.PrevRoot, att.Roots)
}
