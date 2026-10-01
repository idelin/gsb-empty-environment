// Command demo shows hand-built protobuf wire encoding, reflection-based
// decoding with required-field validation, truncation safety, and the
// throughput of the lock-free MPMC queue versus a mutex-guarded queue.
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

// Order is a nested schema decoded purely by reflection.
type Order struct {
	ID      uint64   `protobuf:"1,varint,req"`
	Buyer   string   `protobuf:"2,req"`
	Delta   int64    `protobuf:"3,zigzag"`
	Tags    []string `protobuf:"4"`
	Amounts []int32  `protobuf:"5,zigzag,packed"`
	Price   float64  `protobuf:"6,fixed64"`
	Addr    *Address `protobuf:"7,req"`
}

type Address struct {
	City string `protobuf:"1,req"`
	Zip  uint32 `protobuf:"2"`
}

func encodeOrder() []byte {
	var e wire.Encoder
	e.Tag(1, wire.WTVarint)
	e.Varint(9001)
	e.Tag(2, wire.WTBytes)
	e.AppendBytes([]byte("delin"))
	e.Tag(3, wire.WTVarint)
	e.ZigZag(-7)
	for _, tag := range []string{"vip", "rush"} {
		e.Tag(4, wire.WTBytes)
		e.AppendBytes([]byte(tag))
	}
	var packed wire.Encoder
	for _, a := range []int32{100, -20, 35} {
		packed.ZigZag(int64(a))
	}
	e.Tag(5, wire.WTBytes)
	e.AppendBytes(packed.Bytes())
	e.Tag(6, wire.WTFixed64)
	e.Fixed64(0x4058ff5c28f5c28f) // 99.99
	var a wire.Encoder
	a.Tag(1, wire.WTBytes)
	a.AppendBytes([]byte("shanghai"))
	a.Tag(2, wire.WTVarint)
	a.Varint(200000)
	e.Tag(7, wire.WTBytes)
	e.AppendBytes(a.Bytes())
	return e.Bytes()
}

func main() {
	fmt.Println("== protobuf wire-format demo ==")
	data := encodeOrder()
	fmt.Printf("encoded %d bytes: %x\n", len(data), data)

	var o Order
	if err := wire.Unmarshal(data, &o); err != nil {
		fmt.Println("decode error:", err)
		return
	}
	fmt.Printf("decoded: ID=%d Buyer=%s Delta=%d Tags=%v Amounts=%v Price=%.2f Addr=%+v\n",
		o.ID, o.Buyer, o.Delta, o.Tags, o.Amounts, o.Price, *o.Addr)

	// Truncation safety: cut the stream mid-field.
	var broken Order
	err := wire.Unmarshal(data[:len(data)-3], &broken)
	fmt.Printf("truncated stream -> %v\n", err)

	// Required-field validation: stream without Buyer/Addr.
	var partial wire.Encoder
	partial.Tag(1, wire.WTVarint)
	partial.Varint(1)
	var o2 Order
	fmt.Printf("missing required -> %v\n", wire.Unmarshal(partial.Bytes(), &o2))

	fmt.Println("\n== lock-free MPMC queue demo ==")
	const producers, perProducer = 8, 100000
	benchLockFree(producers, perProducer)
	benchMutex(producers, perProducer)
}

func benchLockFree(producers, perProducer int) {
	q := queue.NewBounded(1024)
	total := producers * perProducer
	var consumed int64
	var cwg, pwg sync.WaitGroup
	done := make(chan struct{})
	start := time.Now()
	for c := 0; c < 4; c++ {
		cwg.Add(1)
		go func() {
			defer cwg.Done()
			for {
				if _, ok := q.Dequeue(); ok {
					atomic.AddInt64(&consumed, 1)
					continue
				}
				select {
				case <-done:
					return
				default:
					runtime.Gosched()
				}
			}
		}()
	}
	for p := 0; p < producers; p++ {
		pwg.Add(1)
		go func(base int) {
			defer pwg.Done()
			for i := 0; i < perProducer; i++ {
				for !q.Enqueue(base + i) {
					runtime.Gosched()
				}
			}
		}(p * perProducer)
	}
	pwg.Wait()
	for atomic.LoadInt64(&consumed) != int64(total) {
		runtime.Gosched()
	}
	close(done)
	cwg.Wait()
	el := time.Since(start)
	fmt.Printf("lock-free queue: %d ops in %v (%.0f ops/sec)\n",
		2*total, el, float64(2*total)/el.Seconds())
}

type mutexQueue struct {
	mu    sync.Mutex
	items []interface{}
}

func (m *mutexQueue) Enqueue(v interface{}) {
	m.mu.Lock()
	m.items = append(m.items, v)
	m.mu.Unlock()
}

func (m *mutexQueue) Dequeue() (interface{}, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.items) == 0 {
		return nil, false
	}
	v := m.items[0]
	m.items = m.items[1:]
	return v, true
}

func benchMutex(producers, perProducer int) {
	q := &mutexQueue{}
	total := producers * perProducer
	var consumed int64
	var cwg, pwg sync.WaitGroup
	done := make(chan struct{})
	start := time.Now()
	for c := 0; c < 4; c++ {
		cwg.Add(1)
		go func() {
			defer cwg.Done()
			for {
				if _, ok := q.Dequeue(); ok {
					atomic.AddInt64(&consumed, 1)
					continue
				}
				select {
				case <-done:
					return
				default:
					runtime.Gosched()
				}
			}
		}()
	}
	for p := 0; p < producers; p++ {
		pwg.Add(1)
		go func(base int) {
			defer pwg.Done()
			for i := 0; i < perProducer; i++ {
				q.Enqueue(base + i)
			}
		}(p * perProducer)
	}
	pwg.Wait()
	for atomic.LoadInt64(&consumed) != int64(total) {
		runtime.Gosched()
	}
	close(done)
	cwg.Wait()
	el := time.Since(start)
	fmt.Printf("mutex queue:    %d ops in %v (%.0f ops/sec)\n",
		2*total, el, float64(2*total)/el.Seconds())
}
