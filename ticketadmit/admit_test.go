package ticketadmit

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

func testRing(t *testing.T) (*KeyRing, *Key, *Key) {
	t.Helper()
	current, err := GenerateKey(1)
	if err != nil {
		t.Fatalf("GenerateKey current: %v", err)
	}
	previous, err := GenerateKey(0)
	if err != nil {
		t.Fatalf("GenerateKey previous: %v", err)
	}
	ring, err := NewKeyRing(current, previous)
	if err != nil {
		t.Fatalf("NewKeyRing: %v", err)
	}
	return ring, current, previous
}

func sampleFingerprint(t *testing.T) []byte {
	t.Helper()
	fp := make([]byte, fingerprintSize)
	if _, err := rand.Read(fp); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return fp
}

func sampleClaims(t *testing.T, fingerprint []byte) Claims {
	t.Helper()
	return Claims{
		SNI:                   "lenient.example",
		ALPN:                  "h2",
		ClientCertFingerprint: fingerprint,
		CipherSuite:           0x1301,
		IssuedAt:              time.Now().Add(-time.Minute).UTC(),
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	ring, _, _ := testRing(t)
	admitter, err := NewAdmitter(ring)
	if err != nil {
		t.Fatalf("NewAdmitter: %v", err)
	}
	fp := sampleFingerprint(t)
	ticket, err := admitter.Issue(sampleClaims(t, fp))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	opened, err := ring.Open(ticket.Raw)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened.KeyGeneration != ticket.KeyGeneration {
		t.Errorf("key generation = %d, want %d", opened.KeyGeneration, ticket.KeyGeneration)
	}
	if opened.Claims.SNI != "lenient.example" || opened.Claims.ALPN != "h2" {
		t.Errorf("claims mismatch: %+v", opened.Claims)
	}
	if !bytes.Equal(opened.Claims.ClientCertFingerprint, fp) {
		t.Errorf("fingerprint not preserved")
	}
	if opened.Claims.CipherSuite != 0x1301 {
		t.Errorf("cipher suite not preserved: %x", opened.Claims.CipherSuite)
	}
	if opened.AgeAdd != ticket.AgeAdd {
		t.Errorf("age add not preserved: got %d want %d", opened.AgeAdd, ticket.AgeAdd)
	}
}

func TestSealOpenWithoutCertificate(t *testing.T) {
	ring, _, _ := testRing(t)
	admitter, _ := NewAdmitter(ring)
	ticket, err := admitter.Issue(sampleClaims(t, nil))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	opened, err := ring.Open(ticket.Raw)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(opened.Claims.ClientCertFingerprint) != 0 {
		t.Errorf("expected absent fingerprint, got %x", opened.Claims.ClientCertFingerprint)
	}
}

func TestPreviousGenerationKeyOpens(t *testing.T) {
	ring, _, previous := testRing(t)

	// Seal with the previous generation directly.
	oldRing, err := NewKeyRing(previous, nil)
	if err != nil {
		t.Fatalf("old ring: %v", err)
	}
	oldAdmitter, _ := NewAdmitter(oldRing)
	ticket, err := oldAdmitter.Issue(sampleClaims(t, nil))
	if err != nil {
		t.Fatalf("Issue old: %v", err)
	}

	// The two-generation ring must still open it.
	opened, err := ring.Open(ticket.Raw)
	if err != nil {
		t.Fatalf("Open previous-generation ticket: %v", err)
	}
	if opened.KeyGeneration != previous.Generation {
		t.Errorf("generation = %d, want previous %d", opened.KeyGeneration, previous.Generation)
	}

	// And admission must succeed using the previous key.
	admitter, _ := NewAdmitter(ring)
	result := admitter.Admit(AdmitRequest{
		RawTicket: ticket.Raw,
		SNI:       "lenient.example",
		ALPN:      "h2",
	})
	if result.Decision != DecisionResume {
		t.Fatalf("decision = %v reason %v, want resume", result.Decision, result.Reason)
	}

	// After rotating again, the old generation becomes three-generations-old
	// relative to the new current key and must be treated as unknown.
	next, err := GenerateKey(2)
	if err != nil {
		t.Fatalf("GenerateKey next: %v", err)
	}
	rotated, err := ring.Rotate(next)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if _, err := rotated.Open(ticket.Raw); !errors.Is(err, ErrUnknownTicket) {
		t.Fatalf("rotated ring open err = %v, want ErrUnknownTicket", err)
	}
}

func TestUnknownTicketNeverHardFails(t *testing.T) {
	ring, _, _ := testRing(t)
	admitter, _ := NewAdmitter(ring)

	cases := map[string][]byte{
		"nil":             nil,
		"short":           []byte("short"),
		"bad magic":       bytesWithBadMagic(t),
		"foreign key":     foreignKeyTicket(t),
		"tampered ticket": tamperedTicket(t, ring),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			result := admitter.Admit(AdmitRequest{RawTicket: raw, SNI: "any.example"})
			if result.Decision != DecisionReject || result.Reason != ReasonUnknownTicket {
				t.Fatalf("decision=%v reason=%v, want reject/unknown", result.Decision, result.Reason)
			}
			if !errors.Is(result.Err, ErrUnknownTicket) {
				t.Fatalf("err = %v, want ErrUnknownTicket", result.Err)
			}
		})
	}
}

