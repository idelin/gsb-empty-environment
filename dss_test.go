package netsec

import (
	"math"
	"reflect"
	"testing"
)

// dsnByte is the reference byte at a given DSN. Any honest subflow mapping a
// DSN range must supply exactly these bytes, so retransmissions from
// different subflows agree by construction.
func dsnByte(dsn uint64) byte { return byte(dsn*31 + 7) }

func refBytes(dsn uint64, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = dsnByte(dsn + uint64(i))
	}
	return b
}

func entry(id uint32, ssn uint32, dsn uint64, n int) Entry {
	return Entry{SubflowID: id, SSN: ssn, DSN: dsn, Length: uint16(n), Payload: refBytes(dsn, n)}
}

// model is the per-DSN reference model checked against the ledger.
type model struct {
	next uint64 // next DSN expected to be delivered
}

// drain pulls everything the ledger can deliver and checks it byte by byte
// against the reference stream, then checks the freeze positions.
func (m *model) drain(t *testing.T, l *Ledger, want int, frozen ...uint32) {
	t.Helper()
	got := l.Deliver()
	if len(got) != want {
		t.Fatalf("Deliver: got %d bytes, want %d", len(got), want)
	}
	for i, b := range got {
		dsn := m.next + uint64(i)
		if want := dsnByte(dsn); b != want {
			t.Fatalf("DSN %d: delivered byte %#02x, want %#02x", dsn, b, want)
		}
	}
	m.next += uint64(len(got))
	if l.Delivered() != m.next {
		t.Fatalf("Delivered: got %d, want %d", l.Delivered(), m.next)
	}
	if again := l.Deliver(); len(again) != 0 {
		t.Fatalf("byte delivered twice: %d extra bytes", len(again))
	}
	for _, id := range frozen {
		if !l.Frozen(id) {
			t.Fatalf("subflow %d: want frozen", id)
		}
	}
}

func wantResult(t *testing.T, got, want Result) {
	t.Helper()
	if got != want {
		t.Fatalf("Submit: got %v, want %v", got, want)
	}
}

func wantSources(t *testing.T, l *Ledger, dsn uint64, want ...uint32) {
	t.Helper()
	if got := l.Sources(dsn); !reflect.DeepEqual(got, want) {
		t.Fatalf("Sources(%d): got %v, want %v", dsn, got, want)
	}
}

// Two subflows interleave disjoint DSN ranges; a later retransmission only
// merges sources and never re-delivers a byte.
func TestTwoSubflowsInterleaved(t *testing.T) {
	l := NewLedger(nil)
	var m model

	wantResult(t, l.Submit(entry(1, 0, 0, 4)), Accepted)
	m.drain(t, l, 4)
	wantResult(t, l.Submit(entry(2, 100, 4, 4)), Accepted)
	m.drain(t, l, 4)
	wantResult(t, l.Submit(entry(1, 4, 8, 4)), Accepted)
	m.drain(t, l, 4)
	wantResult(t, l.Submit(entry(2, 104, 12, 4)), Accepted)
	m.drain(t, l, 4)

	// Retransmit [0,4) from subflow 2: identical bytes, sources merge.
	wantResult(t, l.Submit(entry(2, 108, 0, 4)), Accepted)
	m.drain(t, l, 0)
	wantSources(t, l, 0, 1, 2)
	wantSources(t, l, 3, 1, 2)
	wantSources(t, l, 4, 2)
	wantSources(t, l, 8, 1)
}

// A subflow whose 32-bit sequence space wraps past 2^32-1 is reconstructed
// relative to the authenticated checkpoint; spans beyond 2^31 are rejected.
func TestSubflowSeqWrap(t *testing.T) {
	l := NewLedger(nil)
	var m model

	wantResult(t, l.Submit(entry(1, math.MaxUint32-1, 0, 4)), Accepted) // checkpoint wraps to 2
	m.drain(t, l, 4)
	wantResult(t, l.Submit(entry(1, 2, 4, 4)), Accepted)
	m.drain(t, l, 4)

	// Exactly 2^31 ahead of the checkpoint: outside the window.
	wantResult(t, l.Submit(entry(1, 6+(1<<31), 8, 4)), RejectedSeqWindow)
	// Behind the checkpoint: also outside the window.
	wantResult(t, l.Submit(entry(1, 0, 8, 4)), RejectedSeqWindow)
	m.drain(t, l, 0)

	// Legal continuation right at the checkpoint still works.
	wantResult(t, l.Submit(entry(1, 6, 8, 4)), Accepted)
	m.drain(t, l, 4)
}

