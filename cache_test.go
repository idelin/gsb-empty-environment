package netsec_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	netsec "example.org/netsec/q0079"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newCache(t *testing.T, zone string, clock netsec.Clock) *netsec.Cache {
	t.Helper()
	c, err := netsec.NewCache(zone, netsec.WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func recordAndReceipt(owner, next string, types netsec.TypeBitmap, ttl uint32, serial uint32, expires time.Time) (netsec.NSECRecord, netsec.Receipt) {
	id := fmt.Sprintf("receipt:%s:%s:%d", owner, next, serial)
	rec := netsec.NSECRecord{
		Owner:            owner,
		Next:             next,
		Types:            append(netsec.TypeBitmap(nil), types...),
		TTL:              ttl,
		SignatureExpires: expires,
		ReceiptID:        id,
		ZoneSerial:       serial,
	}
	return rec, netsec.Receipt{
		ID:               id,
		Zone:             "z.",
		Owner:            owner,
		Next:             next,
		Types:            append(netsec.TypeBitmap(nil), types...),
		SignatureExpires: expires,
		ZoneSerial:       serial,
	}
}

func addRecord(t *testing.T, c *netsec.Cache, clock *testClock, owner, next string, types netsec.TypeBitmap, serial uint32) {
	t.Helper()
	rec, receipt := recordAndReceipt(owner, next, types, 300, serial, clock.Now().Add(time.Hour))
	if err := c.Insert(rec, receipt); err != nil {
		t.Fatalf("Insert(%s -> %s): %v", owner, next, err)
	}
}

func requireStatus(t *testing.T, c *netsec.Cache, qname string, qtype uint16, want netsec.Status) netsec.Answer {
	t.Helper()
	answer, err := c.Query(qname, qtype)
	if err != nil {
		t.Fatalf("Query(%s, %d): %v", qname, qtype, err)
	}
	if answer.Status != want {
		t.Fatalf("Query(%s, %d).Status = %v, want %v", qname, qtype, answer.Status, want)
	}
	return answer
}

func TestNODATAOnlyAtExactOwner(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, "z.", clock)
	addRecord(t, c, clock, "z.", "*.z.", netsec.TypeBitmap{netsec.TypeNSEC, netsec.TypeRRSIG}, 1)
	addRecord(t, c, clock, "*.z.", "a.z.", netsec.TypeBitmap{netsec.TypeNSEC, netsec.TypeRRSIG}, 1)
	addRecord(t, c, clock, "a.z.", "b.z.", netsec.TypeBitmap{netsec.TypeNSEC, netsec.TypeRRSIG, netsec.TypeA}, 1)
	addRecord(t, c, clock, "b.z.", "z.", netsec.TypeBitmap{netsec.TypeNSEC, netsec.TypeCNAME, netsec.TypeA}, 1)

	answer := requireStatus(t, c, "a.z.", netsec.TypeTXT, netsec.NoData)
	if len(answer.Proof) != 1 || answer.Proof[0].Owner != "a.z." {
		t.Fatalf("NODATA proof = %#v, want exact owner only", answer.Proof)
	}
	requireStatus(t, c, "a.z.", netsec.TypeA, netsec.NeedUpstream)
	requireStatus(t, c, "a.z.", netsec.TypeANY, netsec.NeedUpstream)

	// A CNAME at the exact owner means a missing type is not a plain NODATA
	// answer; the cache lacks the information needed to follow the CNAME.
	requireStatus(t, c, "b.z.", netsec.TypeTXT, netsec.NeedUpstream)
	requireStatus(t, c, "b.z.", netsec.TypeCNAME, netsec.NeedUpstream)
}

func TestOwnerEqualsNextCoversWholeRing(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, "z.", clock)
	addRecord(t, c, clock, "z.", "z.", netsec.TypeBitmap{netsec.TypeNSEC, netsec.TypeRRSIG}, 1)

	answer := requireStatus(t, c, "a.a.z.", netsec.TypeA, netsec.NXDomain)
	if len(answer.Proof) != 1 || answer.Proof[0].Owner != "z." || answer.Proof[0].Next != "z." {
		t.Fatalf("proof = %#v, want the owner==next record", answer.Proof)
	}
	requireStatus(t, c, "z.", netsec.TypeTXT, netsec.NoData)
	requireStatus(t, c, "z.", netsec.TypeA, netsec.NoData)
}

