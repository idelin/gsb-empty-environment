// Package cfverify performs deterministic control-flow verification of a
// binary image.
//
// The caller supplies trusted entry addresses, a fixed version byte that must
// appear at a fixed offset, and a deterministic decoder. The decoder reports,
// for every instruction, its byte interval, its direct successors and the
// permitted targets of any indirect call it contains.
//
// Verification walks every reachable successor. A loadable conclusion is only
// issued when every permitted indirect target is the unique start of an
// instruction and no two reachable instructions overlap. Overlap conflicts are
// never overwritten: the first recorded owner of every byte wins, and the
// shortest path from a trusted entry to the conflicting target is returned as
// a witness. When the work budget is exhausted before the reachable graph is
// fully explored, the result is Unknown rather than a loadable conclusion.
package cfverify

import "fmt"

// Interval is the half-open byte interval [Start, End) occupied by an
// instruction. End must be strictly greater than Start.
type Interval struct {
	Start uint64
	End   uint64
}

// DecodedInstr is the deterministic result of decoding one instruction at PC.
type DecodedInstr struct {
	// PC is the instruction start address.
	PC uint64
	// Bytes is the half-open interval [Start, End) covered by the instruction.
	Bytes Interval
	// Successors are the direct control-flow successors (fall-through and
	// direct branch targets).
	Successors []uint64
	// IndirectTargets are the permitted targets of any indirect call emitted
	// by this instruction. Each must be the unique start of an instruction.
	IndirectTargets []uint64
}

// Decoder deterministically decodes the instruction starting at PC within
// image. Repeated calls with identical arguments must return identical
// results. A non-nil error means the image cannot be decoded and is reported
// as a Reject verdict.
type Decoder func(image []byte, pc uint64) (DecodedInstr, error)

// Verdict classifies the verification outcome.
type Verdict int

const (
	// VerdictUnknown means the budget was exhausted before the reachable
	// control-flow graph could be fully explored. No loadable conclusion is
	// issued.
	VerdictUnknown Verdict = iota
	// VerdictSafe means every reachable instruction was decoded, no intervals
	// overlap and every permitted indirect target is a unique instruction
	// start.
	VerdictSafe
	// VerdictConflict means two reachable instructions overlap, a successor
	// enters the middle of an instruction, or a permitted indirect target is
	// not a unique instruction start.
	VerdictConflict
	// VerdictReject means the image violates the version constraint, the
	// decoder contract or the address bounds of the image.
	VerdictReject
)

// String returns a stable textual name for the verdict.
func (v Verdict) String() string {
	switch v {
	case VerdictSafe:
		return "safe"
	case VerdictConflict:
		return "conflict"
	case VerdictReject:
		return "reject"
	default:
		return "unknown"
	}
}

// ConflictKind identifies why a conflict was raised.
type ConflictKind int

const (
	// ConflictUnknown is the zero value and must never appear in a result.
	ConflictUnknown ConflictKind = iota
	// ConflictOverlap means two decoded instruction intervals share at least
	// one byte.
	ConflictOverlap
	// ConflictMidEntry means a successor or permitted target lands inside an
	// existing instruction rather than at its start.
	ConflictMidEntry
	// ConflictIndirectNotStart means a permitted indirect target is not the
	// start of any reachable instruction.
	ConflictIndirectNotStart
)

// String returns a stable textual name for the conflict kind.
func (k ConflictKind) String() string {
	switch k {
	case ConflictOverlap:
		return "overlap"
	case ConflictMidEntry:
		return "mid-entry"
	case ConflictIndirectNotStart:
		return "indirect-not-start"
	default:
		return "unknown"
	}
}

// Conflict describes a non-loadable control-flow finding.
type Conflict struct {
	// Kind is the category of the conflict.
	Kind ConflictKind
	// Target is the address at which the conflict is observed.
	Target uint64
	// OwnerPC is the start of the instruction already owning Target, when one
	// exists.
	OwnerPC uint64
	// HasOwner reports whether OwnerPC is meaningful.
	HasOwner bool
	// Detail is a human-readable explanation.
	Detail string
}

// Budget bounds the verification work. A zero field is replaced by a default.
type Budget struct {
	// MaxInstructions caps the number of distinct instruction decodes.
	MaxInstructions uint64
	// MaxEdges caps the number of distinct successor edges enqueued.
	MaxEdges uint64
}

