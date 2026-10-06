package kvindex

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func mustApply(t *testing.T, ix *Index, op Op) {
	t.Helper()
	if err := ix.Apply(op); err != nil {
		t.Fatalf("Apply(%+v): %v", op, err)
	}
}

func put(key, value string, seq uint64) Op {
	return Op{Kind: OpPut, Key: key, Value: value, Seq: seq}
}

func del(start, end string, seq uint64) Op {
	return Op{Kind: OpDeleteRange, Start: start, End: end, Seq: seq}
}

func checkGet(t *testing.T, ix *Index, key string, snapshot uint64, wantValue string, wantOK bool) {
	t.Helper()
	value, ok := ix.GetAt(key, snapshot)
	if ok != wantOK || (ok && value != wantValue) {
		t.Fatalf("GetAt(%q, %d) = (%q, %v), want (%q, %v)", key, snapshot, value, ok, wantValue, wantOK)
	}
}

func TestNestedTombstonesAndResurrection(t *testing.T) {
	ix := New()
	mustApply(t, ix, put("a", "a1", 1))
	mustApply(t, ix, put("b", "b1", 2))
	mustApply(t, ix, put("c", "c1", 3))
	mustApply(t, ix, put("d", "d1", 4))
	mustApply(t, ix, put("e", "e1", 5))

	// Outer tombstone covers [a, e); "e" survives (half-open end).
	mustApply(t, ix, del("a", "e", 10))
	// Resurrect "c" with a newer point write.
	mustApply(t, ix, put("c", "c2", 11))
	// Inner tombstone nested inside the outer one's range kills "c" again.
	mustApply(t, ix, del("b", "d", 12))
	// And resurrect once more.
	mustApply(t, ix, put("c", "c3", 13))

	// History before any tombstone.
	checkGet(t, ix, "c", 3, "c1", true)
	checkGet(t, ix, "c", 9, "c1", true)
	// Outer tombstone hides older writes.
	checkGet(t, ix, "a", 10, "", false)
	checkGet(t, ix, "c", 10, "", false)
	// Half-open end: "e" was never covered.
	checkGet(t, ix, "e", 10, "e1", true)
	checkGet(t, ix, "e", 13, "e1", true)
	// Resurrection by newer point write.
	checkGet(t, ix, "c", 11, "c2", true)
	// Nested tombstone wins again.
	checkGet(t, ix, "c", 12, "", false)
	// Final resurrection; older snapshots still see history.
	checkGet(t, ix, "c", 13, "c3", true)
	checkGet(t, ix, "c", 100, "c3", true)
	// "b" and "d" relative to the inner tombstone [b, d): b covered, d not.
	checkGet(t, ix, "b", 12, "", false)
	checkGet(t, ix, "d", 12, "", false) // covered by outer tombstone only
	mustApply(t, ix, put("d", "d2", 14))
	checkGet(t, ix, "d", 14, "d2", true)
	checkGet(t, ix, "d", 13, "", false)
}

func TestHalfOpenEndpoints(t *testing.T) {
	ix := New()
	mustApply(t, ix, put("b", "vb", 1))
	mustApply(t, ix, put("c", "vc", 2))
	mustApply(t, ix, put("d", "vd", 3))
	mustApply(t, ix, del("b", "d", 4))

	checkGet(t, ix, "b", 4, "", false) // start is inclusive
	checkGet(t, ix, "c", 4, "", false)
	checkGet(t, ix, "d", 4, "vd", true) // end is exclusive

	page := ix.ScanAt("a", "z", 4, 0)
	want := []KV{{Key: "d", Value: "vd"}}
	if !reflect.DeepEqual(page.Items, want) || !page.Done {
		t.Fatalf("ScanAt = %+v, want items %v done", page, want)
	}

	// Empty and inverted ranges are rejected.
	if err := ix.Apply(del("x", "x", 5)); err == nil {
		t.Fatal("expected error for empty range")
	}
	if err := ix.Apply(del("y", "x", 6)); err == nil {
		t.Fatal("expected error for inverted range")
	}
}

func TestDuplicateSequence(t *testing.T) {
	ix := New()
	mustApply(t, ix, put("a", "v1", 1))

	// Identical retry is an idempotent no-op.
	mustApply(t, ix, put("a", "v1", 1))
	checkGet(t, ix, "a", 1, "v1", true)

	// Same seq, different value: conflict.
	if err := ix.Apply(put("a", "v2", 1)); err != ErrSeqConflict {
		t.Fatalf("expected ErrSeqConflict, got %v", err)
	}
	// Same seq, different key: conflict.
	if err := ix.Apply(put("b", "v1", 1)); err != ErrSeqConflict {
		t.Fatalf("expected ErrSeqConflict, got %v", err)
	}
	// Same seq, different kind: conflict.
	if err := ix.Apply(del("a", "z", 1)); err != ErrSeqConflict {
		t.Fatalf("expected ErrSeqConflict, got %v", err)
	}
	// Identical tombstone retry is fine; differing tombstone is not.
	mustApply(t, ix, del("a", "z", 2))
	mustApply(t, ix, del("a", "z", 2))
	if err := ix.Apply(del("a", "y", 2)); err != ErrSeqConflict {
		t.Fatalf("expected ErrSeqConflict, got %v", err)
	}
	// Conflicting ops must not have mutated anything.
	checkGet(t, ix, "a", 1, "v1", true)
	checkGet(t, ix, "a", 2, "", false)
}

