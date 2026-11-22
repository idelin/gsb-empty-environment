package mtuinfer

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestPaddingAndTwoRouteInference(t *testing.T) {
	input := Input{
		Links: []LinkSpec{
			{ID: "A", MinMTU: 20, MaxMTU: 48},
			{ID: "B", MinMTU: 20, MaxMTU: 48},
		},
		Context: SendContext{MinPayloadAlignment: 2, Header: 1, OuterAlignment: 4},
		Routes: map[PathKey][]Route{
			{Generation: 1, Label: "edge"}: {
				{ID: "r-a", Segments: []RouteSegment{{LinkID: "A", Segment: Segment{Header: 3, InnerPadding: 1, OuterAlignment: 4}}}},
				{ID: "r-b", Segments: []RouteSegment{{LinkID: "B", Segment: Segment{Header: 2, InnerPadding: 0, OuterAlignment: 8}}}},
			},
		},
		Receipts: []Receipt{
			{Generation: 1, Label: "edge", Candidates: []string{"r-a", "r-b"}, SendLength: 20},
			{Generation: 1, Label: "edge", Candidates: []string{"r-b", "r-a"}, SendLength: 44, Failed: true},
		},
		Target: TargetRoute{Generation: 1, Label: "edge"},
	}

	result, err := Analyze(context.Background(), input)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	wantGuaranteed, wantPossible := bruteForce(t, input)
	if result.Guaranteed.Payload != wantGuaranteed {
		t.Fatalf("guaranteed payload = %d, want %d", result.Guaranteed.Payload, wantGuaranteed)
	}
	if result.Possible.Payload != wantPossible {
		t.Fatalf("possible payload = %d, want %d", result.Possible.Payload, wantPossible)
	}
	if err := VerifyResult(input, result); err != nil {
		t.Fatalf("VerifyResult returned error: %v", err)
	}
}

func TestSameLabelMultipleReceiptsShareRoute(t *testing.T) {
	input := Input{
		Links: []LinkSpec{
			{ID: "A", MinMTU: 10, MaxMTU: 30},
			{ID: "B", MinMTU: 10, MaxMTU: 30},
		},
		Context: SendContext{MinPayloadAlignment: 1, Header: 0, OuterAlignment: 1},
		Routes: map[PathKey][]Route{
			{Generation: 1, Label: "p"}: {
				{ID: "ra", Segments: []RouteSegment{{LinkID: "A", Segment: Segment{OuterAlignment: 1}}}},
				{ID: "rb", Segments: []RouteSegment{{LinkID: "B", Segment: Segment{OuterAlignment: 1}}}},
			},
		},
		Receipts: []Receipt{
			{Generation: 1, Label: "p", Candidates: []string{"ra", "rb"}, SendLength: 18},
			{Generation: 1, Label: "p", Candidates: []string{"ra", "rb"}, SendLength: 25, Failed: true},
		},
		Target: TargetRoute{Generation: 1, Label: "p"},
	}

	result, err := Analyze(context.Background(), input)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	wantGuaranteed, wantPossible := bruteForce(t, input)
	if result.Guaranteed.Payload != wantGuaranteed || result.Possible.Payload != wantPossible {
		t.Fatalf("payloads = (%d,%d), want (%d,%d)", result.Guaranteed.Payload, result.Possible.Payload, wantGuaranteed, wantPossible)
	}
	if err := VerifyResult(input, result); err != nil {
		t.Fatalf("VerifyResult returned error: %v", err)
	}
	if len(result.Guaranteed.UniversalProof.Assignments) != 2 {
		t.Fatalf("feasible assignments = %d, want 2", len(result.Guaranteed.UniversalProof.Assignments))
	}
}