// Stats reports the work performed before the verdict was reached.
type Stats struct {
	// Decoded is the number of distinct instructions decoded.
	Decoded uint64
	// Edges is the number of distinct successor edges enqueued.
	Edges uint64
	// Visited is the number of distinct addresses dequeued and processed.
	Visited uint64
}

// Config is the full verification input.
type Config struct {
	// Image is the binary image under review, addressed relative to Base.
	Image []byte
	// Base is the absolute address of Image[0].
	Base uint64
	// Entries are the trusted entry addresses. Order does not affect the
	// verdict; it only affects tie-breaking among equally short witnesses.
	Entries []uint64
	// VersionOffset is the image-relative offset of the fixed version byte.
	VersionOffset uint64
	// VersionByte is the value that must appear at VersionOffset.
	VersionByte byte
	// Decode decodes a single instruction deterministically.
	Decode Decoder
	// Work bounds the verification effort. A zero value uses the defaults.
	Work Budget
}

// Result is the verification outcome. Exactly one verdict is returned.
type Result struct {
	// Verdict is the final classification.
	Verdict Verdict
	// Conflict is populated for VerdictConflict.
	Conflict *Conflict
	// Witness is the shortest sequence of instruction start PCs from a
	// trusted entry to the conflict target. The final element is the
	// conflicting target address, which may not itself be an instruction
	// start. Populated for VerdictConflict and VerdictReject findings that
	// have a reachable context.
	Witness []uint64
	// Stats counts the work performed.
	Stats Stats
	// Reason explains VerdictReject and VerdictUnknown.
	Reason string
}

const (
	defaultMaxInstructions = 100_000
	defaultMaxEdges        = 1_000_000
)

const noParent = ^uint64(0)

// Verify runs the control-flow verification described by cfg.
func Verify(cfg Config) Result {
	if cfg.Decode == nil {
		return rejectResult(Stats{}, "nil decoder", nil)
	}
	if len(cfg.Entries) == 0 {
		return rejectResult(Stats{}, "no trusted entries supplied", nil)
	}

	budget := cfg.Work
	if budget.MaxInstructions == 0 {
		budget.MaxInstructions = defaultMaxInstructions
	}
	if budget.MaxEdges == 0 {
		budget.MaxEdges = defaultMaxEdges
	}

	imageLen := uint64(len(cfg.Image))
	if cfg.VersionOffset >= imageLen {
		return rejectResult(Stats{},
			fmt.Sprintf("version offset %d outside image of length %d", cfg.VersionOffset, imageLen), nil)
	}
	if cfg.Image[cfg.VersionOffset] != cfg.VersionByte {
		return rejectResult(Stats{},
			fmt.Sprintf("version byte at offset %d is 0x%02x, want 0x%02x",
				cfg.VersionOffset, cfg.Image[cfg.VersionOffset], cfg.VersionByte), nil)
	}

	r := &runner{
		cfg:     cfg,
		owner:   make(map[uint64]uint64),
		parent:  make(map[uint64]uint64),
		queued:  make(map[uint64]bool),
		decoded: make(map[uint64]DecodedInstr),
	}

	for _, entry := range cfg.Entries {
		if !r.inImage(entry) {
			return rejectResult(r.stats,
				fmt.Sprintf("entry 0x%x outside image [0x%x, 0x%x)", entry, cfg.Base, cfg.Base+imageLen), nil)
		}
		if r.queued[entry] {
			continue
		}
		r.queued[entry] = true
		r.parent[entry] = noParent
		r.queue = append(r.queue, queueEntry{addr: entry, kind: edgeEntry})
	}

	for len(r.queue) > 0 {
		// A budget that has been reached means the reachable graph cannot be
		// fully certified. Stop immediately rather than draining a backlog of
		// already-enqueued addresses, and never issue a loadable conclusion.
		if r.stats.Visited >= budget.MaxInstructions || r.stats.Edges >= budget.MaxEdges {
			return r.unknownResult(budget, "verification budget exhausted before traversal completed")
		}
		if result := r.step(budget); result != nil {
			return *result
		}
	}

	return r.finalCheck(imageLen)
}
