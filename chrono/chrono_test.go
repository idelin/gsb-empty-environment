package chrono

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func labWall(t *testing.T, choice OffsetChoice, version string) WallClock {
	t.Helper()
	w, err := NewWallClock(2024, 10, 27, 2, 30, 0, 0, 3600, choice, PrecNanosecond, "Lab/Zone", version)
	if err != nil {
		t.Fatalf("NewWallClock: %v", err)
	}
	return w
}

// Overlapping wall readings keep the selected offset: the same civil reading
// resolves to two different instants depending on the recorded choice.
func TestOverlapPreservesSelectedOffset(t *testing.T) {
	reg := LabRegistry()
	early, err := Resolve(labWall(t, ChoiceEarlier, "v1"), reg)
	if err != nil {
		t.Fatalf("resolve earlier: %v", err)
	}
	late, err := Resolve(labWall(t, ChoiceLater, "v1"), reg)
	if err != nil {
		t.Fatalf("resolve later: %v", err)
	}
	if late.Nanos()-early.Nanos() != 3600000000000 {
		t.Fatalf("occurrences should differ by 1h, got %d ns", late.Nanos()-early.Nanos())
	}
	if early.Nanos() != 1729989000000000000 { // 2024-10-27T00:30:00Z
		t.Fatalf("earlier = %d", early.Nanos())
	}
	if late.Nanos() != 1729992600000000000 { // 2024-10-27T01:30:00Z
		t.Fatalf("later = %d", late.Nanos())
	}
}

// Ambiguous readings without a recorded choice are refused; gap readings are
// refused. Neither is silently reinterpreted.
func TestAmbiguousAndGapRejected(t *testing.T) {
	reg := LabRegistry()
	if _, err := Resolve(labWall(t, ChoiceNone, "v1"), reg); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("want ErrAmbiguous, got %v", err)
	}
	gap, err := NewWallClock(2024, 3, 31, 2, 30, 0, 0, 3600, ChoiceNone, PrecSecond, "Lab/Zone", "v1")
	if err != nil {
		t.Fatalf("NewWallClock: %v", err)
	}
	if _, err := Resolve(gap, reg); !errors.Is(err, ErrGap) {
		t.Fatalf("want ErrGap, got %v", err)
	}
}

// An unknown table version is marked unresolvable, never guessed.
func TestUnknownTZVersionUnresolvable(t *testing.T) {
	reg := LabRegistry()
	if _, err := Resolve(labWall(t, ChoiceLater, "v99"), reg); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("want ErrUnknownVersion, got %v", err)
	}
	w, err := NewWallClock(2024, 1, 1, 0, 0, 0, 0, 0, ChoiceNone, PrecSecond, "No/Zone", "v1")
	if err != nil {
		t.Fatalf("NewWallClock: %v", err)
	}
	if _, err := Resolve(w, reg); !errors.Is(err, ErrUnknownZone) {
		t.Fatalf("want ErrUnknownZone, got %v", err)
	}
}

// Precision scaling: up is lossless; down is lossy with evidence; the
// reverse direction yields the full precision interval of candidates.
func TestPrecisionScalingEvidence(t *testing.T) {
	i, err := NewInstant(123456789, PrecNanosecond)
	if err != nil {
		t.Fatal(err)
	}
	up, m, err := ScaleInstant(i, PrecNanosecond)
	if err != nil || !m.Reversible || len(m.Losses) != 0 || up != i {
		t.Fatalf("identity scale: %v %v %v", up, m, err)
	}
	down, m, err := ScaleInstant(i, PrecMillisecond)
	if err != nil {
		t.Fatal(err)
	}
	if m.Reversible || len(m.Losses) != 1 || m.Losses[0].Kind != LossPrecision {
		t.Fatalf("expected lossy migration with precision evidence: %+v", m)
	}
	if down.Nanos() != 123000000 {
		t.Fatalf("scaled = %d", down.Nanos())
	}
	lo, hi := ReverseScaleInterval(down)
	if lo != 123000000 || hi != 123999999 {
		t.Fatalf("interval = [%d,%d]", lo, hi)
	}
	if i.Nanos() < lo || i.Nanos() > hi {
		t.Fatal("original not inside reverse interval")
	}
}

// Nanosecond precision is never truncated through milliseconds: scaling to
// microseconds keeps sub-millisecond digits, and exact grid points scale
// losslessly in both directions.
func TestNanosNotTruncatedThroughMillis(t *testing.T) {
	i, err := NewInstant(123456789, PrecNanosecond)
	if err != nil {
		t.Fatal(err)
	}
	micro, m, err := ScaleInstant(i, PrecMicrosecond)
	if err != nil {
		t.Fatal(err)
	}
	if micro.Nanos() != 123456000 {
		t.Fatalf("micro scaling must not route through millis: got %d", micro.Nanos())
	}
	if m.Reversible {
		t.Fatal("dropping 789 ns must be recorded as lossy")
	}
	exact, err := NewInstant(123456000, PrecMicrosecond)
	if err != nil {
		t.Fatal(err)
	}
	nano, m, err := ScaleInstant(exact, PrecNanosecond)
	if err != nil || !m.Reversible || nano.Nanos() != 123456000 {
		t.Fatalf("micro->nano must be lossless: %v %v", nano, m)
	}
	lo, hi := ReverseScaleInterval(nano)
	if lo != hi {
		t.Fatal("nanosecond interval must be a single point")
	}
}

