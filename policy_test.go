package netsec

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"math/big"
	"testing"
)

// fakeVerifier records which (DS, DNSKEY) pairs the policy asked it to
// validate and only accepts keys whose tag is in passTags.
type fakeVerifier struct {
	passTags map[uint16]bool
	calls    []uint16
	err      error
}

func (f *fakeVerifier) verify(_ DSRecord, key DNSKeyRecord) error {
	f.calls = append(f.calls, key.KeyTag)
	if f.passTags[key.KeyTag] {
		return nil
	}
	if f.err != nil {
		return f.err
	}
	return errors.New("chain verification failed")
}

// rsaBlob builds an RFC 3110 public key blob whose modulus is exactly
// modulusBits long, letting tests pin precise strength-boundary sizes.
func rsaBlob(t *testing.T, exponent, modulusBits int) []byte {
	t.Helper()
	modulusLen := (modulusBits + 7) / 8
	modulus := new(big.Int).Lsh(big.NewInt(1), uint(modulusBits-1))

	expBytes := big.NewInt(int64(exponent)).Bytes()
	var blob []byte
	if len(expBytes) <= 255 {
		blob = append(blob, byte(len(expBytes)))
	} else {
		blob = append(blob, 0, byte(len(expBytes)>>8), byte(len(expBytes)))
	}
	blob = append(blob, expBytes...)

	modBytes := modulus.Bytes()
	if len(modBytes) < modulusLen {
		blob = append(blob, make([]byte, modulusLen-len(modBytes))...)
	}
	blob = append(blob, modBytes...)
	return blob
}

// rsaBlobFromKey serializes a generated RSA public key in RFC 3110 form.
func rsaBlobFromKey(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	return rsaBlob(t, key.PublicKey.E, key.PublicKey.N.BitLen())
}

func TestWeakChainCannotRescueMissingStrongChain(t *testing.T) {
	policy := Policy{MinRSABits: 2048}
	ds := []DSRecord{
		{KeyTag: 100, Algorithm: AlgorithmRSASHA256},
		{KeyTag: 200, Algorithm: AlgorithmRSASHA512},
	}
	keys := []DNSKeyRecord{
		{KeyTag: 100, Algorithm: AlgorithmRSASHA256, PublicKey: rsaBlob(t, 65537, 2048)},
	}
	verifier := &fakeVerifier{passTags: map[uint16]bool{100: true}}

	decision := policy.Evaluate(ds, keys, verifier.verify)

	if decision.Status != StatusBogus {
		t.Fatalf("status = %s, want bogus; weak chain must not rescue missing strong chain", decision.Status)
	}
	if decision.Algorithm != AlgorithmRSASHA512 {
		t.Fatalf("algorithm = %d, want strongest %d", decision.Algorithm, AlgorithmRSASHA512)
	}
	if decision.Reason != ReasonNoAcceptableDNSKEY {
		t.Fatalf("reason = %q, want %q", decision.Reason, ReasonNoAcceptableDNSKEY)
	}
	if len(verifier.calls) != 0 {
		t.Fatalf("verifier invoked for %v, must never run for weaker algorithm", verifier.calls)
	}
}

func TestWeakChainPassingStrongChainFailingIsBogus(t *testing.T) {
	policy := Policy{MinRSABits: 2048}
	ds := []DSRecord{
		{KeyTag: 100, Algorithm: AlgorithmRSASHA256},
		{KeyTag: 200, Algorithm: AlgorithmRSASHA512},
	}
	keys := []DNSKeyRecord{
		{KeyTag: 100, Algorithm: AlgorithmRSASHA256, PublicKey: rsaBlob(t, 65537, 2048)},
		{KeyTag: 200, Algorithm: AlgorithmRSASHA512, PublicKey: rsaBlob(t, 65537, 2048)},
	}
	verifier := &fakeVerifier{passTags: map[uint16]bool{100: true}}

	decision := policy.Evaluate(ds, keys, verifier.verify)

	if decision.Status != StatusBogus {
		t.Fatalf("status = %s, want bogus", decision.Status)
	}
	if decision.Reason != ReasonChainFailed {
		t.Fatalf("reason = %q, want %q", decision.Reason, ReasonChainFailed)
	}
	if decision.Err == nil {
		t.Fatal("expected the chain error to be surfaced")
	}
	if len(verifier.calls) != 1 || verifier.calls[0] != 200 {
		t.Fatalf("verifier calls = %v, want only strongest key tag 200", verifier.calls)
	}
}

