package disclosure

import (
	"fmt"
	"sort"
)

// Bucket classifies an effective record relative to the query interval.
type Bucket uint8

const (
	// Definite records are not erased and their complete event interval lies
	// inside the query interval.
	Definite Bucket = iota
	// Possible records intersect the query interval but are not wholly inside
	// it.
	Possible
	// Erased records intersect the query interval and have an accepted,
	// authorized tombstone in their version chain.
	Erased
)

func (b Bucket) String() string {
	switch b {
	case Definite:
		return "definite"
	case Possible:
		return "possible"
	case Erased:
		return "erased"
	default:
		return "unknown"
	}
}

// Record is one independently reconstructed effective record. BodyKnown is
// false both for legal erasure and for a selectively withheld body; in either
// case the record itself is still present in the result.
type Record struct {
	RootSeq        uint64   `json:"root_seq"`
	VersionSeq     uint64   `json:"version_seq"`
	Tenant         string   `json:"tenant"`
	EventStart     int64    `json:"event_start"`
	EventEnd       int64    `json:"event_end"`
	Bucket         Bucket   `json:"bucket"`
	Erased         bool     `json:"erased"`
	BodyCommitment Hash     `json:"body_commitment"`
	Body           []byte   `json:"body"`
	BodyKnown      bool     `json:"body_known"`
	TombstoneSeq   uint64   `json:"tombstone_seq,omitempty"`
	LeafSeqs       []uint64 `json:"leaf_seqs"`
}

// Result is the verified three-way partition of the query interval.
type Result struct {
	Query              Query    `json:"query"`
	CheckpointDigest   Hash     `json:"checkpoint_digest"`
	Definite           []Record `json:"definite"`
	Possible           []Record `json:"possible"`
	Erased             []Record `json:"erased"`
	VerifiedLeaves     int      `json:"verified_leaves"`
	VerifiedIndexItems int      `json:"verified_index_items"`
}

// Records returns the three buckets in classification order.
func (r *Result) Records() []Record {
	if r == nil {
		return nil
	}
	out := make([]Record, 0, len(r.Definite)+len(r.Possible)+len(r.Erased))
	out = append(out, r.Definite...)
	out = append(out, r.Possible...)
	out = append(out, r.Erased...)
	return out
}

// VerifyOptions configures trust decisions that cannot be inferred from tree
// roots alone.
type VerifyOptions struct {
	// VerifyCheckpoint authenticates the signed checkpoint. It must be set by
	// an audit host; without it, root binding detects mixed trees but does not
	// authenticate the checkpoint publisher.
	VerifyCheckpoint func(Checkpoint) error
	// AuthorizeTombstone validates opaque authorization bound to target. If
	// nil, a non-empty authorization is accepted (the required structure is
	// still checked).
	AuthorizeTombstone func(target Hash, authorization []byte) error
}

