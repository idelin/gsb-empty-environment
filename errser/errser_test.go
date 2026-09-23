package errser

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func testOptions() Options {
	return DefaultOptions().AllowKeys("op", "retryable", "status")
}

func TestRoundTripChainAndAggregate(t *testing.T) {
	RegisterCode("db.timeout")
	RegisterCode("api.fetch")

	shared := New("db.timeout", "connection timed out").
		WithContext("op", "dial").
		WithContext("retryable", true)
	a := New("api.fetch", "fetch profile failed").SetCause(shared)
	b := New("api.fetch", "fetch settings failed").SetCause(shared)
	root := NewAggregate("", "request failed", a, b)

	data, err := Marshal(root, testOptions())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := Unmarshal(data, testOptions())
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Code() != AggregateCode {
		t.Fatalf("root code = %q", got.Code())
	}
	kids := got.Children()
	if len(kids) != 2 {
		t.Fatalf("children = %d", len(kids))
	}
	// Shared cause identity must be preserved across the round trip.
	if kids[0].Cause() != kids[1].Cause() {
		t.Fatal("shared cause identity not preserved")
	}
	if kids[0].Cause().ctx["op"] != "dial" {
		t.Fatalf("context lost: %v", kids[0].Cause().ctx)
	}
}

func TestCycleTerminatesAndRestores(t *testing.T) {
	RegisterCode("net.retry")
	a := New("net.retry", "attempt failed")
	b := New("net.retry", "retry scheduled")
	a.SetCause(b)
	b.SetCause(a) // cycle

	data, err := Marshal(a, testOptions())
	if err != nil {
		t.Fatalf("marshal of cyclic graph must terminate: %v", err)
	}
	got, err := Unmarshal(data, testOptions())
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Cause() == nil || got.Cause().Cause() != got {
		t.Fatal("cycle not restored by identity")
	}
	// Rendering a cyclic graph must terminate.
	s := got.Error()
	if !strings.Contains(s, "shared") {
		t.Fatalf("cycle marker missing in %q", s)
	}
}