func TestRootZone(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, ".", clock)
	rec, receipt := recordAndReceipt(".", ".", netsec.TypeBitmap{netsec.TypeNSEC}, 300, 1, clock.Now().Add(time.Hour))
	receipt.Zone = "."
	if err := c.Add(rec, receipt); err != nil {
		t.Fatal(err)
	}
	answer := requireStatus(t, c, "a.", netsec.TypeA, netsec.NXDomain)
	if answer.Wildcard != "*." || len(answer.Proof) != 1 {
		t.Fatalf("root-zone answer = %#v", answer)
	}
	requireStatus(t, c, ".", netsec.TypeA, netsec.NoData)
}

func TestStrictIntervalBoundaries(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, "z.", clock)
	addRecord(t, c, clock, "z.", "b.z.", netsec.TypeBitmap{netsec.TypeNSEC}, 1)

	// a.z is strictly between owner and next, and the same record also covers
	// *.z, so one minimal proof item is enough.
	answer := requireStatus(t, c, "a.z.", netsec.TypeA, netsec.NXDomain)
	if len(answer.Proof) != 1 {
		t.Fatalf("proof = %#v, want one record", answer.Proof)
	}

	// next itself exists and is not denied by this interval.
	requireStatus(t, c, "b.z.", netsec.TypeA, netsec.NeedUpstream)
	// Names at or after next are not covered by this non-wrapping interval.
	requireStatus(t, c, "c.z.", netsec.TypeA, netsec.NeedUpstream)
}

func TestWrappingIntervalsAndTwoProofItems(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, "z.", clock)
	addRecord(t, c, clock, "z.", "b.z.", netsec.TypeBitmap{netsec.TypeNSEC}, 1)
	addRecord(t, c, clock, "b.z.", "z.", netsec.TypeBitmap{netsec.TypeNSEC}, 1)

	answer := requireStatus(t, c, "c.z.", netsec.TypeA, netsec.NXDomain)
	if len(answer.Proof) != 2 {
		t.Fatalf("proof = %#v, want query-name and wildcard proofs", answer.Proof)
	}
	if answer.ClosestEncloser != "z." || answer.Wildcard != "*.z." {
		t.Fatalf("CE/wildcard = %q/%q", answer.ClosestEncloser, answer.Wildcard)
	}

	// a.z and *.z both fall in (z., b.z.), so this one record is sufficient.
	one := requireStatus(t, c, "a.z.", netsec.TypeA, netsec.NXDomain)
	if len(one.Proof) != 1 {
		t.Fatalf("proof = %#v, want one record", one.Proof)
	}
}

func TestNonApexWrappingInterval(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, "z.", clock)
	addRecord(t, c, clock, "b.z.", "a.z.", netsec.TypeBitmap{netsec.TypeNSEC}, 1)

	// (b.z., a.z.) wraps through the end of the canonical order.  c.z. is
	// after the owner, while *.z. is before the next name; both are covered by
	// the same record.
	answer := requireStatus(t, c, "c.z.", netsec.TypeA, netsec.NXDomain)
	if len(answer.Proof) != 1 {
		t.Fatalf("proof = %#v, want one record", answer.Proof)
	}
	// The next name exists and is never strictly covered.
	requireStatus(t, c, "a.z.", netsec.TypeA, netsec.NeedUpstream)
}

