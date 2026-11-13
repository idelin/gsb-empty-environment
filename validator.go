package netsec

import (
	"errors"
	"fmt"
)

// Policy controls how a route whose ASPA data is incomplete is treated.
type Policy uint8

const (
	// Strict rejects Unknown paths. It is the zero value and therefore the
	// safe default.
	Strict Policy = iota
	// Monitor accepts Unknown paths with a warning, but keeps the verdict
	// Unknown and never reports them as valid.
	Monitor
)

func (p Policy) String() string {
	if p == Monitor {
		return "monitor"
	}
	return "strict"
}

// Verdict is the result of ASPA path verification.
type Verdict string

const (
	VerdictValid   Verdict = "valid"
	VerdictInvalid Verdict = "invalid"
	VerdictUnknown Verdict = "unknown"
)

// Direction is the relationship inferred for one hop after the valley shape
// has been assigned.
type Direction uint8

const (
	DirectionUnknown Direction = iota
	// DirectionUp is a customer-to-provider hop in propagation order.
	DirectionUp
	// DirectionDown is a provider-to-customer hop in propagation order.
	DirectionDown
	// DirectionPeer is a lateral peer hop.
	DirectionPeer
)

func (d Direction) String() string {
	switch d {
	case DirectionUp:
		return "up"
	case DirectionDown:
		return "down"
	case DirectionPeer:
		return "peer"
	default:
		return "unknown"
	}
}

// ReasonCode identifies the first structural violation or data gap.
type ReasonCode string

const (
	ReasonNone                    ReasonCode = ""
	ReasonNoDataset               ReasonCode = "no_dataset"
	ReasonInvalidSession          ReasonCode = "invalid_session"
	ReasonEmptyPath               ReasonCode = "empty_path"
	ReasonNonSequenceSegment      ReasonCode = "non_sequence_segment"
	ReasonInvalidASN              ReasonCode = "invalid_asn"
	ReasonLocalASInPath           ReasonCode = "local_as_in_path"
	ReasonNeighborMismatch        ReasonCode = "neighbor_mismatch"
	ReasonPathLoop                ReasonCode = "path_loop"
	ReasonValleyViolation         ReasonCode = "valley_violation"
	ReasonUnauthorizedCustomer    ReasonCode = "unauthorized_customer_provider"
	ReasonMissingASPA             ReasonCode = "missing_aspa"
	ReasonUnattestedPeer          ReasonCode = "unattested_peer"
	ReasonSessionRelationMismatch ReasonCode = "session_relation_mismatch"
	ReasonCustomerRouteExport     ReasonCode = "customer_route_export"
	ReasonMalformedPath           ReasonCode = "malformed_path"
)

// Issue points at the first non-valid hop or at a malformed-input condition.
// HopIndex is -1 when the failure is not associated with an adjacent hop.
type Issue struct {
	HopIndex int
	From     ASN
	To       ASN
	Class    Verdict
	Reason   ReasonCode
	Detail   string
}

// Hop is the ASPA classification of one adjacent pair. For the terminal hop,
// To is the local AS even though the local AS is not stored in AS_PATH.
type Hop struct {
	Index            int
	From             ASN
	To               ASN
	Direction        Direction
	Class            Verdict
	Reason           ReasonCode
	Detail           string
	Terminal         bool
	Checked          bool
	PrependCount     int
	PrependTruncated bool
}

// Evaluation is the complete, immutable result for one route.
type Evaluation struct {
	Verdict        Verdict
	Policy         Policy
	DatasetVersion string
	Session        Session
	Path           NormalizedPath
	Hops           []Hop
	FirstIssue     *Issue
	FirstInvalid   *Issue
	FirstDataGap   *Issue
	Accepted       bool
	Warning        bool
}

// IsValid reports whether every checked hop was proven valid.
func (e Evaluation) IsValid() bool {
	return e.Verdict == VerdictValid
}

// Options configures a Validator.
type Options struct {
	// Policy selects Strict or Monitor handling for Unknown paths.
	Policy Policy
	// MaxPrepend caps retained repetition evidence per consecutive AS run.
	// Zero selects DefaultMaxPrepend.
	MaxPrepend int
}

// Validator evaluates AS paths against one immutable dataset snapshot.
type Validator struct {
	dataset    *Dataset
	policy     Policy
	maxPrepend int
}

// NewValidator constructs a validator. A nil dataset is rejected rather than
// silently treating all relationships as unknown.
func NewValidator(dataset *Dataset, options Options) (*Validator, error) {
	if dataset == nil {
		return nil, fmt.Errorf("%w: nil dataset", ErrInvalidDataset)
	}
	maxPrepend := options.MaxPrepend
	if maxPrepend <= 0 {
		maxPrepend = DefaultMaxPrepend
	}
	return &Validator{dataset: dataset, policy: options.Policy, maxPrepend: maxPrepend}, nil
}

