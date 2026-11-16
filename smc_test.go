package netsec

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

// ueSecurityCapabilities builds the value octets of a UE security
// capabilities IE (TS 24.501 9.11.3.58): 5GS EAIA pair followed by a nested
// EPS NAS security algorithms IE (IEI 0x19, 4 value octets).
func ueSecurityCapabilities(ea5gs, ia5gs, eaeps, iaeps byte) []byte {
	return []byte{
		ea5gs, ia5gs,
		ieEPSNASCaps, 0x04,
		eaeps, 0x00, iaeps, 0x00,
	}
}

// buildInnerSMC encodes the plaintext body of a NAS Security Mode Command.
func buildInnerSMC(nea, nia byte, replayedCaps []byte) []byte {
	inner := []byte{
		PD5GMM,
		0x00,
		MsgSecurityModeCommand,
		0xC0, // ngKSI half-octet IE (TSC=0, ksi=0)
		ieSelectedNASAlgs, (nia << 3) | nea,
		ieReplayedUECaps, byte(len(replayedCaps)),
	}
	return append(inner, replayedCaps...)
}

// wrapIntegrityProtected builds the outer integrity-protected 5GMM envelope.
func wrapIntegrityProtected(seq byte, mac [4]byte, plaintext []byte) []byte {
	msg := make([]byte, 0, protectedMessageHeaderLen+len(plaintext))
	msg = append(msg,
		PD5GMM,
		SHTIntegrityProtected,
		mac[0], mac[1], mac[2], mac[3],
		seq,
	)
	return append(msg, plaintext...)
}

// signNIA2 derives K_nasint from kamf and fills in a valid NIA2 NAS-MAC.
func signNIA2(t *testing.T, kamf []byte, seq byte, plaintext []byte) []byte {
	t.Helper()
	kNasInt, err := DeriveNASIntegrityKey(kamf, NIA2)
	if err != nil {
		t.Fatalf("derive K_nasint: %v", err)
	}
	mac, err := nia2{}.ComputeMAC(kNasInt[:16], uint32(seq), DirectionDownlink, plaintext)
	if err != nil {
		t.Fatalf("compute NAS-MAC: %v", err)
	}
	return wrapIntegrityProtected(seq, mac, plaintext)
}

func testKAMF() []byte {
	return bytes.Repeat([]byte{0xA5}, 32)
}

func testConfig(caps []byte) SMCConfig {
	return SMCConfig{
		KAMF:                   testKAMF(),
		UESecurityCapabilities: caps,
		ExpectedSequenceNumber: 1,
	}
}

// TestVerifyValidSMC covers the happy path: integrity protected SMC with a
// valid K_AMF-derived NIA2 MAC and an exact capability echo.
func TestVerifyValidSMC(t *testing.T) {
	caps := ueSecurityCapabilities(0xE0, 0xE0, 0xE0, 0xE0)
	inner := buildInnerSMC(2 /* NEA2 */, 2 /* NIA2 */, caps)
	msg := signNIA2(t, testKAMF(), 1, inner)

	result, err := VerifySecurityModeCommand(msg, testConfig(caps))
	if err != nil {
		t.Fatalf("expected SMC acceptance, got %v", err)
	}
	if result.SelectedNIA != NIA2 || result.SelectedNEA != 2 {
		t.Fatalf("unexpected algorithms: NIA=%d NEA=%d", result.SelectedNIA, result.SelectedNEA)
	}
	if result.SequenceNumber != 1 {
		t.Fatalf("unexpected sequence number %d", result.SequenceNumber)
	}
	if len(result.KNasInt) != 32 || len(result.KNasEnc) != 32 {
		t.Fatalf("derived keys must be 256 bits, got int=%d enc=%d",
			len(result.KNasInt), len(result.KNasEnc))
	}
}

