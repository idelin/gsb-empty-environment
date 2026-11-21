package gatetable

// Transmission is one egress copy start in a device replay.
type Transmission struct {
	Frame    int
	Instance int // period generation, may be negative for carry-in
	Port     int
	Start    int // absolute tick
	End      int // absolute exclusive end
}

// SlotEvent records a shared-slot acquisition or release.  Tick is absolute.
type SlotEvent struct {
	Frame    int
	Instance int
	Tick     int
}

// ReplayTick is one tick of the device replay.
type ReplayTick struct {
	Tick         int
	PortMask     uint8
	BusyPorts    []int
	Starts       []Transmission
	ActiveCopies []Transmission
	SlotAcquires []SlotEvent
	SlotReleases []SlotEvent
	ActiveSlots  int
}

// Replay returns a tick-by-tick replay for the requested number of periods.
// Tick 0 is a steady-state tick: slots and copies from older generations are
// included in ActiveSlots and PortMask even if their starts are before tick 0.
// Events whose absolute tick falls in [0,cycles*Period) are listed.
func (r *Result) Replay(cycles int) []ReplayTick {
	if cycles <= 0 || r == nil || r.Period <= 0 {
		return nil
	}
	p := r.problem
	endTick := cycles * p.Period
	ticks := make([]ReplayTick, endTick)
	acquires := make([][]SlotEvent, endTick)
	releases := make([][]SlotEvent, endTick)
	starts := make([][]Transmission, endTick)

	active := 0
	var activeCopies []Transmission
	for fi, frame := range p.Frames {
		latency := r.FrameLatencies[fi]
		for k := -3; k <= cycles+1; k++ {
			release := frame.Release + k*p.Period
			// Slot interval is [release, release+latency).
			// State just before tick 0.  Intervals ending exactly at 0 are
			// included here and removed by the release event at tick 0.
			if release < 0 && release+latency >= 0 {
				active++
			}
			if release >= 0 && release < endTick {
				acquires[release] = append(acquires[release], SlotEvent{
					Frame: fi, Instance: k, Tick: release,
				})
			}
			if rel := release + latency; rel >= 0 && rel < endTick {
				releases[rel] = append(releases[rel], SlotEvent{
					Frame: fi, Instance: k, Tick: rel,
				})
			}

			for i, port := range frame.Ports {
				start := release + r.Frames[fi].Offsets[i]
				cp := Transmission{
					Frame: fi, Instance: k, Port: port,
					Start: start, End: start + frame.Durations[i],
				}
				if start < 0 && cp.End > 0 {
					activeCopies = append(activeCopies, cp)
				}
				if start < 0 || start >= endTick {
					continue
				}
				starts[start] = append(starts[start], cp)
			}
		}
	}

	for t := 0; t < endTick; t++ {
		for range releases[t] {
			active--
		}
		for range acquires[t] {
			active++
		}
		kept := activeCopies[:0]
		for _, cp := range activeCopies {
			if cp.End > t {
				kept = append(kept, cp)
			}
		}
		activeCopies = append(kept, starts[t]...)
		tick := ReplayTick{
			Tick:         t,
			PortMask:     r.Masks[t%p.Period],
			Starts:       starts[t],
			ActiveCopies: append([]Transmission(nil), activeCopies...),
			SlotAcquires: acquires[t],
			SlotReleases: releases[t],
			ActiveSlots:  active,
		}
		for port := 0; port < len(p.Ports); port++ {
			if tick.PortMask&(uint8(1)<<uint(port)) != 0 {
				tick.BusyPorts = append(tick.BusyPorts, port)
			}
		}
		ticks[t] = tick
	}
	return ticks
}
