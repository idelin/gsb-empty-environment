// simdemo runs a small packet/queue/link scenario, checkpoints it at
// tick 40, restores the checkpoint, and verifies that the restored
// trajectory matches a continuous run.
package main

import (
	"bytes"
	"fmt"
	"os"
	"reflect"

	"example.com/desim"
)

func build() *desim.World {
	return desim.NewWorld(
		&desim.Source{Interval: 3, Size: 100, Remaining: 12}, // 12 packets, one every 3 ticks
		&desim.Queue{Cap: 4},            // 4-packet FIFO, then drop
		&desim.Link{Rate: 10, Delay: 2}, // 10 B/tick, 2-tick propagation
	)
}

func main() {
	const checkpointTick, endTick = 40, 200

	// Continuous reference run.
	full := build()
	full.Sched.RunUntil(endTick)

	// Run to the checkpoint tick and save.
	w := build()
	w.Sched.RunUntil(checkpointTick)
	var buf bytes.Buffer
	if err := w.Save(&buf); err != nil {
		panic(err)
	}
	fmt.Printf("checkpoint @tick=%d: %d bytes, pending events=%d, queue=%d packet(s)\n",
		w.Sched.Now(), buf.Len(), w.Sched.Pending(), len(w.Q.Buf))

	// Restore and continue to the end.
	restored, err := desim.LoadWorld(&buf)
	if err != nil {
		panic(err)
	}
	restored.Sched.RunUntil(endTick)

	fmt.Println("\nrestored trajectory (packet deliveries):")
	fmt.Println(" tick  packet")
	for _, d := range restored.Sn.Log {
		fmt.Printf(" %4d  #%d\n", d.Tick, d.ID)
	}
	fmt.Printf("\ndelivered=%d dropped=%d (queue cap=%d)\n",
		len(restored.Sn.Log), restored.Q.Dropped, restored.Q.Cap)

	same := reflect.DeepEqual(restored.Sn.Log, full.Sn.Log) &&
		restored.Q.Dropped == full.Q.Dropped
	fmt.Printf("restored trajectory identical to continuous run: %v\n", same)
	if !same {
		os.Exit(1)
	}
}
