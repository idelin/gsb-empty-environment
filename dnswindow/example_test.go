package dnswindow_test

import (
	"fmt"
	"time"

	"example.org/netsec/uid1260/dnswindow"
)

func Example() {
	manager, err := dnswindow.NewManager(time.Minute)
	if err != nil {
		panic(err)
	}

	base := time.Unix(0, 0).UTC()
	events := []dnswindow.Event{
		{ID: "1", Client: "10.0.0.1", RegisteredDomain: "evil.example", QueriedName: "aaaa.evil.example", NXDOMAIN: true, Time: base.Add(5 * time.Second)},
		{ID: "2", Client: "10.0.0.1", RegisteredDomain: "evil.example", QueriedName: "bbbb.evil.example", NXDOMAIN: true, Time: base.Add(10 * time.Second)},
		{ID: "3", Client: "10.0.0.1", RegisteredDomain: "evil.example", QueriedName: "ok.evil.example", NXDOMAIN: false, Time: base.Add(15 * time.Second)},
	}
	for _, event := range events {
		if _, err := manager.Submit(event); err != nil {
			panic(err)
		}
	}

	// Same event ID replayed out of order must not be counted again.
	outcome, err := manager.Submit(events[0])
	if err != nil {
		panic(err)
	}

	// Correct event 3 while its window is still open.
	_, err = manager.Submit(dnswindow.Event{
		ID: "3", Client: "10.0.0.1", RegisteredDomain: "evil.example",
		QueriedName: "ok2.evil.example", NXDOMAIN: true, Time: base.Add(20 * time.Second),
	})
	if err != nil {
		panic(err)
	}

	results := manager.Advance(base.Add(time.Minute))
	group := results[0].Groups[0]
	fmt.Printf("replay=%s events=%d nx=%d ratio=%.2f distinctLabels=%d entropy=%.3f\n",
		outcome, group.TotalEvents, group.NXDOMAINEvents,
		group.NXDOMAINRatio, group.DistinctLabels, group.ByteEntropy)
	// Output: replay=duplicate events=3 nx=3 ratio=1.00 distinctLabels=5 entropy=3.424
}
