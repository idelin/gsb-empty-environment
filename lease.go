// Package lease implements a single-machine simulation of a primary-backup
// lease negotiation state machine over a discrete tick clock.
//
// Design notes:
//   - Time advances in discrete ticks; every event is stamped with its tick.
//   - A leader must renew its lease via majority heartbeat acks (RoundTrip
//     ticks after each heartbeat).
//   - Clock drift between nodes is bounded by Config.MaxClockDrift; write
//     safety checks are conservative by that bound.
//   - When the network degrades (e.g. a blackhole) and the leader can no
//     longer collect majority acks, it voluntarily demotes itself to
//     candidate *before* its lease expires, so two leaders never overlap.
//   - A new leader is then elected by majority vote; the stale primary's
//     requests are fenced (rejected) by the new lease holder.
package lease

import "fmt"

// Role is the state of a node in the lease state machine.
type Role int

const (
	Follower Role = iota
	Candidate
	Leader
)

func (r Role) String() string {
	switch r {
	case Follower:
		return "follower"
	case Candidate:
		return "candidate"
	case Leader:
		return "leader"
	}
	return "unknown"
}

// Config tunes the simulation. All durations are in ticks.
type Config struct {
	RoundTrip       int // ticks for a heartbeat/vote round trip
	LeaseDuration   int // ticks a lease stays valid after a majority ack
	HeartbeatEvery  int // ticks between leader heartbeats
	StepDownTimeout int // ticks without majority ack before a leader demotes
	ElectionTimeout int // ticks without a heartbeat before a follower campaigns
	MaxClockDrift   int // bounded clock drift between nodes, in ticks
}

// Node is one participant in the cluster.
type Node struct {
	ID              int
	Role            Role
	Term            int
	LeaderID        int
	LeaseExpiry     int  // tick at which this node's lease expires (leader only)
	LeaseConfirmed  bool // lease confirmed by a majority at least once
	LastMajorityAck int  // last tick the leader refreshed a majority ack
	LastHeartbeat   int  // last tick this node heard from a leader
	LastCampaign    int  // last tick this node started an election
	VotedFor        int  // candidate voted for in Term, -1 if none

	lastSent int          // last tick a heartbeat was sent (leader only)
	promises map[int]bool // votes collected for the in-flight election
}

// Event is a single entry in the discrete-tick event log.
type Event struct {
	Tick   int
	Node   int
	Kind   string
	Detail string
}

func (e Event) String() string {
	return fmt.Sprintf("tick=%03d node=%d  %-16s %s", e.Tick, e.Node, e.Kind, e.Detail)
}

type pendingConfirm struct {
	deliverAt int
	leaderID  int
	term      int
}

type pendingElection struct {
	deliverAt   int
	candidateID int
	term        int
}

// Cluster simulates the whole system on one machine.
type Cluster struct {
	Tick   int
	Nodes  []*Node
	Config Config
	Events []Event

	isolated  map[int]bool
	confirms  []pendingConfirm
	elections []pendingElection
}

// NewCluster creates a cluster of size nodes, all followers at term 0.
func NewCluster(size int, cfg Config) *Cluster {
	c := &Cluster{Config: cfg, isolated: map[int]bool{}}
	for i := 0; i < size; i++ {
		c.Nodes = append(c.Nodes, &Node{
			ID:       i,
			Role:     Follower,
			LeaderID: -1,
			VotedFor: -1,
			promises: map[int]bool{},
		})
	}
	return c
}

// Boot places the cluster at the given tick as if a heartbeat had just been
// observed, so no elections fire before the scenario starts.
func (c *Cluster) Boot(at int) {
	c.Tick = at
	for _, n := range c.Nodes {
		n.LastHeartbeat = at
	}
}

func (c *Cluster) log(node int, kind, detail string) {
	c.Events = append(c.Events, Event{Tick: c.Tick, Node: node, Kind: kind, Detail: detail})
}

func (c *Cluster) majority() int { return len(c.Nodes)/2 + 1 }

func (c *Cluster) reachable(a, b int) bool {
	return !c.isolated[a] && !c.isolated[b]
}

