package coroutine

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitTrue 在超时内轮询等待条件成立，失败则 t.Fatal。
func waitTrue(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", what)
}

// 验收场景：协程 P 向容量为 2 的 Channel 写入 3 个数据；
// 写入第 3 个时 P 自动挂起；消费者 C 读取一个数据后，
// P 立即被唤醒完成写入。
func TestAcceptanceScenario(t *testing.T) {
	ch := New(2)

	var sent3 atomic.Bool // 第 3 次 Send 是否已返回
	done := make(chan struct{})
	go func() { // 生产者协程 P
		defer close(done)
		ch.Send(1)
		ch.Send(2)
		ch.Send(3) // 缓冲已满，应在此挂起
		sent3.Store(true)
	}()

	// 给 P 充足时间完成前两次写入并阻塞在第 3 次。
	time.Sleep(100 * time.Millisecond)
	if sent3.Load() {
		t.Fatal("P 的第 3 次写入应当在通道满时挂起，但却已完成")
	}

	// 消费者 C 读取一个数据。
	if v, ok := ch.Recv(); !ok || v != 1 {
		t.Fatalf("Recv() = (%v, %v), want (1, true)", v, ok)
	}

	// P 应立即被唤醒并完成第 3 次写入。
	waitTrue(t, "P 被唤醒完成第 3 次写入", sent3.Load)
	<-done

	// 剩余数据按 FIFO 顺序读出。
	if v, ok := ch.Recv(); !ok || v != 2 {
		t.Fatalf("Recv() = (%v, %v), want (2, true)", v, ok)
	}
	if v, ok := ch.Recv(); !ok || v != 3 {
		t.Fatalf("Recv() = (%v, %v), want (3, true)", v, ok)
	}
}

// 关闭通道后，所有等待读的协程收到 EOF。
func TestCloseBroadcastsEOF(t *testing.T) {
	ch := New(0)
	const readers = 5

	var wg sync.WaitGroup
	eofs := make([]bool, readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, ok := ch.Recv() // 通道为空，挂起
			eofs[i] = (v == nil && !ok)
		}(i)
	}

	// 确保所有读者都已挂起，然后关闭通道。
	time.Sleep(100 * time.Millisecond)
	ch.Close()

	wg.Wait()
	for i, eof := range eofs {
		if !eof {
			t.Fatalf("读者 %d 未收到 EOF", i)
		}
	}
	// 关闭后再读，依然立即返回 EOF。
	if v, ok := ch.Recv(); ok || v != nil {
		t.Fatalf("关闭后 Recv() = (%v, %v), want (nil, false)", v, ok)
	}
}

// 无缓冲通道：发送方在接收方到达前必须挂起（rendezvous）。
func TestUnbufferedRendezvous(t *testing.T) {
	ch := New(0)

	var sent atomic.Bool
	go func() {
		ch.Send("hello")
		sent.Store(true)
	}()

	time.Sleep(100 * time.Millisecond)
	if sent.Load() {
		t.Fatal("无缓冲通道上的 Send 在接收方到达前应当挂起")
	}

	v, ok := ch.Recv()
	if !ok || v != "hello" {
		t.Fatalf("Recv() = (%v, %v), want (hello, true)", v, ok)
	}
	waitTrue(t, "发送协程被唤醒", sent.Load)
}

// 精确唤醒：多个发送者按 FIFO 排队，一次 Recv 只唤醒队首那一个。
func TestPreciseFIFOWakeup(t *testing.T) {
	ch := New(1)
	ch.Send(0) // 占满缓冲

	var wokeA, wokeB atomic.Bool
	go func() { ch.Send("A"); wokeA.Store(true) }()
	time.Sleep(50 * time.Millisecond) // 确保 A 先进入等待队列队首
	go func() { ch.Send("B"); wokeB.Store(true) }()

	// 两个发送者都应挂起（A 在队首，B 其次）。
	time.Sleep(100 * time.Millisecond)
	if wokeA.Load() || wokeB.Load() {
		t.Fatal("缓冲已满，两个发送协程都应挂起")
	}

	// 读走一个，只应精确唤醒队首的 A。
	if v, _ := ch.Recv(); v != 0 {
		t.Fatalf("Recv() = %v, want 0", v)
	}
	waitTrue(t, "队首发送者 A 被唤醒", wokeA.Load)
	if wokeB.Load() {
		t.Fatal("B 不应被唤醒：一次 Recv 只精确唤醒一个匹配的发送者")
	}

	// 再读走 A 写入的值，B 才被唤醒。
	if v, _ := ch.Recv(); v != "A" {
		t.Fatalf("Recv() = %v, want A", v)
	}
	waitTrue(t, "发送者 B 被唤醒", wokeB.Load)
	if v, _ := ch.Recv(); v != "B" {
		t.Fatalf("Recv() = %v, want B", v)
	}
}

// 关闭后缓冲中的数据仍可读出，耗尽后才返回 EOF。
func TestCloseDrainsBufferThenEOF(t *testing.T) {
	ch := New(2)
	ch.Send(1)
	ch.Send(2)
	ch.Close()

	for _, want := range []int{1, 2} {
		if v, ok := ch.Recv(); !ok || v != want {
			t.Fatalf("Recv() = (%v, %v), want (%d, true)", v, ok, want)
		}
	}
	if v, ok := ch.Recv(); ok || v != nil {
		t.Fatalf("缓冲耗尽后 Recv() = (%v, %v), want (nil, false)", v, ok)
	}
}

// 向已关闭的通道发送会 panic（包括被 Close 唤醒的挂起发送者）。
func TestSendOnClosedPanics(t *testing.T) {
	ch := New(0)
	panicked := make(chan bool, 1)
	go func() {
		defer func() { panicked <- recover() != nil }()
		ch.Send(1) // 挂起，随后被 Close 唤醒，应 panic
	}()
	time.Sleep(50 * time.Millisecond)
	ch.Close()
	if !<-panicked {
		t.Fatal("挂起的发送者被 Close 唤醒后应 panic")
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("向已关闭通道 Send 应 panic")
			}
		}()
		ch.Send(2)
	}()
}

// 多生产者多消费者冒烟测试：数据不丢不重。
func TestChannelMPMC(t *testing.T) {
	ch := New(8)
	const producers, perProducer = 4, 500
	total := producers * perProducer

	var got sync.Map
	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				ch.Send(base + i)
			}
		}(p * perProducer)
	}

	var count atomic.Int64
	var cwg sync.WaitGroup
	for c := 0; c < 3; c++ {
		cwg.Add(1)
		go func() {
			defer cwg.Done()
			for {
				v, ok := ch.Recv()
				if !ok {
					return
				}
				if _, dup := got.LoadOrStore(v, true); dup {
					t.Errorf("重复接收: %v", v)
				}
				count.Add(1)
			}
		}()
	}

	wg.Wait()
	ch.Close()
	cwg.Wait()
	if n := count.Load(); n != int64(total) {
		t.Fatalf("接收总数 = %d, want %d", n, total)
	}
}
