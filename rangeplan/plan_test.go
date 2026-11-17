package rangeplan

import (
	"errors"
	"testing"
)

func TestNewLimitsAndBudgetRejection(t *testing.T) {
	limits := NewLimits()
	if limits.MaxRanges <= 0 || limits.MaxSegments <= 0 || limits.MaxResponseBytes <= 0 {
		t.Fatalf("NewLimits returned ineffective limits: %+v", limits)
	}

	// A request inside the default budget succeeds and reports total bytes.
	plan, err := PlanRange("bytes=0-99", 1000, limits)
	if err != nil {
		t.Fatalf("in-budget request rejected: %v", err)
	}
	total, err := plan.TotalBytes()
	if err != nil {
		t.Fatalf("TotalBytes: %v", err)
	}
	if total > limits.MaxResponseBytes {
		t.Fatalf("total %d exceeds configured budget %d", total, limits.MaxResponseBytes)
	}

	// Many tiny segments are rejected because framing alone blows the budget.
	header := "bytes="
	for i := 0; i < limits.MaxSegments; i++ {
		if i > 0 {
			header += ","
		}
		header += "0-0"
	}
	if _, err := PlanRange(header, 1, Limits{
		MaxRanges:        limits.MaxRanges,
		MaxSegments:      limits.MaxSegments,
		MaxResponseBytes: 64,
	}); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("framing-heavy request: err=%v, want ErrResponseTooLarge", err)
	}
}

func TestUnsatisfiableRequest(t *testing.T) {
	_, err := PlanRange("bytes=100-200", 10, NewLimits())
	if !errors.Is(err, ErrUnsatisfiable) {
		t.Fatalf("err=%v, want ErrUnsatisfiable", err)
	}
	if HTTPStatus(err) != 416 {
		t.Fatalf("status=%d, want 416", HTTPStatus(err))
	}

	// Empty object: every range is unsatisfiable.
	if _, err := PlanRange("bytes=0-", 0, NewLimits()); !errors.Is(err, ErrUnsatisfiable) {
		t.Fatalf("empty object range: err=%v", err)
	}
}

func TestCustomFramingChangesOverhead(t *testing.T) {
	planA, err := PlanRange("bytes=0-3", 10, Limits{Boundary: "b", ContentType: "multipart/byteranges"})
	if err != nil {
		t.Fatal(err)
	}
	planB, err := PlanRange("bytes=0-3", 10, Limits{Boundary: "a-much-longer-boundary-value", ContentType: "multipart/byteranges"})
	if err != nil {
		t.Fatal(err)
	}
	overheadA, _ := planA.OverheadBytes()
	overheadB, _ := planB.OverheadBytes()
	if overheadB <= overheadA {
		t.Fatalf("longer boundary did not increase overhead: %d vs %d", overheadB, overheadA)
	}

	data, _ := planA.DataBytes()
	if data != 4 {
		t.Fatalf("data bytes = %d, want 4", data)
	}
}

func TestOverlappingRangesMerge(t *testing.T) {
	plan, err := PlanRange("bytes=0-9,5-15,20-25,22-30", 40, permissiveLimits())
	if err != nil {
		t.Fatal(err)
	}
	wantReads := []Span{{0, 15}, {20, 30}}
	if len(plan.Reads) != len(wantReads) {
		t.Fatalf("reads=%+v, want %+v", plan.Reads, wantReads)
	}
	for i, want := range wantReads {
		if plan.Reads[i] != want {
			t.Errorf("read[%d]=%+v, want %+v", i, plan.Reads[i], want)
		}
	}
	if len(plan.Segments) != 4 {
		t.Fatalf("segments=%d, want 4 (response semantics preserved)", len(plan.Segments))
	}
	read, _ := plan.ReadBytes()
	data, _ := plan.DataBytes()
	if read != 27 || data != 10+11+6+9 {
		t.Fatalf("read=%d data=%d", read, data)
	}
}
