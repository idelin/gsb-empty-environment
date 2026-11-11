package energy_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"sync"
	"testing"

	"example.org/netsec/q0406/energy"
)

func assertStateMatchesModel(t *testing.T, allocator *energy.Allocator, model map[string]energy.Sample) {
	t.Helper()

	samples := allocator.Samples()
	if len(samples) != len(model) {
		t.Fatalf("sample count %d, model has %d", len(samples), len(model))
	}
	for _, sample := range samples {
		expected, ok := model[sample.ID]
		if !ok {
			t.Fatalf("unexpected sample %q in allocator", sample.ID)
		}
		expected.Coverage = dedupFeatures(expected.Coverage)
		if sample.Execs != expected.Execs ||
			!sameFeatures(sample.Coverage, expected.Coverage) {
			t.Fatalf("sample %q = %+v, want %+v", sample.ID, sample, expected)
		}
	}

	gotFrequencies := allocator.Frequencies()
	wantFrequencies := oracleFrequencies(samples)
	for feature, count := range gotFrequencies {
		if count == 0 {
			delete(gotFrequencies, feature)
		}
	}
	if !reflect.DeepEqual(gotFrequencies, wantFrequencies) {
		t.Fatalf("frequency table = %v, want %v", gotFrequencies, wantFrequencies)
	}
}

func sameFeatures(got, want []energy.Feature) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func assertAllocationMatchesOracle(t *testing.T, allocator *energy.Allocator, budget int64) {
	t.Helper()
	samples := allocator.Samples()
	got, err := allocator.Allocate(budget)
	if err != nil {
		t.Fatalf("Allocate(%d): %v", budget, err)
	}
	want, err := energy.RecomputeAllocate(samples, budget)
	if err != nil {
		t.Fatalf("RecomputeAllocate(%d): %v", budget, err)
	}
	assertOrderedByID(t, got)
	assertAllocationsEqual(t, got, want)
	assertConservation(t, got, budget, weightPositive(samples))
}

func samplesToModel(samples []energy.Sample) map[string]energy.Sample {
	model := make(map[string]energy.Sample, len(samples))
	for _, sample := range samples {
		sample.Coverage = dedupFeatures(sample.Coverage)
		model[sample.ID] = sample
	}
	return model
}

