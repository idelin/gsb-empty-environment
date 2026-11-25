package handoff

import (
	"fmt"
	"net/netip"
	"sort"
)

// EventID identifies one ordered protocol event.
type EventID string

// EventKind distinguishes route delivery, controller-observed ack, and cut.
type EventKind string

const (
	EventDeliver EventKind = "deliver"
	EventAck     EventKind = "ack"
	EventCut     EventKind = "cut"
)

// AuthzOp is a ground-truth grant or revocation at a cut event.
type AuthzOp struct {
	Grant  bool
	Tenant TenantID
	Prefix netip.Prefix
}

// Event is one vertex of the allowed-order partial order.
type Event struct {
	ID          EventID
	Kind        EventKind
	Egress      EgressID
	StatementID string
	Ops         []AuthzOp
	// Seq is the intended per-egress delivery order; -1 means not constrained
	// by the reorder window.
	Seq int
}

// Protocol is the independently replayable description of messages and order.
type Protocol struct {
	Spec       Spec
	Statements map[string]Statement
	Events     map[EventID]Event
	EventOrder []EventID
	Precedence [][2]EventID
	Sequence   map[EgressID][]EventID
}

// NewProtocol creates an empty protocol for spec.
func NewProtocol(spec Spec) (*Protocol, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	return &Protocol{
		Spec:       spec,
		Statements: make(map[string]Statement),
		Events:     make(map[EventID]Event),
		Sequence:   make(map[EgressID][]EventID),
	}, nil
}

// AddPrecedence adds a before b.
func (p *Protocol) AddPrecedence(before, after EventID) {
	p.Precedence = append(p.Precedence, [2]EventID{before, after})
}

func (p *Protocol) addEvent(ev Event) EventID {
	if ev.Kind == EventDeliver && ev.Seq == 0 {
		ev.Seq = len(p.Sequence[ev.Egress])
		p.Sequence[ev.Egress] = append(p.Sequence[ev.Egress], ev.ID)
	}
	if ev.Kind != EventDeliver && ev.Seq == 0 {
		ev.Seq = -1
	}
	if _, exists := p.Events[ev.ID]; exists {
		panic(fmt.Sprintf("duplicate protocol event %s", ev.ID))
	}
	p.Events[ev.ID] = ev
	p.EventOrder = append(p.EventOrder, ev.ID)
	return ev.ID
}

// AddStatementBroadcast adds deliver and ack events for every egress. Ack is
// ordered after its local delivery.
func (p *Protocol) AddStatementBroadcast(st Statement) (deliver, ack map[EgressID]EventID) {
	if _, exists := p.Statements[st.ID]; exists {
		panic(fmt.Sprintf("duplicate statement %s", st.ID))
	}
	p.Statements[st.ID] = st
	deliver = make(map[EgressID]EventID, len(p.Spec.Egresses))
	ack = make(map[EgressID]EventID, len(p.Spec.Egresses))
	for _, e := range p.Spec.Egresses {
		dID := EventID("deliver:" + st.ID + ":" + string(e))
		aID := EventID("ack:" + st.ID + ":" + string(e))
		dID = p.addEvent(Event{ID: dID, Kind: EventDeliver, Egress: e, StatementID: st.ID})
		p.addEvent(Event{ID: aID, Kind: EventAck, Egress: e, StatementID: st.ID})
		p.AddPrecedence(dID, aID)
		deliver[e] = dID
		ack[e] = aID
	}
	return deliver, ack
}

// RequireAcksBefore orders every egress ack of statementID before eventID.
func (p *Protocol) RequireAcksBefore(statementID string, eventID EventID) {
	for _, e := range p.Spec.Egresses {
		id := EventID("ack:" + statementID + ":" + string(e))
		p.AddPrecedence(id, eventID)
	}
}

// AddCut adds a global authorization cut event.
func (p *Protocol) AddCut(id string, ops ...AuthzOp) EventID {
	eid := EventID("cut:" + id)
	return p.addEvent(Event{ID: eid, Kind: EventCut, Ops: ops})
}

