package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrNoAbuseTrace means the scheduler explored every state within MaxDepth and
// found no violation.
var ErrNoAbuseTrace = errors.New("netsec: no unsafe trajectory found")

// AuditFlaws describes assumptions made by an implementation under review.
// A secure implementation sets every field to false. The fields let an audit
// search explain the shortest way a flawed implementation can be abused.
type AuditFlaws struct {
	// PendingCanAuthenticate lets Pending methods log in or recover.
	PendingCanAuthenticate bool
	// PendingCanConfirm lets Pending methods count as confirmations.
	PendingCanConfirm bool
	// AllowEarlyActivation activates before the delay boundary.
	AllowEarlyActivation bool
}

// TwoChangeSearch configures the exhaustive scheduler search. The initial
// state contains two confirmed, pending changes competing for activation and
// two active strong factors.
type TwoChangeSearch struct {
	Delay                 int64 // delay in MonoTime nanoseconds
	MaxDepth              int
	RequiredConfirmations int
	InitialConfirmations  int
	Flaws                 AuditFlaws
}

// DefaultTwoChangeSearch returns a bounded, deterministic search.
func DefaultTwoChangeSearch() TwoChangeSearch {
	return TwoChangeSearch{
		Delay:                 int64(24 * 60 * 60 * 1e9),
		MaxDepth:              24,
		RequiredConfirmations: 2,
		InitialConfirmations:  2,
	}
}

// SearchStep is one scheduled action in a counterexample.
type SearchStep struct {
	Action   string `json:"action"`
	MethodID string `json:"methodID,omitempty"`
	ChangeID string `json:"changeID,omitempty"`
	At       int64  `json:"at"`
}

// AbuseTrace is the shortest unsafe schedule found by the search.
type AbuseTrace struct {
	Violation string       `json:"violation"`
	Steps     int          `json:"steps"`
	Trace     []SearchStep `json:"trace"`
}

// FindShortestUnsafeTrajectory searches for the shortest schedule in which a
// Pending method authenticates/recoveries are accepted, Pending methods confirm
// each other, or a Pending change activates before its delay.
func FindShortestUnsafeTrajectory(cfg TwoChangeSearch) (*AbuseTrace, error) {
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 24
	}
	if cfg.Delay < 0 {
		cfg.Delay = 0
	}
	if cfg.RequiredConfirmations <= 0 {
		cfg.RequiredConfirmations = 2
	}
	if cfg.InitialConfirmations < 0 {
		cfg.InitialConfirmations = 0
	}
	start := initialAuditState(MonoTime(cfg.Delay), cfg.RequiredConfirmations, cfg.InitialConfirmations)

	type node struct {
		state auditState
		trace []SearchStep
	}
	queue := []node{{state: start}}
	visited := map[string]bool{start.key(): true}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if len(current.trace) >= cfg.MaxDepth {
			continue
		}
		for _, transition := range current.state.transitions(cfg.Flaws) {
			next := current.state.clone()
			violation := next.apply(transition, cfg.Flaws)
			trace := append(append([]SearchStep(nil), current.trace...), SearchStep{
				Action:   transition.kind,
				MethodID: transition.methodID,
				ChangeID: transition.changeID,
				At:       int64(next.time),
			})
			if violation != "" {
				return &AbuseTrace{Violation: violation, Steps: len(trace), Trace: trace}, nil
			}
			key := next.key()
			if visited[key] {
				continue
			}
			visited[key] = true
			queue = append(queue, node{state: next, trace: trace})
		}
	}
	return nil, ErrNoAbuseTrace
}

// FindShortestUnsafeTrace is an alias for FindShortestUnsafeTrajectory.
func FindShortestUnsafeTrace(cfg TwoChangeSearch) (*AbuseTrace, error) {
	return FindShortestUnsafeTrajectory(cfg)
}

type auditMethod struct {
	kind   MethodKind
	status Status
}

type auditChange struct {
	id            string
	methodID      string
	kind          MethodKind
	status        Status
	notBefore     MonoTime
	required      int
	confirmations map[string]MethodKind
	pendingConf   map[string]bool
}

type auditState struct {
	time         MonoTime
	sessionValid bool
	generation   uint64
	methods      map[string]auditMethod
	changes      map[string]auditChange
}

type auditTransition struct {
	kind     string
	methodID string
	changeID string
	targetID string
	until    MonoTime
}

