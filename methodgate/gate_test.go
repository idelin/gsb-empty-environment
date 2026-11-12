package methodgate

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func okHandler(v any) Handler {
	return func(context.Context, []any) (any, error) { return v, nil }
}

func newTestGate(t *testing.T, opts ...Option) *Gate {
	t.Helper()
	g := New(opts...)
	for _, m := range []Method{
		{Name: "demo.ping", Handler: okHandler("pong")},
		{Name: "demo.login", Perm: "auth", Handler: okHandler("token")},
		{Name: "admin.shutdown", Perm: "admin", Handler: okHandler("bye")},
		{Name: "café.menu", Handler: okHandler("coffee")}, // NFC é
	} {
		if err := g.Register(m); err != nil {
			t.Fatalf("Register(%s): %v", m.Name, err)
		}
	}
	return g
}

func subcall(name string, params ...any) map[string]any {
	return map[string]any{"methodName": name, "params": params}
}

func asFault(t *testing.T, v any) (code int) {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected fault struct, got %#v", v)
	}
	code, ok = m["faultCode"].(int)
	if !ok {
		t.Fatalf("fault struct missing faultCode: %#v", m)
	}
	if _, ok := m["faultString"].(string); !ok {
		t.Fatalf("fault struct missing faultString: %#v", m)
	}
	return code
}

func asResult(t *testing.T, v any) any {
	t.Helper()
	arr, ok := v.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("expected single-element result array, got %#v", v)
	}
	return arr[0]
}

// Exact-match whitelist: case, whitespace and Unicode variants must all miss.
func TestExactMatchRejectsVariants(t *testing.T) {
	g := newTestGate(t)
	ctx := context.Background()

	variants := []string{
		"Demo.ping",   // case variant
		"demo.Ping",   // case variant
		"DEMO.PING",   // case variant
		"demo.ping ",  // trailing space
		" demo.ping",  // leading space
		"demo.ping\n", // trailing newline
		"demo.\tping", // embedded tab
		"demo．ping",   // fullwidth full stop U+FF0E
		"ｄemo.ping",   // fullwidth d U+FF44
		"demo.​ping",  // zero-width space U+200B before "ping"
		"café.menu",  // NFD: e + combining acute U+0301
		"café.menu ",  // NFC name + trailing space
	}
	for _, name := range variants {
		if _, fault := g.Dispatch(ctx, name, nil); fault == nil {
			t.Errorf("variant %q was accepted, want rejection", name)
		} else if fault.Code != FaultMethodNotFound {
			t.Errorf("variant %q: fault code = %d, want %d", name, fault.Code, FaultMethodNotFound)
		}
	}

	// Sanity: the exact names still work.
	for _, name := range []string{"demo.ping", "café.menu"} {
		if _, fault := g.Dispatch(ctx, name, nil); fault != nil {
			t.Errorf("exact name %q rejected: %v", name, fault)
		}
	}
}

// Register must reject whitespace-bearing and reserved names.
func TestRegisterRejectsBadNames(t *testing.T) {
	g := New()
	for _, name := range []string{"", "demo. ping", "demo.ping ", "system.multicall", "system.listMethods"} {
		if err := g.Register(Method{Name: name, Handler: okHandler(nil)}); err == nil {
			t.Errorf("Register(%q) succeeded, want error", name)
		}
	}
}

// Nested system.multicall inside a multicall is rejected per subcall,
// while sibling subcalls still run.
func TestNestedMulticallRejected(t *testing.T) {
	g := newTestGate(t)
	nested := []any{subcall("demo.ping")}
	res, fault := g.Dispatch(context.Background(), MethodMulticall, []any{
		[]any{
			subcall("demo.ping"),
			map[string]any{"methodName": MethodMulticall, "params": []any{nested}},
			subcall("demo.ping"),
		},
	})
	if fault != nil {
		t.Fatalf("multicall rejected as a whole: %v", fault)
	}
	results, ok := res.([]any)
	if !ok || len(results) != 3 {
		t.Fatalf("expected 3 results, got %#v", res)
	}
	if got := asResult(t, results[0]); got != "pong" {
		t.Errorf("results[0] = %v, want pong", got)
	}
	if code := asFault(t, results[1]); code != FaultNestedMulticall {
		t.Errorf("nested multicall fault code = %d, want %d", code, FaultNestedMulticall)
	}
	if got := asResult(t, results[2]); got != "pong" {
		t.Errorf("results[2] = %v, want pong (subcalls after the fault must still run)", got)
	}
}