func TestClosestEncloserUsesEmptyNonTerminalEvidence(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, "z.", clock)
	addRecord(t, c, clock, "z.", "a.b.z.", netsec.TypeBitmap{netsec.TypeNSEC}, 1)
	addRecord(t, c, clock, "a.b.z.", "z.", netsec.TypeBitmap{netsec.TypeNSEC}, 1)

	answer := requireStatus(t, c, "x.b.z.", netsec.TypeA, netsec.NXDomain)
	if answer.ClosestEncloser != "b.z." {
		t.Fatalf("ClosestEncloser = %q, want b.z.", answer.ClosestEncloser)
	}
	if answer.Wildcard != "*.b.z." {
		t.Fatalf("Wildcard = %q, want *.b.z.", answer.Wildcard)
	}
	if len(answer.Proof) != 2 {
		t.Fatalf("proof = %#v, want two records", answer.Proof)
	}

	// b.z is an existing empty non-terminal.  The covering NSEC whose next is
	// a.b.z. proves that b.z has no RRsets, so this is NODATA rather than
	// NXDOMAIN (or an unnecessary upstream query).
	ent := requireStatus(t, c, "b.z.", netsec.TypeA, netsec.NoData)
	if len(ent.Proof) != 1 || ent.Proof[0].Owner != "z." || ent.Proof[0].Next != "a.b.z." {
		t.Fatalf("ENT proof = %#v, want z. -> a.b.z.", ent.Proof)
	}
}

func TestWildcardExistenceBlocksNXDomain(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, "z.", clock)
	addRecord(t, c, clock, "z.", "*.z.", netsec.TypeBitmap{netsec.TypeNSEC}, 1)
	addRecord(t, c, clock, "*.z.", "a.z.", netsec.TypeBitmap{netsec.TypeNSEC}, 1)
	addRecord(t, c, clock, "a.z.", "*.a.z.", netsec.TypeBitmap{netsec.TypeNSEC}, 1)
	addRecord(t, c, clock, "*.a.z.", "z.", netsec.TypeBitmap{netsec.TypeNSEC}, 1)

	requireStatus(t, c, "x.z.", netsec.TypeA, netsec.NeedUpstream)
	requireStatus(t, c, "x.a.z.", netsec.TypeA, netsec.NeedUpstream)
}

func TestExpiryIsMinOfTTLAndSignature(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, "z.", clock)
	now := clock.Now()
	rec, receipt := recordAndReceipt("z.", "z.", netsec.TypeBitmap{netsec.TypeNSEC}, 10, 1, now.Add(time.Hour))
	if err := c.Add(rec, receipt); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, c, "z.", netsec.TypeTXT, netsec.NoData)

	clock.Advance(5 * time.Second)
	requireStatus(t, c, "z.", netsec.TypeTXT, netsec.NoData)
	clock.Advance(5 * time.Second)
	requireStatus(t, c, "z.", netsec.TypeTXT, netsec.NeedUpstream)
	if c.Len() != 0 {
		t.Fatalf("Len() = %d after TTL expiry", c.Len())
	}

	clock = newTestClock()
	c = newCache(t, "z.", clock)
	now = clock.Now()
	rec, receipt = recordAndReceipt("z.", "z.", netsec.TypeBitmap{netsec.TypeNSEC}, 300, 1, now.Add(7*time.Second))
	if err := c.Add(rec, receipt); err != nil {
		t.Fatal(err)
	}
	clock.Advance(6 * time.Second)
	requireStatus(t, c, "z.", netsec.TypeTXT, netsec.NoData)
	clock.Advance(time.Second)
	requireStatus(t, c, "z.", netsec.TypeTXT, netsec.NeedUpstream)
}

func TestExpiredAncestorEvidencePreventsGuessedClosestEncloser(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, "z.", clock)
	now := clock.Now()

	valid := func(owner, next string) {
		rec, receipt := recordAndReceipt(owner, next, netsec.TypeBitmap{netsec.TypeNSEC}, 300, 1, now.Add(time.Hour))
		if err := c.Add(rec, receipt); err != nil {
			t.Fatal(err)
		}
	}
	valid("z.", "b.z.")
	valid("c.z.", "z.")
	rec, receipt := recordAndReceipt("a.z.", "c.z.", netsec.TypeBitmap{netsec.TypeNSEC}, 5, 1, now.Add(time.Hour))
	if err := c.Add(rec, receipt); err != nil {
		t.Fatal(err)
	}
	clock.Advance(6 * time.Second)

	// There are currently valid records covering both x.a.z. and *.a.z., but
	// the expired a.z. record means the cache no longer has current existence
	// evidence for the actual closest encloser.  It must not guess z. instead.
	requireStatus(t, c, "x.a.z.", netsec.TypeA, netsec.NeedUpstream)
}

