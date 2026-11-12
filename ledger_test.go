package netsec

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// oracle is the independent interval-reference model: it keeps the same
// logical bookkeeping as Ledger using plain maps and slices so the two
// implementations can be diffed under randomized workloads.
type oracle struct {
	mask      uint64
	modulus   uint64
	epoch     uint64
	paths     map[PathID]bool
	live      map[uint64]*oracleEntry
	released  map[FrameRef]struct{}
	haveLast  bool
	lastPN    uint64
	largest   uint64
	haveLarge bool
	sentCount uint64
	ackCount  uint64
}

type oracleEntry struct {
	pn     uint64
	epoch  uint64
	path   PathID
	frames []FrameRef
	acked  bool
}

func newOracle(modulus uint64) *oracle {
	return &oracle{
		mask:     modulus - 1,
		modulus:  modulus,
		paths:    map[PathID]bool{},
		live:     map[uint64]*oracleEntry{},
		released: map[FrameRef]struct{}{},
	}
}

func (o *oracle) addPath(p PathID)    { o.paths[p] = true }
func (o *oracle) removePath(p PathID) { delete(o.paths, p) }

func (o *oracle) sent(pn uint64, path PathID, frames []FrameRef) error {
	if !o.paths[path] {
		return ErrUnknownPath
	}
	if pn > o.mask {
		return ErrInvalidAckRange
	}
	if o.haveLast {
		want := o.lastPN + 1
		if o.modulus != 0 {
			want &= o.mask
		}
		if pn != want {
			return ErrNonSequentialPacket
		}
		if pn <= o.lastPN && len(o.live) != 0 {
			return ErrWrapWhileInFlight
		}
		if pn <= o.lastPN {
			o.epoch++
		}
	}
	if _, ok := o.live[pn]; ok {
		return ErrDuplicatePacket
	}
	o.live[pn] = &oracleEntry{
		pn:     pn,
		epoch:  o.epoch,
		path:   path,
		frames: append([]FrameRef(nil), frames...),
	}
	o.lastPN = pn
	o.haveLast = true
	o.sentCount++
	return nil
}

func (o *oracle) ack(path PathID, ranges []AckRange) ([]FrameRef, error) {
	if !o.paths[path] {
		return nil, ErrUnauthenticatedPath
	}
	if len(ranges) == 0 {
		return nil, ErrEmptyAck
	}
	for _, r := range ranges {
		if r.First > r.Last || r.First > o.mask || r.Last > o.mask {
			return nil, ErrInvalidAckRange
		}
	}
	for _, r := range ranges {
		for pn, ok := r.First, true; ok; pn, ok = nextPN(pn, r.Last) {
			if _, exists := o.live[pn]; !exists {
				return nil, ErrUnknownPacket
			}
		}
	}

	var out []FrameRef
	for _, r := range ranges {
		for pn, ok := r.First, true; ok; pn, ok = nextPN(pn, r.Last) {
			rec := o.live[pn]
			if !rec.acked {
				rec.acked = true
				o.ackCount++
				if !o.haveLarge || pn > o.largest {
					o.largest = pn
					o.haveLarge = true
				}
			}
			for _, fr := range rec.frames {
				if _, seen := o.released[fr]; !seen {
					o.released[fr] = struct{}{}
					out = append(out, fr)
				}
			}
		}
	}
	return out, nil
}

func (o *oracle) retire() int {
	retired := 0
	for pn, rec := range o.live {
		if rec.acked {
			delete(o.live, pn)
			retired++
		}
	}
	return retired
}

func frameSet(refs []FrameRef) map[FrameRef]struct{} {
	out := map[FrameRef]struct{}{}
	for _, fr := range refs {
		out[fr] = struct{}{}
	}
	return out
}

