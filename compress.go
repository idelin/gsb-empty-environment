package eventlog

// Run is a run-length encoded batch of consecutive identical events.
type Run struct {
	Event Event
	Count int
}

// Observation is a public observation point preserved by compression.
type Observation struct {
	Index int // entry index in the expanded (original) log
	Value int // observed state value right after the event at Index
}

// CompressedLog is a compressed log segment. It keeps the initial and final
// state summaries and every public observation point, and carries the full
// ordered list of original event summaries (run-length encoded) so a
// verifier can replay the segment.
//
// Events are merged only when consecutive and identical. They are never
// reordered or merged by type counts: Add/Sub/Mul/Set do not commute, so
// count-based merging would change replay results.
type CompressedLog struct {
	Initial      State
	Final        State
	Runs         []Run
	Observations []Observation
}

// Compress compresses a log segment that was executed from initial and
// ended in final.
func Compress(initial State, entries []Entry, final State) CompressedLog {
	c := CompressedLog{Initial: initial, Final: final}
	for i, entry := range entries {
		if n := len(c.Runs); n > 0 && c.Runs[n-1].Event == entry.Event {
			c.Runs[n-1].Count++
		} else {
			c.Runs = append(c.Runs, Run{Event: entry.Event, Count: 1})
		}
		if entry.Public {
			c.Observations = append(c.Observations, Observation{Index: i, Value: entry.Observe})
		}
	}
	return c
}

// Expand returns the original ordered event list.
func (c CompressedLog) Expand() []Event {
	var events []Event
	for _, run := range c.Runs {
		for i := 0; i < run.Count; i++ {
			events = append(events, run.Event)
		}
	}
	return events
}