func initialAuditState(delay MonoTime, required, initial int) auditState {
	state := auditState{
		time:         0,
		sessionValid: true,
		generation:   1,
		methods: map[string]auditMethod{
			"device-1": {kind: MethodDeviceKey, status: StatusActive},
			"mail-1":   {kind: MethodEmail, status: StatusActive},
			"add-mail": {kind: MethodEmail, status: StatusPending},
			"add-key":  {kind: MethodRecoveryContact, status: StatusPending},
		},
		changes: map[string]auditChange{
			"change-a": {
				id:            "change-a",
				methodID:      "add-mail",
				kind:          MethodEmail,
				status:        StatusPending,
				notBefore:     delay,
				required:      required,
				confirmations: make(map[string]MethodKind),
				pendingConf:   make(map[string]bool),
			},
			"change-b": {
				id:            "change-b",
				methodID:      "add-key",
				kind:          MethodRecoveryContact,
				status:        StatusPending,
				notBefore:     delay,
				required:      required,
				confirmations: make(map[string]MethodKind),
				pendingConf:   make(map[string]bool),
			},
		},
	}
	if initial >= 1 {
		state.changes["change-a"].confirmations["device-1"] = MethodDeviceKey
	}
	if initial >= 2 {
		state.changes["change-a"].confirmations["mail-1"] = MethodEmail
	}
	if initial >= 1 {
		state.changes["change-b"].confirmations["mail-1"] = MethodEmail
	}
	if initial >= 2 {
		state.changes["change-b"].confirmations["device-1"] = MethodDeviceKey
	}
	return state
}

func (s auditState) clone() auditState {
	out := auditState{
		time:         s.time,
		sessionValid: s.sessionValid,
		generation:   s.generation,
		methods:      make(map[string]auditMethod, len(s.methods)),
		changes:      make(map[string]auditChange, len(s.changes)),
	}
	for k, v := range s.methods {
		out.methods[k] = v
	}
	for k, v := range s.changes {
		confirmations := make(map[string]MethodKind, len(v.confirmations))
		for id, kind := range v.confirmations {
			confirmations[id] = kind
		}
		pending := make(map[string]bool, len(v.pendingConf))
		for id, p := range v.pendingConf {
			pending[id] = p
		}
		v.confirmations = confirmations
		v.pendingConf = pending
		out.changes[k] = v
	}
	return out
}

func (s auditState) key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d|%t|%d", s.time, s.sessionValid, s.generation)
	methodIDs := make([]string, 0, len(s.methods))
	for id := range s.methods {
		methodIDs = append(methodIDs, id)
	}
	sort.Strings(methodIDs)
	for _, id := range methodIDs {
		m := s.methods[id]
		fmt.Fprintf(&b, "|m:%s:%s:%s", id, m.kind, m.status)
	}
	changeIDs := make([]string, 0, len(s.changes))
	for id := range s.changes {
		changeIDs = append(changeIDs, id)
	}
	sort.Strings(changeIDs)
	for _, id := range changeIDs {
		c := s.changes[id]
		fmt.Fprintf(&b, "|c:%s:%s:%d:%d", id, c.status, c.notBefore, c.required)
		confIDs := make([]string, 0, len(c.confirmations))
		for cid := range c.confirmations {
			confIDs = append(confIDs, cid)
		}
		sort.Strings(confIDs)
		for _, cid := range confIDs {
			fmt.Fprintf(&b, "|a:%s:%s", cid, c.confirmations[cid])
		}
		pendingIDs := make([]string, 0, len(c.pendingConf))
		for pid := range c.pendingConf {
			pendingIDs = append(pendingIDs, pid)
		}
		sort.Strings(pendingIDs)
		for _, pid := range pendingIDs {
			fmt.Fprintf(&b, "|p:%s", pid)
		}
	}
	return b.String()
}

func (s auditState) transitions(flaws AuditFlaws) []auditTransition {
	var out []auditTransition
	// Time advances only to a real boundary. This keeps the state graph finite.
	var next MonoTime
	for _, change := range s.changes {
		if change.status == StatusPending && change.notBefore > s.time {
			if next == 0 || change.notBefore < next {
				next = change.notBefore
			}
		}
	}
	if next > s.time {
		out = append(out, auditTransition{kind: "advance-time", until: next})
	}

	for _, id := range []string{"change-a", "change-b"} {
		change, ok := s.changes[id]
		if !ok || change.status != StatusPending {
			continue
		}
		out = append(out, auditTransition{kind: "activate", changeID: id, methodID: change.methodID})
		out = append(out, auditTransition{kind: "recover", changeID: id, methodID: change.methodID})
		out = append(out, auditTransition{kind: "login", changeID: id, methodID: change.methodID})
	}

	if a, ok := s.changes["change-a"]; ok && a.status == StatusPending {
		out = append(out, auditTransition{kind: "confirm", changeID: "change-b", methodID: a.methodID, targetID: "change-b"})
	}
	if b, ok := s.changes["change-b"]; ok && b.status == StatusPending {
		out = append(out, auditTransition{kind: "confirm", changeID: "change-a", methodID: b.methodID, targetID: "change-a"})
	}

	for _, id := range []string{"device-1", "mail-1"} {
		if s.methods[id].status == StatusActive {
			out = append(out, auditTransition{kind: "revoke-factor", methodID: id})
			out = append(out, auditTransition{kind: "confirm", methodID: id, targetID: "change-a"})
			out = append(out, auditTransition{kind: "confirm", methodID: id, targetID: "change-b"})
		}
	}
	if s.sessionValid {
		out = append(out, auditTransition{kind: "reset-password"})
	}
	return out
}