func TestTwelveSegmentsPaddingAndFailureAttribution(t *testing.T) {
	links := make([]LinkSpec, 0, 12)
	segments := make([]RouteSegment, 0, 12)
	for index := 0; index < 12; index++ {
		id := string(rune('A' + index))
		links = append(links, LinkSpec{ID: id, MinMTU: 50, MaxMTU: 51})
		segments = append(segments, RouteSegment{
			LinkID: id,
			Segment: Segment{
				Header:         1 + index%3,
				InnerPadding:   index % 2,
				OuterAlignment: 2 + index%4,
			},
		})
	}
	input := Input{
		Links:   links,
		Context: SendContext{MinPayloadAlignment: 2, Header: 1, OuterAlignment: 4},
		Routes: map[PathKey][]Route{
			{Generation: 1, Label: "tunnel"}: {
				{ID: "long", Segments: segments},
			},
		},
		Receipts: []Receipt{
			{Generation: 1, Label: "tunnel", Candidates: []string{"long"}, SendLength: 0},
			{Generation: 1, Label: "tunnel", Candidates: []string{"long"}, SendLength: 10, Failed: true},
		},
		Target: TargetRoute{Generation: 1, Label: "tunnel"},
	}

	result, err := Analyze(context.Background(), input)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	wantGuaranteed, wantPossible := bruteForce(t, input)
	if result.Guaranteed.Payload != wantGuaranteed || result.Possible.Payload != wantPossible {
		t.Fatalf("payloads = (%d,%d), want (%d,%d)", result.Guaranteed.Payload, result.Possible.Payload, wantGuaranteed, wantPossible)
	}
	if len(result.Guaranteed.UniversalProof.Assignments[0].FailureReasons) != 1 {
		t.Fatalf("failure attestation count = %d, want 1", len(result.Guaranteed.UniversalProof.Assignments[0].FailureReasons))
	}
	if err := VerifyResult(input, result); err != nil {
		t.Fatalf("VerifyResult returned error: %v", err)
	}
}

func TestGenerationChangeOnlyReusesExplicitPhysicalLinks(t *testing.T) {
	input := Input{
		Links: []LinkSpec{
			{ID: "A", MinMTU: 10, MaxMTU: 30},
			{ID: "B", MinMTU: 10, MaxMTU: 30},
		},
		Context: SendContext{MinPayloadAlignment: 1, Header: 0, OuterAlignment: 1},
		Routes: map[PathKey][]Route{
			{Generation: 1, Label: "old"}: {
				{ID: "old-a", Segments: []RouteSegment{{LinkID: "A", Segment: Segment{OuterAlignment: 1}}}},
			},
			{Generation: 2, Label: "new"}: {
				{ID: "new-b", Segments: []RouteSegment{{LinkID: "B", Segment: Segment{OuterAlignment: 1}}}},
			},
		},
		Receipts: []Receipt{
			{Generation: 1, Label: "old", Candidates: []string{"old-a"}, SendLength: 22},
		},
		Target: TargetRoute{Generation: 2, Label: "new"},
	}

	result, err := Analyze(context.Background(), input)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	wantGuaranteed, wantPossible := bruteForce(t, input)
	if result.Guaranteed.Payload != wantGuaranteed || result.Possible.Payload != wantPossible {
		t.Fatalf("payloads = (%d,%d), want (%d,%d)", result.Guaranteed.Payload, result.Possible.Payload, wantGuaranteed, wantPossible)
	}
	if err := VerifyResult(input, result); err != nil {
		t.Fatalf("VerifyResult returned error: %v", err)
	}
	if got := result.Guaranteed.UniversalProof.Assignments[0].GuaranteedMTUs; !reflect.DeepEqual(got, []LinkBound{{LinkID: "A", Lower: 22, Upper: 30}, {LinkID: "B", Lower: 10, Upper: 30}}) {
		t.Fatalf("cross-generation certificate MTUs = %+v", got)
	}
}

func TestDuplicateNotificationsDoNotAddConstraints(t *testing.T) {
	base := Input{
		Links:   []LinkSpec{{ID: "A", MinMTU: 10, MaxMTU: 20}},
		Context: SendContext{MinPayloadAlignment: 1, Header: 0, OuterAlignment: 1},
		Target:  TargetRoute{Generation: 1, Label: "p"},
	}
	base.Routes = map[PathKey][]Route{
		{Generation: 1, Label: "p"}: {{ID: "r", Segments: []RouteSegment{{LinkID: "A", Segment: Segment{OuterAlignment: 1}}}}},
	}
	base.Receipts = []Receipt{
		{Generation: 1, Label: "p", Candidates: []string{"r"}, SendLength: 16, Failed: true},
	}
	withDuplicate := base
	withDuplicate.Receipts = append(withDuplicate.Receipts, base.Receipts[0], base.Receipts[0])

	first, err := Analyze(context.Background(), base)
	if err != nil {
		t.Fatalf("Analyze base returned error: %v", err)
	}
	second, err := Analyze(context.Background(), withDuplicate)
	if err != nil {
		t.Fatalf("Analyze duplicates returned error: %v", err)
	}
	if first.Guaranteed.Payload != second.Guaranteed.Payload || first.Possible.Payload != second.Possible.Payload {
		t.Fatalf("duplicate receipt changed payloads from (%d,%d) to (%d,%d)", first.Guaranteed.Payload, first.Possible.Payload, second.Guaranteed.Payload, second.Possible.Payload)
	}
	if err := VerifyResult(withDuplicate, second); err != nil {
		t.Fatalf("VerifyResult returned error: %v", err)
	}
}

