package netsec

// FindingKind classifies a security-relevant observation on a path.
type FindingKind int

const (
	// FindingErrorMasked means an authentication error value was
	// overwritten, cleared, dropped by recovery, or otherwise made
	// unobservable while the authentication failure remained in effect.
	FindingErrorMasked FindingKind = iota
	// FindingRejectionConverted means a path that experienced an
	// authentication failure reaches a non-denial return without surfacing
	// the authentication error: the intended rejection was converted into a
	// success for the wrapper's caller.
	FindingRejectionConverted
	// FindingUnauthorizedCallback means a protected callback executed on a
	// path with no valid credential (either never authenticated or still
	// carrying an uncleared authentication failure).
	FindingUnauthorizedCallback
)

func (k FindingKind) String() string {
	switch k {
	case FindingErrorMasked:
		return "error-masked"
	case FindingRejectionConverted:
		return "rejection-converted"
	case FindingUnauthorizedCallback:
		return "unauthorized-callback"
	default:
		return "unknown"
	}
}

// EventKind classifies a recorded path event.
type EventKind int

const (
	EventAuthSuccess EventKind = iota
	EventAuthFailure
	EventErrorMasked
	EventPanicRecovered
	EventProtectedCallback
	EventMerge
	EventReturn
	EventImplicitEnd
)

func (k EventKind) String() string {
	switch k {
	case EventAuthSuccess:
		return "auth-success"
	case EventAuthFailure:
		return "auth-failure"
	case EventErrorMasked:
		return "error-masked"
	case EventPanicRecovered:
		return "panic-recovered"
	case EventProtectedCallback:
		return "protected-callback"
	case EventMerge:
		return "merge"
	case EventReturn:
		return "return"
	case EventImplicitEnd:
		return "implicit-end"
	default:
		return "unknown"
	}
}

// Snapshot captures the state visible to a path event.
type Snapshot struct {
	// Authed reports a currently valid credential, i.e. the most recent
	// authentication on this path succeeded.
	Authed bool
	// Credential is the credential variable bound by the latest success.
	Credential string
	// Failed reports a sticky authentication failure that no later success
	// has cleared.
	Failed bool
	// FailSite is the site that produced the sticky failure.
	FailSite string
	// Masked reports that the sticky failure's error value is no longer
	// observable in any error variable.
	Masked bool
	// PanicInFlight reports an unrecovered panic.
	PanicInFlight bool
	// HeldAuthErrors maps each error variable still carrying the sticky
	// authentication error to the originating auth site.
	HeldAuthErrors map[string]string
}

// PathEvent is one observable step on an enumerated path.
type PathEvent struct {
	Kind   EventKind
	Block  string
	Site   string
	Detail string
	State  Snapshot
}

// Finding is a single observation, always tied to the concrete path that
// produced it.
type Finding struct {
	Kind  FindingKind
	Block string
	Site  string
	// PathIndex is the index of the path in Report.Paths.
	PathIndex int
	// Count is the number of enumerated paths exhibiting the same finding
	// (same kind, block and site). It is populated on report-level entries.
	Count  int
	Detail string
}

// TerminalKind classifies how a path ends.
type TerminalKind int

const (
	// TerminalReturn ends at an explicit OpReturn.
	TerminalReturn TerminalKind = iota
	// TerminalImplicit ends at a block with no outgoing edge.
	TerminalImplicit
	// TerminalCutOff ends because an enumeration limit was reached.
	TerminalCutOff
)

func (k TerminalKind) String() string {
	switch k {
	case TerminalReturn:
		return "return"
	case TerminalImplicit:
		return "implicit"
	default:
		return "cut-off"
	}
}

// Terminal describes the end of an enumerated path.
type Terminal struct {
	Kind   TerminalKind
	Block  string
	Site   string
	Denied bool
	// FailureSurfaced reports that the terminal return still carries the
	// authentication error, so the failure remains observable to the caller.
	FailureSurfaced bool
}

// PathResult is one fully enumerated concrete path.
type PathResult struct {
	Index int
	// Blocks lists blocks in visit order.
	Blocks []string
	// EdgeKinds records, for each transfer between consecutive blocks, the
	// edge taken (len is len(Blocks)-1).
	EdgeKinds []EdgeKind
	Events    []PathEvent
	Findings  []Finding
	Terminal  Terminal
}

// Report is the analysis result returned to the review service.
type Report struct {
	Paths []PathResult
	// Findings aggregates findings across paths, deduplicated by
	// (Kind, Block, Site) and ordered by first occurrence.
	Findings []Finding
	// Merges lists every block reachable from more than one predecessor,
	// including whether exceptional and normal incoming flows join there.
	Merges []MergeInfo
	// Truncated reports that an enumeration limit was reached; remaining
	// paths were not explored.
	Truncated bool
	// LimitReason explains why enumeration stopped when Truncated is set.
	LimitReason string
}

// MergeInfo describes a confluence point.
type MergeInfo struct {
	Block        string
	Predecessors int
	// HasExceptionIn reports that at least one incoming edge is exceptional.
	HasExceptionIn bool
	// HasNormalIn reports that at least one incoming edge is normal.
	HasNormalIn bool
	// ReachableViaException reports that at least one enumerated concrete
	// path reaches this block after taking an exceptional edge earlier on
	// the path: an exceptional flow really does merge here at runtime.
	ReachableViaException bool
}

// HasFinding reports whether the report contains a finding of the given kind.
func (r *Report) HasFinding(kind FindingKind) bool {
	for i := range r.Findings {
		if r.Findings[i].Kind == kind {
			return true
		}
	}
	return false
}
