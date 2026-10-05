package mpmcq

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// 正常关闭：已有元素必须被消费完，之后 Recv 报 ErrClosed，新发送被拒绝。
func TestGracefulCloseDrains(t *testing.T) {
	q := New[int](4)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if err := q.Send(ctx, i); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if !q.Close() {
		t.Fatal("Close should take effect")
	}
	if err := q.Send(ctx, 99); !errors.Is(err, ErrClosed) {
		t.Fatalf("send after close: got %v, want ErrClosed", err)
	}
	for i := 0; i < 4; i++ {
		v, err := q.Recv(ctx)
		if err != nil || v != i {
			t.Fatalf("recv: got (%d, %v), want (%d, nil)", v, err, i)
		}
	}
	if _, err := q.Recv(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("recv after drain: got %v, want ErrClosed", err)
	}
	c := q.Conservation()
	if !c.OwnershipOK() || !c.CapacityOK() {
		t.Fatalf("conservation violated: %+v", c)
	}
	s, errs := Replay(q.History(), 4)
	if len(errs) != 0 {
		t.Fatalf("replay violations: %v", errs)
	}
	if !s.Closed || s.Aborted || !s.OwnershipOK() {
		t.Fatalf("bad summary: %+v", s)
	}
}

// 故障关闭：立即停止，余项返回给 Abort 调用者，阻塞的收发双方被唤醒。
func TestAbortReturnsLeftovers(t *testing.T) {
	q := New[int](8)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := q.Send(ctx, i); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	// 一个接收者先取走 2 个；其后的 Recv 在 Abort 生效后应收到 ErrAborted。
	took := make(chan struct{})
	proceed := make(chan struct{})
	recvErr := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			if _, err := q.Recv(ctx); err != nil {
				recvErr <- err
				return
			}
		}
		close(took)
		<-proceed
		_, err := q.Recv(ctx)
		recvErr <- err
	}()
	<-took

	left := q.Abort()
	close(proceed)
	if left == nil {
		t.Fatal("Abort should win and return leftovers")
	}
	if len(left) != 3 {
		t.Fatalf("leftovers: got %v, want 3 items [2 3 4]", left)
	}
	for i, v := range left {
		if v != i+2 {
			t.Fatalf("leftover order: got %v, want [2 3 4]", left)
		}
	}
	if err := <-recvErr; !errors.Is(err, ErrAborted) {
		t.Fatalf("blocked recv: got %v, want ErrAborted", err)
	}
	if err := q.Send(ctx, 1); !errors.Is(err, ErrAborted) {
		t.Fatalf("send after abort: got %v, want ErrAborted", err)
	}
	if _, err := q.Recv(ctx); !errors.Is(err, ErrAborted) {
		t.Fatalf("recv after abort: got %v, want ErrAborted", err)
	}
	c := q.Conservation()
	if !c.OwnershipOK() || c.Buffered != 0 {
		t.Fatalf("conservation violated: %+v", c)
	}
	s, errs := Replay(q.History(), 8)
	if len(errs) != 0 {
		t.Fatalf("replay violations: %v", errs)
	}
	if !s.Aborted || s.Closed || s.Outstanding != 0 || !s.OwnershipOK() {
		t.Fatalf("bad summary: %+v", s)
	}
}

// 关闭竞争：Close 与 Abort 并发调用，必须恰好一个生效，且语义一致。
func TestCloseAbortRaceFirstWins(t *testing.T) {
	for trial := 0; trial < 2000; trial++ {
		q := New[int](4)
		ctx := context.Background()
		n := trial % 5
		for i := 0; i < n; i++ {
			_ = q.Send(ctx, i)
		}
		var wg sync.WaitGroup
		var closeWon, abortWon bool
		var leftovers []int
		wg.Add(2)
		go func() { defer wg.Done(); closeWon = q.Close() }()
		go func() { defer wg.Done(); l := q.Abort(); abortWon = l != nil; leftovers = l }()
		wg.Wait()

		if closeWon == abortWon {
			t.Fatalf("trial %d: exactly one shutdown must win, closeWon=%v abortWon=%v", trial, closeWon, abortWon)
		}
		if _, errs := Replay(q.History(), 4); len(errs) != 0 {
			t.Fatalf("trial %d: replay violations: %v", trial, errs)
		}
		if closeWon {
			// 正常关闭语义：n 个元素全部可排空。
			for i := 0; i < n; i++ {
				if _, err := q.Recv(ctx); err != nil {
					t.Fatalf("trial %d: drain recv %d: %v", trial, i, err)
				}
			}
			if _, err := q.Recv(ctx); !errors.Is(err, ErrClosed) {
				t.Fatalf("trial %d: want ErrClosed after drain, got %v", trial, err)
			}
		} else {
			// 故障关闭语义：余项立即返回，队列已空。
			if len(leftovers) != n {
				t.Fatalf("trial %d: leftovers %d, want %d", trial, len(leftovers), n)
			}
			if _, err := q.Recv(ctx); !errors.Is(err, ErrAborted) {
				t.Fatalf("trial %d: want ErrAborted, got %v", trial, err)
			}
		}
		if c := q.Conservation(); !c.OwnershipOK() {
			t.Fatalf("trial %d: ownership violated: %+v", trial, c)
		}
	}
}

