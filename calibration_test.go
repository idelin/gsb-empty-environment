package taskpkg

import (
	"errors"
	"math/big"
	"testing"
	"time"
)

func at(second int) time.Time {
	return time.Unix(int64(second), 0).UTC()
}

func rat(t *testing.T, numerator, denominator int64) *big.Rat {
	t.Helper()
	value, err := Rat(numerator, denominator)
	if err != nil {
		t.Fatalf("Rat(%d,%d): %v", numerator, denominator, err)
	}
	return value
}

func sample(device string, sequence uint64, takenAt time.Time, raw *big.Rat) Sample {
	return Sample{Device: device, Sequence: sequence, TakenAt: takenAt, Raw: raw}
}

func calibration(device string, version uint64, from, until time.Time, scale, offset *big.Rat) Calibration {
	return Calibration{
		Device:         device,
		Version:        version,
		EffectiveFrom:  from,
		EffectiveUntil: until,
		Scale:          scale,
		Offset:         offset,
	}
}

func mustPutSample(t *testing.T, engine *Engine, incoming Sample) Revision {
	t.Helper()
	revision, err := engine.PutSample(incoming)
	if err != nil {
		t.Fatalf("PutSample: %v", err)
	}
	return revision
}

func mustPutCalibration(t *testing.T, engine *Engine, cal Calibration) {
	t.Helper()
	if err := engine.PutCalibration(cal); err != nil {
		t.Fatalf("PutCalibration v%d: %v", cal.Version, err)
	}
}

func assertRat(t *testing.T, name string, got, want *big.Rat) {
	t.Helper()
	if !ratEqual(got, want) {
		t.Fatalf("%s: got %s want %s", name, got.RatString(), want.RatString())
	}
}

func id(device string, sequence uint64) SampleID {
	return SampleID{Device: device, Sequence: sequence}
}

// TestLateCalibrationRecomputesOnlyIntersecting verifies that a calibration
// arriving after the samples recomputes only intersecting samples and never
// touches the stored raw reading.
func TestLateCalibrationRecomputesOnlyIntersecting(t *testing.T) {
	engine := NewEngine()

	mustPutSample(t, engine, sample("dev", 1, at(1), RatInt(10)))
	mustPutSample(t, engine, sample("dev", 2, at(2), RatInt(20)))
	mustPutSample(t, engine, sample("dev", 3, at(3), RatInt(30)))

	// Raw readings materialize unchanged before any calibration exists.
	for sequence, want := range map[uint64]int64{1: 10, 2: 20, 3: 30} {
		got, ok := engine.MaterializedValue(id("dev", sequence))
		if !ok {
			t.Fatalf("missing materialized sample %d", sequence)
		}
		assertRat(t, "identity value", got.Value, RatInt(want))
	}

	// Late calibration: v = 2*raw + 1 on [2,4).
	mustPutCalibration(t, engine, calibration("dev", 1, at(2), at(4), RatInt(2), RatInt(1)))

	cases := []struct {
		sequence uint64
		want     *big.Rat
		reasons  int
	}{
		{1, RatInt(10), 1}, // outside the interval: untouched
		{2, RatInt(41), 2}, // 2*20+1
		{3, RatInt(61), 2}, // 2*30+1
	}
	for _, tc := range cases {
		got, ok := engine.MaterializedValue(id("dev", tc.sequence))
		if !ok {
			t.Fatalf("missing materialized sample %d", tc.sequence)
		}
		assertRat(t, "recomputed value", got.Value, tc.want)

		revisions := engine.RevisionsFor(id("dev", tc.sequence))
		if len(revisions) != tc.reasons {
			t.Fatalf("sample %d revisions: got %d want %d", tc.sequence, len(revisions), tc.reasons)
		}
		raw, ok := engine.RawSample(id("dev", tc.sequence))
		if !ok {
			t.Fatalf("missing raw sample %d", tc.sequence)
		}
		if raw.Raw.Cmp(RatInt(int64(tc.sequence)*10)) != 0 {
			t.Fatalf("raw reading for sample %d was overwritten: %s", tc.sequence, raw.Raw.RatString())
		}
	}

	if err := engine.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestIdempotentReinsertAndMissingLookups covers the no-op re-insertion path
// and defensive not-found accessors.
func TestIdempotentReinsertAndMissingLookups(t *testing.T) {
	engine := NewEngine()
	first := mustPutSample(t, engine, sample("dev", 1, at(5), RatInt(3)))

	if _, ok := engine.RawSample(id("dev", 99)); ok {
		t.Fatal("unknown raw sample lookup must report false")
	}
	if _, ok := engine.MaterializedValue(id("dev", 99)); ok {
		t.Fatal("unknown materialized lookup must report false")
	}
	if revisions := engine.RevisionsFor(id("dev", 99)); revisions != nil {
		t.Fatal("unknown sample must have no revisions")
	}

	// Identical re-insertion returns the same initial revision without
	// appending another event.
	again, err := engine.PutSample(sample("dev", 1, at(5), RatInt(3)))
	if err != nil {
		t.Fatalf("identical re-insertion: %v", err)
	}
	if again.Seq != first.Seq || len(engine.Revisions()) != 1 {
		t.Fatal("identical re-insertion must be a no-op")
	}
}

// TestConcurrentIngestion exercises the mutex under mixed sample and
// calibration traffic; the race detector is the primary assertion here.
func TestConcurrentIngestion(t *testing.T) {
	engine := NewEngine()
	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_, _ = engine.PutSample(sample("dev", uint64(i)+1, at(i), RatInt(int64(i))))
		}
	}()

	for version := uint64(1); version <= 20; version++ {
		cal := calibration("dev", version, at(0), time.Time{}, RatInt(1), RatInt(int64(version)))
		_ = engine.PutCalibration(cal)
	}

	<-done
	if err := engine.Verify(); err != nil {
		t.Fatalf("Verify under concurrent ingest: %v", err)
	}
}

