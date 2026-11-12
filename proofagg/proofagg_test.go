package proofagg

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"testing"
)

var (
	bootChallenge  = [32]byte{0xA1}
	prevChallenge  = [32]byte{0xB2}
	firmwareDigest = [32]byte{0xC3}
	otherFirmware  = [32]byte{0xD4}
	requiredTests  = []string{"rom_crc", "ram_march", "crypto_kat", "wdog"}
)

// trustedKey stands in for key material held inside the trusted measurement
// callback; tests sign and verify through the same HMAC callback.
var trustedKey = []byte("trusted-measurement-key")

func sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, trustedKey)
	mac.Write(payload)
	return mac.Sum(nil)
}

func verify(payload, signature []byte) bool {
	return hmac.Equal(sign(payload), signature)
}

func signedResult(testID string, challenge, firmware [32]byte, status Status) Result {
	return Result{
		TestID:         testID,
		Challenge:      challenge,
		FirmwareDigest: firmware,
		Status:         status,
		Signature:      sign(CanonicalPayload(testID, challenge, firmware, status)),
	}
}

func fullPassingBatch() []Result {
	out := make([]Result, 0, len(requiredTests))
	for _, id := range requiredTests {
		out = append(out, signedResult(id, bootChallenge, firmwareDigest, StatusPass))
	}
	return out
}

func newAggregator(t *testing.T) *Aggregator {
	t.Helper()
	a, err := NewAggregator(bootChallenge, firmwareDigest, requiredTests, verify)
	if err != nil {
		t.Fatalf("NewAggregator: %v", err)
	}
	return a
}

func TestAggregateAllRequiredPass(t *testing.T) {
	v := newAggregator(t).Aggregate(fullPassingBatch())
	if !v.Passed {
		t.Fatalf("expected pass, got rejections=%v missing=%v", v.Rejected, v.Missing)
	}
	if len(v.Evidence) != len(requiredTests) {
		t.Fatalf("evidence count = %d, want %d", len(v.Evidence), len(requiredTests))
	}
	for _, e := range v.Evidence {
		if e.Challenge != bootChallenge || e.FirmwareDigest != firmwareDigest {
			t.Fatalf("evidence %q references wrong challenge/firmware", e.TestID)
		}
	}
}

func TestReplayFromPreviousBootRejected(t *testing.T) {
	var batch []Result
	for _, id := range requiredTests {
		batch = append(batch, signedResult(id, prevChallenge, firmwareDigest, StatusPass))
	}
	v := newAggregator(t).Aggregate(batch)
	if v.Passed {
		t.Fatal("replayed previous-boot results must not pass")
	}
	if len(v.Evidence) != 0 {
		t.Fatalf("no evidence may be cited from a foreign challenge, got %v", v.Evidence)
	}
	if len(v.Missing) != len(requiredTests) {
		t.Fatalf("all tests must be reported missing, got %v", v.Missing)
	}
}

func TestCrossBootSplicingRejected(t *testing.T) {
	// Two results from the current boot, two spliced in from a previous
	// boot with a different challenge, and one from other firmware.
	batch := []Result{
		signedResult("rom_crc", bootChallenge, firmwareDigest, StatusPass),
		signedResult("ram_march", bootChallenge, firmwareDigest, StatusPass),
		signedResult("crypto_kat", prevChallenge, firmwareDigest, StatusPass),
		signedResult("wdog", prevChallenge, otherFirmware, StatusPass),
	}
	v := newAggregator(t).Aggregate(batch)
	if v.Passed {
		t.Fatal("cross-boot spliced batch must not pass")
	}
	if len(v.Evidence) != 2 {
		t.Fatalf("only same-challenge same-firmware results may be cited, got %v", v.Evidence)
	}
	if len(v.Rejected) != 2 {
		t.Fatalf("spliced results must be rejected, got %v", v.Rejected)
	}
}

func TestDuplicateTestIDRejected(t *testing.T) {
	batch := fullPassingBatch()
	batch = append(batch, signedResult("rom_crc", bootChallenge, firmwareDigest, StatusPass))
	v := newAggregator(t).Aggregate(batch)
	if v.Passed {
		t.Fatal("duplicate test id must not pass")
	}
	if len(v.Rejected) != 1 || v.Rejected[0].Reason != "duplicate test id" {
		t.Fatalf("expected one duplicate rejection, got %v", v.Rejected)
	}
}

