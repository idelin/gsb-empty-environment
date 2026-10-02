// Command replaydemo records a deterministic trace (50 packet arrivals,
// 3 retransmission timeouts), then time-travels to event #35 and verifies
// the replayed state is byte-identical to the historical snapshot.
package main

import (
	"bytes"
	"fmt"
	"os"

	"replaydemo/replay"
)

const (
	seed        = 2
	envSeed     = 0xDEADBEEF
	packetCount = 50
	targetEvent = 35
)

func main() {
	// Phase 1: live recording.
	kernel, snaps := replay.Record(seed, envSeed, packetCount)
	log := kernel.Log()
	fmt.Printf("recorded %d events: %d packet arrivals, %d timeouts, %d seed\n",
		log.Len(),
		log.Count(replay.EventPacketArrival),
		log.Count(replay.EventTimeout),
		log.Count(replay.EventSeed))

	hist := snaps[targetEvent-1] // state after applying the 35th event
	fmt.Printf("historical snapshot @ event %d: tick=%d hash=%x\n",
		targetEvent, hist.Tick, hist.Hash[:8])

	// Phase 2: side-effect-free time travel to event #35.
	replayed, err := replay.ReplayTo(log, targetEvent)
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay failed:", err)
		os.Exit(1)
	}
	now := replayed.Snapshot()
	fmt.Printf("replayed   snapshot @ event %d: tick=%d hash=%x\n",
		targetEvent, now.Tick, now.Hash[:8])

	if now.Hash != hist.Hash || !bytes.Equal(now.Bytes, hist.Bytes) {
		fmt.Fprintln(os.Stderr, "MISMATCH: replayed state differs from history")
		os.Exit(1)
	}
	fmt.Println("MATCH: replayed state is byte-identical to the historical snapshot")

	// Phase 3: prove time travel had no side effects on the live kernel.
	finalBefore := kernel.Snapshot()
	if err := kernel.TravelTo(targetEvent); err != nil {
		fmt.Fprintln(os.Stderr, "travel failed:", err)
		os.Exit(1)
	}
	travelled := kernel.Snapshot()
	fmt.Printf("time-travelled live kernel to event %d (hash=%x)\n",
		targetEvent, travelled.Hash[:8])
	if err := kernel.TravelTo(log.Len()); err != nil {
		fmt.Fprintln(os.Stderr, "travel failed:", err)
		os.Exit(1)
	}
	restored := kernel.Snapshot()
	if restored.Hash != finalBefore.Hash {
		fmt.Fprintln(os.Stderr, "MISMATCH: time travel mutated the live run")
		os.Exit(1)
	}
	fmt.Printf("returned to present (event %d): live run untouched, hash=%x\n",
		log.Len(), restored.Hash[:8])
}
