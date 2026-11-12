package netsec

import (
	"errors"
	"math"
	"reflect"
	"sort"
	"testing"
)

// testEstimator charges a fixed per-part overhead and a fixed closing
// overhead, independently of the production MultipartEstimator.
const (
	testPartOverhead    int64 = 25
	testClosingOverhead int64 = 10
)

type testEstimator struct{}

func (testEstimator) Estimate(size int64, segments []Segment) (int64, error) {
	var wire int64
	for _, segment := range segments {
		part, ok := checkedAdd(segment.Length(), testPartOverhead)
		if !ok {
			return 0, ErrOverflow
		}
		sum, ok := checkedAdd(wire, part)
		if !ok {
			return 0, ErrOverflow
		}
		wire = sum
	}
	var ok bool
	wire, ok = checkedAdd(wire, testClosingOverhead)
	if !ok {
		return 0, ErrOverflow
	}
	return wire, nil
}

func expectedWire(segments []Segment) int64 {
	var wire int64 = testClosingOverhead
	for _, segment := range segments {
		wire += segment.Length() + testPartOverhead
	}
	return wire
}

// allSpecs enumerates every interval shape that can target an object of the
// given size, including values just past the end.
func allSpecs(size int64) []Spec {
	var specs []Spec
	for start := int64(0); start <= size; start++ {
		for end := start; end <= size+1; end++ {
			specs = append(specs, Spec{Kind: Closed, Start: start, End: end})
		}
	}
	for start := int64(0); start <= size; start++ {
		specs = append(specs, Spec{Kind: OpenTail, Start: start})
	}
	for suffix := int64(1); suffix <= size+1; suffix++ {
		specs = append(specs, Spec{Kind: Suffix, Suffix: suffix})
	}
	return specs
}

// oracleNormalize is an independent reference implementation of the
// normalization rules.
func oracleNormalize(spec Spec, size int64) (Segment, bool) {
	switch spec.Kind {
	case Closed:
		if spec.Start >= size {
			return Segment{}, false
		}
		last := spec.End
		if last >= size {
			last = size - 1
		}
		return Segment{First: spec.Start, Last: last}, true
	case OpenTail:
		if spec.Start >= size {
			return Segment{}, false
		}
		return Segment{First: spec.Start, Last: size - 1}, true
	default:
		if size == 0 {
			return Segment{}, false
		}
		n := spec.Suffix
		if n > size {
			n = size
		}
		return Segment{First: size - n, Last: size - 1}, true
	}
}

func oracleMerge(segments []Segment) []Segment {
	sorted := append([]Segment(nil), segments...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].First < sorted[j].First })
	reads := []Segment{}
	for _, segment := range sorted {
		if len(reads) == 0 || segment.First > reads[len(reads)-1].Last+1 {
			reads = append(reads, segment)
		} else if segment.Last > reads[len(reads)-1].Last {
			reads[len(reads)-1].Last = segment.Last
		}
	}
	return reads
}

