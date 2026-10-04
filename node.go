package main

// NodeID identifies a node; as a message source it also namespaces the
// per-source sequence numbers.
type NodeID string

// Message is tagged with its source and a per-source increasing sequence
// number, which together form the dedup identity.
type Message struct {
	Source NodeID
	Seq    uint64
	Body   string
}

// Node is a broadcast participant. It keeps only bounded dedup history
// (one SourceWindow per source); delivered payloads live in the
// application log, which also serves catch-up to rejoining peers.
type Node struct {
	ID        NodeID
	epoch     uint64 // membership generation, bumped on every (re)join
	online    bool
	maxGaps   int
	windows   map[NodeID]*SourceWindow
	delivered []Message
	dups      int
}

func NewNode(id NodeID, maxGaps int) *Node {
	return &Node{
		ID:      id,
		online:  true,
		maxGaps: maxGaps,
		windows: make(map[NodeID]*SourceWindow),
	}
}

func (n *Node) Epoch() uint64   { return n.epoch }
func (n *Node) Online() bool    { return n.online }
func (n *Node) Duplicates() int { return n.dups }

func (n *Node) windowFor(source NodeID) *SourceWindow {
	w, ok := n.windows[source]
	if !ok {
		w = NewSourceWindow(n.maxGaps)
		n.windows[source] = w
	}
	return w
}

// Window returns the dedup window for a source, creating it if needed.
func (n *Node) Window(source NodeID) *SourceWindow { return n.windowFor(source) }

// Receive processes an incoming message. It reports whether the message
// was accepted as new; duplicates (at/below the watermark or floor, or
// already in the gap table) are counted and dropped.
func (n *Node) Receive(m Message) (accepted bool, err error) {
	w := n.windowFor(m.Source)
	if w.Seen(m.Seq) {
		n.dups++
		return false, nil
	}
	if _, err := w.Add(m.Seq); err != nil {
		return false, err
	}
	n.delivered = append(n.delivered, m)
	return true, nil
}

// AckFloor is the contiguous confirmed horizon for a source: exactly the
// dedup watermark.
func (n *Node) AckFloor(source NodeID) uint64 {
	if w, ok := n.windows[source]; ok {
		return w.Watermark()
	}
	return 0
}

// Has reports whether the node has delivered (and can therefore serve)
// the given message.
func (n *Node) Has(source NodeID, seq uint64) bool {
	w, ok := n.windows[source]
	return ok && w.Seen(seq)
}

// MessageAt returns a delivered message for retransmission to a peer.
func (n *Node) MessageAt(source NodeID, seq uint64) (Message, bool) {
	if !n.Has(source, seq) {
		return Message{}, false
	}
	for _, m := range n.delivered {
		if m.Source == source && m.Seq == seq {
			return m, true
		}
	}
	return Message{}, false
}

// Delivered returns a copy of the application-level delivery log.
func (n *Node) Delivered() []Message {
	out := make([]Message, len(n.delivered))
	copy(out, n.delivered)
	return out
}
