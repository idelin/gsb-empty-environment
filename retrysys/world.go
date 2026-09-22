package retrysys

import (
	"fmt"

	"example/retrysim/sim"
)

// Config fixes the retry and timing parameters of one top-level call.
type Config struct {
	UpperAttempts       int
	LowerAttempts       int
	MaxRequests         int // shared physical-request budget across both layers
	Deadline            int // virtual tick; no send at or after it
	UpperAttemptTimeout int
	LowerAttemptTimeout int
	UpperBackoff        int
	Replicas            int
	MaxSimSteps         int
}

// DefaultConfig is the canonical "upper 3, lower 3" configuration with a
// shared budget deliberately smaller than the 3x3 product.
func DefaultConfig() Config {
	return Config{
		UpperAttempts:       3,
		LowerAttempts:       3,
		MaxRequests:         3,
		Deadline:            100,
		UpperAttemptTimeout: 60,
		LowerAttemptTimeout: 10,
		UpperBackoff:        1,
		Replicas:            3,
		MaxSimSteps:         10000,
	}
}

// World wires the proxy, service and replicas into one simulator and owns the
// shared budget, deadline and ledger.
type World struct {
	cfg    Config
	sim    *sim.Simulator
	Ledger *Ledger
	budget *Budget

	proxy   *Proxy
	service *Service
	reps    []*Replica
}

// NewWorld assembles the actors. replicas maps each replica name to its
// scripted behavior (len must equal cfg.Replicas).
func NewWorld(cfg Config, replicaBeh map[string]Behavior) *World {
	w := &World{cfg: cfg, Ledger: &Ledger{}}
	w.budget = NewBudget(cfg.MaxRequests, cfg.Deadline)

	w.proxy = NewProxy(w, cfg.UpperAttempts, cfg.UpperAttemptTimeout, cfg.UpperBackoff)
	w.service = NewService(w, cfg.LowerAttempts, cfg.LowerAttemptTimeout)

	names := replicaNames(cfg.Replicas)
	actors := map[string]sim.Actor{
		"proxy":   w.proxy,
		"service": w.service,
	}
	for _, name := range names {
		beh := replicaBeh[name]
		if beh == nil {
			beh = DropAll()
		}
		r := NewReplica(name, w, beh)
		w.reps = append(w.reps, r)
		actors[name] = r
	}
	w.sim = sim.New(actors)
	return w
}

// Run starts one top-level call, advances virtual time, fires the shared
// deadline exactly once, and returns the terminal result and final tick.
func (w *World) Run() (ok bool, why Why, endTick int, err error) {
	w.sim.Send(sim.Msg{To: "proxy", Body: StartCall{Call: 1}})
	// The single shared deadline: budget expires and both layers are told to
	// cancel; afterwards no attempt may be opened.
	w.sim.After("service", w.cfg.Deadline, DeadlineFire{Call: 1})
	w.sim.After("proxy", w.cfg.Deadline, DeadlineFire{Call: 1})

	if _, err = w.sim.Run(w.cfg.MaxSimSteps); err != nil {
		return false, "", 0, err
	}
	term, found := w.Ledger.FirstTerminal(1, "proxy")
	if !found {
		return false, "", 0, fmt.Errorf("world: no proxy terminal event (liveness failure)")
	}
	// The terminal tick (not the final drained timer tick) is when the call
	// actually settled; remaining pre-scheduled timers cannot change it.
	return term.Why == WhyOK, term.Why, term.Tick, nil
}

// Now exposes the simulator's virtual clock.
func (w *World) Now() int { return w.sim.Now() }

// Budget exposes the shared budget (tests inspect actual usage through it).
func (w *World) Budget() *Budget { return w.budget }

// ExpireBudget is used by the deadline path.
func (w *World) expireBudget() { w.budget.Expire() }

// pickReplica chooses the target replica for physical ordinal n (1-based).
// A deterministic round-robin keeps the trace stable.
func (w *World) pickReplica(ordinal int) string {
	names := replicaNames(w.cfg.Replicas)
	return names[(ordinal-1)%len(names)]
}

// localTo schedules a same-component control message with Local ordering.
func (w *World) localTo(actor string, body any) {
	w.sim.Local(sim.Msg{To: actor, Body: body})
}

// sendTo schedules a message from the service to an actor.
func (w *World) sendTo(to string, body any) {
	w.sim.Send(sim.Msg{From: "service", To: to, Body: body})
}

// afterTo returns a scheduler bound to the given actor; used when a lower
// retry is launched outside the actor's own Receive call.
func (w *World) afterTo(actor string) func(any, int) {
	return func(data any, ticks int) { w.sim.After(actor, ticks, data) }
}