func TestRejectsUnverifiedMismatchedAndExpiredRecords(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, "z.", clock)
	rec, receipt := recordAndReceipt("z.", "z.", netsec.TypeBitmap{netsec.TypeNSEC}, 60, 1, clock.Now().Add(time.Hour))

	bad := rec
	bad.ReceiptID = ""
	if err := c.Add(bad, receipt); !errors.Is(err, netsec.ErrUnverified) {
		t.Fatalf("missing receipt ID error = %v", err)
	}

	bad = rec
	bad.ReceiptID = "different"
	if err := c.Add(bad, receipt); !errors.Is(err, netsec.ErrReceiptMismatch) {
		t.Fatalf("receipt ID mismatch error = %v", err)
	}

	badReceipt := receipt
	badReceipt.Zone = "other."
	if err := c.Add(rec, badReceipt); !errors.Is(err, netsec.ErrReceiptMismatch) {
		t.Fatalf("zone mismatch error = %v", err)
	}
	badReceipt = receipt
	badReceipt.Owner = "a.z."
	if err := c.Add(rec, badReceipt); !errors.Is(err, netsec.ErrReceiptMismatch) {
		t.Fatalf("owner mismatch error = %v", err)
	}
	badReceipt = receipt
	badReceipt.Next = "a.z."
	if err := c.Add(rec, badReceipt); !errors.Is(err, netsec.ErrReceiptMismatch) {
		t.Fatalf("next mismatch error = %v", err)
	}
	badReceipt = receipt
	badReceipt.Types = netsec.TypeBitmap{netsec.TypeNSEC, netsec.TypeA}
	if err := c.Add(rec, badReceipt); !errors.Is(err, netsec.ErrReceiptMismatch) {
		t.Fatalf("bitmap mismatch error = %v", err)
	}
	badReceipt = receipt
	badReceipt.SignatureExpires = clock.Now().Add(2 * time.Hour)
	if err := c.Add(rec, badReceipt); !errors.Is(err, netsec.ErrReceiptMismatch) {
		t.Fatalf("signature expiry mismatch error = %v", err)
	}
	badReceipt = receipt
	badReceipt.ZoneSerial = 2
	if err := c.Add(rec, badReceipt); !errors.Is(err, netsec.ErrReceiptMismatch) {
		t.Fatalf("serial mismatch error = %v", err)
	}

	ttlZero, ttlReceipt := recordAndReceipt("z.", "z.", netsec.TypeBitmap{netsec.TypeNSEC}, 0, 1, clock.Now().Add(time.Hour))
	if err := c.Add(ttlZero, ttlReceipt); !errors.Is(err, netsec.ErrExpired) {
		t.Fatalf("TTL zero error = %v", err)
	}
	expired, expiredReceipt := recordAndReceipt("z.", "z.", netsec.TypeBitmap{netsec.TypeNSEC}, 60, 1, clock.Now())
	if err := c.Add(expired, expiredReceipt); !errors.Is(err, netsec.ErrExpired) {
		t.Fatalf("expired signature error = %v", err)
	}
	if c.Len() != 0 {
		t.Fatalf("cache accepted an invalid record: Len() = %d", c.Len())
	}
}

