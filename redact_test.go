package netsec

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const testBudget = 1 << 20

func mustRedact(t *testing.T, root *Value) []byte {
	t.Helper()
	out, err := Redact(root, testBudget)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	return out
}

// Changing the size, shape, and contents of the secret subgraph must
// not change a single byte of the public output, including reference
// numbering of public shared nodes.
func TestSecretSubgraphInvariance(t *testing.T) {
	build := func(secretPayload *Value) *Value {
		shared := Arr(Str("public-shared"))
		return Dict(map[string]*Value{
			"user":  Str("alice"),
			"again": shared,
			"copy":  shared,
			"token": Sec(secretPayload),
		})
	}

	small := Str("s3cr3t")

	big := Dict(map[string]*Value{
		"password": Str("a much longer and different secret value"),
		"nested":   Arr(Num(1), Num(2), Num(3), Str("deep")),
		"extra":    Dict(map[string]*Value{"k1": Str("v1"), "k2": Str("v2")}),
	})
	cycle := Arr()
	cycle.Elems = append(cycle.Elems, cycle)
	big.Dict["loop"] = cycle

	out1 := mustRedact(t, build(small))
	out2 := mustRedact(t, build(big))
	if !bytes.Equal(out1, out2) {
		t.Fatalf("public output changed with secret subgraph:\n%s\n%s", out1, out2)
	}

	want := `{"again":{"$id":1,"value":["public-shared"]},"copy":{"$ref":1},"token":"[REDACTED]","user":"alice"}`
	if string(out1) != want {
		t.Fatalf("got  %s\nwant %s", out1, want)
	}
}

// A node reachable from a secret parent stays confidential even when a
// public path aliases it (reverse alias): taint wins over sharing.
func TestReverseAliasTaintWins(t *testing.T) {
	shared := Str("top-secret")
	root := Dict(map[string]*Value{
		"public": shared,
		"alias":  RefTo(shared),
		"wrap":   Sec(Dict(map[string]*Value{"leak": shared})),
	})
	out := mustRedact(t, root)
	want := `{"alias":"[REDACTED]","public":"[REDACTED]","wrap":"[REDACTED]"}`
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
	if strings.Contains(string(out), "top-secret") || strings.Contains(string(out), "leak") {
		t.Fatalf("secret material leaked: %s", out)
	}
}

// A secret tag sitting on a reference node taints the resolved target
// and everything beyond it.
func TestSecretTagOnReference(t *testing.T) {
	target := Dict(map[string]*Value{"inner": Str("hidden")})
	root := Dict(map[string]*Value{
		"open": Str("visible"),
		"sec":  Sec(RefTo(target)),
	})
	out := mustRedact(t, root)
	want := `{"open":"visible","sec":"[REDACTED]"}`
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
}

func TestCyclesStableNumbering(t *testing.T) {
	self := Arr()
	self.Elems = append(self.Elems, self)
	out := mustRedact(t, self)
	if want := `{"$id":1,"value":[{"$ref":1}]}`; string(out) != want {
		t.Fatalf("self cycle: got  %s\nwant %s", out, want)
	}

	a := Dict(map[string]*Value{})
	b := Dict(map[string]*Value{})
	a.Dict["b"] = b
	b.Dict["a"] = a
	out = mustRedact(t, a)
	if want := `{"$id":1,"value":{"b":{"a":{"$ref":1}}}}`; string(out) != want {
		t.Fatalf("two-node cycle: got  %s\nwant %s", out, want)
	}
}

// A public cycle keeps its stable numbering when it merely contains a
// secret leaf; the leaf collapses without disturbing the $id sequence.
func TestCycleWithSecretLeaf(t *testing.T) {
	a := Dict(map[string]*Value{})
	b := Dict(map[string]*Value{"name": Sec(Str("hidden"))})
	a.Dict["b"] = b
	b.Dict["a"] = a
	out := mustRedact(t, a)
	want := `{"$id":1,"value":{"b":{"a":{"$ref":1},"name":"[REDACTED]"}}}`
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
}

