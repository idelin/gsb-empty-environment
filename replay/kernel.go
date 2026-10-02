package replay

import (
	"encoding/binary"
	"fmt"
)

// TimeoutInterval is the virtual-clock delay after which an unacknowledged
// packet triggers a retransmission clock interrupt.
const TimeoutInterval uint64 = 100

// lossModulo controls the simulated link: an ack is lost when the kernel
// RNG draw modulo lossModulo is zero. All draws come from the seeded,
// recorded RNG so losses replay deterministically.
const lossModulo uint64 = 16

type timer struct {
	fireAt uint64
	seq    uint64
}

// Kernel is the deterministic event scheduler. It owns the virtual clock,
// the seeded RNG, the component under debug and the pending timer queue.
// Replaying the same event log from a fresh kernel always reproduces the
// exact same memory state.
type Kernel struct {
	clock   uint64
	rng     *Rng
	sys     *NetSystem
	timers  []timer
	log     *EventLog
	applied int // number of log events applied so far
}

// NewKernel returns a kernel in its pristine initial state.
func NewKernel() *Kernel {
	return &Kernel{
		rng: NewRng(0),
		sys: &NetSystem{},
		log: &EventLog{},
	}
}

// Clock returns the current virtual clock value.
func (k *Kernel) Clock() uint64 { return k.clock }

// System returns the component under debug.
func (k *Kernel) System() *NetSystem { return k.sys }

// Log returns the kernel's event log.
func (k *Kernel) Log() *EventLog { return k.log }

// Applied returns how many log events have been applied.
func (k *Kernel) Applied() int { return k.applied }

// apply executes one event. It is the only state-transition function and
// is used identically by live recording and by replay.
func (k *Kernel) apply(e Event) {
	k.clock = e.Tick
	switch e.Type {
	case EventSeed:
		k.rng = NewRng(binary.LittleEndian.Uint64(e.Payload))
	case EventPacketArrival:
		k.sys.onPacket(e.Seq, e.Payload)
		if k.rng.Intn(lossModulo) == 0 {
			// Ack lost on the link: arm a retransmission timer.
			k.sys.Lost++
			k.timers = append(k.timers, timer{fireAt: e.Tick + TimeoutInterval, seq: e.Seq})
		} else {
			k.sys.ack(e.Seq)
		}
	case EventTimeout:
		k.sys.onTimeout(e.Seq)
		for i, t := range k.timers {
			if t.seq == e.Seq {
				k.timers = append(k.timers[:i], k.timers[i+1:]...)
				break
			}
		}
	}
	k.applied++
}

// step records (when recording) and applies one event.
func (k *Kernel) step(e Event, record bool) {
	if record {
		k.log.Append(e)
	}
	k.apply(e)
}

// nextTimer returns the earliest pending timer, if any.
func (k *Kernel) nextTimer() (timer, bool) {
	if len(k.timers) == 0 {
		return timer{}, false
	}
	earliest := k.timers[0]
	for _, t := range k.timers[1:] {
		if t.fireAt < earliest.fireAt {
			earliest = t
		}
	}
	return earliest, true
}

// Record runs a live simulation: packetCount packet arrivals are generated
// by the environment (envSeed), interleaved with any retransmission clock
// interrupts the kernel schedules. Every external input, clock interrupt
// and the random seed is appended to the returned log, and a snapshot is
// captured after each applied event.
func Record(seed, envSeed uint64, packetCount int) (*Kernel, []Snapshot) {
	k := NewKernel()

	seedPayload := make([]byte, 8)
	binary.LittleEndian.PutUint64(seedPayload, seed)
	k.step(Event{Tick: 0, Type: EventSeed, Payload: seedPayload}, true)

	snaps := []Snapshot{k.Snapshot()}

	env := NewRng(envSeed)
	type arrival struct {
		tick    uint64
		payload []byte
	}
	arrivals := make([]arrival, 0, packetCount)
	tick := uint64(0)
	for seq := 0; seq < packetCount; seq++ {
		tick += 1 + env.Intn(50)
		size := 16 + env.Intn(240)
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(env.Next())
		}
		arrivals = append(arrivals, arrival{tick: tick, payload: payload})
	}

	next := 0
	for next < len(arrivals) || len(k.timers) > 0 {
		fireTimer, hasTimer := k.nextTimer()
		if hasTimer && (next >= len(arrivals) || fireTimer.fireAt <= arrivals[next].tick) {
			k.step(Event{Tick: fireTimer.fireAt, Type: EventTimeout, Seq: fireTimer.seq}, true)
		} else {
			a := arrivals[next]
			k.step(Event{Tick: a.tick, Type: EventPacketArrival, Seq: uint64(next), Payload: a.payload}, true)
			next++
		}
		snaps = append(snaps, k.Snapshot())
	}
	return k, snaps
}

// ReplayTo reconstructs a kernel by applying the first n events of log to
// a pristine kernel. It performs no I/O and touches no other kernel, so
// time travel is side-effect free.
func ReplayTo(log *EventLog, n int) (*Kernel, error) {
	if n < 0 || n > log.Len() {
		return nil, fmt.Errorf("replay: target %d out of range [0, %d]", n, log.Len())
	}
	k := NewKernel()
	k.log = log
	for i := 0; i < n; i++ {
		k.apply(log.Events[i])
	}
	return k, nil
}

// TravelTo moves this kernel to the state after the first n recorded
// events by deterministic replay. The log is never mutated, and travelling
// back to the present restores the exact prior state.
func (k *Kernel) TravelTo(n int) error {
	fresh, err := ReplayTo(k.log, n)
	if err != nil {
		return err
	}
	log := k.log
	*k = *fresh
	k.log = log
	return nil
}
