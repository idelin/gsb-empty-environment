package reservoir_test

import (
	"fmt"

	"example.org/netsec/uid1246/reservoir"
)

func Example() {
	cfg := reservoir.Config{
		Capacity: 3,
		MinQuota: map[string]int{"brute-force": 1, "exfil": 1},
	}

	// Each probe shard gets the shared window key.
	shard, err := reservoir.New(cfg, []byte("window-shared-secret"))
	if err != nil {
		panic(err)
	}

	shard.Add(reservoir.Event{Class: "brute-force", ID: "evt-1", Payload: []byte("...")})
	shard.Add(reservoir.Event{Class: "brute-force", ID: "evt-1", Payload: []byte("retransmit")})
	shard.Add(reservoir.Event{Class: "exfil", ID: "evt-2", Payload: []byte("...")})
	shard.Add(reservoir.Event{Class: "noise", ID: "evt-3", Payload: []byte("...")})

	for _, record := range shard.Snapshot() {
		fmt.Printf("%s %s\n", record.Event.Class, record.Event.ID)
	}

	stats := shard.Stats()
	fmt.Printf("seen=%d duplicates=%d retained=%d\n",
		stats.EventsSeen, stats.Duplicates, len(shard.Snapshot()))

	// Rotate the secret to open a fresh, uncorrelated sampling window.
	shard.RotateKey([]byte("next-window-secret"))
	fmt.Printf("window=%d retained=%d\n", shard.WindowID(), len(shard.Snapshot()))
	// Unordered output:
	// brute-force evt-1
	// exfil evt-2
	// noise evt-3
	// seen=4 duplicates=1 retained=3
	// window=1 retained=0
}
