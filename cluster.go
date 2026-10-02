package swimtrust

import (
	"fmt"
	"sort"
	"strings"
)

// Cluster is a deterministic, in-memory SWIM-style simulation. One
// ProbeRound = one protocol period for one observer.
type Cluster struct {
	nodes  map[string]*Node
	Round  int
	Events []string
}

// NewCluster creates an empty cluster.
func NewCluster() *Cluster {
	return &Cluster{nodes: make(map[string]*Node)}
}

// AddNode registers a node. directProbeable=false simulates a node that is
// up but never answers direct Pings (forcing the PingReq path).
func (c *Cluster) AddNode(id string, behavior Behavior, directProbeable bool) *Node {
	n := &Node{
		ID:              id,
		behavior:        behavior,
		up:              true,
		directProbeable: directProbeable,
		cluster:         c,
	}
	n.Scorer = NewTrustScorer(id)
	n.Scorer.OnQuarantine = func(peerID string, score int) {
		c.log("node=%s QUARANTINE peer=%s score=%d -> cut direct probes, announce isolation", id, peerID, score)
	}
	c.nodes[id] = n
	return n
}

// Crash kills a node (ground truth).
func (c *Cluster) Crash(id string) {
	if n, ok := c.nodes[id]; ok {
		n.crash()
		c.log("node=%s crashed (ground truth)", id)
	}
}

// Node returns a node by id.
func (c *Cluster) Node(id string) *Node { return c.nodes[id] }

func (c *Cluster) log(format string, args ...interface{}) {
	c.Events = append(c.Events, fmt.Sprintf("[round %02d] %s", c.Round, fmt.Sprintf(format, args...)))
}

// ProbeOutcome summarizes one probe round.
type ProbeOutcome struct {
	Observer     string
	Target       string
	DirectProbed bool
	Reached      bool
	ViaIndirect  bool
}

// Probe runs one full SWIM probe: observer -> direct Ping -> on failure,
// PingReq via all non-quarantined helpers. Violations by helpers are
// confirmed against the quorum of honest responses and scored locally.
func (c *Cluster) Probe(observerID, targetID string) ProbeOutcome {
	obs := c.nodes[observerID]
	target := c.nodes[targetID]
	out := ProbeOutcome{Observer: observerID, Target: targetID}

	if obs.Scorer.IsQuarantined(targetID) {
		c.log("node=%s skips direct probe of quarantined peer=%s", observerID, targetID)
		return out
	}
	out.DirectProbed = true

	// 1. Direct path.
	if target.handlePing() {
		out.Reached = true
		c.log("node=%s ping %s -> ack (direct)", observerID, targetID)
		return out
	}
	c.log("node=%s ping %s -> no ack, escalating to PingReq", observerID, targetID)

	// 2. Indirect path: ask every other non-quarantined peer.
	type reply struct {
		helperID string
		answered bool
		claim    string
	}
	var replies []reply
	for _, id := range c.sortedIDs() {
		if id == observerID || id == targetID {
			continue
		}
		if obs.Scorer.IsQuarantined(id) {
			c.log("node=%s skips PingReq via quarantined helper=%s", observerID, id)
			continue
		}
		helper := c.nodes[id]
		answered, claim := helper.handlePingReq(target)
		replies = append(replies, reply{id, answered, claim})
		if !answered {
			rec := obs.Scorer.Record(id, ViolationRefusePingReq, c.Round,
				fmt.Sprintf("helper=%s swallowed PingReq(target=%s)", id, targetID))
			c.log("node=%s VIOLATION %s helper=%s score=%d", observerID, ViolationRefusePingReq, id, rec.Score)
		}
	}

	// 3. Quorum check: a helper claiming "alive" while the answering
	//    majority says "dead" is forging Alive status.
	aliveVotes, deadVotes := 0, 0
	for _, r := range replies {
		if !r.answered {
			continue
		}
		if r.claim == "alive" {
			aliveVotes++
		} else {
			deadVotes++
		}
	}
	for _, r := range replies {
		if r.answered && r.claim == "alive" && deadVotes > aliveVotes {
			rec := obs.Scorer.Record(r.helperID, ViolationForgeAlive, c.Round,
				fmt.Sprintf("helper=%s claimed %s alive against %d dead votes", r.helperID, targetID, deadVotes))
			c.log("node=%s VIOLATION %s helper=%s score=%d", observerID, ViolationForgeAlive, r.helperID, rec.Score)
		}
	}

	if aliveVotes > deadVotes {
		out.Reached = true
		out.ViaIndirect = true
		c.log("node=%s learned %s is alive via indirect probe (quorum %d alive vs %d dead)", observerID, targetID, aliveVotes, deadVotes)
	} else {
		c.log("node=%s marks %s unreachable (quorum %d dead vs %d alive)", observerID, targetID, deadVotes, aliveVotes)
	}
	return out
}

// Tick advances the protocol round.
func (c *Cluster) Tick() { c.Round++ }

// QuarantineAnnouncements returns the isolation declarations of every node
// that has quarantined peerID.
func (c *Cluster) QuarantineAnnouncements(peerID string) []string {
	var out []string
	for _, id := range c.sortedIDs() {
		if id != peerID && c.nodes[id].Scorer.IsQuarantined(peerID) {
			out = append(out, fmt.Sprintf("%s declares %s ISOLATED", id, peerID))
		}
	}
	return out
}

func (c *Cluster) sortedIDs() []string {
	ids := make([]string, 0, len(c.nodes))
	for id := range c.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// EventLog renders the collected events.
func (c *Cluster) EventLog() string { return strings.Join(c.Events, "\n") }