func TestCrossSNIMigrationRejected(t *testing.T) {
	ring, _, _ := testRing(t)
	admitter, _ := NewAdmitter(ring)
	ticket, _ := admitter.Issue(sampleClaims(t, nil))

	result := admitter.Admit(AdmitRequest{
		RawTicket: ticket.Raw,
		SNI:       "strict.example", // different virtual host
		ALPN:      "h2",
	})
	if result.Decision != DecisionReject || result.Reason != ReasonSNIMismatch {
		t.Fatalf("decision=%v reason=%v, want reject/sni mismatch", result.Decision, result.Reason)
	}

	// Same SNI resumes.
	ok := admitter.Admit(AdmitRequest{RawTicket: ticket.Raw, SNI: "lenient.example", ALPN: "h2"})
	if ok.Decision != DecisionResume {
		t.Fatalf("decision=%v reason=%v, want resume", ok.Decision, ok.Reason)
	}
}

func TestCertlessTicketRejectedOnStrictVhost(t *testing.T) {
	ring, _, _ := testRing(t)
	admitter, _ := NewAdmitter(ring)
	ticket, _ := admitter.Issue(sampleClaims(t, nil))

	result := admitter.Admit(AdmitRequest{
		RawTicket: ticket.Raw,
		SNI:       "lenient.example",
		ALPN:      "h2",
		Policy:    VhostPolicy{RequireClientCert: true},
	})
	if result.Decision != DecisionReject || result.Reason != ReasonClientCertRequired {
		t.Fatalf("decision=%v reason=%v, want reject/client cert required", result.Decision, result.Reason)
	}

	// A ticket carrying a fingerprint is accepted on the strict vhost.
	withCert, _ := admitter.Issue(sampleClaims(t, sampleFingerprint(t)))
	ok := admitter.Admit(AdmitRequest{
		RawTicket: withCert.Raw,
		SNI:       "lenient.example",
		ALPN:      "h2",
		Policy:    VhostPolicy{RequireClientCert: true},
	})
	if ok.Decision != DecisionResume {
		t.Fatalf("decision=%v reason=%v, want resume", ok.Decision, ok.Reason)
	}
}

func TestCertTicketRejectedOnNoCertVhost(t *testing.T) {
	ring, _, _ := testRing(t)
	admitter, _ := NewAdmitter(ring)
	ticket, _ := admitter.Issue(sampleClaims(t, sampleFingerprint(t)))

	result := admitter.Admit(AdmitRequest{
		RawTicket: ticket.Raw,
		SNI:       "lenient.example",
		ALPN:      "h2",
		Policy:    VhostPolicy{ExpectNoClientCert: true},
	})
	if result.Decision != DecisionReject || result.Reason != ReasonClientCertNotExpected {
		t.Fatalf("decision=%v reason=%v, want reject/not expected", result.Decision, result.Reason)
	}
}

func TestTicketExpiry(t *testing.T) {
	ring, _, _ := testRing(t)
	admitter, _ := NewAdmitter(ring)

	old := sampleClaims(t, nil)
	old.IssuedAt = time.Now().Add(-MaxTicketAge - time.Hour)
	ticket, err := admitter.Issue(old)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	result := admitter.Admit(AdmitRequest{RawTicket: ticket.Raw, SNI: "lenient.example", ALPN: "h2"})
	if result.Decision != DecisionReject || result.Reason != ReasonTicketExpired {
		t.Fatalf("decision=%v reason=%v, want reject/expired", result.Decision, result.Reason)
	}

	// Boundary: a ticket just inside the window still resumes.
	fresh := sampleClaims(t, nil)
	fresh.IssuedAt = time.Now().Add(-MaxTicketAge + time.Minute)
	freshTicket, _ := admitter.Issue(fresh)
	ok := admitter.Admit(AdmitRequest{RawTicket: freshTicket.Raw, SNI: "lenient.example", ALPN: "h2"})
	if ok.Decision != DecisionResume {
		t.Fatalf("decision=%v reason=%v, want resume inside window", ok.Decision, ok.Reason)
	}
}

