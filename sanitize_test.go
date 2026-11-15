package netsec

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := NewRegistry(
		CodeSpec{
			Code:     "AUTH_TOKEN_EXPIRED",
			Priority: 10,
			Params: []ParamSpec{
				{Name: "realm", Kind: KindString, MaxLen: 64},
				{Name: "retry_after", Kind: KindInt},
				{Name: "refreshable", Kind: KindBool},
			},
		},
		CodeSpec{Code: "AUTH_BAD_SIGNATURE", Priority: 10},
		CodeSpec{Code: "AUTH_RATE_LIMITED", Priority: 20},
		CodeSpec{Code: "AUTH_UNSUPPORTED", Priority: 1},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

// Mutating any private message or any private (unregistered) subgraph
// must not change the external output.
func TestPrivateMutationDoesNotChangeOutput(t *testing.T) {
	r := testRegistry(t)

	build := func(secret string, privateSub *Node) *Node {
		leaf := NewNode("AUTH_TOKEN_EXPIRED", secret, map[string]any{"realm": "sso"})
		mid := NewNode("db.timeout.v9", "private: "+secret, nil, leaf, privateSub)
		return NewNode("AUTH_UNSUPPORTED", "top secret: "+secret, nil, mid)
	}

	a := build("alice-password-hash", NewNode("internal.sql", "schema=accounts", nil))
	b := build("mallory-api-key", NewNode("internal.sql", "schema=secrets", nil,
		NewNode("kernel.panic", "addr=0xdeadbeef", nil)))

	gotA := r.Sanitize(a, Budget{})
	gotB := r.Sanitize(b, Budget{})
	if !reflect.DeepEqual(gotA, gotB) {
		t.Fatalf("private mutation changed output:\n a=%+v\n b=%+v", gotA, gotB)
	}
	if gotA.Code != "AUTH_TOKEN_EXPIRED" {
		t.Fatalf("expected AUTH_TOKEN_EXPIRED, got %q", gotA.Code)
	}
}

// Selection is by (priority desc, code asc) over the set of registered
// codes, independent of graph shape and traversal order.
func TestSelectionIgnoresTraversalOrder(t *testing.T) {
	r := testRegistry(t)

	expired := NewNode("AUTH_TOKEN_EXPIRED", "m1", nil)
	badSig := NewNode("AUTH_BAD_SIGNATURE", "m2", nil)
	rateLimited := NewNode("AUTH_RATE_LIMITED", "m3", nil)
	unknown := NewNode("totally.unknown", "m4", nil)

	want := r.Sanitize(NewNode("root", "x", nil, expired, badSig, rateLimited, unknown), Budget{})
	if want.Code != "AUTH_RATE_LIMITED" {
		t.Fatalf("highest priority should win, got %q", want.Code)
	}

	// Same code set, different shapes and cause orders.
	variants := []*Node{
		NewNode("root", "x", nil, unknown, rateLimited, badSig, expired),
		NewNode("root", "x", nil, NewNode("wrap", "w", nil, rateLimited, expired), badSig, unknown),
		NewNode("AUTH_RATE_LIMITED", "m3", nil, expired, badSig),
	}
	for i, v := range variants {
		if got := r.Sanitize(v, Budget{}); got.Code != want.Code {
			t.Fatalf("variant %d: traversal order changed selection: %q", i, got.Code)
		}
	}

	// Equal priority: lexicographically smaller code wins.
	tie := NewNode("root", "x", nil, badSig, expired)
	if got := r.Sanitize(tie, Budget{}); got.Code != "AUTH_BAD_SIGNATURE" {
		t.Fatalf("tie should break to smaller code, got %q", got.Code)
	}
	reversed := NewNode("root", "x", nil, expired, badSig)
	if got := r.Sanitize(reversed, Budget{}); got.Code != "AUTH_BAD_SIGNATURE" {
		t.Fatalf("reversed order changed tie-break: %q", got.Code)
	}
}

// Unknown codes and empty graphs map to the unified failure.
func TestUnknownAndEmptyMapToUnifiedFailure(t *testing.T) {
	r := testRegistry(t)
	cases := map[string]*Node{
		"nil root":        nil,
		"unknown only":    NewNode("evil.code", "secret", map[string]any{"realm": "x"}),
		"unknown chain":   NewNode("a", "s1", nil, NewNode("b", "s2", nil, NewNode("c", "s3", nil))),
		"empty code node": NewNode("", "blank", nil),
	}
	for name, root := range cases {
		got := r.Sanitize(root, Budget{})
		if got.Code != FailureCode || got.Params != nil {
			t.Fatalf("%s: expected bare %s, got %+v", name, FailureCode, got)
		}
	}
}

// Only whitelisted, strictly typed parameters survive; embedded object
// references, floats, oversized strings and unlisted keys are stripped.
func TestParamWhitelistAndStrictTypes(t *testing.T) {
	r := testRegistry(t)
	root := NewNode("AUTH_TOKEN_EXPIRED", "secret", map[string]any{
		"realm":       "sso",
		"retry_after": 30,
		"refreshable": true,
		"password":    "hunter2",
		"session":     map[string]any{"id": 1},
		"node_ref":    NewNode("x", "y", nil),
		"stack":       []string{"f1", "f2"},
	})
	got := r.Sanitize(root, Budget{})
	want := PublicError{
		Code: "AUTH_TOKEN_EXPIRED",
		Params: map[string]any{
			"realm":       "sso",
			"retry_after": int64(30),
			"refreshable": true,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	// Wrong types are dropped individually; valid ones survive.
	bad := NewNode("AUTH_TOKEN_EXPIRED", "s", map[string]any{
		"realm":       strings.Repeat("a", 65),
		"retry_after": 30.5,
		"refreshable": "yes",
	})
	got = r.Sanitize(bad, Budget{})
	if got.Code != "AUTH_TOKEN_EXPIRED" || got.Params != nil {
		t.Fatalf("all-invalid params should yield bare code, got %+v", got)
	}
}

// Cycles, self-loops, and massive shared subgraphs must terminate
// quickly within budget; depth and node explosions yield the failure.
func TestMaliciousGraphsDoNotExhaustResources(t *testing.T) {
	r := testRegistry(t)
	done := make(chan struct{})

	go func() {
		defer close(done)

		// Self-loop and mutual cycle with a valid code inside.
		self := NewNode("AUTH_TOKEN_EXPIRED", "loop", map[string]any{"realm": "sso"})
		self.AddCause(self)
		a := NewNode("x", "a", nil)
		b := NewNode("y", "b", nil, a)
		a.AddCause(b, self)
		if got := r.Sanitize(a, Budget{}); got.Code != "AUTH_TOKEN_EXPIRED" {
			t.Errorf("cycle: got %+v", got)
		}

		// Diamond DAG: 40 levels of 2-way sharing, 2^40 paths, 41 nodes.
		leaf := NewNode("AUTH_RATE_LIMITED", "leaf", nil)
		top := leaf
		for i := 0; i < 40; i++ {
			top = NewNode("dup", "d", nil, top, top)
		}
		if got := r.Sanitize(top, Budget{}); got.Code != "AUTH_RATE_LIMITED" {
			t.Errorf("diamond: got %+v", got)
		}

		// Deep chain beyond MaxDepth maps to the unified failure.
		deep := NewNode("AUTH_TOKEN_EXPIRED", "bottom", nil)
		for i := 0; i < 5000; i++ {
			deep = NewNode("wrap", "w", nil, deep)
		}
		if got := r.Sanitize(deep, Budget{MaxDepth: 64, MaxNodes: 100000}); got.Code != FailureCode {
			t.Errorf("deep chain: got %+v", got)
		}

		// Wide graph beyond MaxNodes maps to the unified failure.
		wide := NewNode("root", "w", nil)
		for i := 0; i < 5000; i++ {
			wide.AddCause(NewNode(fmt.Sprintf("c%d", i), "w", nil))
		}
		if got := r.Sanitize(wide, Budget{MaxNodes: 128, MaxDepth: 100000}); got.Code != FailureCode {
			t.Errorf("wide graph: got %+v", got)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sanitization did not terminate within 5s")
	}
}

// Serialized output must contain only the public code and whitelisted
// params, never messages, causes, or stack fields.
func TestOutputContainsNoPrivateFields(t *testing.T) {
	r := testRegistry(t)
	root := NewNode("AUTH_TOKEN_EXPIRED", "private db password: s3cret",
		map[string]any{"realm": "sso"},
		NewNode("internal", "stack frame at auth.go:123", nil))
	got := r.Sanitize(root, Budget{})

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	for _, leak := range []string{"s3cret", "msg", "message", "stack", "cause", "auth.go"} {
		if strings.Contains(s, leak) {
			t.Fatalf("output %s leaks %q", s, leak)
		}
	}
	if got.Error() != "AUTH_TOKEN_EXPIRED" {
		t.Fatalf("Error() leaked detail: %q", got.Error())
	}

	// The unified failure serializes to nothing but the code.
	raw, _ = json.Marshal(r.Sanitize(nil, Budget{}))
	if string(raw) != `{"code":"INTERNAL_FAILURE"}` {
		t.Fatalf("failure output = %s", raw)
	}
}

// Registry construction rejects ambiguous allowlists.
func TestRegistryValidation(t *testing.T) {
	if _, err := NewRegistry(CodeSpec{Code: ""}); err == nil {
		t.Fatal("empty code accepted")
	}
	if _, err := NewRegistry(CodeSpec{Code: "A"}, CodeSpec{Code: "A"}); err == nil {
		t.Fatal("duplicate code accepted")
	}
	if _, err := NewRegistry(CodeSpec{Code: "A", Params: []ParamSpec{
		{Name: "p", Kind: KindString}, {Name: "p", Kind: KindInt},
	}}); err == nil {
		t.Fatal("duplicate param accepted")
	}
}