// VerifyDisclosure verifies provenance, index completeness, correction
// closure, tombstone authorization, and reconstructs every record that could
// fall in the query.
func VerifyDisclosure(disclosure *Disclosure, options VerifyOptions) (*Result, error) {
	if disclosure == nil {
		return nil, fmt.Errorf("%w: nil disclosure", ErrInvalidProof)
	}
	checkpoint := disclosure.Checkpoint
	if err := checkpoint.Validate(); err != nil {
		return nil, err
	}
	if options.VerifyCheckpoint != nil {
		if err := options.VerifyCheckpoint(checkpoint); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCheckpointAuthentication, err)
		}
	}
	if err := disclosure.Query.Validate(); err != nil {
		return nil, err
	}

	leaves, err := verifyLogProof(checkpoint, disclosure.LogProof, disclosure.Leaves)
	if err != nil {
		return nil, err
	}
	entries, err := verifyIndexRange(checkpoint, disclosure.Query, disclosure.IndexProof)
	if err != nil {
		return nil, err
	}

	children, err := validateDisclosedVersionGraph(leaves)
	if err != nil {
		return nil, err
	}

	authorize := options.AuthorizeTombstone
	if authorize == nil {
		authorize = defaultTombstoneAuthorizer
	}

	result := &Result{
		Query:              disclosure.Query,
		CheckpointDigest:   checkpoint.Digest(),
		VerifiedLeaves:     len(leaves),
		VerifiedIndexItems: len(entries),
	}
	seenRoots := make(map[uint64]struct{})
	for _, entry := range entries {
		if entry.Key.Tenant != disclosure.Query.Tenant || entry.Key.Start > disclosure.Query.To {
			continue
		}
		if entry.Key.End < disclosure.Query.From {
			continue
		}
		if _, duplicate := seenRoots[entry.Key.RootSeq]; duplicate {
			return nil, fmt.Errorf("%w: duplicate effective record root %d", ErrCorrectionMismatch, entry.Key.RootSeq)
		}
		seenRoots[entry.Key.RootSeq] = struct{}{}

		record, chain, err := reconstructRecord(entry, leaves, authorize)
		if err != nil {
			return nil, err
		}
		if later, exists := children[record.VersionSeq]; exists {
			return nil, fmt.Errorf("%w: index seq %d is stale; correction seq %d continues the chain",
				ErrCorrectionMismatch, record.VersionSeq, later)
		}
		record.LeafSeqs = make([]uint64, 0, len(chain)+1)
		for _, leaf := range chain {
			record.LeafSeqs = append(record.LeafSeqs, leaf.Seq)
		}
		if record.Erased {
			record.LeafSeqs = append(record.LeafSeqs, record.TombstoneSeq)
		}
		sort.Slice(record.LeafSeqs, func(i, j int) bool { return record.LeafSeqs[i] < record.LeafSeqs[j] })

		switch {
		case record.Erased:
			record.Bucket = Erased
			result.Erased = append(result.Erased, record)
		case record.EventStart >= disclosure.Query.From && record.EventEnd <= disclosure.Query.To:
			record.Bucket = Definite
			result.Definite = append(result.Definite, record)
		default:
			record.Bucket = Possible
			result.Possible = append(result.Possible, record)
		}
	}

	sortRecords(result.Definite)
	sortRecords(result.Possible)
	sortRecords(result.Erased)
	return result, nil
}

// VerifyDisclosureAt additionally binds the response to an expected
// checkpoint, including its signature bytes when both carry one.
func VerifyDisclosureAt(disclosure *Disclosure, expected Checkpoint, options VerifyOptions) (*Result, error) {
	if disclosure == nil {
		return nil, fmt.Errorf("%w: nil disclosure", ErrInvalidProof)
	}
	got := disclosure.Checkpoint
	if got.BaseSeq != expected.BaseSeq ||
		got.Size != expected.Size ||
		got.LogRoot != expected.LogRoot ||
		got.IndexSize != expected.IndexSize ||
		got.IndexRoot != expected.IndexRoot ||
		(len(expected.Signature) > 0 && string(got.Signature) != string(expected.Signature)) {
		return nil, ErrCrossRoot
	}
	return VerifyDisclosure(disclosure, options)
}