func TestNewerSerialInvalidatesAndOlderSerialIsRejected(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, "z.", clock)
	addRecord(t, c, clock, "z.", "b.z.", netsec.TypeBitmap{netsec.TypeNSEC}, 1)
	addRecord(t, c, clock, "b.z.", "z.", netsec.TypeBitmap{netsec.TypeNSEC}, 1)
	requireStatus(t, c, "c.z.", netsec.TypeA, netsec.NXDomain)

	addRecord(t, c, clock, "z.", "c.z.", netsec.TypeBitmap{netsec.TypeNSEC}, 2)
	if serial, ok := c.Serial(); !ok || serial != 2 {
		t.Fatalf("Serial() = %d, %v; want 2, true", serial, ok)
	}
	if c.Len() != 1 {
		t.Fatalf("Len() = %d, want only the serial-2 record", c.Len())
	}
	requireStatus(t, c, "c.z.", netsec.TypeA, netsec.NeedUpstream)

	older, olderReceipt := recordAndReceipt("b.z.", "z.", netsec.TypeBitmap{netsec.TypeNSEC}, 300, 1, clock.Now().Add(time.Hour))
	if err := c.Add(older, olderReceipt); !errors.Is(err, netsec.ErrStaleSerial) {
		t.Fatalf("older serial error = %v", err)
	}
	if c.Len() != 1 {
		t.Fatalf("stale add changed cache: Len() = %d", c.Len())
	}
}

func TestSerialNumberWraparound(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, "z.", clock)
	addRecord(t, c, clock, "z.", "a.z.", netsec.TypeBitmap{netsec.TypeNSEC}, ^uint32(0))
	if serial, ok := c.Serial(); !ok || serial != ^uint32(0) {
		t.Fatalf("Serial() = %d, %v", serial, ok)
	}

	// RFC 1982 serial arithmetic treats zero as newer than 2^32-1.
	addRecord(t, c, clock, "z.", "b.z.", netsec.TypeBitmap{netsec.TypeNSEC}, 0)
	if serial, ok := c.Serial(); !ok || serial != 0 {
		t.Fatalf("Serial() after wrap = %d, %v; want 0, true", serial, ok)
	}
	if c.Len() != 1 {
		t.Fatalf("Len() after serial wrap = %d, want 1", c.Len())
	}
}

func TestOutOfZoneNamesAreRejected(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, "example.", clock)
	for _, qname := range []string{"other.", "example.other.", "notexample."} {
		if _, err := c.Query(qname, netsec.TypeA); !errors.Is(err, netsec.ErrOutOfZone) {
			t.Fatalf("Query(%s) error = %v, want ErrOutOfZone", qname, err)
		}
	}

	rec, receipt := recordAndReceipt("example.other.", "example.", netsec.TypeBitmap{netsec.TypeNSEC}, 60, 1, clock.Now().Add(time.Hour))
	receipt.Zone = "example."
	if err := c.Add(rec, receipt); !errors.Is(err, netsec.ErrOutOfZone) {
		t.Fatalf("out-of-zone Add error = %v", err)
	}
}

func TestBitmapNormalization(t *testing.T) {
	types, err := netsec.TypeBitmap{9, netsec.TypeA, 9, netsec.TypeNSEC}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	want := netsec.TypeBitmap{netsec.TypeA, 9, netsec.TypeNSEC}
	if !types.Equal(want) || types.String() != want.String() {
		t.Fatalf("Normalize() = %v, want %v", types, want)
	}
	if _, err := (netsec.TypeBitmap{0, 1}).Normalize(); err == nil {
		t.Fatal("bitmap accepted type zero")
	}
}

func TestBitmapReceiptUsesSetEqualityAndStoredTypesAreCopied(t *testing.T) {
	clock := newTestClock()
	c := newCache(t, "z.", clock)
	rec, receipt := recordAndReceipt("z.", "z.", netsec.TypeBitmap{netsec.TypeA, netsec.TypeA, netsec.TypeNSEC}, 300, 1, clock.Now().Add(time.Hour))
	receipt.Types = netsec.TypeBitmap{netsec.TypeNSEC, netsec.TypeA}
	if err := c.Add(rec, receipt); err != nil {
		t.Fatalf("set-equivalent receipt rejected: %v", err)
	}

	rec.Types[0] = netsec.TypeTXT
	answer := requireStatus(t, c, "z.", netsec.TypeTXT, netsec.NoData)
	if answer.Proof[0].Types.String() != "{1,47}" {
		t.Fatalf("stored proof types = %v, want normalized {1,47}", answer.Proof[0].Types)
	}
}