// Any cycle member reachable from a secret tag taints the whole cycle
// (taint is forward-closed), so the public side collapses entirely and
// emits no reference ids that could hint at the hidden shape.
func TestCycleReachableFromSecretCollapses(t *testing.T) {
	a := Dict(map[string]*Value{})
	b := Dict(map[string]*Value{})
	a.Dict["next"] = b
	b.Dict["back"] = a
	root := Dict(map[string]*Value{
		"node": a,
		"sec":  Sec(b),
	})
	out := mustRedact(t, root)
	want := `{"node":"[REDACTED]","sec":"[REDACTED]"}`
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
	if strings.Contains(string(out), "$id") || strings.Contains(string(out), "$ref") {
		t.Fatalf("reference numbering leaked into output: %s", out)
	}
}

// Public sharing is preserved: the first occurrence carries the value
// with an $id, later occurrences are $ref aliases.
func TestPublicSharingPreserved(t *testing.T) {
	shared := Arr(Str("x"), Num(42))
	root := Dict(map[string]*Value{
		"first":  shared,
		"second": RefTo(shared),
		"third":  shared,
	})
	out := mustRedact(t, root)
	want := `{"first":{"$id":1,"value":["x",42]},"second":{"$ref":1},"third":{"$ref":1}}`
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
}

func TestDictKeyByteOrder(t *testing.T) {
	root := Dict(map[string]*Value{
		"z":  Num(1),
		"a":  Num(2),
		"b":  Num(3),
		"é":  Num(4),
		"aa": Num(5),
	})
	out := mustRedact(t, root)
	want := `{"a":2,"aa":5,"b":3,"z":1,"é":4}`
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
}

// Keys of a secret dict are confidential too: the whole dict collapses
// to the placeholder and no key string appears in the output.
func TestSecretDictKeysHidden(t *testing.T) {
	root := Dict(map[string]*Value{
		"public": Str("ok"),
		"vault": Sec(Dict(map[string]*Value{
			"api_key":    Str("AKIA..."),
			"private_ip": Str("10.0.0.8"),
		})),
	})
	out := mustRedact(t, root)
	for _, leak := range []string{"api_key", "private_ip", "AKIA", "10.0.0.8"} {
		if strings.Contains(string(out), leak) {
			t.Fatalf("secret dict material %q leaked: %s", leak, out)
		}
	}
	if want := `{"public":"ok","vault":"[REDACTED]"}`; string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
}

// Budget exhaustion rejects the whole output: an error is returned and
// no partial bytes escape.
func TestBudgetRejectsAllOutput(t *testing.T) {
	root := Dict(map[string]*Value{
		"a": Str("hello"),
		"b": Arr(Num(1), Num(2), Num(3)),
	})
	full, err := Redact(root, testBudget)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if _, err := Redact(root, len(full)); err != nil {
		t.Fatalf("exact budget should succeed: %v", err)
	}
	out, err := Redact(root, len(full)-1)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("want ErrBudgetExceeded, got %v", err)
	}
	if out != nil {
		t.Fatalf("partial output escaped: %q", out)
	}
}

// Serialization is deterministic across runs and graph construction
// orders for the same logical value.
func TestDeterministic(t *testing.T) {
	build := func() *Value {
		shared := Str("shared")
		return Dict(map[string]*Value{
			"k2": Arr(shared, Num(7), RefTo(shared)),
			"k1": Sec(Str("x")),
		})
	}
	first := mustRedact(t, build())
	for i := 0; i < 8; i++ {
		if got := mustRedact(t, build()); !bytes.Equal(first, got) {
			t.Fatalf("run %d differs:\n%s\n%s", i, first, got)
		}
	}
}

// Reference-only cycles carry no concrete value and encode as null
// instead of looping forever.
func TestReferenceOnlyCycle(t *testing.T) {
	r1 := &Value{Kind: KindRef}
	r2 := &Value{Kind: KindRef}
	r1.Ref = r2
	r2.Ref = r1
	out := mustRedact(t, Dict(map[string]*Value{"r": r1}))
	if want := `{"r":null}`; string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
}

// Deeply nested adversarial graphs must not overflow the stack.
func TestDeepGraphNoStackOverflow(t *testing.T) {
	const depth = 200000
	root := Arr()
	cur := root
	for i := 0; i < depth; i++ {
		next := Arr()
		cur.Elems = append(cur.Elems, next)
		cur = next
	}
	cur.Elems = append(cur.Elems, Sec(Str("bottom")))
	out := mustRedact(t, root)
	if want := 2*(depth+1) + len(`"[REDACTED]"`); len(out) != want {
		t.Fatalf("unexpected output length %d, want %d", len(out), want)
	}
}