func sortedFrames(refs []FrameRef) []FrameRef {
	out := append([]FrameRef(nil), refs...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sameError(got, want error) bool {
	return errors.Is(got, want) || (got == nil && want == nil)
}

func shuffle(rng *rand.Rand, xs []uint64) {
	rng.Shuffle(len(xs), func(i, j int) { xs[i], xs[j] = xs[j], xs[i] })
}

func randomFrames(rng *rand.Rand, nextID *uint64, max int) []FrameRef {
	n := rng.Intn(max + 1)
	frames := make([]FrameRef, n)
	for i := range frames {
		if *nextID > 1 && rng.Intn(3) == 0 {
			frames[i] = FrameRef{ID: rng.Uint64()%(*nextID-1) + 1} // reuse an earlier frame
		} else {
			frames[i] = FrameRef{ID: *nextID}
			*nextID++
		}
	}
	return frames
}

func randomAckRanges(rng *rand.Rand, live map[uint64]*oracleEntry, modulus uint64) []AckRange {
	var pns []uint64
	for pn, rec := range live {
		if !rec.acked {
			pns = append(pns, pn)
		}
	}
	sort.Slice(pns, func(i, j int) bool { return pns[i] < pns[j] })

	switch rng.Intn(3) {
	case 0: // single known point
		pn := pns[rng.Intn(len(pns))]
		return []AckRange{{First: pn, Last: pn}}
	case 1: // contiguous known span
		lo := rng.Intn(len(pns))
		hi := lo
		if span := len(pns) - lo; span > 1 {
			hi = lo + rng.Intn(span)
		}
		return []AckRange{{First: pns[lo], Last: pns[hi]}}
	default: // two ranges when enough points exist
		if len(pns) < 2 {
			pn := pns[0]
			return []AckRange{{First: pn, Last: pn}}
		}
		mid := rng.Intn(len(pns)-1) + 1
		return []AckRange{
			{First: pns[0], Last: pns[mid-1]},
			{First: pns[mid], Last: pns[len(pns)-1]},
		}
	}
}

func mustSend(t *testing.T, l *Ledger, pn uint64, path PathID, frames []FrameRef) {
	t.Helper()
	if err := l.Sent(pn, path, frames); err != nil {
		t.Fatalf("Sent(%d) unexpected error: %v", pn, err)
	}
}

// assertEquivalent cross-checks every externally observable ledger state
// against the reference oracle.
func assertEquivalent(t *testing.T, l *Ledger, o *oracle) {
	t.Helper()

	if l.SentCount() != o.sentCount {
		t.Fatalf("sent count ledger=%d oracle=%d", l.SentCount(), o.sentCount)
	}
	if l.AckedCount() != o.ackCount {
		t.Fatalf("ack count ledger=%d oracle=%d", l.AckedCount(), o.ackCount)
	}
	if l.ReleasedCount() != uint64(len(o.released)) {
		t.Fatalf("released count ledger=%d oracle=%d", l.ReleasedCount(), len(o.released))
	}
	if l.Epoch() != o.epoch {
		t.Fatalf("epoch ledger=%d oracle=%d", l.Epoch(), o.epoch)
	}

	var oracleInFlight []uint64
	for pn, rec := range o.live {
		if !rec.acked {
			oracleInFlight = append(oracleInFlight, pn)
		}
	}
	sort.Slice(oracleInFlight, func(i, j int) bool { return oracleInFlight[i] < oracleInFlight[j] })
	if got := l.InFlight(); !reflect.DeepEqual(got, oracleInFlight) {
		t.Fatalf("inflight ledger=%v oracle=%v", got, oracleInFlight)
	}

	for pn, rec := range o.live {
		got, ok := l.Record(pn)
		if !ok {
			t.Fatalf("ledger missing record for pn %d", pn)
		}
		if got.PacketNumber != rec.pn || got.Epoch != rec.epoch || got.Path != rec.path {
			t.Fatalf("record mismatch pn=%d ledger=%+v oracle=%+v", pn, got, rec)
		}
		if !reflect.DeepEqual(got.Frames, rec.frames) {
			t.Fatalf("frames mismatch pn=%d ledger=%v oracle=%v", pn, got.Frames, rec.frames)
		}
	}

	for pn := range l.live {
		if _, ok := o.live[pn]; !ok {
			t.Fatalf("ledger has stale record for pn %d", pn)
		}
	}

	ledgerLargest, ledgerHas := l.LargestAcked()
	if ledgerHas != o.haveLarge || (ledgerHas && ledgerLargest != o.largest) {
		t.Fatalf("largest acked ledger=(%d,%v) oracle=(%d,%v)",
			ledgerLargest, ledgerHas, o.largest, o.haveLarge)
	}
}

func TestDifferentialAgainstReferenceModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))

	moduli := []uint64{4, 8, 16, 64, 1 << 20}
	for _, modulus := range moduli {
		l := NewLedgerWithModulus(modulus)
		o := newOracle(modulus)

		paths := []PathID{1, 2, 3, 4}
		for _, p := range paths {
			l.AddPath(p)
			o.addPath(p)
		}

		pn := uint64(0)
		frameID := uint64(1)

		for iter := 0; iter < 3000; iter++ {
			switch rng.Intn(6) {
			case 0, 1: // send
				path := paths[rng.Intn(len(paths))]
				frames := randomFrames(rng, &frameID, 3)
				errL := l.Sent(pn, path, frames)
				errO := o.sent(pn, path, frames)
				if !sameError(errL, errO) {
					t.Fatalf("mod=%d iter=%d send pn=%d: ledger err %v, oracle err %v",
						modulus, iter, pn, errL, errO)
				}
				if errL == nil {
					pn = (pn + 1) & (modulus - 1)
				}

			case 2, 3: // acknowledge live packets from any (authenticated) path
				unacked := 0
				for _, rec := range o.live {
					if !rec.acked {
						unacked++
					}
				}
				if unacked == 0 {
					continue
				}
				path := paths[rng.Intn(len(paths))]
				ranges := randomAckRanges(rng, o.live, modulus)
				outL, errL := l.AckRanges(path, ranges)
				outO, errO := o.ack(path, ranges)
				if !sameError(errL, errO) {
					t.Fatalf("mod=%d iter=%d ack %v on path %d: ledger err %v, oracle err %v",
						modulus, iter, ranges, path, errL, errO)
				}
				if errL == nil && !reflect.DeepEqual(sortedFrames(outL), sortedFrames(outO)) {
					t.Fatalf("released mismatch ledger=%v oracle=%v", outL, outO)
				}

			case 4: // retire acknowledged records, allowing wraps
				if l.RetireAcked() != o.retire() {
					t.Fatalf("retire count mismatch at iter %d", iter)
				}

			case 5: // mark a random subset lost
				var candidates []uint64
				for p, rec := range o.live {
					if !rec.acked {
						candidates = append(candidates, p)
					}
				}
				if len(candidates) == 0 {
					continue
				}
				shuffle(rng, candidates)
				limit := 1 + rng.Intn(len(candidates))
				out, err := l.MarkLost(candidates[:limit]...)
				if err != nil {
					t.Fatalf("MarkLost unexpected error: %v", err)
				}
				want := map[FrameRef]struct{}{}
				for _, p := range candidates[:limit] {
					for _, fr := range o.live[p].frames {
						if _, released := o.released[fr]; !released {
							want[fr] = struct{}{}
						}
					}
				}
				if !reflect.DeepEqual(frameSet(out), want) {
					t.Fatalf("MarkLost frames=%v want=%v", out, want)
				}
			}
		}
		assertEquivalent(t, l, o)
	}
}

