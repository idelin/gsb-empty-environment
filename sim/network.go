package sim

import "math/rand"

// Switch is a virtual network switch sitting inside the simulator.
// It models per-link partitions, a uniform packet loss rate and
// uniformly distributed latency jitter. All randomness comes from a
// single seeded RNG, so traffic patterns are exactly reproducible.
type Switch struct {
	sim      *Simulator
	rng      *rand.Rand
	loss     float64
	minDelay uint64
	maxDelay uint64
	handlers map[string]func(from string, msg any)
	cut      map[[2]string]bool

	Sent      uint64
	Dropped   uint64
	Delivered uint64
}

// NewSwitch builds a switch. lossRate is the probability [0,1) of
// dropping a packet; delivery delay is uniform in [minDelay, maxDelay]
// ticks.
func NewSwitch(sim *Simulator, seed int64, lossRate float64, minDelay, maxDelay uint64) *Switch {
	return &Switch{
		sim:      sim,
		rng:      rand.New(rand.NewSource(seed)),
		loss:     lossRate,
		minDelay: minDelay,
		maxDelay: maxDelay,
		handlers: make(map[string]func(from string, msg any)),
		cut:      make(map[[2]string]bool),
	}
}

// Attach registers the delivery handler for an address.
func (sw *Switch) Attach(addr string, h func(from string, msg any)) {
	sw.handlers[addr] = h
}

// Partition cuts the link between a and b in both directions.
func (sw *Switch) Partition(a, b string) {
	sw.cut[[2]string{a, b}] = true
	sw.cut[[2]string{b, a}] = true
}

// Heal restores a link previously cut by Partition.
func (sw *Switch) Heal(a, b string) {
	delete(sw.cut, [2]string{a, b})
	delete(sw.cut, [2]string{b, a})
}

// Send queues msg for delivery from -> to, applying the partition,
// loss and jitter models. Delivery happens inside the simulator, so
// the receiving handler runs on the virtual clock.
func (sw *Switch) Send(from, to string, msg any) {
	sw.Sent++
	if sw.cut[[2]string{from, to}] {
		sw.Dropped++
		return
	}
	if sw.rng.Float64() < sw.loss {
		sw.Dropped++
		return
	}
	delay := sw.minDelay
	if sw.maxDelay > sw.minDelay {
		delay += uint64(sw.rng.Int63n(int64(sw.maxDelay - sw.minDelay + 1)))
	}
	sw.sim.After(delay, func() {
		sw.Delivered++
		if h, ok := sw.handlers[to]; ok {
			h(from, msg)
		}
	})
}
