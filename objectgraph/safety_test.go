package objectgraph

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

// buildSelfCycle encodes a 2-node graph: node0 object {self:node1}, node1
// object {back:node0}, root 0. Both are forward/backward declared.
func buildTwoNodeCycle(t *testing.T) []byte {
	b := NewBuilder()
	n0 := b.Declare()
	n1 := b.Declare()
	if err := b.SetObject(n0, []KV{{Key: "next", Ref: n1}}); err != nil {
		t.Fatal(err)
	}
	if err := b.SetObject(n1, []KV{{Key: "prev", Ref: n0}}); err != nil {
		t.Fatal(err)
	}
	return mustEnc(t, b, n0)
}

func TestCycleRoundTripAndIdentity(t *testing.T) {
	v, err := Decode(buildTwoNodeCycle(t))
	if err != nil {
		t.Fatalf("decode cycle: %v", err)
	}
	nxt := v.Field("next")
	if nxt == nil {
		t.Fatal("missing next")
	}
	if nxt.Field("prev") != v {
		t.Fatal("cycle must return the same root entity (pointer identity)")
	}
}

func TestCycleDepthIsOne(t *testing.T) {
	adj := [][]int{{1}, {0}}
	st, err := AnalyzeGraph(adj, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.Depth != 1 {
		t.Fatalf("single SCC depth = %d, want 1", st.Depth)
	}
	if st.CyclicNodes != 2 {
		t.Fatalf("cyclic nodes = %d, want 2", st.CyclicNodes)
	}
}

func TestSelfLoopCountsOneCyclicNode(t *testing.T) {
	adj := [][]int{{0}}
	st, err := AnalyzeGraph(adj, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.Depth != 1 || st.CyclicNodes != 1 {
		t.Fatalf("depth=%d cyclic=%d", st.Depth, st.CyclicNodes)
	}
}

func TestChainDepthOrderIndependent(t *testing.T) {
	// 0->1->2->3 with an extra back edge 3->1. SCCs: {0},{1,2,3}.
	adj := [][]int{{1}, {2}, {3}, {1}}
	st, err := AnalyzeGraph(adj, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.Depth != 2 {
		t.Fatalf("depth = %d, want 2", st.Depth)
	}
	if st.CyclicNodes != 3 {
		t.Fatalf("cyclic = %d, want 3", st.CyclicNodes)
	}
	// Root at 1: only the SCC {1,2,3} is reachable.
	st2, err := AnalyzeGraph(adj, 1)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Depth != 1 || len(st2.Unreachable) != 1 {
		t.Fatalf("depth=%d unreachable=%v", st2.Depth, st2.Unreachable)
	}
}

func TestForwardRefsStagedAndResolved(t *testing.T) {
	// Root index 2; record order 0,1,2 means root points back, and node0
	// points forward to node1. Decoding must stage and resolve both.
	b := NewBuilder()
	n0 := b.Declare()
	n1 := b.Declare()
	n2 := b.Declare()
	if err := b.SetArray(n0, []int{n1}); err != nil {
		t.Fatal(err)
	}
	if err := b.SetString(n1, "deep"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetObject(n2, []KV{{Key: "child", Ref: n0}}); err != nil {
		t.Fatal(err)
	}
	v, err := Decode(mustEnc(t, b, n2))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v.Field("child").Elems()[0].Text() != "deep" {
		t.Fatal("forward reference not resolved")
	}
}

func TestPendingForwardRefCap(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxPendingRefs = 2
	d, err := NewDecoder(lim)
	if err != nil {
		t.Fatal(err)
	}
	// node0 array fans out to nodes 1..5: four forward edges staged before
	// any target is parsed, exceeding cap 2.
	b := NewBuilder()
	targets := []int{}
	first := b.Declare()
	for i := 0; i < 4; i++ {
		targets = append(targets, b.AddInt(int64(i)))
	}
	if err := b.SetArray(first, targets); err != nil {
		t.Fatal(err)
	}
	p := mustEnc(t, b, first)
	if _, err := d.Decode(p); !errors.Is(err, ErrPendingRefs) {
		t.Fatalf("err = %v, want ErrPendingRefs", err)
	}
}

func TestSmallInputManyRefsRefBudget(t *testing.T) {
	// A tiny byte payload can declare an array with many repeated references
	// to node0 itself; the reference budget must still bound it.
	lim := DefaultLimits()
	lim.MaxRefs = 8
	d, err := NewDecoder(lim)
	if err != nil {
		t.Fatal(err)
	}
	b := NewBuilder()
	n0 := b.Declare()
	refs := []int{n0, n0, n0, n0, n0, n0, n0, n0, n0, n0}
	if err := b.SetArray(n0, refs); err != nil {
		t.Fatal(err)
	}
	p := mustEnc(t, b, n0)
	if _, err := d.Decode(p); !errors.Is(err, ErrRefLimit) {
		t.Fatalf("err = %v, want ErrRefLimit", err)
	}
}

func TestNodeAndCyclicQuotaAndDepth(t *testing.T) {
	t.Run("node limit", func(t *testing.T) {
		lim := DefaultLimits()
		lim.MaxNodes = 2
		d, _ := NewDecoder(lim)
		b := NewBuilder()
		a := b.AddNull()
		b.AddNull()
		b.AddNull()
		if _, err := d.Decode(mustEnc(t, b, a)); !errors.Is(err, ErrNodeLimit) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("cyclic quota", func(t *testing.T) {
		lim := DefaultLimits()
		lim.MaxCyclicNodes = 1
		d, _ := NewDecoder(lim)
		if _, err := d.Decode(buildTwoNodeCycle(t)); !errors.Is(err, ErrCyclicNodes) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("depth quota", func(t *testing.T) {
		lim := DefaultLimits()
		lim.MaxDepth = 3
		d, _ := NewDecoder(lim)
		b := NewBuilder()
		n4 := b.AddNull()
		n3 := b.AddArray([]int{n4})
		n2 := b.AddArray([]int{n3})
		n1 := b.AddArray([]int{n2})
		n0 := b.AddArray([]int{n1})
		if _, err := d.Decode(mustEnc(t, b, n0)); !errors.Is(err, ErrDepth) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestLengthValidatedBeforeAllocation(t *testing.T) {
	// Declare array length 1000000 but provide zero reference bytes. The
	// length is rejected before any 1,000,000-capacity slice is retained.
	var buf []byte
	buf = append(buf, magic0, magic1, magic2, version1)
	var tb [binary.MaxVarintLen64]byte
	k := binary.PutUvarint(tb[:], 1)
	buf = append(buf, tb[:k]...) // nodeCount 1
	k = binary.PutUvarint(tb[:], 0)
	buf = append(buf, tb[:k]...) // root 0
	buf = append(buf, tagArray)
	k = binary.PutUvarint(tb[:], 1_000_000)
	buf = append(buf, tb[:k]...)
	// no references follow -> also exceeds container cap
	d, _ := NewDecoder(DefaultLimits())
	if _, err := d.Decode(buf); !errors.Is(err, ErrBadLength) {
		t.Fatalf("err = %v, want ErrBadLength", err)
	}

	// Declared string length larger than remaining input must fail as
	// truncated without handing back a string.
	buf2 := []byte{magic0, magic1, magic2, version1}
	buf2 = append(buf2, tb[:1]...) // nodeCount placeholder, rebuild properly
	buf2 = buildTruncatedString()
	if _, err := d.Decode(buf2); err == nil {
		t.Fatal("want error for truncated string")
	}
}

func buildTruncatedString() []byte {
	var buf []byte
	buf = append(buf, magic0, magic1, magic2, version1)
	var tb [binary.MaxVarintLen64]byte
	k := binary.PutUvarint(tb[:], 1)
	buf = append(buf, tb[:k]...)
	k = binary.PutUvarint(tb[:], 0)
	buf = append(buf, tb[:k]...)
	buf = append(buf, tagString)
	k = binary.PutUvarint(tb[:], 50)
	buf = append(buf, tb[:k]...)
	buf = append(buf, 'x') // only 1 of 50 claimed bytes
	return buf
}

func TestMalformedInputsNoLeak(t *testing.T) {
	d, _ := NewDecoder(DefaultLimits())
	good := buildTwoNodeCycle(t)

	bad := map[string][]byte{
		"empty":          nil,
		"short":          {magic0, magic1},
		"badmagic":       append([]byte{'X', 'X', 'X', version1}, good[4:]...),
		"badversion":     append([]byte{magic0, magic1, magic2, 9}, good[4:]...),
		"truncated":      good[:len(good)-3],
		"trailing":       append(append([]byte{}, good...), 0, 0),
		"badtag":         withTag(good, 99),
		"badbool":        buildBadBool(t),
		"varintoverflow": buildOverflowVarint(),
	}
	for name, in := range bad {
		t.Run(name, func(t *testing.T) {
			v, err := d.Decode(in)
			if err == nil {
				t.Fatalf("%s: expected error, got value %v", name, v)
			}
			if v != nil {
				t.Fatalf("%s: partial object leaked: %v", name, v)
			}
		})
	}
}

func withTag(p []byte, tag byte) []byte {
	out := append([]byte{}, p...)
	// header is 4 bytes + 2 varints; for our cycle payload both are 1 byte.
	pos := 6
	if pos >= len(out) {
		return out
	}
	out[pos] = tag
	return out
}

func buildBadBool(t *testing.T) []byte {
	b := NewBuilder()
	p := mustEnc(t, b, b.AddBool(true))
	out := append([]byte{}, p...)
	out[len(out)-1] = 7 // bool payload byte must be 0/1
	return out
}

func buildOverflowVarint() []byte {
	var buf []byte
	buf = append(buf, magic0, magic1, magic2, version1)
	var tb [binary.MaxVarintLen64]byte
	k := binary.PutUvarint(tb[:], 1)
	buf = append(buf, tb[:k]...)
	k = binary.PutUvarint(tb[:], 0)
	buf = append(buf, tb[:k]...)
	buf = append(buf, tagInt)
	// 11 continuation bytes -> >64 bits
	for i := 0; i < 11; i++ {
		buf = append(buf, 0x80)
	}
	buf = append(buf, 0)
	return buf
}

func TestIllegalRefAndOffset(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxContainerLen = 10
	d, _ := NewDecoder(lim)

	// Reference target beyond nodeCount.
	var buf []byte
	buf = append(buf, magic0, magic1, magic2, version1)
	var tb [binary.MaxVarintLen64]byte
	k := binary.PutUvarint(tb[:], 2)
	buf = append(buf, tb[:k]...)
	k = binary.PutUvarint(tb[:], 0)
	buf = append(buf, tb[:k]...)
	buf = append(buf, tagArray)
	k = binary.PutUvarint(tb[:], 1)
	buf = append(buf, tb[:k]...)
	k = binary.PutUvarint(tb[:], 7) // target 7, only 2 nodes
	buf = append(buf, tb[:k]...)
	buf = append(buf, tagNull)
	if _, err := d.Decode(buf); !errors.Is(err, ErrBadRef) {
		t.Fatalf("err = %v, want ErrBadRef", err)
	}
}

func TestUnreachableNodeRejected(t *testing.T) {
	d, _ := NewDecoder(DefaultLimits())
	b := NewBuilder()
	root := b.AddNull()
	b.AddNull() // index 1, unreachable
	if _, err := d.Decode(mustEnc(t, b, root)); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
}

func TestQuotaRecoversAfterFailure(t *testing.T) {
	d, _ := NewDecoder(DefaultLimits())
	// A failing call...
	if _, err := d.Decode(nil); err == nil {
		t.Fatal("expected failure on nil input")
	}
	// ...must not leave the decoder unusable.
	b := NewBuilder()
	v, err := d.Decode(mustEnc(t, b, b.AddString("ok")))
	if err != nil {
		t.Fatalf("decoder not reusable: %v", err)
	}
	if v.Text() != "ok" {
		t.Fatalf("value %q", v.Text())
	}
}

func TestHugeFanoutNoPanic(t *testing.T) {
	d, _ := NewDecoder(DefaultLimits())
	var buf []byte
	buf = append(buf, magic0, magic1, magic2, version1)
	var tb [binary.MaxVarintLen64]byte
	k := binary.PutUvarint(tb[:], 1)
	buf = append(buf, tb[:k]...)
	k = binary.PutUvarint(tb[:], 0)
	buf = append(buf, tb[:k]...)
	buf = append(buf, tagArray)
	k = binary.PutUvarint(tb[:], math.MaxUint32)
	buf = append(buf, tb[:k]...)
	if _, err := d.Decode(buf); !errors.Is(err, ErrBadLength) {
		t.Fatalf("err = %v, want ErrBadLength", err)
	}
}