// 取消不吞元素：队列满时 Send 被取消，元素留在调用者手中，不得出现在队列里。
func TestSendCancelDoesNotSwallow(t *testing.T) {
	q := New[int](2)
	ctx := context.Background()
	_ = q.Send(ctx, 1)
	_ = q.Send(ctx, 2)

	cctx, cancel := context.WithCancel(ctx)
	sendErr := make(chan error, 1)
	go func() { sendErr <- q.Send(cctx, 3) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-sendErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled send: got %v, want context.Canceled", err)
	}
	// 元素 3 不得入队：排空后只能看到 1 和 2。
	q.Close()
	var got []int
	for {
		v, err := q.Recv(ctx)
		if errors.Is(err, ErrClosed) {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		got = append(got, v)
	}
	if fmt.Sprint(got) != "[1 2]" {
		t.Fatalf("canceled element leaked into queue: %v", got)
	}
	s, errs := Replay(q.History(), 2)
	if len(errs) != 0 {
		t.Fatalf("replay violations: %v", errs)
	}
	if s.SendCancels != 1 || s.Sent != 2 || s.Received != 2 {
		t.Fatalf("bad summary: %+v", s)
	}
}

// 取消不吞元素：队列空时 Recv 被取消，不得取走或丢失任何元素。
func TestRecvCancelDoesNotLose(t *testing.T) {
	q := New[int](2)
	ctx := context.Background()

	cctx, cancel := context.WithCancel(ctx)
	recvErr := make(chan error, 1)
	go func() {
		_, err := q.Recv(cctx)
		recvErr <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-recvErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled recv: got %v, want context.Canceled", err)
	}
	// 之后入队的元素必须完好可收。
	if err := q.Send(ctx, 42); err != nil {
		t.Fatalf("send: %v", err)
	}
	v, err := q.Recv(ctx)
	if err != nil || v != 42 {
		t.Fatalf("recv: got (%d, %v), want (42, nil)", v, err)
	}
	s, errs := Replay(q.History(), 2)
	if len(errs) != 0 {
		t.Fatalf("replay violations: %v", errs)
	}
	if s.RecvCancels != 1 || !s.OwnershipOK() {
		t.Fatalf("bad summary: %+v", s)
	}
}

// 压力测试：多生产者多消费者并发，随机选择关闭方式，
// 验证所有权守恒（入队 = 消费 + 余项 + 滞留）与容量守恒。
func testStress(t *testing.T, abort bool) {
	const (
		capacity     = 8
		producers    = 4
		consumers    = 4
		itemsPerProd = 200
	)
	q := New[int](capacity)
	ctx := context.Background()

	var wg sync.WaitGroup
	consumedCh := make(chan int, producers*itemsPerProd)
	retainedCh := make(chan int, producers*itemsPerProd) // 发送被拒/中止时调用者保留的元素

	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < itemsPerProd; i++ {
				v := base + i
				err := q.Send(ctx, v)
				if errors.Is(err, ErrClosed) || errors.Is(err, ErrAborted) {
					retainedCh <- v // 元素未被吞，仍归调用者
				}
			}
		}(p * itemsPerProd)
	}
	for c := 0; c < consumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				v, err := q.Recv(ctx)
				if errors.Is(err, ErrClosed) || errors.Is(err, ErrAborted) {
					return
				}
				if err != nil {
					t.Errorf("recv: %v", err)
					return
				}
				consumedCh <- v
			}
		}()
	}

	time.Sleep(2 * time.Millisecond)
	var leftovers []int
	if abort {
		leftovers = q.Abort()
		if leftovers == nil {
			t.Fatal("abort should win")
		}
	} else {
		if !q.Close() {
			t.Fatal("close should win")
		}
	}
	wg.Wait()
	close(consumedCh)
	close(retainedCh)

	seen := make(map[int]int)
	add := func(v int, where string) {
		seen[v]++
		if seen[v] > 1 {
			t.Errorf("element %d duplicated (%s)", v, where)
		}
	}
	nConsumed := 0
	for v := range consumedCh {
		add(v, "consumed")
		nConsumed++
	}
	nRetained := 0
	for v := range retainedCh {
		add(v, "retained by producer")
		nRetained++
	}
	for _, v := range leftovers {
		add(v, "leftover")
	}
	// 每个生产出的元素恰有归宿：被消费 / 被调用者保留 / 作为余项返回。
	if len(seen) != producers*itemsPerProd {
		t.Fatalf("elements lost: accounted %d, want %d", len(seen), producers*itemsPerProd)
	}
	c := q.Conservation()
	if !c.OwnershipOK() || !c.CapacityOK() {
		t.Fatalf("conservation violated: %+v", c)
	}
	if abort && c.Buffered != 0 {
		t.Fatalf("abort must leave empty buffer, got %d", c.Buffered)
	}
	if !abort && uint64(nConsumed) != c.Accepted {
		t.Fatalf("graceful close must drain all: consumed %d, accepted %d", nConsumed, c.Accepted)
	}
	if abort && uint64(nConsumed)+uint64(len(leftovers)) != c.Accepted {
		t.Fatalf("abort accounting: consumed %d + leftovers %d != accepted %d",
			nConsumed, len(leftovers), c.Accepted)
	}
	s, errs := Replay(q.History(), capacity)
	if len(errs) != 0 {
		t.Fatalf("replay violations: %v", errs)
	}
	if !s.OwnershipOK() || s.MaxBuffered > capacity {
		t.Fatalf("bad summary: %+v", s)
	}
	t.Logf("abort=%v accepted=%d consumed=%d returned=%d retained=%d maxBuffered=%d",
		abort, c.Accepted, nConsumed, c.Returned, nRetained, s.MaxBuffered)
}

func TestStressGracefulClose(t *testing.T) { testStress(t, false) }
func TestStressAbort(t *testing.T)         { testStress(t, true) }
