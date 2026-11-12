package netsec

import (
	"testing"
	"time"
)

const (
	unknownUser = "unknown-account"
	badPassword = "known-account-wrong-password"
)

func ms(value int64) time.Duration {
	return time.Duration(value) * time.Millisecond
}

func intervalPtr(value Int64Interval) *Int64Interval {
	return &value
}

func hasField(predicates []Predicate, field string) bool {
	for _, predicate := range predicates {
		if predicate.Field == field {
			return true
		}
	}
	return false
}

func fieldCount(predicates []Predicate, field string) int {
	count := 0
	for _, predicate := range predicates {
		if predicate.Field == field {
			count++
		}
	}
	return count
}

// TestDuplicateHeaderMultiplicity verifies that repeated headers are compared
// with multiplicity and per-index order rather than collapsed to a set.
func TestDuplicateHeaderMultiplicity(t *testing.T) {
	left := Observation{
		Label:  unknownUser,
		Status: 401,
		Headers: []HeaderView{
			{Name: "Set-Cookie", Values: []string{"sid=aaa", "trace=111"}},
			{Name: "Content-Type", Values: []string{"application/json"}},
		},
		Body: BodyView{
			Template: `{"error":"invalid credentials"}`,
			Length:   Int64Interval{Min: 32, Max: 32},
		},
	}
	right := Observation{
		Label:  badPassword,
		Status: 401,
		Headers: []HeaderView{
			{Name: "set-cookie", Values: []string{"sid=bbb", "trace=111"}},
			{Name: "Content-Type", Values: []string{"application/json"}},
		},
		Body: BodyView{
			Template: `{"error":"invalid credentials"}`,
			Length:   Int64Interval{Min: 32, Max: 32},
		},
	}

	report := Compare(left, right, nil)
	if report.Indistinguishable() {
		t.Fatal("expected the differing Set-Cookie value to be a distinguisher")
	}
	want := headerIndexPath("Set-Cookie", 0)
	if !hasField(report.Distinguishers, want) {
		t.Fatalf("missing per-index predicate %q, got %+v", want, report.Distinguishers)
	}
	if hasField(report.Distinguishers, headerPrefix("Content-Type")) {
		t.Fatal("identical header must not produce a predicate")
	}

	// Multiplicity difference on the same repeated header.
	right.Headers[0].Values = []string{"sid=bbb", "trace=111", "extra=222"}
	report = Compare(left, right, nil)
	if !hasField(report.Distinguishers, headerPrefix("Set-Cookie")) {
		t.Fatalf("expected multiplicity predicate, got %+v", report.Distinguishers)
	}
	if fieldCount(report.Distinguishers, headerPrefix("Set-Cookie")) != 1 {
		t.Fatal("multiplicity mismatch must emit exactly one minimal predicate")
	}

	// Allowlisting one index suppresses only that index; other occurrences
	// remain visible.
	left.Headers[0].Values = []string{"sid=aaa", "trace=111"}
	right.Headers = []HeaderView{
		{Name: "Set-Cookie", Values: []string{"sid=zzz", "trace=999"}},
		{Name: "Content-Type", Values: []string{"application/json"}},
	}
	report = Compare(left, right, []FieldSelector{SelectHeaderValue("set-cookie", 0)})
	if !hasField(report.Ignored, headerIndexPath("Set-Cookie", 0)) {
		t.Fatal("allowlisted index should appear in Ignored")
	}
	if !hasField(report.Distinguishers, headerIndexPath("Set-Cookie", 1)) {
		t.Fatalf("non-allowlisted index must still distinguish, got %+v", report.Distinguishers)
	}

	// Whole-header allowlist suppresses every predicate for that header.
	report = Compare(left, right, []FieldSelector{SelectHeader("Set-Cookie")})
	if !report.Indistinguishable() {
		t.Fatalf("header-level allowlist should suppress all values, got %+v", report.Distinguishers)
	}
}

