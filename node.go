package jointpaxos

import (
	"fmt"
	"reflect"
)

// Timeout is the leader-retry interval in virtual time units.
const Timeout = 10

// Value is what a view decides: a client command plus, optionally, the
// config of the next view. The config chain is itself decided by
// consensus, so every node agrees on when membership changes.
type Value struct {
	Cmd  string
	Next *Config // nil: keep current config (terminal for the chain)
}

func equalValue(a, b Value) bool { return reflect.DeepEqual(a, b) }

func (v Value) String() string {
	if v.Next != nil {
		return fmt.Sprintf("{cmd=%q next=%s}", v.Cmd, v.Next)
	}
	return fmt.Sprintf("{cmd=%q}", v.Cmd)
}

// Ballot orders leadership attempts inside a view.
type Ballot struct {
	View int
	Num  int
}

func (b Ballot) Less(o Ballot) bool {
	if b.View != o.View {
		return b.View < o.View
	}
	return b.Num < o.Num
}

// MsgKind enumerates protocol messages.
type MsgKind int

const (
	MPrepare MsgKind = iota
	MPromise
	MAccept
	MAccepted
	MCommit
)

func (k MsgKind) String() string {
	switch k {
	case MPrepare:
		return "Prepare"
	case MPromise:
		return "Promise"
	case MAccept:
		return "Accept"
	case MAccepted:
		return "Accepted"
	case MCommit:
		return "Commit"
	}
	return "?"
}

// Msg is the single wire format. Every message is stamped with the view
// version it belongs to; receivers only act on messages of their current
// view, so late votes from an old view never count in a newer one.
type Msg struct {
	Kind      MsgKind
	View      int
	Bal       Ballot
	Val       Value  // Accept/Commit: the value; Promise: accepted value if HasVal
	AccBal    Ballot // Promise: ballot at which Val was accepted
	HasVal    bool   // Promise: sender has accepted a value
	Committed bool   // Promise: sender already committed Val in this view
	From      string
	To        string
}

func (m Msg) describe() string {
	return fmt.Sprintf("%s(v=%d,b=%d,%s->%s)", m.Kind, m.View, m.Bal.Num, m.From, m.To)
}

// ViewState is the acceptor state for one view. It lives in Store, which
// models stable storage: it survives crashes and is reloaded on restart.
type ViewState struct {
	Promised  Ballot
	AccBal    Ballot
	AccVal    Value
	Accepted  bool
	Committed bool
	Val       Value
}

// Store is a node's persistent state (stable storage in the simulation).
type Store struct {
	CurrentView int
	Confs       map[int]Config
	Views       map[int]*ViewState
}

func NewStore(initial Config) *Store {
	return &Store{
		CurrentView: 0,
		Confs:       map[int]Config{0: initial},
		Views:       map[int]*ViewState{},
	}
}

// proposal is volatile proposer state for one view (lost on crash, which
// is safe: a crashed leader is replaced via a higher ballot).
type proposal struct {
	bal      Ballot
	phase    int // 1 = collecting promises, 2 = collecting accepts
	promises map[string]Msg
	accepts  map[string]bool
	val      Value
	done     bool
}

// Node is one replica.
type Node struct {
	ID      string
	sim     *Sim
	net     *Network
	store   *Store
	crashed bool
	prop    map[int]*proposal
	attempt map[int]int

	// ProposeFn supplies the leader's initial proposal for each view.
	ProposeFn func(view int) Value

	// Instrumentation used by tests and the demo.
	StaleIgnored     int
	FutureIgnored    int
	RejectedReconfig int
	Violations       []string
	CommitLog        []Value
}

func NewNode(id string, store *Store, sim *Sim, net *Network, propose func(view int) Value) *Node {
	nd := &Node{
		ID:        id,
		sim:       sim,
		net:       net,
		store:     store,
		prop:      map[int]*proposal{},
		attempt:   map[int]int{},
		ProposeFn: propose,
	}
	net.nodes[id] = nd
	return nd
}

// Boot starts participation in view 0.
func (nd *Node) Boot() { nd.enterView(0) }

func (nd *Node) vs(view int) *ViewState {
	s := nd.store.Views[view]
	if s == nil {
		s = &ViewState{Promised: Ballot{View: view, Num: -1}}
		nd.store.Views[view] = s
	}
	return s
}

func (nd *Node) conf(view int) Config { return nd.store.Confs[view] }

func (nd *Node) isMember(view int) bool {
	for _, m := range nd.conf(view).Members() {
		if m == nd.ID {
			return true
		}
	}
	return false
}

// enterView joins a view whose config is already known (decided by the
// previous view). Proposer state is volatile and starts empty.
func (nd *Node) enterView(view int) {
	nd.store.CurrentView = view
	nd.sim.Logf("%s enters view %d  config=%s", nd.ID, view, nd.conf(view))
	if !nd.isMember(view) {
		return // non-members only learn the outcome
	}
	nd.maybeLead(view)
	nd.scheduleCheck(view)
}

func (nd *Node) leaderOf(view, num int) string {
	members := nd.conf(view).Members()
	return members[num%len(members)]
}