func (s *auditState) apply(t auditTransition, flaws AuditFlaws) string {
	switch t.kind {
	case "advance-time":
		if t.until > s.time {
			s.time = t.until
		}
		return ""

	case "activate":
		return s.applyActivate(t, flaws)

	case "recover", "login":
		change, ok := s.changes[t.changeID]
		if !ok {
			return ""
		}
		method := s.methods[change.methodID]
		if method.status == StatusActive {
			return ""
		}
		if method.status == StatusPending && flaws.PendingCanAuthenticate {
			if t.kind == "recover" {
				return "pending method completed account recovery"
			}
			return "pending method completed login"
		}
		return ""

	case "confirm":
		return s.applyConfirmation(t, flaws)

	case "revoke-factor":
		method, ok := s.methods[t.methodID]
		if !ok || method.status != StatusActive {
			return ""
		}
		method.status = StatusRevoked
		s.methods[t.methodID] = method
		return ""

	case "reset-password":
		s.sessionValid = false
		return ""
	}
	return ""
}

func (s *auditState) applyActivate(t auditTransition, flaws AuditFlaws) string {
	change, ok := s.changes[t.changeID]
	if !ok || change.status != StatusPending {
		return ""
	}
	if !s.sessionValid {
		return ""
	}
	activeCategories := make(map[MethodKind]bool)
	for id, kind := range change.confirmations {
		if m, ok := s.methods[id]; ok && m.status == StatusActive {
			activeCategories[kind] = true
		}
	}
	if len(activeCategories) < change.required {
		return ""
	}
	if s.time < change.notBefore {
		if !flaws.AllowEarlyActivation {
			return ""
		}
		change.status = StatusActive
		s.changes[t.changeID] = change
		method := s.methods[change.methodID]
		method.status = StatusActive
		s.methods[change.methodID] = method
		s.generation++
		return "pending change activated before its delay boundary"
	}
	change.status = StatusActive
	s.changes[t.changeID] = change
	method := s.methods[change.methodID]
	method.status = StatusActive
	s.methods[change.methodID] = method
	s.generation++
	for id, other := range s.changes {
		if id == t.changeID || other.status != StatusPending {
			continue
		}
		other.status = StatusRevoked
		s.changes[id] = other
		otherMethod := s.methods[other.methodID]
		otherMethod.status = StatusRevoked
		s.methods[other.methodID] = otherMethod
	}
	return ""
}

func (s *auditState) applyConfirmation(t auditTransition, flaws AuditFlaws) string {
	change, ok := s.changes[t.targetID]
	if !ok || change.status != StatusPending {
		return ""
	}
	source, ok := s.methods[t.methodID]
	if !ok {
		return ""
	}
	sourceID := t.methodID
	if source.status == StatusPending {
		if !flaws.PendingCanConfirm {
			return ""
		}
		if sourceID == change.methodID {
			return ""
		}
		change.pendingConf[sourceID] = true
		s.changes[t.targetID] = change

		// Two pending changes confirming each other is the canonical
		// mutual-confirmation counterexample.
		if t.targetID == "change-b" && s.changes["change-a"].pendingConf["add-key"] {
			return "pending methods confirmed each other"
		}
		if t.targetID == "change-a" && s.changes["change-b"].pendingConf["add-mail"] {
			return "pending methods confirmed each other"
		}
		if len(change.confirmations)+len(change.pendingConf) >= change.required {
			return "pending method counted as a change confirmation"
		}
		return ""
	}
	if source.status != StatusActive {
		return ""
	}
	change.confirmations[sourceID] = source.kind
	change.pendingConf = make(map[string]bool)
	s.changes[t.targetID] = change
	return ""
}