// TestNilCoefficientRejected guards coefficient validation.
func TestNilCoefficientRejected(t *testing.T) {
	engine := NewEngine()
	bad := Calibration{
		Device:         "dev",
		Version:        1,
		EffectiveFrom:  at(0),
		EffectiveUntil: at(10),
		Scale:          RatInt(1),
		Offset:         RatInt(0),
	}
	bad.Offset = nil
	if err := engine.PutCalibration(bad); !errors.Is(err, ErrInvalidCalibration) {
		t.Fatalf("nil offset: got %v want ErrInvalidCalibration", err)
	}

	// Zero denominator is rejected at construction time as well.
	if _, err := Rat(1, 0); err == nil {
		t.Fatal("Rat must reject a zero denominator")
	}
}

// TestRationalAffinePrecision verifies exact rational affine math, including
// non-integer scales and offsets, instead of floating-point rounding.
func TestRationalAffinePrecision(t *testing.T) {
	engine := NewEngine()
	mustPutSample(t, engine, sample("dev", 1, at(5), rat(t, 1, 3)))

	// value * (2/7) + (1/2) = 2/21 + 1/2 = (4+21)/42 = 25/42.
	mustPutCalibration(t, engine, calibration("dev", 1, at(0), at(10),
		rat(t, 2, 7), rat(t, 1, 2)))

	got, _ := engine.MaterializedValue(id("dev", 1))
	assertRat(t, "exact rational result", got.Value, rat(t, 25, 42))

	if _, err := Rat(1, 0); err == nil {
		t.Fatal("zero denominator must be rejected")
	}
}

