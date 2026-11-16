package arc

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

var testBody = []byte("the message body")

func aarLine(i int) string {
	return fmt.Sprintf("ARC-Authentication-Results: i=%d; spf=pass smtp.mailfrom=example.net", i)
}

func amsLine(i int) string {
	return fmt.Sprintf("ARC-Message-Signature: i=%d; a=rsa-sha256; d=example.net; s=sel; b=ams-b-%d", i, i)
}

func asLine(i int, cv string) string {
	return fmt.Sprintf("ARC-Seal: i=%d; cv=%s; a=rsa-sha256; d=example.net; s=sel; b=as-b-%d", i, cv, i)
}

// chainLines builds a structurally valid chain of n sets, in wire order.
func chainLines(n int) []string {
	lines := make([]string, 0, 3*n)
	for i := 1; i <= n; i++ {
		cv := "none"
		if i > 1 {
			cv = "pass"
		}
		lines = append(lines, aarLine(i), amsLine(i), asLine(i, cv))
	}
	return lines
}

// fakeVerifiers is a caller-side verifier stand-in. failAMS/failAS hold the
// instance whose verification should fail (0 disables). In strict mode it
// additionally checks that every AMS signature b= matches its instance, which
// lets a content swap be detected through the covered-header sets.
type fakeVerifiers struct {
	failAMS      int
	failAS       int
	strict       bool
	amsCalls     []int
	asCalls      []int
	amsCovered   []*Header
	coveredCount map[int]int
}

func newFakeVerifiers() *fakeVerifiers {
	return &fakeVerifiers{coveredCount: map[int]int{}}
}

func (f *fakeVerifiers) verifiers() Verifiers {
	return Verifiers{
		VerifyAMS: f.verifyAMS,
		VerifyAS:  f.verifyAS,
	}
}

func (f *fakeVerifiers) verifyAMS(ams *Header, covered []*Header, _ []byte) error {
	f.amsCalls = append(f.amsCalls, ams.Instance)
	f.amsCovered = covered
	if f.strict && ams.TagValue("b") != fmt.Sprintf("ams-b-%d", ams.Instance) {
		return errors.New("AMS b= does not match its instance")
	}
	if ams.Instance == f.failAMS {
		return errors.New("injected AMS failure")
	}
	return nil
}

func (f *fakeVerifiers) verifyAS(seal *Header, covered []*Header, _ []byte) error {
	f.asCalls = append(f.asCalls, seal.Instance)
	f.coveredCount[seal.Instance] = len(covered)
	if f.strict {
		for _, header := range covered {
			if header.Kind == AMS &&
				header.TagValue("b") != fmt.Sprintf("ams-b-%d", header.Instance) {
				return fmt.Errorf("covered AMS i=%d signature mismatch", header.Instance)
			}
		}
	}
	if seal.Instance == f.failAS {
		return errors.New("injected AS failure")
	}
	return nil
}

func TestValidateValidChain(t *testing.T) {
	fake := newFakeVerifiers()

	result, err := Validate(chainLines(3), testBody, fake.verifiers())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusPass {
		t.Fatalf("status = %s (%s), want pass", result.Status, result.Reason)
	}
	if result.N != 3 {
		t.Fatalf("N = %d, want 3", result.N)
	}

	// Only the newest AMS is independently verified.
	if got := fmt.Sprint(fake.amsCalls); got != "[3]" {
		t.Fatalf("AMS verifier calls = %s, want [3]", got)
	}
	if got := fmt.Sprint(fake.asCalls); got != "[1 2 3]" {
		t.Fatalf("AS verifier calls = %s, want [1 2 3]", got)
	}

	// AMS(3) covers every ARC header of instances below 3.
	if len(fake.amsCovered) != 6 {
		t.Fatalf("AMS(3) covered %d headers, want 6", len(fake.amsCovered))
	}

	// AS(i) covers all headers at instance i or below except itself: 3i-1.
	wantCovered := map[int]int{1: 2, 2: 5, 3: 8}
	for instance, want := range wantCovered {
		if got := fake.coveredCount[instance]; got != want {
			t.Fatalf("AS(%d) covered %d headers, want %d", instance, got, want)
		}
	}
}