// Isolate cuts node id off from the network (blackhole injection).
func (c *Cluster) Isolate(id int) {
	c.isolated[id] = true
	c.log(id, "net-blackhole", fmt.Sprintf("node %d isolated from the network", id))
}

// Heal reconnects node id.
func (c *Cluster) Heal(id int) { delete(c.isolated, id) }

// Leader returns the current lease holder, or nil.
func (c *Cluster) Leader() *Node {
	for _, n := range c.Nodes {
		if n.Role == Leader {
			return n
		}
	}
	return nil
}

// AcquireLease lets node id request the lease and start heartbeat
// confirmation rounds with the majority.
func (c *Cluster) AcquireLease(id int) {
	n := c.Nodes[id]
	n.Role = Leader
	n.Term++
	n.LeaderID = id
	n.LeaseConfirmed = false
	n.LeaseExpiry = 0
	n.LastMajorityAck = c.Tick
	c.log(id, "lease-request", fmt.Sprintf("node %d requests lease (term %d)", id, n.Term))
	c.sendHeartbeat(n)
}

// sendHeartbeat emits a heartbeat: reachable followers observe it now, the
// majority ack completes RoundTrip ticks later.
func (c *Cluster) sendHeartbeat(n *Node) {
	n.lastSent = c.Tick
	for _, m := range c.Nodes {
		if m.ID != n.ID && c.reachable(n.ID, m.ID) {
			m.LastHeartbeat = c.Tick
			m.LeaderID = n.ID
			m.VotedFor = -1
			if m.Term < n.Term {
				m.Term = n.Term // heartbeat carries the leader's term
			}
		}
	}
	c.confirms = append(c.confirms, pendingConfirm{
		deliverAt: c.Tick + c.Config.RoundTrip,
		leaderID:  n.ID,
		term:      n.Term,
	})
}

func (c *Cluster) completeHeartbeats() {
	var rest []pendingConfirm
	for _, pc := range c.confirms {
		if pc.deliverAt > c.Tick {
			rest = append(rest, pc)
			continue
		}
		n := c.Nodes[pc.leaderID]
		if n.Role != Leader || n.Term != pc.term {
			continue // stale heartbeat from a previous incarnation
		}
		acks := 1 // self
		for _, m := range c.Nodes {
			if m.ID != n.ID && c.reachable(n.ID, m.ID) {
				acks++
			}
		}
		if acks >= c.majority() {
			n.LastMajorityAck = c.Tick
			n.LeaseExpiry = c.Tick + c.Config.LeaseDuration
			if !n.LeaseConfirmed {
				n.LeaseConfirmed = true
				c.log(n.ID, "lease-confirmed", fmt.Sprintf("majority %d/%d acked; lease valid until tick %d", acks, len(c.Nodes), n.LeaseExpiry))
			} else {
				c.log(n.ID, "lease-renewed", fmt.Sprintf("majority %d/%d acked; lease extended to tick %d", acks, len(c.Nodes), n.LeaseExpiry))
			}
		} else {
			c.log(n.ID, "heartbeat-failed", fmt.Sprintf("only %d/%d reachable; no majority ack", acks, len(c.Nodes)))
		}
	}
	c.confirms = rest
}

// campaign starts an election: the candidate votes for itself and collects
// promises from reachable peers immediately; the result is announced
// RoundTrip ticks later.
func (c *Cluster) campaign(n *Node) {
	n.Role = Candidate
	n.Term++
	n.LastCampaign = c.Tick
	n.VotedFor = n.ID
	n.promises = map[int]bool{n.ID: true}
	for _, m := range c.Nodes {
		if m.ID == n.ID || !c.reachable(n.ID, m.ID) {
			continue
		}
		if m.Term <= n.Term && (m.VotedFor == -1 || m.VotedFor == n.ID || m.Term < n.Term) {
			if m.Role != Follower && m.Term < n.Term {
				m.Role = Follower // stale leader/candidate yields to higher term
			}
			m.Term = n.Term
			m.VotedFor = n.ID
			m.LastHeartbeat = c.Tick
			n.promises[m.ID] = true
		}
	}
	c.log(n.ID, "campaign", fmt.Sprintf("node %d campaigns for term %d (%d promises)", n.ID, n.Term, len(n.promises)))
	c.elections = append(c.elections, pendingElection{
		deliverAt:   c.Tick + c.Config.RoundTrip,
		candidateID: n.ID,
		term:        n.Term,
	})
}