// TestOpenEndedIntervalAndDeviceIsolation covers zero EffectiveUntil
// intervals and guarantees calibrations never cross device instances.
func TestOpenEndedIntervalAndDeviceIsolation(t *testing.T) {
	engine := NewEngine()
	mustPutSample(t, engine, sample("a", 1, at(1000), RatInt(5)))
	mustPutSample(t, engine, sample("b", 1, at(1000), RatInt(5)))

	mustPutCalibration(t, engine, calibration("a", 1, at(10), time.Time{}, RatInt(2), RatInt(0)))

	gotA, _ := engine.MaterializedValue(id("a", 1))
	gotB, _ := engine.MaterializedValue(id("b", 1))
	assertRat(t, "open-ended calibration applies far in the future", gotA.Value, RatInt(10))
	assertRat(t, "calibration does not leak across devices", gotB.Value, RatInt(5))

	if err := engine.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestInvalidInputs documents structural validation errors.
func TestInvalidInputs(t *testing.T) {
	engine := NewEngine()

	if _, err := engine.PutSample(sample("", 1, at(1), RatInt(1))); !errors.Is(err, ErrInvalidCalibration) {
		t.Fatalf("empty device sample: got %v", err)
	}
	if _, err := engine.PutSample(sample("dev", 1, at(1), nil)); !errors.Is(err, ErrInvalidCalibration) {
		t.Fatalf("nil raw sample: got %v", err)
	}

	bad := []Calibration{
		{Device: "dev", Version: 1, EffectiveFrom: at(5), EffectiveUntil: at(5), Scale: RatInt(1), Offset: RatInt(0)},
		{Device: "dev", Version: 2, EffectiveFrom: at(6), EffectiveUntil: at(5), Scale: RatInt(1), Offset: RatInt(0)},
		{Device: "dev", Version: 3, EffectiveFrom: at(1), EffectiveUntil: at(2), Scale: nil, Offset: RatInt(0)},
		{Device: "", Version: 4, EffectiveFrom: at(1), EffectiveUntil: at(2), Scale: RatInt(1), Offset: RatInt(0)},
	}
	for index, cal := range bad {
		if err := engine.PutCalibration(cal); !errors.Is(err, ErrInvalidCalibration) {
			t.Fatalf("invalid calibration %d: got %v", index, err)
		}
	}
}

// TestFullRecomputeCrossCheck drives an interleaved ingest/withdraw history
// and asserts Recompute independently reproduces every live value, rollback
// flag and per-sample revision count, including boundary equality and
// withdrawn calibrations.
func TestFullRecomputeCrossCheck(t *testing.T) {
	engine := NewEngine()

	// Interleaved samples across two devices, including a clock rollback.
	mustPutSample(t, engine, sample("a", 1, at(0), RatInt(2)))
	mustPutSample(t, engine, sample("a", 2, at(10), RatInt(4)))
	mustPutSample(t, engine, sample("b", 1, at(3), RatInt(8)))
	mustPutCalibration(t, engine, calibration("a", 1, at(5), at(15), RatInt(2), RatInt(1)))
	mustPutSample(t, engine, sample("a", 3, at(7), RatInt(6))) // inside v1
	mustPutSample(t, engine, sample("a", 4, at(6), RatInt(8))) // rollback (time < seq3 at 7)
	mustPutCalibration(t, engine, calibration("a", 2, at(5), at(10), RatInt(10), RatInt(0)))
	mustPutCalibration(t, engine, calibration("b", 1, at(0), time.Time{}, RatInt(0), RatInt(7)))

	// Boundary sample exactly at v2's until: v2 ends at 10, so it falls back
	// to v1 which still covers [5,15).
	mustPutSample(t, engine, sample("a", 5, at(10), RatInt(3)))
	got, _ := engine.MaterializedValue(id("a", 5))
	if !ratEqual(got.Value, RatInt(7)) {
		t.Fatal("boundary sample should use v1 (2*3+1), not v2")
	}

	if err := engine.WithdrawCalibration("a", 2); err != nil {
		t.Fatalf("withdraw v2: %v", err)
	}

	result := engine.Recompute()

	wantValues := map[SampleID]*big.Rat{
		id("a", 1): RatInt(2),  // before any interval
		id("a", 2): RatInt(9),  // v1: 2*4+1
		id("a", 3): RatInt(13), // v1 after v2 withdrawn: 2*6+1
		id("a", 4): RatInt(17), // v1: 2*8+1
		id("a", 5): RatInt(7),  // v1 at excluded v2 boundary
		id("b", 1): RatInt(7),  // b v1: 0*8+7
	}
	wantRollback := map[SampleID]bool{
		id("a", 1): false,
		id("a", 2): false,
		id("a", 3): true, // t7 after seq2 at t10
		id("a", 4): true,
		id("a", 5): false,
		id("b", 1): false,
	}

	if len(result.Materialized) != len(wantValues) {
		t.Fatalf("recompute entries: got %d want %d", len(result.Materialized), len(wantValues))
	}
	for _, materialized := range result.Materialized {
		sampleID := materialized.ID()
		want, ok := wantValues[sampleID]
		if !ok {
			t.Fatalf("unexpected recomputed entry %v", sampleID)
		}
		assertRat(t, "recomputed value", materialized.Value, want)
		if materialized.RolledBack != wantRollback[sampleID] {
			t.Fatalf("rollback for %v: got %v want %v", sampleID, materialized.RolledBack, wantRollback[sampleID])
		}
		live, ok := engine.MaterializedValue(sampleID)
		if !ok || !ratEqual(live.Value, materialized.Value) || live.RolledBack != materialized.RolledBack {
			t.Fatalf("live projection diverges from recompute for %v", sampleID)
		}
	}

	for sampleID, hits := range result.RevisionHits {
		if hits != len(engine.RevisionsFor(sampleID)) {
			t.Fatalf("revision count mismatch for %v", sampleID)
		}
		if hits < 1 {
			t.Fatalf("sample %v has no revisions", sampleID)
		}
	}

	if err := engine.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestOverlappingCalibrationsVersionPriority verifies that overlapping
// calibrations resolve to the greatest explicit version, that lower versions
// do not disturb higher ones, and that raising then withdrawing versions
// recomputes correctly.
func TestOverlappingCalibrationsVersionPriority(t *testing.T) {
	engine := NewEngine()
	mustPutSample(t, engine, sample("dev", 1, at(5), RatInt(2)))

	mustPutCalibration(t, engine, calibration("dev", 1, at(0), at(10), RatInt(2), RatInt(0)))
	got, _ := engine.MaterializedValue(id("dev", 1))
	assertRat(t, "v1 mapping", got.Value, RatInt(4))

	// A lower overlapping version must not win over v1.
	lower := calibration("dev", 0, at(0), at(10), RatInt(9), RatInt(9))
	mustPutCalibration(t, engine, lower)
	got, _ = engine.MaterializedValue(id("dev", 1))
	assertRat(t, "lower version ignored", got.Value, RatInt(4))

	// A higher overlapping version wins.
	mustPutCalibration(t, engine, calibration("dev", 2, at(0), at(10), RatInt(3), RatInt(1)))
	got, _ = engine.MaterializedValue(id("dev", 1))
	assertRat(t, "v2 mapping wins", got.Value, RatInt(7))

	revisions := engine.RevisionsFor(id("dev", 1))
	if len(revisions) != 3 {
		t.Fatalf("got %d revisions, want 3 (initial, v1, v2)", len(revisions))
	}
	if revisions[2].Evidence.CalVersion != 2 {
		t.Fatalf("latest revision evidence version: got %d want 2", revisions[2].Evidence.CalVersion)
	}

	// Withdraw v2: the sample falls back to v1.
	if err := engine.WithdrawCalibration("dev", 2); err != nil {
		t.Fatalf("WithdrawCalibration: %v", err)
	}
	got, _ = engine.MaterializedValue(id("dev", 1))
	assertRat(t, "fallback after withdrawal", got.Value, RatInt(4))

	// Withdraw v1: the still-active lower version v0 (9x+9) now wins.
	if err := engine.WithdrawCalibration("dev", 1); err != nil {
		t.Fatalf("WithdrawCalibration: %v", err)
	}
	got, _ = engine.MaterializedValue(id("dev", 1))
	assertRat(t, "fallback to lower version", got.Value, RatInt(27))

	// Withdraw the last calibration: the identity mapping returns the raw
	// reading.
	if err := engine.WithdrawCalibration("dev", 0); err != nil {
		t.Fatalf("WithdrawCalibration v0: %v", err)
	}
	got, _ = engine.MaterializedValue(id("dev", 1))
	assertRat(t, "identity after all withdrawn", got.Value, RatInt(2))

	// Re-using a withdrawn version is rejected.
	err := engine.PutCalibration(calibration("dev", 2, at(0), at(10), RatInt(1), RatInt(0)))
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("reuse withdrawn version: got %v want ErrVersionConflict", err)
	}

	if err := engine.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestSameVersionDifferentParametersRejected verifies the explicit rule that
// identical versions with different parameters are rejected, while an
// identical re-submission is an idempotent no-op.
func TestSameVersionDifferentParametersRejected(t *testing.T) {
	engine := NewEngine()
	mustPutSample(t, engine, sample("dev", 1, at(5), RatInt(2)))

	original := calibration("dev", 7, at(0), at(10), RatInt(2), RatInt(1))
	mustPutCalibration(t, engine, original)

	// Identical re-submission is a no-op and adds no revision.
	mustPutCalibration(t, engine, original)
	if len(engine.Revisions()) != 2 {
		t.Fatalf("identical re-submission should be a no-op, got %d revisions", len(engine.Revisions()))
	}

	conflicts := []Calibration{
		calibration("dev", 7, at(0), at(10), RatInt(3), RatInt(1)), // different scale
		calibration("dev", 7, at(0), at(10), RatInt(2), RatInt(9)), // different offset
		calibration("dev", 7, at(1), at(10), RatInt(2), RatInt(1)), // different interval
	}
	for index, conflict := range conflicts {
		err := engine.PutCalibration(conflict)
		if !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("conflict case %d: got %v want ErrVersionConflict", index, err)
		}
	}

	// Unknown and double withdrawals are reported distinctly.
	if err := engine.WithdrawCalibration("dev", 99); !errors.Is(err, ErrCalibrationNotFound) {
		t.Fatalf("unknown withdrawal: got %v want ErrCalibrationNotFound", err)
	}
	if err := engine.WithdrawCalibration("dev", 7); err != nil {
		t.Fatalf("first withdrawal: %v", err)
	}
	if err := engine.WithdrawCalibration("dev", 7); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("double withdrawal: got %v want ErrVersionConflict", err)
	}
}