// Case variants of system.multicall must not reach the multicall handler.
func TestMulticallNameVariantsRejected(t *testing.T) {
	g := newTestGate(t)
	for _, name := range []string{"System.Multicall", "system.MultiCall", "system.multicall ", "system.multicall"} {
		_, fault := g.Dispatch(context.Background(), name, []any{[]any{subcall("demo.ping")}})
		if name == MethodMulticall {
			if fault != nil {
				t.Errorf("exact system.multicall rejected: %v", fault)
			}
			continue
		}
		if fault == nil || fault.Code != FaultMethodNotFound {
			t.Errorf("variant %q: fault = %v, want method-not-found", name, fault)
		}
	}
}

// More than 20 subcalls rejects the whole request; exactly 20 is fine.
func TestSubcallLimit(t *testing.T) {
	g := newTestGate(t)

	calls := make([]any, 21)
	for i := range calls {
		calls[i] = subcall("demo.ping")
	}
	_, fault := g.Dispatch(context.Background(), MethodMulticall, []any{calls})
	if fault == nil || fault.Code != FaultTooManySubcalls {
		t.Fatalf("21 subcalls: fault = %v, want FaultTooManySubcalls", fault)
	}

	calls = calls[:20]
	res, fault := g.Dispatch(context.Background(), MethodMulticall, []any{calls})
	if fault != nil {
		t.Fatalf("20 subcalls rejected: %v", fault)
	}
	if results := res.([]any); len(results) != 20 {
		t.Fatalf("got %d results, want 20", len(results))
	}

	// Custom limit via option.
	g2 := newTestGate(t, WithMaxSubcalls(2))
	_, fault = g2.Dispatch(context.Background(), MethodMulticall, []any{[]any{
		subcall("demo.ping"), subcall("demo.ping"), subcall("demo.ping"),
	}})
	if fault == nil || fault.Code != FaultTooManySubcalls {
		t.Fatalf("custom limit: fault = %v, want FaultTooManySubcalls", fault)
	}
}