// TestCompressedLengthAndEncoding compares bodies through their encoded length
// band rather than only the final error text.
func TestCompressedLengthAndEncoding(t *testing.T) {
	template := `{"error":"invalid credentials","req":"<uuid>"}`

	unknown := Observation{
		Label:  badPassword,
		Status: 401,
		Headers: []HeaderView{
			{Name: "Content-Encoding", Values: []string{"gzip"}},
			{Name: "Vary", Values: []string{"Accept-Encoding"}},
		},
		Body: BodyView{
			Template:   template,
			Length:     Int64Interval{Min: 60, Max: 80},
			Compressed: intervalPtr(Int64Interval{Min: 48, Max: 52}),
		},
	}
	known := Observation{
		Label:  unknownUser,
		Status: 401,
		Headers: []HeaderView{
			{Name: "Content-Encoding", Values: []string{"gzip"}},
			{Name: "Vary", Values: []string{"Accept-Encoding"}},
		},
		Body: BodyView{
			Template:   template,
			Length:     Int64Interval{Min: 62, Max: 78},
			Compressed: intervalPtr(Int64Interval{Min: 60, Max: 64}),
		},
	}

	report := Compare(unknown, known, nil)
	if !hasField(report.Distinguishers, FieldCompressed) {
		t.Fatalf("disjoint compressed bands must distinguish, got %+v", report.Distinguishers)
	}
	if hasField(report.Distinguishers, FieldLength) {
		t.Fatal("overlapping raw length bands must not distinguish")
	}

	// Overlapping compressed bands remove the side channel.
	known.Body.Compressed = intervalPtr(Int64Interval{Min: 50, Max: 55})
	report = Compare(unknown, known, nil)
	if !report.Indistinguishable() {
		t.Fatalf("overlapping bands should be indistinguishable, got %+v", report.Distinguishers)
	}

	// Compression observed on only one side is itself a distinguisher.
	known.Body.Compressed = nil
	report = Compare(unknown, known, nil)
	if !hasField(report.Distinguishers, FieldCompressed) {
		t.Fatalf("one-sided compressed observation must distinguish, got %+v", report.Distinguishers)
	}

	// Allowlisting compressed length leaves the pair indistinguishable.
	report = Compare(unknown, known, []FieldSelector{SelectField(FieldCompressed)})
	if !report.Indistinguishable() {
		t.Fatalf("allowlisted compressed length should be ignored, got %+v", report.Distinguishers)
	}
	if !hasField(report.Ignored, FieldCompressed) {
		t.Fatal("suppressed predicate should be recorded in Ignored")
	}
}

