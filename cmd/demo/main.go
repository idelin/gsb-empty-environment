// Command demo exercises the hand-rolled protobuf wire codec, the
// reflection validation engine, and the lock-free MPMC queue.
package main

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"pbwire/queue"
	"pbwire/wire"
)

// --- demo schema (hand-written, zero codegen, zero dependencies) ---

type Geo struct {
	Lat int64 `pb:"1,zigzag"`
	Lng int64 `pb:"2,zigzag"`
}

type Address struct {
	City string `pb:"1,string"`
	Zip  uint32 `pb:"2,fixed32"`
	Geo  *Geo   `pb:"3,message"`
}

type Person struct {
	ID      uint64   `pb:"1,varint"`
	Name    string   `pb:"2,string"`
	Active  bool     `pb:"3,varint"`
	Balance int64    `pb:"4,zigzag"`
	Stamp   uint64   `pb:"5,fixed64"`
	Addr    *Address `pb:"6,message"`
	Scores  []int64  `pb:"7,packed"`
	Tags    []string `pb:"8,string"`
}

func main() {
	fmt.Println("=== 1. Protobuf wire-format: 3-level nested message ===")
	in := &Person{
		ID:      1<<63 + 42,
		Name:    "Ada",
		Active:  true,
		Balance: -123456789,
		Stamp:   0xdeadbeefcafebabe,
		Addr: &Address{
			City: "Shanghai",
			Zip:  200001,
			Geo:  &Geo{Lat: 31120000, Lng: -121470000},
		},
		Scores: []int64{0, -1, 1, -2, 2, 1<<62 - 1},
		Tags:   []string{"go", "wire", "lock-free"},
	}
	data, err := wire.Marshal(in)
	must(err)
	fmt.Printf("encoded %d bytes: % x\n", len(data), data)

	var out Person
	must(wire.Unmarshal(data, &out))
	fmt.Printf("decoded: ID=%d Name=%q Active=%v Balance=%d Stamp=%#x\n",
		out.ID, out.Name, out.Active, out.Balance, out.Stamp)
	fmt.Printf("nested:  City=%q Zip=%d Geo=(%d,%d)\n",
		out.Addr.City, out.Addr.Zip, out.Addr.Geo.Lat, out.Addr.Geo.Lng)
	fmt.Printf("packed:  Scores=%v Tags=%v\n", out.Scores, out.Tags)

	fmt.Println("\n=== 2. Safe-bounds guard: truncated field ===")
	cut := data[:len(data)-3] // chop the tail off the last field
	var victim Person
	err = wire.Unmarshal(cut, &victim)
	fmt.Printf("truncated input (%d/%d bytes) -> err: %v\n", len(cut), len(data), err)
	fmt.Printf("wire.IsTruncated(err) = %v\n", wire.IsTruncated(err))

	fmt.Println("\n=== 3. Lock-free MPMC queue (CAS, no mutex) ===")
	const (
		producers    = 8
		consumers    = 8
		itemsPerProd = 250_000
		total        = producers * itemsPerProd
	)
	q := queue.New[int]()
	var produced, consumed atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < itemsPerProd; i++ {
				q.Enqueue(base + i)
				produced.Add(1)
			}
		}(p * itemsPerProd)
	}
	var sum atomic.Int64
	for c := 0; c < consumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				n := consumed.Load()
				if n >= total {
					return
				}
				v, ok := q.Dequeue()
				if !ok {
					runtime.Gosched()
					continue
				}
				sum.Add(int64(v))
				consumed.Add(1)
			}
		}()
	}
	wg.Wait()
	// Drain any stragglers so the leak check is exact.
	for {
		v, ok := q.Dequeue()
		if !ok {
			break
		}
		sum.Add(int64(v))
		consumed.Add(1)
	}
	el := time.Since(start)
	want := int64(total-1) * int64(total) / 2
	fmt.Printf("moved %d items in %v (%.1f M ops/s, %d producers x %d consumers)\n",
		consumed.Load(), el, float64(consumed.Load())/el.Seconds()/1e6, producers, consumers)
	fmt.Printf("no-leak check: sum=%d want=%d match=%v, queue empty=%v len=%d\n",
		sum.Load(), want, sum.Load() == want, q.Empty(), q.Len())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