// Validate evaluates path as received on session.
func (v *Validator) Validate(path Path, session Session) Evaluation {
	base := Evaluation{
		Policy:         v.policy,
		DatasetVersion: v.dataset.Version(),
		Session:        session,
	}
	if err := session.Validate(); err != nil {
		return rejected(base, ReasonInvalidSession, err.Error())
	}

	normalized, err := NormalizePath(path, session.LocalASN, v.maxPrepend)
	if err != nil {
		reason := ReasonMalformedPath
		switch {
		case errors.Is(err, ErrEmptyPath):
			reason = ReasonEmptyPath
		case errors.Is(err, ErrNonSequenceSegment):
			reason = ReasonNonSequenceSegment
		case errors.Is(err, ErrPathLoop):
			reason = ReasonPathLoop
		case errors.Is(err, ErrInvalidASN):
			reason = ReasonInvalidASN
		case errors.Is(err, ErrLocalASInPath):
			reason = ReasonLocalASInPath
		}
		return rejected(base, reason, err.Error())
	}
	base.Path = normalized

	neighbor, _ := normalized.Last()
	if neighbor != session.NeighborASN {
		return rejected(base, ReasonNeighborMismatch, fmt.Sprintf(
			"last AS_PATH AS is %d, session neighbor is %d", uint32(neighbor), uint32(session.NeighborASN)))
	}

	if session.Relation == Customer {
		hops := make([]Hop, len(normalized.ASNs))
		for i, asn := range normalized.ASNs {
			hops[i] = Hop{
				Index:            i,
				From:             asn,
				To:               session.LocalASN,
				Direction:        DirectionUnknown,
				Class:            VerdictUnknown,
				Reason:           ReasonCustomerRouteExport,
				Detail:           "customer-learned route is export-eligible without valley verification",
				Terminal:         i == len(normalized.ASNs)-1,
				Checked:          false,
				PrependCount:     normalized.Runs[i].Count,
				PrependTruncated: normalized.Runs[i].Truncated,
			}
			if i < len(normalized.ASNs)-1 {
				hops[i].To = normalized.ASNs[i+1]
			} else {
				hops[i].Direction = DirectionUp
				hops[i].Class = VerdictValid
				hops[i].Reason = ReasonNone
				hops[i].Detail = "host-authenticated customer session edge"
				hops[i].Checked = true
			}
		}
		base.Hops = hops
		base.Verdict = VerdictValid
		base.Accepted = true
		return base
	}

	evaluation := v.evaluateProviderOrPeer(normalized, session)
	evaluation.Policy = v.policy
	evaluation.DatasetVersion = v.dataset.Version()
	evaluation.Session = session
	return evaluation
}

// ValidateRoute is a convenience wrapper around Validate.
func (v *Validator) ValidateRoute(route Route) Evaluation {
	return v.Validate(route.Path, route.Session)
}

// EvaluatePath is a stateless convenience API. It returns Invalid rather than
// an error so a routing host can apply one fail-closed decision path.
func EvaluatePath(dataset *Dataset, policy Policy, session Session, path Path, maxPrepend int) Evaluation {
	validator, err := NewValidator(dataset, Options{Policy: policy, MaxPrepend: maxPrepend})
	if err != nil {
		return rejected(Evaluation{Policy: policy, Session: session}, ReasonNoDataset, err.Error())
	}
	return validator.Validate(path, session)
}

func rejected(base Evaluation, reason ReasonCode, detail string) Evaluation {
	issue := &Issue{
		HopIndex: -1,
		Class:    VerdictInvalid,
		Reason:   reason,
		Detail:   detail,
	}
	base.Verdict = VerdictInvalid
	base.FirstIssue = issue
	base.FirstInvalid = issue
	base.Accepted = false
	return base
}

type edgeFact struct {
	from             ASN
	to               ASN
	terminal         bool
	sessionDirection Direction
	upAuthorized     bool
	downAuthorized   bool
	fromHasRecord    bool
	toHasRecord      bool
}

type hopChoice struct {
	direction Direction
	class     Verdict
	reason    ReasonCode
	detail    string
}

type phase uint8

const (
	phaseUp phase = iota
	phasePeer
	phaseDown
)

type candidate struct {
	phase        phase
	choices      []hopChoice
	invalidCount int
	unknownCount int
	firstProblem int
	firstInvalid int
	firstDataGap int
}