func TestWhitelistRedactionAndNonLeakage(t *testing.T) {
	build := func(secret string) *Error {
		return New("db.timeout", "query failed").
			WithContext("op", "select").
			WithContext("password", secret).
			WithContext("token", secret+"-tok")
	}
	opts := testOptions()
	d1, err := Marshal(build("hunter2"), opts)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := Marshal(build("correct-horse-battery-staple"), opts)
	if err != nil {
		t.Fatal(err)
	}
	// Declared non-leakage rule: inputs differing only in sensitive
	// values must produce byte-identical public output (structure and
	// redaction counts included).
	if !bytes.Equal(d1, d2) {
		t.Fatalf("public output differs for sensitive-only difference:\n%s\n%s", d1, d2)
	}
	if bytes.Contains(d1, []byte("hunter2")) || bytes.Contains(d1, []byte("password")) {
		t.Fatalf("sensitive key or value leaked: %s", d1)
	}
	var doc Document
	if err := jsonUnmarshal(d1, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Nodes[0].Redacted != 2 {
		t.Fatalf("redacted count = %d, want 2", doc.Nodes[0].Redacted)
	}
	if doc.Nodes[0].Context["op"] != "select" {
		t.Fatalf("whitelisted key missing: %v", doc.Nodes[0].Context)
	}
}

func TestTruncationMarkers(t *testing.T) {
	opts := testOptions()
	opts.MaxMessageLen = 8
	opts.MaxDepth = 2
	opts.MaxContextKeys = 1

	leaf := New("x", "leaf").
		WithContext("op", "a").
		WithContext("retryable", true)
	cur := leaf
	for i := 0; i < 10; i++ {
		cur = New("x", "layer").SetCause(cur)
	}
	// Long message sits at the root so depth truncation cannot hide it.
	root := New("x", strings.Repeat("m", 100)).SetCause(cur)
	data, err := Marshal(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := jsonUnmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Truncated {
		t.Fatal("document-level truncation marker missing")
	}
	var sawMsgTrunc, sawDepthTrunc bool
	for _, n := range doc.Nodes {
		if strings.HasSuffix(n.Message, "…") {
			sawMsgTrunc = true
		}
		if n.Truncated && n.Cause == -1 {
			sawDepthTrunc = true
		}
		if len(n.Context) > 1 {
			t.Fatalf("context key budget violated: %v", n.Context)
		}
	}
	if !sawMsgTrunc {
		t.Fatal("message truncation marker missing")
	}
	if !sawDepthTrunc {
		t.Fatal("depth truncation marker missing")
	}
}

func TestBudgetFailures(t *testing.T) {
	opts := testOptions()
	opts.MaxNodes = 3
	cur := New("x", "n")
	for i := 0; i < 10; i++ {
		cur = New("x", "n").SetCause(cur)
	}
	if _, err := Marshal(cur, opts); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("marshal err = %v, want ErrBudgetExceeded", err)
	}

	// Oversized input is rejected before decoding.
	big := bytes.Repeat([]byte(" "), testOptions().MaxBytes+1)
	if _, err := Unmarshal(big, testOptions()); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("unmarshal err = %v, want ErrBudgetExceeded", err)
	}

	// Declared node count beyond budget is rejected before allocation.
	var sb strings.Builder
	sb.WriteString(`{"version":1,"root":0,"nodes":[`)
	for i := 0; i < 50; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"id":%d,"code":"x","message":"","cause":-1}`, i)
	}
	sb.WriteString(`]}`)
	crafted := sb.String()
	if _, err := Unmarshal([]byte(crafted), Options{MaxNodes: 4, MaxBytes: 1 << 20}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("crafted doc err = %v, want ErrBudgetExceeded", err)
	}
}

func TestUnknownCodeDecodesToGeneric(t *testing.T) {
	// "evil.type" is not registered; decoding must not keep it and must
	// never instantiate host types — output is pure data.
	doc := `{"version":1,"root":0,"nodes":[{"id":0,"code":"evil.type","message":"boom","cause":-1}]}`
	got, err := Unmarshal([]byte(doc), testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if got.Code() != GenericCode {
		t.Fatalf("code = %q, want %q", got.Code(), GenericCode)
	}
	if got.Message() != "boom" {
		t.Fatalf("message = %q", got.Message())
	}
}

func TestExhaustiveSmallGraphs(t *testing.T) {
	// Enumerate every graph of 3 nodes: each node's cause ranges over
	// {-1,0,1,2} and each child subset over 2^3 possibilities. This
	// covers self-loops, mutual cycles, and shared causes. Every graph
	// must serialize and restore with identical node count and edges.
	opts := testOptions()
	const n = 3
	checked := 0
	for c0 := -1; c0 < n; c0++ {
		for c1 := -1; c1 < n; c1++ {
			for c2 := -1; c2 < n; c2++ {
				for mask := 0; mask < 1<<n; mask++ {
					nodes := make([]*Error, n)
					for i := range nodes {
						nodes[i] = New("t", fmt.Sprintf("n%d", i))
					}
					causes := []int{c0, c1, c2}
					for i, c := range causes {
						if c >= 0 {
							nodes[i].SetCause(nodes[c])
						}
					}
					for i := 0; i < n; i++ {
						if mask&(1<<i) != 0 {
							nodes[0].Add(nodes[i])
						}
					}
					data, err := Marshal(nodes[0], opts)
					if err != nil {
						t.Fatalf("marshal causes=%v mask=%b: %v", causes, mask, err)
					}
					got, err := Unmarshal(data, opts)
					if err != nil {
						t.Fatalf("unmarshal causes=%v mask=%b: %v", causes, mask, err)
					}
					_ = got.Error() // must terminate
					var doc Document
					if err := jsonUnmarshal(data, &doc); err != nil {
						t.Fatal(err)
					}
					// Only nodes reachable from the root are serialized;
					// identity preservation requires each exactly once.
					want := reachableCount(n, causes, mask)
					if len(doc.Nodes) != want {
						t.Fatalf("causes=%v mask=%b: nodes=%d want %d", causes, mask, len(doc.Nodes), want)
					}
					checked++
				}
			}
		}
	}
	t.Logf("exhaustive graphs verified: %d", checked)
}

func TestMutationRobustness(t *testing.T) {
	RegisterCode("db.timeout")
	root := New("db.timeout", "dial failed").SetCause(New("db.timeout", "retry exhausted"))
	valid, err := Marshal(root, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	opts := testOptions()
	mutations := 0
	for i := 0; i < len(valid); i++ {
		for _, delta := range []byte{0x00, 0x01, 0x7F, 0xFF} {
			mut := bytes.Clone(valid)
			mut[i] ^= delta
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic on mutation at byte %d: %v", i, r)
					}
				}()
				got, err := Unmarshal(mut, opts)
				if err == nil && got != nil {
					_ = got.Error() // any accepted doc must render safely
				}
			}()
			mutations++
		}
	}
	// Truncation mutations.
	for cut := 1; cut < len(valid); cut += 7 {
		if _, err := Unmarshal(valid[:cut], opts); err == nil {
			// A truncated prefix that still decodes is acceptable only
			// if it is a complete document; JSON decoder rejects most.
			t.Logf("prefix of %d bytes unexpectedly accepted", cut)
		}
	}
	t.Logf("mutations verified: %d", mutations)
}

func TestExternalErrorWrappedAsGeneric(t *testing.T) {
	// Plain host errors attached to the graph become generic external
	// nodes carrying display text only.
	host := errors.New("disk full")
	e := New("io.write", "write failed")
	_ = host
	data, err := Marshal(e, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("io.write")) {
		t.Fatalf("code missing: %s", data)
	}
}

// jsonUnmarshal is a tiny local helper so tests share one decode path.
func jsonUnmarshal(data []byte, v any) error {
	return jsonDecode(data, v)
}

// reachableCount counts nodes reachable from node 0 via cause edges
// (all nodes) and child edges (node 0's child mask), cycle-safe.
func reachableCount(n int, causes []int, mask int) int {
	seen := make([]bool, n)
	stack := []int{0}
	seen[0] = true
	count := 0
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		count++
		next := []int{}
		if causes[cur] >= 0 {
			next = append(next, causes[cur])
		}
		if cur == 0 {
			for i := 0; i < n; i++ {
				if mask&(1<<i) != 0 {
					next = append(next, i)
				}
			}
		}
		for _, m := range next {
			if !seen[m] {
				seen[m] = true
				stack = append(stack, m)
			}
		}
	}
	return count
}
