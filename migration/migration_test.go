package migration

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func testSigner(t *testing.T) *MemorySigner {
	t.Helper()
	signer := NewMemorySigner()
	for _, keyID := range []string{"old-a", "old-b", "new-device"} {
		if err := signer.GenerateKey(keyID); err != nil {
			t.Fatalf("generate key %q: %v", keyID, err)
		}
	}
	return signer
}

func signSegment(t *testing.T, signer *MemorySigner, segment Segment) Segment {
	t.Helper()
	segment.Signature = Signature{KeyID: segment.OldDeviceID}
	signed, err := signer.SignSegment(segment)
	if err != nil {
		t.Fatalf("sign segment: %v", err)
	}
	return signed
}

func signReceipt(t *testing.T, signer *MemorySigner, receipt Receipt) Receipt {
	t.Helper()
	receipt.Signature = Signature{KeyID: receipt.DeviceID}
	signed, err := signer.SignReceipt(receipt)
	if err != nil {
		t.Fatalf("sign receipt: %v", err)
	}
	return signed
}

func baseInput() Input {
	return Input{
		NewDeviceID: "new-device",
		Domain:      Interval{Start: 0, End: 199},
	}
}

func TestReverseSegmentMapsIntervalsAndVerifies(t *testing.T) {
	signer := testSigner(t)
	input := baseInput()
	input.Segments = []Segment{
		signSegment(t, signer, Segment{
			ID:          "reverse",
			OldDeviceID: "old-a",
			NewDeviceID: "new-device",
			Old:         Interval{Start: 10, End: 19},
			New:         Interval{Start: 100, End: 109},
			Orientation: Reverse,
		}),
	}
	input.Receipts = []Receipt{
		signReceipt(t, signer, Receipt{
			RequestID: "req-1",
			Side:      OldSide,
			DeviceID:  "old-a",
			Steps:     Interval{Start: 12, End: 14},
		}),
	}

	cert, err := BuildCertificate(input, signer)
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	if !cert.Acceptable() {
		t.Fatalf("certificate is not acceptable: %+v", cert.Findings)
	}
	if ok, findings := VerifyCertificate(cert, signer); !ok {
		t.Fatalf("certificate verification failed: %+v", findings)
	}

	wantUnknown := RangeSet{Intervals: []Interval{{Start: 105, End: 107}}}
	if !cert.Unknown.Equal(wantUnknown) {
		t.Fatalf("reverse unknown = %+v, want %+v", cert.Unknown, wantUnknown)
	}
	wantSafe := NewRangeSet([]Interval{{Start: 0, End: 104}, {Start: 108, End: 199}})
	if !cert.Safe.Equal(wantSafe) {
		t.Fatalf("reverse safe = %+v, want %+v", cert.Safe, wantSafe)
	}
	if !cert.Consumed.IsEmpty() {
		t.Fatalf("reverse consumed = %+v, want empty", cert.Consumed)
	}
}

func TestTwoOldDevicesOverlappingNewRangeIsBlocked(t *testing.T) {
	signer := testSigner(t)
	input := baseInput()
	input.Segments = []Segment{
		signSegment(t, signer, Segment{
			ID: "a", OldDeviceID: "old-a", NewDeviceID: "new-device",
			Old: Interval{Start: 0, End: 49}, New: Interval{Start: 50, End: 99}, Orientation: Forward,
		}),
		signSegment(t, signer, Segment{
			ID: "b", OldDeviceID: "old-b", NewDeviceID: "new-device",
			Old: Interval{Start: 100, End: 149}, New: Interval{Start: 70, End: 119}, Orientation: Forward,
		}),
	}

	cert, err := BuildCertificate(input, signer)
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	if cert.Acceptable() {
		t.Fatal("overlapping active segments must not be acceptable")
	}
	wantConflicts := RangeSet{Intervals: []Interval{{Start: 70, End: 99}}}
	if !cert.Conflicts.Equal(wantConflicts) {
		t.Fatalf("conflicts = %+v, want %+v", cert.Conflicts, wantConflicts)
	}
	if !cert.Blocked.Equal(wantConflicts) {
		t.Fatalf("blocked = %+v, want %+v", cert.Blocked, wantConflicts)
	}

	usableA := RangeSet{Intervals: []Interval{{Start: 50, End: 69}}}
	usableB := RangeSet{Intervals: []Interval{{Start: 100, End: 119}}}
	if got := mappingNewRanges(cert.Mapping, "a"); !got.Equal(usableA) {
		t.Fatalf("segment a usable = %+v, want %+v", got, usableA)
	}
	if got := mappingNewRanges(cert.Mapping, "b"); !got.Equal(usableB) {
		t.Fatalf("segment b usable = %+v, want %+v", got, usableB)
	}
	if ok, _ := VerifyCertificate(cert, signer); ok {
		t.Fatal("verifier must reject an unacceptable certificate")
	}
}