// TestPathMigration proves acknowledgments remain connection-level: a frame
// sent on path 1 is correctly released by an ACK arriving on migrated path 2.
func TestPathMigration(t *testing.T) {
	l := NewLedger()
	const oldPath, newPath PathID = 1, 2
	l.AddPath(oldPath)
	l.AddPath(newPath)

	mustSend(t, l, 0, oldPath, []FrameRef{{ID: 100}, {ID: 101}})
	mustSend(t, l, 1, oldPath, []FrameRef{{ID: 102}})

	l.RemovePath(oldPath)
	if _, err := l.AckRanges(oldPath, []AckRange{{First: 0, Last: 0}}); !errors.Is(err, ErrUnauthenticatedPath) {
		t.Fatalf("ack on removed path err = %v", err)
	}

	released, err := l.AckRanges(newPath, []AckRange{{First: 0, Last: 1}})
	if err != nil {
		t.Fatalf("migration ack: %v", err)
	}
	if got := frameSet(released); len(got) != 3 {
		t.Fatalf("released = %v, want 3 distinct frames", released)
	}

	rec, ok := l.Record(0)
	if !ok || rec.Path != oldPath {
		t.Fatalf("path attribution lost after migration: %+v ok=%v", rec, ok)
	}

	l.AddPath(3)
	again, err := l.AckRanges(3, []AckRange{{First: 0, Last: 1}})
	if err != nil || len(again) != 0 {
		t.Fatalf("duplicate ack should be idempotent, got frames=%v err=%v", again, err)
	}
	if l.ReleasedCount() != 3 {
		t.Fatalf("released count = %d, want 3", l.ReleasedCount())
	}
}

