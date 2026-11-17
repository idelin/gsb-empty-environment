package errsan_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.org/netsec/q-020/errsan"
)

func testRegistry(t *testing.T) *errsan.Registry {
	t.Helper()
	reg := errsan.NewRegistry()
	specs := []errsan.CodeSpec{
		{Code: "auth_invalid_token", Priority: 10, Params: []errsan.ParamSpec{
			{Name: "realm", Type: errsan.ParamString},
			{Name: "attempt", Type: errsan.ParamInt},
		}},
		{Code: "auth_expired", Priority: 10, Params: []errsan.ParamSpec{
			{Name: "expired", Type: errsan.ParamBool},
		}},
		{Code: "auth_rate_limited", Priority: 20, Params: []errsan.ParamSpec{
			{Name: "retry_after", Type: errsan.ParamInt},
		}},
		{Code: "user_lookup_failed", Priority: 5},
	}
	for _, s := range specs {
		if err := reg.Register(s); err != nil {
			t.Fatalf("register %s: %v", s.Code, err)
		}
	}
	return reg
}

func TestPrivateMessageDoesNotAffectOutput(t *testing.T) {
	reg := testRegistry(t)
	build := func(msg string) *errsan.Node {
		return errsan.NewNode("auth_invalid_token", msg,
			map[string]any{"realm": "admin", "attempt": 3})
	}
	a := errsan.Sanitize(build("token signature mismatch kid=7f3a"), reg, errsan.Options{})
	b := errsan.Sanitize(build("completely different secret detail"), reg, errsan.Options{})
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("private message leaked into output: %+v vs %+v", a, b)
	}
}

func TestPrivateSubgraphDoesNotAffectOutput(t *testing.T) {
	reg := testRegistry(t)
	public := func() *errsan.Node {
		return errsan.NewNode("auth_rate_limited", "x",
			map[string]any{"retry_after": 30})
	}
	plain := errsan.Sanitize(public(), reg, errsan.Options{})

	root := public()
	priv := errsan.NewNode("db_password_leak", "pw=hunter2", nil)
	priv.AddCause(errsan.NewNode("stack_frame", "secret", nil))
	root.AddCause(priv)
	withPriv := errsan.Sanitize(root, reg, errsan.Options{})

	if !reflect.DeepEqual(plain, withPriv) {
		t.Fatalf("private subgraph changed output: %+v vs %+v", plain, withPriv)
	}
}

