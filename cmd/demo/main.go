// Command demo runs the acceptance scenario end to end:
//
//   - a Master owns a 3000 QPS pool, split 1000/1000/1000 across workers A, B, C
//   - B is moderately busy (200 QPS), C is completely idle
//   - A bursts to 1500 QPS, exhausts its local 1000-token share, and its
//     asynchronous batched heartbeats pull idle quota away from C
//
// Usage:
//
//	go run ./cmd/demo                 # in-process transport (default)
//	go run ./cmd/demo -transport http # real TCP sockets via net/http
package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"quota"
)

func main() {
	transport := flag.String("transport", "direct", "transport: direct | http")
	addr := flag.String("addr", "127.0.0.1:18080", "listen address for -transport http")
	flag.Parse()

	master := quota.NewMaster(3000, 3, 10*time.Second)

	var newWorker func(id string) *quota.Worker
	switch *transport {
	case "http":
		ln, err := quota.ServeHTTP(master, *addr)
		if err != nil {
			log.Fatalf("serve: %v", err)
		}
		defer ln.Close()
		fmt.Printf("master listening on %s (HTTP/JSON over TCP)\n", ln.Addr())
		newWorker = func(id string) *quota.Worker {
			return quota.NewWorker(id, quota.DialMaster(*addr), 200, 0.2)
		}
	default:
		fmt.Println("master running in-process (direct transport)")
		newWorker = func(id string) *quota.Worker {
			return quota.NewWorker(id, quota.NewDirectHeartbeater(master), 200, 0.2)
		}
	}

	A, B, C := newWorker("A"), newWorker("B"), newWorker("C")
	for _, w := range []*quota.Worker{A, B, C} {
		if err := w.Register(); err != nil {
			log.Fatalf("register: %v", err)
		}
	}
	fmt.Println("\n== initial fair split (3000 QPS / 3 workers) ==")
	printSnapshot(master)

	// B is moderately busy; C stays completely idle.
	consume(B, 200)
	B.Flush() // report usage so the master sees B as active, C as idle

	// A bursts past its 1000-token share. Allow never blocks; when the
	// local balance hits zero it fails closed until the asynchronous
	// batched renewal lands, so the burst loop retries briefly.
	fmt.Println("\n== A bursts to 1500 QPS (local share is only 1000) ==")
	consumed := consume(A, 1500)
	A.Wait()
	fmt.Printf("A consumed %d tokens locally, renewals (RPC round-trips): %d\n",
		consumed, A.Stats().Heartbeats)

	fmt.Println("\n== leases after rebalancing ==")
	printSnapshot(master)
	fmt.Printf("master served %d heartbeats total for ~%d tokens moved\n",
		master.Stats(), consumed+200)
}

// consume drives w until n tokens are spent, retrying while a renewal is
// in flight (Allow fails closed on an empty balance).
func consume(w *quota.Worker, n int64) int64 {
	var consumed int64
	for consumed < n {
		if w.Allow() {
			consumed++
		} else {
			time.Sleep(time.Millisecond)
		}
	}
	w.Wait()
	return consumed
}

func printSnapshot(m *quota.Master) {
	var total int64
	for _, l := range m.Snapshot() {
		fmt.Printf("  worker %s: granted=%5d used=%5d\n", l.WorkerID, l.Granted, l.Used)
		total += l.Granted
	}
	fmt.Printf("  total granted=%d (pool cap 3000)\n", total)
}