func verifyLogProof(checkpoint Checkpoint, proof LogProof, leaves []Leaf) (map[uint64]Leaf, error) {
	if proof.BaseSeq != checkpoint.BaseSeq || proof.Size != checkpoint.Size || proof.Root != checkpoint.LogRoot {
		return nil, ErrCrossRoot
	}
	if uint64(len(proof.Proofs)) != checkpoint.Size || uint64(len(leaves)) != checkpoint.Size {
		// A disclosure is allowed to be partial, but then it must still contain
		// every leaf it claims. The proofs and leaves must be one-to-one.
		if len(proof.Proofs) != len(leaves) {
			return nil, fmt.Errorf("%w: proof/leaf count mismatch", ErrInvalidProof)
		}
	}
	byProof := make(map[uint64]LeafProof, len(proof.Proofs))
	for _, item := range proof.Proofs {
		if checkpoint.Size == 0 || item.Seq < checkpoint.BaseSeq {
			return nil, fmt.Errorf("%w: seq %d outside checkpoint", ErrInvalidProof, item.Seq)
		}
		last, _ := checkpoint.LastSeq()
		if item.Seq > last {
			return nil, fmt.Errorf("%w: seq %d outside checkpoint", ErrInvalidProof, item.Seq)
		}
		if _, duplicate := byProof[item.Seq]; duplicate {
			return nil, fmt.Errorf("%w: duplicate seq %d", ErrInvalidProof, item.Seq)
		}
		byProof[item.Seq] = item
	}
	bySeq := make(map[uint64]Leaf, len(leaves))
	for _, leaf := range leaves {
		if err := validateLeafShape(leaf); err != nil {
			return nil, fmt.Errorf("%w: seq %d: %w", ErrInvalidLeaf, leaf.Seq, err)
		}
		item, exists := byProof[leaf.Seq]
		if !exists {
			return nil, fmt.Errorf("%w: no proof for seq %d", ErrInvalidProof, leaf.Seq)
		}
		if _, duplicate := bySeq[leaf.Seq]; duplicate {
			return nil, fmt.Errorf("%w: duplicate leaf seq %d", ErrInvalidLeaf, leaf.Seq)
		}
		digest := leaf.Digest()
		if item.Digest != digest {
			return nil, fmt.Errorf("%w: leaf digest mismatch at seq %d", ErrInvalidProof, leaf.Seq)
		}
		pos := leaf.Seq - checkpoint.BaseSeq
		root, err := merkleRootFromProof(pos, checkpoint.Size, digest, item.Path)
		if err != nil {
			return nil, err
		}
		if root != checkpoint.LogRoot {
			return nil, fmt.Errorf("%w: seq %d does not lead to log root", ErrCrossRoot, leaf.Seq)
		}
		bySeq[leaf.Seq] = leaf
	}
	if len(bySeq) != len(byProof) {
		return nil, fmt.Errorf("%w: returned leaves do not match proofs", ErrInvalidProof)
	}
	return bySeq, nil
}