// Multi-rollback: v1 -> v2 -> v3 -> v2 -> v1. The choice metadata survives
// the versions where the overlap is gone, so the full round trip restores
// the original value, while each collapsing step records loss evidence.
func TestMultiRollbackRoundTrip(t *testing.T) {
	reg := LabRegistry()
	orig := labWall(t, ChoiceLater, "v1")

	w2, m12, err := MigrateWallClock(orig, reg, "v2")
	if err != nil {
		t.Fatal(err)
	}
	if m12.Reversible || len(m12.Losses) != 1 || m12.Losses[0].Kind != LossAmbiguity {
		t.Fatalf("v1->v2 must record ambiguity collapse: %+v", m12)
	}
	if w2.OffsetSec() != 7200 || w2.Choice() != ChoiceLater {
		t.Fatalf("v2 state: %s", w2)
	}

	w3, m23, err := MigrateWallClock(w2, reg, "v3")
	if err != nil {
		t.Fatal(err)
	}
	if m23.Reversible || w3.OffsetSec() != 3600 {
		t.Fatalf("v2->v3: %+v %s", m23, w3)
	}

	back2, _, err := MigrateWallClock(w3, reg, "v2")
	if err != nil {
		t.Fatal(err)
	}
	back1, m21, err := MigrateWallClock(back2, reg, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if !m21.Reversible {
		t.Fatalf("v2->v1 re-applies the retained choice, must be reversible: %+v", m21)
	}
	if back1 != orig {
		t.Fatalf("round trip mismatch:\n got %s\nwant %s", back1, orig)
	}

	// The instant referent changed mid-flight even though the wall reading
	// round-trips: evidence that wall values are not silently instants.
	i1, err := Resolve(orig, reg)
	if err != nil {
		t.Fatal(err)
	}
	i2, err := Resolve(w2, reg)
	if err != nil {
		t.Fatal(err)
	}
	if i1 == i2 {
		t.Fatal("v1 and v2 referents must differ")
	}
}

// The lossy reverse direction returns all candidates under the old table.
func TestReverseMigrationReturnsAllCandidates(t *testing.T) {
	reg := LabRegistry()
	w2v, _, err := MigrateWallClock(labWall(t, ChoiceLater, "v1"), reg, "v2")
	if err != nil {
		t.Fatal(err)
	}
	cands, m, err := ReverseWallClock(w2v, reg, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("want 2 candidates, got %d (%+v)", len(cands), m)
	}
	if cands[0].Choice() != ChoiceEarlier || cands[1].Choice() != ChoiceLater {
		t.Fatalf("candidate choices: %s %s", cands[0].Choice(), cands[1].Choice())
	}
	if cands[1] != labWall(t, ChoiceLater, "v1") {
		t.Fatalf("later candidate should equal the original: %s", cands[1])
	}
}

// Integer overflow and out-of-range values are rejected before publication.
func TestOverflowRejected(t *testing.T) {
	if _, err := NewInstant(MaxNanos+1, PrecSecond); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("want ErrOutOfRange, got %v", err)
	}
	if _, err := NewInstant(MinNanos-1, PrecSecond); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("want ErrOutOfRange, got %v", err)
	}
	if _, err := InstantFromSeconds(math.MaxInt64, 0, PrecSecond); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	if _, err := InstantFromSeconds(0, 1000000000, PrecSecond); !errors.Is(err, ErrBadField) {
		t.Fatalf("want ErrBadField, got %v", err)
	}
	d, err := NewDuration(MaxNanos)
	if err != nil {
		t.Fatal(err)
	}
	one, err := NewDuration(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Add(one); err == nil {
		t.Fatal("duration add must fail beyond supported range")
	}
	big, err := NewDuration(1 << 40)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := big.Mul(1 << 40); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
}

