package netsec

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func mustSpecs(t *testing.T, header string) []Spec {
	t.Helper()
	specs, err := Parse(header)
	if err != nil {
		t.Fatalf("parse %q: %v", header, err)
	}
	return specs
}

func TestPlanMergesReadsPreservesSegments(t *testing.T) {
	specs := mustSpecs(t, "bytes=0-99,50-149,200-299")
	plan, err := PlanRequest(1000, specs, Limits{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantSegments := []Segment{{0, 99}, {50, 149}, {200, 299}}
	if !reflect.DeepEqual(plan.Segments, wantSegments) {
		t.Fatalf("segments = %+v", plan.Segments)
	}
	wantReads := []Segment{{0, 149}, {200, 299}}
	if !reflect.DeepEqual(plan.Reads, wantReads) {
		t.Fatalf("reads = %+v", plan.Reads)
	}
	if plan.DataBytes != 300 {
		t.Fatalf("DataBytes = %d", plan.DataBytes)
	}
	if plan.ReadBytes != 250 {
		t.Fatalf("ReadBytes = %d", plan.ReadBytes)
	}
	if plan.AmplificationBytes != 50 {
		t.Fatalf("AmplificationBytes = %d", plan.AmplificationBytes)
	}
	if plan.WireBytes != 300 {
		t.Fatalf("default WireBytes should equal DataBytes, got %d", plan.WireBytes)
	}
}

func TestPlanDeduplicatesRepeatedParts(t *testing.T) {
	specs := mustSpecs(t, "bytes=0-99,0-99,-50")
	plan, err := PlanRequest(1000, specs, Limits{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantSegments := []Segment{{0, 99}, {950, 999}}
	if !reflect.DeepEqual(plan.Segments, wantSegments) {
		t.Fatalf("segments = %+v", plan.Segments)
	}
	if plan.DuplicateParts != 1 {
		t.Fatalf("DuplicateParts = %d", plan.DuplicateParts)
	}
}

func TestPlanAdjacentSpansCoalesce(t *testing.T) {
	specs := mustSpecs(t, "bytes=0-99,100-199,200-")
	plan, err := PlanRequest(300, specs, Limits{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantReads := []Segment{{0, 299}}
	if !reflect.DeepEqual(plan.Reads, wantReads) {
		t.Fatalf("reads = %+v", plan.Reads)
	}
	if len(plan.Segments) != 3 {
		t.Fatalf("response parts must stay distinct: %+v", plan.Segments)
	}
	if plan.ReadBytes != plan.DataBytes {
		t.Fatalf("adjacent reads should waste no bytes")
	}
}

func TestPlanUnsatisfiableCarriesIndex(t *testing.T) {
	specs := mustSpecs(t, "bytes=0-9,500-600")
	_, err := PlanRequest(100, specs, Limits{}, nil)
	var unsatisfiable *UnsatisfiableError
	if !errors.As(err, &unsatisfiable) {
		t.Fatalf("got %v, want UnsatisfiableError", err)
	}
	if unsatisfiable.Index != 1 {
		t.Fatalf("Index = %d, want 1", unsatisfiable.Index)
	}
	if !errors.Is(err, ErrUnsatisfiable) {
		t.Fatalf("must wrap ErrUnsatisfiable")
	}
}

func TestPlanLimits(t *testing.T) {
	specs := mustSpecs(t, "bytes=0-9,20-29,40-49")

	if _, err := PlanRequest(100, specs, Limits{MaxSegments: 2}, nil); !errors.Is(err, ErrTooManySegments) {
		t.Fatalf("MaxSegments: got %v", err)
	}
	if _, err := PlanRequest(100, specs, Limits{MaxDataBytes: 25}, nil); !errors.Is(err, ErrDataLimit) {
		t.Fatalf("MaxDataBytes: got %v", err)
	}
	if _, err := PlanRequest(100, specs, Limits{MaxReads: 2}, nil); !errors.Is(err, ErrReadLimit) {
		t.Fatalf("MaxReads: got %v", err)
	}

	estimator := EstimatorFunc(func(size int64, segments []Segment) (int64, error) {
		var total int64
		for range segments {
			total += 100
		}
		return total, nil
	})
	if _, err := PlanRequest(100, specs, Limits{MaxWireBytes: 250}, estimator); !errors.Is(err, ErrWireLimit) {
		t.Fatalf("MaxWireBytes: got %v", err)
	}

	plan, err := PlanRequest(100, specs, Limits{
		MaxSegments: 3, MaxDataBytes: 30, MaxWireBytes: 300, MaxReads: 3,
	}, estimator)
	if err != nil {
		t.Fatalf("within limits should pass: %v", err)
	}
	if plan.WireBytes != 300 {
		t.Fatalf("WireBytes = %d", plan.WireBytes)
	}
}

func TestPlanNoRangesAndInvalidSize(t *testing.T) {
	if _, err := PlanRequest(10, nil, Limits{}, nil); !errors.Is(err, ErrNoRanges) {
		t.Fatalf("empty specs: got %v", err)
	}
	if _, err := PlanRequest(-1, mustSpecs(t, "bytes=0-1"), Limits{}, nil); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("negative size: got %v", err)
	}
}

func TestPlannerMethod(t *testing.T) {
	planner := &Planner{Limits: Limits{MaxSegments: 1}}
	_, err := planner.Plan(100, mustSpecs(t, "bytes=0-1,3-4"))
	if !errors.Is(err, ErrTooManySegments) {
		t.Fatalf("got %v", err)
	}
}

func TestMultipartEstimatorIncludesOverhead(t *testing.T) {
	estimator := &MultipartEstimator{Boundary: "BOUNDARY"}
	segments := []Segment{{0, 99}, {200, 249}}
	wire, err := estimator.Estimate(1000, segments)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}

	expectedBody :=
		"\r\n--BOUNDARY\r\nContent-Range: bytes 0-99/1000\r\n\r\n" +
			"0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789"[:100] +
			"\r\n--BOUNDARY\r\nContent-Range: bytes 200-249/1000\r\n\r\n" +
			"01234567890123456789012345678901234567890123456789"[:50] +
			"\r\n--BOUNDARY--\r\n"
	if wire != int64(len(expectedBody)) {
		t.Fatalf("wire = %d, want %d", wire, len(expectedBody))
	}
	if wire <= 150 {
		t.Fatalf("wire budget must exceed payload-only size")
	}
	if got := estimator.ContentType(); got != "multipart/byteranges; boundary=BOUNDARY" {
		t.Fatalf("ContentType = %q", got)
	}
}

func TestNewMultipartEstimatorBoundary(t *testing.T) {
	first := NewMultipartEstimator()
	second := NewMultipartEstimator()
	if len(first.Boundary) != 30 || first.Boundary == second.Boundary {
		t.Fatalf("boundaries should be random 30-char strings: %q %q", first.Boundary, second.Boundary)
	}
	plan, err := PlanRequest(100, mustSpecs(t, "bytes=0-9"), Limits{}, first)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.WireBytes <= plan.DataBytes {
		t.Fatalf("wire must include boundary overhead")
	}
}

func TestEstimatorErrorPropagates(t *testing.T) {
	estimator := EstimatorFunc(func(size int64, segments []Segment) (int64, error) {
		return 0, ErrOverflow
	})
	if _, err := PlanRequest(100, mustSpecs(t, "bytes=0-9"), Limits{}, estimator); !errors.Is(err, ErrOverflow) {
		t.Fatalf("got %v", err)
	}

	empty := &MultipartEstimator{}
	if _, err := empty.Estimate(10, []Segment{{0, 1}}); err == nil {
		t.Fatalf("empty boundary must fail")
	}

	// Internal overflow branch inside coalesce: impossible through normal
	// planning because data accumulation overflows first, so exercise the
	// estimator's multi-segment overflow path directly instead.
	max := int64(math.MaxInt64)
	mpEstimator := &MultipartEstimator{Boundary: "B"}
	if _, err := mpEstimator.Estimate(max, []Segment{
		{First: 0, Last: max / 2},
		{First: max/2 + 1, Last: max - 1},
	}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("two max-sized parts must overflow: %v", err)
	}
}

func TestIntegerOverflow(t *testing.T) {
	max := int64(math.MaxInt64)

	// Parsing numbers beyond int64 is a malformed header.
	if _, err := Parse("bytes=9223372036854775808-9223372036854775809"); !errors.Is(err, ErrMalformed) {
		t.Fatalf("overflow parse: got %v", err)
	}

	// A valid huge open-tail normalizes without arithmetic overflow.
	spec := Spec{Kind: OpenTail, Start: max}
	if _, err := spec.Normalize(max + 0); !errors.Is(err, ErrUnsatisfiable) {
		t.Fatalf("start at size should be unsatisfiable, got %v", err)
	}
	normalized, err := Spec{Kind: Closed, Start: max - 1, End: max}.Normalize(max)
	if err != nil {
		t.Fatalf("max closed normalize: %v", err)
	}
	if normalized.Length() != 1 {
		t.Fatalf("length = %d", normalized.Length())
	}

	// Suffix larger than the object clamps without overflowing size-suffix.
	suffix, err := Spec{Kind: Suffix, Suffix: max}.Normalize(max)
	if err != nil {
		t.Fatalf("max suffix normalize: %v", err)
	}
	if suffix.First != 0 || suffix.Last != max-1 {
		t.Fatalf("suffix = %+v", suffix)
	}

	// A wire estimator that overflows the int64 budget fails the plan.
	estimator := EstimatorFunc(func(size int64, segments []Segment) (int64, error) {
		return max, nil
	})
	if _, err := PlanRequest(max, []Spec{{Kind: Suffix, Suffix: 1}}, Limits{MaxWireBytes: 1}, estimator); !errors.Is(err, ErrWireLimit) {
		t.Fatalf("wire limit at max: got %v", err)
	}
}

func TestCheckedArithmeticHelpers(t *testing.T) {
	max := int64(math.MaxInt64)
	if _, ok := checkedAdd(max, 1); ok {
		t.Fatalf("checkedAdd should detect overflow")
	}
	if sum, ok := checkedAdd(max-2, 2); !ok || sum != max {
		t.Fatalf("checkedAdd near max failed")
	}
	if _, ok := checkedSub(1, 2); ok {
		t.Fatalf("checkedSub should detect negative result")
	}
	if diff, ok := checkedSub(5, 3); !ok || diff != 2 {
		t.Fatalf("checkedSub failed")
	}
}