// TestPacketNumberWrap exercises epochs and slot reuse under a tiny modulus.
func TestPacketNumberWrap(t *testing.T) {
	l := NewLedgerWithModulus(4)
	l.AddPath(1)

	for pn := uint64(0); pn < 4; pn++ {
		mustSend(t, l, pn, 1, []FrameRef{{ID: pn + 1}})
	}
	if err := l.Sent(0, 1, []FrameRef{{ID: 99}}); !errors.Is(err, ErrWrapWhileInFlight) {
		t.Fatalf("wrap with in-flight packets err = %v", err)
	}

	if _, err := l.AckRanges(1, []AckRange{{First: 0, Last: 3}}); err != nil {
		t.Fatalf("ack full epoch: %v", err)
	}
	if err := l.Sent(0, 1, []FrameRef{{ID: 99}}); !errors.Is(err, ErrWrapWhileInFlight) {
		t.Fatalf("wrap before retirement err = %v", err)
	}
	if n := l.RetireAcked(); n != 4 {
		t.Fatalf("retired %d records, want 4", n)
	}

	if err := l.Sent(0, 1, []FrameRef{{ID: 99}}); err != nil {
		t.Fatalf("reuse after retire: %v", err)
	}
	if l.Epoch() != 1 {
		t.Fatalf("epoch = %d, want 1", l.Epoch())
	}
	rec, ok := l.Record(0)
	if !ok || rec.Epoch != 1 || rec.PacketNumber != 0 {
		t.Fatalf("wrapped record = %+v ok=%v", rec, ok)
	}

	// A stale ACK targeting a retired slot must be rejected, not applied to
	// the new epoch's packet 0.
	if _, err := l.AckRanges(1, []AckRange{{First: 1, Last: 1}}); !errors.Is(err, ErrUnknownPacket) {
		t.Fatalf("stale ack err = %v, want ErrUnknownPacket", err)
	}
}

