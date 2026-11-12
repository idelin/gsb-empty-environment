package netsec

import (
	"errors"
	"sync"
	"testing"
)

// recorder 是测试用的原生函数:记录每次触达的帧与参数。
// 安全闸门的核心断言就是:越权调用时 calls 必须保持为空。
type recorder struct {
	mu    sync.Mutex
	calls []recordedCall
}

type recordedCall struct {
	binding string
	args    []any
}

func (r *recorder) fn(binding string) NativeFunc {
	return func(args []any) (any, error) {
		r.mu.Lock()
		r.calls = append(r.calls, recordedCall{binding: binding, args: args})
		r.mu.Unlock()
		return "ok:" + binding, nil
	}
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func mustGrant(t *testing.T, table *CapabilityTable, binding string, origin Origin, frameType FrameType, fn NativeFunc) {
	t.Helper()
	if err := table.Grant(binding, origin, frameType, fn); err != nil {
		t.Fatalf("Grant(%q) unexpected error: %v", binding, err)
	}
}

func TestSubframeCapabilityIsolation(t *testing.T) {
	table := NewCapabilityTable()
	rec := &recorder{}
	app := NewTupleOrigin("https", "app.example.com", "")
	evil := NewTupleOrigin("https", "evil.example.com", "")

	// 同一绑定只对 app 来源的主帧与子帧分别授权,且子帧授权独立登记。
	mustGrant(t, table, "host.readFile", app, FrameMain, rec.fn("host.readFile"))
	mustGrant(t, table, "host.readFile", app, FrameSubframe, rec.fn("host.readFile"))

	main := NewFrame("frame-main", FrameMain)
	main.Navigate(NavigationRequest{URL: "https://app.example.com/home", Initiator: app})
	sub := NewFrame("frame-sub", FrameSubframe)
	sub.Navigate(NavigationRequest{URL: "https://app.example.com/widget", Initiator: app})

	if _, err := table.Invoke("host.readFile", main, nil); err != nil {
		t.Fatalf("main frame authorized call failed: %v", err)
	}
	if _, err := table.Invoke("host.readFile", sub, nil); err != nil {
		t.Fatalf("subframe authorized call failed: %v", err)
	}
	if rec.count() != 2 {
		t.Fatalf("native function should have been reached twice, got %d", rec.count())
	}

	// 跨来源子帧:即使嵌在同一页面,evil 来源也没有该能力。
	evilSub := NewFrame("frame-evil-sub", FrameSubframe)
	evilSub.Navigate(NavigationRequest{URL: "https://evil.example.com/ad", Initiator: evil})
	if _, err := table.Invoke("host.readFile", evilSub, nil); !errors.Is(err, ErrCapabilityNotRegistered) {
		t.Fatalf("cross-origin subframe must be denied with ErrCapabilityNotRegistered, got %v", err)
	}

	// 只授权子帧的绑定不得在主帧使用,反之亦然。
	mustGrant(t, table, "host.childOnly", app, FrameSubframe, rec.fn("host.childOnly"))
	if _, err := table.Invoke("host.childOnly", main, nil); !errors.Is(err, ErrCapabilityNotRegistered) {
		t.Fatalf("subframe-only binding must not be callable from main frame, got %v", err)
	}

	if rec.count() != 2 {
		t.Fatalf("denied calls must not reach the native function, call count = %d", rec.count())
	}
}

func TestAboutBlankAndSrcdocInheritInitiatorCapability(t *testing.T) {
	table := NewCapabilityTable()
	rec := &recorder{}
	app := NewTupleOrigin("https", "app.example.com", "")
	mustGrant(t, table, "host.ping", app, FrameSubframe, rec.fn("host.ping"))

	sub := NewFrame("frame-sub", FrameSubframe)
	sub.Navigate(NavigationRequest{URL: "about:blank", Initiator: app})
	if got := sub.Origin(); !got.Equal(app) {
		t.Fatalf("about:blank subframe must inherit initiator origin, got %s", got)
	}
	if _, err := table.Invoke("host.ping", sub, nil); err != nil {
		t.Fatalf("about:blank inheriting a granted initiator origin should be allowed: %v", err)
	}

	sub.Navigate(NavigationRequest{URL: "about:srcdoc", Initiator: app})
	if _, err := table.Invoke("host.ping", sub, nil); err != nil {
		t.Fatalf("srcdoc inheriting a granted initiator origin should be allowed: %v", err)
	}
	if rec.count() != 2 {
		t.Fatalf("expected two native calls through inherited origins, got %d", rec.count())
	}

	// 没有可信发起者的 about:blank 是不透明来源,默认无权。
	orphan := NewFrame("frame-orphan", FrameSubframe)
	orphan.Navigate(NavigationRequest{URL: "about:blank"})
	if !orphan.Origin().IsOpaque() {
		t.Fatalf("about:blank without initiator must be opaque")
	}
	if _, err := table.Invoke("host.ping", orphan, nil); !errors.Is(err, ErrOpaqueOrigin) {
		t.Fatalf("opaque about:blank must be denied with ErrOpaqueOrigin, got %v", err)
	}
	if rec.count() != 2 {
		t.Fatalf("opaque denial must not reach native function, count = %d", rec.count())
	}
}

func TestBlobInheritsCreatorOrigin(t *testing.T) {
	table := NewCapabilityTable()
	rec := &recorder{}
	app := NewTupleOrigin("https", "app.example.com", "")
	other := NewTupleOrigin("https", "other.example.net", "")
	mustGrant(t, table, "host.blobApi", app, FrameMain, rec.fn("host.blobApi"))

	frame := NewFrame("frame-blob", FrameMain)

	// blob URL 内嵌 app 创建者来源:即使由其它来源脚本触发导航,
	// 文档有效来源仍是创建者 app,能力随创建者走。
	frame.Navigate(NavigationRequest{
		URL:       "blob:https://app.example.com/11111111-2222-3333-4444-555555555555",
		Initiator: other,
	})
	if got := frame.Origin(); !got.Equal(app) {
		t.Fatalf("blob document must take the embedded creator origin, got %s", got)
	}
	if _, err := table.Invoke("host.blobApi", frame, []any{"x"}); err != nil {
		t.Fatalf("blob inheriting creator capability should be allowed: %v", err)
	}

	// 无内嵌来源的 blob 回退到发起者来源。
	frame.Navigate(NavigationRequest{URL: "blob:null/abcd", Initiator: app})
	if got := frame.Origin(); !got.Equal(app) {
		t.Fatalf("blob without embedded origin must fall back to initiator, got %s", got)
	}
	if _, err := table.Invoke("host.blobApi", frame, nil); err != nil {
		t.Fatalf("blob via initiator inheritance should be allowed: %v", err)
	}

	// 既无内嵌来源也无可信发起者:不透明来源,拒绝且不触达原生函数。
	before := rec.count()
	frame.Navigate(NavigationRequest{URL: "blob:null/abcd"})
	if !frame.Origin().IsOpaque() {
		t.Fatalf("blob with no creator and no initiator must be opaque")
	}
	if _, err := table.Invoke("host.blobApi", frame, nil); !errors.Is(err, ErrOpaqueOrigin) {
		t.Fatalf("opaque blob must be denied with ErrOpaqueOrigin, got %v", err)
	}
	if rec.count() != before {
		t.Fatalf("opaque blob denial must not reach the native function")
	}
}

func TestDataAndOpaqueOriginsDeniedByDefault(t *testing.T) {
	table := NewCapabilityTable()
	rec := &recorder{}
	app := NewTupleOrigin("https", "app.example.com", "")

	// 登记侧:不透明来源无法被授权。
	if err := table.Grant("host.x", OpaqueOrigin(), FrameMain, rec.fn("host.x")); !errors.Is(err, ErrOpaqueOrigin) {
		t.Fatalf("granting capability to opaque origin must fail, got %v", err)
	}
	mustGrant(t, table, "host.x", app, FrameMain, rec.fn("host.x"))

	frame := NewFrame("frame-data", FrameMain)
	frame.Navigate(NavigationRequest{URL: "data:text/html;base64,PGgxPmhkPC9oMT4=", Initiator: app})
	if !frame.Origin().IsOpaque() {
		t.Fatalf("data document must resolve to an opaque origin")
	}
	if _, err := table.Invoke("host.x", frame, nil); !errors.Is(err, ErrOpaqueOrigin) {
		t.Fatalf("data document call must be denied with ErrOpaqueOrigin, got %v", err)
	}

	// 不透明来源上也不允许绑定回调。
	if _, err := table.Bind("host.x", frame); !errors.Is(err, ErrOpaqueOrigin) {
		t.Fatalf("binding on opaque origin must fail, got %v", err)
	}
	if rec.count() != 0 {
		t.Fatalf("no native call should ever occur from an opaque origin, got %d", rec.count())
	}
}

func TestStaleCallbackAfterNavigationCannotReachNative(t *testing.T) {
	table := NewCapabilityTable()
	rec := &recorder{}
	app := NewTupleOrigin("https", "app.example.com", "")
	partner := NewTupleOrigin("https", "partner.example.com", "")
	mustGrant(t, table, "host.save", app, FrameMain, rec.fn("host.save"))
	mustGrant(t, table, "host.save", partner, FrameMain, rec.fn("host.save"))

	frame := NewFrame("frame-nav", FrameMain)
	frame.Navigate(NavigationRequest{URL: "https://app.example.com/a", Initiator: app})

	cb, err := table.Bind("host.save", frame)
	if err != nil {
		t.Fatalf("initial bind failed: %v", err)
	}
	boundGeneration := frame.Generation()

	// 导航前:旧回调正常工作。
	if _, err := cb.Call(frame, []any{"v1"}); err != nil {
		t.Fatalf("callback before navigation should work: %v", err)
	}

	// 导航到另一个同样被授权的来源:静态授权仍然成立,
	// 但旧回调的代次已经过期,必须在闸门处失效。
	frame.Navigate(NavigationRequest{URL: "https://partner.example.com/b", Initiator: partner})
	if frame.Generation() == boundGeneration {
		t.Fatal("navigation must advance the frame generation")
	}
	if _, err := cb.Call(frame, []any{"v2"}); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale callback must fail with ErrStaleGeneration, got %v", err)
	}

	// 直接 Invoke 走的是当前代次的新文档授权,不受旧回调影响。
	if _, err := table.Invoke("host.save", frame, []any{"v3"}); err != nil {
		t.Fatalf("fresh invoke on the new document should be allowed: %v", err)
	}

	// 再导航到无权的 data: 文档,新代次回调同样拿不到。
	frame.Navigate(NavigationRequest{URL: "data:text/html,nowhere", Initiator: partner})
	if _, err := table.Bind("host.save", frame); !errors.Is(err, ErrOpaqueOrigin) {
		t.Fatalf("binding into a data document must fail, got %v", err)
	}

	// 只有导航前的 1 次与新文档 Invoke 的 1 次触达了原生函数,
	// 过期回调的 v2 与 data 文档的绑定尝试都不得触达。
	if rec.count() != 2 {
		t.Fatalf("exactly two calls should reach native function, got %d (%+v)", rec.count(), rec.calls)
	}
}