// A failing subcall yields a fault struct; the rest of the batch continues.
func TestFailedSubcallDoesNotAbortBatch(t *testing.T) {
	g := newTestGate(t)
	if err := g.Register(Method{
		Name: "demo.boom",
		Handler: func(context.Context, []any) (any, error) {
			return nil, errors.New("kaboom")
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.Register(Method{
		Name: "demo.coded",
		Handler: func(context.Context, []any) (any, error) {
			return nil, &FaultError{Code: 42, Msg: "custom"}
		},
	}); err != nil {
		t.Fatal(err)
	}

	res, fault := g.Dispatch(context.Background(), MethodMulticall, []any{[]any{
		subcall("demo.ping"),
		subcall("demo.boom"),
		subcall("demo.coded"),
		subcall("no.such.method"),
		subcall("demo.ping"),
	}})
	if fault != nil {
		t.Fatalf("batch rejected: %v", fault)
	}
	results := res.([]any)
	if len(results) != 5 {
		t.Fatalf("got %d results, want 5", len(results))
	}
	if got := asResult(t, results[0]); got != "pong" {
		t.Errorf("results[0] = %v, want pong", got)
	}
	if code := asFault(t, results[1]); code != FaultInternal {
		t.Errorf("results[1] code = %d, want %d", code, FaultInternal)
	}
	if code := asFault(t, results[2]); code != 42 {
		t.Errorf("results[2] code = %d, want 42 (FaultError passthrough)", code)
	}
	if code := asFault(t, results[3]); code != FaultMethodNotFound {
		t.Errorf("results[3] code = %d, want %d", code, FaultMethodNotFound)
	}
	if got := asResult(t, results[4]); got != "pong" {
		t.Errorf("results[4] = %v, want pong", got)
	}
}

// Every auth-failed subcall costs exactly one token from the caller's
// single bucket, shared with top-level failures — bundling never discounts.
func TestRateLimitDeductionPerSubcall(t *testing.T) {
	bucket := NewBucket(5)
	g := newTestGate(t, WithRateLimiter(bucket))
	ctx := WithCaller(context.Background(), &Caller{ID: "attacker"})

	// 3 auth-failing subcalls in one multicall: 3 tokens, not 1.
	res, fault := g.Dispatch(ctx, MethodMulticall, []any{[]any{
		subcall("demo.login"), subcall("demo.login"), subcall("demo.login"),
	}})
	if fault != nil {
		t.Fatalf("batch rejected: %v", fault)
	}
	for i, r := range res.([]any) {
		if code := asFault(t, r); code != FaultForbidden {
			t.Errorf("subcall %d code = %d, want %d", i, code, FaultForbidden)
		}
	}
	if rem := bucket.Remaining("attacker"); rem != 2 {
		t.Fatalf("remaining = %d, want 2 (3 subcalls must cost 3 tokens)", rem)
	}

	// A top-level auth failure draws from the same bucket.
	if _, fault := g.Dispatch(ctx, "demo.login", nil); fault == nil || fault.Code != FaultForbidden {
		t.Fatalf("top-level: fault = %v, want FaultForbidden", fault)
	}
	if rem := bucket.Remaining("attacker"); rem != 1 {
		t.Fatalf("remaining = %d, want 1 (top-level shares the bucket)", rem)
	}

	// Last token consumed by a subcall...
	res, _ = g.Dispatch(ctx, MethodMulticall, []any{[]any{subcall("demo.login")}})
	if code := asFault(t, res.([]any)[0]); code != FaultForbidden {
		t.Fatalf("code = %d, want FaultForbidden", code)
	}
	// ...and the bucket is now empty: further attempts are rate limited.
	if _, fault := g.Dispatch(ctx, "demo.login", nil); fault == nil || fault.Code != FaultRateLimited {
		t.Fatalf("exhausted bucket: fault = %v, want FaultRateLimited", fault)
	}

	// A different caller has an independent bucket.
	other := WithCaller(context.Background(), &Caller{ID: "someone-else"})
	if _, fault := g.Dispatch(other, "demo.login", nil); fault == nil || fault.Code != FaultForbidden {
		t.Fatalf("other caller: fault = %v, want FaultForbidden", fault)
	}

	// Successful calls never touch the bucket.
	admin := WithCaller(context.Background(), &Caller{ID: "root", Perms: []string{"auth", "admin"}})
	for i := 0; i < 10; i++ {
		if _, fault := g.Dispatch(admin, "demo.login", nil); fault != nil {
			t.Fatalf("authorized call rejected: %v", fault)
		}
	}
	if rem := bucket.Remaining("root"); rem != 5 {
		t.Fatalf("root bucket = %d, want untouched 5", rem)
	}
}

// system.listMethods exposes only what the caller may invoke.
func TestListMethodsScopedToCaller(t *testing.T) {
	g := newTestGate(t)

	want := func(ctx context.Context, expected ...string) {
		t.Helper()
		res, fault := g.Dispatch(ctx, MethodListMethods, nil)
		if fault != nil {
			t.Fatalf("listMethods fault: %v", fault)
		}
		got := map[string]bool{}
		for _, v := range res.([]any) {
			got[v.(string)] = true
		}
		if len(got) != len(expected) {
			t.Fatalf("listMethods = %v, want %v", got, expected)
		}
		for _, name := range expected {
			if !got[name] {
				t.Errorf("listMethods missing %q (got %v)", name, got)
			}
		}
	}

	want(context.Background(),
		"demo.ping", "café.menu", MethodListMethods, MethodMulticall)
	want(WithCaller(context.Background(), &Caller{ID: "u", Perms: []string{"auth"}}),
		"demo.ping", "café.menu", "demo.login", MethodListMethods, MethodMulticall)
	want(WithCaller(context.Background(), &Caller{ID: "a", Perms: []string{"auth", "admin"}}),
		"demo.ping", "café.menu", "demo.login", "admin.shutdown", MethodListMethods, MethodMulticall)
}

// Concurrency smoke test: parallel multicalls and registrations.
func TestConcurrentUse(t *testing.T) {
	g := newTestGate(t, WithRateLimiter(NewBucket(1000)))
	done := make(chan struct{})
	for w := 0; w < 8; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			ctx := WithCaller(context.Background(), &Caller{ID: fmt.Sprint(w), Perms: []string{"auth"}})
			for i := 0; i < 50; i++ {
				if _, fault := g.Dispatch(ctx, MethodMulticall, []any{[]any{
					subcall("demo.ping"), subcall("demo.login"), subcall("nope"),
				}}); fault != nil {
					t.Errorf("worker %d: %v", w, fault)
					return
				}
			}
		}(w)
	}
	for w := 0; w < 8; w++ {
		<-done
	}
}
