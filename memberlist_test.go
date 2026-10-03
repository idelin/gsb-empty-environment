package noderouter

import (
	"sync"
	"testing"
	"time"
)

func TestVersionArbitration(t *testing.T) {
	ml := NewMemberList("n1")
	ml.Join("127.0.0.1:7001", Metadata{Zone: "us-east", Weight: 10, Role: RoleWorker, Healthy: true}, nil)

	// Stale update (lower version) must be rejected.
	fresh, _ := ml.Get("n1")
	stale := fresh
	stale.Meta.Weight = 999
	stale.Version = fresh.Version - 1
	if applied := ml.Merge([]NodeState{stale}); applied != 0 {
		t.Fatalf("stale update applied, want 0 applied, got %d", applied)
	}
	if got, _ := ml.Get("n1"); got.Meta.Weight != 10 {
		t.Fatalf("stale update overwrote weight: got %d, want 10", got.Meta.Weight)
	}

	// Newer update (higher version) must win.
	newer := fresh
	newer.Meta.Weight = 80
	newer.Version = fresh.Version + 1
	newer.UpdatedAt = fresh.UpdatedAt.Add(time.Second)
	if applied := ml.Merge([]NodeState{newer}); applied != 1 {
		t.Fatalf("newer update rejected, want 1 applied, got %d", applied)
	}
	if got, _ := ml.Get("n1"); got.Meta.Weight != 80 {
		t.Fatalf("weight not updated: got %d, want 80", got.Meta.Weight)
	}
}

func TestConcurrentMetadataUpdates(t *testing.T) {
	ml := NewMemberList("n1")
	ml.Join("127.0.0.1:7001", Metadata{Zone: "us-east", Weight: 10, Role: RoleWorker, Healthy: true}, nil)

	const writers = 32
	const updatesPerWriter = 50
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < updatesPerWriter; i++ {
				if _, err := ml.UpdateLocal(func(meta *Metadata) {
					meta.Weight++
				}); err != nil {
					t.Errorf("UpdateLocal: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	got, _ := ml.Get("n1")
	wantWeight := 10 + writers*updatesPerWriter
	if got.Meta.Weight != wantWeight {
		t.Fatalf("weight = %d, want %d (lost update under concurrency)", got.Meta.Weight, wantWeight)
	}
	wantVersion := uint64(1 + writers*updatesPerWriter)
	if got.Version != wantVersion {
		t.Fatalf("version = %d, want %d", got.Version, wantVersion)
	}
}

func TestMergeTieBreakDeterministic(t *testing.T) {
	ml := NewMemberList("observer")
	base := NodeState{
		ID: "n1", Version: 5, UpdatedAt: time.Unix(1000, 0),
		Meta: Metadata{Zone: "us-east", Weight: 10, Healthy: true},
	}
	// Same version, newer timestamp wins.
	a := base
	a.Meta.Weight = 20
	a.UpdatedAt = time.Unix(2000, 0)
	ml.Merge([]NodeState{base, a})
	if got, _ := ml.Get("n1"); got.Meta.Weight != 20 {
		t.Fatalf("tie-break by UpdatedAt failed: weight = %d, want 20", got.Meta.Weight)
	}
	// Replaying the older same-version state must not roll back.
	ml.Merge([]NodeState{base})
	if got, _ := ml.Get("n1"); got.Meta.Weight != 20 {
		t.Fatalf("replay rolled back state: weight = %d, want 20", got.Meta.Weight)
	}
}
