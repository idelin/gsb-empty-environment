package merge

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"
)

// Node is a single cluster member. All mutable state is guarded by mu and,
// crucially, no network send ever happens while the lock is held: handlers
// return the messages to emit and the caller sends them after unlocking.
// This, plus the deterministic merge-driver election (the lexicographically
// smaller cluster ID always drives), is what makes the merge deadlock-free.
type Node struct {
	mu        sync.Mutex
	id        int
	clusterID string
	epoch     uint64
	lamport   uint64
	members   map[int]uint64 // member ID -> highest Lamport heartbeat seen

	rnd *rand.Rand

	net     *Network
	inbox   chan Message
	stopCh  chan struct{}
	stopped chan struct{}
	running bool
}

// NewNode creates a node and registers it on the network. The node starts as
// a singleton cluster named "cluster-<id>"; use FormCluster to close a ring.
func NewNode(id int, net *Network) *Node {
	n := &Node{
		id:        id,
		clusterID: fmt.Sprintf("cluster-%d", id),
		epoch:     1,
		members:   map[int]uint64{id: 0},
		rnd:       rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)*7919)),
		net:       net,
		inbox:     make(chan Message, 4096),
		stopCh:    make(chan struct{}),
		stopped:   make(chan struct{}),
	}
	net.Add(n)
	return n
}

// FormCluster closes the given nodes into one logical cluster (a ring: every
// node seeds its successor as a known member, gossip spreads the rest). The
// cluster is named after its lowest node ID, which gives every partition a
// deterministic, distinct identity.
func FormCluster(nodes ...*Node) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].id < nodes[j].id })
	cid := fmt.Sprintf("cluster-%d", nodes[0].id)
	for i, n := range nodes {
		next := nodes[(i+1)%len(nodes)]
		n.mu.Lock()
		n.clusterID = cid
		if _, ok := n.members[next.id]; !ok && next.id != n.id {
			n.members[next.id] = 0
		}
		n.mu.Unlock()
	}
}

func (n *Node) ID() int { return n.id }

func (n *Node) ClusterID() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.clusterID
}

func (n *Node) Epoch() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.epoch
}

func (n *Node) Lamport() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lamport
}

// Members returns a copy of the membership table.
func (n *Node) Members() map[int]uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make(map[int]uint64, len(n.members))
	for id, hb := range n.members {
		out[id] = hb
	}
	return out
}