// TestMultiStageTimeoutTiers checks timing tiers stage by stage, including
// timeouts which form their own incomparable band.
func TestMultiStageTimeoutTiers(t *testing.T) {
	baseTiming := func() TimingProfile {
		return TimingProfile{
			StageDNS:  {Bucket: "fast", Range: DurationRange{Min: ms(1), Max: ms(5)}},
			StageTCP:  {Bucket: "fast", Range: DurationRange{Min: ms(5), Max: ms(20)}},
			StageTLS:  {Bucket: "fast", Range: DurationRange{Min: ms(10), Max: ms(40)}},
			StageTTFB: {Bucket: "normal", Range: DurationRange{Min: ms(80), Max: ms(200)}},
		}
	}

	unknown := Observation{
		Label:  unknownUser,
		Status: 401,
		Body: BodyView{
			Template: `{"error":"invalid credentials"}`,
			Length:   Int64Interval{Min: 32, Max: 32},
		},
		Timing: baseTiming(),
	}
	known := Observation{
		Label:  badPassword,
		Status: 401,
		Body: BodyView{
			Template: `{"error":"invalid credentials"}`,
			Length:   Int64Interval{Min: 32, Max: 32},
		},
		Timing: baseTiming(),
	}

	// Only one side reaches the TTFB timeout.
	known.Timing[StageTTFB] = TimingTier{Bucket: "timeout", TimedOut: true}

	report := Compare(unknown, known, nil)
	timeoutPath := FieldTiming + "." + StageTTFB
	if !hasField(report.Distinguishers, timeoutPath) {
		t.Fatalf("one-sided timeout must distinguish, got %+v", report.Distinguishers)
	}

	// A disjoint non-timeout band on a second stage yields a second minimal
	// predicate, while overlapping bands stay quiet.
	known.Timing[StageTTFB] = TimingTier{
		Bucket: "slow",
		Range:  DurationRange{Min: ms(400), Max: ms(900)},
	}
	known.Timing[StageTCP] = TimingTier{
		Bucket: "slow",
		Range:  DurationRange{Min: ms(50), Max: ms(90)},
	}
	report = Compare(unknown, known, nil)
	if !hasField(report.Distinguishers, timeoutPath) {
		t.Fatal("disjoint TTFB band must distinguish")
	}
	if !hasField(report.Distinguishers, FieldTiming+"."+StageTCP) {
		t.Fatal("disjoint TCP band must distinguish")
	}
	if hasField(report.Distinguishers, FieldTiming+"."+StageDNS) {
		t.Fatal("overlapping DNS bands must not distinguish")
	}

	// Missing stage on one side is detectable.
	delete(known.Timing, StageTLS)
	report = Compare(unknown, known, nil)
	if !hasField(report.Distinguishers, FieldTiming+"."+StageTLS) {
		t.Fatal("missing stage must distinguish")
	}

	// Both sides timing out the same stage is indistinguishable.
	unknown.Timing = baseTiming()
	known.Timing = baseTiming()
	unknown.Timing[StageTTFB] = TimingTier{Bucket: "timeout", TimedOut: true}
	known.Timing[StageTTFB] = TimingTier{Bucket: "timeout", TimedOut: true}
	report = Compare(unknown, known, nil)
	if !report.Indistinguishable() {
		t.Fatalf("matching timeout tiers should be indistinguishable, got %+v", report.Distinguishers)
	}

	// Stage-level allowlist suppresses exactly that stage.
	unknown.Timing = baseTiming()
	known.Timing = baseTiming()
	known.Timing[StageTCP] = TimingTier{
		Bucket: "slow",
		Range:  DurationRange{Min: ms(50), Max: ms(90)},
	}
	report = Compare(unknown, known, []FieldSelector{SelectStage(StageTCP)})
	if !report.Indistinguishable() {
		t.Fatalf("allowlisted stage should be ignored, got %+v", report.Distinguishers)
	}
}

