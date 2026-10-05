// Command demo replays the production fault: the old primary is paused, its
// lease expires, a new primary commits a write, and the resumed old primary
// still flushes its queued write - which fencing rejects.
package main

import (
	"fmt"

	"leasesim"
)

func main() {
	s := leasesim.New()

	// Old primary A certifies at epoch 1 with lease [1,11).
	s.Campaign("A", 1, 10)
	s.Tick(1)
	s.Flush()
	s.Tick(1)
	s.Flush()

	// A is paused with a write still queued locally; its lease expires.
	s.Pause("A")
	s.Write("A", "w1", "v1")
	s.Tick(10)

	// New primary B certifies at epoch 2 and commits its write.
	s.Campaign("B", 2, 10)
	s.Tick(1)
	s.Flush()
	s.Tick(1)
	s.Flush()
	s.Write("B", "w2", "v2")
	s.Tick(1)
	s.Flush()

	// A resumes and blindly flushes the queued stale write.
	s.Resume("A")
	s.Tick(1)
	s.Flush()

	for _, ev := range s.Events() {
		fmt.Println(ev)
	}
	fmt.Println("committed writes:", s.Committed())
}
