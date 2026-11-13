package netsec

import (
	"fmt"
	"sort"
	"time"
)

// AuditActionKind is one externally observable event in a graceful-restart
// audit trace.
type AuditActionKind string

const (
	AuditDisconnect AuditActionKind = "disconnect"
	AuditPolicy     AuditActionKind = "policy-update"
	AuditVRP        AuditActionKind = "vrp-update"
	AuditReconnect  AuditActionKind = "reconnect"
	AuditAnnounce   AuditActionKind = "announce"
	AuditEOR        AuditActionKind = "end-of-rib"
	AuditAdvance    AuditActionKind = "advance"
)

// AuditAction is a declarative event.  SearchShortestViolation interleaves the
// actions in every order and replays each candidate trace from a fresh ledger.
// Actions that are not applicable in a given state simply make that branch
// invalid; they are not reported as security findings.
type AuditAction struct {
	Kind          AuditActionKind
	Duration      time.Duration // AuditAdvance; also optional audit label
	Route         Route         // AuditAnnounce
	Reason        DisconnectReason
	PolicyVersion PolicyVersion
	Policy        func(Route) bool
	VRPVersion    VRPVersion
	VRP           func(Route) bool
	Generation    uint64 // AuditReconnect; zero means previous + 1
	NoGraceful    bool   // AuditReconnect with graceful restart disabled
}

// AuditScenario describes the deterministic prefix of a trace.  The first
// session is always opened with graceful restart and generation 1, and
// InitialRoute is announced on it when non-empty.
type AuditScenario struct {
	Peer         Peer
	RestartTime  time.Duration
	Start        time.Time
	InitialRoute Route

	InitialPolicyVersion PolicyVersion
	InitialVRPVersion    VRPVersion
	InitialPolicy        func(Route) bool
	InitialVRP           func(Route) bool

	Actions []AuditAction

	// MaxDepth limits the search.  Zero means len(Actions), capped at 9 to
	// avoid factorial blowups.
	MaxDepth int
}

// AuditFinding is a shortest trace that reaches a stale-route safety
// violation.
type AuditFinding struct {
	Violation string
	Detail    string
	At        time.Time
	Trace     []AuditAction
	Stale     StaleRoute
}

// SearchShortestViolation performs a breadth-first search over interleavings
// of scenario.Actions.  It looks for a stale route that is still forwardable
// after policy/VRP revocation, is exportable, participates in FlowSpec
// validation, survives EOR for an older generation, or is forwardable at its
// deadline.  A correct implementation returns (nil, nil).
func SearchShortestViolation(sc AuditScenario) (*AuditFinding, error) {
	n := len(sc.Actions)
	maxDepth := sc.MaxDepth
	if maxDepth <= 0 || maxDepth > n {
		maxDepth = n
	}
	if maxDepth > 9 {
		maxDepth = 9
	}

	// Depth zero still checks the initial state.
	for depth := 0; depth <= maxDepth; depth++ {
		var found *AuditFinding
		permute(n, depth, func(indices []int) bool {
			finding, err := replayAudit(sc, indices)
			if err != nil {
				return false
			}
			if finding != nil {
				found = finding
				return true
			}
			return false
		})
		if found != nil {
			return found, nil
		}
	}
	return nil, nil
}

func permute(n, depth int, visit func([]int) bool) {
	if depth == 0 {
		visit(nil)
		return
	}
	used := make([]bool, n)
	indices := make([]int, 0, depth)
	var dfs func() bool
	dfs = func() bool {
		if len(indices) == depth {
			return visit(indices)
		}
		for i := 0; i < n; i++ {
			if used[i] {
				continue
			}
			used[i] = true
			indices = append(indices, i)
			stop := dfs()
			indices = indices[:len(indices)-1]
			used[i] = false
			if stop {
				return true
			}
		}
		return false
	}
	dfs()
}

type auditReplay struct {
	ledger  *Ledger
	clock   *VirtualClock
	sc      AuditScenario
	session *Session
	gen     uint64
	policy  func(Route) bool
	vrp     func(Route) bool

	routes         map[RouteID]Route
	announcedByGen map[uint64]map[RouteID]Route
	candidates     map[RouteID]*auditStaleCandidate
}

type auditStaleCandidate struct {
	route        Route
	generation   uint64
	deadline     time.Time
	mustWithdraw bool
}

