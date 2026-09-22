package causalslice

import "fmt"

// Envelope is a single causal message exchanged between actors in the
// single-process event simulator.
type Envelope struct {
	From string
	To   string
	Msg  interface{}
}

// Actor is any entity driven by the simulator: replicas and clients.
// Receive is invoked only while the actor is up.
type Actor interface {
	ID() string
	Receive(sim *Sim, env Envelope)
}

// Sim is a deterministic, single-process event simulator. Time is logical:
// every event carries an integer tick, the heap always releases the smallest
// (time, sequence) pair, so identical inputs produce identical executions.
type Sim struct {
	t      int
	actors map[string]Actor
	heap   eventHeap
	net    *Network
	log    func(time int, text string)
	seq    int
}

// NewSim builds a simulator with the given network policy and optional logger.
func NewSim(net *Network, log func(time int, text string)) *Sim {
	if net == nil {
		net = NewNetwork()
	}
	if log == nil {
		log = func(int, string) {}
	}
	return &Sim{
		actors: map[string]Actor{},
		net:    net,
		log:    log,
	}
}

// Network exposes the installed network policy.
func (s *Sim) Network() *Network { return s.net }

// Time returns the current logical time.
func (s *Sim) Time() int { return s.t }

// Register adds an actor; duplicate registration panics.
func (s *Sim) Register(a Actor) {
	if _, ok := s.actors[a.ID()]; ok {
		panic("duplicate actor " + a.ID())
	}
	s.actors[a.ID()] = a
}

// Actor returns the registered actor.
func (s *Sim) Actor(id string) Actor { return s.actors[id] }

// Log records a time-stamped line through the configured logger.
func (s *Sim) Log(format string, args ...interface{}) {
	s.log(s.t, fmt.Sprintf(format, args...))
}

// Send submits a message to the network policy for delivery scheduling.
func (s *Sim) Send(env Envelope) {
	if env.From == "" || env.To == "" || env.Msg == nil {
		panic("ill-formed envelope")
	}
	if _, ok := s.actors[env.To]; !ok {
		panic("unknown destination " + env.To)
	}
	s.net.route(s, env)
}

// deliver is the Network hook: a message that survived the policy is placed
// on the event heap. Messages addressed to a currently crashed actor are
// discarded, modelling loss of the in-flight packet.
func (s *Sim) deliver(env Envelope, at int) {
	s.seq++
	heapPush(&s.heap, event{time: at, seq: s.seq, env: env})
}

// Schedule places a self-timer on the heap. Tag is carried verbatim so one
// actor can distinguish its timers.
func (s *Sim) Schedule(target string, at int, tag interface{}) {
	if at <= s.t {
		panic("timers must be scheduled strictly in the future")
	}
	if _, ok := s.actors[target]; !ok {
		panic("unknown timer target " + target)
	}
	s.seq++
	heapPush(&s.heap, event{time: at, seq: s.seq, isTimer: true, timer: timer{target: target, tag: tag}})
}

// Run advances logical time, dispatching events, until no event remains or the
// next event exceeds horizon. Crash/recovery events are honoured here: a
// crashed actor receives neither messages nor timer firings.
func (s *Sim) Run(horizon int) {
	for len(s.heap) > 0 {
		ev := heapPop(&s.heap)
		if ev.time > horizon {
			heapPush(&s.heap, ev)
			break
		}
		s.t = ev.time
		if ev.isTimer {
			a, ok := s.actors[ev.timer.target]
			if ok && up(a) {
				a.Receive(s, Envelope{To: a.ID(), Msg: Timer{Tag: ev.timer.tag}})
			}
			continue
		}
		a, ok := s.actors[ev.env.To]
		if ok && up(a) {
			a.Receive(s, ev.env)
		} else {
			s.Log("packet to %s dropped: actor unavailable", ev.env.To)
		}
	}
}

type lifecycle interface{ down() bool }

func up(a Actor) bool {
	if l, ok := a.(lifecycle); ok {
		return !l.down()
	}
	return true
}

// Timer is delivered to an actor when one of its scheduled timers fires.
type Timer struct{ Tag interface{} }

type timer struct {
	target string
	tag    interface{}
}

type event struct {
	time    int
	seq     int
	isTimer bool
	env     Envelope
	timer   timer
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func heapPush(h *eventHeap, e event) {
	*h = append(*h, e)
	n := len(*h) - 1
	for n > 0 {
		parent := (n - 1) / 2
		if !h.Less(n, parent) {
			break
		}
		h.Swap(n, parent)
		n = parent
	}
}

func heapPop(h *eventHeap) event {
	old := *h
	top := old[0]
	last := len(old) - 1
	old[0] = old[last]
	*h = old[:last]
	n := 0
	for {
		left := 2*n + 1
		right := left + 1
		best := n
		if left < len(*h) && h.Less(left, best) {
			best = left
		}
		if right < len(*h) && h.Less(right, best) {
			best = right
		}
		if best == n {
			break
		}
		h.Swap(n, best)
		n = best
	}
	return top
}
