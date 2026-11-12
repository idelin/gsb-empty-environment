package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// scriptedSource 按脚本返回 Intn，并记录每次调用的上界，便于断言
// PRR 只抽取一次、IRR 每次都抽取。
type scriptedSource struct {
	mu       sync.Mutex
	values   []int
	bounds   []int
	position int
}

func (s *scriptedSource) Intn(n int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bounds = append(s.bounds, n)
	if s.position >= len(s.values) {
		panic("scriptedSource: no more scripted values")
	}
	v := s.values[s.position]
	s.position++
	if v < 0 || v >= n {
		panic(fmt.Sprintf("scriptedSource: value %d out of bounds %d", v, n))
	}
	return v
}

func (s *scriptedSource) calls() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.bounds...)
}

func repeatValues(v, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func testMatrices() (Matrix, Matrix) {
	prr := Matrix{Row: [][]Rational{{{3, 4}, {1, 4}}, {{1, 4}, {3, 4}}}}
	irr := Matrix{Row: [][]Rational{{{2, 3}, {1, 3}}, {{1, 3}, {2, 3}}}}
	return prr, irr
}

func newTestReporter(t *testing.T, store Store, src Source, version string) *Reporter {
	t.Helper()
	prr, irr := testMatrices()
	r, err := NewReporter(Config{
		DeviceID:      "device-1",
		Field:         "browsing_history",
		ParamVersion:  version,
		Permanent:     prr,
		Instantaneous: irr,
		Store:         store,
		Source:        src,
	})
	if err != nil {
		t.Fatalf("NewReporter: %v", err)
	}
	return r
}

func TestRetryDoesNotRedraw(t *testing.T) {
	store := NewMemoryStore()
	// PRR 行 0 的总权重为 4，第一次脚本值 3 -> 永久符号 1；
	// 之后只应产生 IRR 调用（上界 3），绝不再出现上界 4。
	src := &scriptedSource{values: []int{3, 0, 2, 1}}
	r := newTestReporter(t, store, src, "v1")
	ctx := context.Background()

	permanent, err := r.Permanent(ctx, 0)
	if err != nil {
		t.Fatalf("Permanent: %v", err)
	}
	if permanent != 1 {
		t.Fatalf("permanent = %d, want 1", permanent)
	}
	again, err := r.Permanent(ctx, 0)
	if err != nil || again != permanent {
		t.Fatalf("retry Permanent = (%d, %v), want (%d, nil)", again, err, permanent)
	}

	out1, err := r.Report(ctx, 0)
	if err != nil {
		t.Fatalf("Report 1: %v", err)
	}
	out2, err := r.Report(ctx, 0)
	if err != nil {
		t.Fatalf("Report 2: %v", err)
	}
	// IRR 行 1 的权重为 [1,2]/3：脚本值 0 -> 符号 0，脚本值 2 -> 符号 1。
	if out1 != 0 || out2 != 1 {
		t.Fatalf("reports = (%d, %d), want (0, 1)", out1, out2)
	}

	bounds := src.calls()
	wantBounds := []int{4, 3, 3}
	if len(bounds) != len(wantBounds) {
		t.Fatalf("Intn call bounds = %v, want %v (no PRR redraw)", bounds, wantBounds)
	}
	for i, b := range wantBounds {
		if bounds[i] != b {
			t.Fatalf("call %d bound = %d, want %d (full calls: %v)", i, bounds[i], b, bounds)
		}
	}
	if store.Len() != 1 {
		t.Fatalf("store records = %d, want 1", store.Len())
	}
}

func TestPermanentSurvivesNewReporter(t *testing.T) {
	store := NewMemoryStore()
	src := &scriptedSource{values: []int{0, 1, 2}}
	r1 := newTestReporter(t, store, src, "v1")
	permanent, err := r1.Permanent(context.Background(), 0)
	if err != nil {
		t.Fatalf("Permanent: %v", err)
	}
	// 模拟进程重启：相同配置新建 Reporter，剩余脚本值都不应被 PRR 消耗。
	r2 := newTestReporter(t, store, src, "v1")
	reloaded, err := r2.Permanent(context.Background(), 0)
	if err != nil {
		t.Fatalf("reloaded Permanent: %v", err)
	}
	if reloaded != permanent {
		t.Fatalf("reloaded permanent = %d, want %d", reloaded, permanent)
	}
	if calls := src.calls(); len(calls) != 1 {
		t.Fatalf("PRR redraw after reload: bounds %v", calls)
	}
}

// gateStore 在放行前阻塞所有 Commit，确保并发首次上报真正同时竞争首次写入。
type gateStore struct {
	Store
	started   chan struct{}
	release   chan struct{}
	committed atomic.Int32
}

func newGateStore(inner Store) *gateStore {
	return &gateStore{Store: inner, started: make(chan struct{}), release: make(chan struct{})}
}

func (g *gateStore) Commit(ctx context.Context, key string, rec Record) error {
	g.committed.Add(1)
	close(g.started)
	select {
	case <-g.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return g.Store.Commit(ctx, key, rec)
}

func TestConcurrentFirstReport(t *testing.T) {
	store := NewMemoryStore()
	gate := newGateStore(store)
	// 无论多少 goroutine 并发，只有一个会真正执行 PRR 抽取，因此只需一个脚本值。
	src := &scriptedSource{values: []int{0}}
	r := newTestReporter(t, gate, src, "v1")

	const n = 64
	var wg sync.WaitGroup
	results := make([]int, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = r.Permanent(context.Background(), 0)
		}(i)
	}
	<-gate.started
	close(gate.release)
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Permanent error: %v", err)
		}
	}
	for i := 1; i < n; i++ {
		if results[i] != results[0] {
			t.Fatalf("goroutine %d got %d, want %d (single shared permanent value)", i, results[i], results[0])
		}
	}
	if gate.committed.Load() != 1 {
		t.Fatalf("Commit attempts = %d, want exactly 1", gate.committed.Load())
	}
	if store.Len() != 1 {
		t.Fatalf("store records = %d, want 1", store.Len())
	}
	if calls := src.calls(); len(calls) != 1 || calls[0] != 4 {
		t.Fatalf("PRR draw calls = %v, want exactly one bound-4 draw", calls)
	}
}

