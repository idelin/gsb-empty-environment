package dircat

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func mustApply(t *testing.T, d *Directory, ops ...Op) {
	t.Helper()
	if err := d.Apply(ops...); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
}

func TestBatchNameSwap(t *testing.T) {
	d := New()
	mustApply(t, d,
		Put(Object{ID: "a", Name: "alpha", Tags: []string{"x"}}),
		Put(Object{ID: "b", Name: "beta", Tags: []string{"y"}}),
	)
	// Swap names in a single batch: legal because unbind happens first.
	mustApply(t, d,
		Put(Object{ID: "a", Name: "beta"}),
		Put(Object{ID: "b", Name: "alpha"}),
	)
	if o, ok := d.GetByName("alpha"); !ok || o.ID != "b" {
		t.Fatalf("alpha should resolve to b, got %+v ok=%v", o, ok)
	}
	if o, ok := d.GetByName("beta"); !ok || o.ID != "a" {
		t.Fatalf("beta should resolve to a, got %+v ok=%v", o, ok)
	}
	// Rename drops old tags when the put omits them.
	if got := d.GetByTag("x"); len(got) != 0 {
		t.Fatalf("tag x should be empty after rename, got %v", got)
	}
	if err := d.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestConflictFailsWholeBatch(t *testing.T) {
	d := New()
	mustApply(t, d,
		Put(Object{ID: "a", Name: "alpha", Tags: []string{"x"}}),
		Put(Object{ID: "b", Name: "beta", Tags: []string{"y"}}),
	)
	before := d.Snapshot()

	// Batch: valid new object + delete + a put that steals alpha's name.
	err := d.Apply(
		Put(Object{ID: "c", Name: "gamma", Tags: []string{"z"}}),
		Delete("b"),
		Put(Object{ID: "d", Name: "alpha"}),
	)
	if _, ok := err.(*ConflictError); !ok {
		t.Fatalf("expected ConflictError, got %v", err)
	}

	// Rollback: nothing from the batch may be visible.
	if _, ok := d.GetByID("c"); ok {
		t.Fatal("c must not exist after failed batch")
	}
	if _, ok := d.GetByID("d"); ok {
		t.Fatal("d must not exist after failed batch")
	}
	if o, ok := d.GetByID("b"); !ok || o.Name != "beta" {
		t.Fatal("b must survive the failed batch")
	}
	if o, ok := d.GetByName("alpha"); !ok || o.ID != "a" {
		t.Fatal("alpha must still belong to a")
	}
	if got := d.GetByTag("y"); len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("tag y must still list b, got %v", got)
	}
	if err := d.Verify(); err != nil {
		t.Fatalf("Verify after rollback: %v", err)
	}

	// The pre-batch snapshot is unaffected too.
	if o, ok := before.GetByName("beta"); !ok || o.ID != "b" {
		t.Fatal("snapshot must still resolve beta to b")
	}
	if err := before.Verify(); err != nil {
		t.Fatalf("snapshot Verify: %v", err)
	}
}

func TestDuplicateNameInsideBatch(t *testing.T) {
	d := New()
	mustApply(t, d, Put(Object{ID: "a", Name: "alpha"}))
	err := d.Apply(
		Put(Object{ID: "b", Name: "shared"}),
		Put(Object{ID: "c", Name: "shared"}),
	)
	if _, ok := err.(*ConflictError); !ok {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	if _, ok := d.GetByID("b"); ok {
		t.Fatal("b must not exist after failed batch")
	}
	if _, ok := d.GetByID("c"); ok {
		t.Fatal("c must not exist after failed batch")
	}
	if err := d.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestDeleteClearsReverseTagRefs(t *testing.T) {
	d := New()
	mustApply(t, d,
		Put(Object{ID: "a", Name: "alpha", Tags: []string{"x", "y"}}),
		Put(Object{ID: "b", Name: "beta", Tags: []string{"y", "z"}}),
	)
	mustApply(t, d, Delete("a"))

	if got := d.GetByTag("x"); len(got) != 0 {
		t.Fatalf("tag x must be empty after deleting a, got %v", got)
	}
	got := d.GetByTag("y")
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("tag y must only list b, got %v", got)
	}
	if _, ok := d.GetByName("alpha"); ok {
		t.Fatal("alpha must be gone from the name index")
	}
	// Rebuild check proves tagsOf/byTag hold no stale reverse refs to a.
	if err := d.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// checkSnapshot asserts that a snapshot's three indexes agree with each
// other and with the rebuilt indexes.
func checkSnapshot(t *testing.T, s *Snapshot) {
	t.Helper()
	if err := s.Verify(); err != nil {
		t.Fatalf("snapshot index drift: %v", err)
	}
	for _, tag := range []string{"t0", "t1", "t2", "t3"} {
		for _, o := range s.GetByTag(tag) {
			byID, ok := s.GetByID(o.ID)
			if !ok || byID.Name != o.Name {
				t.Fatalf("id index disagrees with tag index for %q", o.ID)
			}
			byName, ok := s.GetByName(o.Name)
			if !ok || byName.ID != o.ID {
				t.Fatalf("name index disagrees with tag index for %q", o.Name)
			}
		}
	}
}

func TestSnapshotConsistencyUnderConcurrency(t *testing.T) {
	d := New()
	const objects = 8
	for i := 0; i < objects; i++ {
		mustApply(t, d, Put(Object{
			ID:   fmt.Sprintf("obj-%d", i),
			Name: fmt.Sprintf("name-%d", i),
			Tags: []string{fmt.Sprintf("t%d", i%4)},
		}))
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer: rotate names forward (obj-i takes the name of obj-(i+1)) and
	// flip tags, always in one atomic batch so every committed state is
	// consistent.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for round := 0; round < 200; round++ {
			ops := make([]Op, 0, objects)
			for i := 0; i < objects; i++ {
				ops = append(ops, Put(Object{
					ID:   fmt.Sprintf("obj-%d", i),
					Name: fmt.Sprintf("name-%d", (i+1+round)%objects),
					Tags: []string{fmt.Sprintf("t%d", (i+round)%4)},
				}))
			}
			if err := d.Apply(ops...); err != nil {
				t.Errorf("writer Apply: %v", err)
				return
			}
		}
		close(stop)
	}()

	// Readers: grab snapshots mid-write and demand cross-index agreement.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s := d.Snapshot()
				if s.Len() != objects {
					t.Errorf("snapshot Len = %d, want %d", s.Len(), objects)
					return
				}
				checkSnapshot(t, s)
			}
		}()
	}
	wg.Wait()
	if err := d.Verify(); err != nil {
		t.Fatalf("final Verify: %v", err)
	}
}

func TestRandomBatchesAlwaysVerify(t *testing.T) {
	d := New()
	rng := rand.New(rand.NewSource(1))
	for batch := 0; batch < 300; batch++ {
		var ops []Op
		for i, n := 0, 1+rng.Intn(6); i < n; i++ {
			id := fmt.Sprintf("id-%d", rng.Intn(20))
			if rng.Intn(4) == 0 {
				ops = append(ops, Delete(id))
				continue
			}
			var tags []string
			for j, m := 0, rng.Intn(4); j < m; j++ {
				tags = append(tags, fmt.Sprintf("tag-%d", rng.Intn(6)))
			}
			ops = append(ops, Put(Object{
				ID:   id,
				Name: fmt.Sprintf("name-%d", rng.Intn(20)),
				Tags: tags,
			}))
		}
		_ = d.Apply(ops...) // conflicts are fine; state must stay valid
		if err := d.Verify(); err != nil {
			t.Fatalf("batch %d: %v", batch, err)
		}
	}
}