func TestRevokedSegmentNoLongerOccupiesNewRange(t *testing.T) {
	signer := testSigner(t)
	input := baseInput()
	input.Segments = []Segment{
		signSegment(t, signer, Segment{
			ID: "a", OldDeviceID: "old-a", NewDeviceID: "new-device",
			Old: Interval{Start: 0, End: 49}, New: Interval{Start: 50, End: 99}, Orientation: Forward,
		}),
		signSegment(t, signer, Segment{
			ID: "b", OldDeviceID: "old-b", NewDeviceID: "new-device",
			Old: Interval{Start: 100, End: 149}, New: Interval{Start: 70, End: 119}, Orientation: Forward,
		}),
	}
	input.RevokedSegmentIDs = []string{"b"}

	cert, err := BuildCertificate(input, signer)
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	if !cert.Acceptable() {
		t.Fatalf("revoked overlap should be acceptable: %+v", cert.Findings)
	}
	if !cert.Conflicts.IsEmpty() || !cert.Blocked.IsEmpty() {
		t.Fatalf("conflicts=%+v blocked=%+v, want empty", cert.Conflicts, cert.Blocked)
	}
	wantCoverage := RangeSet{Intervals: []Interval{{Start: 50, End: 99}}}
	if !cert.Coverage.Equal(wantCoverage) {
		t.Fatalf("coverage = %+v, want %+v", cert.Coverage, wantCoverage)
	}
	if ok, findings := VerifyCertificate(cert, signer); !ok {
		t.Fatalf("verification failed: %+v", findings)
	}
}

func TestNoReceiptsMarksDomainSafe(t *testing.T) {
	signer := testSigner(t)
	input := baseInput()
	input.Segments = []Segment{
		signSegment(t, signer, Segment{
			ID: "forward", OldDeviceID: "old-a", NewDeviceID: "new-device",
			Old: Interval{Start: 0, End: 99}, New: Interval{Start: 100, End: 199}, Orientation: Forward,
		}),
	}
	cert, err := BuildCertificate(input, signer)
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	if !cert.Acceptable() {
		t.Fatalf("findings: %+v", cert.Findings)
	}
	wantSafe := RangeSet{Intervals: []Interval{input.Domain}}
	if !cert.Safe.Equal(wantSafe) {
		t.Fatalf("safe = %+v, want %+v", cert.Safe, wantSafe)
	}
	if ok, findings := VerifyCertificate(cert, signer); !ok {
		t.Fatalf("verification failed: %+v", findings)
	}
}

func TestMissingMigrationSignatureRejectsSegment(t *testing.T) {
	signer := testSigner(t)
	input := baseInput()
	input.Segments = []Segment{{
		ID: "unsigned", OldDeviceID: "old-a", NewDeviceID: "new-device",
		Old: Interval{Start: 0, End: 9}, New: Interval{Start: 90, End: 99}, Orientation: Forward,
	}}

	cert, err := BuildCertificate(input, signer)
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	if cert.Acceptable() || cert.Status != StatusInvalid {
		t.Fatalf("unsigned segment must invalidate certificate, status=%s", cert.Status)
	}
	if !containsCode(cert.Findings, "segment_signature_invalid") {
		t.Fatalf("findings = %+v, want segment_signature_invalid", cert.Findings)
	}
	if len(cert.Mapping) != 0 {
		t.Fatalf("mapping = %+v, want no usable mapping", cert.Mapping)
	}
	if !cert.Safe.IsEmpty() {
		t.Fatalf("safe = %+v, want empty for invalid certificate", cert.Safe)
	}
	if ok, _ := VerifyCertificate(cert, signer); ok {
		t.Fatal("verifier must not accept an invalid certificate")
	}
}