func TestConcurrentReportsShareMemoButRedrawIRR(t *testing.T) {
	store := NewMemoryStore()
	// 1 次 PRR（上界 4）+ 32 次 IRR（上界 3），脚本值统一给 0。
	src := &scriptedSource{values: repeatValues(0, 33)}
	r := newTestReporter(t, store, src, "v1")

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, err := r.Report(context.Background(), 0); err != nil {
				t.Errorf("Report: %v", err)
			}
		}()
	}
	wg.Wait()

	var prrCalls, irrCalls int
	for _, b := range src.calls() {
		switch b {
		case 4:
			prrCalls++
		case 3:
			irrCalls++
		default:
			t.Fatalf("unexpected Intn bound %d", b)
		}
	}
	if prrCalls != 1 || irrCalls != n {
		t.Fatalf("want 1 PRR draw and %d IRR draws, got prr=%d irr=%d", n, prrCalls, irrCalls)
	}
	if store.Len() != 1 {
		t.Fatalf("store records = %d, want 1", store.Len())
	}
}

func TestParamRotationCreatesNewMemo(t *testing.T) {
	store := NewMemoryStore()
	// v1: PRR 值 3 -> 永久符号 1；v2: PRR 值 0 -> 永久符号 0。
	src := &scriptedSource{values: []int{3, 0}}
	r1 := newTestReporter(t, store, src, "v1")
	r2 := newTestReporter(t, store, src, "v2")

	p1, err := r1.Permanent(context.Background(), 0)
	if err != nil {
		t.Fatalf("v1 Permanent: %v", err)
	}
	p2, err := r2.Permanent(context.Background(), 0)
	if err != nil {
		t.Fatalf("v2 Permanent: %v", err)
	}
	if p1 == p2 {
		t.Fatalf("two versions produced same permanent symbol %d; expected independent draws", p1)
	}
	if store.Len() != 2 {
		t.Fatalf("store records = %d, want 2 (one per version)", store.Len())
	}
	if r1.Key() == r2.Key() {
		t.Fatal("storage keys must differ across param versions")
	}
	// 切回旧版本仍读到旧记忆，且不再抽取。
	p1Again, err := r1.Permanent(context.Background(), 0)
	if err != nil || p1Again != p1 {
		t.Fatalf("rollback Permanent = (%d, %v), want (%d, nil)", p1Again, err, p1)
	}
	if calls := src.calls(); len(calls) != 2 {
		t.Fatalf("expected 2 PRR draws total, got bounds %v", calls)
	}
}

func TestFingerprintMismatch(t *testing.T) {
	store := NewMemoryStore()
	r1 := newTestReporter(t, store, &scriptedSource{values: []int{0}}, "v1")
	if _, err := r1.Permanent(context.Background(), 0); err != nil {
		t.Fatalf("seed Permanent: %v", err)
	}

	// 同版本号但替换 PRR 矩阵：指纹不同，复用旧记忆必须显式报错。
	otherPRR := Matrix{Row: [][]Rational{{{1, 2}, {1, 2}}, {{1, 2}, {1, 2}}}}
	_, irr := testMatrices()
	r2, err := NewReporter(Config{
		DeviceID:      "device-1",
		Field:         "browsing_history",
		ParamVersion:  "v1",
		Permanent:     otherPRR,
		Instantaneous: irr,
		Store:         store,
		Source:        &scriptedSource{values: []int{0}},
	})
	if err != nil {
		t.Fatalf("NewReporter: %v", err)
	}
	_, err = r2.Permanent(context.Background(), 0)
	var mismatch *FingerprintMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected FingerprintMismatchError, got %v", err)
	}
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("errors.Is(ErrFingerprintMismatch) failed for %v", err)
	}
}

