package main

import (
	"testing"
	"time"
)

func ev(ts string, typ EventType) Event {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		panic(err)
	}
	return Event{Time: t, Type: typ}
}

func rules(freeMin, unitMin, price, cap int64) Rules {
	return Rules{
		Free:      time.Duration(freeMin) * time.Minute,
		Unit:      time.Duration(unitMin) * time.Minute,
		UnitPrice: price,
		DailyCap:  cap,
	}
}

func feeOf(t *testing.T, e *Extreme) int64 {
	t.Helper()
	if e == nil || e.FeeCents == nil {
		t.Fatalf("expected finite fee, got %+v", e)
	}
	return *e.FeeCents
}

func checkWindow(t *testing.T, w Window, from, to string) {
	t.Helper()
	if from == "" {
		if w.From != nil {
			t.Fatalf("window.From: want nil, got %v", w.From)
		}
	} else if w.From == nil || !w.From.Equal(ev(from, Enter).Time) {
		t.Fatalf("window.From: want %s, got %v", from, w.From)
	}
	if to == "" {
		if w.To != nil {
			t.Fatalf("window.To: want nil, got %v", w.To)
		}
	} else if w.To == nil || !w.To.Equal(ev(to, Enter).Time) {
		t.Fatalf("window.To: want %s, got %v", to, w.To)
	}
}

// Missing exit between two enters; fee depends on where in [10:00,12:00]
// the exit is inserted.
func TestMiddleDefect(t *testing.T) {
	res := Solve([]Event{
		ev("2026-01-05T08:00:00+08:00", Enter),
		ev("2026-01-05T09:00:00+08:00", Exit),
		ev("2026-01-05T10:00:00+08:00", Enter),
		ev("2026-01-05T12:00:00+08:00", Enter),
		ev("2026-01-05T13:00:00+08:00", Exit),
	}, rules(15, 30, 100, 1000))

	if res.Status != StatusAmbiguous {
		t.Fatalf("status: want ambiguous, got %s (%s)", res.Status, res.Reason)
	}
	if res.Inserted == nil || res.Inserted.Type != Exit || res.Inserted.Position != 3 {
		t.Fatalf("inserted: %+v", res.Inserted)
	}
	if got := feeOf(t, res.Min); got != 400 {
		t.Fatalf("min fee: want 400, got %d", got)
	}
	if got := feeOf(t, res.Max); got != 800 {
		t.Fatalf("max fee: want 800, got %d", got)
	}
	if len(res.Min.Windows) != 1 {
		t.Fatalf("min windows: %+v", res.Min.Windows)
	}
	checkWindow(t, res.Min.Windows[0], "2026-01-05T10:00:00+08:00", "2026-01-05T10:15:00+08:00")
	if len(res.Max.Windows) != 1 {
		t.Fatalf("max windows: %+v", res.Max.Windows)
	}
	checkWindow(t, res.Max.Windows[0], "2026-01-05T11:45:00+08:00", "2026-01-05T12:00:00+08:00")
}

// Same record, tighter daily cap: the cap flattens the top of the range.
func TestDailyCapFlattens(t *testing.T) {
	res := Solve([]Event{
		ev("2026-01-05T08:00:00+08:00", Enter),
		ev("2026-01-05T09:00:00+08:00", Exit),
		ev("2026-01-05T10:00:00+08:00", Enter),
		ev("2026-01-05T12:00:00+08:00", Enter),
		ev("2026-01-05T13:00:00+08:00", Exit),
	}, rules(15, 30, 100, 500))

	if res.Status != StatusAmbiguous {
		t.Fatalf("status: want ambiguous, got %s", res.Status)
	}
	if got := feeOf(t, res.Min); got != 400 {
		t.Fatalf("min fee: want 400, got %d", got)
	}
	if got := feeOf(t, res.Max); got != 500 {
		t.Fatalf("max fee: want 500, got %d", got)
	}
	checkWindow(t, res.Max.Windows[0], "2026-01-05T10:15:00+08:00", "2026-01-05T12:00:00+08:00")
}