func TestALPNMismatch(t *testing.T) {
	ring, _, _ := testRing(t)
	admitter, _ := NewAdmitter(ring)
	ticket, _ := admitter.Issue(sampleClaims(t, nil))

	result := admitter.Admit(AdmitRequest{
		RawTicket: ticket.Raw,
		SNI:       "lenient.example",
		ALPN:      "h3",
	})
	if result.Decision != DecisionReject || result.Reason != ReasonALPNMismatch {
		t.Fatalf("decision=%v reason=%v, want reject/alpn mismatch", result.Decision, result.Reason)
	}
}

func TestCipherSuiteRestriction(t *testing.T) {
	ring, _, _ := testRing(t)
	admitter, _ := NewAdmitter(ring)
	ticket, _ := admitter.Issue(sampleClaims(t, nil))

	blocked := admitter.Admit(AdmitRequest{
		RawTicket: ticket.Raw,
		SNI:       "lenient.example",
		ALPN:      "h2",
		Policy:    VhostPolicy{AllowedCipherSuites: []uint16{0x1302}},
	})
	if blocked.Decision != DecisionReject || blocked.Reason != ReasonCipherSuiteMismatch {
		t.Fatalf("decision=%v reason=%v, want reject/cipher mismatch", blocked.Decision, blocked.Reason)
	}

	allowed := admitter.Admit(AdmitRequest{
		RawTicket: ticket.Raw,
		SNI:       "lenient.example",
		ALPN:      "h2",
		Policy:    VhostPolicy{AllowedCipherSuites: []uint16{0x1301, 0x1302}},
	})
	if allowed.Decision != DecisionResume {
		t.Fatalf("decision=%v reason=%v, want resume", allowed.Decision, allowed.Reason)
	}
}

func TestEarlyDataAgeSkew(t *testing.T) {
	ring, _, _ := testRing(t)
	admitter, _ := NewAdmitter(ring)

	now := time.Now()
	issuedAt := now.Add(-30 * time.Second)
	claims := sampleClaims(t, nil)
	claims.IssuedAt = issuedAt
	ticket, err := admitter.Issue(claims)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Accurate obfuscated age: server age ~30s, client age 30s.
	accurate := uint32((30 * time.Second / time.Millisecond)) + ticket.AgeAdd
	ok := admitter.Admit(AdmitRequest{
		RawTicket:           ticket.Raw,
		SNI:                 "lenient.example",
		ALPN:                "h2",
		EarlyData:           true,
		ObfuscatedTicketAge: accurate,
		Now:                 now,
	})
	if ok.Decision != DecisionResumeEarlyData {
		t.Fatalf("decision=%v reason=%v, want early data", ok.Decision, ok.Reason)
	}

	// Skewed obfuscated age: client claims an age 30s different from the
	// server's. Early data must be refused but resumption must remain allowed.
	skewed := uint32((60 * time.Second / time.Millisecond)) + ticket.AgeAdd
	rejected := admitter.Admit(AdmitRequest{
		RawTicket:           ticket.Raw,
		SNI:                 "lenient.example",
		ALPN:                "h2",
		EarlyData:           true,
		ObfuscatedTicketAge: skewed,
		Now:                 now,
	})
	if rejected.Decision != DecisionResume || rejected.Reason != ReasonEarlyDataAgeSkew {
		t.Fatalf("decision=%v reason=%v, want resume/age skew", rejected.Decision, rejected.Reason)
	}

	// Exactly at the 10s boundary must still be accepted.
	boundary := uint32(((30*time.Second + MaxEarlyDataAgeSkew) / time.Millisecond)) + ticket.AgeAdd
	bres := admitter.Admit(AdmitRequest{
		RawTicket:           ticket.Raw,
		SNI:                 "lenient.example",
		ALPN:                "h2",
		EarlyData:           true,
		ObfuscatedTicketAge: boundary,
		Now:                 now,
	})
	if bres.Decision != DecisionResumeEarlyData {
		t.Fatalf("decision=%v reason=%v at boundary, want early data", bres.Decision, bres.Reason)
	}

	// No early data offered: plain resumption regardless of the skew field.
	plain := admitter.Admit(AdmitRequest{
		RawTicket:           ticket.Raw,
		SNI:                 "lenient.example",
		ALPN:                "h2",
		ObfuscatedTicketAge: skewed,
		Now:                 now,
	})
	if plain.Decision != DecisionResume {
		t.Fatalf("decision=%v reason=%v, want plain resume", plain.Decision, plain.Reason)
	}
}

func TestDeobfuscateWraparound(t *testing.T) {
	// RFC 8446 modular arithmetic: obfuscated = age + ageAdd mod 2^32.
	const ageAdd uint32 = 0xfffffffe
	const ageMillis uint32 = 5000
	// ageAdd + ageMillis wraps modulo 2^32.
	var obfuscated uint32 = ageAdd
	obfuscated += ageMillis
	got := DeobfuscateTicketAge(obfuscated, ageAdd)
	want := time.Duration(ageMillis) * time.Millisecond
	if got != want {
		t.Fatalf("deobfuscated = %v, want %v", got, want)
	}
}

