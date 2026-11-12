package netsec

import "strings"

// FindingKind classifies a problem detected while ingesting events.
type FindingKind int

const (
	// StaleIdentityRead means a thread still held a previous request's
	// identity when a new request observed or installed identity on it.
	StaleIdentityRead FindingKind = iota + 1
	// ReentrancyViolation means borrow scopes were not unwound as a strict
	// LIFO stack (mismatched return tag, return underflow, or return that
	// would pop a non-borrow base frame).
	ReentrancyViolation
	// DirtyExit means an Exit event arrived while frames were still live.
	DirtyExit
	// ImplicitInheritance means a Spawn attempted to inherit identity without
	// supplying an explicit snapshot.
	ImplicitInheritance
)

// String renders the finding kind for diagnostics and test output.
func (k FindingKind) String() string {
	switch k {
	case StaleIdentityRead:
		return "stale_identity_read"
	case ReentrancyViolation:
		return "reentrancy_violation"
	case DirtyExit:
		return "dirty_exit"
	case ImplicitInheritance:
		return "implicit_inheritance"
	default:
		return "unknown"
	}
}

// Provenance is one hop in the source chain explaining where an identity came
// from. The chain is ordered oldest-first: the base SetIdentity or Borrow that
// introduced the identity, followed by every snapshot/borrow that propagated it.
type Provenance struct {
	Seq       uint64
	Kind      EventKind
	ThreadID  string
	RequestID string
	Principal string
	Tag       string
	Detail    string
}

// Finding reports a single policy violation together with the full source chain
// of the leaked identity.
type Finding struct {
	Kind      FindingKind
	Seq       uint64
	ThreadID  string
	RequestID string
	Message   string
	Chain     []Provenance
}

// chainString joins a provenance chain into a compact one-line arrow path.
func chainString(chain []Provenance) string {
	var b strings.Builder
	for i, hop := range chain {
		if i > 0 {
			b.WriteString(" -> ")
		}
		b.WriteString(hop.Kind.String())
		b.WriteByte('#')
		b.WriteString(hop.ThreadID)
		if hop.Principal != "" {
			b.WriteByte('(')
			b.WriteString(hop.Principal)
			b.WriteByte(')')
		}
		if hop.Tag != "" {
			b.WriteByte('[')
			b.WriteString(hop.Tag)
			b.WriteByte(']')
		}
	}
	return b.String()
}

// SourceChain renders the provenance chain as "a -> b -> c" for logging.
func (f Finding) SourceChain() string {
	return chainString(f.Chain)
}