func (v *Validator) evaluateProviderOrPeer(path NormalizedPath, session Session) Evaluation {
	vertices := make([]ASN, 0, len(path.ASNs)+1)
	vertices = append(vertices, path.ASNs...)
	vertices = append(vertices, session.LocalASN)

	facts := make([]edgeFact, len(vertices)-1)
	for i := 0; i < len(facts); i++ {
		fact := edgeFact{from: vertices[i], to: vertices[i+1]}
		if i == len(facts)-1 {
			fact.terminal = true
			fact.sessionDirection = relationDirection(session.Relation)
		} else {
			fact.upAuthorized = v.dataset.Authorizes(fact.from, fact.to)
			fact.downAuthorized = v.dataset.Authorizes(fact.to, fact.from)
			fact.fromHasRecord = v.dataset.HasRecord(fact.from)
			fact.toHasRecord = v.dataset.HasRecord(fact.to)
		}
		facts[i] = fact
	}

	states := make(map[phase]candidate, 3)
	for i, fact := range facts {
		next := make(map[phase]candidate, 3)
		if i == 0 {
			for _, direction := range allowedDirections(true, phaseUp) {
				choice := classifyHop(fact, direction)
				updated := appendCandidate(candidate{}, choice)
				updated.phase = phaseFor(phaseUp, direction, true)
				if existing, ok := next[updated.phase]; !ok || betterCandidate(updated, existing) {
					next[updated.phase] = updated
				}
			}
		} else {
			for previousPhase, current := range states {
				for _, direction := range allowedDirections(false, previousPhase) {
					choice := classifyHop(fact, direction)
					updated := appendCandidate(current, choice)
					updated.phase = phaseFor(previousPhase, direction, false)
					if existing, ok := next[updated.phase]; !ok || betterCandidate(updated, existing) {
						next[updated.phase] = updated
					}
				}
			}
		}
		states = next
	}

	var best candidate
	found := false
	for _, current := range states {
		if !found || betterCandidate(current, best) {
			best = current
			found = true
		}
	}

	evaluation := Evaluation{
		Policy:         v.policy,
		DatasetVersion: v.dataset.Version(),
		Session:        session,
		Path:           path,
	}
	if !found {
		return rejected(evaluation, ReasonValleyViolation, "AS_PATH cannot be assigned a valley-free shape")
	}

	evaluation.Hops = make([]Hop, len(facts))
	for i, fact := range facts {
		choice := best.choices[i]
		run := path.Runs[i]
		evaluation.Hops[i] = Hop{
			Index:            i,
			From:             fact.from,
			To:               fact.to,
			Direction:        choice.direction,
			Class:            choice.class,
			Reason:           choice.reason,
			Detail:           choice.detail,
			Terminal:         fact.terminal,
			Checked:          true,
			PrependCount:     run.Count,
			PrependTruncated: run.Truncated,
		}
		issue := issueAt(evaluation.Hops[i], choice)
		if choice.class != VerdictValid {
			if evaluation.FirstIssue == nil {
				evaluation.FirstIssue = issue
			}
		}
		if choice.class == VerdictInvalid && evaluation.FirstInvalid == nil {
			evaluation.FirstInvalid = issue
		}
		if choice.class == VerdictUnknown && evaluation.FirstDataGap == nil {
			evaluation.FirstDataGap = issue
		}
	}

	switch {
	case best.invalidCount > 0:
		evaluation.Verdict = VerdictInvalid
		evaluation.Accepted = false
	case best.unknownCount > 0:
		evaluation.Verdict = VerdictUnknown
		evaluation.Accepted = v.policy == Monitor
		evaluation.Warning = v.policy == Monitor
	default:
		evaluation.Verdict = VerdictValid
		evaluation.Accepted = true
	}
	return evaluation
}

func relationDirection(relation Relation) Direction {
	switch relation {
	case Customer:
		return DirectionUp
	case Provider:
		return DirectionDown
	case Peer:
		return DirectionPeer
	default:
		return DirectionUnknown
	}
}

func allowedDirections(first bool, previous phase) []Direction {
	if first {
		return []Direction{DirectionUp, DirectionPeer, DirectionDown}
	}
	switch previous {
	case phaseUp:
		return []Direction{DirectionUp, DirectionPeer, DirectionDown}
	case phasePeer:
		return []Direction{DirectionDown}
	default:
		return []Direction{DirectionDown}
	}
}

func phaseFor(previous phase, direction Direction, first bool) phase {
	if first {
		switch direction {
		case DirectionUp:
			return phaseUp
		case DirectionPeer:
			return phasePeer
		default:
			return phaseDown
		}
	}
	switch direction {
	case DirectionUp:
		return phaseUp
	case DirectionPeer:
		return phasePeer
	default:
		return phaseDown
	}
}

