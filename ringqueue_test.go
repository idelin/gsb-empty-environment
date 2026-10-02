package coroutine

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestRingQueueFIFO(t *testing.T) {
	q := NewRingQueue(4)
	for i := 1; i <= 4; i++ {
		if !q.Enqueue(i) {
			t.Fatalf("Enqueue(%d) 失败", i)
		}
	}
	for i := 1; i <= 4; i++ {
		v, ok := q.Dequeue()
		if !ok || v != i {
			t.Fatalf("Dequeue() = (%v, %v), want (%d, true)", v, ok, i)
		}
	}
}

func TestRingQueueFullAndEmpty(t *testing.T) {
	q := NewRingQueue(3) // 容量向上取整为 4
	if q.Cap() != 4 {
		t.Fatalf("Cap() = %d, want 4", q.Cap())
	}
	if _, ok := q.Dequeue(); ok {
		t.Fatal("空队列 Dequeue 应返回 false")
	}
	for i := 0; i < 4; i++ {
		q.Enqueue(i)
	}
	if q.Enqueue(99) {
		t.Fatal("队列已满时 Enqueue 应返回 false")
	}
	for i := 0; i < 4; i++ {
		if _, ok := q.Dequeue(); !ok {
			t.Fatalf("第 %d 次 Dequeue 应成功", i)
		}
	}
	if _, ok := q.Dequeue(); ok {
		t.Fatal("耗尽后 Dequeue 应返回 false")
	}
}

// 环绕复用槽位：反复入队出队，序列号机制应正确回收槽位。
func TestRingQueueWrapAround(t *testing.T) {
	q := NewRingQueue(2)
	const n = 10000
	for i := 0; i < n; i++ {
		if !q.Enqueue(i) {
			t.Fatalf("第 %d 次 Enqueue 失败", i)
		}
		v, ok := q.Dequeue()
		if !ok || v != i {
			t.Fatalf("第 %d 轮 Dequeue() = (%v, %v)", i, v, ok)
		}
	}
}

// 多生产者多消费者并发压测（配合 go test -race）：
// 所有元素必须恰好被消费一次。
func TestRingQueueConcurrentMPMC(t *testing.T) {
	const producers, perProducer = 4, 2000
	const consumers = 3
	total := producers * perProducer

	q := NewRingQueue(64)
	var consumed sync.Map
	var count atomic.Int64

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				v := base + i
				for !q.Enqueue(v) { // 满则自旋重试
				}
			}
		}(p * perProducer)
	}
	for c := 0; c < consumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if count.Load() == int64(total) {
					return
				}
				v, ok := q.Dequeue()
				if !ok {
					continue
				}
				if _, dup := consumed.LoadOrStore(v, true); dup {
					t.Errorf("重复消费: %v", v)
				}
				count.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := count.Load(); n != int64(total) {
		t.Fatalf("消费总数 = %d, want %d", n, total)
	}
}
