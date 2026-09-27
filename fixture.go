package main

const (
	TraceGood    = "trace-good"
	TraceInvalid = "trace-invalid"
	TraceCycle   = "trace-cycle"
)

func ms(value int64) int64 {
	return value * 1000000
}

// SampleEvents is the local, deterministic fixture used for replay.
// All timestamps are nanoseconds since an arbitrary local clock.
func SampleEvents() []Event {
	return append(append(goodEvents(), invalidEvents()...), cycleEvents()...)
}

func goodEvents() []Event {
	return []Event{
		{At: 1, TraceID: TraceGood, SpanID: "root", Kind: EventStart, Start: ms(0)},
		{At: 2, TraceID: TraceGood, SpanID: "left", Kind: EventStart, Start: ms(2)},
		{At: 3, TraceID: TraceGood, SpanID: "left", Kind: EventEnd, End: ms(12)},
		{At: 4, TraceID: TraceGood, SpanID: "right", Kind: EventStart, Start: ms(2)},
		{At: 5, TraceID: TraceGood, SpanID: "right", Kind: EventEnd, End: ms(12)},
		{At: 6, TraceID: TraceGood, SpanID: "root", Kind: EventEnd, End: ms(12)},
		{At: 7, TraceID: TraceGood, SpanID: "left", Kind: EventReference, RefType: RefChildOf, ParentSpanID: "root"},
		{At: 8, TraceID: TraceGood, SpanID: "right", Kind: EventReference, RefType: RefChildOf, ParentSpanID: "root"},
		{At: 9, TraceID: TraceGood, SpanID: "join", Kind: EventStart, Start: ms(16)},
		{At: 10, TraceID: TraceGood, SpanID: "join", Kind: EventEnd, End: ms(22)},
		{At: 11, TraceID: TraceGood, SpanID: "join", Kind: EventReference, RefType: RefFollowsFrom, ParentSpanID: "left"},
		{At: 12, TraceID: TraceGood, SpanID: "join", Kind: EventReference, RefType: RefFollowsFrom, ParentSpanID: "right"},
	}
}

func invalidEvents() []Event {
	return []Event{
		{At: 101, TraceID: TraceInvalid, SpanID: "negative", Kind: EventStart, Start: ms(10)},
		{At: 102, TraceID: TraceInvalid, SpanID: "negative", Kind: EventEnd, End: ms(0)},
		{At: 103, TraceID: TraceInvalid, SpanID: "producer", Kind: EventStart, Start: ms(0)},
		{At: 104, TraceID: TraceInvalid, SpanID: "producer", Kind: EventEnd, End: ms(10)},
		{At: 105, TraceID: TraceInvalid, SpanID: "consumer", Kind: EventStart, Start: ms(5)},
		{At: 106, TraceID: TraceInvalid, SpanID: "consumer", Kind: EventEnd, End: ms(15)},
		{At: 107, TraceID: TraceInvalid, SpanID: "consumer", Kind: EventReference, RefType: RefFollowsFrom, ParentSpanID: "producer"},
		{At: 108, TraceID: TraceInvalid, SpanID: "consumer", Kind: EventReference, RefType: RefFollowsFrom, ParentSpanID: "missing"},
	}
}

func cycleEvents() []Event {
	events := []Event{}
	at := int64(201)
	for _, id := range []string{"alpha", "beta", "gamma"} {
		events = append(events,
			Event{At: at, TraceID: TraceCycle, SpanID: id, Kind: EventStart, Start: ms(50)},
			Event{At: at + 1, TraceID: TraceCycle, SpanID: id, Kind: EventEnd, End: ms(50)},
		)
		at += 2
	}
	edges := [][2]string{
		{"alpha", "beta"},
		{"beta", "gamma"},
		{"gamma", "alpha"},
	}
	for _, edge := range edges {
		events = append(events, Event{
			At:           at,
			TraceID:      TraceCycle,
			SpanID:       edge[1],
			Kind:         EventReference,
			RefType:      RefFollowsFrom,
			ParentSpanID: edge[0],
		})
		at++
	}
	return events
}