func classifyHop(fact edgeFact, direction Direction) hopChoice {
	if fact.terminal {
		if direction == fact.sessionDirection && direction != DirectionUnknown {
			return hopChoice{
				direction: direction,
				class:     VerdictValid,
				detail:    "host-authenticated session edge",
			}
		}
		return hopChoice{
			direction: direction,
			class:     VerdictInvalid,
			reason:    ReasonSessionRelationMismatch,
			detail: fmt.Sprintf(
				"terminal hop %d->%d conflicts with %s session relationship",
				uint32(fact.from), uint32(fact.to), fact.sessionDirection),
		}
	}

	switch direction {
	case DirectionUp:
		switch {
		case fact.upAuthorized:
			return hopChoice{direction: direction, class: VerdictValid, detail: "customer authorizes provider"}
		case fact.downAuthorized:
			return hopChoice{
				direction: direction,
				class:     VerdictInvalid,
				reason:    ReasonValleyViolation,
				detail:    "hop is authenticated in the opposite (downhill) direction",
			}
		case fact.fromHasRecord:
			return hopChoice{
				direction: direction,
				class:     VerdictInvalid,
				reason:    ReasonUnauthorizedCustomer,
				detail:    "customer has a complete ASPA record but does not authorize this provider",
			}
		default:
			return hopChoice{
				direction: direction,
				class:     VerdictUnknown,
				reason:    ReasonMissingASPA,
				detail:    "customer has no ASPA record",
			}
		}
	case DirectionDown:
		switch {
		case fact.downAuthorized:
			return hopChoice{direction: direction, class: VerdictValid, detail: "right-hand customer authorizes left-hand provider"}
		case fact.upAuthorized:
			return hopChoice{
				direction: direction,
				class:     VerdictInvalid,
				reason:    ReasonValleyViolation,
				detail:    "hop is authenticated in the opposite (uphill) direction",
			}
		case fact.toHasRecord:
			return hopChoice{
				direction: direction,
				class:     VerdictInvalid,
				reason:    ReasonUnauthorizedCustomer,
				detail:    "customer has a complete ASPA record but does not authorize this provider",
			}
		default:
			return hopChoice{
				direction: direction,
				class:     VerdictUnknown,
				reason:    ReasonMissingASPA,
				detail:    "customer has no ASPA record",
			}
		}
	default:
		if fact.upAuthorized || fact.downAuthorized {
			return hopChoice{
				direction: DirectionPeer,
				class:     VerdictInvalid,
				reason:    ReasonValleyViolation,
				detail:    "a customer-provider authorization exists where a peer hop is required",
			}
		}
		return hopChoice{
			direction: DirectionPeer,
			class:     VerdictUnknown,
			reason:    ReasonUnattestedPeer,
			detail:    "ASPA does not attest lateral peer relationships",
		}
	}
}

func appendCandidate(current candidate, choice hopChoice) candidate {
	updated := candidate{
		choices:      append(append([]hopChoice(nil), current.choices...), choice),
		invalidCount: current.invalidCount,
		unknownCount: current.unknownCount,
		firstProblem: current.firstProblem,
		firstInvalid: current.firstInvalid,
		firstDataGap: current.firstDataGap,
	}
	if len(current.choices) == 0 {
		updated.firstProblem = -1
		updated.firstInvalid = -1
		updated.firstDataGap = -1
	}
	index := len(updated.choices) - 1
	switch choice.class {
	case VerdictInvalid:
		updated.invalidCount = current.invalidCount + 1
		if updated.firstInvalid < 0 {
			updated.firstInvalid = index
		}
	case VerdictUnknown:
		updated.unknownCount = current.unknownCount + 1
		if updated.firstDataGap < 0 {
			updated.firstDataGap = index
		}
	}
	if choice.class != VerdictValid && updated.firstProblem < 0 {
		updated.firstProblem = index
	}
	return updated
}

func betterCandidate(a, b candidate) bool {
	if a.invalidCount != b.invalidCount {
		return a.invalidCount < b.invalidCount
	}
	if a.unknownCount != b.unknownCount {
		return a.unknownCount < b.unknownCount
	}
	if a.firstProblem != b.firstProblem {
		return a.firstProblem < b.firstProblem
	}
	for i := range a.choices {
		if a.choices[i].direction != b.choices[i].direction {
			return directionRank(a.choices[i].direction) < directionRank(b.choices[i].direction)
		}
	}
	return false
}

func directionRank(direction Direction) int {
	switch direction {
	case DirectionUp:
		return 0
	case DirectionPeer:
		return 1
	default:
		return 2
	}
}

func issueAt(hop Hop, choice hopChoice) *Issue {
	return &Issue{
		HopIndex: hop.Index,
		From:     hop.From,
		To:       hop.To,
		Class:    hop.Class,
		Reason:   choice.reason,
		Detail:   choice.detail,
	}
}