// Illegal fields are rejected by the constructor, before any object exists.
func TestIllegalFieldsRejected(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*WallClock)
		want error
	}{
		{"month", func(w *WallClock) { w.month = 13 }, ErrBadField},
		{"day", func(w *WallClock) { w.day = 31; w.month = 4 }, ErrBadField},
		{"leap-day", func(w *WallClock) { w.year = 2023; w.month = 2; w.day = 29 }, ErrBadField},
		{"hour", func(w *WallClock) { w.hour = 24 }, ErrBadField},
		{"nanos", func(w *WallClock) { w.nanos = 1000000000 }, ErrBadField},
		{"offset", func(w *WallClock) { w.offsetSec = 86400 }, ErrBadField},
		{"choice", func(w *WallClock) { w.choice = OffsetChoice(9) }, ErrBadChoice},
		{"precision", func(w *WallClock) { w.prec = Precision(9) }, ErrBadPrecision},
		{"zone", func(w *WallClock) { w.zone = "" }, ErrBadField},
		{"version", func(w *WallClock) { w.tzVersion = "" }, ErrBadField},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := labWall(t, ChoiceLater, "v1")
			c.mut(&w)
			_, err := NewWallClock(w.year, w.month, w.day, w.hour, w.minute, w.second,
				w.nanos, w.offsetSec, w.choice, w.prec, w.zone, w.tzVersion)
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
}

func TestCodecRoundTrip(t *testing.T) {
	values := []Value{
		mustInstant(t, -123456789, PrecNanosecond),
		mustInstant(t, MaxNanos, PrecSecond),
		mustDuration(t, -42),
		labWall(t, ChoiceLater, "v1"),
	}
	for _, v := range values {
		b, err := Encode(v)
		if err != nil {
			t.Fatalf("encode %T: %v", v, err)
		}
		got, err := Decode(b)
		if err != nil {
			t.Fatalf("decode %T: %v", v, err)
		}
		if !reflect.DeepEqual(got, v) {
			t.Fatalf("round trip: got %v want %v", got, v)
		}
	}
}

func mustInstant(t *testing.T, n int64, p Precision) Instant {
	t.Helper()
	i, err := NewInstant(n, p)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func mustDuration(t *testing.T, n int64) Duration {
	t.Helper()
	d, err := NewDuration(n)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// The codec rejects unknown tags, truncation, oversized length prefixes,
// invalid field bytes and trailing garbage before publishing anything.
func TestCodecRejectsBadInput(t *testing.T) {
	if _, err := Decode([]byte{0xFF}); !errors.Is(err, ErrUnknownTag) {
		t.Fatalf("tag: want ErrUnknownTag, got %v", err)
	}
	if _, err := Decode(nil); !errors.Is(err, ErrTruncated) {
		t.Fatalf("empty: want ErrTruncated, got %v", err)
	}
	full, err := Encode(mustInstant(t, 7, PrecMillisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(full[:len(full)-1]); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated: want ErrTruncated, got %v", err)
	}
	if _, err := Decode(append(append([]byte{}, full...), 0)); !errors.Is(err, ErrTrailing) {
		t.Fatalf("trailing: want ErrTrailing, got %v", err)
	}

	// Oversized string length prefix: rejected before allocation.
	bad := []byte{tagWallClock, byte(PrecSecond)}
	bad = append(bad, 0, 0, 7, 232) // year 2024
	bad = append(bad, 1, 1, 0, 0, 0)
	bad = append(bad, 0, 0, 0, 0) // nanos
	bad = append(bad, 0, 0, 0, 0) // offset
	bad = append(bad, 0)          // choice
	bad = append(bad, 0xFF, 0xFF) // zone length 65535
	if _, err := Decode(bad); !errors.Is(err, ErrStringTooLong) {
		t.Fatalf("huge len: want ErrStringTooLong, got %v", err)
	}

	// Corrupt individual fields of a valid encoding.
	wall, err := Encode(labWall(t, ChoiceLater, "v1"))
	if err != nil {
		t.Fatal(err)
	}
	corrupt := func(off int, v byte) []byte {
		c := append([]byte{}, wall...)
		c[off] = v
		return c
	}
	if _, err := Decode(corrupt(1, 9)); !errors.Is(err, ErrBadPrecision) {
		t.Fatalf("prec byte: want ErrBadPrecision, got %v", err)
	}
	if _, err := Decode(corrupt(6, 13)); !errors.Is(err, ErrBadField) { // month offset
		t.Fatalf("month byte: want ErrBadField, got %v", err)
	}
	if _, err := Decode(corrupt(19, 7)); !errors.Is(err, ErrBadChoice) { // choice offset
		t.Fatalf("choice byte: want ErrBadChoice, got %v", err)
	}
}

// Instant values at the exact serialization range edges survive a round
// trip; one step beyond is rejected at construction.
func TestSerializationRangeEdges(t *testing.T) {
	for _, n := range []int64{MinNanos, MaxNanos} {
		i, err := NewInstant(n, PrecNanosecond)
		if err != nil {
			t.Fatalf("edge %d rejected: %v", n, err)
		}
		b, err := Encode(i)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(b)
		if err != nil {
			t.Fatal(err)
		}
		if got.(Instant).Nanos() != n {
			t.Fatalf("edge round trip: got %d want %d", got.(Instant).Nanos(), n)
		}
	}
}
