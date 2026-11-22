package permverifier

// Perm is a view permission bitmask over a logical object.
type Perm uint8

const (
	// PermRead grants byte reads; never participates in the W^X invariant.
	PermRead Perm = 1 << iota
	// PermWrite grants byte writes through the view.
	PermWrite
	// PermExec grants instruction fetch/execution through the view.
	PermExec
)

// Has reports whether all bits in want are present.
func (p Perm) Has(want Perm) bool { return p&want == want }

// Segment binds one logical half-open span of an object generation onto a
// physical half-open span. Segment lengths must match their logical span.
type Segment struct {
	Off   uint64
	Len   uint64
	PAddr uint64
}

// CowSpec requests a copy-on-write backing for a view. Pages are identity
// rooted at PAddr for Len bytes inside the verifier pool; only pages that are
// actually written are ever split onto private frames.
type CowSpec struct {
	PAddr uint64
	Len   uint64
	Page  uint64
}

// AllocOp reserves a physical span. With Reuse set, the span may already be
// reserved; the verifier still evaluates whether outstanding aliases make the
// reuse unsafe instead of treating it as a malformed batch.
type AllocOp struct {
	Phys    Interval
	Reuse   bool
	Depends []uint64
}

// FreeOp releases an exact previously allocated physical span. Permissions
// retained by CPUs that never acknowledged a revocation are not released here.
type FreeOp struct {
	Phys    Interval
	Depends []uint64
}

// MapOp installs a view of object generation Gen for the listed CPUs.
// Provide either static Segments or a single Cow, never both. Segments jointly
// cover the half-open logical range [Off, Off+Len).
type MapOp struct {
	View     uint64
	Object   uint64
	Gen      uint64
	Off      uint64
	Len      uint64
	Perm     Perm
	CPUs     []uint64
	Segments []Segment
	Cow      *CowSpec
	Depends  []uint64
}

// RemapOp installs a later generation of the same object with a new segmented
// layout. It is a MapOp alias kept explicit for callers modeling segmented
// remapping; its semantics are identical to mapping a fresh generation.
type RemapOp struct {
	Map     MapOp
	Depends []uint64
}

// RevokeOp asks every CPU that holds generation Gen of Object to drop it.
// The generation stays effectively valid until one AckOp per holding CPU has
// arrived with the matching (CPU, generation) pair.
type RevokeOp struct {
	Object  uint64
	Gen     uint64
	Depends []uint64
}

// AckOp is a single CPU's confirmation that generation Gen of Object is gone
// from its local permissions. Acks may arrive in any interleaving; an ack for
// a generation that is not being revoked is ignored.
type AckOp struct {
	CPU     uint64
	Object  uint64
	Gen     uint64
	Depends []uint64
}

// ForkOp creates a new copy-on-write view backed by a shared parent COW node.
// No page is copied until a WriteOp touches it.
type ForkOp struct {
	View    uint64
	Parent  uint64
	Object  uint64
	Gen     uint64
	Off     uint64
	Len     uint64
	Perm    Perm
	CPUs    []uint64
	Depends []uint64
}

// WriteOp records a store through a view. Static views simply keep their
// bindings. COW views split exactly the pages intersected by [Off, Off+Len)
// onto private frames drawn from the verifier pool.
type WriteOp struct {
	View    uint64
	Object  uint64
	Gen     uint64
	Off     uint64
	Len     uint64
	Depends []uint64
}

// Op is one entry of a submitted batch.
type Op struct {
	ID     uint64
	Alloc  *AllocOp
	Free   *FreeOp
	Map    *MapOp
	Remap  *RemapOp
	Revoke *RevokeOp
	Ack    *AckOp
	Fork   *ForkOp
	Write  *WriteOp
}

// Deps returns the dependency edges declared by the op payload.
func (o Op) Deps() []uint64 {
	switch {
	case o.Alloc != nil:
		return o.Alloc.Depends
	case o.Free != nil:
		return o.Free.Depends
	case o.Map != nil:
		return o.Map.Depends
	case o.Remap != nil:
		if len(o.Remap.Depends) > 0 {
			return o.Remap.Depends
		}
		return o.Remap.Map.Depends
	case o.Revoke != nil:
		return o.Revoke.Depends
	case o.Ack != nil:
		return o.Ack.Depends
	case o.Fork != nil:
		return o.Fork.Depends
	case o.Write != nil:
		return o.Write.Depends
	default:
		return nil
	}
}

// Verdict classifies the batch verification outcome.
type Verdict int

const (
	// VerdictUnknown means the exploration budget was exhausted before proof.
	VerdictUnknown Verdict = iota
	// VerdictSafe means every reachable interleaving preserves W^X.
	VerdictSafe
	// VerdictUnsafe means some interleaving maps the same physical byte W and X.
	VerdictUnsafe
	// VerdictIncomplete means no conflict was found but some interleaving ends
	// with operations that can never become enabled or revokes never acked.
	VerdictIncomplete
)

// String renders the verdict for diagnostics.
func (v Verdict) String() string {
	switch v {
	case VerdictSafe:
		return "safe"
	case VerdictUnsafe:
		return "unsafe"
	case VerdictIncomplete:
		return "incomplete"
	default:
		return "unknown"
	}
}

// ViewRef identifies one contributing view inside a reported conflict.
type ViewRef struct {
	ID   uint64
	Gen  uint64
	CPU  uint64
	Perm Perm
}

// Conflict names the first physical interval on which W and X coexist and the
// views that supply each permission.
type Conflict struct {
	Phys    Interval
	Writers []ViewRef
	Execers []ViewRef
}

// Report is the verifier result. Prefix holds the shortest failing sequence of
// batch op IDs (empty for safe/unknown/incomplete-without-prefix results).
type Report struct {
	Verdict        Verdict
	Conflict       *Conflict
	Prefix         []uint64
	StatesExplored int
	Reason         string
}

// Options bounds the exhaustive exploration. Zero values select defaults.
type Options struct {
	// MaxStates caps distinct batch-prefix states; exhaustion yields Unknown.
	MaxStates int
	// MaxSteps caps the longest interleaving explored; exhaustion yields Unknown.
	MaxSteps int
	// Pool is the verifier-owned physical span used to back private COW frames.
	Pool Interval
}

// coveragePiece is one disjoint physical interval with its contributing views.
type coveragePiece struct {
	Phys Interval
	Refs []ViewRef
	W    bool
	X    bool
}
