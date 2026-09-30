package dvsim

import "sort"

// Route is one entry of a routing table.
type Route struct {
	Metric  int // Infinity means unreachable
	NextHop int // neighbor id used to reach Dest
	Version int // sequence number originated by the destination
}

// Entry is a single (dest, metric, version) triple inside an update.
type Entry struct {
	Dest    int
	Metric  int
	Version int
}

// Message is a distance-vector update sent over one link.
type Message struct {
	From    int
	Seq     int // per-sender message sequence number
	Entries []Entry
}

// Node is a simulated router.
type Node struct {
	ID      int
	sim     *Sim
	table   map[int]Route
	links   map[int]*Link // neighbor id -> link
	seq     int           // own version, bumped on local link changes
	msgSeq  int           // per-sender message sequence
	lastSeq map[int]int   // last accepted message seq per sender
	pending bool          // a triggered update is already scheduled
}

// Routes returns a copy of the routing table.
func (n *Node) Routes() map[int]Route {
	out := make(map[int]Route, len(n.table))
	for d, r := range n.table {
		out[d] = r
	}
	return out
}

// Metric returns the current metric to dest (Infinity if unknown).
func (n *Node) Metric(dest int) int {
	if dest == n.ID {
		return 0
	}
	if r, ok := n.table[dest]; ok {
		return r.Metric
	}
	return Infinity
}

// onLinkChange is called by the simulator when the link to peer toggles.
func (n *Node) onLinkChange(peer int) {
	n.seq++ // local topology changed: bump own version
	if !n.links[peer].Up {
		// Everything learned via the failed link is now unreachable.
		// The version is kept: this is bad news about the same
		// destination version, not newer information.
		for dest, r := range n.table {
			if dest != n.ID && r.NextHop == peer && r.Metric != Infinity {
				r.Metric = Infinity
				n.table[dest] = r
			}
		}
	}
	n.trigger()
}

// trigger schedules one update to all up neighbors, coalescing bursts.
func (n *Node) trigger() {
	if n.pending {
		return
	}
	n.pending = true
	n.sim.Schedule(0, func() {
		n.pending = false
		for _, peer := range n.sortedPeers() {
			if n.links[peer].Up {
				n.sendUpdate(peer)
			}
		}
	})
}

func (n *Node) sortedPeers() []int {
	peers := make([]int, 0, len(n.links))
	for p := range n.links {
		peers = append(peers, p)
	}
	sort.Ints(peers)
	return peers
}

// advertisement builds the entries sent to one neighbor.
// Split horizon with poison reverse: routes whose next hop is this
// neighbor are advertised back with metric Infinity.
func (n *Node) advertisement(peer int) []Entry {
	entries := make([]Entry, 0, len(n.table)+1)
	entries = append(entries, Entry{Dest: n.ID, Metric: 0, Version: n.seq})
	dests := make([]int, 0, len(n.table))
	for d := range n.table {
		if d != n.ID {
			dests = append(dests, d)
		}
	}
	sort.Ints(dests)
	for _, d := range dests {
		r := n.table[d]
		m := r.Metric
		if r.NextHop == peer {
			m = Infinity // poison reverse
		}
		entries = append(entries, Entry{Dest: d, Metric: m, Version: r.Version})
	}
	return entries
}

// sendUpdate queues one update message for delivery to peer. Delivery
// latency is random in [1, MaxLatency] ticks, so messages from the same
// sender may arrive out of order.
func (n *Node) sendUpdate(peer int) {
	msg := Message{From: n.ID, Seq: n.msgSeq, Entries: n.advertisement(peer)}
	n.msgSeq++
	latency := 1 + n.sim.rng.Intn(n.sim.MaxLatency)
	n.sim.Schedule(latency, func() { n.sim.nodes[peer].receive(msg) })
}

// receive processes an incoming update message.
func (n *Node) receive(msg Message) {
	link, ok := n.links[msg.From]
	if !ok || !link.Up {
		return // link is down: the message is lost
	}
	if msg.Seq <= n.lastSeq[msg.From] {
		return // stale or duplicated message (reordering guard)
	}
	n.lastSeq[msg.From] = msg.Seq
	for _, e := range msg.Entries {
		n.processEntry(msg.From, link.Cost, e)
	}
}

// processEntry applies the version rules to one advertised entry:
//   - newer destination version always wins;
//   - same version: accept from the current next hop (good or bad news),
//     or from anyone if the metric improves;
//   - older version: stale, ignored.
func (n *Node) processEntry(from, linkCost int, e Entry) {
	if e.Dest == n.ID {
		return
	}
	metric := e.Metric + linkCost
	if metric > Infinity {
		metric = Infinity
	}
	cur, known := n.table[e.Dest]
	if !known {
		if metric < Infinity {
			n.table[e.Dest] = Route{Metric: metric, NextHop: from, Version: e.Version}
			n.trigger()
		}
		return
	}
	install := func() {
		if cur.Metric == metric && cur.NextHop == from && cur.Version == e.Version {
			return
		}
		n.table[e.Dest] = Route{Metric: metric, NextHop: from, Version: e.Version}
		n.trigger()
	}
	switch {
	case e.Version > cur.Version:
		install()
	case e.Version == cur.Version:
		if from == cur.NextHop || metric < cur.Metric {
			install()
		}
	}
}
