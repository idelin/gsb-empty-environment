package gatetable

// Copy describes one egress copy in the periodic plan.  Start is the phase
// in [0,Period); the actual copy interval is [Start,Start+Duration), and the
// same copy repeats every Period ticks.
type Copy struct {
	Frame    int
	Port     int
	Offset   int
	Start    int
	Duration int
	End      int
}

// FramePlan is the compiled plan for one frame type.
type FramePlan struct {
	Frame   int
	Name    string
	Offsets []int // parallel to Problem.Frames[Frame].Ports
	Latency int   // worst-case completion latency of this frame type
	Copies  []Copy
}

// MaskSpan is one physical gate-table entry representing a maximal circular
// run of an unchanged port mask.
type MaskSpan struct {
	Start  int   // first tick of the run
	Length int   // number of ticks; ticks are (Start+i) mod Period
	Mask   uint8 // busy-port bitmap
}

// Result is a compiled, periodic gate-table plan.
type Result struct {
	Period int

	// Frames is parallel to the input frames.
	Frames []FramePlan

	// Offsets[f] is parallel to Problem.Frames[f].Ports.
	Offsets [][]int

	// Latency is the primary objective: the maximum per-frame completion
	// latency.
	Latency int
	// FrameLatencies[f] is max{o(f,p)+duration(f,p)} over p.
	FrameLatencies []int

	// SlotsUsed is the peak number of concurrently held shared slots.
	SlotsUsed int
	// TableEntries is the number of circular mask runs.
	TableEntries int
	// PortTableEntries[p] is the number of circular runs in port p's own
	// one-bit bitmap.  It is diagnostic; the budget uses TableEntries.
	PortTableEntries []int
	// Masks[t] is the busy-port mask in tick t.
	Masks []uint8
	// Spans is Masks compressed circularly, one element per physical entry.
	Spans []MaskSpan

	problem Problem
}
