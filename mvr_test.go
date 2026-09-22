package mvr

import (
	"reflect"
	"testing"
)

func TestCompareRelations(t *testing.T) {
	cases := []struct {
		name string
		a, b VV
		want Relation
	}{
		{"equal", VV{"A": 1}, VV{"A": 1}, RelEqual},
		{"before", VV{"A": 1}, VV{"A": 2}, RelBefore},
		{"after", VV{"A": 2, "B": 1}, VV{"A": 1, "B": 1}, RelAfter},
		{"concurrent-disjoint", VV{"A": 1}, VV{"B": 1}, RelConcurrent},
		{"concurrent-overlap", VV{"A": 2, "B": 1}, VV{"A": 1, "B": 2}, RelConcurrent},
		{"empty-before", VV{}, VV{"A": 1}, RelBefore},
		{"empty-equal", VV{}, VV{}, RelEqual},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("%s: Compare(%v, %v) = %v, want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}

func TestConcurrentWritesAllRetained(t *testing.T) {
	ra, rb := NewRegister("A"), NewRegister("B")
	ea := ra.Write("apple", 10)
	eb := rb.Write("banana", 20)
	ra.Apply(eb)
	rb.Apply(ea)
	want := []string{"apple", "banana"}
	if got := ra.Values(); !reflect.DeepEqual(got, want) {
		t.Fatalf("A values = %v, want %v", got, want)
	}
	if got := rb.Values(); !reflect.DeepEqual(got, want) {
		t.Fatalf("B values = %v, want %v", got, want)
	}
}

func TestSuccessorWriteOverwritesObserved(t *testing.T) {
	ra, rb := NewRegister("A"), NewRegister("B")
	ea := ra.Write("apple", 1)
	rb.Apply(ea)                // B observes apple
	eb := rb.Write("banana", 2) // B's write causally follows apple
	ra.Apply(eb)
	for _, r := range []*Register{ra, rb} {
		if got := r.Values(); !reflect.DeepEqual(got, []string{"banana"}) {
			t.Fatalf("%s values = %v, want [banana]", r.ID, got)
		}
	}
}

func TestLargerWallClockCannotSwallowConcurrentValue(t *testing.T) {
	ra, rb := NewRegister("A"), NewRegister("B")
	ea := ra.Write("old-clock", 1)
	eb := rb.Write("huge-clock", 1<<40) // concurrent, absurdly larger wall clock
	ra.Apply(eb)
	rb.Apply(ea)
	want := []string{"huge-clock", "old-clock"}
	for _, r := range []*Register{ra, rb} {
		if got := r.Values(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s values = %v, want %v (wall clock must not decide)", r.ID, got, want)
		}
	}
}

func TestResolveCoversOnlyObservedVersions(t *testing.T) {
	ra, rb, rc := NewRegister("A"), NewRegister("B"), NewRegister("C")
	ex := ra.Write("x", 1)
	ey := rb.Write("y", 1) // concurrent with x
	ra.Apply(ey)
	rb.Apply(ex)           // A and B both see {x, y}
	ez := rc.Write("z", 1) // C writes concurrently, having seen nothing
	er := ra.Resolve("xy-resolved", 2)
	for _, e := range []Entry{ez, er} {
		ra.Apply(e)
		rb.Apply(e)
		rc.Apply(e)
	}
	// The resolve supersedes {x, y} but must not erase the unobserved z.
	want := []string{"xy-resolved", "z"}
	for _, r := range []*Register{ra, rb, rc} {
		if got := r.Values(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s values = %v, want %v", r.ID, got, want)
		}
	}
}

func TestMergeIsIdempotentAndCommutative(t *testing.T) {
	ra, rb, rc := NewRegister("A"), NewRegister("B"), NewRegister("C")
	ea := ra.Write("a", 1)
	eb := rb.Write("b", 1)
	rc.Apply(ea)
	ec := rc.Write("c", 1) // c causally follows a
	state := []Entry{ea, eb, ec}

	r1, r2 := NewRegister("X"), NewRegister("X")
	r1.Merge(state)
	r1.Merge(state) // idempotent
	r2.Merge([]Entry{ec, eb, ea})
	r2.Merge([]Entry{eb})
	if !r1.Equal(r2) {
		t.Fatalf("merge not order-insensitive: %v vs %v", r1.Entries(), r2.Entries())
	}
	// c dominates a, b is concurrent: expect {b, c}.
	if got := r1.Values(); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("values = %v, want [b c]", got)
	}
}

func TestApplyIgnoresDominatedDuplicates(t *testing.T) {
	ra, rb := NewRegister("A"), NewRegister("B")
	e1 := ra.Write("one", 1)
	rb.Apply(e1)
	e2 := ra.Write("two", 2)
	rb.Apply(e2)
	if rb.Apply(e1) {
		t.Fatal("re-applying a dominated entry must not change state")
	}
	if rb.Apply(e2) {
		t.Fatal("duplicate delivery must not change state")
	}
	if got := rb.Values(); !reflect.DeepEqual(got, []string{"two"}) {
		t.Fatalf("values = %v, want [two]", got)
	}
}
