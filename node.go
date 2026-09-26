package syncsim

// MsgType enumerates the two protocol messages.
type MsgType int

const (
	// MsgDigests carries the child digests of the tree node at
	// (Prefix, Plen). The receiver compares them against its own and
	// answers only for mismatched subtrees.
	MsgDigests MsgType = iota
	// MsgRecords carries full records (the "differing leaves") under
	// (Prefix, Plen). Unless Reply is set, the receiver answers with its
	// own records under the same prefix, so both sides converge.
	MsgRecords
)

// Msg is one protocol message on the simulated network.
type Msg struct {
	From, To string
	Type     MsgType
	Prefix   uint32
	Plen     int
	Children map[int]Digest // MsgDigests: child nibble -> digest
	Records  []Record       // MsgRecords: differing leaves
	Reply    bool           // MsgRecords: already a reply; do not echo
	Audit    bool           // triggered by the collision review path
}

// Stats counts what a node put on the wire; used as evidence that
// identical subtrees are not transmitted in full.
type Stats struct {
	DigestsSent int
	RecordsSent int
	BytesSent   int
	Applied     int // remote records that changed local state
}

// Node is one replica. All communication, faults and time are driven by
// the owning Sim.
type Node struct {
	ID    string
	Store *Store
	sim   *Sim
	// AuditRate in [0,1]: probability of reviewing a matched digest by
	// exchanging the full leaves underneath it. This is the review path
	// for forced summary collisions. 0 disables it.
	AuditRate float64
	Stats     Stats
}

func newNode(sim *Sim, id string) *Node {
	return &Node{ID: id, Store: NewStore(id), sim: sim}
}

func (n *Node) summary() *Summary {
	return BuildSummary(n.sim.DH, n.Store.Records())
}

// StartSync initiates one anti-entropy round towards peer by sending the
// digests of the root's children.
func (n *Node) StartSync(peer string) {
	sum := n.summary()
	n.send(Msg{To: peer, Type: MsgDigests, Children: sum.Children(0, 0)})
}

func (n *Node) send(m Msg) {
	m.From = n.ID
	n.Stats.DigestsSent += len(m.Children)
	n.Stats.RecordsSent += len(m.Records)
	n.Stats.BytesSent += 16 + 8*len(m.Children)
	for _, r := range m.Records {
		n.Stats.BytesSent += 24 + len(r.Key) + len(r.Value) + len(r.Ver.Node)
	}
	n.sim.Send(m)
}

func (n *Node) handle(m Msg) {
	switch m.Type {
	case MsgDigests:
		n.onDigests(m)
	case MsgRecords:
		n.onRecords(m)
	}
}

// onDigests compares the received child digests of (Prefix, Plen) with
// the local ones, nibble by nibble:
//   - equal digests: skip the subtree (identical subtrees are never
//     transmitted), except when the audit review path fires;
//   - mismatch at leaf level: send our records under that prefix;
//   - mismatch below leaf level: recurse with our child digests.
func (n *Node) onDigests(m Msg) {
	sum := n.summary()
	mine := sum.Children(m.Prefix, m.Plen)
	for nib := 0; nib < Arity; nib++ {
		theirs, okT := m.Children[nib]
		local, okL := mine[nib]
		if !okT {
			theirs = 0
		}
		if !okL {
			local = 0
		}
		childPrefix := extend(m.Prefix, nib, m.Plen)
		if theirs == local {
			if local != 0 && n.sim.rng.Float64() < n.AuditRate {
				// Review path: digests matched, but we re-check the
				// actual leaves to detect a forced summary collision.
				n.send(Msg{To: m.From, Type: MsgRecords, Prefix: childPrefix,
					Plen: m.Plen + 1, Records: sum.Leaves(childPrefix, m.Plen+1), Audit: true})
			}
			continue
		}
		if m.Plen+1 >= Depth {
			n.send(Msg{To: m.From, Type: MsgRecords, Prefix: childPrefix,
				Plen: m.Plen + 1, Records: sum.Leaves(childPrefix, m.Plen+1)})
		} else {
			n.send(Msg{To: m.From, Type: MsgDigests, Prefix: childPrefix,
				Plen: m.Plen + 1, Children: sum.Children(childPrefix, m.Plen+1)})
		}
	}
}

// onRecords applies remote leaves under the explicit conflict rule and,
// unless this was already a reply, answers with the local leaves under
// the same prefix so the other side converges too.
func (n *Node) onRecords(m Msg) {
	for _, r := range m.Records {
		if n.Store.Apply(r) {
			n.Stats.Applied++
		}
	}
	if !m.Reply {
		sum := n.summary()
		n.send(Msg{To: m.From, Type: MsgRecords, Prefix: m.Prefix, Plen: m.Plen,
			Records: sum.Leaves(m.Prefix, m.Plen), Reply: true, Audit: m.Audit})
	}
}