func TestKeyRingRotationValidation(t *testing.T) {
	current, err := GenerateKey(1)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	previous, err := GenerateKey(0)
	if err != nil {
		t.Fatalf("previous: %v", err)
	}

	if _, err := NewKeyRing(nil, previous); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("nil current: err=%v, want ErrInvalidKey", err)
	}
	if _, err := NewKeyRing(current, current); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("duplicate generations: err=%v, want ErrInvalidKey", err)
	}

	ring, err := NewKeyRing(current, previous)
	if err != nil {
		t.Fatalf("ring: %v", err)
	}

	// Rotating onto an already-used generation must fail.
	if _, err := ring.Rotate(previous); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("rotate onto previous generation: err=%v, want ErrInvalidKey", err)
	}

	next, err := GenerateKey(2)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	rotated, err := ring.Rotate(next)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated.current.Generation != 2 || rotated.previous.Generation != 1 {
		t.Fatalf("rotated generations current=%d previous=%d",
			rotated.current.Generation, rotated.previous.Generation)
	}

	// The original ring must be unchanged (rotation is non-destructive).
	if ring.current.Generation != 1 || ring.previous.Generation != 0 {
		t.Fatalf("rotation mutated the original ring")
	}
}

func TestFutureDatedTicketRejected(t *testing.T) {
	ring, _, _ := testRing(t)
	admitter, _ := NewAdmitter(ring)

	future := sampleClaims(t, nil)
	future.IssuedAt = time.Now().Add(MaxTicketAge + time.Hour)
	ticket, err := admitter.Issue(future)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	result := admitter.Admit(AdmitRequest{
		RawTicket: ticket.Raw,
		SNI:       "lenient.example",
		ALPN:      "h2",
	})
	if result.Decision != DecisionReject || result.Reason != ReasonTicketExpired {
		t.Fatalf("decision=%v reason=%v, want reject/expired", result.Decision, result.Reason)
	}
}

func TestMalformedClaimsAfterDecrypt(t *testing.T) {
	ring, _, _ := testRing(t)

	// Hand-craft a valid envelope whose plaintext has a supported version but
	// a truncated body. It must fail AEAD-independent claim parsing rather
	// than panic, and admission must still degrade to a reject/full handshake.
	key := ring.current
	var nonce [nonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	header := make([]byte, fixedHeaderSize)
	copy(header, envelopeMagic[:])
	header[4] = envelopeVersion
	binary.BigEndian.PutUint32(header[5:], key.Generation)
	binary.BigEndian.PutUint32(header[9:], 0x42)
	copy(header[13:], nonce[:])

	body := []byte{claimsVersion, 0x00, 0x01, 'x'} // truncated
	raw := key.aead.Seal(nil, nonce[:], body, header)
	wire := append(header, raw...)

	if _, err := ring.Open(wire); !errors.Is(err, ErrMalformedTicket) {
		t.Fatalf("Open err=%v, want ErrMalformedTicket", err)
	}

	admitter, _ := NewAdmitter(ring)
	result := admitter.Admit(AdmitRequest{RawTicket: wire, SNI: "lenient.example"})
	if result.Decision != DecisionReject {
		t.Fatalf("decision=%v, want reject for malformed claims", result.Decision)
	}
}

// --- ticket-mangling helpers -------------------------------------------------

func bytesWithBadMagic(t *testing.T) []byte {
	t.Helper()
	ring, _, _ := testRing(t)
	admitter, _ := NewAdmitter(ring)
	ticket, err := admitter.Issue(sampleClaims(t, nil))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	raw := append([]byte(nil), ticket.Raw...)
	raw[0] ^= 0xff
	return raw
}

func foreignKeyTicket(t *testing.T) []byte {
	t.Helper()
	foreign, err := GenerateKey(99)
	if err != nil {
		t.Fatalf("foreign key: %v", err)
	}
	foreignRing, err := NewKeyRing(foreign, nil)
	if err != nil {
		t.Fatalf("foreign ring: %v", err)
	}
	admitter, _ := NewAdmitter(foreignRing)
	ticket, err := admitter.Issue(sampleClaims(t, nil))
	if err != nil {
		t.Fatalf("Issue foreign: %v", err)
	}
	return ticket.Raw
}

func tamperedTicket(t *testing.T, ring *KeyRing) []byte {
	t.Helper()
	admitter, _ := NewAdmitter(ring)
	ticket, err := admitter.Issue(sampleClaims(t, nil))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	raw := append([]byte(nil), ticket.Raw...)
	// Flip a bit inside the GCM ciphertext.
	raw[len(raw)-1] ^= 0x01
	return raw
}