// TestMinimalConditionOnlyAllowlistedFieldsIgnored proves the engine never
// suppresses a field the caller did not list, and the surviving predicates
// form the minimal distinguishing condition.
func TestMinimalConditionOnlyAllowlistedFieldsIgnored(t *testing.T) {
	unknown := Observation{
		Label:  unknownUser,
		Status: 404,
		Headers: []HeaderView{
			{Name: "X-Request-Id", Values: []string{"<uuid>"}},
			{Name: "X-Account-State", Values: []string{"absent"}},
		},
		Body: BodyView{
			Template: `{"error":"not found"}`,
			Length:   Int64Interval{Min: 20, Max: 20},
		},
		Timing: TimingProfile{
			StageTTFB: {Range: DurationRange{Min: ms(10), Max: ms(30)}},
		},
	}
	known := Observation{
		Label:  badPassword,
		Status: 401,
		Headers: []HeaderView{
			{Name: "X-Request-Id", Values: []string{"<uuid>"}},
			{Name: "X-Account-State", Values: []string{"locked"}},
		},
		Body: BodyView{
			Template: `{"error":"invalid credentials"}`,
			Length:   Int64Interval{Min: 28, Max: 28},
		},
		Timing: TimingProfile{
			StageTTFB: {Range: DurationRange{Min: ms(120), Max: ms(260)}},
		},
	}

	// Only the request-id header is declared random; the other five
	// differences survive as the minimal condition.
	report := Compare(unknown, known, []FieldSelector{SelectHeader("X-Request-Id")})
	for _, field := range []string{
		FieldStatus,
		headerIndexPath("X-Account-State", 0),
		FieldBody,
		FieldLength,
		FieldTiming + "." + StageTTFB,
	} {
		if !hasField(report.Distinguishers, field) {
			t.Fatalf("non-allowlisted field %q must distinguish, got %+v", field, report.Distinguishers)
		}
	}
	if len(report.MinimalCondition()) != 5 {
		t.Fatalf("expected exactly 5 minimal predicates, got %d: %+v",
			len(report.Distinguishers), report.Distinguishers)
	}
	if len(report.Ignored) != 0 {
		t.Fatal("identical allowlisted header should not generate an ignored predicate")
	}

	// Suppressing every leaked dimension makes the pair indistinguishable
	// even though the raw error text differs.
	report = Compare(unknown, known, []FieldSelector{
		SelectHeader("X-Request-Id"),
		SelectHeader("X-Account-State"),
		SelectField(FieldStatus),
		SelectField(FieldBody),
		SelectField(FieldLength),
		SelectStage(StageTTFB),
	})
	if !report.Indistinguishable() {
		t.Fatalf("fully allowlisted pair must be indistinguishable, got %+v", report.Distinguishers)
	}
	if len(report.Ignored) != 5 {
		t.Fatalf("expected 5 suppressed predicates, got %d: %+v",
			len(report.Ignored), report.Ignored)
	}
}

// TestFullLoginSample is the end-to-end sample from the task: duplicate
// headers, compressed length and multi-stage timeout evaluated together,
// showing that identical final error text is not sufficient.
func TestFullLoginSample(t *testing.T) {
	bodyTemplate := `{"error":"invalid credentials","trace":"<trace-id>"}`

	unknown := Observation{
		Label:  unknownUser,
		Status: 401,
		Headers: []HeaderView{
			{Name: "Content-Type", Values: []string{"application/json; charset=utf-8"}},
			{Name: "Content-Encoding", Values: []string{"gzip"}},
			{Name: "Set-Cookie", Values: []string{"sid=<rand>", "ctx=<rand>"}},
		},
		Body: BodyView{
			Template:   bodyTemplate,
			Length:     Int64Interval{Min: 64, Max: 72},
			Compressed: intervalPtr(Int64Interval{Min: 52, Max: 56}),
		},
		Timing: TimingProfile{
			StageTCP:  {Bucket: "fast", Range: DurationRange{Min: ms(5), Max: ms(18)}},
			StageTTFB: {Bucket: "normal", Range: DurationRange{Min: ms(90), Max: ms(180)}},
		},
	}
	known := Observation{
		Label:  badPassword,
		Status: 401,
		Headers: []HeaderView{
			{Name: "Content-Type", Values: []string{"application/json; charset=utf-8"}},
			{Name: "Content-Encoding", Values: []string{"gzip"}},
			{Name: "Set-Cookie", Values: []string{"sid=<rand>", "ctx=<rand>", "atk=<rand>"}},
		},
		Body: BodyView{
			Template:   bodyTemplate,
			Length:     Int64Interval{Min: 66, Max: 74},
			Compressed: intervalPtr(Int64Interval{Min: 61, Max: 65}),
		},
		Timing: TimingProfile{
			StageTCP:  {Bucket: "fast", Range: DurationRange{Min: ms(6), Max: ms(17)}},
			StageTTFB: {Bucket: "timeout", TimedOut: true},
		},
	}

	report := Compare(unknown, known, nil)
	if report.Indistinguishable() {
		t.Fatal("identical error text must not hide header, length and timing leaks")
	}
	expected := []string{
		headerPrefix("Set-Cookie"),
		FieldCompressed,
		FieldTiming + "." + StageTTFB,
	}
	for _, field := range expected {
		if !hasField(report.Distinguishers, field) {
			t.Fatalf("full sample missing distinguisher %q, got %+v", field, report.Distinguishers)
		}
	}
	if hasField(report.Distinguishers, FieldBody) {
		t.Fatal("identical body templates must not distinguish")
	}

	// Only genuinely random fields may be ignored; the timeout remains.
	report = Compare(unknown, known, []FieldSelector{
		SelectHeader("Set-Cookie"),
		SelectField(FieldCompressed),
	})
	if report.Indistinguishable() {
		t.Fatal("non-allowlisted timeout must still distinguish")
	}
	if !hasField(report.Distinguishers, FieldTiming+"."+StageTTFB) {
		t.Fatalf("timeout predicate must survive the allowlist, got %+v", report.Distinguishers)
	}
	if hasField(report.Distinguishers, headerPrefix("Set-Cookie")) ||
		hasField(report.Distinguishers, FieldCompressed) {
		t.Fatal("allowlisted fields must be suppressed")
	}
}

