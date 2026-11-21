package recovery

import "math/big"

// TxnID is the global transaction number written into log-participant marks.
type TxnID uint64

// PhysicalVolumeID identifies one physical volume.
type PhysicalVolumeID string

// NodeID identifies a checkpoint inside one physical volume's tree.
type NodeID string

// BlockID identifies a content block that may have to be downloaded.
type BlockID string

// Mark is one participant mark carried by an indivisible log segment.  Volume
// is a logical-volume index, not a physical-volume identity.
type Mark struct {
	Txn    TxnID
	Volume int
}

// TreeNode is one checkpoint in a physical volume's rooted tree.  Parent is
// empty exactly for the root.  Marks and Blocks on a node belong to the edge
// that enters the node (the root's own fields describe its base state).
type TreeNode struct {
	ID         NodeID
	Parent     NodeID
	Marks      []Mark
	Blocks     []BlockID
	Generation uint64
	Digest     string
}

// PhysicalVolume is one rooted checkpoint tree.
type PhysicalVolume struct {
	ID         PhysicalVolumeID
	Generation uint64
	Nodes      []TreeNode
}

// Candidate is one way to mount one logical volume.  Its marks are all marks
// on the root -> Node path (duplicates are retained and counted); its content
// blocks are the set union of path blocks plus the candidate-specific Blocks
// below.
type Candidate struct {
	ID             string
	PhysicalVolume PhysicalVolumeID
	Node           NodeID
	Blocks         []BlockID
}

// LogicalVolume is one logical volume and the candidates that may be mounted
// for it.  The slice order is the candidate input order used by ranked
// certificates.
type LogicalVolume struct {
	Name       string
	Candidates []Candidate
}

// Transaction describes a global transaction.  Participants are logical-volume
// indices.  Every participant must have exactly one mark in the selected
// candidate for that volume; non-participants must have none.  Benefit is a
// signed exact integer (nil means zero).  Prerequisites must also be committed
// whenever this transaction is committed.
type Transaction struct {
	ID            TxnID
	Participants  []int
	Prerequisites []TxnID
	Benefit       *big.Int
}

// Input is one recovery-planning request.
type Input struct {
	LogicalVolumes  []LogicalVolume
	PhysicalVolumes []PhysicalVolume
	Transactions    []Transaction

	// BlockCosts gives the exact download cost of each content block.  A block
	// that is not present in the map has zero cost.
	BlockCosts map[BlockID]*big.Int

	// Required transactions must be committed in every legal combination.
	Required []TxnID

	// MaxBoundaryItems optionally overrides DefaultMaxBoundaryItems.  The
	// package rejects inputs whose retained frontier state is wider.
	MaxBoundaryItems int
}

// BoundaryStats reports the widest frontier actually retained by the planner.
// The retained state is: two bits per active transaction (viable, must-commit),
// a usage bit for every physical volume referenced on both sides of the cut,
// and a downloaded bit for every content block referenced on both sides of the
// cut.
type BoundaryStats struct {
	Limit              int
	MaxTransactions    int
	MaxPhysicalVolumes int
	MaxBlocks          int
	MaxItems           int
	PeakLayer          int
}

// Result is the answer for one ranked request.  Feasible is false when no legal
// combination exists.  Benefit, DownloadCost and Count are exact integers.
type Result struct {
	Feasible     bool
	Benefit      *big.Int
	DownloadCost *big.Int
	Count        *big.Int
	Certificate  *Certificate
	Boundary     BoundaryStats
}

// Choice is one mounted logical-volume selection in a certificate.
type Choice struct {
	LogicalVolume      int
	LogicalVolumeName  string
	Candidate          int
	CandidateID        string
	PhysicalVolume     PhysicalVolumeID
	VolumeGeneration   uint64
	Node               NodeID
	SnapshotGeneration uint64
	SnapshotDigest     string
	Blocks             []BlockID
}

// Certificate is the complete mount group selected for a requested rank.  A
// host must call Verify or Confirm with fresh observations before publishing
// any part of it.
type Certificate struct {
	Rank         *big.Int
	Benefit      *big.Int
	DownloadCost *big.Int
	Choices      []Choice
	Committed    []TxnID
}

// Observation is the host's fresh reading of one physical volume selected by a
// certificate.
type Observation struct {
	PhysicalVolume     PhysicalVolumeID
	VolumeGeneration   uint64
	Node               NodeID
	SnapshotGeneration uint64
	SnapshotDigest     string
}