func (c *Cluster) completeElections() {
	var rest []pendingElection
	for _, pe := range c.elections {
		if pe.deliverAt > c.Tick {
			rest = append(rest, pe)
			continue
		}
		n := c.Nodes[pe.candidateID]
		if n.Role != Candidate || n.Term != pe.term {
			continue // superseded while the election was in flight
		}
		votes := len(n.promises)
		if votes >= c.majority() {
			n.Role = Leader
			n.LeaderID = n.ID
			n.LeaseConfirmed = true
			n.LastMajorityAck = c.Tick
			n.LeaseExpiry = c.Tick + c.Config.LeaseDuration
			c.log(n.ID, "elected", fmt.Sprintf("node %d elected leader by majority %d/%d (term %d); lease until tick %d", n.ID, votes, len(c.Nodes), n.Term, n.LeaseExpiry))
			c.sendHeartbeat(n)
		} else {
			c.log(n.ID, "election-failed", fmt.Sprintf("node %d got %d/%d votes; stays candidate", n.ID, votes, len(c.Nodes)))
		}
	}
	c.elections = rest
}

// Step advances the simulation by one tick.
func (c *Cluster) Step() {
	c.Tick++
	c.completeElections()
	c.completeHeartbeats()
	for _, n := range c.Nodes {
		if n.Role == Leader {
			// Voluntary demotion: if majority acks have been missing for
			// StepDownTimeout ticks, the lease cannot be renewed, so step
			// down to candidate *before* the lease expires.
			if c.Tick-n.LastMajorityAck >= c.Config.StepDownTimeout {
				c.log(n.ID, "step-down", fmt.Sprintf("no majority ack for %d ticks; demoting to candidate before lease expiry (tick %d)", c.Config.StepDownTimeout, n.LeaseExpiry))
				n.Role = Follower
				n.LeaseConfirmed = false
				c.campaign(n)
				continue
			}
			if c.Tick-n.lastSent >= c.Config.HeartbeatEvery {
				c.sendHeartbeat(n)
			}
			continue
		}
		idle := c.Tick - n.LastHeartbeat
		if n.Role == Candidate {
			idle = c.Tick - n.LastCampaign
		}
		votedElsewhere := n.VotedFor != -1 && n.VotedFor != n.ID
		if idle >= c.Config.ElectionTimeout && !votedElsewhere {
			c.campaign(n)
		}
	}
}

// Run advances the simulation by the given number of ticks.
func (c *Cluster) Run(ticks int) {
	for i := 0; i < ticks; i++ {
		c.Step()
	}
}

// CanWrite reports whether node id may serve writes at the current tick.
// MaxClockDrift makes the check conservative: the leader only writes while
// its lease is valid even under worst-case clock drift, and only while it
// is still hearing majority acks.
func (c *Cluster) CanWrite(id int) bool {
	n := c.Nodes[id]
	return n.Role == Leader &&
		n.LeaseConfirmed &&
		c.Tick+c.Config.MaxClockDrift < n.LeaseExpiry &&
		c.Tick-n.LastMajorityAck < c.Config.StepDownTimeout
}

// SendWrite delivers a write/renew request from node from to the current
// lease holder. The holder fences off any other node, which is how a stale
// (old) primary's requests are rejected.
func (c *Cluster) SendWrite(from int, key string) bool {
	n := c.Nodes[from]
	leader := c.Leader()
	if leader == nil {
		c.log(from, "write-rejected", "no lease holder; cluster unavailable")
		return false
	}
	if from == leader.ID && c.CanWrite(from) {
		c.log(from, "write-accepted", fmt.Sprintf("applied %q (term %d)", key, n.Term))
		return true
	}
	c.log(leader.ID, "write-rejected", fmt.Sprintf("rejecting node %d (term %d): lease held by node %d (term %d)", from, n.Term, leader.ID, leader.Term))
	return false
}