// TestSameFrameMultipleSends verifies a frame carried by several packets
// (e.g. after loss-based retransmission) is released exactly once.
func TestSameFrameMultipleSends(t *testing.T) {
	l := NewLedger()
	l.AddPath(1)

	shared := FrameRef{ID: 7}
	mustSend(t, l, 0, 1, []FrameRef{shared, {ID: 8}})
	mustSend(t, l, 1, 1, []FrameRef{shared}) // retransmit shared frame

	lost, err := l.MarkLost(0)
	if err != nil {
		t.Fatalf("MarkLost: %v", err)
	}
	if got := frameSet(lost); !reflect.DeepEqual(got, frameSet([]FrameRef{shared, {ID: 8}})) {
		t.Fatalf("lost frames = %v", lost)
	}

	first, err := l.AckRanges(1, []AckRange{{First: 0, Last: 0}})
	if err != nil {
		t.Fatalf("ack pn0: %v", err)
	}
	if got := frameSet(first); len(got) != 2 {
		t.Fatalf("first ack released = %v, want 2 frames", first)
	}

	second, err := l.AckRanges(1, []AckRange{{First: 1, Last: 1}})
	if err != nil {
		t.Fatalf("ack pn1: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("shared frame released twice: %v", second)
	}
	if l.ReleasedCount() != 2 {
		t.Fatalf("released count = %d, want 2", l.ReleasedCount())
	}
}

// TestUnknownAndInvalidAcknowledgments verifies the ledger only releases
// ranges that are really sent and authenticated, with no partial side effects.
func TestUnknownAndInvalidAcknowledgments(t *testing.T) {
	l := NewLedger()
	l.AddPath(1)
	mustSend(t, l, 0, 1, []FrameRef{{ID: 1}})
	mustSend(t, l, 1, 1, []FrameRef{{ID: 2}})

	if _, err := l.AckRanges(9, []AckRange{{First: 0, Last: 0}}); !errors.Is(err, ErrUnauthenticatedPath) {
		t.Fatalf("unauthenticated err = %v", err)
	}
	if _, err := l.AckRanges(1, nil); !errors.Is(err, ErrEmptyAck) {
		t.Fatalf("empty ack err = %v", err)
	}
	if _, err := l.AckRanges(1, []AckRange{{First: 2, Last: 2}}); !errors.Is(err, ErrUnknownPacket) {
		t.Fatalf("unknown pn err = %v", err)
	}
	if _, err := l.AckRanges(1, []AckRange{{First: 0, Last: 2}}); !errors.Is(err, ErrUnknownPacket) {
		t.Fatalf("range spanning unknown pn err = %v", err)
	}
	if _, err := l.AckRanges(1, []AckRange{{First: 1, Last: 0}}); !errors.Is(err, ErrInvalidAckRange) {
		t.Fatalf("inverted range err = %v", err)
	}

	if l.AckedCount() != 0 || l.ReleasedCount() != 0 {
		t.Fatal("rejected acknowledgments must not mutate state")
	}

	released, err := l.AckRanges(1, []AckRange{{First: 0, Last: 0}, {First: 0, Last: 1}})
	if err != nil {
		t.Fatalf("overlapping ranges: %v", err)
	}
	if got := frameSet(released); len(got) != 2 {
		t.Fatalf("released = %v, want 2 frames", released)
	}
}

// TestSendSequenceValidation covers the connection-level numbering rules.
func TestSendSequenceValidation(t *testing.T) {
	l := NewLedger()
	l.AddPath(1)

	if err := l.Sent(0, 42, nil); !errors.Is(err, ErrUnknownPath) {
		t.Fatalf("unknown path err = %v", err)
	}
	mustSend(t, l, 5, 1, nil) // first packet may start at any number
	if err := l.Sent(7, 1, nil); !errors.Is(err, ErrNonSequentialPacket) {
		t.Fatalf("non-sequential err = %v", err)
	}
	if err := l.Sent(6, 1, nil); err != nil {
		t.Fatalf("sequential send: %v", err)
	}
	if err := l.Sent(6, 1, nil); !errors.Is(err, ErrNonSequentialPacket) {
		t.Fatalf("repeated number err = %v", err)
	}

	small := NewLedgerWithModulus(4)
	small.AddPath(1)
	if err := small.Sent(4, 1, nil); !errors.Is(err, ErrInvalidAckRange) {
		t.Fatalf("out-of-space pn err = %v", err)
	}
	if _, ok := small.Record(4); ok {
		t.Fatal("Record must report false for unknown pn")
	}

	small2 := NewLedgerWithModulus(2)
	small2.AddPath(1)
	mustSend(t, small2, 0, 1, []FrameRef{{ID: 1}})
	if _, err := small2.AckRanges(1, []AckRange{{First: 0, Last: 2}}); !errors.Is(err, ErrInvalidAckRange) {
		t.Fatalf("ack outside space err = %v", err)
	}
}

// TestInvalidModulus panics on construction with a non-power-of-two modulus.
func TestInvalidModulus(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for invalid modulus")
		}
	}()
	NewLedgerWithModulus(10)
}

// TestMarkLostRejectsAckedOrUnknown ensures loss hints cannot reference
// packets that no longer need retransmission.
func TestMarkLostRejectsAckedOrUnknown(t *testing.T) {
	l := NewLedger()
	l.AddPath(1)
	mustSend(t, l, 0, 1, []FrameRef{{ID: 1}})

	if _, err := l.MarkLost(7); !errors.Is(err, ErrUnknownPacket) {
		t.Fatalf("unknown lost pn err = %v", err)
	}
	if _, err := l.AckRanges(1, []AckRange{{First: 0, Last: 0}}); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if _, err := l.MarkLost(0); !errors.Is(err, ErrUnknownPacket) {
		t.Fatalf("acked lost pn err = %v", err)
	}
}

// TestLargestAckedAndModulus covers the simple accessor semantics.
func TestLargestAckedAndModulus(t *testing.T) {
	l := NewLedger()
	if l.Modulus() != 0 {
		t.Fatalf("full-space modulus = %d, want 0 (2^64 marker)", l.Modulus())
	}
	if _, ok := l.LargestAcked(); ok {
		t.Fatal("LargestAcked should be unset before any ack")
	}
	l.AddPath(1)
	mustSend(t, l, 10, 1, nil)
	mustSend(t, l, 11, 1, nil)
	if _, err := l.AckRanges(1, []AckRange{{First: 11, Last: 11}}); err != nil {
		t.Fatalf("ack: %v", err)
	}
	got, ok := l.LargestAcked()
	if !ok || got != 11 {
		t.Fatalf("LargestAcked = %d,%v", got, ok)
	}

	small := NewLedgerWithModulus(8)
	if small.Modulus() != 8 {
		t.Fatalf("small modulus = %d, want 8", small.Modulus())
	}
}

