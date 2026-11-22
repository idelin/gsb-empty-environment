package disclosure

import (
	"fmt"
	"sort"
)

// DiscloseOptions controls which bodies a server reveals. Erased records are
// always returned without a body, regardless of this callback.
type DiscloseOptions struct {
	// IncludeBody is called with the effective record's root sequence and the
	// version leaf. A nil callback reveals bodies for every non-erased record.
	IncludeBody func(rootSeq uint64, leaf Leaf) bool
}

// BuildDisclosure constructs a selective disclosure from a complete log. The
// returned response contains only the index range that can affect the query,
// plus every correction ancestor and tombstone needed to verify those records.
func BuildDisclosure(leaves []Leaf, checkpoint Checkpoint, query Query) (*Disclosure, error) {
	return BuildDisclosureWithOptions(leaves, checkpoint, query, DiscloseOptions{})
}

// BuildDisclosureWithOptions is BuildDisclosure with a body-reveal policy.
func BuildDisclosureWithOptions(leaves []Leaf, checkpoint Checkpoint, query Query, options DiscloseOptions) (*Disclosure, error) {
	if err := query.Validate(); err != nil {
		return nil, err
	}
	view, err := prepareLog(leaves)
	if err != nil {
		return nil, err
	}
	if err := checkpoint.Validate(); err != nil {
		return nil, err
	}
	computed, err := BuildCheckpoint(view.leaves)
	if err != nil {
		return nil, err
	}
	if checkpoint.BaseSeq != computed.BaseSeq ||
		checkpoint.Size != computed.Size ||
		checkpoint.LogRoot != computed.LogRoot ||
		checkpoint.IndexSize != computed.IndexSize ||
		checkpoint.IndexRoot != computed.IndexRoot {
		return nil, fmt.Errorf("%w: checkpoint does not match supplied log", ErrInvalidCheckpoint)
	}

	index, err := buildIndex(view)
	if err != nil {
		return nil, err
	}
	lower, upper, err := IndexKeyForQuery(query)
	if err != nil {
		return nil, err
	}
	left := sort.Search(len(index), func(i int) bool { return !index[i].Key.Less(lower) })
	right := sort.Search(len(index), func(i int) bool { return upper.Less(index[i].Key) })
	if right < left {
		right = left
	}

	indexProof := IndexRangeProof{
		IndexSize: uint64(len(index)),
		Root:      computed.IndexRoot,
		LeftPos:   uint64(left),
		Count:     uint64(right - left),
	}
	hashes := indexHashes(index)
	for i := left; i < right; i++ {
		_, path := merkleProof(hashes, i)
		indexProof.Entries = append(indexProof.Entries, IndexEntryProof{
			Pos:   uint64(i),
			Entry: index[i],
			Path:  path,
		})
	}
	if left > 0 {
		_, path := merkleProof(hashes, left-1)
		indexProof.LeftBoundary = &IndexEntryProof{
			Pos:   uint64(left - 1),
			Entry: index[left-1],
			Path:  path,
		}
	}
	if right < len(index) {
		_, path := merkleProof(hashes, right)
		indexProof.RightBoundary = &IndexEntryProof{
			Pos:   uint64(right),
			Entry: index[right],
			Path:  path,
		}
	}

	included := make(map[uint64]struct{})
	erasedRoots := make(map[uint64]bool)
	rootBySeq := make(map[uint64]uint64)

	for i := left; i < right; i++ {
		entry := index[i]
		chain, err := view.correctionChainLeaf(entry.VersionSeq)
		if err != nil {
			return nil, err
		}
		for _, leaf := range chain {
			included[leaf.Seq] = struct{}{}
			rootBySeq[leaf.Seq] = entry.Key.RootSeq
		}
		if entry.Erased {
			erasedRoots[entry.Key.RootSeq] = true
			if _, exists := view.bySeq[entry.TombstoneSeq]; !exists {
				return nil, fmt.Errorf("%w: tombstone seq %d", ErrMissingLeaf, entry.TombstoneSeq)
			}
			included[entry.TombstoneSeq] = struct{}{}
			rootBySeq[entry.TombstoneSeq] = entry.Key.RootSeq
		}
	}

	seqs := make([]uint64, 0, len(included))
	for seq := range included {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })

	logHashes := view.leafHashes()
	response := &Disclosure{
		Checkpoint: checkpoint,
		Query:      query,
		LogProof: LogProof{
			BaseSeq: checkpoint.BaseSeq,
			Size:    checkpoint.Size,
			Root:    checkpoint.LogRoot,
		},
		IndexProof: indexProof,
	}

	for _, seq := range seqs {
		leaf := view.bySeq[seq]
		pos := int(seq - checkpoint.BaseSeq)
		_, path := merkleProof(logHashes, pos)
		disclosed := leaf
		rootSeq := rootBySeq[seq]
		reveal := options.IncludeBody == nil || options.IncludeBody(rootSeq, leaf)
		if leaf.Kind == KindTombstone || erasedRoots[rootSeq] || !reveal {
			disclosed.Body = nil
		}
		response.Leaves = append(response.Leaves, disclosed)
		response.LogProof.Proofs = append(response.LogProof.Proofs, LeafProof{
			Seq:    leaf.Seq,
			Digest: leaf.Digest(),
			Path:   path,
		})
	}
	return response, nil
}
