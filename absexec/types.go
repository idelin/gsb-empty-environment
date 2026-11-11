package absexec

// Op identifies the operation carried by a CFG node.
type Op uint8

const (
	// OpNoop does nothing and has no effect on the buffer.
	OpNoop Op = iota
	// OpWrite definitely writes the half-open interval [Lo, Hi).
	OpWrite
	// OpDynWrite writes some interval [lo, hi) where lo in [LoMin, LoMax]
	// and hi in [HiMin, HiMax]. Only the guaranteed intersection
	// [LoMax, HiMin) is treated as necessarily written.
	OpDynWrite
	// OpBranch has two successors, both considered feasible.
	OpBranch
	// OpSend inspects the required interval [Lo, Hi) (default: whole buffer).
	OpSend
)

// Node is one CFG vertex. Successors reference Node IDs via Succ.
type Node struct {
	ID    int
	Op    Op
	Lo    int   // fixed interval start (OpWrite, optional OpSend)
	Hi    int   // fixed interval end (OpWrite, optional OpSend)
	LoMin int   // dynamic write: minimum start
	LoMax int   // dynamic write: maximum start
	HiMin int   // dynamic write: minimum end
	HiMax int   // dynamic write: maximum end
	Succ  []int // 0..2 successors
}

// BackEdge declares a CFG edge From -> To that closes a loop and may be
// traversed at most MaxCount times on any single path. Counting from zero
// therefore makes the zero-iteration path a first-class, always-feasible path.
type BackEdge struct {
	From     int
	To       int
	MaxCount int
}

// CFG is the finite control-flow graph handed to Analyze.
type CFG struct {
	// BufferSize is the number of bytes of the output buffer (indices 0..N-1).
	BufferSize int
	// Entry is the Node ID where execution starts.
	Entry int
	Nodes []Node
	// BackEdges may be empty; without them the CFG must be acyclic.
	BackEdges []BackEdge
}

// Interval is a half-open byte range [Lo, Hi).
type Interval struct {
	Lo int
	Hi int
}

// SendStatus classifies the proof result at one send point.
type SendStatus uint8

const (
	// SendUnreachable means no bounded path reaches the send.
	SendUnreachable SendStatus = iota
	// SendSafe means every reaching path fully writes the required interval.
	SendSafe
	// SendUninitialized means at least one reaching path leaves bytes
	// unwritten; counterexamples are attached to the finding.
	SendUninitialized
	// SendUnknown means the visit budget was exhausted before the analysis
	// could decide soundly. No uninitialized claim is made.
	SendUnknown
)

// Counterexample is one concrete bounded path, expressed as the sequence of
// visited Node IDs starting at CFG.Entry and ending at the send point, whose
// written set misses the interval it is attached to.
type Counterexample struct {
	// Path is the sequence of Node IDs on the counterexample path.
	Path []int
	// MissingOnPath are the maximal intervals never written along Path.
	MissingOnPath []Interval
}

// UninitializedInterval is a maximal interval missing from the must-written
// set of a send point, together with a witness reaching path.
type UninitializedInterval struct {
	Interval       Interval
	Counterexample Counterexample
}

// SendFinding is the per-send-point result.
type SendFinding struct {
	NodeID int
	Status SendStatus
	// Uninitialized is populated only for SendUninitialized.
	Uninitialized []UninitializedInterval
}

// Report is the full analysis result.
type Report struct {
	// Complete is true when every bounded path state was explored within
	// budget; false means at least one finding is SendUnknown.
	Complete bool
	// Visited is the number of expanded (node, back-edge-count) states.
	Visited int
	Sends   []SendFinding
}

// Request pairs a CFG with analysis parameters.
type Request struct {
	CFG *CFG
	// Budget caps the number of expanded states across the must-set
	// enumeration and every witness search. Zero means unlimited.
	Budget int
}
