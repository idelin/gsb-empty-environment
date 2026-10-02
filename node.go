package swim

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"
)

// State is the membership status of a peer as seen by a node.
type State int

const (
	Alive State = iota
	Suspect
	Down
)

func (s State) String() string {
	switch s {
	case Alive:
		return "Alive"
	case Suspect:
		return "Suspect"
	case Down:
		return "Down"
	}
	return "Unknown"
}

// EventType classifies observability events emitted by a Node.
type EventType int

const (
	EventProbeStart EventType = iota
	EventDirectTimeout
	EventIndirectStart
	EventProbeSuccess
	EventProbeFailed
	EventStateChange
)

// Event is an observability hook emitted during probing.
type Event struct {
	Node    string
	Type    EventType
	Target  string
	Via     string   // proxy that delivered the Ack; empty for direct Acks
	Proxies []string // set on EventIndirectStart
	From    State    // set on EventStateChange
	To      State    // set on EventStateChange
}

func (e Event) String() string {
	switch e.Type {
	case EventProbeStart:
		return fmt.Sprintf("[%s] probe start -> %s", e.Node, e.Target)
	case EventDirectTimeout:
		return fmt.Sprintf("[%s] direct ping to %s timed out", e.Node, e.Target)
	case EventIndirectStart:
		return fmt.Sprintf("[%s] indirect probe of %s via %v", e.Node, e.Target, e.Proxies)
	case EventProbeSuccess:
		if e.Via == "" {
			return fmt.Sprintf("[%s] ack from %s (direct)", e.Node, e.Target)
		}
		return fmt.Sprintf("[%s] ack from %s via %s", e.Node, e.Target, e.Via)
	case EventProbeFailed:
		return fmt.Sprintf("[%s] probe of %s failed (direct + indirect)", e.Node, e.Target)
	case EventStateChange:
		return fmt.Sprintf("[%s] %s: %s -> %s", e.Node, e.Target, e.From, e.To)
	}
	return "unknown event"
}

// Config tunes a Node.
type Config struct {
	ID string
	// Interval is the probe period: one random peer is probed per interval.
	Interval time.Duration
	// AckTimeout is how long a direct Ping waits for an Ack before
	// falling back to indirect probes.
	AckTimeout time.Duration
	// IndirectTimeout is how long PingReq probes wait for a forwarded Ack.
	IndirectTimeout time.Duration
	// K is the number of indirect proxy nodes used per probe.
	K int
	// PickTarget optionally overrides random target selection (tests).
	PickTarget func(peers []string) string
	// Events, if non-nil, receives probe/membership events (best effort).
	Events chan<- Event
}

const (
	phaseDirect = iota
	phaseIndirect
)

type probe struct {
	target string
	seq    int
	phase  int
}

// Node is a SWIM-style failure detector instance.
type Node struct {
	cfg   Config
	clock Clock
	tr    Transport
	rnd   *rand.Rand

	inbox    chan Message
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}

	mu      sync.Mutex
	members map[string]State

	// seq and the current probe are only touched by the Run goroutine.
	seq int
}

// NewNode creates a node; peers is the initial membership list (all Alive).
func NewNode(cfg Config, clock Clock, tr Transport, peers []string, seed int64) *Node {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	if cfg.AckTimeout <= 0 {
		cfg.AckTimeout = 200 * time.Millisecond
	}
	if cfg.IndirectTimeout <= 0 {
		cfg.IndirectTimeout = 400 * time.Millisecond
	}
	if cfg.K <= 0 {
		cfg.K = 2
	}
	n := &Node{
		cfg:     cfg,
		clock:   clock,
		tr:      tr,
		rnd:     rand.New(rand.NewSource(seed)),
		inbox:   make(chan Message, 128),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		members: make(map[string]State),
	}
	for _, p := range peers {
		if p != cfg.ID {
			n.members[p] = Alive
		}
	}
	return n
}

// Inbox returns the channel to register with a Transport.
func (n *Node) Inbox() chan<- Message { return n.inbox }

// Start launches the probe loop in a goroutine.
func (n *Node) Start() { go n.Run() }

// Stop terminates the probe loop and waits for it to exit.
func (n *Node) Stop() {
	n.stopOnce.Do(func() { close(n.stop) })
	<-n.done
}

// StateOf returns the locally known state of a peer.
func (n *Node) StateOf(id string) State {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.members[id]
}

// Members returns a snapshot of the local membership view.
func (n *Node) Members() map[string]State {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make(map[string]State, len(n.members))
	for id, st := range n.members {
		out[id] = st
	}
	return out
}

