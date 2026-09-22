package readindexsim

import (
	"container/heap"
	"fmt"
	"sort"
)

const (
	evElection = iota
	evHeartbeat
	evDeliver
	evClientRead
)

type event struct {
	tick int64
	seq  int64
	kind int
	node int
	gen  int
	read ReadRequest
	msg  Message
}

type eventHeap []*event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(*event)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

type Sim struct {
	tick        int64
	nextSeq     int64
	nextReadID  int
	nodes       []*Node
	events      eventHeap
	connected   []bool
	results     []ReadResult
	audit       []AuditEvent
	verbose     bool
	networkTick int64
	leaderTerm  []int
}

func NewCluster(verbose bool) *Sim {
	s := &Sim{verbose: verbose, networkTick: 1}
	for id := 0; id < 3; id++ {
		n := &Node{
			id:              id,
			role:            Follower,
			votedFor:        -1,
			leaderID:        -1,
			state:           map[string]string{},
			electionTimeout: 8 + id*2,
			nextIndex:       map[int]int{},
			matchIndex:      map[int]int{},
			votes:           map[int]bool{},
			reads:           map[int]*ReadState{},
		}
		n.log = append(n.log, Entry{Index: 0, Term: 0})
		s.nodes = append(s.nodes, n)
		s.connected = append(s.connected, true)
		s.leaderTerm = append(s.leaderTerm, 0)
		s.scheduleElection(id, int64(n.electionTimeout))
	}
	return s
}

func (s *Sim) Tick() int64 { return s.tick }

func (s *Sim) RunFor(ticks int) {
	deadline := s.tick + int64(ticks)
	for s.events.Len() > 0 && s.events[0].tick <= deadline {
		s.advance()
	}
	s.tick = deadline
}

func (s *Sim) RunUntil(deadline int64) {
	for s.events.Len() > 0 && s.events[0].tick <= deadline {
		s.advance()
	}
}

func (s *Sim) advance() {
	ev := heap.Pop(&s.events).(*event)
	s.tick = ev.tick
	switch ev.kind {
	case evElection:
		if s.nodes[ev.node].electionGen == ev.gen && s.nodes[ev.node].role != Leader {
			s.startElection(ev.node)
		}
	case evHeartbeat:
		if s.nodes[ev.node].role == Leader {
			s.broadcastAppend(ev.node, s.oldestReadID(ev.node))
			s.scheduleHeartbeat(ev.node)
		}
	case evDeliver:
		s.handleMessage(ev.msg)
	case evClientRead:
		s.requestRead(ev.node, ev.read)
	}
}

func (s *Sim) newEvent(tick int64, kind int, node int, msg Message, read ReadRequest, raw Message) *event {
	s.nextSeq++
	var rawMsg Message
	if kind == evDeliver {
		rawMsg = raw
	} else {
		rawMsg = msg
	}
	return &event{tick: tick, seq: s.nextSeq, kind: kind, node: node, read: read, msg: rawMsg}
}

func (s *Sim) scheduleElection(id int, after int64) {
	n := s.nodes[id]
	n.electionGen++
	tick := s.tick + after
	s.nextSeq++
	heap.Push(&s.events, &event{
		tick: tick,
		seq:  s.nextSeq,
		kind: evElection,
		node: id,
		gen:  n.electionGen,
	})
}

func (s *Sim) scheduleHeartbeat(id int) {
	heap.Push(&s.events, s.newEvent(s.tick+5, evHeartbeat, id, Message{}, ReadRequest{}, Message{}))
}

func (s *Sim) resetElectionTimer(id int) {
	s.scheduleElection(id, int64(s.nodes[id].electionTimeout))
}

func (s *Sim) send(msg Message) {
	if !s.connected[msg.From] || !s.connected[msg.To] {
		return
	}
	s.logf(msg.To, "deliver %s t%d from n%d", messageName(msg.Type), msg.Term, msg.From)
	s.nextSeq++
	heap.Push(&s.events, &event{
		tick: s.tick + s.networkTick,
		seq:  s.nextSeq,
		kind: evDeliver,
		node: msg.To,
		msg:  msg,
	})
}

