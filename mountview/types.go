// Package mountview reconstructs a layered, in-memory mount view from
// signed manifests and a (partially missing) recovery log. It never reads
// or writes on-disk content: every input is an in-memory manifest or log
// record and every verdict is derived purely from those records.
package mountview

import "errors"

// Verdict classifies the recoverability of a target handle.
type Verdict int

const (
	// Unknown means at least two checkpoint-compatible gap-completion
	// histories disagree (or a may-set is irreducibly open). No credential
	// is issued.
	Unknown Verdict = iota
	// Recoverable means every compatible history yields the same immutable
	// object id, the same logical interval and the same effective bits, and
	// no history produces a permission relaxation beyond policy.
	Recoverable
	// Rejected means the (deterministically or possibly) resolved binding
	// is incompatible with the active policy, or the signed inputs are
	// mutually inconsistent.
	Rejected
)

func (v Verdict) String() string {
	switch v {
	case Recoverable:
		return "Recoverable"
	case Rejected:
		return "Rejected"
	default:
		return "Unknown"
	}
}

// Propagation is the relative mount-propagation attribute of a node.
type Propagation int

const (
	Private Propagation = iota
	Slave
	Shared
)

func (p Propagation) String() string {
	switch p {
	case Shared:
		return "shared"
	case Slave:
		return "slave"
	default:
		return "private"
	}
}

// ParsePropagation parses the textual propagation attribute.
func ParsePropagation(s string) (Propagation, error) {
	switch s {
	case "shared":
		return Shared, nil
	case "slave":
		return Slave, nil
	case "private":
		return Private, nil
	default:
		return 0, errors.New("mountview: unknown propagation attribute: " + s)
	}
}

// ObjectID names an immutable underlying content object. Objects are never
// materialised by this package; only their identifiers are tracked.
type ObjectID string

// GroupID identifies a hardlink group: every handle that shares a GroupID
// aliases the same immutable object, interval and protection mask.
type GroupID uint64

// DomainID identifies a shared propagation domain.
type DomainID uint64

// Interval is the precise half-open byte sub-range [Offset, Offset+Len) of an
// immutable object bound by a handle.
type Interval struct {
	Offset uint64
	Len    uint64
}

// Contains reports whether other is fully inside i.
func (i Interval) Contains(other Interval) bool {
	if other.Offset < i.Offset {
		return false
	}
	end, ok := addU64(i.Offset, i.Len)
	if !ok {
		return false
	}
	oEnd, ok := addU64(other.Offset, other.Len)
	if !ok {
		return false
	}
	return oEnd <= end
}

// Overlaps reports whether the two intervals share any byte.
func (i Interval) Overlaps(other Interval) bool {
	end, ok := addU64(i.Offset, i.Len)
	if !ok {
		return false
	}
	oEnd, ok := addU64(other.Offset, other.Len)
	if !ok {
		return false
	}
	if i.Offset >= oEnd || other.Offset >= end {
		return false
	}
	return true
}

// Perm is a read/write/execute protection mask.
type Perm struct {
	Read    bool
	Write   bool
	Execute bool
}

// Bits packs the mask into three low bits (r=4, w=2, x=1).
func (p Perm) Bits() uint8 {
	var b uint8
	if p.Read {
		b |= 1 << 2
	}
	if p.Write {
		b |= 1 << 1
	}
	if p.Execute {
		b |= 1
	}
	return b
}

// PermFromBits unpacks a packed rwx mask.
func PermFromBits(b uint8) Perm {
	return Perm{Read: b&0b100 != 0, Write: b&0b010 != 0, Execute: b&0b001 != 0}
}

// RelaxedBy reports whether p grants any right that ceiling denies, i.e. the
// effective mask is not a subset of ceiling.
func (p Perm) RelaxedBy(ceiling Perm) bool {
	if p.Write && !ceiling.Write {
		return true
	}
	if p.Execute && !ceiling.Execute {
		return true
	}
	if p.Read && !ceiling.Read {
		return true
	}
	return false
}

