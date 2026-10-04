package rbcast

// Simulator wires sources, subscriber nodes, membership and an unreliable
// network together, and drives ack collection plus history compaction.
type Simulator struct {
	Net          *Network
	Members      *Membership
	Nodes        map[string]*Node
	Sources      map[string]*Source
	online       map[string]bool
	maxGaps      int
	RejectedAcks int
}

func NewSimulator(net *Network, maxGaps int) *Simulator {
	return &Simulator{
		Net:     net,
		Members: NewMembership(),
		Nodes:   make(map[string]*Node),
		Sources: make(map[string]*Source),
		online:  make(map[string]bool),
		maxGaps: maxGaps,
	}
}

// AddSource registers a broadcasting source.
func (s *Simulator) AddSource(id string) *Source {
	src := NewSource(id)
	s.Sources[id] = src
	return src
}

// Join subscribes a node (creating it on first join) and brings it online.
// Rejoining after a leave bumps the member's generation.
func (s *Simulator) Join(id string) {
	if _, ok := s.Nodes[id]; !ok {
		s.Nodes[id] = NewNode(id, s.maxGaps)
	}
	s.Members.Join(id)
	s.online[id] = true
}

// Leave unsubscribes a node: it stops blocking compaction, its ack state is
// discarded, and its current generation becomes stale.
func (s *Simulator) Leave(id string) {
	s.Members.Leave(id)
	s.online[id] = false
	for _, src := range s.Sources {
		src.ForgetMember(id)
	}
}

// GoOffline simulates a crash/disconnect without leaving the group: the
// member stays valid (and keeps pinning compaction) but receives nothing.
func (s *Simulator) GoOffline(id string) { s.online[id] = false }

// GoOnline reconnects a member and triggers catch-up retransmission from
// every source, starting at the member's contiguous watermark. Messages the
// node already holds in its sparse gap table are recognised as duplicates.
func (s *Simulator) GoOnline(id string) {
	s.online[id] = true
	s.CatchUp(id)
}

// CatchUp retransmits everything a node is missing from all sources.
func (s *Simulator) CatchUp(id string) {
	node := s.Nodes[id]
	for _, src := range s.Sources {
		for _, m := range src.RetransmitFrom(node.Watermark(src.ID)) {
			s.Net.Send(id, m)
		}
	}
	s.flush()
}

// Broadcast sends one message from a source to every online valid member.
func (s *Simulator) Broadcast(sourceID, body string) Message {
	src := s.Sources[sourceID]
	m := src.Broadcast(body)
	for _, id := range s.Members.ValidMembers() {
		if s.online[id] {
			s.Net.Send(id, m)
		}
	}
	s.flush()
	return m
}

func (s *Simulator) flush() {
	onlineNodes := make(map[string]*Node)
	for id, node := range s.Nodes {
		if s.online[id] {
			onlineNodes[id] = node
		}
	}
	s.Net.Flush(onlineNodes)
}

// CollectAcks gathers an ack from every online valid member for every
// source, rejecting stale-generation acks, and returns the accepted acks.
func (s *Simulator) CollectAcks() []Ack {
	var accepted []Ack
	for _, id := range s.Members.ValidMembers() {
		if !s.online[id] {
			continue
		}
		node := s.Nodes[id]
		for _, src := range s.Sources {
			a := Ack{
				MemberID:   id,
				Generation: s.Members.Generation(id),
				Source:     src.ID,
				Watermark:  node.Watermark(src.ID),
			}
			if !s.Members.AcceptAck(a) {
				s.RejectedAcks++
				continue
			}
			src.RecordAck(id, a.Watermark)
			accepted = append(accepted, a)
		}
	}
	return accepted
}

// SubmitAck injects an externally produced ack (used to exercise stale
// generation rejection). It returns true if the ack was accepted.
func (s *Simulator) SubmitAck(a Ack) bool {
	if !s.Members.AcceptAck(a) {
		s.RejectedAcks++
		return false
	}
	s.Sources[a.Source].RecordAck(a.MemberID, a.Watermark)
	return true
}

// CompactAll compacts every source's retransmission log, gated on the acks
// of all currently valid subscribers.
func (s *Simulator) CompactAll() {
	valid := s.Members.ValidMembers()
	for _, src := range s.Sources {
		src.Compact(valid)
	}
}

// Converge runs catch-up rounds until every online valid member's contiguous
// watermark reaches every source's latest seq, or maxRounds is exhausted.
// Each round retransmits from the member's watermark; entries the member
// already holds in its sparse gap table are recognised as duplicates.
func (s *Simulator) Converge(maxRounds int) {
	for round := 0; round < maxRounds; round++ {
		lagging := false
		for _, id := range s.Members.ValidMembers() {
			if !s.online[id] {
				continue
			}
			for _, src := range s.Sources {
				if s.Nodes[id].Watermark(src.ID) < src.LatestSeq() {
					lagging = true
				}
			}
		}
		if !lagging {
			return
		}
		for _, id := range s.Members.ValidMembers() {
			if s.online[id] {
				s.CatchUp(id)
			}
		}
	}
}
