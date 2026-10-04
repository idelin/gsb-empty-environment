package resultbox

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// counted 是携带释放计数的对象：Release 每被调用一次计数加一。
// 测试结束后计数必须恰好为 1——为 0 是泄漏，大于 1 是双重释放。
type counted struct {
	id       int
	releases *atomic.Int32
}

func newCounted(id int) (*counted, *atomic.Int32) {
	n := &atomic.Int32{}
	return &counted{id: id, releases: n}, n
}

func releaseOf(c *counted) { c.releases.Add(1) }

// 领取成功：责任随结果转移，Close 不再释放；领取方释放后计数为 1。
func TestClaimTransfersResponsibility(t *testing.T) {
	obj, n := newCounted(1)
	box := New[*counted](releaseOf)
	defer box.Close()

	if err := box.Put(obj); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := box.Claim(context.Background())
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got != obj {
		t.Fatalf("got %v, want %v", got, obj)
	}
	box.Close() // 结果已被领取，Close 不得释放
	if got := n.Load(); got != 0 {
		t.Fatalf("box released a claimed result: releases=%d", got)
	}
	releaseOf(got) // 领取方履行清理责任
	if got := n.Load(); got != 1 {
		t.Fatalf("releases=%d, want 1", got)
	}
}

// 无人领取时关闭：盒子负责释放，计数为 1。
func TestCloseReleasesUnclaimed(t *testing.T) {
	obj, n := newCounted(2)
	box := New[*counted](releaseOf)
	if err := box.Put(obj); err != nil {
		t.Fatalf("Put: %v", err)
	}
	box.Close()
	if got := n.Load(); got != 1 {
		t.Fatalf("releases=%d, want 1", got)
	}
	if _, err := box.Claim(context.Background()); err != ErrClosed {
		t.Fatalf("Claim after Close: err=%v, want ErrClosed", err)
	}
}

// 领取超时不消费结果：结果仍在盒中，可被后续领取。
func TestClaimTimeoutDoesNotConsume(t *testing.T) {
	obj, n := newCounted(3)
	box := New[*counted](releaseOf)
	defer box.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := box.Claim(ctx); err != context.DeadlineExceeded {
		t.Fatalf("Claim on empty box: err=%v, want DeadlineExceeded", err)
	}

	// 超时后结果照常放入并被领取。
	if err := box.Put(obj); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := box.Claim(context.Background())
	if err != nil {
		t.Fatalf("Claim after timeout: %v", err)
	}
	releaseOf(got)
	if got := n.Load(); got != 1 {
		t.Fatalf("releases=%d, want 1", got)
	}
}

// 只有一个领取者成功，其余得到 ErrClaimed。
func TestOnlyOneClaimerWins(t *testing.T) {
	obj, n := newCounted(4)
	box := New[*counted](releaseOf)
	defer box.Close()
	if err := box.Put(obj); err != nil {
		t.Fatalf("Put: %v", err)
	}

	const claimers = 8
	var wg sync.WaitGroup
	errs := make([]error, claimers)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := box.Claim(context.Background())
			if err == nil {
				releaseOf(got) // 唯一的赢家履行清理责任
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()

	wins := 0
	for _, err := range errs {
		switch err {
		case nil:
			wins++
		case ErrClaimed:
		default:
			t.Fatalf("unexpected err: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("wins=%d, want 1", wins)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("releases=%d, want 1", got)
	}
}

// 关闭与领取竞争：多轮并发，每轮释放计数必须恰好为 1。
// 要么某个领取者胜出并负责释放，要么 Close 释放——绝不泄漏或双重释放。
func TestCloseClaimRaceExactlyOnce(t *testing.T) {
	for round := 0; round < 2000; round++ {
		obj, n := newCounted(round)
		box := New[*counted](releaseOf)
		if err := box.Put(obj); err != nil {
			t.Fatalf("round %d Put: %v", round, err)
		}

		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := box.Claim(context.Background())
				if err == nil {
					releaseOf(got) // 赢家接管清理责任
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			box.Close()
		}()
		wg.Wait()

		if got := n.Load(); got != 1 {
			t.Fatalf("round %d: releases=%d, want exactly 1 (0=leak, >1=double free)", round, got)
		}
	}
}

// Put 到已关闭的盒子：值被立即释放，不泄漏。
func TestPutAfterCloseReleases(t *testing.T) {
	obj, n := newCounted(5)
	box := New[*counted](releaseOf)
	box.Close()
	if err := box.Put(obj); err != ErrClosed {
		t.Fatalf("Put after Close: err=%v, want ErrClosed", err)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("releases=%d, want 1", got)
	}
}

// 空盒关闭：等待中的领取者得到 ErrClosed。
func TestCloseWakesWaiters(t *testing.T) {
	box := New[*counted](releaseOf)
	done := make(chan error, 1)
	go func() {
		_, err := box.Claim(context.Background())
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	box.Close()
	select {
	case err := <-done:
		if err != ErrClosed {
			t.Fatalf("err=%v, want ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter not woken by Close")
	}
}