func TestMalformedSegmentIsRejected(t *testing.T) {
	signer := testSigner(t)
	input := baseInput()
	input.Segments = []Segment{
		signSegment(t, signer, Segment{
			ID: "bad-orientation", OldDeviceID: "old-a", NewDeviceID: "new-device",
			Old: Interval{Start: 0, End: 9}, New: Interval{Start: 90, End: 99}, Orientation: "sideways",
		}),
	}
	cert, err := BuildCertificate(input, signer)
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	if cert.Acceptable() {
		t.Fatal("malformed orientation must invalidate certificate")
	}
	if !containsCode(cert.Findings, "segment_orientation_invalid") {
		t.Fatalf("findings = %+v, want segment_orientation_invalid", cert.Findings)
	}
	if len(cert.Mapping) != 0 {
		t.Fatalf("mapping = %+v, want empty", cert.Mapping)
	}
	if !cert.Safe.IsEmpty() {
		t.Fatalf("safe = %+v, want empty for invalid certificate", cert.Safe)
	}
}

func containsCode(findings []Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func TestSameRequestRetryIsCountedOnceAndCanBecomeConsumed(t *testing.T) {
	signer := testSigner(t)
	input := baseInput()
	input.Segments = []Segment{
		signSegment(t, signer, Segment{
			ID: "forward", OldDeviceID: "old-a", NewDeviceID: "new-device",
			Old: Interval{Start: 0, End: 99}, New: Interval{Start: 100, End: 199}, Orientation: Forward,
		}),
	}
	input.Receipts = []Receipt{
		signReceipt(t, signer, Receipt{RequestID: "req-retry", Side: OldSide, DeviceID: "old-a", Steps: Interval{Start: 10, End: 10}}),
		signReceipt(t, signer, Receipt{RequestID: "req-retry", Side: OldSide, DeviceID: "old-a", Steps: Interval{Start: 10, End: 11}}),
		signReceipt(t, signer, Receipt{RequestID: "req-retry", Side: NewSide, DeviceID: "new-device", Steps: Interval{Start: 110, End: 110}}),
	}

	cert, err := BuildCertificate(input, signer)
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	if !cert.Acceptable() {
		t.Fatalf("certificate findings: %+v", cert.Findings)
	}
	if len(cert.Requests) != 1 || cert.Requests[0].ReceiptCount != 3 {
		t.Fatalf("requests = %+v, want one request with three receipt records", cert.Requests)
	}
	wantConsumed := RangeSet{Intervals: []Interval{{Start: 110, End: 110}}}
	if !cert.Consumed.Equal(wantConsumed) {
		t.Fatalf("consumed = %+v, want %+v", cert.Consumed, wantConsumed)
	}
	if !cert.Unknown.IsEmpty() {
		t.Fatalf("unknown = %+v, want empty", cert.Unknown)
	}
}

func TestGlobalCompatibilityDeterminesConsumedAndUnknown(t *testing.T) {
	signer := testSigner(t)
	input := baseInput()
	input.Segments = []Segment{
		signSegment(t, signer, Segment{
			ID: "forward", OldDeviceID: "old-a", NewDeviceID: "new-device",
			Old: Interval{Start: 0, End: 99}, New: Interval{Start: 100, End: 199}, Orientation: Forward,
		}),
	}
	input.Receipts = []Receipt{
		signReceipt(t, signer, Receipt{RequestID: "req-a", Side: NewSide, DeviceID: "new-device", Steps: Interval{Start: 100, End: 101}}),
		signReceipt(t, signer, Receipt{RequestID: "req-b", Side: NewSide, DeviceID: "new-device", Steps: Interval{Start: 101, End: 101}}),
	}

	cert, err := BuildCertificate(input, signer)
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	if !cert.Acceptable() {
		t.Fatalf("certificate findings: %+v", cert.Findings)
	}
	wantConsumed := RangeSet{Intervals: []Interval{{Start: 100, End: 101}}}
	if !cert.Consumed.Equal(wantConsumed) {
		t.Fatalf("consumed = %+v, want %+v", cert.Consumed, wantConsumed)
	}
	if !cert.Unknown.IsEmpty() {
		t.Fatalf("unknown = %+v, want empty", cert.Unknown)
	}
	wantSafe := NewRangeSet([]Interval{{Start: 0, End: 99}, {Start: 102, End: 199}})
	if !cert.Safe.Equal(wantSafe) {
		t.Fatalf("safe = %+v, want %+v", cert.Safe, wantSafe)
	}
	if ok, findings := VerifyCertificate(cert, signer); !ok {
		t.Fatalf("verification failed: %+v", findings)
	}
}

func TestGlobalConstraintSeparatesConsumedFromUnknown(t *testing.T) {
	signer := testSigner(t)
	input := baseInput()
	input.Segments = []Segment{
		signSegment(t, signer, Segment{
			ID: "forward", OldDeviceID: "old-a", NewDeviceID: "new-device",
			Old: Interval{Start: 0, End: 99}, New: Interval{Start: 100, End: 199}, Orientation: Forward,
		}),
	}
	input.Receipts = []Receipt{
		signReceipt(t, signer, Receipt{RequestID: "req-a", Side: NewSide, DeviceID: "new-device", Steps: Interval{Start: 100, End: 102}}),
		signReceipt(t, signer, Receipt{RequestID: "req-b", Side: NewSide, DeviceID: "new-device", Steps: Interval{Start: 101, End: 101}}),
	}

	cert, err := BuildCertificate(input, signer)
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	if !cert.Acceptable() {
		t.Fatalf("certificate findings: %+v", cert.Findings)
	}
	wantConsumed := RangeSet{Intervals: []Interval{{Start: 101, End: 101}}}
	if !cert.Consumed.Equal(wantConsumed) {
		t.Fatalf("consumed = %+v, want %+v", cert.Consumed, wantConsumed)
	}
	wantUnknown := RangeSet{Intervals: []Interval{{Start: 100, End: 100}, {Start: 102, End: 102}}}
	if !cert.Unknown.Equal(wantUnknown) {
		t.Fatalf("unknown = %+v, want %+v", cert.Unknown, wantUnknown)
	}
}

func TestTwoRequestsInThreeStepCellRemainUnknown(t *testing.T) {
	signer := testSigner(t)
	input := baseInput()
	input.Segments = []Segment{
		signSegment(t, signer, Segment{
			ID: "forward", OldDeviceID: "old-a", NewDeviceID: "new-device",
			Old: Interval{Start: 0, End: 99}, New: Interval{Start: 100, End: 199}, Orientation: Forward,
		}),
	}
	input.Receipts = []Receipt{
		signReceipt(t, signer, Receipt{RequestID: "req-a", Side: NewSide, DeviceID: "new-device", Steps: Interval{Start: 100, End: 102}}),
		signReceipt(t, signer, Receipt{RequestID: "req-b", Side: NewSide, DeviceID: "new-device", Steps: Interval{Start: 100, End: 102}}),
	}

	cert, err := BuildCertificate(input, signer)
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	wantUnknown := RangeSet{Intervals: []Interval{{Start: 100, End: 102}}}
	if !cert.Unknown.Equal(wantUnknown) {
		t.Fatalf("unknown = %+v, want %+v", cert.Unknown, wantUnknown)
	}
	if !cert.Consumed.IsEmpty() {
		t.Fatalf("consumed = %+v, want empty", cert.Consumed)
	}
}

func TestLaterReceiptNarrowsOnlyLocalUnknownInterval(t *testing.T) {
	signer := testSigner(t)
	segment := signSegment(t, signer, Segment{
		ID: "forward", OldDeviceID: "old-a", NewDeviceID: "new-device",
		Old: Interval{Start: 0, End: 99}, New: Interval{Start: 100, End: 199}, Orientation: Forward,
	})

	first := baseInput()
	first.Segments = []Segment{segment}
	first.Receipts = []Receipt{
		signReceipt(t, signer, Receipt{RequestID: "req", Side: OldSide, DeviceID: "old-a", Steps: Interval{Start: 10, End: 19}}),
	}
	before, err := BuildCertificate(first, signer)
	if err != nil {
		t.Fatalf("build first certificate: %v", err)
	}
	wantBefore := RangeSet{Intervals: []Interval{{Start: 110, End: 119}}}
	if !before.Unknown.Equal(wantBefore) {
		t.Fatalf("before unknown = %+v, want %+v", before.Unknown, wantBefore)
	}

	second := first
	second.Receipts = append(append([]Receipt(nil), first.Receipts...),
		signReceipt(t, signer, Receipt{RequestID: "req", Side: NewSide, DeviceID: "new-device", Steps: Interval{Start: 114, End: 116}}))
	after, err := BuildCertificate(second, signer)
	if err != nil {
		t.Fatalf("build second certificate: %v", err)
	}
	wantAfter := RangeSet{Intervals: []Interval{{Start: 114, End: 116}}}
	if !after.Unknown.Equal(wantAfter) {
		t.Fatalf("after unknown = %+v, want %+v", after.Unknown, wantAfter)
	}
	wantSafe := NewRangeSet([]Interval{{Start: 0, End: 113}, {Start: 117, End: 199}})
	if !after.Safe.Equal(wantSafe) {
		t.Fatalf("after safe = %+v, want %+v", after.Safe, wantSafe)
	}
	if ok, findings := VerifyCertificate(after, signer); !ok {
		t.Fatalf("verification failed: %+v", findings)
	}
}

func TestLargeIntervalsAreHandledWithoutStepExpansion(t *testing.T) {
	signer := testSigner(t)
	maxStep := Step(math.MaxUint64)
	input := Input{
		NewDeviceID: "new-device",
		Domain:      Interval{Start: 0, End: maxStep},
		Segments: []Segment{
			signSegment(t, signer, Segment{
				ID: "full", OldDeviceID: "old-a", NewDeviceID: "new-device",
				Old: Interval{Start: 0, End: maxStep}, New: Interval{Start: 0, End: maxStep}, Orientation: Reverse,
			}),
		},
		Receipts: []Receipt{
			signReceipt(t, signer, Receipt{RequestID: "req", Side: OldSide, DeviceID: "old-a", Steps: Interval{Start: 1, End: 2}}),
		},
	}

	cert, err := BuildCertificate(input, signer)
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	if !cert.Acceptable() {
		t.Fatalf("findings: %+v", cert.Findings)
	}
	wantUnknown := RangeSet{Intervals: []Interval{{Start: maxStep - 2, End: maxStep - 1}}}
	if !cert.Unknown.Equal(wantUnknown) {
		t.Fatalf("unknown = %+v, want %+v", cert.Unknown, wantUnknown)
	}
	wantSafe := NewRangeSet([]Interval{{Start: 0, End: maxStep - 3}, {Start: maxStep, End: maxStep}})
	if !cert.Safe.Equal(wantSafe) {
		t.Fatalf("safe = %+v, want %+v", cert.Safe, wantSafe)
	}
	if ok, findings := VerifyCertificate(cert, signer); !ok {
		t.Fatalf("verification failed: %+v", findings)
	}
	if cert.Unknown.Length().String() != "2" {
		t.Fatalf("unknown length = %s, want 2", cert.Unknown.Length())
	}
}

func TestTamperedCertificateIsRejected(t *testing.T) {
	signer := testSigner(t)
	input := baseInput()
	input.Segments = []Segment{
		signSegment(t, signer, Segment{
			ID: "forward", OldDeviceID: "old-a", NewDeviceID: "new-device",
			Old: Interval{Start: 0, End: 9}, New: Interval{Start: 100, End: 109}, Orientation: Forward,
		}),
	}
	input.Receipts = []Receipt{
		signReceipt(t, signer, Receipt{RequestID: "req", Side: OldSide, DeviceID: "old-a", Steps: Interval{Start: 1, End: 3}}),
	}
	cert, err := BuildCertificate(input, signer)
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	cert.Safe = RangeSet{Intervals: []Interval{{Start: 0, End: 199}}}
	if ok, _ := VerifyCertificate(cert, signer); ok {
		t.Fatal("tampered safe range must be rejected")
	}
}

func TestCertificateSerializesIntervalDecomposition(t *testing.T) {
	signer := testSigner(t)
	input := baseInput()
	input.Segments = []Segment{
		signSegment(t, signer, Segment{
			ID: "forward", OldDeviceID: "old-a", NewDeviceID: "new-device",
			Old: Interval{Start: 0, End: 9}, New: Interval{Start: 100, End: 109}, Orientation: Forward,
		}),
	}
	input.Receipts = []Receipt{
		signReceipt(t, signer, Receipt{RequestID: "req", Side: OldSide, DeviceID: "old-a", Steps: Interval{Start: 1, End: 3}}),
	}
	cert, err := BuildCertificate(input, signer)
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	encoded, err := json.Marshal(cert)
	if err != nil {
		t.Fatalf("marshal certificate: %v", err)
	}
	text := string(encoded)
	for _, fragment := range []string{`"consumed"`, `"unknown"`, `"safe"`, `"intervals"`, `"requestId"`, `"segmentId"`} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("serialized certificate missing %s: %s", fragment, text)
		}
	}
	var decoded Certificate
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal certificate: %v", err)
	}
	if ok, findings := VerifyCertificate(decoded, signer); !ok {
		t.Fatalf("decoded certificate verification failed: %+v", findings)
	}
}

func mappingNewRanges(mapping []MappingClaim, segmentID string) RangeSet {
	intervals := make([]Interval, 0)
	for _, claim := range mapping {
		if claim.SegmentID == segmentID {
			intervals = append(intervals, claim.New)
		}
	}
	return NewRangeSet(intervals)
}