func (nd *Node) maybeLead(view int) {
	if nd.vs(view).Committed {
		return
	}
	num := nd.attempt[view]
	if nd.leaderOf(view, num) == nd.ID {
		nd.startPrepare(view, Ballot{View: view, Num: num})
	}
}

func (nd *Node) scheduleCheck(view int) {
	nd.sim.After(Timeout, func() {
		if nd.crashed || nd.store.CurrentView != view || nd.vs(view).Committed {
			return
		}
		nd.attempt[view]++
		nd.maybeLead(view)
		nd.scheduleCheck(view)
	})
}

// --- proposer ---

func (nd *Node) startPrepare(view int, bal Ballot) {
	nd.prop[view] = &proposal{
		bal:      bal,
		phase:    1,
		promises: map[string]Msg{},
		accepts:  map[string]bool{},
	}
	nd.sim.Logf("%s starts Prepare for view %d ballot %d", nd.ID, view, bal.Num)
	for _, m := range nd.conf(view).Members() {
		nd.net.Send(Msg{Kind: MPrepare, View: view, Bal: bal, From: nd.ID, To: m})
	}
}

func (nd *Node) handlePromise(m Msg) {
	p := nd.prop[m.View]
	if p == nil || p.bal != m.Bal || p.phase != 1 {
		return
	}
	p.promises[m.From] = m
	votes := map[string]bool{}
	for from := range p.promises {
		votes[from] = true
	}
	if !nd.conf(m.View).Quorum(votes) {
		return
	}
	// A quorum promised. If anyone already committed, adopt and rebroadcast.
	chosen := nd.ProposeFn(m.View)
	best := Ballot{View: m.View, Num: -1}
	for _, pm := range p.promises {
		if pm.Committed {
			nd.sim.Logf("%s learns committed value for view %d via promise", nd.ID, m.View)
			nd.commit(m.View, pm.Val)
			return
		}
		if pm.HasVal && !pm.AccBal.Less(best) {
			best = pm.AccBal
			chosen = pm.Val
		}
	}
	p.phase = 2
	p.val = chosen
	nd.sim.Logf("%s proposes %s in view %d ballot %d", nd.ID, chosen, m.View, p.bal.Num)
	for _, mem := range nd.conf(m.View).Members() {
		nd.net.Send(Msg{Kind: MAccept, View: m.View, Bal: p.bal, Val: chosen, From: nd.ID, To: mem})
	}
}

func (nd *Node) handleAccepted(m Msg) {
	p := nd.prop[m.View]
	if p == nil || p.bal != m.Bal || p.phase != 2 || p.done {
		return
	}
	p.accepts[m.From] = true
	if !nd.conf(m.View).Quorum(p.accepts) {
		return
	}
	p.done = true
	nd.commit(m.View, p.val)
}

func (nd *Node) commit(view int, val Value) {
	vs := nd.vs(view)
	if vs.Committed {
		if !equalValue(vs.Val, val) {
			nd.Violations = append(nd.Violations,
				fmt.Sprintf("conflicting commit in view %d: %s vs %s", view, vs.Val, val))
		}
		return
	}
	vs.Committed = true
	vs.Val = val
	nd.CommitLog = append(nd.CommitLog, val)
	nd.sim.Logf("%s COMMITS view %d: %s", nd.ID, view, val)
	// Broadcast the decision to every known node so the whole system
	// (including members that only join in the next view) can advance.
	for to := range nd.net.nodes {
		nd.net.Send(Msg{Kind: MCommit, View: view, Bal: Ballot{View: view}, Val: val, From: nd.ID, To: to})
	}
	nd.advance(view, val)
}

// --- acceptor ---

func (nd *Node) handlePrepare(m Msg) {
	vs := nd.vs(m.View)
	reply := Msg{Kind: MPromise, View: m.View, Bal: m.Bal, From: nd.ID, To: m.From}
	if vs.Committed {
		reply.Committed = true
		reply.Val = vs.Val
		nd.net.Send(reply)
		return
	}
	if !vs.Promised.Less(m.Bal) {
		return // already promised to this or a higher ballot
	}
	vs.Promised = m.Bal
	if vs.Accepted {
		reply.HasVal = true
		reply.Val = vs.AccVal
		reply.AccBal = vs.AccBal
	}
	nd.net.Send(reply)
}

func (nd *Node) handleAccept(m Msg) {
	vs := nd.vs(m.View)
	if vs.Committed {
		return
	}
	if m.Val.Next != nil && !validTransition(nd.conf(m.View), *m.Val.Next) {
		nd.RejectedReconfig++
		nd.sim.Logf("%s REJECTS unsafe reconfig %s -> %s (disjoint switch without joint stage)",
			nd.ID, nd.conf(m.View), *m.Val.Next)
		return
	}
	if vs.Promised.Less(m.Bal) || vs.Promised == m.Bal {
		vs.Promised = m.Bal
		vs.Accepted = true
		vs.AccBal = m.Bal
		vs.AccVal = m.Val
		nd.net.Send(Msg{Kind: MAccepted, View: m.View, Bal: m.Bal, From: nd.ID, To: m.From})
	}
}