// TestClockRollbackMarking verifies that rollback flags are computed in
// sequence order, recomputed when a late sample inserts into the middle, and
// stay attached to revision evidence.
func TestClockRollbackMarking(t *testing.T) {
	engine := NewEngine()
	mustPutSample(t, engine, sample("dev", 1, at(10), RatInt(1)))
	mustPutSample(t, engine, sample("dev", 2, at(20), RatInt(2)))

	if got, _ := engine.MaterializedValue(id("dev", 2)); got.RolledBack {
		t.Fatal("forward sequence must not be flagged")
	}

	// A late sequence-3 sample whose clock went backwards flags rollback.
	mustPutSample(t, engine, sample("dev", 3, at(15), RatInt(3)))
	if got, _ := engine.MaterializedValue(id("dev", 3)); !got.RolledBack {
		t.Fatal("backwards sample must be flagged as rolled back")
	}

	// A late middle sample (sequence 4 sorts last by sequence but time 12 is
	// earlier than its predecessor sequence 3 at 15): flag follows sequence.
	mustPutSample(t, engine, sample("dev", 4, at(12), RatInt(4)))
	if got, _ := engine.MaterializedValue(id("dev", 4)); !got.RolledBack {
		t.Fatal("late sample with earlier clock than predecessor must be flagged")
	}
	if got, _ := engine.MaterializedValue(id("dev", 3)); !got.RolledBack {
		t.Fatal("seq3 stays flagged: seq2 at 20 -> seq3 at 15 is itself a rollback")
	}

	// Equal timestamps are not a rollback: build a fresh engine for clarity.
	equal := NewEngine()
	mustPutSample(t, equal, sample("d", 1, at(7), RatInt(1)))
	mustPutSample(t, equal, sample("d", 2, at(7), RatInt(2)))
	if got, _ := equal.MaterializedValue(id("d", 2)); got.RolledBack {
		t.Fatal("equal timestamps must not be flagged")
	}

	if err := engine.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestRawSampleImmutableAndEvidencePreserved ensures recomputation never
// overwrites the raw reading and every revision keeps original evidence.
func TestRawSampleImmutableAndEvidencePreserved(t *testing.T) {
	engine := NewEngine()
	mustPutSample(t, engine, sample("dev", 1, at(5), RatInt(6)))
	mustPutCalibration(t, engine, calibration("dev", 1, at(0), at(10), RatInt(2), RatInt(3)))
	mustPutCalibration(t, engine, calibration("dev", 2, at(0), at(10), RatInt(4), RatInt(5)))

	raw, ok := engine.RawSample(id("dev", 1))
	if !ok || raw.Raw.Cmp(RatInt(6)) != 0 {
		t.Fatalf("raw reading changed: %v", raw.Raw)
	}

	revisions := engine.RevisionsFor(id("dev", 1))
	if len(revisions) != 3 {
		t.Fatalf("got %d revisions, want 3", len(revisions))
	}
	for index, revision := range revisions {
		if revision.Evidence.Sample.Raw.Cmp(RatInt(6)) != 0 {
			t.Fatalf("revision %d lost original evidence", index)
		}
	}
	if revisions[0].Evidence.HasCalibration || revisions[0].Evidence.Calibration != nil {
		t.Fatal("initial identity revision must carry no calibration evidence")
	}
	if revisions[1].Evidence.CalVersion != 1 || revisions[2].Evidence.CalVersion != 2 {
		t.Fatal("calibration evidence versions not recorded in order")
	}

	// Re-inserting the same fixed identity with a different reading is refused.
	_, err := engine.PutSample(sample("dev", 1, at(5), RatInt(99)))
	if !errors.Is(err, ErrSampleConflict) {
		t.Fatalf("got %v want ErrSampleConflict", err)
	}
	raw, _ = engine.RawSample(id("dev", 1))
	if raw.Raw.Cmp(RatInt(6)) != 0 {
		t.Fatal("failed insertion must not mutate the raw sample")
	}

	// Mutating a returned copy must not affect engine state.
	raw.Raw.SetInt64(123)
	again, _ := engine.RawSample(id("dev", 1))
	if again.Raw.Cmp(RatInt(6)) != 0 {
		t.Fatal("engine state leaked through returned evidence copy")
	}
}

// TestLateSampleAppliesExistingCalibration verifies that a sample arriving
// after a calibration is mapped immediately without a separate event burst.
func TestLateSampleAppliesExistingCalibration(t *testing.T) {
	engine := NewEngine()
	mustPutCalibration(t, engine, calibration("dev", 1, at(0), at(100), RatInt(2), RatInt(1)))

	revision := mustPutSample(t, engine, sample("dev", 1, at(50), RatInt(10)))
	if revision.Reason != ReasonInitial || revision.Evidence.CalVersion != 1 {
		t.Fatalf("late sample initial revision: reason=%q calVersion=%d", revision.Reason, revision.Evidence.CalVersion)
	}
	got, _ := engine.MaterializedValue(id("dev", 1))
	assertRat(t, "late sample mapped on arrival", got.Value, RatInt(21))

	if err := engine.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestHalfOpenBoundaryEquality pins down that EffectiveUntil is exclusive and
// EffectiveFrom is inclusive.
func TestHalfOpenBoundaryEquality(t *testing.T) {
	engine := NewEngine()
	mustPutSample(t, engine, sample("dev", 1, at(5), RatInt(4)))  // at from: included
	mustPutSample(t, engine, sample("dev", 2, at(10), RatInt(4))) // at until: excluded
	mustPutSample(t, engine, sample("dev", 3, at(9), RatInt(4)))  // inside: included

	mustPutCalibration(t, engine, calibration("dev", 1, at(5), at(10), RatInt(3), RatInt(2)))

	got, _ := engine.MaterializedValue(id("dev", 1))
	assertRat(t, "from boundary included", got.Value, RatInt(14)) // 3*4+2
	got, _ = engine.MaterializedValue(id("dev", 3))
	assertRat(t, "interior included", got.Value, RatInt(14))
	got, _ = engine.MaterializedValue(id("dev", 2))
	assertRat(t, "until boundary excluded", got.Value, RatInt(4))

	if err := engine.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
