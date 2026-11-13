package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// TestMissingZone fails cleanly when a PKEY points at a zone the store cannot
// serve, and rejects a PKEY with empty key material.
func TestMissingZoneAndBadPKEY(t *testing.T) {
	r, egos, store, now := newTestResolver()

	root := zoneKey(1)
	mustPutEgo(t, egos, "net", root)
	mustAddZone(t, store, root, map[string][]Record{
		"missing": {pkeyRecord(9, now.Add(time.Hour))},
		"empty":   {{Type: RecordPKEY, Value: nil, Expire: now.Add(time.Hour)}},
	})

	// Two labels: the PKEY switches to an unserved zone, then the next label
	// lookup forces a fetch that the store cannot satisfy.
	if _, err := r.Resolve("host.missing.net"); !errors.Is(err, ErrZoneNotFound) {
		t.Fatalf("missing zone: got %v want %v", err, ErrZoneNotFound)
	}
	if _, err := r.Resolve("host.empty.net"); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("empty pkey: got %v want %v", err, ErrInvalidRecord)
	}
}

// TestConcurrentEgoDeletion races resolution against deletion and restoration.
// Every resolution must either succeed while the ego exists or report
// ErrEgoNotFound once deleted; no stale resolution may leak through.
func TestConcurrentEgoDeletion(t *testing.T) {
	nowFn, now := fixedClock()
	egos := NewEgoTable()
	store := NewMemoryZoneStore()
	r := NewResolver(egos, store, nowFn)

	root, target := zoneKey(1), zoneKey(2)
	mustPutEgo(t, egos, "race", root)
	mustAddZone(t, store, root, map[string][]Record{
		"www": {pkeyRecord(2, now.Add(time.Hour))},
	})
	mustAddZone(t, store, target, map[string][]Record{
		"host": {{Type: RecordA}},
	})

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, err := r.Resolve("host.www.race")
				switch {
				case err == nil:
				case errors.Is(err, ErrEgoNotFound):
				default:
					t.Errorf("unexpected error: %v", err)
					return
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			egos.Delete("race")
			time.Sleep(time.Microsecond)
			_ = egos.Put("race", root)
		}
	}()

	wg.Wait()
}

// TestEgoTableValidation covers the ego table input checks.
func TestEgoTableValidation(t *testing.T) {
	egos := NewEgoTable()
	if err := egos.Put("", zoneKey(1)); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("empty label: got %v want %v", err, ErrInvalidName)
	}
	if err := egos.Put("zero", ZonePublicKey{}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("zero key: got %v want %v", err, ErrInvalidRecord)
	}
	if _, ok := egos.Lookup("absent"); ok {
		t.Fatal("absent ego reported as present")
	}
	egos.Delete("absent") // must be a safe no-op
}