func verifyIndexRange(checkpoint Checkpoint, query Query, proof IndexRangeProof) ([]IndexEntry, error) {
	if proof.Root != checkpoint.IndexRoot || proof.IndexSize != checkpoint.IndexSize {
		return nil, ErrCrossRoot
	}
	if proof.IndexSize == 0 {
		if proof.LeftPos != 0 || proof.Count != 0 || len(proof.Entries) != 0 ||
			proof.LeftBoundary != nil || proof.RightBoundary != nil {
			return nil, fmt.Errorf("%w: non-empty proof for empty index", ErrIncompleteIndexRange)
		}
		return nil, nil
	}
	if proof.LeftPos > proof.IndexSize || proof.Count > proof.IndexSize-proof.LeftPos {
		return nil, fmt.Errorf("%w: range bounds outside index", ErrIncompleteIndexRange)
	}
	if uint64(len(proof.Entries)) != proof.Count {
		return nil, fmt.Errorf("%w: index entry count mismatch", ErrIncompleteIndexRange)
	}
	rightPos := proof.LeftPos + proof.Count

	seen := make(map[uint64]struct{}, proof.Count+2)
	checkOne := func(item *IndexEntryProof, wantPos uint64) error {
		if item == nil {
			return fmt.Errorf("%w: missing index proof at position %d", ErrIncompleteIndexRange, wantPos)
		}
		if item.Pos != wantPos {
			return fmt.Errorf("%w: expected position %d, got %d", ErrIncompleteIndexRange, wantPos, item.Pos)
		}
		if item.Pos >= proof.IndexSize {
			return fmt.Errorf("%w: position %d outside index", ErrIncompleteIndexRange, item.Pos)
		}
		if _, duplicate := seen[item.Pos]; duplicate {
			return fmt.Errorf("%w: duplicate index position %d", ErrIncompleteIndexRange, item.Pos)
		}
		seen[item.Pos] = struct{}{}
		if item.Entry.Key.Tenant == "" || item.Entry.Key.Start > item.Entry.Key.End {
			return fmt.Errorf("%w: malformed index entry at %d", ErrCorrectionMismatch, item.Pos)
		}
		if item.Entry.LeafDigest.IsZero() || item.Entry.VersionSeq < checkpoint.BaseSeq {
			return fmt.Errorf("%w: malformed index source at %d", ErrCorrectionMismatch, item.Pos)
		}
		if last, ok := checkpoint.LastSeq(); ok &&
			(item.Entry.VersionSeq > last || item.Entry.Key.RootSeq > last) {
			return fmt.Errorf("%w: index source outside checkpoint at %d", ErrCorrectionMismatch, item.Pos)
		}
		if item.Entry.Key.RootSeq > item.Entry.VersionSeq {
			return fmt.Errorf("%w: index root follows its version at %d", ErrCorrectionMismatch, item.Pos)
		}
		if !item.Entry.Erased && (!item.Entry.TombstoneDigest.IsZero() || item.Entry.TombstoneSeq != 0) {
			return fmt.Errorf("%w: tombstone fields set on live entry %d", ErrCorrectionMismatch, item.Pos)
		}
		if item.Entry.Erased {
			last, _ := checkpoint.LastSeq()
			if item.Entry.TombstoneDigest.IsZero() ||
				item.Entry.TombstoneSeq < checkpoint.BaseSeq ||
				item.Entry.TombstoneSeq > last {
				return fmt.Errorf("%w: malformed tombstone source at %d", ErrCorrectionMismatch, item.Pos)
			}
		}
		root, err := merkleRootFromProof(item.Pos, proof.IndexSize, item.Entry.digest(item.Pos), item.Path)
		if err != nil {
			return err
		}
		if root != proof.Root {
			return fmt.Errorf("%w: index position %d does not lead to index root", ErrCrossRoot, item.Pos)
		}
		return nil
	}

	if proof.LeftPos == 0 {
		if proof.LeftBoundary != nil {
			return nil, fmt.Errorf("%w: unexpected left boundary", ErrIncompleteIndexRange)
		}
	} else if err := checkOne(proof.LeftBoundary, proof.LeftPos-1); err != nil {
		return nil, err
	}
	for i := range proof.Entries {
		if err := checkOne(&proof.Entries[i], proof.LeftPos+uint64(i)); err != nil {
			return nil, err
		}
	}
	if rightPos == proof.IndexSize {
		if proof.RightBoundary != nil {
			return nil, fmt.Errorf("%w: unexpected right boundary", ErrIncompleteIndexRange)
		}
	} else if err := checkOne(proof.RightBoundary, rightPos); err != nil {
		return nil, err
	}

	var ordered []*IndexEntryProof
	if proof.LeftBoundary != nil {
		ordered = append(ordered, proof.LeftBoundary)
	}
	for i := range proof.Entries {
		ordered = append(ordered, &proof.Entries[i])
	}
	if proof.RightBoundary != nil {
		ordered = append(ordered, proof.RightBoundary)
	}
	for i := 1; i < len(ordered); i++ {
		if !ordered[i-1].Entry.Key.Less(ordered[i].Entry.Key) {
			return nil, fmt.Errorf("%w: index keys are not strictly ordered", ErrIncompleteIndexRange)
		}
	}

	lower, upper, err := IndexKeyForQuery(query)
	if err != nil {
		return nil, err
	}
	if proof.LeftPos > 0 && !proof.LeftBoundary.Entry.Key.Less(lower) {
		return nil, fmt.Errorf("%w: left boundary does not precede query range", ErrIncompleteIndexRange)
	}
	if rightPos < proof.IndexSize && !upper.Less(proof.RightBoundary.Entry.Key) {
		return nil, fmt.Errorf("%w: right boundary does not follow query range", ErrIncompleteIndexRange)
	}

	out := make([]IndexEntry, len(proof.Entries))
	roots := make(map[uint64]struct{}, len(proof.Entries))
	versions := make(map[uint64]struct{}, len(proof.Entries))
	for i := range proof.Entries {
		entry := proof.Entries[i].Entry
		if _, duplicate := roots[entry.Key.RootSeq]; duplicate {
			return nil, fmt.Errorf("%w: duplicate root sequence %d", ErrCorrectionMismatch, entry.Key.RootSeq)
		}
		if _, duplicate := versions[entry.VersionSeq]; duplicate {
			return nil, fmt.Errorf("%w: duplicate version sequence %d", ErrCorrectionMismatch, entry.VersionSeq)
		}
		roots[entry.Key.RootSeq] = struct{}{}
		versions[entry.VersionSeq] = struct{}{}
		out[i] = entry
	}
	return out, nil
}