// faultyStore 在 Load/Commit 上注入可配置次数的失败。
type faultyStore struct {
	inner      Store
	failLoad   atomic.Int32
	failCommit atomic.Int32
}

func (f *faultyStore) Load(ctx context.Context, key string) (Record, error) {
	if f.failLoad.Load() > 0 {
		f.failLoad.Add(-1)
		return Record{}, errors.New("injected load failure")
	}
	return f.inner.Load(ctx, key)
}

func (f *faultyStore) Commit(ctx context.Context, key string, rec Record) error {
	if f.failCommit.Load() > 0 {
		f.failCommit.Add(-1)
		return errors.New("injected commit failure")
	}
	return f.inner.Commit(ctx, key, rec)
}

func TestFailedCommitRetriesThenSucceeds(t *testing.T) {
	faulty := &faultyStore{inner: NewMemoryStore()}
	faulty.failCommit.Store(1)
	// 第一次抽取给值 3（->符号 1）但提交失败；重试给值 0（->符号 0）成功，
	// 成功后永久记忆固定为第二次结果，后续调用不再抽取。
	src := &scriptedSource{values: []int{3, 0, 2}}
	r := newTestReporter(t, faulty, src, "v1")
	ctx := context.Background()

	if _, err := r.Permanent(ctx, 0); err == nil {
		t.Fatal("expected injected commit failure")
	}
	if faulty.inner.(*MemoryStore).Len() != 0 {
		t.Fatal("failed commit must not persist anything")
	}
	permanent, err := r.Permanent(ctx, 0)
	if err != nil {
		t.Fatalf("retry Permanent: %v", err)
	}
	if permanent != 0 {
		t.Fatalf("permanent after retry = %d, want 0", permanent)
	}
	again, err := r.Permanent(ctx, 0)
	if err != nil || again != permanent {
		t.Fatalf("post-retry Permanent = (%d, %v), want (%d, nil)", again, err, permanent)
	}
	if bounds := src.calls(); len(bounds) != 2 || bounds[0] != 4 || bounds[1] != 4 {
		t.Fatalf("expected 2 PRR draws, got bounds %v", bounds)
	}
}

func TestResponseLossKeepsMemo(t *testing.T) {
	store := NewMemoryStore()
	// 上报“响应丢失”对本地没有任何影响：PRR 只抽一次，重试只补 IRR。
	src := &scriptedSource{values: []int{3, 0, 2, 1}}
	r := newTestReporter(t, store, src, "v1")
	ctx := context.Background()

	first, err := r.Report(ctx, 0)
	if err != nil {
		t.Fatalf("first Report: %v", err)
	}
	// 模拟网络响应丢失后的宿主重试两次。
	retry1, err := r.Report(ctx, 0)
	if err != nil {
		t.Fatalf("retry Report 1: %v", err)
	}
	retry2, err := r.Report(ctx, 0)
	if err != nil {
		t.Fatalf("retry Report 2: %v", err)
	}

	// 永久符号恒为 1；IRR 输入恒定，三次上报的差异仅来自 IRR 抽取。
	permanent, err := r.Permanent(ctx, 0)
	if err != nil {
		t.Fatalf("Permanent: %v", err)
	}
	if permanent != 1 {
		t.Fatalf("permanent drifted after response loss: %d", permanent)
	}
	// IRR 行 1 权重 [1,2]/3：脚本值 0 -> 0，值 2 -> 1，值 1 -> 1。
	if first != 0 || retry1 != 1 || retry2 != 1 {
		t.Fatalf("reports = (%d,%d,%d), want (0,1,1)", first, retry1, retry2)
	}
	bounds := src.calls()
	wantBounds := []int{4, 3, 3, 3}
	if len(bounds) != len(wantBounds) {
		t.Fatalf("bounds = %v, want %v", bounds, wantBounds)
	}
	for i, b := range wantBounds {
		if bounds[i] != b {
			t.Fatalf("call %d bound = %d, want %d (full: %v)", i, bounds[i], b, bounds)
		}
	}
	if store.Len() != 1 {
		t.Fatalf("store records = %d, want 1", store.Len())
	}
}

func TestContextCancelled(t *testing.T) {
	store := NewMemoryStore()
	r := newTestReporter(t, store, &scriptedSource{values: repeatValues(0, 4)}, "v1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Permanent(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Permanent error = %v, want context.Canceled", err)
	}
	if _, err := r.Report(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Report error = %v, want context.Canceled", err)
	}
	if store.Len() != 0 {
		t.Fatalf("cancelled attempt persisted a record, store len = %d", store.Len())
	}
}

func TestInvalidTruthRejected(t *testing.T) {
	r := newTestReporter(t, NewMemoryStore(), &scriptedSource{values: []int{0}}, "v1")
	if _, err := r.Permanent(context.Background(), 7); !errors.Is(err, ErrSymbolOutOfRange) {
		t.Fatalf("truth out of range error = %v, want ErrSymbolOutOfRange", err)
	}
}
