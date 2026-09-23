package errser

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

var allowAll = map[string]bool{"host": true, "field": true, "user": true, "retry": true}

func mustEncode(t *testing.T, root error) []byte {
	t.Helper()
	data, err := Marshal(root, allowAll)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return data
}

func mustDecode(t *testing.T, data []byte) error {
	t.Helper()
	root, err := Decode(data, Limits{})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return root
}

func TestRoundTripChain(t *testing.T) {
	root := New(KindValidation, "bad input").With("field", "email").
		Wrap(New(KindTimeout, "db dial").With("host", "db.internal"))
	data := mustEncode(t, root)
	got := mustDecode(t, data)

	e, ok := got.(*Error)
	if !ok {
		t.Fatalf("root type %T", got)
	}
	if e.Kind() != KindValidation || e.Context()["field"] != "email" {
		t.Fatalf("root mismatch: %+v", e)
	}
	cause, ok := e.Unwrap().(*Error)
	if !ok || cause.Kind() != KindTimeout || cause.Context()["host"] != "db.internal" {
		t.Fatalf("cause mismatch: %+v", e.Unwrap())
	}
	// Stable kind survives: errors.Is works across the wire.
	if !errors.Is(got, New(KindValidation, "")) {
		t.Fatal("errors.Is by kind failed after round trip")
	}
	// Re-encoding is byte-identical (canonical form).
	if again := mustEncode(t, got); string(again) != string(data) {
		t.Fatalf("re-encode differs:\n%s\n%s", again, data)
	}
}

func TestSharedCauseIdentityPreserved(t *testing.T) {
	shared := New(KindTimeout, "dial").With("host", "h1")
	left := New(KindInternal, "left").Wrap(shared)
	root := NewAggregate("batch", left, shared) // diamond: shared reachable twice

	got := mustDecode(t, mustEncode(t, root))
	agg, ok := got.(*Aggregate)
	if !ok {
		t.Fatalf("root type %T", got)
	}
	kids := agg.Errors()
	viaLeft := kids[0].(*Error).Unwrap()
	direct := kids[1]
	if viaLeft != direct {
		t.Fatal("shared cause lost identity: want same pointer")
	}

	var env Envelope
	if err := json.Unmarshal(mustEncode(t, root), &env); err != nil {
		t.Fatal(err)
	}
	refs := 0
	for _, n := range env.Nodes {
		if n.Ref != nil && !n.Cycle {
			refs++
		}
	}
	if refs != 1 {
		t.Fatalf("want exactly 1 shared back-reference, got %d", refs)
	}
}

func TestCycleTerminatesAndRecovers(t *testing.T) {
	a := New(KindInternal, "worker")
	b := New(KindInternal, "supervisor").Wrap(a)
	a.Wrap(b) // cycle a -> b -> a

	data := mustEncode(t, a) // must terminate
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	cycleRefs := 0
	for _, n := range env.Nodes {
		if n.Ref != nil && n.Cycle {
			cycleRefs++
		}
	}
	if cycleRefs != 1 {
		t.Fatalf("want 1 cycle marker, got %d", cycleRefs)
	}

	got := mustDecode(t, data)
	ga := got.(*Error)
	gb := ga.Unwrap().(*Error)
	if gb.Unwrap() != ga {
		t.Fatal("decoded graph lost its cycle")
	}
	_ = ga.Error() // must terminate
	if again := mustEncode(t, got); string(again) != string(data) {
		t.Fatalf("cycle round trip not stable:\n%s\n%s", again, data)
	}
}

// Declared non-leakage rule: two inputs differing only in
// non-whitelisted (sensitive) values produce byte-identical public
// output — same structure, same counts.
func TestNonLeakage(t *testing.T) {
	build := func(password, token string) error {
		leaf := New(KindAuth, "login failed").
			With("user", "alice").      // whitelisted
			With("password", password). // sensitive
			With("token", token)        // sensitive
		return NewAggregate("attempt", leaf, New(KindValidation, "check").With("field", "user"))
	}
	d1 := mustEncode(t, build("hunter2", "tok-aaa"))
	d2 := mustEncode(t, build("correct-horse-battery-staple", "tok-zzz-different"))
	if string(d1) != string(d2) {
		t.Fatalf("sensitive values leaked into public output:\n%s\n%s", d1, d2)
	}
	for _, secret := range []string{"hunter2", "correct-horse", "tok-aaa", "tok-zzz", "password", "token"} {
		if strings.Contains(string(d1), secret) {
			t.Fatalf("output contains sensitive material %q", secret)
		}
	}
	var env Envelope
	if err := json.Unmarshal(d1, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Nodes) != 3 {
		t.Fatalf("node count = %d, want 3", len(env.Nodes))
	}
}