// Missing final exit: open-ended insert domain, bounded by the daily cap.
func TestMissingFinalExit(t *testing.T) {
	res := Solve([]Event{
		ev("2026-01-05T08:00:00+08:00", Enter),
		ev("2026-01-05T09:00:00+08:00", Exit),
		ev("2026-01-05T10:00:00+08:00", Enter),
	}, rules(15, 30, 100, 1000))

	if res.Status != StatusAmbiguous {
		t.Fatalf("status: want ambiguous, got %s", res.Status)
	}
	if res.Inserted.Type != Exit || res.Inserted.Position != 3 {
		t.Fatalf("inserted: %+v", res.Inserted)
	}
	if got := feeOf(t, res.Min); got != 200 {
		t.Fatalf("min fee: want 200, got %d", got)
	}
	checkWindow(t, res.Min.Windows[0], "2026-01-05T10:00:00+08:00", "2026-01-05T10:15:00+08:00")
	if got := feeOf(t, res.Max); got != 1000 {
		t.Fatalf("max fee: want 1000, got %d", got)
	}
	checkWindow(t, res.Max.Windows[0], "2026-01-05T13:45:00+08:00", "")
}

// Missing initial enter: the segment may be billed to the previous calendar
// day, where the daily cap is untouched, so the max exceeds one day's cap.
func TestMissingInitialEnterAcrossMidnight(t *testing.T) {
	res := Solve([]Event{
		ev("2026-01-05T20:00:00+08:00", Exit),
		ev("2026-01-05T21:00:00+08:00", Enter),
		ev("2026-01-05T22:00:00+08:00", Exit),
	}, rules(15, 30, 100, 1000))

	if res.Status != StatusAmbiguous {
		t.Fatalf("status: want ambiguous, got %s", res.Status)
	}
	if res.Inserted.Type != Enter || res.Inserted.Position != 0 {
		t.Fatalf("inserted: %+v", res.Inserted)
	}
	if got := feeOf(t, res.Min); got != 200 {
		t.Fatalf("min fee: want 200, got %d", got)
	}
	checkWindow(t, res.Min.Windows[0], "2026-01-05T19:45:00+08:00", "2026-01-05T20:00:00+08:00")
	if got := feeOf(t, res.Max); got != 1200 {
		t.Fatalf("max fee: want 1200, got %d", got)
	}
	checkWindow(t, res.Max.Windows[0], "", "2026-01-05T00:00:00+08:00")
}

// Missing final exit without a daily cap: the fee is unbounded.
func TestUnboundedWithoutCap(t *testing.T) {
	res := Solve([]Event{
		ev("2026-01-05T10:00:00+08:00", Enter),
	}, rules(15, 30, 100, 0))

	if res.Status != StatusUnbounded {
		t.Fatalf("status: want unbounded, got %s", res.Status)
	}
	if got := feeOf(t, res.Min); got != 0 {
		t.Fatalf("min fee: want 0, got %d", got)
	}
	checkWindow(t, res.Min.Windows[0], "2026-01-05T10:00:00+08:00", "2026-01-05T10:15:00+08:00")
	if res.Max.FeeCents != nil {
		t.Fatalf("max fee: want nil (unbounded), got %d", *res.Max.FeeCents)
	}
	checkWindow(t, res.Max.Windows[0], "2026-01-05T10:00:00+08:00", "")
}

// A complete, legal record needs no insertion and has a unique fee.
func TestAlreadyLegal(t *testing.T) {
	res := Solve([]Event{
		ev("2026-01-05T08:00:00+08:00", Enter),
		ev("2026-01-05T09:30:00+08:00", Exit),
	}, rules(15, 30, 100, 0))

	if res.Status != StatusOK {
		t.Fatalf("status: want ok, got %s (%s)", res.Status, res.Reason)
	}
	if res.Inserted != nil {
		t.Fatalf("inserted: want nil, got %+v", res.Inserted)
	}
	if got := feeOf(t, res.Min); got != 300 {
		t.Fatalf("fee: want 300, got %d", got)
	}
	if feeOf(t, res.Max) != feeOf(t, res.Min) {
		t.Fatalf("min/max differ for a determined record")
	}
}

