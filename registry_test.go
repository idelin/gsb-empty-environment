package strdict

import (
	"errors"
	"testing"
)

func frozenDict(t *testing.T, version uint32, entries ...string) *Dictionary {
	t.Helper()
	d := NewDictionary(version)
	for _, s := range entries {
		if _, err := d.Intern(s); err != nil {
			t.Fatal(err)
		}
	}
	d.Freeze()
	return d
}

func TestPublishRules(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Publish(NewDictionary(1)); !errors.Is(err, ErrNotFrozen) {
		t.Fatalf("Publish(unfrozen) = %v, want ErrNotFrozen", err)
	}
	d := frozenDict(t, 1, "a")
	if err := reg.Publish(d); err != nil {
		t.Fatal(err)
	}
	if err := reg.Publish(d); !errors.Is(err, ErrVersionExists) {
		t.Fatalf("Publish(dup) = %v, want ErrVersionExists", err)
	}
}

func TestLeaseBlocksReclaim(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Publish(frozenDict(t, 1, "a")); err != nil {
		t.Fatal(err)
	}
	lease, err := reg.Acquire(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Reclaim(1); !errors.Is(err, ErrLeaseActive) {
		t.Fatalf("Reclaim with lease = %v, want ErrLeaseActive", err)
	}
	lease.Release()
	if err := reg.Reclaim(1); err != nil {
		t.Fatalf("Reclaim after release = %v", err)
	}
	if _, err := reg.Acquire(1); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("Acquire reclaimed = %v, want ErrUnknownVersion", err)
	}
}

func TestBatchCancelReleasesLeases(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Publish(frozenDict(t, 1, "a")); err != nil {
		t.Fatal(err)
	}
	if err := reg.Publish(frozenDict(t, 2, "b")); err != nil {
		t.Fatal(err)
	}
	b := reg.BeginBatch()
	l1, err := b.Acquire(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Acquire(2); err != nil {
		t.Fatal(err)
	}
	b.Cancel()
	if !l1.Released() {
		t.Fatal("lease not released after Cancel")
	}
	if _, err := b.Acquire(1); !errors.Is(err, ErrCancelled) {
		t.Fatalf("Acquire after Cancel = %v, want ErrCancelled", err)
	}
	// All leases gone: both generations reclaimable.
	if err := reg.Reclaim(1); err != nil {
		t.Fatalf("Reclaim(1) after Cancel = %v", err)
	}
	if err := reg.Reclaim(2); err != nil {
		t.Fatalf("Reclaim(2) after Cancel = %v", err)
	}
	b.Cancel() // idempotent
}