func TestDuplicateCannotCoverMissingTest(t *testing.T) {
	// Attacker replays rom_crc twice and omits wdog entirely.
	batch := []Result{
		signedResult("rom_crc", bootChallenge, firmwareDigest, StatusPass),
		signedResult("rom_crc", bootChallenge, firmwareDigest, StatusPass),
		signedResult("ram_march", bootChallenge, firmwareDigest, StatusPass),
		signedResult("crypto_kat", bootChallenge, firmwareDigest, StatusPass),
	}
	v := newAggregator(t).Aggregate(batch)
	if v.Passed {
		t.Fatal("duplicates must not inflate coverage")
	}
	if len(v.Missing) != 1 || v.Missing[0] != "wdog" {
		t.Fatalf("wdog must be missing, got %v", v.Missing)
	}
}

func TestFailedResultSubstitution(t *testing.T) {
	// Device honestly reports a failed sub-test, properly signed.
	batch := fullPassingBatch()
	batch[2] = signedResult("crypto_kat", bootChallenge, firmwareDigest, StatusFail)
	v := newAggregator(t).Aggregate(batch)
	if v.Passed {
		t.Fatal("a failed sub-test must fail the verdict")
	}

	// Attacker flips the status bit to pass without re-signing.
	batch = fullPassingBatch()
	forged := signedResult("crypto_kat", bootChallenge, firmwareDigest, StatusFail)
	forged.Status = StatusPass
	batch[2] = forged
	v = newAggregator(t).Aggregate(batch)
	if v.Passed {
		t.Fatal("a forged pass over a fail signature must fail the verdict")
	}
	if len(v.Rejected) != 1 || v.Rejected[0].Reason != "invalid signature" {
		t.Fatalf("expected invalid signature rejection, got %v", v.Rejected)
	}
}

func TestMissingRequiredTest(t *testing.T) {
	v := newAggregator(t).Aggregate(fullPassingBatch()[:2])
	if v.Passed {
		t.Fatal("incomplete batch must not pass")
	}
	if len(v.Missing) != 2 {
		t.Fatalf("want 2 missing, got %v", v.Missing)
	}
}

func TestUnknownTestIDNotCounted(t *testing.T) {
	batch := append(fullPassingBatch(),
		signedResult("debug_shell", bootChallenge, firmwareDigest, StatusPass))
	v := newAggregator(t).Aggregate(batch)
	if v.Passed {
		t.Fatal("unknown test id must fail the verdict closed")
	}
	for _, e := range v.Evidence {
		if e.TestID == "debug_shell" {
			t.Fatal("unknown test id must never appear in evidence")
		}
	}
}

func TestFirmwareDigestMismatchRejected(t *testing.T) {
	var batch []Result
	for _, id := range requiredTests {
		batch = append(batch, signedResult(id, bootChallenge, otherFirmware, StatusPass))
	}
	v := newAggregator(t).Aggregate(batch)
	if v.Passed {
		t.Fatal("results bound to other firmware must not pass")
	}
	if len(v.Evidence) != 0 {
		t.Fatalf("no evidence may cite foreign firmware, got %v", v.Evidence)
	}
}

func TestTruncatedSignatureRejected(t *testing.T) {
	batch := fullPassingBatch()
	batch[1].Signature = batch[1].Signature[:8]
	v := newAggregator(t).Aggregate(batch)
	if v.Passed {
		t.Fatal("truncated signature must not pass")
	}
}

func TestConstructorValidation(t *testing.T) {
	if _, err := NewAggregator(bootChallenge, firmwareDigest, nil, verify); err == nil {
		t.Fatal("empty required set must be rejected")
	}
	if _, err := NewAggregator(bootChallenge, firmwareDigest, []string{"a", "a"}, verify); err == nil {
		t.Fatal("duplicate required ids must be rejected")
	}
	if _, err := NewAggregator(bootChallenge, firmwareDigest, requiredTests, nil); err == nil {
		t.Fatal("nil verifier must be rejected")
	}
}

func TestCanonicalPayloadDomainSeparation(t *testing.T) {
	p1 := CanonicalPayload("rom_crc", bootChallenge, firmwareDigest, StatusPass)
	p2 := CanonicalPayload("rom_crc", bootChallenge, firmwareDigest, StatusFail)
	if bytes.Equal(p1, p2) {
		t.Fatal("status bit must be covered by the signed payload")
	}
}
