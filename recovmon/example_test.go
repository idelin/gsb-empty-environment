package recovmon_test

import (
	"fmt"

	"example.org/netsec/q0423/recovmon"
)

// Example shows a host driving a nested parser stream through the monitor
// and rendering the first recovery violation.
func Example() {
	raw := recovmon.Interval{Start: 0, End: 64}
	authenticated := []recovmon.Interval{{Start: 32, End: 48}}

	mon, err := recovmon.NewMonitor(raw, authenticated)
	if err != nil {
		panic(err)
	}

	events := []recovmon.Event{
		recovmon.Begin("message", 0, 64),
		recovmon.Begin("message.body", 32, 48),
		recovmon.Begin("message.body.mac", 40, 48),
		recovmon.Consume(0, 16),
		recovmon.Error(16, 20),
		recovmon.Recovery(16, 40),
	}

	for _, v := range mon.ApplyBatch(events) {
		fmt.Printf("violation %d: %s at byte %d\n", v.Index, v.Kind, v.FirstBadByte)
	}

	first := mon.FirstViolation()
	fmt.Printf("first bad byte: %d, authenticated hits: %v\n",
		first.FirstBadByte, first.AuthHits)

	// Output:
	// violation 5: authenticated_discard_overlap at byte 32
	// first bad byte: 32, authenticated hits: [{32 40}]
}