func TestContradictionIsNotZeroMTU(t *testing.T) {
	input := Input{
		Links:   []LinkSpec{{ID: "A", MinMTU: 10, MaxMTU: 14}},
		Context: SendContext{MinPayloadAlignment: 1, Header: 0, OuterAlignment: 1},
		Target:  TargetRoute{Generation: 1, Label: "p"},
		Routes: map[PathKey][]Route{
			{Generation: 1, Label: "p"}: {{ID: "r", Segments: []RouteSegment{{LinkID: "A", Segment: Segment{OuterAlignment: 1}}}}},
		},
		Receipts: []Receipt{
			{Generation: 1, Label: "p", Candidates: []string{"r"}, SendLength: 18},
			{Generation: 1, Label: "p", Candidates: []string{"r"}, SendLength: 18, Failed: true},
		},
	}

	result, err := Analyze(context.Background(), input)
	if !errors.Is(err, ErrInconsistentEvidence) {
		t.Fatalf("error = %v, want ErrInconsistentEvidence", err)
	}
	if result.Compatible || result.Guaranteed.Payload != 0 || result.Possible.Payload != 0 {
		t.Fatalf("contradiction produced payload result: %+v", result)
	}
}

func TestZeroWireFailureIsInconsistentNotNegativeMTU(t *testing.T) {
	input := Input{
		Links:   []LinkSpec{{ID: "A", MinMTU: 0, MaxMTU: 2}},
		Context: SendContext{MinPayloadAlignment: 1, Header: 0, OuterAlignment: 1},
		Target:  TargetRoute{Generation: 1, Label: "p"},
		Routes: map[PathKey][]Route{
			{Generation: 1, Label: "p"}: {{ID: "r", Segments: []RouteSegment{{LinkID: "A", Segment: Segment{OuterAlignment: 1}}}}},
		},
		Receipts: []Receipt{{Generation: 1, Label: "p", Candidates: []string{"r"}, SendLength: 0, Failed: true}},
	}
	result, err := Analyze(context.Background(), input)
	if !errors.Is(err, ErrInconsistentEvidence) {
		t.Fatalf("error = %v, want ErrInconsistentEvidence", err)
	}
	if result.Compatible {
		t.Fatalf("zero-wire failure unexpectedly produced a compatible result: %+v", result)
	}
}

func TestZeroPayloadCompatibleWhenWireFitsZeroMTU(t *testing.T) {
	input := Input{
		Links:   []LinkSpec{{ID: "A", MinMTU: 0, MaxMTU: 0}},
		Context: SendContext{MinPayloadAlignment: 1, Header: 0, OuterAlignment: 1},
		Target:  TargetRoute{Generation: 1, Label: "p"},
		Routes: map[PathKey][]Route{
			{Generation: 1, Label: "p"}: {{ID: "r", Segments: []RouteSegment{{LinkID: "A", Segment: Segment{OuterAlignment: 1}}}}},
		},
		Receipts: []Receipt{{Generation: 1, Label: "p", Candidates: []string{"r"}, SendLength: 0}},
	}
	result, err := Analyze(context.Background(), input)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if result.Guaranteed.Payload != 0 || result.Possible.Payload != 0 {
		t.Fatalf("payloads = (%d,%d), want (0,0)", result.Guaranteed.Payload, result.Possible.Payload)
	}
	if err := VerifyResult(input, result); err != nil {
		t.Fatalf("VerifyResult returned error: %v", err)
	}
}

func TestInvalidCandidateSet(t *testing.T) {
	input := Input{
		Links:   []LinkSpec{{ID: "A", MinMTU: 1, MaxMTU: 10}},
		Context: SendContext{MinPayloadAlignment: 1, Header: 0, OuterAlignment: 1},
		Target:  TargetRoute{Generation: 1, Label: "p"},
		Routes: map[PathKey][]Route{
			{Generation: 1, Label: "p"}: {{ID: "r", Segments: []RouteSegment{{LinkID: "A", Segment: Segment{OuterAlignment: 1}}}}},
		},
		Receipts: []Receipt{{Generation: 1, Label: "p", Candidates: []string{"other"}, SendLength: 1}},
	}
	if _, err := Analyze(context.Background(), input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}
