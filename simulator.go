package majorityregister

import (
	"container/heap"
	"math/rand"
)

const defaultTimeout = 5

// Phase identifies which phase a client operation is currently executing.
type Phase int

const (
	queryPhase Phase = iota
	writePhase
)

// Status identifies the terminal state of an operation.
type Status int

const (
	Running Status = iota
	// Completed means the operation obtained a required response quorum.
	Completed
	// Aborted means the operation lost a same-version unique-label tie. It is
	// unfinished; the caller may retry with a fresh operation.
	Aborted
)

func (s Status) String() string {
	switch s {
	case Completed:
		return "completed"
	case Aborted:
		return "aborted"
	default:
		return "running"
	}
}

// MessageInfo describes one simulated network delivery decision.
type MessageInfo struct {
	From      int
	To        int
	OpID      string
	Round     uint64
	ToReplica bool
}

// Config controls time and communication. Zero fields use deterministic,
// fair defaults. A custom Drop function can model partitions or message loss;
// Delay can force out-of-order delivery.
type Config struct {
	Seed     int64
	MinDelay uint64
	MaxDelay uint64
	Timeout  uint64
	Drop     func(MessageInfo) bool
	Delay    func(MessageInfo, *rand.Rand) uint64
}

// Simulation is a single-threaded asynchronous event simulator.
type Simulation struct {
	n            int
	f            int
	quorum       int
	now          uint64
	cfg          Config
	rng          *rand.Rand
	replicas     []*Replica
	clients      map[string]*Client
	operationIDs map[string]bool
	timers       timerHeap
	messages     messageHeap
	nextTimer    uint64
	nextEvent    uint64
}

// New creates a cluster with 2f+1 replicas. It tolerates f permanent replica
// crashes or unavailable replicas; f+1 live replicas form a majority.
func New(f int, cfg Config) (*Simulation, error) {
	if f < 0 {
		return nil, ErrInvalidNode
	}
	n := 2*f + 1
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxDelay == 0 && cfg.MinDelay == 0 {
		cfg.MinDelay = 1
		cfg.MaxDelay = 2
	}
	if cfg.MaxDelay < cfg.MinDelay {
		cfg.MaxDelay = cfg.MinDelay
	}

	sim := &Simulation{
		n:            n,
		f:            f,
		quorum:       f + 1,
		cfg:          cfg,
		rng:          rand.New(rand.NewSource(cfg.Seed)),
		replicas:     make([]*Replica, n),
		clients:      make(map[string]*Client),
		operationIDs: make(map[string]bool),
	}
	for i := range sim.replicas {
		sim.replicas[i] = newReplica(i)
	}
	heap.Init(&sim.timers)
	heap.Init(&sim.messages)
	return sim, nil
}

// N returns the total number of replicas.
func (s *Simulation) N() int { return s.n }

// F returns the maximum number of crashes tolerated by the quorum size.
func (s *Simulation) F() int { return s.f }

// Quorum returns the majority quorum size.
func (s *Simulation) Quorum() int { return s.quorum }

// Now returns logical time in ticks.
func (s *Simulation) Now() uint64 { return s.now }

// Replica returns one simulated node.
func (s *Simulation) Replica(id int) (*Replica, error) {
	if id < 0 || id >= s.n {
		return nil, ErrInvalidNode
	}
	return s.replicas[id], nil
}

// Crash stops a node without erasing durable storage.
func (s *Simulation) Crash(id int) error {
	if id < 0 || id >= s.n {
		return ErrInvalidNode
	}
	s.replicas[id].crash()
	return nil
}

// Restart brings a node back up and reloads its durable state.
func (s *Simulation) Restart(id int) error {
	if id < 0 || id >= s.n {
		return ErrInvalidNode
	}
	s.replicas[id].restart()
	return nil
}

// IsDown reports whether a node is stopped.
func (s *Simulation) IsDown(id int) (bool, error) {
	if id < 0 || id >= s.n {
		return false, ErrInvalidNode
	}
	return !s.replicas[id].running, nil
}

// NewClient registers a client. Client IDs identify the sender in network
// hooks and do not affect register safety.
func (s *Simulation) NewClient(id string) *Client {
	if client, ok := s.clients[id]; ok {
		return client
	}
	client := &Client{id: id, sim: s, ops: make(map[string]*Operation)}
	s.clients[id] = client
	return client
}

