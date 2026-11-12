package earlydata

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const testWindow = 10 * time.Second

var testNow = time.Unix(1_700_000_000, 0)

func newTestGate(t *testing.T, store ReplayStore, now func() time.Time) *Gate {
	t.Helper()
	gate, err := NewGate(Config{
		Window:    testWindow,
		RecordTTL: time.Minute,
		Store:     store,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	return gate
}

func obfuscated(observedAgeMs int64, ageAdd uint32) uint32 {
	return uint32((uint64(observedAgeMs) + uint64(ageAdd)) & 0xFFFFFFFF)
}

func validRequest(now time.Time) Request {
	age := 500 * time.Millisecond
	const ageAdd uint32 = 0x11223344
	return Request{
		TicketIssuedAt: now.Add(-age),
		TicketAgeAdd:   ageAdd,
		ObfuscatedAge:  obfuscated(age.Milliseconds(), ageAdd),
		Binder:         []byte("psk-binder-A"),
	}
}

// 合法的单次 0-RTT 只放行一次，同一 binder 的重放退回 1-RTT。
func TestAdmit_Legitimate0RTT_AllowedExactlyOnce(t *testing.T) {
	store := NewMemoryStore()
	gate := newTestGate(t, store, func() time.Time { return testNow })
	req := validRequest(testNow)

	first := gate.Admit(context.Background(), req)
	if !first.Allowed || first.Reason != ReasonNone {
		t.Fatalf("first 0-RTT: got %+v, want allowed", first)
	}
	if store.Len() != 1 {
		t.Fatalf("store len = %d, want 1", store.Len())
	}

	replay := gate.Admit(context.Background(), req)
	if replay.Allowed || replay.Reason != ReasonReplay {
		t.Fatalf("replay: got %+v, want ReasonReplay", replay)
	}
}

// 集群另一个节点重放：两个 Gate 共享同一个存储，第二次必须被拒。
func TestAdmit_CrossNodeReplay_Rejected(t *testing.T) {
	sharedStore := NewMemoryStore()
	nodeA := newTestGate(t, sharedStore, func() time.Time { return testNow })
	nodeB := newTestGate(t, sharedStore, func() time.Time { return testNow })
	req := validRequest(testNow)

	if res := nodeA.Admit(context.Background(), req); !res.Allowed {
		t.Fatalf("node A: got %+v, want allowed", res)
	}
	res := nodeB.Admit(context.Background(), req)
	if res.Allowed || res.Reason != ReasonReplay {
		t.Fatalf("node B replay: got %+v, want ReasonReplay", res)
	}
}

// 窗口边界：偏差恰好等于窗口放行，超出一毫秒拒绝；两个方向都要覆盖。
func TestAdmit_WindowBoundary(t *testing.T) {
	store := NewMemoryStore()
	gate := newTestGate(t, store, func() time.Time { return testNow })

	cases := []struct {
		name   string
		skewMs int64
		want   bool
	}{
		{"at window, fast client", testWindow.Milliseconds(), true},
		{"at window, slow client", -testWindow.Milliseconds(), true},
		{"one ms over window, fast", testWindow.Milliseconds() + 1, false},
		{"one ms over window, slow", -(testWindow.Milliseconds() + 1), false},
		{"well inside window", 42, true},
		{"far outside window", 30_000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest(testNow)
			req.Binder = []byte("binder-" + tc.name)
			req.ObfuscatedAge = uint32(int64(req.ObfuscatedAge) + tc.skewMs)

			res := gate.Admit(context.Background(), req)
			if res.Allowed != tc.want {
				t.Fatalf("skew %dms: got %+v, want allowed=%v", tc.skewMs, res, tc.want)
			}
			if !tc.want && res.Reason != ReasonAgeSkew {
				t.Fatalf("skew %dms: reason = %v, want ReasonAgeSkew", tc.skewMs, res.Reason)
			}
		})
	}
}

// 混淆票龄跨越 2^32 边界回绕时，模差仍然对应真实小偏差。
func TestAdmit_AgeAddWraparound_ModularDiff(t *testing.T) {
	store := NewMemoryStore()
	gate := newTestGate(t, store, func() time.Time { return testNow })

	age := 2 * time.Second
	// 让 ageMs + ageAdd 越过 2^32，客户端的混淆票龄落在回绕后的小区间。
	ageAdd := uint32(uint64(ticketAgeModulus) - uint64(age.Milliseconds()) + 7)

	req := Request{
		TicketIssuedAt: testNow.Add(-age),
		TicketAgeAdd:   ageAdd,
		ObfuscatedAge:  7, // 回绕后的期望值
		Binder:         []byte("wrap-binder"),
	}
	if res := gate.Admit(context.Background(), req); !res.Allowed {
		t.Fatalf("wraparound exact: got %+v, want allowed", res)
	}

	// 回绕口径下超窗 1ms：0 附近减去 11ms（窗口 10s 之外），
	// 用 uint32 减法自动得到负的模差。
	req.Binder = []byte("wrap-binder-skew")
	req.ObfuscatedAge = uint32(int64(7) - (testWindow.Milliseconds() + 1))
	res := gate.Admit(context.Background(), req)
	if res.Allowed || res.Reason != ReasonAgeSkew {
		t.Fatalf("wraparound skew: got %+v, want ReasonAgeSkew", res)
	}
}

// 实测票龄达到 2^32 毫秒，32 位混淆票龄已整体回绕一圈，必须拒绝且不碰记录库。
func TestAdmit_TicketAgeOverflow_RejectedWithoutStoreWrite(t *testing.T) {
	fault := &faultStore{}
	gate := newTestGate(t, fault, func() time.Time { return testNow })

	age := time.Duration(ticketAgeModulus) * time.Millisecond
	req := Request{
		TicketIssuedAt: testNow.Add(-age),
		TicketAgeAdd:   123,
		ObfuscatedAge:  0,
		Binder:         []byte("old-binder"),
	}
	res := gate.Admit(context.Background(), req)
	if res.Allowed || res.Reason != ReasonAgeOverflow {
		t.Fatalf("got %+v, want ReasonAgeOverflow", res)
	}
	if fault.calls != 0 {
		t.Fatalf("store calls = %d, want 0 (overflow rejected before store)", fault.calls)
	}
}

// 票据签发时间略超前于本机时钟（负票龄）时，模 2^32 计算仍应一致。
func TestAdmit_NegativeObservedAge(t *testing.T) {
	store := NewMemoryStore()
	gate := newTestGate(t, store, func() time.Time { return testNow })

	observedAgeMs := int64(-5)
	const ageAdd uint32 = 0xABCDEF01
	req := Request{
		TicketIssuedAt: testNow.Add(5 * time.Millisecond),
		TicketAgeAdd:   ageAdd,
		ObfuscatedAge:  obfuscated(observedAgeMs, ageAdd),
		Binder:         []byte("future-ticket"),
	}
	if res := gate.Admit(context.Background(), req); !res.Allowed {
		t.Fatalf("negative age: got %+v, want allowed", res)
	}
}

// 记录库不可用时退回 1-RTT；恢复后新的 binder 可以正常放行。
func TestAdmit_StoreUnavailable_FailClosedThenRecover(t *testing.T) {
	fault := &faultStore{err: errors.New("redis down")}
	gate := newTestGate(t, fault, func() time.Time { return testNow })
	req := validRequest(testNow)

	res := gate.Admit(context.Background(), req)
	if res.Allowed || res.Reason != ReasonStoreUnavailable {
		t.Fatalf("unavailable: got %+v, want ReasonStoreUnavailable", res)
	}

	fault.err = nil
	res = gate.Admit(context.Background(), req)
	if !res.Allowed || res.Reason != ReasonNone {
		t.Fatalf("recovered: got %+v, want allowed", res)
	}
}

// 记录超过 TTL 后同一 binder 可以再次作为首次出现被接受。
func TestAdmit_RecordExpiry_AllowsAgain(t *testing.T) {
	clock := &atomicClock{t: testNow}
	store := NewMemoryStore()
	store.now = clock.get
	gate := newTestGate(t, store, func() time.Time { return testNow })

	req := validRequest(testNow)
	if res := gate.Admit(context.Background(), req); !res.Allowed {
		t.Fatalf("first: got %+v, want allowed", res)
	}

	clock.advance(30 * time.Second)
	if res := gate.Admit(context.Background(), req); res.Allowed || res.Reason != ReasonReplay {
		t.Fatalf("within TTL: got %+v, want ReasonReplay", res)
	}

	clock.advance(31 * time.Second) // 累计 61s，超过 1 分钟 TTL
	res := gate.Admit(context.Background(), req)
	if !res.Allowed || res.Reason != ReasonNone {
		t.Fatalf("after TTL: got %+v, want allowed again", res)
	}
}

// 并发重放同一 binder：无论多少节点同时提交，只能放行一次。
func TestAdmit_ConcurrentSameBinder_ExactlyOneAccepted(t *testing.T) {
	store := NewMemoryStore()
	gate := newTestGate(t, store, func() time.Time { return testNow })
	req := validRequest(testNow)

	const goroutines = 100
	var wg sync.WaitGroup
	results := make([]Result, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx] = gate.Admit(context.Background(), req)
		}(i)
	}
	wg.Wait()

	allowed, replays := 0, 0
	for _, res := range results {
		switch {
		case res.Allowed:
			allowed++
		case res.Reason == ReasonReplay:
			replays++
		default:
			t.Fatalf("unexpected result %+v", res)
		}
	}
	if allowed != 1 || replays != goroutines-1 {
		t.Fatalf("allowed=%d replays=%d, want 1 and %d", allowed, replays, goroutines-1)
	}
}

