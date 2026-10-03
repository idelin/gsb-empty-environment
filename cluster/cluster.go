// Package cluster simulates a SWIM-style failure detector running over a
// set of members, some of which are Byzantine silent nodes: they answer
// direct Ping messages (so they look alive) but silently drop PingReq
// relay requests, or disseminate forged Alive states for dead members.
//
// Every honest node runs a local trust.Scorer. Confirmed violations decay
// the offender's trust score; once the score reaches the quarantine
// threshold the node cuts direct probing of the offender and announces
// its isolation to the rest of the cluster.
package cluster

import (
	"fmt"
	"sort"

	"byzguard/trust"
)

// Behavior describes how a node handles protocol duties.
type Behavior int

const (
	// Honest relays PingReqs and gossips truthful states.
	Honest Behavior = iota
	// ByzantineDropper accepts PingReq but never relays it (silent node).
	ByzantineDropper
	// ByzantineForger gossips Alive states for members that are dead.
	ByzantineForger
)

// Node is a simulated cluster member.
type Node struct {
	ID       trust.NodeID
	Behavior Behavior

	// reachable models the data plane: a node that is unreachable times
	// out on direct probes (e.g. it is dead or its network path is down).
	reachable bool

	scorer *trust.Scorer

	// isolated records peers this node has cut off (no more direct
	// probes or PingReqs are sent to them).
	isolated map[trust.NodeID]bool

	// probeAttempts counts direct probes sent per peer, for assertions.
	probeAttempts map[trust.NodeID]int
}

func newNode(id trust.NodeID, behavior Behavior, cfg trust.Config) *Node {
	return &Node{
		ID:            id,
		Behavior:      behavior,
		reachable:     true,
		scorer:        trust.NewScorer(cfg),
		isolated:      make(map[trust.NodeID]bool),
		probeAttempts: make(map[trust.NodeID]int),
	}
}

// Scorer exposes the node's local trust view.
func (n *Node) Scorer() *trust.Scorer { return n.scorer }

// IsIsolated reports whether this node has cut off the given peer.
func (n *Node) IsIsolated(peer trust.NodeID) bool { return n.isolated[peer] }

// ProbeAttempts returns how many direct probes this node sent to a peer.
func (n *Node) ProbeAttempts(peer trust.NodeID) int { return n.probeAttempts[peer] }

// handlePing answers a direct probe if the data plane is up. Even
// Byzantine silent nodes answer Ping: that is what makes them look alive.
func (n *Node) handlePing() bool { return n.reachable }

// handlePingReq decides whether to relay an indirect probe. Honest nodes
// relay; Byzantine droppers silently discard the request.
func (n *Node) handlePingReq(target *Node) (ack bool, relayed bool) {
	if n.Behavior == ByzantineDropper {
		return false, false
	}
	if !n.reachable {
		return false, true // tried to relay but we are down ourselves
	}
	return target.handlePing(), true
}

// gossipAlive returns the Alive states this node disseminates. A
// Byzantine forger claims dead members are alive.
func (n *Node) gossipAlive(view map[trust.NodeID]bool) map[trust.NodeID]bool {
	out := make(map[trust.NodeID]bool, len(view))
	for id, alive := range view {
		out[id] = alive
	}
	if n.Behavior == ByzantineForger {
		for id := range view {
			out[id] = true // claim everyone is alive, even the dead
		}
	}
	return out
}

// Cluster is the simulated membership group.
type Cluster struct {
	cfg   trust.Config
	nodes map[trust.NodeID]*Node
	order []trust.NodeID

	// Logs collects human-readable protocol traces from every node.
	Logs []string
}

// New builds a cluster. specs maps node ID to behavior; all nodes start
// reachable.
func New(cfg trust.Config, specs map[trust.NodeID]Behavior) *Cluster {
	c := &Cluster{cfg: cfg, nodes: make(map[trust.NodeID]*Node)}
	for id, b := range specs {
		c.nodes[id] = newNode(id, b, cfg)
		c.order = append(c.order, id)
	}
	sort.Slice(c.order, func(i, j int) bool { return c.order[i] < c.order[j] })
	return c
}

