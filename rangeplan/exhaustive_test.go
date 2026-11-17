package rangeplan

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"testing"
)

// exhaustiveMaxSize is the largest object length enumerated. Every object of
// size 0..exhaustiveMaxSize is exercised against every possible range form.
const exhaustiveMaxSize = 6

// allSpecs returns every distinct range spec whose endpoints lie inside the
// small value domain 0..domain, including open-ended and suffix forms.
func allSpecs(domain int64) []Spec {
	specs := make([]Spec, 0)
	for start := int64(0); start <= domain; start++ {
		for end := start; end <= domain; end++ {
			specs = append(specs, Spec{Start: start, End: end})
		}
		specs = append(specs, Spec{Start: start, OpenEnd: true})
	}
	for n := int64(0); n <= domain; n++ {
		specs = append(specs, Spec{End: n, Suffix: true})
	}
	return specs
}

// allSpecsFor returns specs for enumerating against an object of the given
// size, including out-of-bounds and suffix-0 forms so unsatisfiable paths are
// covered too.
func allSpecsFor(size int64) []Spec {
	domain := size + 2
	if size < 2 {
		domain = 3
	}
	return allSpecs(domain)
}

func TestExhaustiveSingles(t *testing.T) {
	for size := int64(0); size <= exhaustiveMaxSize; size++ {
		for _, spec := range allSpecsFor(size) {
			header := specHeader(spec)
			plan, err := PlanRange(header, size, permissiveLimits())

			r, nerr := Normalize(spec, size)
			if nerr != nil {
				if err != nerr {
					t.Fatalf("size=%d spec=%+v: plan err=%v, normalize err=%v", size, spec, err, nerr)
				}
				continue
			}
			if err != nil {
				t.Fatalf("size=%d spec=%+v: unexpected plan error %v", size, spec, err)
			}
			if len(plan.Segments) != 1 || len(plan.Reads) != 1 {
				t.Fatalf("size=%d spec=%+v: segments=%d reads=%d", size, spec, plan.Segments, plan.Reads)
			}
			seg := Segment{r.Start, r.End}
			if plan.Segments[0] != seg {
				t.Errorf("size=%d spec=%+v: segment %+v, want %+v", size, spec, plan.Segments[0], seg)
			}
			if plan.Reads[0] != (Span{r.Start, r.End}) {
				t.Errorf("size=%d spec=%+v: read %+v, want %+v", size, spec, plan.Reads[0], r)
			}
			assertPlanInvariants(t, fmt.Sprintf("size=%d spec=%+v", size, spec), plan, size)
		}
	}
}

func TestExhaustivePairsAndTriples(t *testing.T) {
	for size := int64(0); size <= exhaustiveMaxSize; size++ {
		specs := allSpecsFor(size)
		for i := range specs {
			for j := range specs {
				checkCombo(t, size, specs[i], specs[j])
			}
		}
		// A smaller third-domain keeps the triple case fast while still
		// exercising duplicate and overlap interactions.
		triples := allSpecs(size)
		for i := range triples {
			for j := range triples {
				checkCombo(t, size, triples[i], triples[j], triples[i])
			}
		}
	}
}

func checkCombo(t *testing.T, size int64, combo ...Spec) {
	t.Helper()
	header := joinSpecHeaders(combo)
	plan, err := PlanRange(header, size, permissiveLimits())

	expected, nerr := NormalizeAll(combo, size)
	if nerr != nil {
		if err != nerr {
			t.Fatalf("size=%d combo=%+v: plan err=%v, normalize err=%v", size, combo, err, nerr)
		}
		return
	}
	if err != nil {
		t.Fatalf("size=%d combo=%+v: unexpected error %v", size, combo, err)
	}

	if len(plan.Segments) != len(expected) {
		t.Fatalf("size=%d combo=%+v: %d segments, want %d", size, combo, len(plan.Segments), len(expected))
	}
	// Segment order and multiplicity must mirror the request.
	for k, r := range expected {
		if plan.Segments[k] != (Segment{r.Start, r.End}) {
			t.Errorf("size=%d combo=%+v segment[%d]=%+v, want %+v", size, combo, k, plan.Segments[k], r)
		}
	}
	assertPlanInvariants(t, fmt.Sprintf("size=%d combo=%+v", size, combo), plan, size)
	assertMergeCoverage(t, fmt.Sprintf("size=%d combo=%+v", size, combo), plan)
	assertEnvelopeRoundTrip(t, fmt.Sprintf("size=%d combo=%+v", size, combo), plan)
}

