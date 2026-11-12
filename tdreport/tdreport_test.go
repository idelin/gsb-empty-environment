package tdreport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/binary"
	"strings"
	"testing"
)

var (
	testNonce     = []byte("0123456789abcdef0123456789abcdef")
	testSessionID = []byte("session-7f3a9c21")
)

func testSPKI(t *testing.T) (spki, otherSPKI []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	spki, err = x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherSPKI, err = x509.MarshalPKIXPublicKey(&other.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return spki, otherSPKI
}

func testReference() *ReferenceValues {
	ref := &ReferenceValues{}
	for i := range ref.MRTD {
		ref.MRTD[i] = byte(i)
	}
	for r := range ref.RTMRs {
		for i := range ref.RTMRs[r] {
			ref.RTMRs[r][i] = byte(0x10*(r+1)) + byte(i)
		}
	}
	return ref
}

func goodReport(t *testing.T, spki []byte) *TDReport {
	t.Helper()
	ref := testReference()
	rep := &TDReport{
		ReportData: ExpectedReportData(testNonce, spki, testSessionID),
		MRTD:       ref.MRTD,
		RTMRs:      ref.RTMRs,
	}
	return rep
}

func verify(t *testing.T, rep *TDReport, ref *ReferenceValues, spki []byte, status TCBStatus, advisories []string, pol Policy) error {
	t.Helper()
	return Verify(rep, ref, testNonce, spki, testSessionID, status, advisories, pol)
}

func TestVerifyHappyPath(t *testing.T) {
	spki, _ := testSPKI(t)
	ref := testReference()
	if err := verify(t, goodReport(t, spki), ref, spki, TCBUpToDate, nil, Policy{}); err != nil {
		t.Fatalf("expected valid report to pass, got %v", err)
	}
}

// An attacker replays a genuine quote from another TD but terminates TLS
// with their own key: REPORTDATA no longer matches the session binding.
func TestVerifySwappedTLSKey(t *testing.T) {
	spki, otherSPKI := testSPKI(t)
	ref := testReference()
	// Report was minted for the attacker's key, verified against ours.
	rep := goodReport(t, otherSPKI)
	err := verify(t, rep, ref, spki, TCBUpToDate, nil, Policy{})
	if err == nil || !strings.Contains(err.Error(), "REPORTDATA") {
		t.Fatalf("expected REPORTDATA mismatch, got %v", err)
	}
}

// A debug TD can run identical measurements while exposing TD memory.
func TestVerifyDebugAttribute(t *testing.T) {
	spki, _ := testSPKI(t)
	ref := testReference()
	rep := goodReport(t, spki)
	rep.TDAttributes = 1 // DEBUG bit
	err := verify(t, rep, ref, spki, TCBUpToDate, nil, Policy{})
	if err == nil || !strings.Contains(err.Error(), "DEBUG") {
		t.Fatalf("expected DEBUG rejection, got %v", err)
	}
}

// A single flipped byte in one RTMR must be caught and located.
func TestVerifySingleRTMRDeviation(t *testing.T) {
	spki, _ := testSPKI(t)
	ref := testReference()
	rep := goodReport(t, spki)
	rep.RTMRs[2][47] ^= 0x01
	err := verify(t, rep, ref, spki, TCBUpToDate, nil, Policy{})
	if err == nil || !strings.Contains(err.Error(), "RTMR[2]") {
		t.Fatalf("expected RTMR[2] mismatch, got %v", err)
	}
}

func TestVerifyMRTDDeviation(t *testing.T) {
	spki, _ := testSPKI(t)
	ref := testReference()
	rep := goodReport(t, spki)
	rep.MRTD[0] ^= 0xff
	err := verify(t, rep, ref, spki, TCBUpToDate, nil, Policy{})
	if err == nil || !strings.Contains(err.Error(), "MRTD") {
		t.Fatalf("expected MRTD mismatch, got %v", err)
	}
}

// Stale TCB levels are rejected; SWHardeningNeeded requires a registered
// exemption covering every reported advisory.
func TestVerifyTCBStatus(t *testing.T) {
	spki, _ := testSPKI(t)
	ref := testReference()

	rep := goodReport(t, spki)
	if err := verify(t, rep, ref, spki, TCBOutOfDate, []string{"INTEL-SA-001"}, Policy{}); err == nil {
		t.Fatal("expected OutOfDate TCB to be rejected")
	}

	rep = goodReport(t, spki)
	if err := verify(t, rep, ref, spki, TCBRevoked, nil, Policy{}); err == nil {
		t.Fatal("expected Revoked TCB to be rejected")
	}

	rep = goodReport(t, spki)
	if err := verify(t, rep, ref, spki, TCBSWHardeningNeeded, []string{"INTEL-SA-001"}, Policy{}); err == nil {
		t.Fatal("expected SWHardeningNeeded without exemption to be rejected")
	}

	rep = goodReport(t, spki)
	pol := Policy{SWHardeningExemptions: []string{"INTEL-SA-001"}}
	if err := verify(t, rep, ref, spki, TCBSWHardeningNeeded, []string{"INTEL-SA-001"}, pol); err != nil {
		t.Fatalf("expected exempted SWHardeningNeeded to pass, got %v", err)
	}

	rep = goodReport(t, spki)
	if err := verify(t, rep, ref, spki, TCBSWHardeningNeeded, []string{"INTEL-SA-001", "INTEL-SA-002"}, pol); err == nil {
		t.Fatal("expected partially exempted SWHardeningNeeded to be rejected")
	}
}

func buildRawReport(rep *TDReport) []byte {
	raw := make([]byte, ReportLen)
	copy(raw[reportDataOffset:], rep.ReportData[:])
	binary.LittleEndian.PutUint64(raw[tdAttributesOffset:], rep.TDAttributes)
	copy(raw[mrTDOffset:], rep.MRTD[:])
	for i := range rep.RTMRs {
		copy(raw[rtmrOffset+i*MeasurementLen:], rep.RTMRs[i][:])
	}
	return raw
}

func TestParseTDReportRoundTrip(t *testing.T) {
	spki, _ := testSPKI(t)
	want := goodReport(t, spki)
	got, err := ParseTDReport(buildRawReport(want))
	if err != nil {
		t.Fatal(err)
	}
	if *got != *want {
		t.Fatal("round-trip mismatch")
	}
}

func TestParseTDReportTruncated(t *testing.T) {
	spki, _ := testSPKI(t)
	raw := buildRawReport(goodReport(t, spki))
	for _, n := range []int{0, 100, ReportLen - 1} {
		if _, err := ParseTDReport(raw[:n]); err == nil {
			t.Fatalf("expected error for %d-byte report", n)
		}
	}
}
