package disclosure

// LeafProof proves that one leaf digest is at a sequence in the committed log
// prefix. Path is ordered from the leaf toward the root.
type LeafProof struct {
	Seq    uint64 `json:"seq"`
	Digest Hash   `json:"digest"`
	Path   []Hash `json:"path"`
}

// LogProof is a batch of inclusion proofs over one committed append-only log.
type LogProof struct {
	BaseSeq uint64      `json:"base_seq"`
	Size    uint64      `json:"size"`
	Root    Hash        `json:"root"`
	Proofs  []LeafProof `json:"proofs"`
}

// IndexEntryProof proves one effective index entry and its exact index
// position. Binding Pos is what makes a contiguous range proof resistant to
// silently dropping an entry.
type IndexEntryProof struct {
	Pos   uint64     `json:"pos"`
	Entry IndexEntry `json:"entry"`
	Path  []Hash     `json:"path"`
}

// IndexRangeProof proves every index position in
// [LeftPos, LeftPos+Count), together with the immediately adjacent positions.
// An absent boundary means the range reaches the start or end of the index.
type IndexRangeProof struct {
	IndexSize     uint64            `json:"index_size"`
	Root          Hash              `json:"root"`
	LeftPos       uint64            `json:"left_pos"`
	Count         uint64            `json:"count"`
	Entries       []IndexEntryProof `json:"entries"`
	LeftBoundary  *IndexEntryProof  `json:"left_boundary,omitempty"`
	RightBoundary *IndexEntryProof  `json:"right_boundary,omitempty"`
}

// Disclosure is the server response for one tenant/time query. The same
// checkpoint must authenticate every proof in LogProof and IndexProof.
type Disclosure struct {
	Checkpoint Checkpoint      `json:"checkpoint"`
	Query      Query           `json:"query"`
	Leaves     []Leaf          `json:"leaves"`
	LogProof   LogProof        `json:"log_proof"`
	IndexProof IndexRangeProof `json:"index_proof"`
}