func TestScanPagination(t *testing.T) {
	ix := New()
	mustApply(t, ix, put("a", "1", 1))
	mustApply(t, ix, put("b", "2", 2))
	mustApply(t, ix, put("c", "3", 3))
	mustApply(t, ix, put("d", "4", 4))
	mustApply(t, ix, put("e", "5", 5))
	mustApply(t, ix, del("c", "d", 6)) // hide "c"

	full := ix.ScanAt("", "z", 6, 0)
	want := []KV{{"a", "1"}, {"b", "2"}, {"d", "4"}, {"e", "5"}}
	if !reflect.DeepEqual(full.Items, want) || !full.Done {
		t.Fatalf("full scan = %+v, want %v", full, want)
	}

	// Walk page by page with limit 1 and 3; concatenation must equal full.
	for _, limit := range []int{1, 2, 3, 10} {
		var got []KV
		page := ix.ScanAt("", "z", 6, limit)
		for {
			got = append(got, page.Items...)
			if page.Done {
				break
			}
			page = ix.ScanAfter(page.ResumeAfter, "z", 6, limit)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("paginated(limit=%d) = %v, want %v", limit, got, want)
		}
	}

	// Pages must never repeat or skip the resume key.
	page := ix.ScanAt("", "z", 6, 2)
	if page.Done || page.ResumeAfter != "b" {
		t.Fatalf("first page = %+v, want resume after b", page)
	}
	next := ix.ScanAfter(page.ResumeAfter, "z", 6, 2)
	if len(next.Items) == 0 || next.Items[0].Key != "d" {
		t.Fatalf("second page = %+v, want first item d", next)
	}

	// Scan at an older snapshot sees the since-deleted key.
	old := ix.ScanAt("", "z", 5, 0)
	if len(old.Items) != 5 {
		t.Fatalf("scan at seq 5 = %v, want 5 items", old.Items)
	}
}

// collectPages walks a paginated scan to completion.
func collectPages(ix *Index, start, end string, snapshot uint64, limit int) []KV {
	var got []KV
	page := ix.ScanAt(start, end, snapshot, limit)
	for {
		got = append(got, page.Items...)
		if page.Done {
			return got
		}
		page = ix.ScanAfter(page.ResumeAfter, end, snapshot, limit)
	}
}

func TestModelRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	keySpace := []string{}
	for _, a := range []string{"a", "b", "c"} {
		for _, b := range []string{"a", "b", "c"} {
			keySpace = append(keySpace, a+b)
		}
	}
	keySpace = append(keySpace, "a", "b", "c", "d")

	randKey := func() string { return keySpace[rng.Intn(len(keySpace))] }
	randRange := func() (string, string) {
		start, end := randKey(), randKey()
		if start == end {
			end = end + "\x00"
		}
		if start > end {
			start, end = end, start
		}
		return start, end
	}

	for trial := 0; trial < 30; trial++ {
		ix := New()
		m := newModel()
		var seq uint64
		var applied []Op

		for step := 0; step < 200; step++ {
			seq += uint64(rng.Intn(3) + 1)
			var op Op
			if rng.Intn(3) == 0 {
				start, end := randRange()
				op = del(start, end, seq)
			} else {
				op = put(randKey(), fmt.Sprintf("v%d", seq), seq)
			}
			if err := ix.Apply(op); err != nil {
				t.Fatalf("trial %d step %d: Apply(%+v): %v", trial, step, op, err)
			}
			m.apply(op)
			applied = append(applied, op)

			// Occasionally replay an old op: identical must succeed, mutated
			// must be rejected with ErrSeqConflict.
			if rng.Intn(10) == 0 && len(applied) > 1 {
				old := applied[rng.Intn(len(applied))]
				if err := ix.Apply(old); err != nil {
					t.Fatalf("trial %d: idempotent replay of %+v failed: %v", trial, old, err)
				}
				mutated := old
				if mutated.Kind == OpPut {
					mutated.Value += "x"
				} else {
					mutated.End += "x"
				}
				if err := ix.Apply(mutated); err != ErrSeqConflict {
					t.Fatalf("trial %d: mutated replay of %+v: got %v, want ErrSeqConflict", trial, old, err)
				}
			}
		}

		// Compare point lookups and scans at several historical snapshots.
		for _, snapshot := range []uint64{0, seq / 4, seq / 2, (3 * seq) / 4, seq, seq + 10} {
			for _, key := range keySpace {
				gv, gok := ix.GetAt(key, snapshot)
				mv, mok := m.getAt(key, snapshot)
				if gok != mok || (gok && gv != mv) {
					t.Fatalf("trial %d: GetAt(%q, %d) = (%q,%v), model says (%q,%v)",
						trial, key, snapshot, gv, gok, mv, mok)
				}
			}
			start, end := randRange()
			wantScan := m.scanAt(start, end, snapshot)
			gotScan := ix.ScanAt(start, end, snapshot, 0)
			if !reflect.DeepEqual(gotScan.Items, wantScan) || !gotScan.Done {
				t.Fatalf("trial %d: ScanAt(%q,%q,%d) = %v, model says %v",
					trial, start, end, snapshot, gotScan.Items, wantScan)
			}
			// Paginated continuation must reproduce the same result.
			limit := rng.Intn(4) + 1
			if got := collectPages(ix, start, end, snapshot, limit); !reflect.DeepEqual(got, wantScan) {
				t.Fatalf("trial %d: paginated(limit=%d) scan = %v, model says %v",
					trial, limit, got, wantScan)
			}
		}
	}
}