func (p *Protocol) predecessors() map[EventID]map[EventID]bool {
	pred := make(map[EventID]map[EventID]bool, len(p.Events))
	for id := range p.Events {
		pred[id] = make(map[EventID]bool)
	}
	for _, edge := range p.Precedence {
		if _, ok := p.Events[edge[0]]; !ok {
			panic(fmt.Sprintf("precedence references missing event %s", edge[0]))
		}
		if _, ok := p.Events[edge[1]]; !ok {
			panic(fmt.Sprintf("precedence references missing event %s", edge[1]))
		}
		pred[edge[1]][edge[0]] = true
	}
	return pred
}

type simState struct {
	views map[EgressID]*View
	authz *Authorization
	done  map[EventID]bool
	path  []EventID
}

func (p *Protocol) initialState() *simState {
	views := make(map[EgressID]*View, len(p.Spec.Egresses))
	for _, e := range p.Spec.Egresses {
		views[e] = NewView(e, p.Spec.initialClaimsFor(e)...)
	}
	authz := p.Spec.InitialAuthz
	if authz == nil {
		authz = NewAuthorization()
		for _, c := range p.Spec.InitialClaims {
			authz.Grant(c.Tenant, c.Prefix)
		}
		for _, claims := range p.Spec.InitialViews {
			for _, c := range claims {
				authz.Grant(c.Tenant, c.Prefix)
			}
		}
	}
	return &simState{
		views: views,
		authz: authz.Clone(),
		done:  make(map[EventID]bool, len(p.Events)),
	}
}

func (s *simState) cloneFor(event EventID) *simState {
	next := &simState{
		views: make(map[EgressID]*View, len(s.views)),
		authz: s.authz.Clone(),
		done:  make(map[EventID]bool, len(s.done)+1),
		path:  append(append([]EventID(nil), s.path...), event),
	}
	for e, v := range s.views {
		next.views[e] = v.Clone()
	}
	for id := range s.done {
		next.done[id] = true
	}
	next.done[event] = true
	return next
}

func (p *Protocol) apply(st *simState, ev Event) {
	switch ev.Kind {
	case EventDeliver:
		stmt := p.Statements[ev.StatementID]
		_, _ = st.views[ev.Egress].Apply(stmt)
	case EventCut:
		for _, op := range ev.Ops {
			if op.Grant {
				st.authz.Grant(op.Tenant, op.Prefix)
			} else {
				st.authz.Revoke(op.Tenant, op.Prefix)
			}
		}
	case EventAck:
		// Receipt observation changes controller knowledge, not the RIB or
		// ground-truth authorization.
	}
}

func (p *Protocol) available(st *simState, pred map[EventID]map[EventID]bool) []EventID {
	var out []EventID
	for _, id := range p.EventOrder {
		if st.done[id] {
			continue
		}
		ready := true
		for before := range pred[id] {
			if !st.done[before] {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		ev := p.Events[id]
		if ev.Egress != "" && ev.Seq >= 0 && ev.Kind == EventDeliver {
			window := p.Spec.windowFor(ev.Egress)
			minUndelivered := p.minUndeliveredSeq(ev.Egress, st)
			if ev.Seq >= minUndelivered+window {
				continue
			}
		}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (p *Protocol) minUndeliveredSeq(e EgressID, st *simState) int {
	min := int(^uint(0) >> 1)
	for _, id := range p.Sequence[e] {
		ev := p.Events[id]
		if !st.done[id] && ev.Seq < min {
			min = ev.Seq
		}
	}
	if min == int(^uint(0)>>1) {
		return len(p.Sequence[e])
	}
	return min
}

// EnumerateTraces returns all complete allowed event orders.
func (p *Protocol) EnumerateTraces(limit int) ([][]EventID, error) {
	if limit <= 0 {
		limit = 100000
	}
	pred := p.predecessors()
	root := p.initialState()
	var traces [][]EventID
	var dfs func(*simState) error
	dfs = func(st *simState) error {
		if len(traces) >= limit {
			return ErrTracesExceeded
		}
		next := p.available(st, pred)
		if len(next) == 0 {
			trace := append([]EventID(nil), st.path...)
			traces = append(traces, trace)
			return nil
		}
		for _, id := range next {
			ev := p.Events[id]
			ns := st.cloneFor(id)
			p.apply(ns, ev)
			if err := dfs(ns); err != nil {
				return err
			}
		}
		return nil
	}
	if err := dfs(root); err != nil {
		return traces, err
	}
	return traces, nil
}