// TestVerifyStrippedCapabilities models the attack in the brief: the network
// replays capabilities with the EPS bits deleted, so the byte-identical echo
// check must fail with cause #23.
func TestVerifyStrippedCapabilities(t *testing.T) {
	original := ueSecurityCapabilities(0xE0, 0xE0, 0xE0, 0xE0)
	replayed := ueSecurityCapabilities(0xE0, 0xE0, 0x00, 0x00)
	inner := buildInnerSMC(2, 2, replayed)
	msg := signNIA2(t, testKAMF(), 1, inner)

	_, err := VerifySecurityModeCommand(msg, testConfig(original))
	reject, ok := AsRejectError(err)
	if !ok {
		t.Fatalf("expected reject error, got %v", err)
	}
	if reject.Cause != CauseUECapabilitiesMismatch {
		t.Fatalf("expected cause #%d, got #%d", CauseUECapabilitiesMismatch, reject.Cause)
	}
	if !bytes.Equal(reject.Reject, BuildSecurityModeReject(CauseUECapabilitiesMismatch)) {
		t.Fatalf("reject message mismatch: %x", reject.Reject)
	}
}

// TestVerifyIA0NotEmergency rejects null integrity on a normal registration.
func TestVerifyIA0NotEmergency(t *testing.T) {
	caps := ueSecurityCapabilities(0xE0, 0xE1 /* IA0 supported */, 0xE0, 0xE0)
	inner := buildInnerSMC(0 /* NEA0 */, 0 /* NIA0 */, caps)
	msg := wrapIntegrityProtected(1, [4]byte{}, inner)

	_, err := VerifySecurityModeCommand(msg, testConfig(caps))
	reject, ok := AsRejectError(err)
	if !ok {
		t.Fatalf("expected reject error, got %v", err)
	}
	if reject.Cause != CauseSecurityModeRejectedUnspecified {
		t.Fatalf("expected cause #%d, got #%d",
			CauseSecurityModeRejectedUnspecified, reject.Cause)
	}
}

// TestVerifyIA0Emergency accepts null integrity for an emergency registration,
// where the NAS-MAC is defined as zero (TS 33.501 6.7.2).
func TestVerifyIA0Emergency(t *testing.T) {
	caps := ueSecurityCapabilities(0xE1, 0xE1, 0xE1, 0xE1)
	inner := buildInnerSMC(0, 0, caps)
	msg := wrapIntegrityProtected(7, [4]byte{}, inner)

	cfg := testConfig(caps)
	cfg.EmergencyRegistration = true
	cfg.ExpectedSequenceNumber = 7
	result, err := VerifySecurityModeCommand(msg, cfg)
	if err != nil {
		t.Fatalf("emergency NIA0 SMC must be accepted, got %v", err)
	}
	if result.SelectedNIA != NIA0 {
		t.Fatalf("expected NIA0, got NIA%d", result.SelectedNIA)
	}
}

// TestVerifyBadMAC rejects a tampered MAC with cause #20.
func TestVerifyBadMAC(t *testing.T) {
	caps := ueSecurityCapabilities(0xE0, 0xE0, 0xE0, 0xE0)
	inner := buildInnerSMC(2, 2, caps)
	msg := signNIA2(t, testKAMF(), 1, inner)
	msg[3] ^= 0x01 // corrupt one NAS-MAC octet

	_, err := VerifySecurityModeCommand(msg, testConfig(caps))
	reject, ok := AsRejectError(err)
	if !ok {
		t.Fatalf("expected reject error, got %v", err)
	}
	if reject.Cause != CauseMACFailure {
		t.Fatalf("expected cause #%d, got #%d", CauseMACFailure, reject.Cause)
	}
}

