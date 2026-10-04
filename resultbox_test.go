package resultbox

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countedResource 携带释放计数，用来验证“恰好释放一次”。
type countedResource struct {
	releases atomic.Int32
}

func (c *countedResource) Release() { c.releases.Add(1) }

func (c *countedResource) count() int32 { return c.releases.Load() }

func TestSingleClaimerOthersGetClaimed(t *testing.T) {
	box := New()
	res := &countedResource{}
	box.Put(res)

	got, err := box.Claim(time.Second)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if got != res {
		t.Fatalf("got wrong result")
	}
	for i := 0; i < 3; i++ {
		if _, err := box.Claim(time.Millisecond); !errors.Is(err, ErrClaimed) {
			t.Fatalf("later claim: want ErrClaimed, got %v", err)
		}
	}
	got.Release() // 领取者承担清理责任
	if n := res.count(); n != 1 {
		t.Fatalf("releases = %d, want 1", n)
	}
}

func TestClaimTimeoutDoesNotConsume(t *testing.T) {
	box := New()
	if _, err := box.Claim(20 * time.Millisecond); !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
	// 超时后结果仍可被放入并领取。
	res := &countedResource{}
	box.Put(res)
	got, err := box.Claim(time.Second)
	if err != nil || got != res {
		t.Fatalf("claim after timeout: got=%v err=%v", got, err)
	}
	got.Release()
	if n := res.count(); n != 1 {
		t.Fatalf("releases = %d, want 1", n)
	}
}

func TestCloseReleasesUnclaimedResult(t *testing.T) {
	box := New()
	res := &countedResource{}
	box.Put(res)
	box.Close()
	if n := res.count(); n != 1 {
		t.Fatalf("releases after Close = %d, want 1", n)
	}
	if _, err := box.Claim(time.Millisecond); !errors.Is(err, ErrClosed) {
		t.Fatalf("claim after close: want ErrClosed, got %v", err)
	}
	box.Close() // 重复关闭安全且不再释放
	if n := res.count(); n != 1 {
		t.Fatalf("releases after second Close = %d, want 1", n)
	}
}

func TestCloseAfterClaimDoesNotRelease(t *testing.T) {
	box := New()
	res := &countedResource{}
	box.Put(res)
	got, err := box.Claim(time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	box.Close()
	if n := res.count(); n != 0 {
		t.Fatalf("Close released claimed result: releases = %d", n)
	}
	got.Release() // 责任已转移给领取者
	if n := res.count(); n != 1 {
		t.Fatalf("releases = %d, want 1", n)
	}
}

func TestPutAfterCloseReleasesImmediately(t *testing.T) {
	box := New()
	box.Close()
	res := &countedResource{}
	box.Put(res) // 所有权已交给盒子，盒子必须兜底释放
	if n := res.count(); n != 1 {
		t.Fatalf("releases = %d, want 1", n)
	}
}

func TestManyClaimersExactlyOneWins(t *testing.T) {
	box := New()
	res := &countedResource{}

	const n = 32
	var wg sync.WaitGroup
	var wins, claimedErrs atomic.Int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := box.Claim(5 * time.Second)
			switch {
			case err == nil:
				wins.Add(1)
				got.Release()
			case errors.Is(err, ErrClaimed):
				claimedErrs.Add(1)
			default:
				t.Errorf("unexpected err: %v", err)
			}
		}()
	}
	time.Sleep(10 * time.Millisecond) // 让领取者先就位
	box.Put(res)
	wg.Wait()

	if w := wins.Load(); w != 1 {
		t.Fatalf("winners = %d, want 1", w)
	}
	if c := claimedErrs.Load(); c != n-1 {
		t.Fatalf("ErrClaimed = %d, want %d", c, n-1)
	}
	if c := res.count(); c != 1 {
		t.Fatalf("releases = %d, want 1", c)
	}
}

// TestClaimCloseRace 反复让 Claim 与 Close 竞争：
// 无论谁赢，结果的 Release 必须恰好发生一次（不泄漏、不双重释放）。
func TestClaimCloseRace(t *testing.T) {
	for round := 0; round < 2000; round++ {
		box := New()
		res := &countedResource{}
		box.Put(res)

		var wg sync.WaitGroup
		var won atomic.Bool
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := box.Claim(5 * time.Second)
				if err == nil {
					won.Store(true)
					got.Release() // 领到的人负责释放
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			box.Close() // 没人领到时由 Close 释放
		}()
		wg.Wait()

		if n := res.count(); n != 1 {
			t.Fatalf("round %d (claimer won=%v): releases = %d, want 1", round, won.Load(), n)
		}
	}
}

// TestPutCloseRace 验证 Put 与 Close 竞争时同样恰好释放一次。
func TestPutCloseRace(t *testing.T) {
	for round := 0; round < 2000; round++ {
		box := New()
		res := &countedResource{}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); box.Put(res) }()
		go func() { defer wg.Done(); box.Close() }()
		wg.Wait()

		// 无论顺序如何，此时结果必然无人领取，且已被盒子释放。
		if n := res.count(); n != 1 {
			t.Fatalf("round %d: releases = %d, want 1", round, n)
		}
	}
}