func replayAudit(sc AuditScenario, indices []int) (*AuditFinding, error) {
	start := sc.Start
	if start.IsZero() {
		start = time.Unix(0, 0).UTC()
	}
	clk := NewVirtualClockAt(start)
	l := NewLedger(Config{
		Clock:        clk,
		RestartTime:  sc.RestartTime,
		ImportPolicy: sc.InitialPolicy,
		VRPValid:     sc.InitialVRP,
	})
	if sc.InitialPolicyVersion != 0 {
		l.UpdatePolicy(sc.InitialPolicyVersion)
	}
	if sc.InitialVRPVersion != 0 {
		l.UpdateVRP(sc.InitialVRPVersion)
	}

	peer := sc.Peer
	if peer.RouterID == "" {
		return nil, fmt.Errorf("audit: missing router ID")
	}
	s, err := l.OpenSession(peer, uint64(1), true)
	if err != nil {
		return nil, err
	}
	r := auditReplay{
		ledger:         l,
		clock:          clk,
		sc:             sc,
		session:        s,
		gen:            1,
		policy:         sc.InitialPolicy,
		vrp:            sc.InitialVRP,
		routes:         make(map[RouteID]Route),
		announcedByGen: map[uint64]map[RouteID]Route{1: {}},
		candidates:     make(map[RouteID]*auditStaleCandidate),
	}
	if sc.InitialRoute.Prefix != "" {
		if err := s.Announce(sc.InitialRoute); err != nil {
			return nil, err
		}
		r.recordAnnouncement(1, sc.InitialRoute)
	}
	if finding := r.check(); finding != nil {
		return finding, nil
	}

	applied := make([]AuditAction, 0, len(indices))
	for _, idx := range indices {
		action := sc.Actions[idx]
		if err := r.apply(action); err != nil {
			return nil, err
		}
		applied = append(applied, action)
		if finding := r.check(); finding != nil {
			finding.Trace = append([]AuditAction(nil), applied...)
			return finding, nil
		}
	}
	return nil, nil
}

func (r *auditReplay) apply(a AuditAction) error {
	switch a.Kind {
	case AuditDisconnect:
		if r.session == nil || !r.session.Active() {
			return fmt.Errorf("audit: no active session to disconnect")
		}
		gen := r.gen
		grace := r.session.GracefulRestart() && r.sc.RestartTime > 0 && !a.Reason.AuthenticationFailed()
		err := r.session.Disconnect(a.Reason)
		if err != nil {
			return err
		}
		now := r.clock.Now()
		for id, route := range r.announcedByGen[gen] {
			r.candidates[id] = &auditStaleCandidate{
				route:        route,
				generation:   gen,
				deadline:     now.Add(r.sc.RestartTime),
				mustWithdraw: !grace,
			}
		}
		return nil
	case AuditReconnect:
		gen := a.Generation
		if gen == 0 {
			gen = r.gen + 1
		}
		grace := !a.NoGraceful
		s, err := r.ledger.OpenSession(r.sc.Peer, gen, grace)
		if err != nil {
			return err
		}
		r.session = s
		r.gen = gen
		r.announcedByGen[gen] = make(map[RouteID]Route)
		return nil
	case AuditAnnounce:
		if r.session == nil || !r.session.Active() {
			return fmt.Errorf("audit: no active session to announce")
		}
		if err := r.session.Announce(a.Route); err != nil {
			return err
		}
		r.recordAnnouncement(r.gen, a.Route)
		return nil
	case AuditEOR:
		if r.session == nil || !r.session.Active() {
			return fmt.Errorf("audit: no active session for EOR")
		}
		return r.session.EndOfRIB()
	case AuditAdvance:
		r.ledger.Advance(a.Duration)
		return nil
	case AuditPolicy:
		r.ledger.UpdatePolicy(a.PolicyVersion, a.Policy)
		if a.Policy != nil {
			r.policy = a.Policy
		}
		return nil
	case AuditVRP:
		r.ledger.UpdateVRP(a.VRPVersion, a.VRP)
		if a.VRP != nil {
			r.vrp = a.VRP
		}
		return nil
	default:
		return fmt.Errorf("audit: unknown action %q", a.Kind)
	}
}

func (r *auditReplay) recordAnnouncement(gen uint64, route Route) {
	route = route.normalized()
	if route.ID == "" {
		// The ledger may have generated an ID.  Find the fresh route with the
		// same peer and prefix.
		for _, v := range r.ledger.FreshRoutes() {
			if v.Prefix == route.Prefix && v.Peer == r.sc.Peer {
				route.ID = v.RouteID
				if route.AttrsDigest == "" {
					route.AttrsDigest = v.AttrsDigest
				}
				break
			}
		}
	}
	if route.ID == "" {
		return
	}
	route = route.normalized()
	r.routes[route.ID] = route
	if r.announcedByGen[gen] == nil {
		r.announcedByGen[gen] = make(map[RouteID]Route)
	}
	r.announcedByGen[gen][route.ID] = route

	// A successful announcement of the same route ID or prefix replaces the
	// stale record, so the old candidate is no longer expected to be stale.
	for id, c := range r.candidates {
		if c.route.Prefix == route.Prefix {
			delete(r.candidates, id)
		}
	}
}

