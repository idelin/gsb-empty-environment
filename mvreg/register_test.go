package mvreg

import (
	"reflect"
	"testing"
)

func values[T any](items []Item[T]) []T {
	out := make([]T, len(items))
	for i, item := range items {
		out[i] = item.Value
	}
	return out
}

func TestEmptyRead(t *testing.T) {
	register := New[string]("A")
	if got := register.Read(); len(got) != 0 {
		t.Fatalf("empty read = %v, want no values", got)
	}
}

func TestConcurrentWritesAreBothKept(t *testing.T) {
	a := New[string]("A")
	b := New[string]("B")
	a.Write("red")
	b.Write("blue")

	a.Merge(b)
	got := values(a.Read())
	want := []string{"red", "blue"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Read() = %v, want %v", got, want)
	}
}

func TestSuccessorWriteOverwritesObservedVersions(t *testing.T) {
	a := New[string]("A")
	b := New[string]("B")
	a.Write("red")
	b.Write("blue")
	a.Merge(b)

	item := a.Write("purple")
	if got := values(a.Read()); !reflect.DeepEqual(got, []string{"purple"}) {
		t.Fatalf("after successor write Read() = %v, want [purple]", got)
	}
	if item.Vector["A"] != 2 || item.Vector["B"] != 1 {
		t.Fatalf("successor vector = %v, want A:2,B:1", item.Vector)
	}

	b.Merge(a)
	if got := values(b.Read()); !reflect.DeepEqual(got, []string{"purple"}) {
		t.Fatalf("receiver Read() = %v, want successor only", got)
	}
}

func TestCausalVersionsDoNotResurrect(t *testing.T) {
	a := New[string]("A")
	b := New[string]("B")
	c := New[string]("C")
	a.Write("a1")
	b.Merge(a)
	b.Write("b1")
	c.Merge(a)
	c.Write("c1")

	a.Merge(b)
	a.Merge(c)
	if got := values(a.Read()); !reflect.DeepEqual(got, []string{"b1", "c1"}) {
		t.Fatalf("concurrent causal branches Read() = %v, want [b1 c1]", got)
	}

	staleB := b.Snapshot()
	a.Write("a2")
	b.Merge(a)
	c.Merge(a)
	merge := New[string]("M")
	merge.Merge(b)
	merge.Merge(c)
	merge.MergeSnapshot(staleB)

	if got := values(merge.Read()); !reflect.DeepEqual(got, []string{"a2"}) {
		t.Fatalf("Read() = %v, stale snapshot resurrected values", got)
	}
}

func TestExplicitResolvePropagatesAndRemovesConflict(t *testing.T) {
	a := New[string]("A")
	b := New[string]("B")
	c := New[string]("C")
	a.Write("red")
	b.Write("blue")

	a.Merge(b)
	resolved := a.Resolve("purple")
	if got := values(a.Read()); !reflect.DeepEqual(got, []string{"purple"}) {
		t.Fatalf("Read() = %v, want resolved value", got)
	}

	c.Merge(a)
	c.Merge(b)
	if got := values(c.Read()); !reflect.DeepEqual(got, []string{"purple"}) {
		t.Fatalf("Read() after resolve and branch = %v, want [purple]", got)
	}
	if compareVectors(b.Read()[0].Vector, resolved.Vector) != before {
		t.Fatalf("resolved vector %v must dominate branch vector %v", resolved.Vector, b.Read()[0].Vector)
	}
}

func TestMergeIsIdempotentCommutativeAndAssociative(t *testing.T) {
	a := New[string]("A")
	b := New[string]("B")
	c := New[string]("C")
	a.Write("a")
	b.Write("b")
	c.Write("c")

	one := New[string]("X")
	one.Merge(a)
	one.Merge(b)
	one.Merge(c)
	one.Merge(a)
	one.Merge(b)
	one.Merge(c)
	expected := one.Snapshot()

	two := New[string]("X")
	two.Merge(c)
	two.Merge(a)
	two.Merge(b)
	three := New[string]("X")
	three.Merge(b)
	three.Merge(c)
	three.Merge(a)

	if !reflect.DeepEqual(two.Snapshot(), expected) || !reflect.DeepEqual(three.Snapshot(), expected) {
		t.Fatalf("merge order/repetition changed state:\n%#v\n%#v\n%#v", expected, two.Snapshot(), three.Snapshot())
	}
}

func TestSnapshotDoesNotAliasMutableState(t *testing.T) {
	a := New[string]("A")
	item := a.Write("first")
	snapshot := a.Snapshot()
	snapshot.Items[0].Value = "changed"
	snapshot.Context["A"] = 99
	item.Value = "external"
	item.Vector["A"] = 99

	if got := values(a.Read()); !reflect.DeepEqual(got, []string{"first"}) {
		t.Fatalf("snapshot mutation changed register: %v", got)
	}

	b := New[string]("B")
	b.Write("second")
	snapshot = a.Snapshot()
	b.MergeSnapshot(snapshot)
	snapshot.Items[0].Value = "changed"
	snapshot.Context["A"] = 99
	if got := values(b.Read()); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("snapshot mutation after merge changed register: %v", got)
	}
}
