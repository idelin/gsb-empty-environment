package netsec

import (
	"errors"
	"testing"
	"time"
)

// TestDelegationLoop verifies that any repeated zone key aborts the walk,
// including a zone that delegates straight back to itself.
func TestDelegationLoop(t *testing.T) {
	r, egos, store, now := newTestResolver()

	root, a, b := zoneKey(1), zoneKey(2), zoneKey(3)
	mustPutEgo(t, egos, "loop", root)
	mustAddZone(t, store, root, map[string][]Record{
		"a": {pkeyRecord(2, now.Add(time.Hour))},
	})
	mustAddZone(t, store, a, map[string][]Record{
		"b": {pkeyRecord(3, now.Add(time.Hour))},
	})
	mustAddZone(t, store, b, map[string][]Record{
		"back": {pkeyRecord(1, now.Add(time.Hour))}, // back to the ego zone
	})

	if _, err := r.Resolve("back.b.a.loop"); !errors.Is(err, ErrDelegationLoop) {
		t.Fatalf("cross-zone loop: got %v want %v", err, ErrDelegationLoop)
	}

	// Self loop: zone a delegates straight to itself.
	nowFn, _ := fixedClock()
	selfEgos := NewEgoTable()
	selfStore := NewMemoryZoneStore()
	selfResolver := NewResolver(selfEgos, selfStore, nowFn)
	mustPutEgo(t, selfEgos, "self", root)
	mustAddZone(t, selfStore, root, map[string][]Record{
		"a": {pkeyRecord(2, now.Add(time.Hour))},
	})
	mustAddZone(t, selfStore, a, map[string][]Record{
		"me": {pkeyRecord(2, now.Add(time.Hour))},
	})
	if _, err := selfResolver.Resolve("me.a.self"); !errors.Is(err, ErrDelegationLoop) {
		t.Fatalf("self loop: got %v want %v", err, ErrDelegationLoop)
	}
}

// TestExpiredRecords covers absolute expiration: records at or before now are
// skipped, future records survive, and a zero Expire means never expires.
func TestExpiredRecords(t *testing.T) {
	r, egos, store, now := newTestResolver()

	root, target := zoneKey(1), zoneKey(2)
	mustPutEgo(t, egos, "exp", root)
	mustAddZone(t, store, root, map[string][]Record{
		"old":     {pkeyRecord(2, now.Add(-time.Second))},
		"exact":   {pkeyRecord(2, now)},
		"fresh":   {pkeyRecord(2, now.Add(time.Second))},
		"eternal": {pkeyRecord(2, time.Time{})},
	})
	mustAddZone(t, store, target, map[string][]Record{
		"host": {{Type: RecordA, Value: []byte("data")}},
	})

	for _, label := range []string{"old", "exact"} {
		if _, err := r.Resolve(label + ".exp"); !errors.Is(err, ErrLabelNotFound) {
			t.Fatalf("%s PKEY should be skipped: got %v want %v", label, err, ErrLabelNotFound)
		}
	}

	for _, label := range []string{"fresh", "eternal"} {
		res, err := r.Resolve(label + ".exp")
		if err != nil {
			t.Fatalf("%s delegation: %v", label, err)
		}
		if !res.Zone.Equal(target) {
			t.Fatalf("%s resolved to %x want %x", label, res.Zone.Key, target.Key)
		}
	}
}

// TestPKEYCNAMEConflict requires a block holding both a live PKEY and a live
// CNAME to be rejected wholesale; an expired CNAME no longer conflicts.
func TestPKEYCNAMEConflict(t *testing.T) {
	r, egos, store, now := newTestResolver()

	root, target := zoneKey(1), zoneKey(2)
	mustPutEgo(t, egos, "mix", root)
	mustAddZone(t, store, root, map[string][]Record{
		"clash": {
			pkeyRecord(2, now.Add(time.Hour)),
			{Type: RecordCNAME, Value: []byte("elsewhere.mix"), Expire: now.Add(time.Hour)},
		},
		"staleclash": {
			pkeyRecord(2, now.Add(time.Hour)),
			{Type: RecordCNAME, Value: []byte("elsewhere.mix"), Expire: now.Add(-time.Hour)},
		},
	})
	mustAddZone(t, store, target, map[string][]Record{
		"host": {{Type: RecordA, Value: []byte("data")}},
	})

	if _, err := r.Resolve("clash.mix"); !errors.Is(err, ErrConflictingRecords) {
		t.Fatalf("live conflict: got %v want %v", err, ErrConflictingRecords)
	}

	// With the CNAME expired, the surviving PKEY delegates normally.
	res, err := r.Resolve("staleclash.mix")
	if err != nil {
		t.Fatalf("expired CNAME should not conflict: %v", err)
	}
	if !res.Zone.Equal(target) {
		t.Fatalf("staleclash zone: got %x want %x", res.Zone.Key, target.Key)
	}
}

