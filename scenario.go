package kernel

// TraceSeed is the random seed of the reference test trace. It was chosen
// so that exactly 3 of the 50 packet arrivals are lost and therefore
// expire their retransmission timer.
const TraceSeed uint64 = 2

// BuildTrace returns the external event script used by the demo and the
// tests: 50 packet arrivals (one per tick) plus periodic clock interrupts.
func BuildTrace() []Event {
	var events []Event
	for i := 0; i < 50; i++ {
		events = append(events, Event{
			Tick: uint64(i + 1),
			Kind: KindPacketArrival,
			Packet: Packet{
				ID:   uint64(1000 + i),
				Size: uint32(64 + (i*37)%512),
				Src:  uint32(0x0A000001 + i),
				Dst:  0x0A0000FE,
			},
		})
	}
	for tick := uint64(8); tick <= 56; tick += 8 {
		events = append(events, Event{Tick: tick, Kind: KindClockInterrupt})
	}
	return events
}

// RecordTrace runs a fresh kernel over the external event script and
// returns it fully recorded (log + per-event snapshots).
func RecordTrace(seed uint64, external []Event) *Kernel {
	k := NewKernel(seed)
	for _, ev := range external {
		k.SubmitExternal(ev)
	}
	k.Run()
	return k
}