// TestConcurrentHost exercises the mutex under a realistic mixed workload.
func TestConcurrentHost(t *testing.T) {
	l := NewLedger()
	l.AddPath(1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for pn := uint64(0); pn < 500; pn++ {
			if err := l.Sent(pn, 1, []FrameRef{{ID: pn}}); err != nil {
				t.Errorf("send %d: %v", pn, err)
				return
			}
		}
	}()

	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = l.InFlight()
				_ = l.SentCount()
			}
		}()
	}
	<-done

	released, err := l.AckRanges(1, []AckRange{{First: 0, Last: 499}})
	if err != nil {
		t.Fatalf("bulk ack: %v", err)
	}
	if len(released) != 500 || l.ReleasedCount() != 500 {
		t.Fatalf("released=%d count=%d, want 500", len(released), l.ReleasedCount())
	}
	if n := l.RetireAcked(); n != 500 {
		t.Fatalf("retired = %d, want 500", n)
	}
	if len(l.InFlight()) != 0 {
		t.Fatal("all packets should be settled")
	}
}

// TestRangeQueries verifies the interval-backed views survive retirement and
// keep their normalized shape.
func TestRangeQueries(t *testing.T) {
	l := NewLedger()
	l.AddPath(1)
	for pn := uint64(0); pn < 6; pn++ {
		mustSend(t, l, pn, 1, []FrameRef{{ID: pn + 1}})
	}

	if !l.PathAuthenticated(1) || l.PathAuthenticated(2) {
		t.Fatal("PathAuthenticated wrong")
	}
	if !l.WasSent(3) || l.WasSent(9) {
		t.Fatal("WasSent wrong")
	}
	if l.WasAcked(3) {
		t.Fatal("WasAcked true before ack")
	}

	if _, err := l.AckRanges(1, []AckRange{{First: 0, Last: 1}, {First: 3, Last: 4}}); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if !l.WasAcked(4) || l.WasAcked(2) {
		t.Fatal("WasAcked wrong after ack")
	}
	wantAcked := []AckRange{{First: 0, Last: 1}, {First: 3, Last: 4}}
	if got := l.AckedRanges(); !reflect.DeepEqual(got, wantAcked) {
		t.Fatalf("acked ranges = %v, want %v", got, wantAcked)
	}

	if n := l.RetireAcked(); n != 4 {
		t.Fatalf("retired = %d, want 4", n)
	}
	if l.WasSent(0) {
		t.Fatal("retired packet still reported as sent")
	}
	if !l.WasAcked(0) {
		t.Fatal("retirement must not erase ack history")
	}
	wantSent := []AckRange{{First: 2, Last: 2}, {First: 5, Last: 5}}
	if got := l.SentRanges(); !reflect.DeepEqual(got, wantSent) {
		t.Fatalf("sent ranges after retire = %v, want %v", got, wantSent)
	}
}

func TestIntervalSetQueryHelpers(t *testing.T) {
	var s intervalSet
	if _, ok := s.max(); ok {
		t.Fatal("empty set should have no max")
	}
	if s.covers(1, 2) || s.contains(0) {
		t.Fatal("empty set should cover nothing")
	}

	s.add(5, 8)
	if !s.covers(5, 8) || s.covers(5, 9) || s.covers(4, 8) {
		t.Fatal("covers boundary wrong")
	}
	if max, ok := s.max(); !ok || max != 8 {
		t.Fatalf("max = %d,%v", max, ok)
	}
	if got := saturatingAdd(^uint64(0), 5); got != ^uint64(0) {
		t.Fatalf("saturating add = %d", got)
	}
	if got := saturatingAdd(10, 5); got != 15 {
		t.Fatalf("ordinary add = %d", got)
	}

	var empty intervalSet
	empty.remove(1, 3) // no-op path
	if !empty.empty() {
		t.Fatal("remove on empty set mutated it")
	}
}