func TestDisabledAlgorithmsAreIgnored(t *testing.T) {
	tests := []struct {
		name      string
		algorithm uint8
	}{
		{"RSAMD5", AlgorithmRSAMD5},
		{"DSA", AlgorithmDSA},
		{"DSA-NSEC3-SHA1", AlgorithmDSANSEC3SHA1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := Policy{}
			ds := []DSRecord{{KeyTag: 1, Algorithm: tt.algorithm}}
			keys := []DNSKeyRecord{{KeyTag: 1, Algorithm: tt.algorithm, PublicKey: rsaBlob(t, 65537, 2048)}}
			verifier := &fakeVerifier{passTags: map[uint16]bool{1: true}}

			decision := policy.Evaluate(ds, keys, verifier.verify)

			if decision.Status != StatusInsecure {
				t.Fatalf("status = %s, want insecure when only a disabled algorithm remains", decision.Status)
			}
			if decision.Reason != ReasonNoUsableAlgorithm {
				t.Fatalf("reason = %q, want %q", decision.Reason, ReasonNoUsableAlgorithm)
			}
			if len(verifier.calls) != 0 {
				t.Fatalf("verifier invoked for disabled algorithm %v", verifier.calls)
			}
		})
	}
}

func TestCallerDisabledAlgorithm(t *testing.T) {
	policy := Policy{DisabledAlgorithms: []uint8{AlgorithmRSASHA256}}
	ds := []DSRecord{{KeyTag: 1, Algorithm: AlgorithmRSASHA256}}
	keys := []DNSKeyRecord{{KeyTag: 1, Algorithm: AlgorithmRSASHA256, PublicKey: rsaBlob(t, 65537, 2048)}}

	if !policy.IsDisabled(AlgorithmRSASHA256) {
		t.Fatal("caller-listed algorithm must be disabled")
	}
	decision := policy.Evaluate(ds, keys, (&fakeVerifier{}).verify)
	if decision.Status != StatusInsecure {
		t.Fatalf("status = %s, want insecure", decision.Status)
	}
}

func TestShortRSAKeyIsUnusable(t *testing.T) {
	policy := Policy{MinRSABits: 2048}
	ds := []DSRecord{
		{KeyTag: 100, Algorithm: AlgorithmRSASHA256},
		{KeyTag: 200, Algorithm: AlgorithmRSASHA512},
	}
	keys := []DNSKeyRecord{
		{KeyTag: 100, Algorithm: AlgorithmRSASHA256, PublicKey: rsaBlob(t, 65537, 2048)},
		{KeyTag: 200, Algorithm: AlgorithmRSASHA512, PublicKey: rsaBlob(t, 65537, 1024)},
	}
	verifier := &fakeVerifier{passTags: map[uint16]bool{100: true}}

	decision := policy.Evaluate(ds, keys, verifier.verify)

	if decision.Status != StatusBogus {
		t.Fatalf("status = %s, want bogus for too-short strongest key", decision.Status)
	}
	if decision.Reason != ReasonNoAcceptableDNSKEY {
		t.Fatalf("reason = %q, want %q", decision.Reason, ReasonNoAcceptableDNSKEY)
	}
	if len(verifier.calls) != 0 {
		t.Fatalf("verifier invoked for %v; short strong key and weak key must not be checked", verifier.calls)
	}
}

func TestOnlyDisabledAlgorithmsRemainingIsInsecure(t *testing.T) {
	policy := Policy{DisabledAlgorithms: []uint8{AlgorithmRSASHA1}}
	ds := []DSRecord{
		{KeyTag: 1, Algorithm: AlgorithmRSAMD5},
		{KeyTag: 2, Algorithm: AlgorithmDSA},
		{KeyTag: 3, Algorithm: AlgorithmRSASHA1},
	}
	keys := []DNSKeyRecord{
		{KeyTag: 1, Algorithm: AlgorithmRSAMD5, PublicKey: rsaBlob(t, 65537, 4096)},
		{KeyTag: 3, Algorithm: AlgorithmRSASHA1, PublicKey: rsaBlob(t, 65537, 4096)},
	}

	decision := policy.Evaluate(ds, keys, (&fakeVerifier{}).verify)
	if decision.Status != StatusInsecure || decision.Reason != ReasonNoUsableAlgorithm {
		t.Fatalf("got status=%s reason=%q, want insecure/no-usable-algorithm", decision.Status, decision.Reason)
	}
}

func TestLongEnoughStrongKeyVerifiesSecure(t *testing.T) {
	policy := Policy{MinRSABits: 2048}
	ds := []DSRecord{
		{KeyTag: 100, Algorithm: AlgorithmRSASHA256},
		{KeyTag: 200, Algorithm: AlgorithmRSASHA512},
	}
	keys := []DNSKeyRecord{
		{KeyTag: 100, Algorithm: AlgorithmRSASHA256, PublicKey: rsaBlob(t, 65537, 2048)},
		{KeyTag: 200, Algorithm: AlgorithmRSASHA512, PublicKey: rsaBlob(t, 65537, 2048)},
	}
	verifier := &fakeVerifier{passTags: map[uint16]bool{200: true}}

	decision := policy.Evaluate(ds, keys, verifier.verify)

	if decision.Status != StatusSecure {
		t.Fatalf("status = %s, want secure", decision.Status)
	}
	if decision.Algorithm != AlgorithmRSASHA512 {
		t.Fatalf("algorithm = %d, want %d", decision.Algorithm, AlgorithmRSASHA512)
	}
	if len(verifier.calls) != 1 || verifier.calls[0] != 200 {
		t.Fatalf("verifier calls = %v, want only strongest key", verifier.calls)
	}
}

