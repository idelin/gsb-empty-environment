package netsec

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentSameSeqDeliveredOnce 大量同序号并行验签：verify 只执行一次，
// 且恰好一个调用成功交付，其余共享同一重放结论。
func TestConcurrentSameSeqDeliveredOnce(t *testing.T) {
	var calls atomic.Int64
	gate := make(chan struct{})
	f, err := NewReplayFilter(8, func(seq uint64, _ any) error {
		calls.Add(1)
		<-gate // 放大在途窗口，保证其余同序号调用都进入等待分支。
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	const n = 64
	var wg sync.WaitGroup
	var ok, replay atomic.Int64
	start := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := f.Verify(123, nil)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrReplay):
				replay.Add(1)
			default:
				t.Errorf("unexpected err: %v", err)
			}
		}()
	}
	close(start)
	close(gate)
	wg.Wait()

	if ok.Load() != 1 {
		t.Fatalf("successful deliveries = %d, want 1", ok.Load())
	}
	if replay.Load() != n-1 {
		t.Fatalf("replay reports = %d, want %d", replay.Load(), n-1)
	}
	if calls.Load() != 1 {
		t.Fatalf("verify invocations = %d, want 1", calls.Load())
	}
}

// TestConcurrentMixedSeqNoReorderLoss 不同序号并行验签，窗口语义在竞态下保持：
// 每个序号恰好交付一次，验签计数与成功交付数一致。
func TestConcurrentMixedSeqNoReorderLoss(t *testing.T) {
	var calls atomic.Int64
	const seqCount = 64
	const width = seqCount*2 + 16 // 宽于序号跨度，任意完成顺序都不会把参与序号判成极旧
	f, err := NewReplayFilter(width, func(seq uint64, _ any) error {
		calls.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	const dup = 5
	var wg sync.WaitGroup
	delivered := make([]atomic.Int64, seqCount)
	start := make(chan struct{})
	for s := range seqCount {
		for range dup {
			wg.Add(1)
			go func(seq uint32) {
				defer wg.Done()
				<-start
				_, err := f.Verify(seq, nil)
				if err == nil {
					delivered[seq].Add(1)
				} else if !errors.Is(err, ErrReplay) {
					t.Errorf("seq %d unexpected err: %v", seq, err)
				}
			}(uint32(s))
		}
	}
	close(start)
	wg.Wait()

	for s := range seqCount {
		if delivered[s].Load() != 1 {
			t.Fatalf("seq %d delivered %d times, want exactly 1", s, delivered[s].Load())
		}
	}
	if calls.Load() != seqCount {
		t.Fatalf("verify invocations = %d, want %d", calls.Load(), seqCount)
	}
}

// TestConcurrentTwoCandidatesAcrossWrap 两个相邻低序号在 32 位回绕点附近
// 同时完成，验证高位重构与位图提交在竞态下仍各自正确且只交付一次。
func TestConcurrentTwoCandidatesAcrossWrap(t *testing.T) {
	var calls atomic.Int64
	f, err := NewReplayFilter(32, func(seq uint64, _ any) error {
		calls.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// 先把最高已认证号推到回绕边界附近：交付 span32-2 与 span32-1。
	for _, low := range []uint32{uint32(span32 - 2), uint32(span32 - 1)} {
		if seq, err := f.Verify(low, nil); err != nil {
			t.Fatalf("seed %d failed: %v", low, err)
		} else if seq != uint64(low) {
			t.Fatalf("seed %d reconstructed to %d", low, seq)
		}
	}

	// 低序号 0 和 1 必须重构为 span32、span32+1，而不是被误判成极旧的 0、1。
	var wg sync.WaitGroup
	results := make(chan uint64, 2)
	start := make(chan struct{})
	for _, low := range []uint32{0, 1} {
		wg.Add(1)
		go func(low uint32) {
			defer wg.Done()
			<-start
			seq, err := f.Verify(low, nil)
			if err != nil {
				t.Errorf("low %d: %v", low, err)
				return
			}
			results <- seq
		}(low)
	}
	close(start)
	wg.Wait()
	close(results)

	got := map[uint64]bool{}
	for seq := range results {
		got[seq] = true
	}
	for _, want := range []uint64{span32, span32 + 1} {
		if !got[want] {
			t.Fatalf("missing reconstructed seq %d, got %v", want, got)
		}
	}

	// 再次到达同低序号必须判重放（高位沿用已提交的 span32 纪元）。
	if _, err := f.Verify(0, nil); !errors.Is(err, ErrReplay) {
		t.Fatalf("post-wrap duplicate: got %v, want ErrReplay", err)
	}
}

// TestVerifyPanicReleasesInFlight 验签 panic 被转化为错误，在途标记被清理，
// 同序号后续调用仍可重新验签成功。
func TestVerifyPanicReleasesInFlight(t *testing.T) {
	var shouldPanic atomic.Bool
	shouldPanic.Store(true)
	f, err := NewReplayFilter(4, func(seq uint64, _ any) error {
		if shouldPanic.Load() {
			panic("boom")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.Verify(7, nil); !errors.Is(err, ErrSignature) {
		t.Fatalf("panic result: got %v, want ErrSignature", err)
	}
	shouldPanic.Store(false)
	if seq, err := f.Verify(7, nil); err != nil || seq != 7 {
		t.Fatalf("retry after panic: seq=%d err=%v", seq, err)
	}
}