func TestUnrecoverable(t *testing.T) {
	cases := [][]Event{
		// even length with a defect
		{ev("2026-01-05T08:00:00+08:00", Enter), ev("2026-01-05T09:00:00+08:00", Enter),
			ev("2026-01-05T10:00:00+08:00", Exit), ev("2026-01-05T11:00:00+08:00", Exit)},
		// two defects
		{ev("2026-01-05T08:00:00+08:00", Enter), ev("2026-01-05T09:00:00+08:00", Enter),
			ev("2026-01-05T10:00:00+08:00", Exit), ev("2026-01-05T11:00:00+08:00", Exit),
			ev("2026-01-05T12:00:00+08:00", Enter)},
		// defect but wrong endpoints
		{ev("2026-01-05T08:00:00+08:00", Exit), ev("2026-01-05T09:00:00+08:00", Enter),
			ev("2026-01-05T10:00:00+08:00", Exit), ev("2026-01-05T11:00:00+08:00", Exit),
			ev("2026-01-05T12:00:00+08:00", Enter)},
	}
	for i, evs := range cases {
		if res := Solve(evs, rules(15, 30, 100, 1000)); res.Status != StatusUnrecoverable {
			t.Fatalf("case %d: want unrecoverable, got %s", i, res.Status)
		}
	}
}

// Zero unit price: every completion is free, so the fee is unique even
// though the insert time is not.
func TestZeroPriceDetermined(t *testing.T) {
	res := Solve([]Event{
		ev("2026-01-05T08:00:00+08:00", Enter),
		ev("2026-01-05T09:00:00+08:00", Exit),
		ev("2026-01-05T10:00:00+08:00", Enter),
	}, rules(15, 30, 0, 1000))

	if res.Status != StatusOK {
		t.Fatalf("status: want ok, got %s", res.Status)
	}
	if got := feeOf(t, res.Min); got != 0 {
		t.Fatalf("fee: want 0, got %d", got)
	}
	checkWindow(t, res.Min.Windows[0], "2026-01-05T10:00:00+08:00", "")
}

func TestInvalidInput(t *testing.T) {
	if res := Solve(nil, rules(15, 30, 100, 0)); res.Status != StatusInvalid {
		t.Fatalf("empty: want invalid, got %s", res.Status)
	}
	unsorted := []Event{
		ev("2026-01-05T09:00:00+08:00", Enter),
		ev("2026-01-05T08:00:00+08:00", Exit),
	}
	if res := Solve(unsorted, rules(15, 30, 100, 0)); res.Status != StatusInvalid {
		t.Fatalf("unsorted: want invalid, got %s", res.Status)
	}
	if res := Solve([]Event{ev("2026-01-05T08:00:00+08:00", Enter)}, rules(15, 0, 100, 0)); res.Status != StatusInvalid {
		t.Fatalf("zero unit: want invalid, got %s", res.Status)
	}
}

// The representative segments of each extreme must really bill to the
// reported fee.
func TestRepresentativeSegmentsMatchFees(t *testing.T) {
	r := rules(15, 30, 100, 1000)
	res := Solve([]Event{
		ev("2026-01-05T08:00:00+08:00", Enter),
		ev("2026-01-05T09:00:00+08:00", Exit),
		ev("2026-01-05T10:00:00+08:00", Enter),
		ev("2026-01-05T12:00:00+08:00", Enter),
		ev("2026-01-05T13:00:00+08:00", Exit),
	}, r)
	for _, ext := range []*Extreme{res.Min, res.Max} {
		dayFees := map[string]int64{}
		for _, s := range ext.Segments {
			dayFees[dayKey(s.Enter)] += s.FeeCents
		}
		var total int64
		for _, v := range dayFees {
			if r.DailyCap > 0 && v > r.DailyCap {
				v = r.DailyCap
			}
			total += v
		}
		if total != *ext.FeeCents {
			t.Fatalf("segments bill %d, extreme says %d", total, *ext.FeeCents)
		}
	}
}
