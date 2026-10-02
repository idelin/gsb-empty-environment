// Package paxos implements a Multi-Paxos majority-consensus replicated log
// with a gap-free commit engine.
//
// Protocol summary:
//   - Phase 1 (Prepare/Promise): a candidate leader picks a ballot higher than
//     any it has seen and collects promises from a majority. Promises carry the
//     acceptor's accepted log, which the leader uses to repair the log: for
//     every slot up to the highest seen, it re-proposes the value accepted
//     under the highest ballot, filling holes with no-ops.
//   - Phase 2 (Accept/Accepted): the leader replicates a value for a slot and
//     commits it once a majority accepts.
//   - Leader lease: while the lease is valid the leader skips Phase 1 and
//     commits consecutive log entries directly via Phase 2. Safety never
//     depends on the lease: acceptors reject stale ballots, so a partitioned
//     old leader cannot commit once a new majority leader exists.
package paxos

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNoMajority is returned when a quorum cannot be reached.
var ErrNoMajority = errors.New("paxos: no majority reachable")

// ErrNotLeader is returned when a node is asked to propose but is not leader.
var ErrNotLeader = errors.New("paxos: not leader")

// OpKind enumerates replicated operations.
type OpKind int

const (
	// OpNoop fills log holes so the commit engine never stalls on gaps.
	OpNoop OpKind = iota
	// OpSet sets a key in the replicated state machine.
	OpSet
)

// Op is a replicated state-machine command.
type Op struct {
	Kind  OpKind
	Key   string
	Value string
}

func (op Op) String() string {
	if op.Kind == OpNoop {
		return "noop"
	}
	return fmt.Sprintf("set %s=%s", op.Key, op.Value)
}

// Ballot is a totally ordered proposal number: (N, NodeID).
type Ballot struct {
	N    int
	Node string
}

// Compare returns -1, 0 or 1 like strings.Compare.
func (b Ballot) Compare(o Ballot) int {
	if b.N != o.N {
		if b.N < o.N {
			return -1
		}
		return 1
	}
	return strings.Compare(b.Node, o.Node)
}

func (b Ballot) String() string { return fmt.Sprintf("(%d,%s)", b.N, b.Node) }

// Entry is a single log slot.
type Entry struct {
	Slot int
	Bal  Ballot
	Op   Op
}

// --- messages ---

// PrepareReq is a Phase 1a message.
type PrepareReq struct{ Bal Ballot }

// PrepareResp is a Phase 1b promise carrying the acceptor's log.
type PrepareResp struct {
	From     string
	Ok       bool
	Promised Ballot
	Accepted map[int]Entry
}

// AcceptReq is a Phase 2a message.
type AcceptReq struct {
	Bal  Ballot
	Slot int
	Op   Op
}

// AcceptResp is a Phase 2b message.
type AcceptResp struct {
	From     string
	Ok       bool
	Promised Ballot
}

// CommitMsg notifies acceptors that a slot is committed.
type CommitMsg struct {
	Slot int
	Bal  Ballot
	Op   Op
}

// --- network ---

// Network is an in-memory transport with partition simulation. RPCs are
// synchronous function calls; unreachable peers are silently absent, exactly
// like dropped packets.
type Network struct {
	mu      sync.Mutex
	nodes   map[string]*Node
	blocked map[[2]string]bool
}

// NewNetwork returns an empty transport.
func NewNetwork() *Network {
	return &Network{nodes: make(map[string]*Node), blocked: make(map[[2]string]bool)}
}

// Size returns the number of nodes in the cluster.
func (n *Network) Size() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.nodes)
}

// Majority returns the quorum size.
func (n *Network) Majority() int { return n.Size()/2 + 1 }

// Partition blocks all traffic between the given groups (traffic inside a
// group flows freely).
func (n *Network) Partition(groups ...[]string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	groupOf := make(map[string]int)
	for gi, g := range groups {
		for _, id := range g {
			groupOf[id] = gi
		}
	}
	for a := range n.nodes {
		for b := range n.nodes {
			if a == b {
				continue
			}
			ga, oka := groupOf[a]
			gb, okb := groupOf[b]
			if oka && okb && ga != gb {
				n.blocked[[2]string{a, b}] = true
			}
		}
	}
}

// Heal removes all partitions.
func (n *Network) Heal() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked = make(map[[2]string]bool)
}

func (n *Network) reachable(from, to string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return !n.blocked[[2]string{from, to}]
}

func (n *Network) peers(from string) []*Node {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []*Node
	for id, nd := range n.nodes {
		if !n.blocked[[2]string{from, id}] {
			out = append(out, nd)
		}
	}
	return out
}

