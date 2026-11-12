package lsadb

import (
	"testing"
	"time"
)

func mustRouterID(v uint32) RouterID {
	return RouterID(v)
}

var (
	localID    = mustRouterID(0x0A000001) // 10.0.0.1
	neighborID = mustRouterID(0x0A000002) // 10.0.0.2
	attackerID = mustRouterID(0x0A000003) // 10.0.0.3

	testLSID = mustRouterID(0xC0A80100) // 192.168.1.0
)

// fakeClock is a manually controlled time source for rate-limit tests.
type fakeClock struct {
	t time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// buildLSA constructs a valid, checksummed LSA for tests.
func buildLSA(t *testing.T, advertiser RouterID, lsType uint8, lsid RouterID, seq int32, age uint16, body []byte) *LSA {
	t.Helper()
	if body == nil {
		body = []byte{0x01, 0x02, 0x03, 0x04}
	}
	l := &LSA{
		LSAGE:             age,
		Options:           2, // E-bit capable.
		LSType:            lsType,
		LinkStateID:       lsid,
		AdvertisingRouter: advertiser,
		SeqNum:            seq,
		Body:              body,
	}
	if _, err := l.Marshal(); err != nil {
		t.Fatalf("marshal LSA: %v", err)
	}
	return l
}

func newTestDB(t *testing.T, interval time.Duration, clock *fakeClock) *Database {
	t.Helper()
	return NewDatabase(localID, DefenseConfig{
		ReoriginateInterval: interval,
		Clock:               clock.now,
	})
}