func TestValidateNoHeaders(t *testing.T) {
	result, err := Validate(nil, testBody, Verifiers{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusNone {
		t.Fatalf("status = %s, want none", result.Status)
	}
}

func TestMissingInstanceGap(t *testing.T) {
	var lines []string
	for _, line := range chainLines(3) {
		if !strings.Contains(line, "i=2;") {
			lines = append(lines, line)
		}
	}

	result, err := Validate(lines, testBody, newFakeVerifiers().verifiers())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFail(t, result, "missing ARC set for instance i=2")
}

func TestGapAtHighInstance(t *testing.T) {
	lines := append(chainLines(2),
		aarLine(4), amsLine(4), asLine(4, "pass"))

	result, err := Validate(lines, testBody, newFakeVerifiers().verifiers())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFail(t, result, "missing ARC set for instance i=3")
}

func TestMissingHeaderWithinSet(t *testing.T) {
	var lines []string
	for _, line := range chainLines(2) {
		if !strings.HasPrefix(line, "ARC-Message-Signature: i=2;") {
			lines = append(lines, line)
		}
	}

	result, err := Validate(lines, testBody, newFakeVerifiers().verifiers())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFail(t, result, "instance i=2 is missing its ARC-Message-Signature header")
}

func TestDuplicateHeader(t *testing.T) {
	lines := append(chainLines(2), asLine(2, "pass"))

	result, err := Validate(lines, testBody, newFakeVerifiers().verifiers())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFail(t, result, "duplicate ARC-Seal header at instance i=2")
}

func TestCVMisplacedFirstSeal(t *testing.T) {
	lines := chainLines(1)
	lines[2] = asLine(1, "pass")

	result, err := Validate(lines, testBody, newFakeVerifiers().verifiers())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FailingInstance != 1 {
		t.Fatalf("failing instance = %d, want 1", result.FailingInstance)
	}
	wantFail(t, result, `ARC-Seal i=1 must have cv=none`)
}

func TestCVIllegalOnLaterSeal(t *testing.T) {
	lines := chainLines(2)
	lines[5] = asLine(2, "none")

	result, err := Validate(lines, testBody, newFakeVerifiers().verifiers())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFail(t, result, `ARC-Seal i=2 must have cv=pass or cv=fail`)
}

func TestCVFailFailsChainWithoutCrypto(t *testing.T) {
	lines := chainLines(3)
	lines[5] = asLine(2, "fail")
	fake := newFakeVerifiers()

	result, err := Validate(lines, testBody, fake.verifiers())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFail(t, result, "cv=fail")
	if result.FailingInstance != 2 {
		t.Fatalf("failing instance = %d, want 2", result.FailingInstance)
	}
	if len(fake.amsCalls) != 0 || len(fake.asCalls) != 0 {
		t.Fatalf("verifiers must not run after cv=fail, got ams=%v as=%v",
			fake.amsCalls, fake.asCalls)
	}
}

func TestSetSwapDetectedByWireOrder(t *testing.T) {
	lines := chainLines(3)
	// Exchange the complete instance-1 and instance-2 blocks.
	copy(lines[0:3], chainLines(2)[3:6])
	copy(lines[3:6], chainLines(2)[0:3])

	result, err := Validate(lines, testBody, newFakeVerifiers().verifiers())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFail(t, result, "out of order")
}

func TestContentSwapDetectedByVerifier(t *testing.T) {
	// Keep every i= tag and position intact, but exchange the signature
	// bodies of AMS(1) and AMS(2). Structural checks cannot see this; the
	// seal over the covered headers must reject it.
	lines := chainLines(3)
	lines[1] = strings.Replace(amsLine(1), "b=ams-b-1", "b=ams-b-2", 1)
	lines[4] = strings.Replace(amsLine(2), "b=ams-b-2", "b=ams-b-1", 1)

	fake := newFakeVerifiers()
	fake.strict = true
	result, err := Validate(lines, testBody, fake.verifiers())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusFail || result.FailingInstance != 1 {
		t.Fatalf("got status=%s instance=%d reason=%q, want fail at i=1",
			result.Status, result.FailingInstance, result.Reason)
	}
}

func TestNewestAMSVerificationFails(t *testing.T) {
	fake := newFakeVerifiers()
	fake.failAMS = 3

	result, err := Validate(chainLines(3), testBody, fake.verifiers())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusFail || result.FailingInstance != 3 {
		t.Fatalf("got status=%s instance=%d, want fail at i=3",
			result.Status, result.FailingInstance)
	}
	if len(fake.asCalls) != 0 {
		t.Fatalf("AS verifier ran after AMS failure: %v", fake.asCalls)
	}
}

func TestASVerificationFails(t *testing.T) {
	fake := newFakeVerifiers()
	fake.failAS = 2

	result, err := Validate(chainLines(3), testBody, fake.verifiers())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusFail || result.FailingInstance != 2 {
		t.Fatalf("got status=%s instance=%d, want fail at i=2",
			result.Status, result.FailingInstance)
	}
}

func TestMissingVerifierIsCallerError(t *testing.T) {
	if _, err := Validate(chainLines(2), testBody, Verifiers{}); err == nil {
		t.Fatal("expected non-nil error when no verifier is injected")
	}
}

func TestNonARCHeaderRejected(t *testing.T) {
	lines := append([]string{"Subject: hello"}, chainLines(1)...)

	result, err := Validate(lines, testBody, newFakeVerifiers().verifiers())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFail(t, result, "not an ARC header")
}

func TestParseHeaderErrors(t *testing.T) {
	cases := map[string]string{
		"no colon":       "ARC-Seal i=1",
		"non-ARC field":  "From: a@example.net",
		"missing i tag":  "ARC-Seal: cv=none; b=abc",
		"non-numeric i":  "ARC-Seal: i=one; cv=none",
		"zero instance":  "ARC-Seal: i=0; cv=none",
		"negative i tag": "ARC-Message-Signature: i=-1; b=abc",
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseHeader(line); err == nil {
				t.Fatalf("ParseHeader(%q) expected error", line)
			}
		})
	}
}

func wantFail(t *testing.T, result *Result, reasonPart string) {
	t.Helper()
	if result.Status != StatusFail {
		t.Fatalf("status = %s, want fail (reason=%q)", result.Status, result.Reason)
	}
	if !strings.Contains(result.Reason, reasonPart) {
		t.Fatalf("reason = %q, want substring %q", result.Reason, reasonPart)
	}
}
