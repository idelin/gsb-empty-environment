package agglog

import (
	"fmt"
	"math/rand"
	"testing"
)

// randomUniverse builds n events for one key with distinct ids.
func randomUniverse(rng *rand.Rand, key string, n int) []Event {
	events := make([]Event, n)
	for i := range events {
		events[i] = Event{
			ID:    fmt.Sprintf("%s-%04d", key, i),
			Key:   key,
			Delta: int64(rng.Intn(201) - 100),
		}
	}
	return events
}

// randomPackages cuts the universe into overlapping packages: each
// package draws a random subset, so ids may appear in many packages.
func randomPackages(rng *rand.Rand, key string, universe []Event, count int) []Package {
	packages := make([]Package, 0, count)
	for len(packages) < count {
		var subset []Event
		for _, ev := range universe {
			if rng.Intn(2) == 0 {
				subset = append(subset, ev)
			}
		}
		if len(subset) == 0 {
			continue
		}
		p, err := Pack(subset)
		if err != nil {
			panic(err)
		}
		packages = append(packages, p)
	}
	return packages
}

// TestStoreConvergesToPerEventModel is the main property: for any
// mix of overlapping packages, replays, and unpack orders, the
// store's per-key state equals the per-event set model's.
func TestStoreConvergesToPerEventModel(t *testing.T) {
	for trial := 0; trial < 200; trial++ {
		rng := rand.New(rand.NewSource(int64(trial)))
		key := fmt.Sprintf("k%d", rng.Intn(3))
		universe := randomUniverse(rng, key, 1+rng.Intn(30))
		packages := randomPackages(rng, key, universe, 1+rng.Intn(8))

		model := NewModel()
		for _, ev := range universe {
			if c := model.Add(ev); c != nil {
				t.Fatalf("trial %d: model conflict on fresh universe: %+v", trial, c)
			}
		}

		store := NewStore()
		// Apply packages in a random order, replaying some of them.
		order := rng.Perm(len(packages))
		for _, i := range order {
			if r := store.Apply(packages[i]); !r.OK() {
				t.Fatalf("trial %d: unexpected conflicts %+v", trial, r.Conflicts)
			}
			if rng.Intn(3) == 0 {
				if r := store.Apply(packages[i]); !r.OK() || len(r.Applied) != 0 {
					t.Fatalf("trial %d: replay not idempotent: %+v", trial, r)
				}
			}
		}

		// The store must hold exactly the union of packaged events.
		want := NewModel()
		for _, p := range packages {
			want.AddPackage(p)
		}
		if !Equivalent(store, want, key) {
			t.Fatalf("trial %d: store diverged from packaged union", trial)
		}
		// Packaged union must be a subset of the full universe model,
		// with matching deltas on shared ids.
		for id, d := range want.Events(key) {
			if model.Events(key)[id] != d {
				t.Fatalf("trial %d: event %s delta mismatch vs universe", trial, id)
			}
		}
		if store.Total(key) != want.Total(key) {
			t.Fatalf("trial %d: total %d != model total %d",
				trial, store.Total(key), want.Total(key))
		}
	}
}

// TestConflictingOverlapMatchesModelConflict feeds the same
// overlapping packages to store and model and requires identical
// conflict sets, regardless of application order.
func TestConflictingOverlapMatchesModelConflict(t *testing.T) {
	for trial := 0; trial < 200; trial++ {
		rng := rand.New(rand.NewSource(int64(1000 + trial)))
		key := "k"
		n := 1 + rng.Intn(10)
		base := randomUniverse(rng, key, n)

		// Second wave reuses some ids with (sometimes) altered deltas.
		var wave []Event
		for _, ev := range base {
			if rng.Intn(2) == 0 {
				continue
			}
			if rng.Intn(2) == 0 {
				ev.Delta++ // conflicting redefinition
			}
			wave = append(wave, ev)
		}
		if len(wave) == 0 {
			continue
		}

		p1, err := Pack(base)
		if err != nil {
			t.Fatal(err)
		}
		p2, err := Pack(wave)
		if err != nil {
			t.Fatal(err)
		}

		store := NewStore()
		store.Apply(p1)
		sr := store.Apply(p2)

		model := NewModel()
		model.AddPackage(p1)
		mc := model.AddPackage(p2)

		if len(sr.Conflicts) != len(mc) {
			t.Fatalf("trial %d: store conflicts %v, model conflicts %v",
				trial, sr.Conflicts, mc)
		}
		for i := range mc {
			if sr.Conflicts[i] != mc[i] {
				t.Fatalf("trial %d: conflict %d: store %+v, model %+v",
					trial, i, sr.Conflicts[i], mc[i])
			}
		}
		if !Equivalent(store, model, key) {
			t.Fatalf("trial %d: store diverged from model after conflicts", trial)
		}
	}
}