// TestVerifySequenceNumberMismatch treats a wrong NAS COUNT sequence number as
// a MAC failure, preventing replay of an old SMC.
func TestVerifySequenceNumberMismatch(t *testing.T) {
	caps := ueSecurityCapabilities(0xE0, 0xE0, 0xE0, 0xE0)
	inner := buildInnerSMC(2, 2, caps)
	msg := signNIA2(t, testKAMF(), 9, inner)

	_, err := VerifySecurityModeCommand(msg, testConfig(caps))
	reject, ok := AsRejectError(err)
	if !ok {
		t.Fatalf("expected reject error, got %v", err)
	}
	if reject.Cause != CauseMACFailure {
		t.Fatalf("expected cause #%d, got #%d", CauseMACFailure, reject.Cause)
	}
}

// TestVerifyPlainSMC rejects an unprotected SMC: an attacker could forge it
// without any key material.
func TestVerifyPlainSMC(t *testing.T) {
	caps := ueSecurityCapabilities(0xE0, 0xE0, 0xE0, 0xE0)
	inner := buildInnerSMC(2, 2, caps)

	_, err := VerifySecurityModeCommand(inner, testConfig(caps))
	reject, ok := AsRejectError(err)
	if !ok {
		t.Fatalf("expected reject error, got %v", err)
	}
	if reject.Cause != CauseSecurityModeRejectedUnspecified {
		t.Fatalf("expected cause #%d, got #%d",
			CauseSecurityModeRejectedUnspecified, reject.Cause)
	}
}

// TestVerifyWrongKAMF shows that MAC verification fails when the verifier
// holds a different K_AMF.
func TestVerifyWrongKAMF(t *testing.T) {
	caps := ueSecurityCapabilities(0xE0, 0xE0, 0xE0, 0xE0)
	inner := buildInnerSMC(2, 2, caps)
	msg := signNIA2(t, testKAMF(), 1, inner)

	cfg := testConfig(caps)
	cfg.KAMF = bytes.Repeat([]byte{0x5A}, 32)
	_, err := VerifySecurityModeCommand(msg, cfg)
	reject, ok := AsRejectError(err)
	if !ok {
		t.Fatalf("expected reject error, got %v", err)
	}
	if reject.Cause != CauseMACFailure {
		t.Fatalf("expected cause #%d, got #%d", CauseMACFailure, reject.Cause)
	}
}

// TestVerifyMissingReplayedCaps rejects an SMC whose replayed UE security
// capabilities IE was stripped entirely.
func TestVerifyMissingReplayedCaps(t *testing.T) {
	inner := []byte{
		PD5GMM, 0x00, MsgSecurityModeCommand,
		0xC0,
		ieSelectedNASAlgs, (NIA2 << 3) | 2,
	}
	msg := signNIA2(t, testKAMF(), 1, inner)

	_, err := VerifySecurityModeCommand(msg,
		testConfig(ueSecurityCapabilities(0xE0, 0xE0, 0xE0, 0xE0)))
	reject, ok := AsRejectError(err)
	if !ok {
		t.Fatalf("expected reject error, got %v", err)
	}
	if reject.Cause != CauseUECapabilitiesMismatch {
		t.Fatalf("expected cause #%d, got #%d", CauseUECapabilitiesMismatch, reject.Cause)
	}
}

// TestExtractRegistrationCapabilities verifies exact extraction of the UE
// security capabilities from a Registration Request, EPS part included.
func TestExtractRegistrationCapabilities(t *testing.T) {
	caps := ueSecurityCapabilities(0xE0, 0xE0, 0xE0, 0xE0)
	req := []byte{
		PD5GMM,
		0x00,
		MsgRegistrationRequest,
		0xC1, // half-octet ngKSI IE
		ieReplayedUECaps, byte(len(caps)),
	}
	req = append(req, caps...)

	got, err := ExtractUESecurityCapabilities(req)
	if err != nil {
		t.Fatalf("extract capabilities: %v", err)
	}
	if !bytes.Equal(got, caps) {
		t.Fatalf("extracted capabilities %x != sent %x", got, caps)
	}

	if _, err := ExtractUESecurityCapabilities(req[:3]); !errors.Is(err, ErrTruncated) {
		t.Fatalf("expected ErrTruncated, got %v", err)
	}
}

