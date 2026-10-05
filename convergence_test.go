package anno

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// baseOps is a small operation set exercising every rule at once:
// concurrent same-predecessor inserts, deletes of annotation anchors,
// overlapping same-key annotations, and an undo of the winning annotation.
func baseOps() []Op {
	i1 := ID{1, "amy"}
	i2 := ID{2, "amy"}
	i3 := ID{1, "bob"}
	i4 := ID{3, "bob"}
	return []Op{
		Insert{ID: i1, Pred: Root, Char: 'h'},
		Insert{ID: i2, Pred: i1, Char: 'i'},
		Insert{ID: i3, Pred: Root, Char: 'H'}, // concurrent with i1 at Root
		Insert{ID: i4, Pred: i2, Char: '!'},
		Delete{ID: ID{4, "bob"}, Target: i1}, // tombstones an anchor of a1
		Delete{ID: ID{6, "bob"}, Target: i3},
		Annotate{ID: ID{4, "amy"}, Ann: "b1", Key: "bold", Val: "1",
			Left: i1, LeftIncl: true, Right: i2, RightIncl: true},
		Annotate{ID: ID{5, "amy"}, Ann: "b2", Key: "bold", Val: "2",
			Left: Root, LeftIncl: false, Right: i4, RightIncl: false},
		Undo{ID: ID{6, "amy"}, Ann: "b2"}, // b1 resurfaces
	}
}

func runsString(rs []Run) string {
	var b strings.Builder
	for _, r := range rs {
		keys := make([]string, 0, len(r.Attrs))
		for k := range r.Attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "%q[", r.Text)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s=%s,", k, r.Attrs[k])
		}
		b.WriteString("]")
	}
	return b.String()
}

func fingerprint(d *Doc) string {
	return d.Text() + "|" + runsString(d.Merge()) + "|" + runsString(RunsFrom(d.Reference()))
}

// eachPerm invokes fn with every permutation of [0, n).
func eachPerm(n int, fn func(perm []int)) {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			fn(idx)
			return
		}
		for i := k; i < n; i++ {
			idx[k], idx[i] = idx[i], idx[k]
			rec(k + 1)
			idx[k], idx[i] = idx[i], idx[k]
		}
	}
	rec(0)
}

// TestConvergence delivers the same operation set in every possible order
// and requires the final text and resolved attributes to be identical, and
// the run merger to agree with the per-character reference evaluator.
func TestConvergence(t *testing.T) {
	ops := baseOps()
	var want string
	count := 0
	eachPerm(len(ops), func(perm []int) {
		d := New()
		for _, i := range perm {
			d.Apply(ops[i])
		}
		if n := d.Pending(); n != 0 {
			t.Fatalf("perm %v: %d operations still pending", perm, n)
		}
		if got, want := runsString(d.Merge()), runsString(RunsFrom(d.Reference())); got != want {
			t.Fatalf("perm %v: Merge() = %s, reference = %s", perm, got, want)
		}
		got := fingerprint(d)
		if count == 0 {
			want = got
		} else if got != want {
			t.Fatalf("perm %v: fingerprint = %s, want %s", perm, got, want)
		}
		count++
	})
	t.Logf("verified %d delivery permutations; final state: %s", count, want)
}