// Run advances logical time until maxTicks elapse or every known operation has
// terminated. It returns true if all operations have terminated.
func (s *Simulation) Run(maxTicks uint64) bool {
	deadline := s.now + maxTicks
	for s.now <= deadline {
		if len(s.clients) > 0 && s.allOperationsDone() {
			return true
		}

		nextTime, haveTimer := s.nextTimerTime()
		nextMessage, haveMessage := s.nextMessageTime()
		if !haveTimer && !haveMessage {
			return s.allOperationsDone()
		}
		if haveTimer && (!haveMessage || nextTime <= nextMessage) {
			if nextTime > deadline {
				return false
			}
			s.now = nextTime
			s.fireTimer()
			continue
		}
		if nextMessage > deadline {
			return false
		}
		s.now = nextMessage
		s.deliverMessage()
	}
	return s.allOperationsDone()
}

func (s *Simulation) allOperationsDone() bool {
	for _, client := range s.clients {
		for _, op := range client.ops {
			if op.Status == Running {
				return false
			}
		}
	}
	return true
}

func (s *Simulation) nextTimerTime() (uint64, bool) {
	if len(s.timers) == 0 {
		return 0, false
	}
	return s.timers[0].at, true
}

func (s *Simulation) nextMessageTime() (uint64, bool) {
	if len(s.messages) == 0 {
		return 0, false
	}
	return s.messages[0].at, true
}

func (s *Simulation) fireTimer() {
	timer := heap.Pop(&s.timers).(*timer)
	if timer.op.Status != Running || timer.round != timer.op.round || timer.phase != timer.op.phase {
		return
	}
	timer.op.handleTimeout()
}

func (s *Simulation) deliverMessage() {
	envelope := heap.Pop(&s.messages).(*envelope)
	if envelope.toReplica {
		req := envelope.request
		replica := s.replicas[req.destination]
		reply, ok := replica.handle(req.Request)
		if ok {
			s.sendReply(envelope.from, req.destination, reply)
		}
		return
	}
	client := s.clients[envelope.clientID]
	if client != nil {
		client.handleReply(envelope.reply.Reply)
	}
}

func (s *Simulation) sendRequest(clientID string, destination int, req Request) {
	info := MessageInfo{From: -1, To: destination, OpID: req.OpID, Round: req.Round, ToReplica: true}
	if s.drop(info) {
		return
	}
	heap.Push(&s.messages, &envelope{
		at:        s.now + s.delay(info),
		seq:       s.nextEvent,
		toReplica: true,
		from:      clientID,
		request:   &requestEnvelope{destination: destination, Request: req},
	})
	s.nextEvent++
}

func (s *Simulation) sendReply(clientID string, from int, reply Reply) {
	info := MessageInfo{From: from, To: -1, OpID: reply.OpID, Round: reply.Round, ToReplica: false}
	if s.drop(info) {
		return
	}
	heap.Push(&s.messages, &envelope{
		at:        s.now + s.delay(info),
		seq:       s.nextEvent,
		toReplica: false,
		clientID:  clientID,
		reply:     &replyEnvelope{Reply: reply},
	})
	s.nextEvent++
}

func (s *Simulation) drop(info MessageInfo) bool {
	return s.cfg.Drop != nil && s.cfg.Drop(info)
}

func (s *Simulation) delay(info MessageInfo) uint64 {
	if s.cfg.Delay != nil {
		return s.cfg.Delay(info, s.rng)
	}
	return s.cfg.MinDelay + uint64(s.rng.Int63n(int64(s.cfg.MaxDelay-s.cfg.MinDelay+1)))
}

func (s *Simulation) scheduleTimer(op *Operation, phase Phase) {
	s.nextTimer++
	heap.Push(&s.timers, &timer{
		at:    s.now + s.cfg.Timeout,
		seq:   s.nextTimer,
		round: op.round,
		phase: phase,
		op:    op,
	})
}

type timer struct {
	at    uint64
	seq   uint64
	round uint64
	phase Phase
	op    *Operation
}

type timerHeap []*timer

func (h timerHeap) Len() int { return len(h) }
func (h timerHeap) Less(i, j int) bool {
	if h[i].at == h[j].at {
		return h[i].seq < h[j].seq
	}
	return h[i].at < h[j].at
}
func (h timerHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *timerHeap) Push(x interface{}) { *h = append(*h, x.(*timer)) }
func (h *timerHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

type requestEnvelope struct {
	destination int
	Request
}

type replyEnvelope struct {
	Reply
}

type envelope struct {
	at        uint64
	seq       uint64
	toReplica bool
	from      string
	clientID  string
	request   *requestEnvelope
	reply     *replyEnvelope
}

type messageHeap []*envelope

func (h messageHeap) Len() int { return len(h) }
func (h messageHeap) Less(i, j int) bool {
	if h[i].at == h[j].at {
		return h[i].seq < h[j].seq
	}
	return h[i].at < h[j].at
}
func (h messageHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *messageHeap) Push(x interface{}) { *h = append(*h, x.(*envelope)) }
func (h *messageHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