// MemberIDs returns the sorted IDs of known members.
func (n *Node) MemberIDs() []int {
	n.mu.Lock()
	defer n.mu.Unlock()
	ids := make([]int, 0, len(n.members))
	for id := range n.members {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// makeMsgLocked snapshots the current state into an outgoing message.
func (n *Node) makeMsgLocked(to int, typ MsgType) Message {
	mem := make(map[int]uint64, len(n.members))
	for id, hb := range n.members {
		mem[id] = hb
	}
	return Message{
		Type:      typ,
		From:      n.id,
		To:        to,
		ClusterID: n.clusterID,
		Epoch:     n.epoch,
		Lamport:   n.lamport,
		Members:   mem,
	}
}

// Gossip sends a digest Ping to up to fanout randomly chosen known members.
func (n *Node) Gossip(fanout int) {
	n.mu.Lock()
	n.lamport++ // local event
	ids := make([]int, 0, len(n.members))
	for id := range n.members {
		if id != n.id {
			ids = append(ids, id)
		}
	}
	n.rnd.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	if len(ids) > fanout {
		ids = ids[:fanout]
	}
	msgs := make([]Message, 0, len(ids))
	for _, id := range ids {
		msgs = append(msgs, n.makeMsgLocked(id, MsgPing))
	}
	n.mu.Unlock()
	n.net.SendAll(msgs)
}

// GossipAll sends a digest Ping to every known member (deterministic flood,
// used by tests and by the demo's convergence rounds).
func (n *Node) GossipAll() {
	n.mu.Lock()
	n.lamport++
	msgs := make([]Message, 0, len(n.members))
	for id := range n.members {
		if id != n.id {
			msgs = append(msgs, n.makeMsgLocked(id, MsgPing))
		}
	}
	n.mu.Unlock()
	n.net.SendAll(msgs)
}

// PingReq sends an indirect probe to target. It returns false when the
// network drops the message (e.g. the target is across the partition).
// The first PingReq that succeeds across a healed partition is what triggers
// the merge handshake.
func (n *Node) PingReq(target int) bool {
	n.mu.Lock()
	n.lamport++
	m := n.makeMsgLocked(target, MsgPingReq)
	n.mu.Unlock()
	return n.net.Send(m)
}

// handle processes one inbound message and returns the messages to send in
// response. The lock is released before anything is sent.
func (n *Node) handle(m Message) []Message {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Lamport clock correction: L = max(L, L_msg) + 1.
	if m.Lamport > n.lamport {
		n.lamport = m.Lamport
	}
	n.lamport++

	// Merge the sender's membership digest into our table.
	if m.From != n.id {
		if hb, ok := n.members[m.From]; !ok || hb < m.Lamport {
			n.members[m.From] = m.Lamport
		}
	}
	for id, hb := range m.Members {
		if cur, ok := n.members[id]; !ok || cur < hb {
			n.members[id] = hb
		}
	}

	// A foreign cluster is only a merge candidate if its epoch is not
	// behind ours. Stragglers from an older epoch are about to be
	// converted by the in-flight MergeSync flood, so they must not
	// trigger a redundant merge (which would bump the epoch again).
	foreign := m.ClusterID != "" && m.ClusterID != n.clusterID && m.Epoch >= n.epoch

	switch m.Type {
	case MsgPing, MsgPingReq:
		out := []Message{n.makeMsgLocked(m.From, MsgAck)}
		if foreign && n.clusterID < m.ClusterID {
			out = append(out, n.driveMergeLocked(m.Epoch)...)
		}
		return out

	case MsgAck:
		if foreign && n.clusterID < m.ClusterID {
			return n.driveMergeLocked(m.Epoch)
		}
		return nil

	case MsgMergeSync:
		if m.Epoch > n.epoch {
			// Adopt the surviving cluster identity and the union topology.
			n.epoch = m.Epoch
			n.clusterID = m.ClusterID
			out := []Message{n.makeMsgLocked(m.From, MsgMergeDone)}
			// Epidemic forwarding so every straggler converges even if the
			// driver's member view was incomplete.
			for id := range n.members {
				if id != n.id && id != m.From {
					out = append(out, n.makeMsgLocked(id, MsgMergeSync))
				}
			}
			return out
		}
		if m.Epoch == n.epoch && m.ClusterID == n.clusterID {
			// Already merged; ack idempotently, do not forward again.
			return []Message{n.makeMsgLocked(m.From, MsgMergeDone)}
		}
		return nil

	case MsgMergeDone:
		return nil
	}
	return nil
}

// driveMergeLocked executes the merge as the driver. Only ever called by the
// side with the lexicographically smaller cluster ID, so exactly one merge is
// ever driven for a given pair of clusters. The membership tables have
// already been unioned by handle(), so the MergeSync flood reaches both
// former partitions directly.
func (n *Node) driveMergeLocked(foreignEpoch uint64) []Message {
	if foreignEpoch > n.epoch {
		n.epoch = foreignEpoch
	}
	n.epoch++ // merged cluster epoch = max(epochA, epochB) + 1
	out := make([]Message, 0, len(n.members))
	for id := range n.members {
		if id != n.id {
			out = append(out, n.makeMsgLocked(id, MsgMergeSync))
		}
	}
	return out
}

// drain processes every queued message; used by PumpAll for synchronous,
// deterministic tests. Returns true if any message was handled.
func (n *Node) drain() bool {
	progress := false
	for {
		select {
		case m := <-n.inbox:
			n.net.SendAll(n.handle(m))
			progress = true
		default:
			return progress
		}
	}
}

// Run starts the node's event loop: inbound messages plus periodic gossip.
func (n *Node) Run(gossipInterval time.Duration, fanout int) {
	n.mu.Lock()
	n.running = true
	n.mu.Unlock()
	go func() {
		defer close(n.stopped)
		t := time.NewTicker(gossipInterval)
		defer t.Stop()
		for {
			select {
			case <-n.stopCh:
				return
			case m := <-n.inbox:
				n.net.SendAll(n.handle(m))
			case <-t.C:
				n.Gossip(fanout)
			}
		}
	}()
}

// Stop shuts the event loop down and waits for it to exit.
func (n *Node) Stop() {
	n.mu.Lock()
	running := n.running
	n.mu.Unlock()
	if !running {
		return
	}
	select {
	case <-n.stopCh:
	default:
		close(n.stopCh)
	}
	<-n.stopped
}