func (nd *Node) handleCommit(m Msg) {
	vs := nd.vs(m.View)
	if vs.Committed {
		if !equalValue(vs.Val, m.Val) {
			nd.Violations = append(nd.Violations,
				fmt.Sprintf("conflicting commit in view %d: %s vs %s", m.View, vs.Val, m.Val))
		}
		return
	}
	if m.Val.Next != nil && !validTransition(nd.conf(m.View), *m.Val.Next) {
		nd.RejectedReconfig++
		nd.sim.Logf("%s REJECTS committed reconfig %s -> %s (disjoint switch without joint stage)",
			nd.ID, nd.conf(m.View), *m.Val.Next)
		return
	}
	vs.Committed = true
	vs.Val = m.Val
	nd.CommitLog = append(nd.CommitLog, m.Val)
	nd.sim.Logf("%s learns commit of view %d: %s", nd.ID, m.View, m.Val)
	nd.advance(m.View, m.Val)
}

// advance installs the decided next config and joins the next view.
func (nd *Node) advance(view int, val Value) {
	if val.Next == nil {
		return
	}
	next := view + 1
	nd.store.Confs[next] = *val.Next
	if nd.store.CurrentView == view {
		nd.enterView(next)
	}
}

// --- fault handling ---

// Crash stops the node. Volatile proposer state is lost; Store survives.
func (nd *Node) Crash() {
	nd.crashed = true
	nd.prop = map[int]*proposal{}
	nd.sim.Logf("%s CRASHES", nd.ID)
}

// Restart brings the node back from its persisted Store. Promised and
// accepted ballots survive, so a restarted node never breaks a promise.
func (nd *Node) Restart() {
	nd.crashed = false
	nd.sim.Logf("%s restarts, resuming view %d", nd.ID, nd.store.CurrentView)
	// Help stragglers: rebroadcast every decision we know about.
	for v := 0; v <= nd.store.CurrentView; v++ {
		if vs := nd.store.Views[v]; vs != nil && vs.Committed {
			for to := range nd.net.nodes {
				nd.net.Send(Msg{Kind: MCommit, View: v, Val: vs.Val, From: nd.ID, To: to})
			}
		}
	}
	// Catch-up ping: a ballot too low to disturb anyone, but peers in a
	// newer view answer with the commits we missed while crashed.
	for to := range nd.net.nodes {
		if to != nd.ID {
			nd.net.Send(Msg{Kind: MPrepare, View: nd.store.CurrentView,
				Bal: Ballot{View: nd.store.CurrentView, Num: -1}, From: nd.ID, To: to})
		}
	}
	view := nd.store.CurrentView
	if nd.isMember(view) && !nd.vs(view).Committed {
		nd.maybeLead(view)
		nd.scheduleCheck(view)
	}
}

// Handle is the message entry point. View-version filtering happens here:
// messages from older views are stale and never counted; messages from
// future views cannot be validated yet and are dropped (the sender's
// commit broadcast will bring this node up to date).
func (nd *Node) Handle(m Msg) {
	if nd.crashed {
		return
	}
	if m.View < nd.store.CurrentView {
		nd.StaleIgnored++
		nd.sim.Logf("%s ignores stale %s from %s (view %d < current %d)",
			nd.ID, m.Kind, m.From, m.View, nd.store.CurrentView)
		// The sender is behind: send every decision it is missing.
		if m.Kind != MCommit {
			for v := m.View; v <= nd.store.CurrentView; v++ {
				if vs := nd.store.Views[v]; vs != nil && vs.Committed {
					nd.net.Send(Msg{Kind: MCommit, View: v, Val: vs.Val, From: nd.ID, To: m.From})
				}
			}
		}
		return
	}
	if m.View > nd.store.CurrentView {
		nd.FutureIgnored++
		return
	}
	switch m.Kind {
	case MPrepare:
		nd.handlePrepare(m)
	case MPromise:
		nd.handlePromise(m)
	case MAccept:
		nd.handleAccept(m)
	case MAccepted:
		nd.handleAccepted(m)
	case MCommit:
		nd.handleCommit(m)
	}
}

// --- read-only accessors for tests and the demo ---

// CurrentView returns the view the node currently participates in.
func (nd *Node) CurrentView() int { return nd.store.CurrentView }

// Committed reports the committed value of a view, if any.
func (nd *Node) Committed(view int) (Value, bool) {
	if vs := nd.store.Views[view]; vs != nil && vs.Committed {
		return vs.Val, true
	}
	return Value{}, false
}

// CommittedCmds lists the commands of all committed views, in order.
func (nd *Node) CommittedCmds() []string {
	var out []string
	for _, v := range nd.CommitLog {
		out = append(out, v.Cmd)
	}
	return out
}

// StaleIgnoredCount returns how many old-view messages were dropped.
func (nd *Node) StaleIgnoredCount() int { return nd.StaleIgnored }

// RejectedReconfigs returns how many unsafe config switches were refused.
func (nd *Node) RejectedReconfigs() int { return nd.RejectedReconfig }

// ViolationList returns recorded safety violations (empty in safe runs).
func (nd *Node) ViolationList() []string { return nd.Violations }