// TestBuildSecurityModeReject checks the plain NAS reject encoding.
func TestBuildSecurityModeReject(t *testing.T) {
	got := BuildSecurityModeReject(CauseUECapabilitiesMismatch)
	want := []byte{PD5GMM, 0x00, MsgSecurityModeReject, 0x59, CauseUECapabilitiesMismatch}
	if !bytes.Equal(got, want) {
		t.Fatalf("reject encoding %x != %x", got, want)
	}
}

// TestAESCMACRFC4493 pins the NIA2 primitive against RFC 4493 test vectors.
func TestAESCMACRFC4493(t *testing.T) {
	key := mustHex(t, "2b7e151628aed2a6abf7158809cf4f3c")
	cases := []struct {
		msg string
		tag string
	}{
		{"", "bb1d6929e95937287fa37d129b756746"},
		{"6bc1bee22e409f96e93d7e117393172a", "070a16b46b4d4144f79bdd9dd04a287c"},
		{
			"6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e5130c81c46a35ce411",
			"dfa66747de9ae63030ca32611497c827",
		},
		{
			"6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e51" +
				"30c81c46a35ce411e5fbc1191a0a52eff69f2445df4f9b17ad2b417be66c3710",
			"51f0bebf7e3b9d92fc49741779363cfe",
		},
	}
	for _, tc := range cases {
		tag, err := aesCMAC(key, mustHex(t, tc.msg))
		if err != nil {
			t.Fatalf("AES-CMAC: %v", err)
		}
		if hex.EncodeToString(tag[:]) != tc.tag {
			t.Fatalf("AES-CMAC(%s) = %x, want %s", tc.msg, tag, tc.tag)
		}
	}
}

// TestKDFDeterministic checks that key derivation is stable and sensitive to
// both the algorithm identifier and the input key.
func TestKDFDeterministic(t *testing.T) {
	kamf := testKAMF()
	k1, err := DeriveNASIntegrityKey(kamf, NIA2)
	if err != nil {
		t.Fatalf("derive K_nasint: %v", err)
	}
	k2, err := DeriveNASIntegrityKey(kamf, NIA2)
	if err != nil {
		t.Fatalf("derive K_nasint again: %v", err)
	}
	if !bytes.Equal(k1, k2) {
		t.Fatal("key derivation must be deterministic")
	}
	kOtherAlg, err := DeriveNASIntegrityKey(kamf, NIA1)
	if err != nil {
		t.Fatalf("derive NIA1 key: %v", err)
	}
	if bytes.Equal(k1, kOtherAlg) {
		t.Fatal("K_nasint must depend on the algorithm identifier")
	}
	kEnc, err := DeriveNASEncryptionKey(kamf, 2)
	if err != nil {
		t.Fatalf("derive K_nasenc: %v", err)
	}
	if bytes.Equal(k1, kEnc) {
		t.Fatal("K_nasint and K_nasenc must differ (different FC)")
	}
	if _, err := DeriveNASIntegrityKey(make([]byte, 16), NIA2); err == nil {
		t.Fatal("short K_AMF must be rejected")
	}
}

// TestParseProtectedMessageErrors covers outer-layer malformed inputs.
func TestParseProtectedMessageErrors(t *testing.T) {
	if _, err := ParseProtectedMessage([]byte{PD5GMM, SHTPlainNAS}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("expected ErrTruncated, got %v", err)
	}
	plain := buildInnerSMC(2, 2, ueSecurityCapabilities(0xE0, 0xE0, 0xE0, 0xE0))
	if _, err := ParseProtectedMessage(plain); err == nil {
		t.Fatal("plain SMC must not parse as a protected message")
	}
	ciphered := append([]byte{PD5GMM, SHTIntegrityAndCiphered, 0, 0, 0, 0, 1}, plain...)
	if _, err := ParseProtectedMessage(ciphered); err == nil {
		t.Fatal("ciphered SMC must be rejected before security context establishment")
	}
}