// assertPlanInvariants checks properties that must hold for every plan.
func assertPlanInvariants(t *testing.T, ctx string, plan *Plan, size int64) {
	t.Helper()

	data, err := plan.DataBytes()
	if err != nil {
		t.Fatalf("%s: DataBytes error %v", ctx, err)
	}
	read, err := plan.ReadBytes()
	if err != nil {
		t.Fatalf("%s: ReadBytes error %v", ctx, err)
	}
	overhead, err := plan.OverheadBytes()
	if err != nil {
		t.Fatalf("%s: OverheadBytes error %v", ctx, err)
	}
	total, err := plan.TotalBytes()
	if err != nil {
		t.Fatalf("%s: TotalBytes error %v", ctx, err)
	}
	if total != data+overhead {
		t.Errorf("%s: total %d != data %d + overhead %d", ctx, total, data, overhead)
	}
	// Merged reads can never exceed response payload bytes (duplicates only
	// inflate the response side) and never exceed object size.
	if read > data {
		t.Errorf("%s: read bytes %d exceed data bytes %d", ctx, read, data)
	}
	if read > size {
		t.Errorf("%s: read bytes %d exceed object size %d", ctx, read, size)
	}
	if overhead <= 0 || total <= data {
		t.Errorf("%s: framing not accounted: overhead=%d total=%d", ctx, overhead, total)
	}

	// Reads are sorted, disjoint and non-adjacent (adjacency would have been
	// merged), and stay inside the object.
	for i, span := range plan.Reads {
		if span.Start < 0 || span.End >= size || span.Start > span.End {
			t.Errorf("%s: invalid span %+v for size %d", ctx, span, size)
		}
		if i > 0 {
			prev := plan.Reads[i-1]
			if span.Start <= prev.End+1 {
				t.Errorf("%s: unmerged spans %+v %+v", ctx, prev, span)
			}
		}
	}

	// Every segment is served wholly by exactly one read span.
	for _, seg := range plan.Segments {
		found := false
		for _, span := range plan.Reads {
			if span.Start <= seg.Start && seg.End <= span.End {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: segment %+v not covered by any read %+v", ctx, seg, plan.Reads)
		}
	}
}

// assertMergeCoverage verifies the merged reads cover exactly the union of
// requested ranges, byte for byte, on the small object.
func assertMergeCoverage(t *testing.T, ctx string, plan *Plan) {
	t.Helper()
	wantCover := map[int64]bool{}
	for _, seg := range plan.Segments {
		for b := seg.Start; b <= seg.End; b++ {
			wantCover[b] = true
		}
	}
	gotCover := map[int64]bool{}
	for _, span := range plan.Reads {
		for b := span.Start; b <= span.End; b++ {
			if gotCover[b] {
				t.Fatalf("%s: byte %d read twice across spans", ctx, b)
			}
			gotCover[b] = true
		}
	}
	if len(gotCover) != len(wantCover) {
		t.Fatalf("%s: covered %d bytes, want %d", ctx, len(gotCover), len(wantCover))
	}
	for b := range wantCover {
		if !gotCover[b] {
			t.Errorf("%s: byte %d missing from reads", ctx, b)
		}
	}
}

// assertEnvelopeRoundTrip independently assembles the multipart response from
// the plan and checks its exact length against TotalBytes, proving the
// overhead budget includes boundary and per-segment headers.
func assertEnvelopeRoundTrip(t *testing.T, ctx string, plan *Plan) {
	t.Helper()
	boundary := DefaultBoundary
	contentType := DefaultContentType

	var buf bytes.Buffer
	payload := bytes.Repeat([]byte("x"), int(plan.Size))
	for _, seg := range plan.Segments {
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", contentType)
		fmt.Fprintf(&buf, "Content-Range: bytes %d-%d/%d\r\n\r\n", seg.Start, seg.End, plan.Size)
		buf.Write(payload[seg.Start : seg.End+1])
		buf.WriteString("\r\n")
	}
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)

	total, err := plan.TotalBytes()
	if err != nil {
		t.Fatalf("%s: %v", ctx, err)
	}
	if int64(buf.Len()) != total {
		t.Errorf("%s: assembled envelope %d bytes, budgeted total %d", ctx, buf.Len(), total)
	}
}

func TestDuplicateSegmentsPreserved(t *testing.T) {
	plan, err := PlanRange("bytes=0-2,0-2,0-2", 10, permissiveLimits())
	if err != nil {
		t.Fatalf("duplicate ranges: %v", err)
	}
	if len(plan.Segments) != 3 {
		t.Fatalf("segments = %d, want 3 duplicates", len(plan.Segments))
	}
	if len(plan.Reads) != 1 || plan.Reads[0] != (Span{0, 2}) {
		t.Fatalf("reads = %+v, want single merged span {0 2}", plan.Reads)
	}
	data, _ := plan.DataBytes()
	read, _ := plan.ReadBytes()
	if data != 9 || read != 3 {
		t.Fatalf("data=%d read=%d, want 9 and 3", data, read)
	}
}

