// Demo: two "databases" under continuous replication (hundreds of thousands
// of changes), while an Inspector patrols their rolling checksums. Midway we
// silently tamper a single field of one row in the target; the inspector
// catches the bucket checksum mismatch and pinpoints the primary key.
package main

import (
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"

	"checksumsync"
)

func main() {
	source := checksumsync.NewTable()
	target := checksumsync.NewTable()

	// Seed 100k rows on both ends.
	for i := 0; i < 100000; i++ {
		v := fmt.Sprintf("user-%d|balance=%d", i, i*7)
		source.Upsert(uint64(i), v)
		target.Upsert(uint64(i), v)
	}
	fmt.Printf("seeded %d rows on both ends\n", source.Len())

	// Replication stream: apply every change to source, then to target.
	var ops int64
	stopSync := make(chan struct{})
	go func() {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		for {
			select {
			case <-stopSync:
				return
			default:
			}
			pk := uint64(rng.Intn(300000))
			val := fmt.Sprintf("user-%d|balance=%d", pk, rng.Intn(1000000))
			source.Upsert(pk, val)
			target.Upsert(pk, val) // replicated
			atomic.AddInt64(&ops, 1)
		}
	}()

	// Inspector: patrol every 2ms, confirm across 2 consecutive cycles.
	detected := make(chan checksumsync.Mismatch, 1)
	in := checksumsync.NewInspector(source, target, 2*time.Millisecond)
	in.OnDetect = func(rep *checksumsync.Report) {
		for _, m := range rep.Mismatches {
			select {
			case detected <- m:
			default:
			}
		}
	}
	go in.Run()

	// Let the change stream flow.
	time.Sleep(300 * time.Millisecond)
	fmt.Printf("replication running: %d changes applied so far\n", atomic.LoadInt64(&ops))

	// Attack: silently modify one field of one row in the TARGET only.
	const victim = uint64(424242)
	source.Upsert(victim, "user-424242|balance=5000")
	target.Upsert(victim, "user-424242|balance=5000")
	time.Sleep(5 * time.Millisecond) // let it replicate & settle
	tamperedAt := time.Now()
	target.Upsert(victim, "user-424242|balance=0") // <-- the silent tamper
	fmt.Printf("[attack] silently modified pk=%d in target\n", victim)

	select {
	case m := <-detected:
		fmt.Printf("[detect] %s\n", m)
		fmt.Printf("[detect] latency=%s changes_streamed=%d patrol_cycles=%d\n",
			time.Since(tamperedAt).Round(time.Microsecond),
			atomic.LoadInt64(&ops), in.Cycles())
	case <-time.After(5 * time.Second):
		fmt.Println("[detect] FAILED: tampering not detected")
	}

	in.Stop()
	close(stopSync)
	fmt.Printf("total changes streamed: %d, total patrol cycles: %d\n",
		atomic.LoadInt64(&ops), in.Cycles())
}
