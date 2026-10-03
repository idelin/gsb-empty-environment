package delta

import "testing"

func buildString(d *Document, s string) {
	parent := Dot{}
	for _, r := range s {
		parent = d.InsertAfter(parent, r)
	}
}

func TestInsertAndString(t *testing.T) {
	d := NewDocument("A")
	buildString(d, "hello")
	if got := d.String(); got != "hello" {
		t.Fatalf("String() = %q, want %q", got, "hello")
	}
	if d.Len() != 5 {
		t.Fatalf("Len() = %d, want 5", d.Len())
	}
}

func TestInsertInMiddle(t *testing.T) {
	d := NewDocument("A")
	buildString(d, "helo")
	l, ok := d.DotAt(1) // the 'l'... insert after 'e' at index 1
	if !ok {
		t.Fatal("DotAt(1) failed")
	}
	d.InsertAfter(l, 'l')
	if got := d.String(); got != "hello" {
		t.Fatalf("String() = %q, want %q", got, "hello")
	}
}

func TestDelete(t *testing.T) {
	d := NewDocument("A")
	buildString(d, "hxello")
	x, _ := d.DotAt(1)
	d.Delete(x)
	if got := d.String(); got != "hello" {
		t.Fatalf("String() = %q, want %q", got, "hello")
	}
}

func TestConcurrentInsertsConverge(t *testing.T) {
	// Two replicas start from the same base, then concurrently insert
	// different characters after the same parent.
	base := NewDocument("A")
	buildString(base, "ab")
	mid, _ := base.DotAt(0)

	a, b := NewDocument("A"), NewDocument("B")
	a.MergeDelta(base.DeltaSince(nil))
	b.MergeDelta(base.DeltaSince(nil))

	a.InsertAfter(mid, 'X')
	b.InsertAfter(mid, 'Y')

	// Exchange in both directions; order of merging must not matter.
	a.MergeDelta(b.DeltaSince(nil))
	b.MergeDelta(a.DeltaSince(nil))

	if a.String() != b.String() {
		t.Fatalf("diverged: %q vs %q", a.String(), b.String())
	}
	if a.Len() != 4 {
		t.Fatalf("Len() = %d, want 4", a.Len())
	}
	if !a.EqualState(b) {
		t.Fatal("states not identical after merge")
	}
}

func TestMergeIdempotent(t *testing.T) {
	src := NewDocument("A")
	buildString(src, "hello")
	delta := src.DeltaSince(nil)

	dst := NewDocument("B")
	dst.MergeDelta(delta)
	first := dst.String()
	dst.MergeDelta(delta) // duplicate delivery
	dst.MergeDelta(delta)
	if dst.String() != first {
		t.Fatalf("not idempotent: %q then %q", first, dst.String())
	}
	if dst.OpCount() != 5 {
		t.Fatalf("OpCount() = %d, want 5", dst.OpCount())
	}
}

func TestDeltaSinceEmptyWatermarkIsFullState(t *testing.T) {
	d := NewDocument("A")
	buildString(d, "hello")
	full := d.DeltaSince(nil)
	if len(full.Ops) != 5 {
		t.Fatalf("full-state delta has %d ops, want 5", len(full.Ops))
	}
	// Everything acknowledged -> empty delta.
	empty := d.DeltaSince(full.Ctx)
	if len(empty.Ops) != 0 {
		t.Fatalf("delta after full ack has %d ops, want 0", len(empty.Ops))
	}
}

func TestDeleteConvergesWithInsert(t *testing.T) {
	// Delete and concurrent insert after the deleted element must both
	// survive: children of tombstones stay visible.
	a, b := NewDocument("A"), NewDocument("B")
	buildString(a, "ac")
	b.MergeDelta(a.DeltaSince(nil))

	doomed, _ := a.DotAt(0)
	a.Delete(doomed)
	b.InsertAfter(doomed, 'b')

	a.MergeDelta(b.DeltaSince(nil))
	b.MergeDelta(a.DeltaSince(nil))

	if a.String() != b.String() {
		t.Fatalf("diverged: %q vs %q", a.String(), b.String())
	}
	if got := a.String(); got != "bc" {
		t.Fatalf("String() = %q, want %q", got, "bc")
	}
}