// TestExhaustiveNormalization checks every interval shape against every
// object length from empty through five bytes.
func TestExhaustiveNormalization(t *testing.T) {
	for size := int64(0); size <= 5; size++ {
		for _, spec := range allSpecs(size) {
			got, err := spec.Normalize(size)
			want, ok := oracleNormalize(spec, size)
			if !ok {
				if !errors.Is(err, ErrUnsatisfiable) {
					t.Fatalf("size=%d spec=%s: got %v, want unsatisfiable", size, spec.String(), err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("size=%d spec=%s: unexpected error %v", size, spec.String(), err)
			}
			if got != want {
				t.Fatalf("size=%d spec=%s: got %+v, want %+v", size, spec.String(), got, want)
			}
			if got.Length() <= 0 || got.First < 0 || got.Last >= size {
				t.Fatalf("segment outside object bounds: %+v size=%d", got, size)
			}
		}
	}
}

// TestExhaustivePairs enumerates every ordered pair of intervals for each
// small object and verifies planning against an independent oracle.
func TestExhaustivePairs(t *testing.T) {
	for size := int64(0); size <= 5; size++ {
		specs := allSpecs(size)
		for _, first := range specs {
			for _, second := range specs {
				request := []Spec{first, second}
				plan, err := PlanRequest(size, request, Limits{}, testEstimator{})

				firstSegment, firstOK := oracleNormalize(first, size)
				secondSegment, secondOK := oracleNormalize(second, size)

				if !firstOK || !secondOK {
					wantIndex := 0
					if firstOK {
						wantIndex = 1
					}
					var unsatisfiable *UnsatisfiableError
					if !errors.As(err, &unsatisfiable) {
						t.Fatalf("size=%d pair=%s,%s: got %v, want unsatisfiable",
							size, first.String(), second.String(), err)
					}
					if unsatisfiable.Index != wantIndex {
						t.Fatalf("size=%d pair: index=%d want %d", size, unsatisfiable.Index, wantIndex)
					}
					continue
				}
				if err != nil {
					t.Fatalf("size=%d pair=%s,%s: unexpected error %v",
						size, first.String(), second.String(), err)
				}

				wantSegments := []Segment{firstSegment}
				var wantDuplicates int64
				if secondSegment != firstSegment {
					wantSegments = append(wantSegments, secondSegment)
				} else {
					wantDuplicates = 1
				}
				if !reflect.DeepEqual(plan.Segments, wantSegments) {
					t.Fatalf("size=%d pair=%s,%s: segments=%+v want %+v",
						size, first.String(), second.String(), plan.Segments, wantSegments)
				}
				if plan.DuplicateParts != wantDuplicates {
					t.Fatalf("duplicates=%d want %d", plan.DuplicateParts, wantDuplicates)
				}

				var wantData int64
				for _, segment := range wantSegments {
					wantData += segment.Length()
				}
				wantReads := oracleMerge(wantSegments)
				var wantReadBytes int64
				for _, read := range wantReads {
					wantReadBytes += read.Length()
				}

				if plan.DataBytes != wantData {
					t.Fatalf("DataBytes=%d want %d", plan.DataBytes, wantData)
				}
				if !reflect.DeepEqual(plan.Reads, wantReads) {
					t.Fatalf("reads=%+v want %+v", plan.Reads, wantReads)
				}
				if plan.ReadBytes != wantReadBytes {
					t.Fatalf("ReadBytes=%d want %d", plan.ReadBytes, wantReadBytes)
				}
				if plan.AmplificationBytes != wantData-wantReadBytes {
					t.Fatalf("amplification=%d", plan.AmplificationBytes)
				}
				if plan.ReadBytes > plan.DataBytes {
					t.Fatalf("reads cannot exceed response payload")
				}
				wantWire := expectedWire(wantSegments)
				if plan.WireBytes != wantWire {
					t.Fatalf("WireBytes=%d want %d", plan.WireBytes, wantWire)
				}
				if plan.WireBytes <= plan.DataBytes {
					t.Fatalf("wire budget must include boundary overhead")
				}

				for _, segment := range wantSegments {
					covered := false
					for _, read := range plan.Reads {
						if read.First <= segment.First && read.Last >= segment.Last {
							covered = true
						}
					}
					if !covered {
						t.Fatalf("segment %+v not covered by reads %+v", segment, plan.Reads)
					}
				}

				assertLimitsRejectAndAllow(t, size, request, plan)
			}
		}
	}
}

// TestExhaustiveTriples covers three-interval requests for the smallest
// objects, exercising repeated and nested overlap combinations.
func TestExhaustiveTriples(t *testing.T) {
	for size := int64(0); size <= 2; size++ {
		specs := allSpecs(size)
		for _, first := range specs {
			for _, second := range specs {
				for _, third := range specs {
					request := []Spec{first, second, third}
					plan, err := PlanRequest(size, request, Limits{}, testEstimator{})

					normalized := make([]Segment, 0, 3)
					unsatIndex := -1
					for index, spec := range request {
						segment, ok := oracleNormalize(spec, size)
						if !ok {
							unsatIndex = index
							break
						}
						normalized = append(normalized, segment)
					}
					if unsatIndex >= 0 {
						var unsatisfiable *UnsatisfiableError
						if !errors.As(err, &unsatisfiable) || unsatisfiable.Index != unsatIndex {
							t.Fatalf("size=%d: err=%v want unsatisfiable at %d", size, err, unsatIndex)
						}
						continue
					}
					if err != nil {
						t.Fatalf("size=%d: unexpected error %v", size, err)
					}

					wantSegments := []Segment{}
					seen := map[Segment]bool{}
					var wantDuplicates int64
					for _, segment := range normalized {
						if seen[segment] {
							wantDuplicates++
							continue
						}
						seen[segment] = true
						wantSegments = append(wantSegments, segment)
					}
					if !reflect.DeepEqual(plan.Segments, wantSegments) {
						t.Fatalf("triple segments=%+v want %+v", plan.Segments, wantSegments)
					}
					if plan.DuplicateParts != wantDuplicates {
						t.Fatalf("triple duplicates=%d want %d", plan.DuplicateParts, wantDuplicates)
					}
					wantReads := oracleMerge(wantSegments)
					if !reflect.DeepEqual(plan.Reads, wantReads) {
						t.Fatalf("triple reads=%+v want %+v", plan.Reads, wantReads)
					}
					if plan.WireBytes != expectedWire(wantSegments) {
						t.Fatalf("triple wire mismatch")
					}
				}
			}
		}
	}
}

func assertLimitsRejectAndAllow(t *testing.T, size int64, request []Spec, plan *Plan) {
	t.Helper()
	if len(plan.Segments) > 1 {
		if _, err := PlanRequest(size, request, Limits{MaxSegments: 1}, testEstimator{}); !errors.Is(err, ErrTooManySegments) {
			t.Fatalf("MaxSegments: got %v want %v", err, ErrTooManySegments)
		}
	}
	if plan.DataBytes > 1 {
		if _, err := PlanRequest(size, request, Limits{MaxDataBytes: plan.DataBytes - 1}, testEstimator{}); !errors.Is(err, ErrDataLimit) {
			t.Fatalf("MaxDataBytes: got %v want %v", err, ErrDataLimit)
		}
	}
	if plan.WireBytes > 1 {
		if _, err := PlanRequest(size, request, Limits{MaxWireBytes: plan.WireBytes - 1}, testEstimator{}); !errors.Is(err, ErrWireLimit) {
			t.Fatalf("MaxWireBytes: got %v want %v", err, ErrWireLimit)
		}
	}
	if len(plan.Reads) > 1 {
		if _, err := PlanRequest(size, request, Limits{MaxReads: 1}, testEstimator{}); !errors.Is(err, ErrReadLimit) {
			t.Fatalf("MaxReads: got %v want %v", err, ErrReadLimit)
		}
	}

	exact := Limits{
		MaxSegments:  int64(len(plan.Segments)),
		MaxDataBytes: plan.DataBytes,
		MaxWireBytes: plan.WireBytes,
		MaxReads:     int64(len(plan.Reads)),
	}
	if _, err := PlanRequest(size, request, exact, testEstimator{}); err != nil {
		t.Fatalf("exact-limit plan should pass: %v", err)
	}
}

// TestOverflowBudgetEnumeration pairs extreme-length segments with the
// multipart estimator to prove int64 overflow is reported, not wrapped.
func TestOverflowBudgetEnumeration(t *testing.T) {
	max := int64(math.MaxInt64)
	estimator := &MultipartEstimator{Boundary: "X"}

	huge := []Segment{
		{First: 0, Last: max - 1},
		{First: max - 1, Last: max - 1},
	}
	if _, err := estimator.Estimate(max, huge); !errors.Is(err, ErrOverflow) {
		t.Fatalf("estimator overflow: got %v", err)
	}

	// A single max-sized segment plus part and closing headers also overflows.
	if _, err := estimator.Estimate(max, []Segment{{First: 0, Last: max - 1}}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("single huge segment estimator: got %v", err)
	}

	// checkedAdd across every offset near the int64 ceiling.
	for delta := int64(0); delta < 4; delta++ {
		if _, ok := checkedAdd(max-delta, delta+1); ok {
			t.Fatalf("checkedAdd overflow undetected at delta %d", delta)
		}
	}

	// A plan whose estimator reports an overflowing value fails closed.
	failing := EstimatorFunc(func(size int64, segments []Segment) (int64, error) {
		return 0, ErrOverflow
	})
	if _, err := PlanRequest(10, []Spec{{Kind: Suffix, Suffix: 1}}, Limits{}, failing); !errors.Is(err, ErrOverflow) {
		t.Fatalf("plan must propagate estimator overflow: %v", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add("bytes=0-99")
	f.Add("bytes=-10")
	f.Add("bytes=100-")
	f.Add("items=0-1")
	f.Fuzz(func(t *testing.T, header string) {
		specs, err := Parse(header)
		if err != nil {
			return
		}
		if len(specs) == 0 {
			t.Fatal("successful parse returned no specs")
		}
		for _, spec := range specs {
			if !spec.valid() {
				t.Fatalf("parser produced invalid spec: %+v", spec)
			}
			if _, err := spec.Normalize(64); err == nil {
				continue
			} else if !errors.Is(err, ErrUnsatisfiable) {
				t.Fatalf("unexpected normalize error: %v", err)
			}
		}
	})
}