// Contained and partially overlapping retransmissions with identical bytes
// merge source sets; a single conflicting byte voids the whole entry and
// freezes the subflow.
func TestContainmentAndPartialOverlap(t *testing.T) {
	l := NewLedger(nil)
	var m model

	wantResult(t, l.Submit(entry(1, 0, 0, 16)), Accepted)

	// Contained retransmission [4,8) from subflow 2.
	wantResult(t, l.Submit(entry(2, 500, 4, 4)), Accepted)
	wantSources(t, l, 5, 1, 2)
	wantSources(t, l, 0, 1)

	// Partial overlap [8,24): [8,16) identical, [16,24) new.
	wantResult(t, l.Submit(entry(2, 504, 8, 16)), Accepted)
	wantSources(t, l, 10, 1, 2)
	wantSources(t, l, 20, 2)
	m.drain(t, l, 24)

	// Malicious subflow 3: [12,28) differs at DSN 13. Whole entry void.
	bad := entry(3, 0, 12, 16)
	bad.Payload[1] ^= 0xff
	wantResult(t, l.Submit(bad), RejectedConflict)
	m.drain(t, l, 0, 3)
	// The tail [24,28) of the voided entry must not occupy DSN space.
	wantSources(t, l, 24)
	wantSources(t, l, 12, 1, 2)

	// Frozen subflow stays frozen even for honest retransmissions.
	wantResult(t, l.Submit(entry(3, 16, 0, 4)), RejectedSubflowFrozen)
	m.drain(t, l, 0, 3)
}

// A failed authentication neither occupies DSN space nor advances the
// subflow checkpoint; a legal retransmission afterwards succeeds.
func TestAuthFailureThenLegalRetransmit(t *testing.T) {
	// Generation 0 is stale and rejected; anything >= 1 passes.
	l := NewLedger(func(e Entry) bool { return e.AuthGen >= 1 })
	var m model

	stale := entry(1, 0, 0, 4)
	stale.AuthGen = 0
	wantResult(t, l.Submit(stale), RejectedAuth)
	m.drain(t, l, 0)
	wantSources(t, l, 0)

	// DSN was not occupied: different bytes at the same range do not
	// conflict, and SSN 0 is still inside the checkpoint window.
	retry := entry(1, 0, 0, 4)
	retry.Payload = []byte{0xde, 0xad, 0xbe, 0xef}
	retry.AuthGen = 1
	wantResult(t, l.Submit(retry), Accepted)

	got := l.Deliver()
	if !reflect.DeepEqual(got, []byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatalf("Deliver: got %x", got)
	}
	m.next = 4
	m.drain(t, l, 0)
}

// FINs must agree on the end-of-data-stream DSN; a conflicting FIN voids the
// entry and freezes its subflow, and mappings past the end are rejected.
func TestConflictingFIN(t *testing.T) {
	l := NewLedger(nil)
	var m model

	fin := entry(1, 0, 0, 8)
	fin.FIN = true
	wantResult(t, l.Submit(fin), Accepted)
	m.drain(t, l, 8)
	if end, ok := l.End(); !ok || end != 8 {
		t.Fatalf("End: got %d,%v want 8,true", end, ok)
	}

	// Same end from another subflow: accepted.
	ackFin := Entry{SubflowID: 2, SSN: 7, DSN: 8, Length: 0, FIN: true, Payload: []byte{}}
	wantResult(t, l.Submit(ackFin), Accepted)

	// Conflicting end from subflow 3: void and frozen.
	badFin := Entry{SubflowID: 3, SSN: 0, DSN: 9, Length: 0, FIN: true, Payload: nil}
	wantResult(t, l.Submit(badFin), RejectedFINConflict)
	m.drain(t, l, 0, 3)
	if end, _ := l.End(); end != 8 {
		t.Fatalf("End moved to %d after conflicting FIN", end)
	}

	// Mapping beyond the FIN-bound end is rejected.
	wantResult(t, l.Submit(entry(4, 0, 8, 4)), RejectedAfterEnd)
	m.drain(t, l, 0)
	if l.Frozen(4) {
		t.Fatalf("subflow 4 must not be frozen by an after-end rejection")
	}
}

// Structural validation: payload length, zero-length rules, and address
// addition overflow.
func TestStructuralRejection(t *testing.T) {
	l := NewLedger(nil)
	var m model

	bad := entry(1, 0, 0, 4)
	bad.Payload = bad.Payload[:3]
	wantResult(t, l.Submit(bad), RejectedPayloadLen)

	zero := Entry{SubflowID: 1, SSN: 0, DSN: 0, Length: 0, FIN: false, Payload: nil}
	wantResult(t, l.Submit(zero), RejectedZeroLength)

	ovf := entry(1, 0, math.MaxUint64-1, 4)
	wantResult(t, l.Submit(ovf), RejectedOverflow)

	m.drain(t, l, 0)
	wantSources(t, l, 0)

	// Nothing was committed, so the subflow is still usable.
	wantResult(t, l.Submit(entry(1, 0, 0, 4)), Accepted)
	m.drain(t, l, 4)
}
