package lockfree

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitNoLeak 在给定期限内等待 goroutine 数量回落到 baseline，否则判定泄漏。
func waitNoLeak(t *testing.T, baseline int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: baseline=%d now=%d", baseline, runtime.NumGoroutine())
}

func TestQueueBasicFIFO(t *testing.T) {
	q := NewQueue[int]()
	if !q.Empty() {
		t.Fatal("new queue must be empty")
	}
	if _, ok := q.Dequeue(); ok {
		t.Fatal("dequeue on empty queue must fail")
	}
	for i := 0; i < 100; i++ {
		q.Enqueue(i)
	}
	if q.Len() != 100 {
		t.Fatalf("Len=%d, want 100", q.Len())
	}
	for i := 0; i < 100; i++ {
		v, ok := q.Dequeue()
		if !ok || v != i {
			t.Fatalf("Dequeue=%v,%v want %v,true (FIFO violated)", v, ok, i)
		}
	}
	if _, ok := q.Dequeue(); ok {
		t.Fatal("queue must be drained")
	}
	if q.Len() != 0 || !q.Empty() {
		t.Fatalf("queue not empty after drain: Len=%d", q.Len())
	}
}

func TestQueueInterleaved(t *testing.T) {
	q := NewQueue[string]()
	q.Enqueue("a")
	if v, _ := q.Dequeue(); v != "a" {
		t.Fatalf("got %q", v)
	}
	if _, ok := q.Dequeue(); ok {
		t.Fatal("expected empty")
	}
	q.Enqueue("b")
	q.Enqueue("c")
	if v, _ := q.Dequeue(); v != "b" {
		t.Fatalf("got %q", v)
	}
	q.Enqueue("d")
	for _, want := range []string{"c", "d"} {
		if v, ok := q.Dequeue(); !ok || v != want {
			t.Fatalf("got %q,%v want %q", v, ok, want)
		}
	}
	if !q.Empty() {
		t.Fatal("queue should be empty")
	}
}

// TestQueueMPMC 多生产者多消费者：每个元素必须被恰好消费一次，无丢失无重复。
func TestQueueMPMC(t *testing.T) {
	const producers = 8
	const perProducer = 5000
	const total = producers * perProducer

	q := NewQueue[int]()
	seen := make([]atomic.Int32, total)
	var consumed atomic.Int64
	var sum atomic.Int64

	var prod sync.WaitGroup
	for p := 0; p < producers; p++ {
		prod.Add(1)
		go func(p int) {
			defer prod.Done()
			for i := 0; i < perProducer; i++ {
				q.Enqueue(p*perProducer + i)
			}
		}(p)
	}

	var cons sync.WaitGroup
	for c := 0; c < 4; c++ {
		cons.Add(1)
		go func() {
			defer cons.Done()
			for consumed.Load() < total {
				v, ok := q.Dequeue()
				if !ok {
					runtime.Gosched()
					continue
				}
				seen[v].Add(1)
				sum.Add(int64(v))
				consumed.Add(1)
			}
		}()
	}
	prod.Wait()
	cons.Wait()

	for i := range seen {
		if n := seen[i].Load(); n != 1 {
			t.Fatalf("item %d consumed %d times, want exactly 1", i, n)
		}
	}
	wantSum := int64(total-1) * int64(total) / 2
	if sum.Load() != wantSum {
		t.Fatalf("sum=%d want %d", sum.Load(), wantSum)
	}
	if q.Len() != 0 {
		t.Fatalf("queue not drained, Len=%d", q.Len())
	}
}

// TestQueueEmptyDequeueConcurrent 空队列上的并发 Dequeue 必须全部返回 false 且不挂死。
func TestQueueEmptyDequeueConcurrent(t *testing.T) {
	q := NewQueue[int]()
	var wg sync.WaitGroup
	var falseCount atomic.Int64
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				if _, ok := q.Dequeue(); ok {
					t.Error("dequeue on empty queue returned ok")
					return
				}
				falseCount.Add(1)
			}
		}()
	}
	wg.Wait()
	if falseCount.Load() != 16*1000 {
		t.Fatalf("falseCount=%d", falseCount.Load())
	}
	if q.Len() != 0 {
		t.Fatalf("Len=%d, want 0", q.Len())
	}
}

// TestQueueLenConsistency 生产者与消费者都静止后，Len 必须精确归零。
func TestQueueLenConsistency(t *testing.T) {
	q := NewQueue[int]()
	const n = 10000
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			q.Enqueue(i)
		}
	}()
	go func() {
		defer wg.Done()
		got := 0
		for got < n {
			if _, ok := q.Dequeue(); ok {
				got++
			}
		}
	}()
	wg.Wait()
	if q.Len() != 0 {
		t.Fatalf("Len=%d after quiescence, want 0", q.Len())
	}
	if !q.Empty() {
		t.Fatal("queue must be empty after quiescence")
	}
}

// TestQueueNoGoroutineLeak 队列本身不启动任何 goroutine；严格断言无泄漏。
func TestQueueNoGoroutineLeak(t *testing.T) {
	baseline := runtime.NumGoroutine()
	q := NewQueue[int]()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				q.Enqueue(j)
				q.Dequeue()
			}
		}(i)
	}
	wg.Wait()
	waitNoLeak(t, baseline)
}

func BenchmarkQueueMPMC(b *testing.B) {
	q := NewQueue[int]()
	b.Run("Enqueue", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				q.Enqueue(1)
			}
		})
	})
	b.Run("Dequeue", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				q.Dequeue()
			}
		})
	})
}