func TestRSAKeyAtBoundary(t *testing.T) {
	policy := Policy{MinRSABits: 2048}
	ds := []DSRecord{{KeyTag: 200, Algorithm: AlgorithmRSASHA512}}

	short := []DNSKeyRecord{{KeyTag: 200, Algorithm: AlgorithmRSASHA512, PublicKey: rsaBlob(t, 65537, 2047)}}
	if decision := policy.Evaluate(ds, short, (&fakeVerifier{}).verify); decision.Status != StatusBogus {
		t.Fatalf("2047-bit key: status = %s, want bogus", decision.Status)
	}

	exact := []DNSKeyRecord{{KeyTag: 200, Algorithm: AlgorithmRSASHA512, PublicKey: rsaBlob(t, 65537, 2048)}}
	verifier := &fakeVerifier{passTags: map[uint16]bool{200: true}}
	if decision := policy.Evaluate(ds, exact, verifier.verify); decision.Status != StatusSecure {
		t.Fatalf("2048-bit key: status = %s, want secure", decision.Status)
	}
}

func TestNonRSAAlgorithmBypassesRSAGate(t *testing.T) {
	policy := Policy{MinRSABits: 4096}
	ds := []DSRecord{
		{KeyTag: 10, Algorithm: AlgorithmECDSAP256},
		{KeyTag: 16, Algorithm: AlgorithmED448},
	}
	keys := []DNSKeyRecord{
		{KeyTag: 10, Algorithm: AlgorithmECDSAP256, PublicKey: []byte{0x01, 0x02}},
		{KeyTag: 16, Algorithm: AlgorithmED448, PublicKey: []byte{0x03, 0x04}},
	}
	verifier := &fakeVerifier{passTags: map[uint16]bool{16: true}}

	decision := policy.Evaluate(ds, keys, verifier.verify)

	if decision.Status != StatusSecure || decision.Algorithm != AlgorithmED448 {
		t.Fatalf("got status=%s algorithm=%d, want secure with ED448", decision.Status, decision.Algorithm)
	}
}

func TestMalformedRSABlobIsUnusable(t *testing.T) {
	policy := Policy{MinRSABits: 2048}
	ds := []DSRecord{{KeyTag: 200, Algorithm: AlgorithmRSASHA512}}
	keys := []DNSKeyRecord{
		{KeyTag: 200, Algorithm: AlgorithmRSASHA512, PublicKey: []byte{0x05, 0x01, 0x00}},
	}

	decision := policy.Evaluate(ds, keys, (&fakeVerifier{}).verify)
	if decision.Status != StatusBogus || decision.Reason != ReasonNoAcceptableDNSKEY {
		t.Fatalf("got status=%s reason=%q, want bogus/no-acceptable-dnskey", decision.Status, decision.Reason)
	}
}

func TestRealGeneratedRSAKeyModulusLength(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	blob := rsaBlobFromKey(t, key)

	bits, ok := rsaModulusBits(blob)
	if !ok {
		t.Fatal("failed to parse generated key blob")
	}
	if bits != key.N.BitLen() {
		t.Fatalf("parsed bits = %d, want %d", bits, key.N.BitLen())
	}
}

func TestRSAModulusBitsParsing(t *testing.T) {
	if _, ok := rsaModulusBits(nil); ok {
		t.Fatal("empty blob must not parse")
	}
	if _, ok := rsaModulusBits([]byte{9, 1, 2, 3}); ok {
		t.Fatal("truncated blob must not parse")
	}
	if _, ok := rsaModulusBits([]byte{0, 1, 0}); ok {
		t.Fatal("blob with only a long exponent prefix must not parse")
	}
	if bits, ok := rsaModulusBits([]byte{1, 1, 0x80}); !ok || bits != 8 {
		t.Fatalf("single-byte prefix: bits=%d ok=%v, want 8/true", bits, ok)
	}
	blob := rsaBlob(t, 1<<20, 16)
	if bits, ok := rsaModulusBits(blob); !ok || bits != 16 {
		t.Fatalf("two-byte prefix: bits=%d ok=%v, want 16/true", bits, ok)
	}
}

func TestStatusString(t *testing.T) {
	cases := map[Status]string{
		StatusInsecure: "insecure",
		StatusSecure:   "secure",
		StatusBogus:    "bogus",
	}
	for status, want := range cases {
		if got := status.String(); got != want {
			t.Fatalf("status %d string = %q, want %q", status, got, want)
		}
	}
}

func TestEmptyDSIsInsecure(t *testing.T) {
	decision := (Policy{}).Evaluate(nil, nil, (&fakeVerifier{}).verify)
	if decision.Status != StatusInsecure || decision.Reason != ReasonNoUsableAlgorithm {
		t.Fatalf("got status=%s reason=%q, want insecure/no-usable-algorithm", decision.Status, decision.Reason)
	}
}