func TestCallbackBoundToFrameCannotCrossFrames(t *testing.T) {
	table := NewCapabilityTable()
	rec := &recorder{}
	app := NewTupleOrigin("https", "app.example.com", "")
	mustGrant(t, table, "host.cross", app, FrameMain, rec.fn("host.cross"))

	frameA := NewFrame("frame-a", FrameMain)
	frameA.Navigate(NavigationRequest{URL: "https://app.example.com/a", Initiator: app})
	frameB := NewFrame("frame-b", FrameMain)
	frameB.Navigate(NavigationRequest{URL: "https://app.example.com/b", Initiator: app})

	cb, err := table.Bind("host.cross", frameA)
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	if _, err := cb.Call(frameB, nil); !errors.Is(err, ErrFrameMismatch) {
		t.Fatalf("callback used on another frame must fail with ErrFrameMismatch, got %v", err)
	}
	if rec.count() != 0 {
		t.Fatalf("cross-frame callback must not reach the native function")
	}
}

func TestRevokeTakesEffectOnNextCall(t *testing.T) {
	table := NewCapabilityTable()
	rec := &recorder{}
	app := NewTupleOrigin("https", "app.example.com", "")
	mustGrant(t, table, "host.revokable", app, FrameMain, rec.fn("host.revokable"))

	frame := NewFrame("frame-revoke", FrameMain)
	frame.Navigate(NavigationRequest{URL: "https://app.example.com/", Initiator: app})
	cb, err := table.Bind("host.revokable", frame)
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}

	table.Revoke("host.revokable", app, FrameMain)
	if err := table.Check("host.revokable", frame); !errors.Is(err, ErrCapabilityNotRegistered) {
		t.Fatalf("revoked capability check should fail, got %v", err)
	}
	if _, err := cb.Call(frame, nil); !errors.Is(err, ErrCapabilityNotRegistered) {
		t.Fatalf("callback after revoke must be denied at re-check, got %v", err)
	}
	if rec.count() != 0 {
		t.Fatalf("revoked callback must not reach the native function")
	}
}