// TestIncrementalMatchesRecompute drives thousands of random mutations through
// the incremental allocator and cross-checks frequencies and every allocation
// against the pure full-recomputation oracle.
func TestIncrementalMatchesRecompute(t *testing.T) {
	const (
		idCount      = 12
		featureCount = 8
		iterations   = 3000
	)
	rng := rand.New(rand.NewPCG(20261112, 42))
	allocator := energy.New()
	model := make(map[string]energy.Sample)

	randomCoverage := func() []energy.Feature {
		coverage := make([]energy.Feature, 0, featureCount)
		for feature := energy.Feature(0); feature < featureCount; feature++ {
			if rng.IntN(2) == 1 {
				coverage = append(coverage, feature)
			}
		}
		return coverage
	}
	existingIDs := func() []string {
		ids := make([]string, 0, len(model))
		for id := range model {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return ids
	}

	for iteration := 0; iteration < iterations; iteration++ {
		switch rng.IntN(6) {
		case 0:
			id := fmt.Sprintf("s%02d", rng.IntN(idCount))
			sample := energy.Sample{
				ID:       id,
				Coverage: randomCoverage(),
				Execs:    int64(rng.IntN(6)),
			}
			err := allocator.Add(sample)
			if _, exists := model[id]; exists {
				if !errors.Is(err, energy.ErrDuplicateID) {
					t.Fatalf("Add existing %q: err=%v, want ErrDuplicateID", id, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("Add %q: %v", id, err)
			}
			sample.Coverage = dedupFeatures(sample.Coverage)
			model[id] = sample
		case 1:
			ids := existingIDs()
			if len(ids) == 0 {
				continue
			}
			id := ids[rng.IntN(len(ids))]
			if err := allocator.Remove(id); err != nil {
				t.Fatalf("Remove %q: %v", id, err)
			}
			delete(model, id)
		case 2:
			ids := existingIDs()
			if len(ids) == 0 {
				continue
			}
			id := ids[rng.IntN(len(ids))]
			execs := int64(rng.IntN(8))
			if err := allocator.SetExecs(id, execs); err != nil {
				t.Fatalf("SetExecs %q: %v", id, err)
			}
			sample := model[id]
			sample.Execs = execs
			model[id] = sample
		case 3:
			ids := existingIDs()
			if len(ids) == 0 {
				continue
			}
			id := ids[rng.IntN(len(ids))]
			coverage := randomCoverage()
			if err := allocator.ReplaceCoverage(id, coverage); err != nil {
				t.Fatalf("ReplaceCoverage %q: %v", id, err)
			}
			sample := model[id]
			sample.Coverage = dedupFeatures(coverage)
			model[id] = sample
		case 4:
			ids := existingIDs()
			if len(ids) == 0 {
				continue
			}
			id := ids[rng.IntN(len(ids))]
			delta := int64(rng.IntN(5)) - 2
			err := allocator.AddExecs(id, delta)
			sample := model[id]
			if sample.Execs+delta < 0 {
				if !errors.Is(err, energy.ErrInvalidExecs) {
					t.Fatalf("AddExecs %q underflow: err=%v, want ErrInvalidExecs", id, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("AddExecs %q: %v", id, err)
			}
			sample.Execs += delta
			model[id] = sample
		case 5:
			// Validation-only round: no state mutation.
		}

		assertStateMatchesModel(t, allocator, model)
		assertAllocationMatchesOracle(t, allocator, int64(rng.IntN(50)))
	}
}

// TestConcurrentMerge exercises concurrent batched merges and readers. The
// final state must be internally consistent and agree with full recomputation
// regardless of merge interleaving.
func TestConcurrentMerge(t *testing.T) {
	const workers = 8
	allocator := energy.New()
	for id := 0; id < 4; id++ {
		mustAdd(t, allocator, energy.Sample{
			ID:       fmt.Sprintf("base%02d", id),
			Coverage: []energy.Feature{energy.Feature(id)},
			Execs:    1,
		})
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			rng := rand.New(rand.NewPCG(uint64(worker)+1, uint64(worker)*7+3))
			for round := 0; round < 200; round++ {
				ops := []energy.Op{
					energy.OpAddOp(energy.Sample{
						ID:       fmt.Sprintf("w%02d-r%03d", worker, round),
						Coverage: []energy.Feature{energy.Feature(rng.IntN(12))},
						Execs:    int64(1 + rng.IntN(4)),
					}),
					energy.OpSetExecsOp(fmt.Sprintf("base%02d", rng.IntN(4)), int64(1+rng.IntN(5))),
				}
				if rng.IntN(2) == 0 {
					ops = append(ops, energy.OpRemoveOp(fmt.Sprintf("w%02d-r%03d", worker, round)))
				}
				if err := allocator.Merge(ops); err != nil {
					t.Errorf("Merge: %v", err)
					return
				}
				if _, err := allocator.Allocate(int64(1 + rng.IntN(64))); err != nil {
					t.Errorf("concurrent Allocate: %v", err)
					return
				}
				_ = allocator.Frequencies()
				_ = allocator.Samples()
			}
		}(worker)
	}
	close(start)
	wg.Wait()

	for id := 0; id < 4; id++ {
		if _, ok := allocator.Get(fmt.Sprintf("base%02d", id)); !ok {
			t.Fatalf("base sample base%02d disappeared", id)
		}
	}
	assertStateMatchesModel(t, allocator, samplesToModel(allocator.Samples()))
	assertAllocationMatchesOracle(t, allocator, 100)

	// Repeated deterministic rounds must produce identical output.
	first, err := allocator.Allocate(100)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	for round := 0; round < 10; round++ {
		again, err := allocator.Allocate(100)
		if err != nil {
			t.Fatalf("Allocate repeat: %v", err)
		}
		assertAllocationsEqual(t, again, first)
	}
}

// TestConcurrentConflictingAdds proves that duplicate inserts inside
// concurrent batches cannot corrupt state: exactly one wins, the rest fail.
func TestConcurrentConflictingAdds(t *testing.T) {
	allocator := energy.New()
	start := make(chan struct{})
	const contenders = 16
	var wg sync.WaitGroup
	results := make([]error, contenders)
	for index := 0; index < contenders; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			results[index] = allocator.Merge([]energy.Op{
				energy.OpAddOp(energy.Sample{ID: "only", Coverage: []energy.Feature{1}, Execs: 1}),
			})
		}(index)
	}
	close(start)
	wg.Wait()

	successes, failures := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, energy.ErrDuplicateID):
			failures++
		default:
			t.Fatalf("unexpected merge error: %v", err)
		}
	}
	if successes != 1 || failures != contenders-1 {
		t.Fatalf("conflicting adds: successes=%d failures=%d", successes, failures)
	}
	if allocator.Len() != 1 {
		t.Fatalf("allocator length = %d, want 1", allocator.Len())
	}
	assertAllocationMatchesOracle(t, allocator, 10)
}