func (r *auditReplay) check() *AuditFinding {
	now := r.clock.Now()
	stale := r.ledger.StaleRoutes()
	// Sort for deterministic findings.
	sort.Slice(stale, func(i, j int) bool {
		if stale[i].RouteID != stale[j].RouteID {
			return stale[i].RouteID < stale[j].RouteID
		}
		return stale[i].SessionGeneration < stale[j].SessionGeneration
	})
	for _, sr := range stale {
		route := Route{ID: sr.RouteID, Prefix: sr.Prefix, AttrsDigest: sr.AttrsDigest}
		if r.policy != nil && !r.policy(route) && r.ledger.CanForward(sr.RouteID) {
			return &AuditFinding{
				Violation: "policy-revoked-stale-forwarded",
				Detail:    fmt.Sprintf("route %s remains forwardable after policy revocation", sr.RouteID),
				At:        now,
				Stale:     sr,
			}
		}
		if r.vrp != nil && !r.vrp(route) && r.ledger.CanForward(sr.RouteID) {
			return &AuditFinding{
				Violation: "vrp-revoked-stale-forwarded",
				Detail:    fmt.Sprintf("route %s remains forwardable after VRP revocation", sr.RouteID),
				At:        now,
				Stale:     sr,
			}
		}
		if r.ledger.CanExport(sr.RouteID) {
			return &AuditFinding{
				Violation: "stale-reexported",
				Detail:    fmt.Sprintf("stale route %s is exportable", sr.RouteID),
				At:        now,
				Stale:     sr,
			}
		}
		if r.ledger.CanFlowSpec(sr.RouteID) {
			return &AuditFinding{
				Violation: "stale-in-flowspec",
				Detail:    fmt.Sprintf("stale route %s is eligible for FlowSpec", sr.RouteID),
				At:        now,
				Stale:     sr,
			}
		}
		if !now.Before(sr.Deadline) && r.ledger.CanForward(sr.RouteID) {
			return &AuditFinding{
				Violation: "stale-past-deadline",
				Detail:    fmt.Sprintf("route %s is forwardable at/after deadline", sr.RouteID),
				At:        now,
				Stale:     sr,
			}
		}
		if r.session != nil && r.session.EORReceived() && sr.SessionGeneration < r.gen {
			return &AuditFinding{
				Violation: "stale-survived-eor",
				Detail:    fmt.Sprintf("old-generation route %s survived End-of-RIB", sr.RouteID),
				At:        now,
				Stale:     sr,
			}
		}
	}
	// Also check tracked stale candidates directly.  This catches a broken
	// implementation that hides the stale record from StaleRoutes while still
	// forwarding or exporting its route ID.
	for id, c := range r.candidates {
		route := c.route
		if c.mustWithdraw && (r.ledger.CanForward(id) || r.ledger.CanExport(id)) {
			return &AuditFinding{
				Violation: "revoked-route-still-used",
				Detail:    fmt.Sprintf("route %s remains usable after its stale authorization was revoked", id),
				At:        now,
				Stale:     candidateAsStale(c),
			}
		}
		if r.policy != nil && !r.policy(route) && (r.ledger.CanForward(id) || r.ledger.CanExport(id)) {
			return &AuditFinding{
				Violation: "policy-revoked-stale-used",
				Detail:    fmt.Sprintf("route %s remains usable after policy revocation", id),
				At:        now,
				Stale:     candidateAsStale(c),
			}
		}
		if r.vrp != nil && !r.vrp(route) && (r.ledger.CanForward(id) || r.ledger.CanExport(id)) {
			return &AuditFinding{
				Violation: "vrp-revoked-stale-used",
				Detail:    fmt.Sprintf("route %s remains usable after VRP revocation", id),
				At:        now,
				Stale:     candidateAsStale(c),
			}
		}
		if !now.Before(c.deadline) && r.ledger.CanForward(id) {
			return &AuditFinding{
				Violation: "stale-past-deadline",
				Detail:    fmt.Sprintf("route %s is forwardable at/after deadline", id),
				At:        now,
				Stale:     candidateAsStale(c),
			}
		}
		if r.session != nil && r.session.EORReceived() && c.generation < r.gen && r.ledger.CanForward(id) {
			return &AuditFinding{
				Violation: "stale-survived-eor",
				Detail:    fmt.Sprintf("old-generation route %s survived End-of-RIB", id),
				At:        now,
				Stale:     candidateAsStale(c),
			}
		}
		if r.ledger.CanExport(id) && r.isStaleCandidateActive(c) {
			return &AuditFinding{
				Violation: "stale-reexported",
				Detail:    fmt.Sprintf("stale route %s is exportable", id),
				At:        now,
				Stale:     candidateAsStale(c),
			}
		}
	}
	return nil
}

func (r *auditReplay) isStaleCandidateActive(c *auditStaleCandidate) bool {
	if c.mustWithdraw {
		return false
	}
	return r.clock.Now().Before(c.deadline)
}

func candidateAsStale(c *auditStaleCandidate) StaleRoute {
	return StaleRoute{
		RouteID:             c.route.ID,
		ID:                  c.route.ID,
		Prefix:              c.route.Prefix,
		AttrsDigest:         c.route.AttrsDigest,
		Attrs:               c.route.AttrsDigest,
		Attributes:          c.route.AttrsDigest,
		SessionGeneration:   c.generation,
		TransportGeneration: c.generation,
		Deadline:            c.deadline,
		StaleUntil:          c.deadline,
		ExpiresAt:           c.deadline,
		State:               StateStale,
	}
}