func TestAdjacentRangesMerged(t *testing.T) {
	plan, err := PlanRange("bytes=0-4,5-9,10-12", 13, permissiveLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Reads) != 1 || plan.Reads[0] != (Span{0, 12}) {
		t.Fatalf("adjacent ranges not merged: %+v", plan.Reads)
	}
	if len(plan.Segments) != 3 {
		t.Fatalf("response semantics lost: segments=%d", len(plan.Segments))
	}
}

func TestLimitsEnforced(t *testing.T) {
	if _, err := PlanRange("bytes=0-1,2-3,4-5", 10, Limits{MaxRanges: 2}); err != ErrTooManyRanges {
		t.Errorf("MaxRanges: err=%v", err)
	}
	if _, err := PlanRange("bytes=0-1,2-3,4-5", 10, Limits{MaxSegments: 2}); err != ErrTooManySegments {
		t.Errorf("MaxSegments: err=%v", err)
	}

	// Budget includes framing: two 1-byte segments need more than 2 bytes.
	_, err := PlanRange("bytes=0-0,2-2", 10, Limits{MaxResponseBytes: 2})
	if err != ErrResponseTooLarge {
		t.Errorf("MaxResponseBytes ignored framing: err=%v", err)
	}

	plan, err := PlanRange("bytes=0-0,2-2", 10, Limits{MaxResponseBytes: 1024})
	if err != nil {
		t.Fatalf("generous budget rejected: %v", err)
	}
	total, _ := plan.TotalBytes()
	if total > 1024 {
		t.Fatalf("planned total %d over budget", total)
	}
}

func TestOverflowAccumulation(t *testing.T) {
	// Normalization at the int64 boundary must clip without underflow.
	maxSuffix, err := Normalize(Spec{End: math.MaxInt64, Suffix: true}, math.MaxInt64)
	if err != nil {
		t.Fatalf("max-int suffix normalize: %v", err)
	}
	if maxSuffix.Length() != math.MaxInt64 {
		t.Fatalf("clipped suffix length = %d, want %d", maxSuffix.Length(), int64(math.MaxInt64))
	}
	maxClosed, err := Normalize(Spec{Start: 0, End: math.MaxInt64}, math.MaxInt64)
	if err != nil {
		t.Fatalf("max-int closed normalize: %v", err)
	}
	if maxClosed.Length() != math.MaxInt64 {
		t.Fatalf("clipped closed length = %d, want %d", maxClosed.Length(), int64(math.MaxInt64))
	}

	// Two full-size duplicate segments overflow the cumulative byte counter.
	_, err = PlanRange(
		"bytes=0-"+strconv.FormatInt(math.MaxInt64-1, 10)+
			",0-"+strconv.FormatInt(math.MaxInt64-1, 10),
		math.MaxInt64, permissiveLimits())
	if err != ErrOverflow {
		t.Fatalf("cumulative overflow: err=%v, want ErrOverflow", err)
	}

	// A single full-size range is valid payload-wise, but formatting its
	// Content-Range header into the byte budget overflows int64; the planner
	// must surface ErrOverflow rather than wrap.
	_, err = PlanRange("bytes=0-"+strconv.FormatInt(math.MaxInt64-1, 10),
		math.MaxInt64, permissiveLimits())
	if err != ErrOverflow {
		t.Fatalf("framing overflow: err=%v, want ErrOverflow", err)
	}
}

func TestHTTPStatusMapping(t *testing.T) {
	cases := map[error]int{
		ErrMalformedRange:   400,
		ErrUnsatisfiable:    416,
		ErrTooManyRanges:    416,
		ErrTooManySegments:  416,
		ErrResponseTooLarge: 416,
		ErrOverflow:         400,
	}
	for err, want := range cases {
		if got := HTTPStatus(err); got != want {
			t.Errorf("HTTPStatus(%v) = %d, want %d", err, got, want)
		}
	}
}

func specHeader(s Spec) string {
	switch {
	case s.IsSuffix():
		return "bytes=-" + strconv.FormatInt(s.End, 10)
	case s.IsOpenEnded():
		return "bytes=" + strconv.FormatInt(s.Start, 10) + "-"
	default:
		return "bytes=" + strconv.FormatInt(s.Start, 10) + "-" + strconv.FormatInt(s.End, 10)
	}
}

func joinSpecHeaders(specs []Spec) string {
	header := "bytes="
	for i, s := range specs {
		if i > 0 {
			header += ","
		}
		header += specHeader(s)[len("bytes="):]
	}
	return header
}

// permissiveLimits disables count and size caps while keeping deterministic
// framing, so enumeration exercises the math rather than the limits.
func permissiveLimits() Limits {
	return Limits{
		Boundary:    DefaultBoundary,
		ContentType: DefaultContentType,
	}
}