func TestLegalPublicParamsPreserved(t *testing.T) {
	reg := testRegistry(t)
	root := errsan.NewNode("auth_invalid_token", "m",
		map[string]any{"realm": "admin", "attempt": 3})
	got := errsan.Sanitize(root, reg, errsan.Options{})
	want := errsan.PublicError{
		Code:   "auth_invalid_token",
		Params: map[string]any{"realm": "admin", "attempt": int64(3)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestUnknownCodeMapsToFailure(t *testing.T) {
	reg := testRegistry(t)
	root := errsan.NewNode("totally_unknown", "m", nil,
		errsan.NewNode("also_unknown", "m", nil))
	got := errsan.Sanitize(root, reg, errsan.Options{})
	if got.Code != errsan.FailureCode || got.Params != nil {
		t.Fatalf("expected bare failure, got %+v", got)
	}
}

func TestPriorityAndTieBreak(t *testing.T) {
	reg := testRegistry(t)
	// Higher priority wins regardless of graph position.
	root := errsan.NewNode("auth_rate_limited", "m", nil,
		errsan.NewNode("auth_invalid_token", "m", nil))
	if got := errsan.Sanitize(root, reg, errsan.Options{}); got.Code != "auth_rate_limited" {
		t.Fatalf("priority not respected: %s", got.Code)
	}
	// Equal priority: lexicographically smaller code wins.
	root = errsan.NewNode("auth_invalid_token", "m", nil,
		errsan.NewNode("auth_expired", "m", nil))
	if got := errsan.Sanitize(root, reg, errsan.Options{}); got.Code != "auth_expired" {
		t.Fatalf("tie-break not respected: %s", got.Code)
	}
}

func TestTraversalOrderIndependence(t *testing.T) {
	reg := testRegistry(t)
	leaves := func() (a, b, c *errsan.Node) {
		return errsan.NewNode("auth_invalid_token", "m1", map[string]any{"realm": "a"}),
			errsan.NewNode("auth_expired", "m2", map[string]any{"expired": true}),
			errsan.NewNode("user_lookup_failed", "m3", nil)
	}
	a1, b1, c1 := leaves()
	root1 := errsan.NewNode("unknown", "m", nil, a1, b1, c1)
	a2, b2, c2 := leaves()
	root2 := errsan.NewNode("unknown", "m", nil, c2, a2, b2)

	got1 := errsan.Sanitize(root1, reg, errsan.Options{})
	got2 := errsan.Sanitize(root2, reg, errsan.Options{})
	if !reflect.DeepEqual(got1, got2) {
		t.Fatalf("traversal order changed output: %+v vs %+v", got1, got2)
	}
}

func TestCyclesTerminate(t *testing.T) {
	reg := testRegistry(t)
	done := make(chan errsan.PublicError, 1)
	go func() {
		a := errsan.NewNode("auth_invalid_token", "m", map[string]any{"realm": "x"})
		b := errsan.NewNode("unknown", "m", nil)
		a.AddCause(b)
		b.AddCause(a) // cycle
		b.AddCause(b) // self cycle
		done <- errsan.Sanitize(a, reg, errsan.Options{})
	}()
	select {
	case got := <-done:
		if got.Code != "auth_invalid_token" {
			t.Fatalf("unexpected code %s", got.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sanitize did not terminate on cyclic graph")
	}
}

func TestSharedNodesCountedOnce(t *testing.T) {
	reg := testRegistry(t)
	shared := errsan.NewNode("auth_expired", "m", map[string]any{"expired": true})
	root := errsan.NewNode("unknown", "m", nil)
	for i := 0; i < 100; i++ {
		root.AddCause(shared) // diamond-style sharing
	}
	got := errsan.Sanitize(root, reg, errsan.Options{MaxNodes: 10})
	if got.Code != "auth_expired" {
		t.Fatalf("shared nodes should be deduplicated, got %+v", got)
	}
}

func TestBudgetExceededYieldsFailure(t *testing.T) {
	reg := testRegistry(t)

	// Node budget: long chain of distinct nodes.
	const n = 5000
	nodes := make([]*errsan.Node, n)
	for i := n - 1; i >= 0; i-- {
		var causes []*errsan.Node
		if i+1 < n {
			causes = []*errsan.Node{nodes[i+1]}
		}
		nodes[i] = errsan.NewNode("unknown", "m", nil, causes...)
	}
	got := errsan.Sanitize(nodes[0], reg, errsan.Options{MaxNodes: 100, MaxDepth: 100000})
	if got.Code != errsan.FailureCode {
		t.Fatalf("node budget not enforced: %+v", got)
	}

	// Depth budget.
	got = errsan.Sanitize(nodes[0], reg, errsan.Options{MaxDepth: 50, MaxNodes: 100000})
	if got.Code != errsan.FailureCode {
		t.Fatalf("depth budget not enforced: %+v", got)
	}

	// Param-count budget.
	params := map[string]any{}
	for i := 0; i < 50; i++ {
		params[fmt.Sprintf("k%d", i)] = "v"
	}
	root := errsan.NewNode("auth_invalid_token", "m", params)
	got = errsan.Sanitize(root, reg, errsan.Options{MaxParamsPerNode: 10})
	if got.Code != errsan.FailureCode {
		t.Fatalf("param budget not enforced: %+v", got)
	}
}

func TestMaliciousCycleDoesNotExhaustResources(t *testing.T) {
	reg := testRegistry(t)
	// Dense cyclic graph: every node points back to the first node.
	const n = 20000
	first := errsan.NewNode("unknown", "m", nil)
	prev := first
	for i := 1; i < n; i++ {
		cur := errsan.NewNode("unknown", "m", nil, first)
		prev.AddCause(cur)
		prev = cur
	}
	prev.AddCause(first)

	done := make(chan errsan.PublicError, 1)
	go func() {
		done <- errsan.Sanitize(first, reg, errsan.Options{MaxNodes: 1000, MaxDepth: 1000})
	}()
	select {
	case got := <-done:
		if got.Code != errsan.FailureCode || got.Params != nil {
			t.Fatalf("expected unified failure, got %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("malicious cycle exhausted resources")
	}
}

func TestObjectReferencesInParamsDropped(t *testing.T) {
	reg := testRegistry(t)
	root := errsan.NewNode("auth_invalid_token", "m", map[string]any{
		"realm":   "admin",
		"attempt": 3,
		"evil":    map[string]any{"ptr": &struct{}{}},
		"node":    errsan.NewNode("auth_expired", "nested", nil),
		"list":    []string{"a"},
	})
	got := errsan.Sanitize(root, reg, errsan.Options{})
	if got.Code != "auth_invalid_token" {
		t.Fatalf("unexpected code %s", got.Code)
	}
	want := map[string]any{"realm": "admin", "attempt": int64(3)}
	if !reflect.DeepEqual(got.Params, want) {
		t.Fatalf("got params %+v want %+v", got.Params, want)
	}
}

func TestStrictTypeValidation(t *testing.T) {
	reg := errsan.NewRegistry()
	if err := reg.Register(errsan.CodeSpec{Code: "c", Priority: 1, Params: []errsan.ParamSpec{
		{Name: "s", Type: errsan.ParamString},
		{Name: "i", Type: errsan.ParamInt},
		{Name: "b", Type: errsan.ParamBool},
	}}); err != nil {
		t.Fatal(err)
	}
	root := errsan.NewNode("c", "m", map[string]any{
		"s": 42,     // wrong type
		"i": 3.14,   // float is not int
		"b": "true", // string is not bool
	})
	got := errsan.Sanitize(root, reg, errsan.Options{})
	if got.Code != "c" || len(got.Params) != 0 {
		t.Fatalf("wrong-typed params must be dropped: %+v", got)
	}
}

func TestOversizedStringDropped(t *testing.T) {
	reg := testRegistry(t)
	root := errsan.NewNode("auth_invalid_token", "m",
		map[string]any{"realm": strings.Repeat("a", 10000), "attempt": 1})
	got := errsan.Sanitize(root, reg, errsan.Options{MaxStringLen: 16})
	if _, ok := got.Params["realm"]; ok {
		t.Fatalf("oversized string kept: %+v", got.Params)
	}
	if got.Params["attempt"] != int64(1) {
		t.Fatalf("valid param lost: %+v", got.Params)
	}
}

func TestNilInputs(t *testing.T) {
	reg := testRegistry(t)
	if got := errsan.Sanitize(nil, reg, errsan.Options{}); got.Code != errsan.FailureCode {
		t.Fatalf("nil root: %+v", got)
	}
	root := errsan.NewNode("auth_expired", "m", nil)
	if got := errsan.Sanitize(root, nil, errsan.Options{}); got.Code != errsan.FailureCode {
		t.Fatalf("nil registry: %+v", got)
	}
}

func TestNoPrivateFieldsInSerializedOutput(t *testing.T) {
	reg := testRegistry(t)
	secret := "db-password-hunter2"
	root := errsan.NewNode("auth_invalid_token", secret,
		map[string]any{"realm": "admin"},
		errsan.NewNode("unknown", secret, nil))
	got := errsan.Sanitize(root, reg, errsan.Options{})
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("private message leaked: %s", raw)
	}
	// PublicError must expose exactly Code and Params.
	typ := reflect.TypeOf(errsan.PublicError{})
	if typ.NumField() != 2 {
		t.Fatalf("PublicError grew unexpected fields: %v", typ)
	}
}

func TestRegistryRejectsInvalidSpecs(t *testing.T) {
	reg := errsan.NewRegistry()
	cases := []errsan.CodeSpec{
		{Code: ""},
		{Code: errsan.FailureCode},
		{Code: "x", Params: []errsan.ParamSpec{{Name: "a", Type: errsan.ParamString}, {Name: "a", Type: errsan.ParamInt}}},
		{Code: "y", Params: []errsan.ParamSpec{{Name: "", Type: errsan.ParamString}}},
	}
	for i, c := range cases {
		if err := reg.Register(c); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
	if err := reg.Register(errsan.CodeSpec{Code: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(errsan.CodeSpec{Code: "ok"}); err == nil {
		t.Fatal("duplicate code accepted")
	}
}
