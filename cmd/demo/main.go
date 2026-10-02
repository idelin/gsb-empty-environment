// Command demo records the reference trace (50 packet arrivals, 3
// timeouts) and then demonstrates side-effect-free time travel: it jumps
// back to the 35th event and proves the reconstructed state is identical
// to the historical snapshot.
package main

import (
	"bytes"
	"fmt"

	"replaykernel"
)

func main() {
	// 1. Record: run the system over the scripted external inputs.
	recorded := kernel.RecordTrace(kernel.TraceSeed, kernel.BuildTrace())
	log := recorded.Log()
	stats := recorded.Stats()

	fmt.Println("=== deterministic event replay debugging kernel ===")
	fmt.Printf("seed: %d\n", log.Seed)
	fmt.Printf("recorded %d events: %d packet arrivals, %d timeouts, %d clock interrupts\n",
		log.Len(),
		log.CountKind(kernel.KindPacketArrival),
		log.CountKind(kernel.KindTimeout),
		log.CountKind(kernel.KindClockInterrupt))
	fmt.Printf("final stats: %+v\n\n", stats)

	fmt.Println("event log (excerpt):")
	for i := 0; i < log.Len(); i++ {
		if i < 6 || i >= log.Len()-3 || log.Events[i].Kind == kernel.KindTimeout {
			fmt.Printf("  [%02d] %s\n", i, log.Events[i])
		} else if i == 6 {
			fmt.Println("  ...")
		}
	}

	// 2. Time travel: jump back to the 35th event (1-based).
	const eventNumber = 35
	index := eventNumber - 1
	dbg := kernel.NewDebugger(log)
	replayed, err := dbg.Goto(index)
	if err != nil {
		panic(err)
	}

	historical := recorded.SnapshotAt(index)
	reconstructed := replayed.SnapshotAt(index)
	fmt.Printf("\n=== time travel to event %d ===\n", eventNumber)
	fmt.Printf("event:                %s\n", log.Events[index])
	fmt.Printf("historical snapshot:  %x\n", historical.Hash)
	fmt.Printf("reconstructed state:  %x\n", reconstructed.Hash)
	fmt.Printf("hash identical:       %v\n", historical.Hash == reconstructed.Hash)
	fmt.Printf("bytes identical:      %v (%d bytes compared)\n",
		bytes.Equal(historical.Bytes, reconstructed.Bytes), len(historical.Bytes))

	// 3. Sweep: verify every single checkpoint the same way.
	mismatches := 0
	for i := 0; i < log.Len(); i++ {
		rk, err := dbg.Goto(i)
		if err != nil || rk.SnapshotAt(i).Hash != recorded.SnapshotAt(i).Hash {
			mismatches++
		}
	}
	fmt.Printf("\nfull sweep: %d/%d checkpoints reproduced byte-for-byte, %d mismatches\n",
		log.Len()-mismatches, log.Len(), mismatches)
}