// Run is the main loop: once per Interval it probes one random peer with
// a direct Ping; on AckTimeout it escalates to K indirect PingReq proxies;
// on IndirectTimeout with no Ack the peer degrades Alive->Suspect->Down.
func (n *Node) Run() {
	defer close(n.done)
	interval := n.clock.NewTimer(n.cfg.Interval)
	var phaseTimer Timer
	var cur *probe

	clearProbe := func() {
		if phaseTimer != nil {
			phaseTimer.Stop()
			phaseTimer = nil
		}
		cur = nil
	}

	for {
		var phaseC <-chan time.Time
		if phaseTimer != nil {
			phaseC = phaseTimer.C()
		}
		select {
		case <-n.stop:
			interval.Stop()
			clearProbe()
			return

		case msg := <-n.inbox:
			acked, via := n.handle(msg, cur)
			if acked && cur != nil {
				n.emit(Event{Type: EventProbeSuccess, Target: cur.target, Via: via})
				n.setState(cur.target, Alive)
				clearProbe()
			}

		case <-interval.C():
			interval = n.clock.NewTimer(n.cfg.Interval)
			if cur != nil {
				continue // previous probe still in flight
			}
			target := n.pickTarget()
			if target == "" {
				continue
			}
			n.seq++
			cur = &probe{target: target, seq: n.seq, phase: phaseDirect}
			n.emit(Event{Type: EventProbeStart, Target: target})
			n.tr.Send(n.cfg.ID, target, Message{Type: Ping, From: n.cfg.ID, Seq: cur.seq})
			phaseTimer = n.clock.NewTimer(n.cfg.AckTimeout)

		case <-phaseC:
			if cur == nil {
				continue
			}
			if cur.phase == phaseDirect {
				cur.phase = phaseIndirect
				proxies := n.pickProxies(cur.target)
				n.emit(Event{Type: EventDirectTimeout, Target: cur.target})
				n.emit(Event{Type: EventIndirectStart, Target: cur.target, Proxies: proxies})
				for _, p := range proxies {
					n.tr.Send(n.cfg.ID, p, Message{Type: PingReq, From: n.cfg.ID, Seq: cur.seq, Target: cur.target})
				}
				phaseTimer = n.clock.NewTimer(n.cfg.IndirectTimeout)
			} else {
				n.emit(Event{Type: EventProbeFailed, Target: cur.target})
				n.failMember(cur.target)
				clearProbe()
			}
		}
	}
}

// handle processes one inbound message. It reports whether the message
// completes the current probe, and via which proxy ("" = direct).
func (n *Node) handle(msg Message, cur *probe) (acked bool, via string) {
	switch msg.Type {
	case Ping:
		n.noteAlive(msg.From)
		n.tr.Send(n.cfg.ID, msg.From, Message{
			Type: Ack, From: n.cfg.ID, Seq: msg.Seq, ForwardTo: msg.ForwardTo,
		})

	case PingReq:
		n.noteAlive(msg.From)
		if msg.Target == n.cfg.ID {
			// We are the target: answer the originator directly.
			n.tr.Send(n.cfg.ID, msg.From, Message{Type: Ack, From: n.cfg.ID, Seq: msg.Seq})
			return false, ""
		}
		// Proxy duty: ping the target, ask it to route the Ack back to us
		// so we can forward it to the originator.
		n.tr.Send(n.cfg.ID, msg.Target, Message{
			Type: Ping, From: n.cfg.ID, Seq: msg.Seq, ForwardTo: msg.From,
		})

	case Ack:
		if msg.ForwardTo != "" {
			// Proxy duty: forward the target's Ack to the originator.
			n.tr.Send(n.cfg.ID, msg.ForwardTo, Message{
				Type: Ack, From: msg.From, Seq: msg.Seq, Via: n.cfg.ID,
			})
			return false, ""
		}
		n.noteAlive(msg.From)
		if cur != nil && cur.seq == msg.Seq && cur.target == msg.From {
			return true, msg.Via
		}
	}
	return false, ""
}

func (n *Node) pickTarget() string {
	n.mu.Lock()
	var peers []string
	for id, st := range n.members {
		if st != Down {
			peers = append(peers, id)
		}
	}
	n.mu.Unlock()
	if len(peers) == 0 {
		return ""
	}
	sort.Strings(peers)
	if n.cfg.PickTarget != nil {
		return n.cfg.PickTarget(peers)
	}
	return peers[n.rnd.Intn(len(peers))]
}

func (n *Node) pickProxies(target string) []string {
	n.mu.Lock()
	var candidates []string
	for id, st := range n.members {
		if id != target && st == Alive {
			candidates = append(candidates, id)
		}
	}
	n.mu.Unlock()
	sort.Strings(candidates)
	n.rnd.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})
	if len(candidates) > n.cfg.K {
		candidates = candidates[:n.cfg.K]
	}
	return candidates
}

func (n *Node) setState(id string, st State) {
	n.mu.Lock()
	old, ok := n.members[id]
	if ok && old != st {
		n.members[id] = st
	}
	n.mu.Unlock()
	if ok && old != st {
		n.emit(Event{Type: EventStateChange, Target: id, From: old, To: st})
	}
}

func (n *Node) noteAlive(id string) {
	n.mu.Lock()
	_, known := n.members[id]
	n.mu.Unlock()
	if known {
		n.setState(id, Alive)
	}
}

func (n *Node) failMember(id string) {
	switch n.StateOf(id) {
	case Alive:
		n.setState(id, Suspect)
	case Suspect:
		n.setState(id, Down)
	}
}

func (n *Node) emit(e Event) {
	if n.cfg.Events == nil {
		return
	}
	e.Node = n.cfg.ID
	select {
	case n.cfg.Events <- e:
	default:
	}
}