// TestHeaderOrderAndPresence covers absent headers and value ordering.
func TestHeaderOrderAndPresence(t *testing.T) {
	left := Observation{
		Label:  unknownUser,
		Status: 200,
		Headers: []HeaderView{
			{Name: "Warning", Values: []string{"a", "b"}},
			{Name: "X-Only-Left", Values: []string{"1"}},
		},
		Body: BodyView{Length: Int64Interval{Min: 0, Max: 0}},
	}
	right := Observation{
		Label:  badPassword,
		Status: 200,
		Headers: []HeaderView{
			{Name: "Warning", Values: []string{"b", "a"}},
			{Name: "X-Only-Right", Values: []string{"1"}},
		},
		Body: BodyView{Length: Int64Interval{Min: 0, Max: 0}},
	}

	report := Compare(left, right, nil)
	if !hasField(report.Distinguishers, headerPrefix("X-Only-Left")) ||
		!hasField(report.Distinguishers, headerPrefix("X-Only-Right")) {
		t.Fatalf("one-sided headers must distinguish, got %+v", report.Distinguishers)
	}
	if !hasField(report.Distinguishers, headerIndexPath("Warning", 0)) ||
		!hasField(report.Distinguishers, headerIndexPath("Warning", 1)) {
		t.Fatalf("reordered duplicate values must distinguish per index, got %+v", report.Distinguishers)
	}
}

// TestInvalidIntervalAndCleanPair documents error handling and the clean
// indistinguishable case.
func TestInvalidIntervalAndCleanPair(t *testing.T) {
	observation := Observation{
		Label:  unknownUser,
		Status: 401,
		Body: BodyView{
			Template: "err",
			Length:   Int64Interval{Min: 10, Max: 5},
		},
	}
	other := observation
	other.Label = badPassword
	report := Compare(observation, other, nil)
	if len(report.Errors) == 0 {
		t.Fatal("invalid length interval must be reported as an error")
	}

	clean := Observation{
		Label:  unknownUser,
		Status: 401,
		Headers: []HeaderView{
			{Name: "X-Trace", Values: []string{"<trace>"}},
		},
		Body: BodyView{
			Template:   "err",
			Length:     Int64Interval{Min: 10, Max: 20},
			Compressed: intervalPtr(Int64Interval{Min: 8, Max: 12}),
		},
		Timing: TimingProfile{
			StageTTFB: {Bucket: "normal", Range: DurationRange{Min: ms(80), Max: ms(160)}},
		},
	}
	cleanOther := clean
	cleanOther.Label = badPassword
	report = Compare(clean, cleanOther, nil)
	if !report.Indistinguishable() || len(report.Errors) != 0 {
		t.Fatalf("identical observations must be indistinguishable without errors, got %+v", report)
	}
}