// TestEgoDeletion ensures a deleted ego cannot resolve anything rooted at its
// label, and that a subsequent Put restores resolution.
func TestEgoDeletion(t *testing.T) {
	r, egos, store, now := newTestResolver()

	root, target := zoneKey(1), zoneKey(2)
	mustPutEgo(t, egos, "gone", root)
	mustAddZone(t, store, root, map[string][]Record{
		"www": {pkeyRecord(2, now.Add(time.Hour))},
	})
	mustAddZone(t, store, target, map[string][]Record{
		"host": {{Type: RecordA}},
	})

	if _, err := r.Resolve("www.gone"); err != nil {
		t.Fatalf("pre-delete resolve: %v", err)
	}

	egos.Delete("gone")
	for _, name := range []string{"gone", "www.gone", "host.www.gone"} {
		if _, err := r.Resolve(name); !errors.Is(err, ErrEgoNotFound) {
			t.Fatalf("%s after deletion: got %v want %v", name, err, ErrEgoNotFound)
		}
	}

	mustPutEgo(t, egos, "gone", root)
	if _, err := r.Resolve("www.gone"); err != nil {
		t.Fatalf("post-restore resolve: %v", err)
	}
}

// TestCNAMERedirect exercises alias restarts and a non-terminating alias loop.
func TestCNAMERedirect(t *testing.T) {
	nowFn, now := fixedClock()
	egos := NewEgoTable()
	store := NewMemoryZoneStore()
	r := NewResolver(egos, store, nowFn)

	root, target := zoneKey(1), zoneKey(2)
	mustPutEgo(t, egos, "alias", root)
	mustPutEgo(t, egos, "real", target)
	mustAddZone(t, store, root, map[string][]Record{
		"www": {{Type: RecordCNAME, Value: []byte("host.real"), Expire: now.Add(time.Hour)}},
	})
	mustAddZone(t, store, target, map[string][]Record{
		"host": {{Type: RecordA, Value: []byte("data")}},
	})

	res, err := r.Resolve("www.alias")
	if err != nil {
		t.Fatalf("cname resolve: %v", err)
	}
	if res.Name != "host.real" || !res.Zone.Equal(target) {
		t.Fatalf("cname result wrong: name=%q zone=%x", res.Name, res.Zone.Key)
	}

	// A self-referential alias must eventually give up rather than spin.
	loopEgos := NewEgoTable()
	loopStore := NewMemoryZoneStore()
	loopResolver := NewResolver(loopEgos, loopStore, nowFn)
	mustPutEgo(t, loopEgos, "spin", root)
	mustAddZone(t, loopStore, root, map[string][]Record{
		"x": {{Type: RecordCNAME, Value: []byte("x.spin"), Expire: now.Add(time.Hour)}},
	})
	if _, err := loopResolver.Resolve("x.spin"); !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("cname loop: got %v want %v", err, ErrTooManyRedirects)
	}
}

// TestInvalidNames covers malformed input and trailing-dot normalization.
func TestInvalidNames(t *testing.T) {
	r, egos, _, _ := newTestResolver()
	mustPutEgo(t, egos, "ok", zoneKey(1))

	for _, name := range []string{"", ".", "a..ok", ".ok"} {
		if _, err := r.Resolve(name); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("Resolve(%q): got %v want %v", name, err, ErrInvalidName)
		}
	}

	res, err := r.Resolve("ok.")
	if err != nil {
		t.Fatalf("trailing dot name: %v", err)
	}
	if !res.Zone.Equal(zoneKey(1)) {
		t.Fatalf("trailing dot zone mismatch: %x", res.Zone.Key)
	}
}