// broadcastPrepare delivers Phase 1a to every reachable node (incl. self).
func (n *Network) broadcastPrepare(from string, req PrepareReq) []PrepareResp {
	var resps []PrepareResp
	for _, p := range n.peers(from) {
		resps = append(resps, p.handlePrepare(req))
	}
	return resps
}

func (n *Network) broadcastAccept(from string, req AcceptReq) []AcceptResp {
	var resps []AcceptResp
	for _, p := range n.peers(from) {
		resps = append(resps, p.handleAccept(req))
	}
	return resps
}

func (n *Network) broadcastCommit(from string, msg CommitMsg) {
	for _, p := range n.peers(from) {
		p.handleCommit(msg)
	}
}

// --- node ---

// Node is a single Paxos acceptor / candidate / leader.
type Node struct {
	mu  sync.Mutex
	id  string
	net *Network

	leaseDur time.Duration

	// acceptor state
	promised  Ballot
	log       map[int]Entry // accepted, not necessarily committed
	committed map[int]Entry // committed values learned so far

	// commit engine: applies committed slots strictly in order (gap-free)
	commitIndex int
	sm          map[string]string

	// leader state
	isLeader  bool
	leaderBal Ballot
	leaseExp  time.Time
	nextSlot  int

	// instrumentation for tests and demos
	PrepareRounds int // Phase 1 rounds initiated by this node
	AcceptRounds  int // Phase 2 rounds initiated by this node
}

// NewNode creates a node and attaches it to the network.
func NewNode(net *Network, id string, leaseDur time.Duration) *Node {
	nd := &Node{
		id:        id,
		net:       net,
		leaseDur:  leaseDur,
		log:       make(map[int]Entry),
		committed: make(map[int]Entry),
		sm:        make(map[string]string),
		nextSlot:  1,
	}
	net.mu.Lock()
	net.nodes[id] = nd
	net.mu.Unlock()
	return nd
}

// ID returns the node identifier.
func (n *Node) ID() string { return n.id }

// --- acceptor handlers (called via Network) ---

func (n *Node) handlePrepare(req PrepareReq) PrepareResp {
	n.mu.Lock()
	defer n.mu.Unlock()
	if req.Bal.Compare(n.promised) <= 0 {
		return PrepareResp{From: n.id, Ok: false, Promised: n.promised}
	}
	n.promised = req.Bal
	if n.isLeader && req.Bal.Node != n.id {
		n.isLeader = false // a higher ballot exists: step down
	}
	accepted := make(map[int]Entry, len(n.log))
	for s, e := range n.log {
		accepted[s] = e
	}
	return PrepareResp{From: n.id, Ok: true, Promised: n.promised, Accepted: accepted}
}

func (n *Node) handleAccept(req AcceptReq) AcceptResp {
	n.mu.Lock()
	defer n.mu.Unlock()
	if req.Bal.Compare(n.promised) < 0 {
		return AcceptResp{From: n.id, Ok: false, Promised: n.promised}
	}
	n.promised = req.Bal
	n.log[req.Slot] = Entry{Slot: req.Slot, Bal: req.Bal, Op: req.Op}
	return AcceptResp{From: n.id, Ok: true, Promised: n.promised}
}

func (n *Node) handleCommit(msg CommitMsg) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if msg.Bal.Compare(n.promised) < 0 {
		return // stale commit notification
	}
	if _, ok := n.committed[msg.Slot]; !ok {
		n.committed[msg.Slot] = Entry{Slot: msg.Slot, Bal: msg.Bal, Op: msg.Op}
	}
	n.applyLocked()
}

// applyLocked applies committed slots in strict slot order. It never skips a
// hole: slot k+1 is applied only after slot k.
func (n *Node) applyLocked() {
	for {
		e, ok := n.committed[n.commitIndex+1]
		if !ok {
			return
		}
		n.commitIndex++
		if e.Op.Kind == OpSet {
			n.sm[e.Op.Key] = e.Op.Value
		}
	}
}

// --- leader logic ---

