package raft

// Sim is a discrete-time, in-process network for a cluster of Nodes.
// There is no real network stack: messages are delivered synchronously
// when the test clock ticks, and links can be cut or healed by the test.
type Sim struct {
	Nodes map[int]*Node
	ids   []int
	down  map[[2]int]bool // directed links currently cut
	Now   int             // current tick
}

// NewSim creates a cluster with one node per id. Default timeouts are
// deterministic; override per node with SetTimeouts.
func NewSim(ids []int) *Sim {
	s := &Sim{
		Nodes: make(map[int]*Node, len(ids)),
		ids:   append([]int(nil), ids...),
		down:  make(map[[2]int]bool),
	}
	for i, id := range s.ids {
		s.Nodes[id] = NewNode(id, s.ids, 10+2*i, 3)
	}
	return s
}

// Disconnect cuts the link between a and b in both directions.
func (s *Sim) Disconnect(a, b int) {
	s.down[[2]int{a, b}] = true
	s.down[[2]int{b, a}] = true
}

// Connect restores the link between a and b in both directions.
func (s *Sim) Connect(a, b int) {
	delete(s.down, [2]int{a, b})
	delete(s.down, [2]int{b, a})
}

// Isolate cuts every link of id, simulating a network partition.
func (s *Sim) Isolate(id int) {
	for _, other := range s.ids {
		if other != id {
			s.Disconnect(id, other)
		}
	}
}

// Heal restores every link of id.
func (s *Sim) Heal(id int) {
	for _, other := range s.ids {
		if other != id {
			s.Connect(id, other)
		}
	}
}

// Tick advances the test clock by one step: every node ticks (which may
// enqueue heartbeats or start a pre-vote), then all pending messages are
// delivered, including any responses they generate.
func (s *Sim) Tick() {
	s.Now++
	for _, id := range s.ids {
		s.Nodes[id].Tick()
	}
	s.flush()
}

// Send injects a single message into the network (e.g. a heartbeat from a
// specific sender) and then delivers everything it triggers.
func (s *Sim) Send(m Message) {
	s.deliver(m)
	s.flush()
}

// Propose offers data to node id and delivers the resulting traffic.
// It reports whether id accepted the proposal (i.e. is a leader).
func (s *Sim) Propose(id int, data string) bool {
	ok := s.Nodes[id].Propose(data)
	s.flush()
	return ok
}

// flush delivers queued messages round by round until the system is
// quiescent. Messages on cut links are silently dropped.
func (s *Sim) flush() {
	for round := 0; round < 100; round++ {
		var pending []Message
		for _, id := range s.ids {
			pending = append(pending, s.Nodes[id].Drain()...)
		}
		if len(pending) == 0 {
			return
		}
		for _, m := range pending {
			s.deliver(m)
		}
	}
	panic("sim: message storm (100 rounds without quiescing)")
}

func (s *Sim) deliver(m Message) {
	if s.down[[2]int{m.From, m.To}] {
		return // link cut: message lost
	}
	s.Nodes[m.To].Step(m)
}
