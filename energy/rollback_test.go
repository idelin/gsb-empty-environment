package energy_test

import (
	"errors"
	"testing"

	"example.org/netsec/q0406/energy"
)

// TestRollbackFailedSample checks that a batch containing a failed sample is
// rolled back atomically, including when valid operations precede the failure.
func TestRollbackFailedSample(t *testing.T) {
	t.Run("merge with unknown remove rolls back earlier add", func(t *testing.T) {
		allocator := energy.New()
		mustAdd(t, allocator, energy.Sample{ID: "kept", Coverage: []energy.Feature{1}, Execs: 2})

		err := allocator.Merge([]energy.Op{
			energy.OpAddOp(energy.Sample{ID: "new", Coverage: []energy.Feature{2}, Execs: 1}),
			energy.OpRemoveOp("ghost"),
		})
		if !errors.Is(err, energy.ErrSampleNotFound) {
			t.Fatalf("Merge err = %v, want ErrSampleNotFound", err)
		}

		if _, ok := allocator.Get("new"); ok {
			t.Fatal("rolled-back add leaked into allocator")
		}
		if allocator.Len() != 1 {
			t.Fatalf("sample count changed after failed merge: %d", allocator.Len())
		}
		if got := allocator.Frequency(2); got != 0 {
			t.Fatalf("rolled-back feature frequency leaked: %d", got)
		}
		if got := allocator.Frequency(1); got != 2 {
			t.Fatalf("surviving frequency changed: %d, want 2", got)
		}
	})

	t.Run("duplicate inside batch rolls back whole batch", func(t *testing.T) {
		allocator := energy.New()
		mustAdd(t, allocator, energy.Sample{ID: "dup", Coverage: []energy.Feature{1}, Execs: 1})

		tx := allocator.Begin()
		if err := tx.Add(energy.Sample{ID: "fresh", Coverage: []energy.Feature{2}, Execs: 1}); err != nil {
			t.Fatalf("Tx.Add: %v", err)
		}
		if err := tx.Add(energy.Sample{ID: "dup", Coverage: []energy.Feature{3}, Execs: 1}); err != nil {
			t.Fatalf("Tx.Add staging: %v", err)
		}
		err := tx.Commit()
		if !errors.Is(err, energy.ErrDuplicateID) {
			t.Fatalf("Commit err = %v, want ErrDuplicateID", err)
		}
		if err := tx.Commit(); !errors.Is(err, energy.ErrTxClosed) {
			t.Fatalf("re-commit err = %v, want ErrTxClosed", err)
		}
		if _, ok := allocator.Get("fresh"); ok {
			t.Fatal("rolled-back transaction leaked sample")
		}
		if allocator.Len() != 1 {
			t.Fatalf("allocator length = %d, want 1", allocator.Len())
		}
	})

	t.Run("explicit rollback leaves state untouched", func(t *testing.T) {
		allocator := energy.New()
		mustAdd(t, allocator, energy.Sample{ID: "kept", Coverage: []energy.Feature{1}, Execs: 1})

		tx := allocator.Begin()
		if err := tx.Remove("kept"); err != nil {
			t.Fatalf("Tx.Remove: %v", err)
		}
		if err := tx.Add(energy.Sample{ID: "new", Coverage: []energy.Feature{2}, Execs: 4}); err != nil {
			t.Fatalf("Tx.Add: %v", err)
		}
		tx.Rollback()
		if err := tx.Add(energy.Sample{ID: "late", Coverage: nil, Execs: 1}); !errors.Is(err, energy.ErrTxClosed) {
			t.Fatalf("staging after rollback err = %v, want ErrTxClosed", err)
		}

		if allocator.Len() != 1 {
			t.Fatalf("allocator length = %d, want 1 after rollback", allocator.Len())
		}
		if _, ok := allocator.Get("kept"); !ok {
			t.Fatal("rollback removed a live sample")
		}
		if _, ok := allocator.Get("new"); ok {
			t.Fatal("rollback leaked a staged sample")
		}
		assertAllocationMatchesOracle(t, allocator, 10)
	})

	t.Run("successful commit applies every staged op", func(t *testing.T) {
		allocator := energy.New()
		mustAdd(t, allocator, energy.Sample{ID: "old", Coverage: []energy.Feature{1}, Execs: 5})

		tx := allocator.Begin()
		if err := tx.Add(energy.Sample{ID: "new", Coverage: []energy.Feature{2}, Execs: 2}); err != nil {
			t.Fatalf("Tx.Add: %v", err)
		}
		if err := tx.SetExecs("old", 7); err != nil {
			t.Fatalf("Tx.SetExecs: %v", err)
		}
		if err := tx.ReplaceCoverage("new", []energy.Feature{3, 3, 4}); err != nil {
			t.Fatalf("Tx.ReplaceCoverage: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}

		old, ok := allocator.Get("old")
		if !ok || old.Execs != 7 {
			t.Fatalf("old sample after commit = %+v ok=%v", old, ok)
		}
		newSample, ok := allocator.Get("new")
		if !ok {
			t.Fatal("committed sample missing")
		}
		if len(newSample.Coverage) != 2 {
			t.Fatalf("coverage not deduplicated: %v", newSample.Coverage)
		}
		if got := allocator.Frequency(3); got != 2 || allocator.Frequency(4) != 2 {
			t.Fatalf("new feature frequencies = %d/%d, want 2/2",
				allocator.Frequency(3), allocator.Frequency(4))
		}
		assertStateMatchesModel(t, allocator, samplesToModel(allocator.Samples()))
	})
}

// TestValidationErrors covers the remaining guard rails of the public API.
func TestValidationErrors(t *testing.T) {
	allocator := energy.New()
	mustAdd(t, allocator, energy.Sample{ID: "s", Coverage: []energy.Feature{1}, Execs: 1})

	if err := allocator.Add(energy.Sample{ID: "s", Execs: 1}); !errors.Is(err, energy.ErrDuplicateID) {
		t.Fatalf("duplicate add err = %v, want ErrDuplicateID", err)
	}
	if err := allocator.Add(energy.Sample{ID: "neg", Execs: -1}); !errors.Is(err, energy.ErrInvalidExecs) {
		t.Fatalf("negative execs err = %v, want ErrInvalidExecs", err)
	}
	if err := allocator.Remove("missing"); !errors.Is(err, energy.ErrSampleNotFound) {
		t.Fatalf("remove missing err = %v, want ErrSampleNotFound", err)
	}
	if err := allocator.SetExecs("missing", 1); !errors.Is(err, energy.ErrSampleNotFound) {
		t.Fatalf("set-execs missing err = %v, want ErrSampleNotFound", err)
	}
	if err := allocator.SetExecs("s", -1); !errors.Is(err, energy.ErrInvalidExecs) {
		t.Fatalf("set-execs negative err = %v, want ErrInvalidExecs", err)
	}
	if err := allocator.AddExecs("s", -5); !errors.Is(err, energy.ErrInvalidExecs) {
		t.Fatalf("add-execs underflow err = %v, want ErrInvalidExecs", err)
	}
	if err := allocator.ReplaceCoverage("missing", nil); !errors.Is(err, energy.ErrSampleNotFound) {
		t.Fatalf("replace-coverage missing err = %v, want ErrSampleNotFound", err)
	}
	if _, err := allocator.Allocate(-1); !errors.Is(err, energy.ErrInvalidBudget) {
		t.Fatalf("negative budget err = %v, want ErrInvalidBudget", err)
	}
	if _, err := energy.RecomputeAllocate(
		[]energy.Sample{{ID: "x", Execs: -1}}, 1,
	); !errors.Is(err, energy.ErrInvalidExecs) {
		t.Fatalf("oracle negative execs err = %v, want ErrInvalidExecs", err)
	}
	if _, err := energy.RecomputeAllocate(
		[]energy.Sample{{ID: "x"}, {ID: "x"}}, 1,
	); !errors.Is(err, energy.ErrDuplicateID) {
		t.Fatalf("oracle duplicate id err = %v, want ErrDuplicateID", err)
	}

	// Underflow must be a no-op.
	if sample, _ := allocator.Get("s"); sample.Execs != 1 {
		t.Fatalf("execs changed after rejected AddExecs: %d", sample.Execs)
	}

	if err := allocator.AddExecs("s", 4); err != nil {
		t.Fatalf("AddExecs positive delta: %v", err)
	}
	if sample, _ := allocator.Get("s"); sample.Execs != 5 {
		t.Fatalf("execs after AddExecs = %d, want 5", sample.Execs)
	}
	if err := allocator.AddExecs("missing", 1); !errors.Is(err, energy.ErrSampleNotFound) {
		t.Fatalf("AddExecs missing err = %v, want ErrSampleNotFound", err)
	}

	tx := allocator.Begin()
	if tx.Len() != 0 {
		t.Fatalf("new tx length = %d, want 0", tx.Len())
	}
	if err := tx.SetExecs("s", 2); err != nil {
		t.Fatalf("Tx.SetExecs: %v", err)
	}
	if tx.Len() != 1 {
		t.Fatalf("tx length after staging = %d, want 1", tx.Len())
	}
	tx.Rollback()
	if sample, _ := allocator.Get("s"); sample.Execs != 5 {
		t.Fatalf("rollback changed execs: got %d, want 5", sample.Execs)
	}

	// Defensive path: an unknown op kind cannot be built via constructors,
	// but Merge must reject it instead of silently succeeding.
	err := allocator.Merge([]energy.Op{{}})
	if err == nil {
		t.Fatal("Merge with zero-value op unexpectedly succeeded")
	}
}

// TestBudgetBoundaries pins down zero/empty behavior and small-budget splits.
func TestBudgetBoundaries(t *testing.T) {
	allocator := energy.New()
	allocations, err := allocator.Allocate(0)
	if err != nil {
		t.Fatalf("Allocate on empty allocator: %v", err)
	}
	if len(allocations) != 0 {
		t.Fatalf("empty allocator returned %d allocations", len(allocations))
	}

	mustAdd(t, allocator, energy.Sample{ID: "a", Coverage: []energy.Feature{1}, Execs: 1})
	mustAdd(t, allocator, energy.Sample{ID: "b", Coverage: []energy.Feature{2}, Execs: 1})

	zero, err := allocator.Allocate(0)
	if err != nil {
		t.Fatalf("Allocate(0): %v", err)
	}
	for _, allocation := range zero {
		if allocation.Energy != 0 {
			t.Fatalf("zero budget gave energy %d to %q", allocation.Energy, allocation.SampleID)
		}
	}

	one, err := allocator.Allocate(1)
	if err != nil {
		t.Fatalf("Allocate(1): %v", err)
	}
	assertConservation(t, one, 1, true)
	got := allocMap(one)
	if got["a"] != 1 || got["b"] != 0 {
		t.Fatalf("single unit split = %v, want a=1 b=0", got)
	}

	oracle, err := energy.RecomputeAllocate(allocator.Samples(), 1)
	if err != nil {
		t.Fatalf("RecomputeAllocate: %v", err)
	}
	assertAllocationsEqual(t, one, oracle)
}