// BecomeLeader runs Phase 1 and, on success, repairs the log: every slot up
// to the highest seen in the quorum is re-committed with the value accepted
// under the highest ballot; holes are filled with no-ops.
func (n *Node) BecomeLeader() error {
	n.mu.Lock()
	bal := Ballot{N: n.promised.N + 1, Node: n.id}
	n.PrepareRounds++
	n.mu.Unlock()

	resps := n.net.broadcastPrepare(n.id, PrepareReq{Bal: bal})

	majority := n.net.Majority()
	okCount := 0
	chosen := make(map[int]Entry)
	maxSlot := 0
	for _, r := range resps {
		if !r.Ok {
			continue
		}
		okCount++
		for slot, e := range r.Accepted {
			if cur, seen := chosen[slot]; !seen || e.Bal.Compare(cur.Bal) > 0 {
				chosen[slot] = e
			}
			if slot > maxSlot {
				maxSlot = slot
			}
		}
	}
	if okCount < majority {
		return ErrNoMajority
	}

	n.mu.Lock()
	n.isLeader = true
	n.leaderBal = bal
	n.leaseExp = time.Now().Add(n.leaseDur)
	n.nextSlot = maxSlot + 1
	n.mu.Unlock()

	// Repair: close every slot in [1, maxSlot] so the commit engine has no gaps.
	for slot := 1; slot <= maxSlot; slot++ {
		e, seen := chosen[slot]
		op := Op{Kind: OpNoop}
		if seen {
			op = e.Op
		}
		if err := n.acceptMajority(slot, op); err != nil {
			n.mu.Lock()
			n.isLeader = false
			n.mu.Unlock()
			return err
		}
	}
	return nil
}

// acceptMajority runs Phase 2 for one slot and commits on quorum accept.
func (n *Node) acceptMajority(slot int, op Op) error {
	n.mu.Lock()
	bal := n.leaderBal
	n.AcceptRounds++
	n.mu.Unlock()

	resps := n.net.broadcastAccept(n.id, AcceptReq{Bal: bal, Slot: slot, Op: op})

	okCount := 0
	for _, r := range resps {
		if r.Ok {
			okCount++
			continue
		}
		if r.Promised.Compare(bal) > 0 {
			// A higher ballot exists: we are a stale leader.
			n.mu.Lock()
			if r.Promised.Compare(n.promised) > 0 {
				n.promised = r.Promised
			}
			n.isLeader = false
			n.mu.Unlock()
		}
	}
	if okCount < n.net.Majority() {
		return ErrNoMajority
	}
	n.net.broadcastCommit(n.id, CommitMsg{Slot: slot, Bal: bal, Op: op})
	return nil
}

// Propose appends op to the log and commits it. If this node holds a valid
// lease, Phase 1 is skipped and the entry commits in a single Phase 2 round.
func (n *Node) Propose(op Op) (int, error) {
	n.mu.Lock()
	fastPath := n.isLeader && time.Now().Before(n.leaseExp)
	n.mu.Unlock()

	if !fastPath {
		if err := n.BecomeLeader(); err != nil {
			return 0, err
		}
	}

	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		return 0, ErrNotLeader
	}
	slot := n.nextSlot
	n.nextSlot++
	n.mu.Unlock()

	if err := n.acceptMajority(slot, op); err != nil {
		return 0, err
	}

	// A successful quorum accept renews the lease.
	n.mu.Lock()
	n.leaseExp = time.Now().Add(n.leaseDur)
	n.mu.Unlock()
	return slot, nil
}

// SyncAll pushes every committed slot to all reachable peers, overwriting
// stale (dirty) minority entries, and re-broadcasts commit notifications.
// Must be called on the current leader.
func (n *Node) SyncAll() error {
	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		return ErrNotLeader
	}
	slots := make([]int, 0, len(n.committed))
	for s := range n.committed {
		slots = append(slots, s)
	}
	sort.Ints(slots)
	entries := make([]Entry, 0, len(slots))
	for _, s := range slots {
		entries = append(entries, n.committed[s])
	}
	n.mu.Unlock()

	for _, e := range entries {
		if err := n.acceptMajority(e.Slot, e.Op); err != nil {
			return err
		}
	}
	return nil
}

// --- read-only accessors (for tests / demos) ---

// StateMachine returns a copy of the applied key/value state.
func (n *Node) StateMachine() map[string]string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make(map[string]string, len(n.sm))
	for k, v := range n.sm {
		out[k] = v
	}
	return out
}

// CommitIndex returns the highest contiguously committed slot.
func (n *Node) CommitIndex() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIndex
}

// IsLeader reports whether the node currently believes it is leader.
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isLeader
}

// LogEntry returns the accepted entry at slot, if any.
func (n *Node) LogEntry(slot int) (Entry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.log[slot]
	return e, ok
}

// CommittedEntry returns the committed entry at slot, if any.
func (n *Node) CommittedEntry(slot int) (Entry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.committed[slot]
	return e, ok
}

// InjectAccepted forces an accepted (uncommitted) entry into the log.
// Test-only helper to construct holes and dirty logs.
func (n *Node) InjectAccepted(slot int, bal Ballot, op Op) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.log[slot] = Entry{Slot: slot, Bal: bal, Op: op}
	if bal.Compare(n.promised) > 0 {
		n.promised = bal
	}
}
