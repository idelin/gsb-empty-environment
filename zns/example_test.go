package zns_test

import (
	"fmt"

	"prescreen.local/ps-3e28dd0313/zns/device"
	"prescreen.local/ps-3e28dd0313/zns/engine"
)

// ExampleEndToEnd wires the emulated ZNS device to the host GC engine, writes
// and overwrites logical blocks, runs garbage collection and prints the WAF.
func Example() {
	dev, err := device.New(device.Config{
		NumZones:       6,
		ZoneSize:       8,
		ZoneCapacity:   6, // ZoneCapacity < ZoneSize: only 6 blocks are writable
		MaxOpenZones:   2,
		MaxActiveZones: 4,
	})
	if err != nil {
		panic(err)
	}

	eng, err := engine.New(engine.Config{Device: dev, Mode: engine.ModeTiered})
	if err != nil {
		panic(err)
	}

	// Cold blocks are written once; hot blocks keep being overwritten.
	for lba := int64(100); lba < 108; lba++ {
		if _, err := eng.WriteWithGC(lba, []byte("cold")); err != nil {
			panic(err)
		}
	}
	for round := 0; round < 6; round++ {
		for lba := int64(0); lba < 4; lba++ {
			if _, err := eng.WriteWithGC(lba, []byte(fmt.Sprintf("hot-r%d", round))); err != nil {
				panic(err)
			}
		}
	}

	// A regular write that ignores the write pointer is rejected by the device.
	if err := dev.Write(1, 1, nil); err != nil {
		fmt.Println("device rejects misaligned write:", err != nil)
	}

	stats := eng.Stats()
	fmt.Printf("host=%d flash=%d gcRuns=%d WAF=%.3f\n",
		stats.HostWrites, stats.FlashWrites, stats.GCRuns, stats.WAF())
	fmt.Println("invariants ok:", eng.CheckInvariants() == nil)

	// Output:
	// device rejects misaligned write: true
	// host=32 flash=32 gcRuns=2 WAF=1.000
	// invariants ok: true
}
