// Package bootpolicy implements an importable, size-optimized boot policy
// package for firmware hosts whose non-volatile storage cannot hold the full
// revocation table.
//
// A policy admits or rejects individual signed-component load events. Every
// load event is evaluated independently against the policy, so a recovery
// exemption granted to one image is never inherited by images that depend on
// it.
package bootpolicy

import "errors"

// EntryKind identifies whether a policy entry forbids a range or grants a
// narrowly scoped recovery exemption.
type EntryKind uint8

const (
	// KindDeny forbids every component matching the entry selector.
	KindDeny EntryKind = 1
	// KindExempt grants a recovery exemption to exactly one image digest.
	KindExempt EntryKind = 2
)

// Digest identifies one exact image digest. Algorithm is a host-defined token
// such as "sha256"; an empty Digest matches no concrete image.
type Digest struct {
	Algorithm string
	Hash      []byte
}

// ParentScope restricts an entry to load events whose direct parent component
// has exactly this permanent signer ID and label. Matching is exact, never
// prefix based, so two parent chains carrying identically labeled components
// never share a scoped entry.
type ParentScope struct {
	SignerID string
	Label    string
}

// Entry is one policy rule.
//
// The common selector matches a component when all of the following hold:
//   - the component signer permanent ID equals SignerID;
//   - the component label has LabelPrefix as a prefix (an empty prefix
//     matches every label);
//   - the component generation lies in the inclusive [GenLo, GenHi] range;
//   - Location is empty or equals the component boot location;
//   - Parent is nil or the loading parent equals Parent exactly.
//
// A KindDeny entry rejects the whole matched range. A KindExempt entry
// additionally requires RootID to match (when non-empty), the component digest
// to match exactly and the boot time not to exceed NotAfter (zero means the
// exemption never expires).
type Entry struct {
	Kind        EntryKind
	SignerID    string
	LabelPrefix string
	GenLo       uint32
	GenHi       uint32
	Location    string
	Parent      *ParentScope

	// Exempt-only fields. Must be zero on KindDeny entries.
	RootID   string
	Digest   Digest
	NotAfter uint64
}

// Component is one signed component presented in a boot input.
type Component struct {
	SignerID   string
	Label      string
	Generation uint32
	Location   string
	RootID     string
	Digest     Digest
}

// LoadEdge states that the child component is loaded by the parent component.
// Indices reference BootInput.Components.
type LoadEdge struct {
	ParentIndex int
	ChildIndex  int
}

// BootInput is one boot attempt: at most eight signed components plus the
// parent/child load edges between them.
type BootInput struct {
	BootTime   uint64
	Components []Component
	Edges      []LoadEdge
}

// MaxComponents is the maximum number of components in a single boot input.
const MaxComponents = 8

// MaxCandidates is the maximum number of host-supplied compression candidates.
const MaxCandidates = 18

// Policy is an ordered collection of entries. Admission order does not depend
// on entry order.
type Policy struct {
	Entries []Entry
}

// Validation and selection errors.
var (
	ErrInvalidEntry      = errors.New("bootpolicy: invalid policy entry")
	ErrInvalidInput      = errors.New("bootpolicy: invalid boot input")
	ErrNewExemption      = errors.New("bootpolicy: compression would introduce a new recovery exemption")
	ErrNoEquivalentSet   = errors.New("bootpolicy: no candidate subset is equivalent to the original policy")
	ErrPartitionTooLarge = errors.New("bootpolicy: equivalence partition exceeds the configured cell limit")
	ErrCertificate       = errors.New("bootpolicy: equivalence certificate verification failed")
	ErrPackage           = errors.New("bootpolicy: malformed policy package")
)

func (d Digest) isZero() bool {
	return d.Algorithm == "" && len(d.Hash) == 0
}

func (d Digest) equal(other Digest) bool {
	return d.Algorithm == other.Algorithm && bytesEqual(d.Hash, other.Hash)
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

// Validate checks structural entry constraints. It does not depend on any
// other entry, so it can be applied to original and candidate entries alike.
func (e Entry) Validate() error {
	if e.SignerID == "" {
		return ErrInvalidEntry
	}
	if e.GenLo > e.GenHi {
		return ErrInvalidEntry
	}
	switch e.Kind {
	case KindDeny:
		if e.RootID != "" || !e.Digest.isZero() || e.NotAfter != 0 {
			return ErrInvalidEntry
		}
	case KindExempt:
		if e.Digest.Algorithm == "" || len(e.Digest.Hash) == 0 {
			return ErrInvalidEntry
		}
	default:
		return ErrInvalidEntry
	}
	if e.Parent != nil {
		if e.Parent.SignerID == "" || e.Parent.Label == "" {
			return ErrInvalidEntry
		}
	}
	return nil
}

// Validate checks every entry in the policy.
func (p *Policy) Validate() error {
	if p == nil {
		return ErrInvalidEntry
	}
	for i := range p.Entries {
		if err := p.Entries[i].Validate(); err != nil {
			return err
		}
	}
	return nil
}
