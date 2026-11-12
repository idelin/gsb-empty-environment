package netsec

import (
	"errors"
	"testing"
)

func clockPtr(name string, min, max int64) *Clock {
	return &Clock{Name: name, MinOffset: min, MaxOffset: max}
}

// TestHalfOpenBoundary checks the include-lower / exclude-upper semantics at
// the exact window edges under a single-clock domain.
func TestHalfOpenBoundary(t *testing.T) {
	client := clockPtr("client", -1, 2)
	server := clockPtr("server", 0, 0)

	// Window [100,102) in client readings; server "now" is 100, so true now
	// is 100 and the client reading is 100 + o_c. The half-open upper bound
	// therefore excludes o_c = 2, leaving feasible offsets {0, 1}.
	report, err := CheckCredential(Credential{
		ClientClock: client,
		ServerClock: server,
		NotBefore:   100,
		NotAfter:    102,
		ServerNow:   100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Verdict != VerdictResync {
		t.Fatalf("verdict = %s, want resync", report.Verdict)
	}
	if report.TotalCombinations != 4 || report.ValidCombinations != 2 {
		t.Fatalf("combinations = %d/%d, want 2/4", report.ValidCombinations, report.TotalCombinations)
	}
	wantOffsets := []int64{0, 1}
	got := calibrationFor(t, report, client).FeasibleOffsets
	if len(got) != len(wantOffsets) {
		t.Fatalf("client calibration = %v, want %v", got, wantOffsets)
	}
	for i := range wantOffsets {
		if got[i] != wantOffsets[i] {
			t.Fatalf("client calibration = %v, want %v", got, wantOffsets)
		}
	}
}

func calibrationFor(t *testing.T, report *Report, clock *Clock) Calibration {
	t.Helper()
	for _, calibration := range report.Calibration {
		if calibration.Clock == clock {
			return calibration
		}
	}
	t.Fatalf("no calibration entry for clock %q", clock.Name)
	return Calibration{}
}

func TestAcceptWhenAllOffsetsValid(t *testing.T) {
	client := clockPtr("client", -2, 2)
	server := clockPtr("server", -1, 1)

	report, err := CheckCredential(Credential{
		ClientClock: client,
		ServerClock: server,
		NotBefore:   90,
		NotAfter:    110,
		ServerNow:   100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Verdict != VerdictAccept {
		t.Fatalf("verdict = %s, want accept", report.Verdict)
	}
	if report.ValidCombinations != report.TotalCombinations || report.TotalCombinations != 15 {
		t.Fatalf("combinations = %d/%d, want 15/15", report.ValidCombinations, report.TotalCombinations)
	}
	if report.Calibration != nil {
		t.Fatalf("accept must not carry calibration, got %+v", report.Calibration)
	}
}

func TestRejectWhenNoOffsetValid(t *testing.T) {
	client := clockPtr("client", -1, 1)
	server := clockPtr("server", 0, 0)

	report, err := CheckCredential(Credential{
		ClientClock: client,
		ServerClock: server,
		NotBefore:   200,
		NotAfter:    210,
		ServerNow:   100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Verdict != VerdictReject {
		t.Fatalf("verdict = %s, want reject", report.Verdict)
	}
	if report.ValidCombinations != 0 || report.TotalCombinations != 3 {
		t.Fatalf("combinations = %d/%d, want 0/3", report.ValidCombinations, report.TotalCombinations)
	}
	if report.Calibration != nil {
		t.Fatalf("reject must not carry calibration, got %+v", report.Calibration)
	}
}

// TestSharedClockJointConstraint proves that two windows on one clock share a
// single offset variable: feasibility is the intersection of the per-window
// offset sets, never the union.
func TestSharedClockJointConstraint(t *testing.T) {
	shared := clockPtr("shared", -2, 2)
	server := clockPtr("server", 0, 0)

	problem := &Problem{
		Clocks: []*Clock{shared, server},
		Now:    Instant{Clock: server, Reading: 100},
		Windows: []Window{
			// true now = 100; shared reading = 100 + o.
			{NotBefore: Bound{shared, 99}, NotAfter: Bound{shared, 101}},  // o in {-1, 0}
			{NotBefore: Bound{shared, 100}, NotAfter: Bound{shared, 102}}, // o in {0, 1}
		},
	}
	report, err := problem.Evaluate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Verdict != VerdictResync {
		t.Fatalf("verdict = %s, want resync", report.Verdict)
	}
	if report.TotalCombinations != 5 || report.ValidCombinations != 1 {
		t.Fatalf("combinations = %d/%d, want 1/5 (joint intersection, not union)",
			report.ValidCombinations, report.TotalCombinations)
	}

	var sharedCal *Calibration
	for i := range report.Calibration {
		if report.Calibration[i].Clock == shared {
			sharedCal = &report.Calibration[i]
		}
	}
	if sharedCal == nil {
		t.Fatal("missing calibration for shared clock")
	}
	if len(sharedCal.FeasibleOffsets) != 1 || sharedCal.FeasibleOffsets[0] != 0 {
		t.Fatalf("shared feasible offsets = %v, want [0]", sharedCal.FeasibleOffsets)
	}
}

func TestContradictoryClockSync(t *testing.T) {
	bad := clockPtr("bad", 5, 2) // Min > Max: contradictory calibration data
	server := clockPtr("server", 0, 0)

	_, err := CheckCredential(Credential{
		ClientClock: bad,
		ServerClock: server,
		NotBefore:   1,
		NotAfter:    2,
		ServerNow:   1,
	})
	if !errors.Is(err, ErrInfeasibleOffsets) {
		t.Fatalf("err = %v, want ErrInfeasibleOffsets", err)
	}
}

func TestInvalidWindowAndUnknownClock(t *testing.T) {
	client := clockPtr("client", 0, 0)
	server := clockPtr("server", 0, 0)
	other := clockPtr("other", 0, 0)

	if _, err := CheckCredential(Credential{
		ClientClock: client,
		ServerClock: server,
		NotBefore:   10,
		NotAfter:    10, // empty half-open interval
		ServerNow:   10,
	}); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("empty window err = %v, want ErrInvalidWindow", err)
	}

	problem := &Problem{
		Clocks: []*Clock{server},
		Now:    Instant{Clock: server, Reading: 100},
		Windows: []Window{{
			NotBefore: Bound{Clock: other, Reading: 1},
			NotAfter:  Bound{Clock: other, Reading: 2},
		}},
	}
	if _, err := problem.Evaluate(); !errors.Is(err, ErrUnknownClock) {
		t.Fatalf("unknown clock err = %v, want ErrUnknownClock", err)
	}
}

func TestDomainTooLarge(t *testing.T) {
	client := clockPtr("client", 0, 999) // 1000 offsets
	server := clockPtr("server", 0, 999) // 1000 offsets -> 1,000,000 (allowed)

	report, err := CheckCredential(Credential{
		ClientClock: client,
		ServerClock: server,
		NotBefore:   0,
		NotAfter:    1_000_000,
		ServerNow:   500_000,
	})
	if err != nil {
		t.Fatalf("1e6 combinations should be allowed, got %v", err)
	}
	if report.TotalCombinations != 1_000_000 {
		t.Fatalf("total = %d, want 1000000", report.TotalCombinations)
	}

	client.MaxOffset = 1000 // 1001 * 1000 > limit
	if _, err := CheckCredential(Credential{
		ClientClock: client,
		ServerClock: server,
		NotBefore:   0,
		NotAfter:    1_000_000,
		ServerNow:   500_000,
	}); !errors.Is(err, ErrDomainTooLarge) {
		t.Fatalf("oversized domain err = %v, want ErrDomainTooLarge", err)
	}
}

// TestThreeClockEnumeration exercises the Cartesian-product enumeration and
// the shared-event semantics across three independent clock domains.
func TestThreeClockEnumeration(t *testing.T) {
	clientA := clockPtr("a", 0, 1)
	clientB := clockPtr("b", -1, 0)
	server := clockPtr("s", 0, 0)

	problem := &Problem{
		Clocks: []*Clock{clientA, clientB, server},
		Now:    Instant{Clock: server, Reading: 100}, // true now = 100
		Windows: []Window{
			// reading = 100 + o_a; [99,102) -> o_a in {-1,0,1} intersect {0,1}.
			{NotBefore: Bound{clientA, 99}, NotAfter: Bound{clientA, 102}},
			// reading = 100 + o_b; [100,101) -> o_b in {0} intersect {-1,0}.
			{NotBefore: Bound{clientB, 100}, NotAfter: Bound{clientB, 101}},
		},
	}
	report, err := problem.Evaluate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalCombinations != 4 {
		t.Fatalf("total = %d, want 4", report.TotalCombinations)
	}
	// o_a in {0,1} (2 choices), o_b must be 0 (1 choice) -> 2 valid.
	if report.ValidCombinations != 2 {
		t.Fatalf("valid = %d, want 2", report.ValidCombinations)
	}
	if report.Verdict != VerdictResync {
		t.Fatalf("verdict = %s, want resync", report.Verdict)
	}
}

// TestAnalyzeSkewMatchesEnumeration cross-checks the interval arithmetic of
// AnalyzeSkew against the exhaustive CheckCredential verdict on many inputs.
func TestAnalyzeSkewMatchesEnumeration(t *testing.T) {
	cases := []Credential{
		{clockPtr("c", -2, 2), clockPtr("s", -1, 1), 90, 110, 100},
		{clockPtr("c", -1, 1), clockPtr("s", 0, 0), 100, 102, 100},
		{clockPtr("c", -1, 1), clockPtr("s", 0, 0), 200, 210, 100},
		{clockPtr("c", -5, -3), clockPtr("s", 1, 3), 0, 50, 100},
		{clockPtr("c", 0, 0), clockPtr("s", 0, 0), 100, 101, 100},
		{clockPtr("c", 0, 0), clockPtr("s", 0, 0), 101, 102, 100},
	}
	for i, cred := range cases {
		enumReport, err := CheckCredential(cred)
		if err != nil {
			t.Fatalf("case %d: enumeration error: %v", i, err)
		}
		skewReport, err := AnalyzeSkew(cred)
		if err != nil {
			t.Fatalf("case %d: skew error: %v", i, err)
		}
		if enumReport.Verdict != skewReport.Verdict {
			t.Fatalf("case %d: enum verdict %s != skew verdict %s",
				i, enumReport.Verdict, skewReport.Verdict)
		}
	}
}

func TestAnalyzeSkewCalibrationRanges(t *testing.T) {
	client := clockPtr("client", -1, 1)
	server := clockPtr("server", 0, 0)

	// Window [100,102), now 100: a <= now + o_c < b gives
	// o_c in [100-100, 102-100) = [0, 2), i.e. integer offsets {0, 1}.
	report, err := AnalyzeSkew(Credential{
		ClientClock: client,
		ServerClock: server,
		NotBefore:   100,
		NotAfter:    102,
		ServerNow:   100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.RequiredClientOffset != (IntRange{Min: 0, Max: 1}) {
		t.Fatalf("required = %+v, want {0,1}", report.RequiredClientOffset)
	}
	if report.Calibration != (IntRange{Min: 0, Max: 1}) {
		t.Fatalf("calibration = %+v, want {0,1}", report.Calibration)
	}
	if report.Verdict != VerdictResync {
		t.Fatalf("verdict = %s, want resync", report.Verdict)
	}
}

func TestVerdictStrings(t *testing.T) {
	cases := map[Verdict]string{
		VerdictReject: "reject",
		VerdictResync: "resync",
		VerdictAccept: "accept",
	}
	for verdict, want := range cases {
		if got := verdict.String(); got != want {
			t.Fatalf("%d.String() = %q, want %q", verdict, got, want)
		}
	}
	if got := Verdict(99).String(); got != "verdict(99)" {
		t.Fatalf("unknown verdict string = %q", got)
	}
}

func TestNilAndErrorPaths(t *testing.T) {
	var nilProblem *Problem
	if _, err := nilProblem.Evaluate(); !errors.Is(err, ErrUnknownClock) {
		t.Fatalf("nil problem err = %v, want ErrUnknownClock", err)
	}

	server := clockPtr("server", 0, 0)
	missingAnchor := &Problem{
		Clocks:  []*Clock{server},
		Windows: []Window{{NotBefore: Bound{server, 0}, NotAfter: Bound{server, 1}}},
	}
	if _, err := missingAnchor.Evaluate(); !errors.Is(err, ErrUnknownClock) {
		t.Fatalf("missing anchor err = %v, want ErrUnknownClock", err)
	}

	noWindows := &Problem{Clocks: []*Clock{server}, Now: Instant{server, 0}}
	if _, err := noWindows.Evaluate(); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("no windows err = %v, want ErrInvalidWindow", err)
	}

	dup := clockPtr("dup", 0, 0)
	dupProblem := &Problem{
		Clocks: []*Clock{dup, dup},
		Now:    Instant{dup, 0},
		Windows: []Window{
			{NotBefore: Bound{dup, 0}, NotAfter: Bound{dup, 1}},
		},
	}
	if _, err := dupProblem.Evaluate(); err == nil {
		t.Fatal("duplicate clock registration must fail")
	}

	mixedClocks := Window{
		NotBefore: Bound{Clock: clockPtr("a", 0, 0), Reading: 0},
		NotAfter:  Bound{Clock: clockPtr("b", 0, 0), Reading: 1},
	}
	if err := mixedClocks.Validate(); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("mixed-clock window err = %v, want ErrInvalidWindow", err)
	}

	var nilClock *Clock
	if err := nilClock.Validate(); !errors.Is(err, ErrInfeasibleOffsets) {
		t.Fatalf("nil clock err = %v, want ErrInfeasibleOffsets", err)
	}
	if _, ok := nilClock.Size(); ok {
		t.Fatal("nil clock Size must report ok=false")
	}

	if _, err := AnalyzeSkew(Credential{
		ClientClock: clockPtr("c", 1, 0),
		ServerClock: server,
		NotBefore:   0,
		NotAfter:    1,
		ServerNow:   0,
	}); !errors.Is(err, ErrInfeasibleOffsets) {
		t.Fatal("AnalyzeSkew must reject contradictory client domain")
	}
	if _, err := AnalyzeSkew(Credential{
		ClientClock: clockPtr("c", 0, 0),
		ServerClock: clockPtr("s", 1, 0),
		NotBefore:   0,
		NotAfter:    1,
		ServerNow:   0,
	}); !errors.Is(err, ErrInfeasibleOffsets) {
		t.Fatal("AnalyzeSkew must reject contradictory server domain")
	}
}

func TestIntRangeAndClockSize(t *testing.T) {
	if !(IntRange{Min: 2, Max: 1}).Empty() {
		t.Fatal("inverted range must be empty")
	}
	if (IntRange{Min: 1, Max: 1}).Empty() {
		t.Fatal("singleton range must not be empty")
	}
	c := clockPtr("c", -3, 2)
	size, ok := c.Size()
	if !ok || size != 6 {
		t.Fatalf("size = %d ok=%v, want 6 true", size, ok)
	}
}