// ViewBinding is the signed, content-layer binding of one anchor path. The
// same GroupID on multiple bindings denotes a hardlink alias set.
type ViewBinding struct {
	Path        string
	Group       GroupID
	Object      ObjectID
	Interval    Interval
	Perm        Perm
	Propagation Propagation
	Domain      DomainID
}

// SymlinkState is the namespace-layer state of a symbolic link anchor.
type SymlinkState struct {
	Path   string
	Target string
}

// Manifest is a host-verified signed manifest binding one directory-hierarchy
// node. ParentDigest chains manifests cryptographically so a tampered manifest
// invalidates every descendant.
type Manifest struct {
	Path          string
	ParentDigest  []byte
	Group         GroupID
	Object        ObjectID
	Interval      Interval
	Perm          Perm
	Propagation   Propagation
	Domain        DomainID
	CanonicalJSON []byte
	Digest        []byte
	Signature     []byte
}

// Checkpoint is a signed boundary snapshot. It authoritatively pins the
// content layer (the manifest registry). The namespace layer — path moves,
// symlink targets and hardlink aliases produced by the log — is deliberately
// not trusted across a missing window.
type Checkpoint struct {
	Seq          uint64
	View         []ViewBinding
	Links        []SymlinkState
	Gaps         []GapSpec
	ParentDigest []byte
	Digest       []byte
	Signature    []byte
}

// GapSpec declares one signed missing-checkpoint window. The signature on the
// bounding checkpoint authenticates both the window's sequence span and the
// exact set of operation primitives that may occur inside it.
type GapSpec struct {
	AfterSeq  uint64
	BeforeSeq uint64
	Allowed   map[OpType]bool
	// Candidates bound the finite universe used by the full-enumeration
	// oracle; they never widen the abstract interpreter's may-sets.
	Candidates GapCandidates
}

// GapCandidates bounds enumeration of a missing window.
type GapCandidates struct {
	Paths     []string
	Targets   []string
	Objects   []ObjectID
	Groups    []GroupID
	Intervals []Interval
	Perms     []Perm
	Domains   []DomainID
	MaxOps    int
}

// OpType enumerates the recovery-log operation primitives.
type OpType string

const (
	OpMove        OpType = "move"        // move a path (rename)
	OpSymlink     OpType = "symlink"     // create/repint a symbolic link
	OpHardlink    OpType = "hardlink"    // add a hardlink alias to a group
	OpBindMount   OpType = "bindmount"   // same-name rebuild bind mount
	OpUnmount     OpType = "unmount"     // remove a mount node
	OpSlice       OpType = "slice"       // sub-interval slice re-mapping
	OpChmod       OpType = "chmod"       // protection-mask change
	OpPropagation OpType = "propagation" // propagation-domain inheritance
)

// LogEntry is one signed recovery-log record.
type LogEntry struct {
	Seq       uint64
	Op        OpType
	Path      string
	NewPath   string
	Target    string
	Group     GroupID
	Object    ObjectID
	Interval  Interval
	Perm      Perm
	Prop      Propagation
	Domain    DomainID
	Digest    []byte
	Signature []byte
}

// Input bundles everything the verifier consumes. Only in-memory manifests
// and log/checkpoint records are accepted; no filesystem access occurs.
type Input struct {
	Manifests   []*Manifest
	Entries     []*LogEntry
	Checkpoints []*Checkpoint
	// OpenGaps are signed missing windows that reach the end of the log:
	// their may-effects are never reconciled by a later checkpoint, so any
	// affected handle stays fail-closed (Unknown).
	OpenGaps []GapSpec
	Verify   Verifier
}

// TargetResult exposes the may-set evidence behind a verdict.
type TargetResult struct {
	Verdict   Verdict
	Path      string
	Objects   []ObjectID
	Intervals []Interval
	Perms     []Perm
	Reason    string
}

func addU64(a, b uint64) (uint64, bool) {
	s := a + b
	if s < a {
		return 0, false
	}
	return s, true
}
