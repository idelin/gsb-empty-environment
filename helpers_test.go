package netsec

import (
	"strings"
	"testing"
	"time"
)

// fixedClock returns a constant instant so expiration checks are deterministic.
func fixedClock() (func() time.Time, time.Time) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return now }, now
}

func zoneKey(id byte) ZonePublicKey {
	return ZonePublicKey{Key: []byte{0x5a, id}}
}

func pkeyRecord(id byte, expire time.Time) Record {
	return Record{Type: RecordPKEY, Value: zoneKey(id).Key, Expire: expire}
}

// countingStore wraps MemoryZoneStore and counts Zone lookups, proving which
// zone keys the resolver actually reached for.
type countingStore struct {
	*MemoryZoneStore
	calls int
}

func (s *countingStore) Zone(k ZonePublicKey) (*Zone, bool) {
	s.calls++
	return s.MemoryZoneStore.Zone(k)
}

func newTestResolver() (*Resolver, *EgoTable, *countingStore, time.Time) {
	nowFn, now := fixedClock()
	egos := NewEgoTable()
	store := &countingStore{MemoryZoneStore: NewMemoryZoneStore()}
	return NewResolver(egos, store, nowFn), egos, store, now
}

func mustPutEgo(t *testing.T, egos *EgoTable, label string, k ZonePublicKey) {
	t.Helper()
	if err := egos.Put(label, k); err != nil {
		t.Fatalf("ego put %q: %v", label, err)
	}
}

func mustAddZone(t *testing.T, store ZoneStore, k ZonePublicKey, records map[string][]Record) {
	t.Helper()
	adder, ok := store.(interface {
		AddZone(*Zone) error
	})
	if !ok {
		t.Fatalf("store %T cannot add zones", store)
	}
	if err := adder.AddZone(&Zone{Key: k, Records: records}); err != nil {
		t.Fatalf("add zone %x: %v", k.Key, err)
	}
}

func labelName(i int) string {
	return "l" + string(rune('a'+i-1))
}

// joinReverse joins labels (given left-to-right) into a name fragment.
func joinReverse(labels []string) string {
	return strings.Join(labels, ".")
}