func validateDisclosedVersionGraph(leaves map[uint64]Leaf) (map[uint64]uint64, error) {
	children := make(map[uint64]uint64)
	for _, leaf := range leaves {
		if leaf.RefSeq == nil {
			continue
		}
		target, exists := leaves[*leaf.RefSeq]
		if !exists {
			return nil, fmt.Errorf("%w: seq %d references missing seq %d", ErrMissingLeaf, leaf.Seq, *leaf.RefSeq)
		}
		if target.Digest() != leaf.PrevDigest {
			return nil, fmt.Errorf("%w: predecessor digest mismatch at seq %d", ErrCorrectionMismatch, leaf.Seq)
		}
		if leaf.Kind == KindCorrection {
			if _, duplicate := children[target.Seq]; duplicate {
				return nil, fmt.Errorf("%w: correction branch at seq %d", ErrCorrectionMismatch, target.Seq)
			}
			children[target.Seq] = leaf.Seq
		}
	}
	for _, leaf := range leaves {
		if leaf.Kind != KindCorrection {
			continue
		}
		seen := make(map[uint64]struct{})
		cur := leaf
		for {
			if _, duplicate := seen[cur.Seq]; duplicate {
				return nil, fmt.Errorf("%w: correction cycle at seq %d", ErrCorrectionMismatch, cur.Seq)
			}
			seen[cur.Seq] = struct{}{}
			if cur.Kind == KindEvent {
				break
			}
			if cur.Kind != KindCorrection || cur.RefSeq == nil {
				return nil, fmt.Errorf("%w: seq %d is not on an event-rooted chain", ErrCorrectionMismatch, cur.Seq)
			}
			next, exists := leaves[*cur.RefSeq]
			if !exists {
				return nil, fmt.Errorf("%w: missing chain node %d", ErrMissingLeaf, *cur.RefSeq)
			}
			cur = next
		}
	}
	return children, nil
}