// Node returns a member by ID.
func (c *Cluster) Node(id trust.NodeID) *Node { return c.nodes[id] }

// SetReachable flips a member's data-plane availability.
func (c *Cluster) SetReachable(id trust.NodeID, reachable bool) {
	c.nodes[id].reachable = reachable
}

func (c *Cluster) logf(format string, args ...interface{}) {
	c.Logs = append(c.Logs, fmt.Sprintf(format, args...))
}

// DirectProbe lets observer probe target directly. Quarantined or
// locally isolated targets are skipped entirely (no probe is sent).
func (c *Cluster) DirectProbe(observer, target trust.NodeID) bool {
	o := c.nodes[observer]
	if o.isolated[target] {
		return false
	}
	o.probeAttempts[target]++
	return c.nodes[target].handlePing()
}

// IndirectProbe asks an intermediary to relay a PingReq toward target on
// behalf of observer, and feeds the outcome into the observer's local
// trust scorer.
func (c *Cluster) IndirectProbe(observer, via, target trust.NodeID) bool {
	o := c.nodes[observer]
	if o.isolated[via] {
		c.logf("[%s] skip PingReq via %s (isolated)", observer, via)
		return false
	}
	ack, relayed := c.nodes[via].handlePingReq(c.nodes[target])
	switch {
	case !relayed:
		score, quarantined := o.scorer.RecordViolation(via, trust.ViolationPingReqRefused)
		c.logf("[%s] PingReq via %s DROPPED -> trust(%s)=%.0f", observer, via, via, score)
		if quarantined {
			c.quarantine(observer, via)
		}
	case ack:
		score := o.scorer.RecordRelay(via)
		c.logf("[%s] PingReq via %s acked -> trust(%s)=%.0f", observer, via, via, score)
	default:
		// Relayed but the target did not answer: the intermediary did
		// its duty, small reward; the target itself is the suspect.
		o.scorer.RecordRelay(via)
	}
	return ack
}

// ReceiveGossip lets observer consume Alive states disseminated by from.
// Forged Alive claims for unreachable members are cross-checked with a
// direct probe; a confirmed forgery decays the gossiper's trust.
func (c *Cluster) ReceiveGossip(observer, from trust.NodeID) {
	o := c.nodes[observer]
	if o.isolated[from] {
		return
	}
	view := make(map[trust.NodeID]bool)
	for _, id := range c.order {
		if id != from {
			view[id] = c.nodes[id].reachable
		}
	}
	claims := c.nodes[from].gossipAlive(view)
	for member, claimedAlive := range claims {
		if member == observer || !claimedAlive {
			continue
		}
		// Cross-check: probe the claimed-alive member directly.
		if !c.nodes[member].reachable {
			score, quarantined := o.scorer.RecordViolation(from, trust.ViolationAliveForged)
			c.logf("[%s] forged Alive(%s) from %s detected -> trust(%s)=%.0f",
				observer, member, from, from, score)
			if quarantined {
				c.quarantine(observer, from)
			}
			return
		}
	}
}

// quarantine cuts the offender off locally and broadcasts the isolation
// announcement; every other honest node accepts the announcement, stops
// probing the offender, and declares the isolation.
func (c *Cluster) quarantine(observer, offender trust.NodeID) {
	c.logf("[%s] QUARANTINE %s: trust fell to %.0f (threshold %.0f)",
		observer, offender, c.nodes[observer].scorer.Score(offender), c.cfg.QuarantineThreshold)
	c.isolate(observer, offender)
	for _, id := range c.order {
		if id == observer || id == offender {
			continue
		}
		n := c.nodes[id]
		if n.Behavior != Honest {
			continue
		}
		c.isolate(id, offender)
	}
}

func (c *Cluster) isolate(node, offender trust.NodeID) {
	n := c.nodes[node]
	if n.isolated[offender] {
		return
	}
	n.isolated[offender] = true
	n.scorer.ForceQuarantine(offender, trust.ViolationPingReqRefused) // mirror local view
	c.logf("[%s] declares %s ISOLATED: cutting direct probes and PingReq relays", node, offender)
}