func (s *Sim) Isolate(id int) {
	s.connected[id] = false
	s.logf(id, "isolated from network")
}

func (s *Sim) Reconnect(id int) {
	s.connected[id] = true
	s.logf(id, "reconnected")
}

func (s *Sim) PauseApply(id int) {
	s.nodes[id].applyPaused = true
	s.logf(id, "state machine apply paused")
}

func (s *Sim) ResumeApply(id int) {
	n := s.nodes[id]
	n.applyPaused = false
	s.logf(id, "state machine apply resumed")
	s.applyCommitted(n)
}

func (s *Sim) Propose(leader int, key, value string) bool {
	n := s.nodes[leader]
	if !s.IsConfirmedLeader(leader) {
		return false
	}
	n.log = append(n.log, Entry{
		Index: len(n.log),
		Term:  n.term,
		Op: Op{
			Kind:  "set",
			Key:   key,
			Value: value,
		},
	})
	n.matchIndex[leader] = lastLogIndex(n)
	n.nextIndex[leader] = lastLogIndex(n) + 1
	s.broadcastAppend(leader, 0)
	return true
}

func (s *Sim) Read(leader int, key string) int {
	s.nextReadID++
	id := s.nextReadID
	heap.Push(&s.events, s.newEvent(s.tick+1, evClientRead, leader, Message{}, ReadRequest{ID: id, Key: key}, Message{}))
	return id
}

func (s *Sim) Results() []ReadResult {
	out := make([]ReadResult, len(s.results))
	copy(out, s.results)
	return out
}

func (s *Sim) Result(id int) (ReadResult, bool) {
	for _, result := range s.results {
		if result.ReadID == id {
			return result, true
		}
	}
	return ReadResult{}, false
}

func (s *Sim) Role(id int) Role { return s.nodes[id].role }
func (s *Sim) Term(id int) int  { return s.nodes[id].term }
func (s *Sim) IsConfirmedLeader(id int) bool {
	n := s.nodes[id]
	return n.role == Leader && s.leaderTerm[id] == n.term
}

func (s *Sim) LeaderHint() int {
	for _, n := range s.nodes {
		if s.IsConfirmedLeader(n.id) && s.connected[n.id] {
			return n.id
		}
	}
	return -1
}

func (s *Sim) CommitIndex(id int) int  { return s.nodes[id].commitIndex }
func (s *Sim) AppliedIndex(id int) int { return s.nodes[id].appliedIndex }

func (s *Sim) WaitCommit(leader, index int, maxTicks int) bool {
	for tick := 0; tick < maxTicks; tick++ {
		if s.nodes[leader].commitIndex >= index {
			return true
		}
		s.RunFor(1)
	}
	return false
}

func (s *Sim) Audit() []AuditEvent {
	out := make([]AuditEvent, len(s.audit))
	copy(out, s.audit)
	return out
}

func (s *Sim) logf(node int, format string, args ...any) {
	if s.verbose {
		s.audit = append(s.audit, AuditEvent{
			Tick:    s.tick,
			Node:    node,
			Message: fmt.Sprintf(format, args...),
		})
	}
}

func lastLogIndex(n *Node) int { return n.log[len(n.log)-1].Index }
func lastLogTerm(n *Node) int  { return n.log[len(n.log)-1].Term }

func entryAt(n *Node, index int) Entry {
	if index < 0 || index >= len(n.log) {
		return Entry{Index: index, Term: -1}
	}
	return n.log[index]
}

func ackQuorum(votes map[int]bool) bool {
	return len(votes) >= 2
}

func sortedAcks(votes map[int]bool) []int {
	acks := make([]int, 0, len(votes))
	for id := range votes {
		acks = append(acks, id)
	}
	sort.Ints(acks)
	return acks
}

func messageName(t MessageType) string {
	switch t {
	case RequestVote:
		return "RequestVote"
	case VoteResult:
		return "RequestVoteResult"
	case AppendEntries:
		return "AppendEntries"
	case AppendResult:
		return "AppendEntriesResult"
	default:
		return "unknown"
	}
}
