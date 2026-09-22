package retrysys

import (
	"strconv"

	"example/retrysim/sim"
)

// Effect is the scripted behavior of a replica for one physical request.
type Effect struct {
	Delay       int  // virtual ticks before the reply
	OK          bool // reply outcome
	Drop        bool // no reply ever (forces timeout/retry)
	HonorCancel bool // a cancel before Delay suppresses the reply
	Late        bool // marks the reply as intentionally late
}

// Behavior maps the physical ordinal of a request (1-based within the call)
// to its scripted effect.
type Behavior func(ordinal int) Effect

// Scripted returns effects from the list by 1-based ordinal; later requests
// use fallback.
func Scripted(effects []Effect, fallback Effect) Behavior {
	return func(ordinal int) Effect {
		if ordinal >= 1 && ordinal <= len(effects) {
			return effects[ordinal-1]
		}
		return fallback
	}
}

// FailFirst fails the first n requests, then succeeds; all replies are fast
// and cancellation is honored. DropFirst drops the first n (no reply).
func FailFirst(n, delay int) Behavior {
	return Scripted(repeat(n, Effect{Delay: delay, OK: false, HonorCancel: true}),
		Effect{Delay: delay, OK: true, HonorCancel: true})
}

// DropAll never replies, exercising timeouts and cancellation.
func DropAll() Behavior {
	return func(int) Effect { return Effect{Drop: true} }
}

func repeat(n int, e Effect) []Effect {
	out := make([]Effect, n)
	for i := range out {
		out[i] = e
	}
	return out
}

type pendingReply struct {
	ord     int
	resp    ReplicaResp
	honored bool
}

type replicaTimer struct {
	key string
}

// Replica is a simulated backend node. It holds no retry logic; it only obeys
// a fault script and schedules its reply on the virtual clock, which makes
// network delay and cancellation controllable.
type Replica struct {
	name    string
	world   *World
	beh     Behavior
	pending map[string]pendingReply
}

// NewReplica builds a replica actor.
func NewReplica(name string, w *World, beh Behavior) *Replica {
	return &Replica{name: name, world: w, beh: beh, pending: map[string]pendingReply{}}
}

// Receive implements sim.Actor.
func (r *Replica) Receive(m sim.Msg, send func(sim.Msg), after func(any, int)) {
	switch b := m.Body.(type) {
	case ReplicaReq:
		eff := r.beh(b.Ordinal)
		if eff.Drop {
			return // request (or reply) lost: nothing is scheduled
		}
		key := b.Physical.String()
		r.pending[key] = pendingReply{
			ord: b.Ordinal,
			resp: ReplicaResp{
				Call:     b.Call,
				Service:  b.Service,
				Replica:  r.name,
				Physical: b.Physical,
				OK:       eff.OK,
				Late:     eff.Late,
			},
			honored: eff.HonorCancel,
		}
		after(replicaTimer{key: key}, eff.Delay)
	case ReplicaCancel:
		p, ok := r.pending[b.Physical.String()]
		if ok && p.honored {
			delete(r.pending, b.Physical.String())
			r.world.Ledger.Record(r.world.Now(), KindCancel, b.Physical,
				WhyCancelled, "replica-suppressed-reply")
		}
		// If the cancel is not honored the failure still arrives later:
		// a controlled "late failure" the retry layer must ignore.
	case replicaTimer:
		p, ok := r.pending[b.key]
		if !ok {
			return // cancelled in the meantime
		}
		delete(r.pending, b.key)
		send(sim.Msg{From: r.name, To: "service", Body: p.resp})
	}
}

// replicaNames builds n stable replica actor names.
func replicaNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = "replica-" + strconv.Itoa(i)
	}
	return names
}
