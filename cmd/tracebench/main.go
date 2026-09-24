// tracebench runs several access traces (friendly and adversarial) through
// both the 2Q cache and the plain LRU baseline and prints comparable
// metrics: hits, hit rate, ghost hits, evictions, peak physical bytes and
// per-op latency. It deliberately includes traces where 2Q is NOT better
// than LRU.
package main

import (
	"fmt"
	"math/rand"
	"time"

	"paircache"
)

const capacity = 64 * 1024 // bytes

func key(i int) string { return fmt.Sprintf("k%d", i) }

// sizeOf gives variable-length entries, 64..255 bytes, deterministic per key.
func sizeOf(i int) int64 { return 64 + int64((i*37)%192) }

func loader(k string) ([]byte, error) {
	var n int
	if _, err := fmt.Sscanf(k, "k%d", &n); err != nil {
		return nil, err
	}
	return make([]byte, sizeOf(n)), nil
}

type cacheIface interface {
	Acquire(string) (*paircache.Lease, error)
	Stats() paircache.Stats
}

func scanTrace() (string, []string) {
	var keys []string
	for r := 0; r < 3; r++ {
		for i := 0; i < 1200; i++ { // ~3x capacity
			keys = append(keys, key(i))
		}
	}
	return "scan", keys
}

func hotScanTrace() (string, []string) {
	var keys []string
	add := func(start, n int) {
		for i := 0; i < n; i++ {
			keys = append(keys, key(start+i))
		}
	}
	add(0, 100)    // warm hot set (~25% of capacity)
	add(1000, 600) // push hot set out of A1in -> ghost records
	add(0, 100)    // repeat touch: 2Q promotes hot set into Am
	for r := 0; r < 3; r++ {
		add(2000+r*800, 800) // sequential scan, churns A1in only
		add(0, 100)          // hot re-touch: 2Q hits, LRU misses
	}
	return "hot+scan", keys
}

// loopTrace cycles over ~2x of what fits: the classic worst case for any
// recency policy. Neither 2Q nor LRU can do well here; included as an
// honest counterexample.
func loopTrace() (string, []string) {
	var keys []string
	for r := 0; r < 5; r++ {
		for i := 0; i < 850; i++ {
			keys = append(keys, key(i))
		}
	}
	return "loop2x", keys
}

func skewTrace() (string, []string) {
	r := rand.New(rand.NewSource(42))
	z := rand.NewZipf(r, 1.2, 10, 800)
	keys := make([]string, 0, 20000)
	for i := 0; i < 20000; i++ {
		keys = append(keys, key(int(z.Uint64())))
	}
	return "zipf", keys
}

// uniformTrace has no reuse locality at all: 2Q's bookkeeping only costs.
// Included as an honest counterexample.
func uniformTrace() (string, []string) {
	r := rand.New(rand.NewSource(42))
	keys := make([]string, 0, 20000)
	for i := 0; i < 20000; i++ {
		keys = append(keys, key(r.Intn(4000)))
	}
	return "uniform", keys
}

func run(keys []string, c cacheIface, leaseWindow int) (paircache.Stats, time.Duration) {
	var held []*paircache.Lease
	start := time.Now()
	for _, k := range keys {
		l, err := c.Acquire(k)
		if err != nil {
			continue
		}
		if leaseWindow > 0 {
			held = append(held, l)
			if len(held) > leaseWindow {
				held[0].Release()
				held = held[1:]
			}
		} else {
			l.Release()
		}
	}
	for _, l := range held {
		l.Release()
	}
	return c.Stats(), time.Since(start)
}

func report(name, pol string, ops int, st paircache.Stats, el time.Duration) {
	fmt.Printf("%-9s %-4s %7d %7d %6.1f%% %6d %7d %8d %8d\n",
		name, pol, ops, st.Hits, 100*float64(st.Hits)/float64(ops),
		st.GhostHits, st.Evictions, st.PeakPhysicalBytes,
		el.Nanoseconds()/int64(ops))
}

func main() {
	traces := []func() (string, []string){scanTrace, hotScanTrace, loopTrace, skewTrace, uniformTrace}
	fmt.Printf("capacity = %d bytes, variable-size entries 64..255 B\n\n", capacity)
	fmt.Printf("%-9s %-4s %7s %7s %7s %6s %7s %8s %8s\n",
		"trace", "pol", "ops", "hits", "hit%", "ghost", "evict", "peakB", "ns/op")
	for _, gen := range traces {
		name, keys := gen()
		for _, pol := range []string{"2q", "lru"} {
			opt := paircache.Options{CapacityBytes: capacity, Loader: loader}
			var c cacheIface
			if pol == "2q" {
				c = paircache.NewTwoQ(opt)
			} else {
				c = paircache.NewLRU(opt)
			}
			st, el := run(keys, c, 0)
			report(name, pol, len(keys), st, el)
		}
	}

	// Lease demo: hold a sliding window of 16 leases; evicted-but-leased
	// entries keep physical occupancy above the logical capacity.
	name, keys := uniformTrace()
	c := paircache.NewTwoQ(paircache.Options{CapacityBytes: capacity, Loader: loader})
	st, el := run(keys, c, 16)
	fmt.Printf("\n%s 2q with 16-lease window: peak physical = %d bytes (logical capacity %d), %d ns/op\n",
		name, st.PeakPhysicalBytes, capacity, el.Nanoseconds()/int64(len(keys)))
}