func TestWhitelistAndValueSanitization(t *testing.T) {
	type secret struct{ s string }
	root := New(KindInternal, "boom").
		With("host", "ok").
		With("password", "p4ss").
		With("obj", secret{"x"}) // non-scalar: placeholder, String() never called
	data, err := (Encoder{Allow: map[string]bool{"host": true, "obj": true}}).Encode(root)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "p4ss") || strings.Contains(s, "password") {
		t.Fatalf("sensitive field leaked: %s", s)
	}
	if !strings.Contains(s, "errser.secret") && !strings.Contains(s, "<errser.secret>") {
		t.Fatalf("non-scalar should become type placeholder: %s", s)
	}
}

func TestUnknownKindDecodesToOpaque(t *testing.T) {
	// Attacker-controlled kind naming a host type must not be instantiated.
	payload := `{"v":1,"root":0,"nodes":[{"id":0,"kind":"os/exec.Command","msg":"rm -rf /"}]}`
	got, err := Decode([]byte(payload), Limits{})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	e, ok := got.(*Error)
	if !ok {
		t.Fatalf("unknown kind decoded to %T", got)
	}
	if e.Kind() != KindOpaque {
		t.Fatalf("kind = %q, want generic %q", e.Kind(), KindOpaque)
	}
	if e.Error() != "[opaque] rm -rf /" {
		t.Fatalf("display text not preserved: %q", e.Error())
	}
}

func TestForeignErrorEncodesAsOpaque(t *testing.T) {
	root := New(KindInternal, "wrap").Wrap(fmt.Errorf("plain foreign: %s", "disk full"))
	got := mustDecode(t, mustEncode(t, root))
	cause := got.(*Error).Unwrap().(*Error)
	if cause.Kind() != KindOpaque {
		t.Fatalf("foreign kind = %q, want opaque", cause.Kind())
	}
	if !strings.Contains(cause.Error(), "disk full") {
		t.Fatalf("foreign message lost: %q", cause.Error())
	}
}

func TestBudgetStrictFails(t *testing.T) {
	root := New(KindInternal, "d0")
	cur := root
	for i := 1; i < 10; i++ {
		n := New(KindInternal, fmt.Sprintf("d%d", i))
		cur.Wrap(n)
		cur = n
	}
	enc := Encoder{Allow: allowAll, Strict: true, Limits: Limits{MaxDepth: 3}}
	if _, err := enc.Encode(root); !errors.Is(err, ErrBudget) {
		t.Fatalf("strict depth: err = %v, want ErrBudget", err)
	}
	enc = Encoder{Allow: allowAll, Strict: true, Limits: Limits{MaxNodes: 2}}
	if _, err := enc.Encode(root); !errors.Is(err, ErrBudget) {
		t.Fatalf("strict nodes: err = %v, want ErrBudget", err)
	}
}

