// Package sim is a single-process deterministic event simulator.
//
// Everything that can fail, communicate, or observe time goes through the
// Simulator: actors exchange messages, timers are scheduled with After, and
// the virtual clock advances only when no event remains at the current
// instant. There is no wall clock and no goroutine inside actors, so a given
// scenario script produces exactly one execution trace.
//
// Two same-instant send primitives are offered:
//
//   - Send models a network delivery: it queues behind events already pending
//     at the current instant (FIFO), so the simulated network keeps a
//     consistent observable order.
//   - Local models a same-component control transition (for example a layer
//     terminalizing before the layer above reacts). It is delivered before
//     events already queued at the same instant, but still runs through the
//     event loop, so actor code never re-enters itself.
package sim

import (
	"errors"
	"fmt"
)

// Msg is an opaque point-to-point message between two named actors.
type Msg struct {
	From string
	To   string
	Body any
}

// Actor is a state machine driven by message deliveries.
type Actor interface {
	Receive(m Msg, send func(Msg), after func(d any, ticks int))
}

// Simulator schedules messages and timers on a discrete virtual clock.
type Simulator struct {
	now     int
	seq     int64 // increasing ids for FIFO (network) events
	localID int64 // decreasing ids for local control events
	queue   []event
	actors  map[string]Actor
	steps   int
}

type event struct {
	time int
	seq  int64
	msg  *Msg
	data any
	to   string
}

// New creates a Simulator with the given actors registered by name.
func New(actors map[string]Actor) *Simulator {
	return &Simulator{actors: actors, localID: 0}
}

// Now returns the current virtual time in ticks.
func (s *Simulator) Now() int { return s.now }

// Steps returns the number of events processed so far.
func (s *Simulator) Steps() int { return s.steps }

// Send queues a network message for delivery at the current instant, behind
// events already queued at that instant.
func (s *Simulator) Send(m Msg) {
	s.seq++
	s.queue = append(s.queue, event{time: s.now, seq: s.seq, msg: &m, to: m.To})
}

// Local queues a same-component control message that is delivered before
// already-queued events at the current instant.
func (s *Simulator) Local(m Msg) {
	s.localID--
	s.queue = append(s.queue, event{time: s.now, seq: s.localID, msg: &m, to: m.To})
}

// After schedules a timer carrying data that is delivered to target after
// ticks (clamped to a non-negative delay).
func (s *Simulator) After(target string, ticks int, data any) {
	if ticks < 0 {
		ticks = 0
	}
	s.seq++
	s.queue = append(s.queue, event{time: s.now + ticks, seq: s.seq, data: data, to: target})
}

// ErrNoActor is returned when an event targets an unregistered actor.
var ErrNoActor = errors.New("sim: message to unknown actor")

// Run drains the event queue up to maxSteps events. Events are ordered by
// time, then by sequence id: local control events (negative) first, FIFO
// network events (positive) afterwards. That makes every trace deterministic.
func (s *Simulator) Run(maxSteps int) (int, error) {
	for len(s.queue) > 0 {
		idx := 0
		for i := 1; i < len(s.queue); i++ {
			if s.queue[i].time < s.queue[idx].time ||
				(s.queue[i].time == s.queue[idx].time && s.queue[i].seq < s.queue[idx].seq) {
				idx = i
			}
		}
		ev := s.queue[idx]
		s.queue = append(s.queue[:idx], s.queue[idx+1:]...)

		if s.steps >= maxSteps {
			return s.now, fmt.Errorf("sim: exceeded %d events (possible livelock)", maxSteps)
		}
		s.now = ev.time
		target, ok := s.actors[ev.to]
		if !ok {
			return s.now, fmt.Errorf("%w: %q at tick %d", ErrNoActor, ev.to, ev.time)
		}
		s.steps++
		var m Msg
		if ev.msg != nil {
			m = *ev.msg
		} else {
			m = Msg{To: ev.to, Body: ev.data}
		}
		target.Receive(m, s.Send, func(d any, ticks int) { s.After(ev.to, ticks, d) })
	}
	return s.now, nil
}