func TestAdmit_InvalidRequest(t *testing.T) {
	store := NewMemoryStore()
	gate := newTestGate(t, store, func() time.Time { return testNow })

	noBinder := validRequest(testNow)
	noBinder.Binder = nil
	if res := gate.Admit(context.Background(), noBinder); res.Allowed || res.Reason != ReasonInvalidRequest {
		t.Fatalf("empty binder: got %+v", res)
	}

	noIssued := validRequest(testNow)
	noIssued.TicketIssuedAt = time.Time{}
	if res := gate.Admit(context.Background(), noIssued); res.Allowed || res.Reason != ReasonInvalidRequest {
		t.Fatalf("zero issued at: got %+v", res)
	}
	if store.Len() != 0 {
		t.Fatalf("invalid request must not write store, len=%d", store.Len())
	}
}

func TestNewGate_InvalidConfig(t *testing.T) {
	cases := []Config{
		{Window: testWindow, Store: nil},
		{Window: 0, Store: NewMemoryStore()},
		{Window: -time.Second, Store: NewMemoryStore()},
		{Window: time.Duration(maxWindowMillis+1) * time.Millisecond, Store: NewMemoryStore()},
	}
	for idx, cfg := range cases {
		if _, err := NewGate(cfg); err == nil {
			t.Fatalf("case %d: expected error", idx)
		}
	}

	gate, err := NewGate(Config{Window: testWindow, Store: NewMemoryStore()})
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if gate.recordTTL != testWindow {
		t.Fatalf("default TTL = %v, want %v", gate.recordTTL, testWindow)
	}
	if gate.now == nil {
		t.Fatal("default clock not set")
	}
}

type faultStore struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (f *faultStore) CheckAndMark(_ context.Context, _ []byte, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	return true, nil
}

type atomicClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *atomicClock) get() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *atomicClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