func TestBudgetTruncationMarkers(t *testing.T) {
	root := New(KindInternal, "d0")
	cur := root
	for i := 1; i < 10; i++ {
		n := New(KindInternal, fmt.Sprintf("d%d", i))
		cur.Wrap(n)
		cur = n
	}
	data, err := (Encoder{Allow: allowAll, Limits: Limits{MaxDepth: 3}}).Encode(root)
	if err != nil {
		t.Fatalf("non-strict encode: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	last := env.Nodes[len(env.Nodes)-1]
	if last.Kind != string(KindTruncated) || last.Trunc != "depth" {
		t.Fatalf("want depth truncation marker, got %+v", last)
	}
	// Marker survives a round trip.
	got := mustDecode(t, data)
	if !strings.Contains(got.Error(), "[truncated: depth]") {
		t.Fatalf("truncation marker lost: %q", got.Error())
	}
	if again := mustEncode(t, got); string(again) != string(data) {
		t.Fatal("truncated graph round trip not stable")
	}
}

func TestMessageTruncation(t *testing.T) {
	long := strings.Repeat("x", 1000)
	data, err := (Encoder{Limits: Limits{MaxMsgLen: 10}}).Encode(New(KindInternal, long))
	if err != nil {
		t.Fatal(err)
	}
	got := mustDecode(t, data)
	msg := got.(*Error).Error()
	if !strings.HasPrefix(msg, "[internal] xxxxxxxxxx…") {
		t.Fatalf("message not truncated with marker: %q", msg)
	}
}

// TestExhaustiveSmallGraphs enumerates every cause graph on 3 nodes
// (each node's cause is nil or one of the 3 nodes: 4^3 = 64 graphs,
// covering chains, self-loops, shared causes and cycles) and checks
// termination, decodability and byte-stable round trips.
func TestExhaustiveSmallGraphs(t *testing.T) {
	for mask := 0; mask < 64; mask++ {
		nodes := []*Error{
			New(KindInternal, "n0"),
			New(KindTimeout, "n1"),
			New(KindAuth, "n2"),
		}
		m := mask
		for i := 0; i < 3; i++ {
			choice := m % 4
			m /= 4
			if choice > 0 {
				nodes[i].Wrap(nodes[choice-1])
			}
		}
		root := nodes[0]
		data, err := Marshal(root, nil)
		if err != nil {
			t.Fatalf("graph %d: encode: %v", mask, err)
		}
		got, err := Decode(data, Limits{})
		if err != nil {
			t.Fatalf("graph %d: decode: %v", mask, err)
		}
		if s := got.Error(); len(s) > 10000 {
			t.Fatalf("graph %d: Error() runaway, len=%d", mask, len(s))
		}
		again, err := Marshal(got, nil)
		if err != nil {
			t.Fatalf("graph %d: re-encode: %v", mask, err)
		}
		if string(again) != string(data) {
			t.Fatalf("graph %d: round trip not stable:\n%s\n%s", mask, data, again)
		}
	}
}

// TestMutation flips and truncates valid envelopes: decoding must never
// panic, never execute host code, and any accepted mutation must itself
// round-trip stably.
func TestMutation(t *testing.T) {
	shared := New(KindTimeout, "t").With("host", "h")
	a := New(KindInternal, "a").Wrap(shared)
	b := New(KindInternal, "b").Wrap(a)
	a.Wrap(b) // cycle
	corpus := [][]byte{
		mustEncode(t, NewAggregate("agg", a, shared, New(KindValidation, "v").With("field", "f"))),
		mustEncode(t, New(KindInternal, strings.Repeat("m", 100))),
	}

	check := func(mut []byte) {
		got, err := Decode(mut, Limits{})
		if err != nil {
			return // rejection is fine
		}
		_ = got.Error() // must terminate
		again, err := Marshal(got, allowAll)
		if err != nil {
			t.Fatalf("re-encode of accepted mutation failed: %v", err)
		}
		if _, err := Decode(again, Limits{}); err != nil {
			t.Fatalf("own output rejected: %v", err)
		}
	}

	for _, base := range corpus {
		for i := 0; i <= len(base); i++ {
			check(base[:i]) // every prefix
		}
		for i := 0; i < len(base); i++ {
			for _, xor := range []byte{0x01, 0x7f, 0xff} {
				mut := append([]byte(nil), base...)
				mut[i] ^= xor
				check(mut)
			}
		}
		// Kind-string swaps toward dangerous-looking names.
		for _, evil := range []string{"os/exec.Command", "runtime.Func", "net/http.Client"} {
			mut := []byte(strings.Replace(string(base), `"kind":"internal"`, `"kind":"`+evil+`"`, 1))
			check(mut)
		}
	}
}

func TestLengthValidatedBeforeAllocation(t *testing.T) {
	// Oversize input rejected before parsing.
	big := make([]byte, 2<<20)
	if _, err := Decode(big, Limits{MaxInputBytes: 1 << 20}); err == nil {
		t.Fatal("oversize input accepted")
	}
	// Declared node count above limit rejected before graph building.
	var sb strings.Builder
	sb.WriteString(`{"v":1,"root":0,"nodes":[`)
	for i := 0; i < 2000; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"id":%d,"kind":"internal","msg":"x"}`, i)
	}
	sb.WriteString(`]}`)
	if _, err := Decode([]byte(sb.String()), Limits{MaxNodes: 1024}); !errors.Is(err, ErrBudget) {
		t.Fatalf("node flood: err = %v, want ErrBudget", err)
	}
	// Out-of-range edges rejected.
	bad := []string{
		`{"v":1,"root":5,"nodes":[{"id":0,"kind":"internal"}]}`,
		`{"v":1,"root":0,"nodes":[{"id":0,"kind":"internal","cause":7}]}`,
		`{"v":1,"root":0,"nodes":[{"id":0,"kind":"aggregate","children":[0]}]}`,
		`{"v":1,"root":0,"nodes":[{"id":0,"ref":0}]}`, // self ref
		`{"v":2,"root":0,"nodes":[{"id":0,"kind":"internal"}]}`,
		`{"v":1,"root":0,"nodes":[{"id":1,"kind":"internal"}]}`, // non-sequential id
	}
	for _, p := range bad {
		if _, err := Decode([]byte(p), Limits{}); err == nil {
			t.Fatalf("accepted invalid envelope: %s", p)
		}
	}
}

func TestAggregateRoundTrip(t *testing.T) {
	root := NewAggregate("three failed",
		New(KindValidation, "v1").With("field", "a"),
		New(KindTimeout, "t1"),
		New(KindAuth, "a1"),
	)
	got := mustDecode(t, mustEncode(t, root))
	agg := got.(*Aggregate)
	if len(agg.Errors()) != 3 {
		t.Fatalf("children = %d", len(agg.Errors()))
	}
	if got.Error() == "" || !strings.Contains(got.Error(), "three failed") {
		t.Fatalf("bad render: %q", got.Error())
	}
}