func reconstructRecord(entry IndexEntry, leaves map[uint64]Leaf, authorize func(Hash, []byte) error) (Record, []Leaf, error) {
	latest, exists := leaves[entry.VersionSeq]
	if !exists {
		return Record{}, nil, fmt.Errorf("%w: effective seq %d", ErrMissingLeaf, entry.VersionSeq)
	}
	if latest.Digest() != entry.LeafDigest {
		return Record{}, nil, fmt.Errorf("%w: index leaf digest mismatch at seq %d", ErrCorrectionMismatch, entry.VersionSeq)
	}
	chain := []Leaf{latest}
	cur := latest
	seen := make(map[uint64]struct{})
	for {
		if _, duplicate := seen[cur.Seq]; duplicate {
			return Record{}, nil, fmt.Errorf("%w: cycle at seq %d", ErrCorrectionMismatch, cur.Seq)
		}
		seen[cur.Seq] = struct{}{}
		if cur.Kind == KindEvent {
			break
		}
		if cur.Kind != KindCorrection || cur.RefSeq == nil {
			return Record{}, nil, fmt.Errorf("%w: invalid version chain at seq %d", ErrCorrectionMismatch, cur.Seq)
		}
		target, exists := leaves[*cur.RefSeq]
		if !exists {
			return Record{}, nil, fmt.Errorf("%w: correction seq %d target seq %d", ErrMissingLeaf, cur.Seq, *cur.RefSeq)
		}
		if target.Digest() != cur.PrevDigest {
			return Record{}, nil, fmt.Errorf("%w: correction predecessor mismatch at seq %d", ErrCorrectionMismatch, cur.Seq)
		}
		chain = append(chain, target)
		cur = target
	}
	if latest.Seq != entry.VersionSeq {
		return Record{}, nil, fmt.Errorf("%w: effective version mismatch", ErrCorrectionMismatch)
	}
	if latest.Tenant != entry.Key.Tenant ||
		latest.EventStart != entry.Key.Start ||
		latest.EventEnd != entry.Key.End {
		return Record{}, nil, fmt.Errorf("%w: index metadata mismatch at seq %d", ErrCorrectionMismatch, latest.Seq)
	}
	if cur.Seq != entry.Key.RootSeq {
		return Record{}, nil, fmt.Errorf("%w: index root sequence mismatch", ErrCorrectionMismatch)
	}

	record := Record{
		RootSeq:      entry.Key.RootSeq,
		VersionSeq:   latest.Seq,
		Tenant:       latest.Tenant,
		EventStart:   latest.EventStart,
		EventEnd:     latest.EventEnd,
		Erased:       entry.Erased,
		TombstoneSeq: entry.TombstoneSeq,
	}
	for _, leaf := range chain {
		if !leaf.BodyCommitment.IsZero() {
			record.BodyCommitment = leaf.BodyCommitment
			record.Body = leaf.Body
			record.BodyKnown = leaf.Body != nil
			break
		}
	}
	if entry.Erased {
		tombstone, exists := leaves[entry.TombstoneSeq]
		if !exists {
			return Record{}, nil, fmt.Errorf("%w: tombstone seq %d", ErrMissingLeaf, entry.TombstoneSeq)
		}
		if tombstone.Kind != KindTombstone || tombstone.Digest() != entry.TombstoneDigest {
			return Record{}, nil, fmt.Errorf("%w: tombstone digest mismatch at seq %d", ErrCorrectionMismatch, entry.TombstoneSeq)
		}
		targetsChain := false
		for _, node := range chain {
			if tombstone.RefSeq != nil && *tombstone.RefSeq == node.Seq {
				targetsChain = true
				break
			}
		}
		if !targetsChain {
			return Record{}, nil, fmt.Errorf("%w: tombstone does not target this record", ErrCorrectionMismatch)
		}
		if err := authorize(tombstone.PrevDigest, tombstone.Authorization); err != nil {
			return Record{}, nil, fmt.Errorf("%w: seq %d: %w", ErrUnauthorizedTombstone, tombstone.Seq, err)
		}
		record.Body = nil
		record.BodyKnown = false
	}
	return record, chain, nil
}

func defaultTombstoneAuthorizer(_ Hash, authorization []byte) error {
	if len(authorization) == 0 {
		return ErrUnauthorizedTombstone
	}
	return nil
}

func sortRecords(records []Record) {
	sort.Slice(records, func(i, j int) bool {
		left := IndexKey{Tenant: records[i].Tenant, Start: records[i].EventStart, End: records[i].EventEnd, RootSeq: records[i].RootSeq}
		right := IndexKey{Tenant: records[j].Tenant, Start: records[j].EventStart, End: records[j].EventEnd, RootSeq: records[j].RootSeq}
		return left.Less(right)
	})
}
