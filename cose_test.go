package netsec

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
)

var testCtx = Context{
	Tenant:  "tenant-a",
	MsgType: "cmd",
	Version: "v1",
	Purpose: "gateway-exec",
}

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x42}, 64)))
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

// protectedMap builds a canonically encoded protected header map.
func protectedMap(tenant, msgType string, kid []byte) []byte {
	var e cborEncoder
	e.head(majorMap, 4)
	e.encodeInt(LabelAlgorithm)
	e.encodeInt(AlgEdDSA)
	e.encodeInt(LabelKeyID)
	e.bstr(kid)
	e.encodeInt(LabelTenant)
	e.tstr(tenant)
	e.encodeInt(LabelMsgType)
	e.tstr(msgType)
	return e.buf
}

var emptyMap = []byte{0xa0}

// signMessage signs Sig_structure over the exact protected bytes given and
// assembles the COSE_Sign1 envelope.
func signMessage(priv ed25519.PrivateKey, protectedRaw, unprotectedRaw, payload, aad []byte) []byte {
	sig := ed25519.Sign(priv, sigStructure(protectedRaw, aad, payload))
	var e cborEncoder
	e.array(4)
	e.bstr(protectedRaw)
	e.buf = append(e.buf, unprotectedRaw...)
	e.bstr(payload)
	e.bstr(sig)
	return e.buf
}

func signWithCtx(priv ed25519.PrivateKey, ctx Context, payload []byte) []byte {
	return signMessage(priv, protectedMap(ctx.Tenant, ctx.MsgType, []byte("kid1")), emptyMap, payload, ctx.ExternalAAD())
}

func TestVerifyValidMessage(t *testing.T) {
	pub, priv := newKey(t)
	payload := []byte(`{"op":"reboot"}`)
	msg := signWithCtx(priv, testCtx, payload)

	got, err := VerifySign1(pub, msg, testCtx)
	if err != nil {
		t.Fatalf("VerifySign1: %v", err)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Fatalf("payload = %q, want %q", got.Payload, payload)
	}
	if got.Tenant != testCtx.Tenant || got.MsgType != testCtx.MsgType {
		t.Fatalf("context = %q/%q", got.Tenant, got.MsgType)
	}
	if !bytes.Equal(got.KeyID, []byte("kid1")) {
		t.Fatalf("kid = %q", got.KeyID)
	}
}

func TestVerifyTaggedSign1(t *testing.T) {
	pub, priv := newKey(t)
	msg := signWithCtx(priv, testCtx, []byte("ping"))
	tagged := append([]byte{0xd2}, msg...) // tag(18)
	if _, err := VerifySign1(pub, tagged, testCtx); err != nil {
		t.Fatalf("VerifySign1 tagged: %v", err)
	}
}

// Cross-tenant replay: a message signed for tenant-a is presented to the
// gateway of tenant-b.
func TestCrossTenantReplayRejected(t *testing.T) {
	pub, priv := newKey(t)
	msg := signWithCtx(priv, testCtx, []byte("payload"))

	other := testCtx
	other.Tenant = "tenant-b"
	if _, err := VerifySign1(pub, msg, other); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("err = %v, want ErrTenantMismatch", err)
	}
}

// The attacker rewrites a tenant header carried in the unprotected bucket
// (which they can modify) to replay the payload into another tenant. The
// external_aad tenant binding must still defeat the replay.
func TestCrossTenantUnprotectedRewriteRejected(t *testing.T) {
	pub, priv := newKey(t)
	payload := []byte("payload")

	var unprot cborEncoder
	unprot.head(majorMap, 1)
	unprot.encodeInt(LabelTenant)
	unprot.tstr("tenant-a")

	var prot cborEncoder
	prot.head(majorMap, 3)
	prot.encodeInt(LabelAlgorithm)
	prot.encodeInt(AlgEdDSA)
	prot.encodeInt(LabelKeyID)
	prot.bstr([]byte("kid1"))
	prot.encodeInt(LabelMsgType)
	prot.tstr(testCtx.MsgType)

	msg := signMessage(priv, prot.buf, unprot.buf, payload, testCtx.ExternalAAD())
	if _, err := VerifySign1(pub, msg, testCtx); err != nil {
		t.Fatalf("sanity verify: %v", err)
	}

	// Attacker swaps the unprotected tenant header, keeping the signature.
	var unprotB cborEncoder
	unprotB.head(majorMap, 1)
	unprotB.encodeInt(LabelTenant)
	unprotB.tstr("tenant-b")
	forged := signMessageBytes(t, msg, prot.buf, unprotB.buf, payload)

	ctxB := testCtx
	ctxB.Tenant = "tenant-b"
	if _, err := VerifySign1(pub, forged, ctxB); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("err = %v, want ErrSignatureMismatch", err)
	}
}

// signMessageBytes reassembles an envelope reusing the signature from an
// existing message (attacker forgery helper).
func signMessageBytes(t *testing.T, orig, protectedRaw, unprotectedRaw, payload []byte) []byte {
	t.Helper()
	env, err := parseSign1(orig)
	if err != nil {
		t.Fatalf("parseSign1: %v", err)
	}
	var e cborEncoder
	e.array(4)
	e.bstr(protectedRaw)
	e.buf = append(e.buf, unprotectedRaw...)
	e.bstr(payload)
	e.bstr(env.signature)
	return e.buf
}

// Header movement: moving alg from the protected to the unprotected bucket
// changes body_protected and must invalidate the signature.
func TestHeaderMovementRejected(t *testing.T) {
	pub, priv := newKey(t)
	payload := []byte("payload")
	msg := signWithCtx(priv, testCtx, payload)

	// Protected map without alg; alg placed in the unprotected bucket.
	var prot cborEncoder
	prot.head(majorMap, 3)
	prot.encodeInt(LabelKeyID)
	prot.bstr([]byte("kid1"))
	prot.encodeInt(LabelTenant)
	prot.tstr(testCtx.Tenant)
	prot.encodeInt(LabelMsgType)
	prot.tstr(testCtx.MsgType)

	var unprot cborEncoder
	unprot.head(majorMap, 1)
	unprot.encodeInt(LabelAlgorithm)
	unprot.encodeInt(AlgEdDSA)

	forged := signMessageBytes(t, msg, prot.buf, unprot.buf, payload)
	if _, err := VerifySign1(pub, forged, testCtx); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("moved alg: err = %v, want ErrSignatureMismatch", err)
	}
}

// A label present in both buckets must be rejected even when the message is
// otherwise validly signed.
func TestDuplicateLabelAcrossBucketsRejected(t *testing.T) {
	pub, priv := newKey(t)
	payload := []byte("payload")

	for _, tc := range []struct {
		name  string
		label int64
		value func(e *cborEncoder)
	}{
		{"alg", LabelAlgorithm, func(e *cborEncoder) { e.encodeInt(AlgEdDSA) }},
		{"kid", LabelKeyID, func(e *cborEncoder) { e.bstr([]byte("kid1")) }},
		{"tenant", LabelTenant, func(e *cborEncoder) { e.tstr(testCtx.Tenant) }},
		{"msgtype", LabelMsgType, func(e *cborEncoder) { e.tstr(testCtx.MsgType) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var unprot cborEncoder
			unprot.head(majorMap, 1)
			unprot.encodeInt(tc.label)
			tc.value(&unprot)
			// Validly signed, but the label sits in both buckets.
			msg := signMessage(priv, protectedMap(testCtx.Tenant, testCtx.MsgType, []byte("kid1")), unprot.buf, payload, testCtx.ExternalAAD())
			if _, err := VerifySign1(pub, msg, testCtx); !errors.Is(err, ErrDuplicateLabel) {
				t.Fatalf("err = %v, want ErrDuplicateLabel", err)
			}
		})
	}
}

// Non-canonical but definite-length protected headers: the signature is
// computed over the exact bytes, so verification must succeed; re-encoding
// the same headers canonically with the same signature must fail.
func TestNonCanonicalSignedEncoding(t *testing.T) {
	pub, priv := newKey(t)
	payload := []byte("payload")

	// map(4) with non-shortest-form head and integer encodings, unsorted keys.
	nonCanonical := []byte{
		0xb8, 0x04, // map(4), non-canonical
		0x18, 0x04, 0x44, 'k', 'i', 'd', '1', // 4: h'6b696431'
		0x18, 0x01, 0x38, 0x07, // 1: -8
		0x3b, 0, 0, 0, 0, 0, 1, 0x11, 0x71, 0x63, 'c', 'm', 'd', // -70002: "cmd"
		0x3b, 0, 0, 0, 0, 0, 1, 0x11, 0x70, 0x68, 't', 'e', 'n', 'a', 'n', 't', '-', 'a', // -70001: "tenant-a"
	}

	msg := signMessage(priv, nonCanonical, emptyMap, payload, testCtx.ExternalAAD())
	got, err := VerifySign1(pub, msg, testCtx)
	if err != nil {
		t.Fatalf("non-canonical signed encoding must verify: %v", err)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Fatalf("payload = %q", got.Payload)
	}

	// Same semantic headers re-encoded canonically: different signed bytes.
	canonical := protectedMap(testCtx.Tenant, testCtx.MsgType, []byte("kid1"))
	if bytes.Equal(canonical, nonCanonical) {
		t.Fatal("test bug: encodings should differ")
	}
	reencoded := signMessageBytes(t, msg, canonical, emptyMap, payload)
	if _, err := VerifySign1(pub, reencoded, testCtx); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("re-encoded: err = %v, want ErrSignatureMismatch", err)
	}
}

func TestTamperedPayloadRejected(t *testing.T) {
	pub, priv := newKey(t)
	msg := signWithCtx(priv, testCtx, []byte("payload"))
	env, err := parseSign1(msg)
	if err != nil {
		t.Fatalf("parseSign1: %v", err)
	}
	tampered := append([]byte(nil), env.payload...)
	tampered[0] ^= 0xff
	forged := signMessageBytes(t, msg, env.protectedRaw, env.unprotectedRaw, tampered)

	got, err := VerifySign1(pub, forged, testCtx)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("err = %v, want ErrSignatureMismatch", err)
	}
	if got != nil {
		t.Fatal("payload must not be exposed before successful verification")
	}
}

func TestIndefiniteLengthRejected(t *testing.T) {
	pub, _ := newKey(t)
	// Indefinite-length array containing the Sign1 items.
	indefArray := []byte{0x9f, 0x40, 0xa0, 0x40, 0x40, 0xff}
	if _, err := VerifySign1(pub, indefArray, testCtx); !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("indef array: err = %v, want ErrMalformedMessage", err)
	}
	// Indefinite-length byte string as protected headers.
	indefBstr := []byte{0x84, 0x5f, 0x40, 0xff, 0xa0, 0x40, 0x40}
	if _, err := VerifySign1(pub, indefBstr, testCtx); !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("indef bstr: err = %v, want ErrMalformedMessage", err)
	}
}

func TestUnsupportedAlgorithmRejected(t *testing.T) {
	pub, priv := newKey(t)
	payload := []byte("payload")

	var prot cborEncoder
	prot.head(majorMap, 3)
	prot.encodeInt(LabelAlgorithm)
	prot.encodeInt(-7) // ES256
	prot.encodeInt(LabelTenant)
	prot.tstr(testCtx.Tenant)
	prot.encodeInt(LabelMsgType)
	prot.tstr(testCtx.MsgType)
	msg := signMessage(priv, prot.buf, emptyMap, payload, testCtx.ExternalAAD())
	if _, err := VerifySign1(pub, msg, testCtx); !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("ES256: err = %v, want ErrUnsupportedAlgorithm", err)
	}

	// No alg anywhere.
	var protNoAlg cborEncoder
	protNoAlg.head(majorMap, 2)
	protNoAlg.encodeInt(LabelTenant)
	protNoAlg.tstr(testCtx.Tenant)
	protNoAlg.encodeInt(LabelMsgType)
	protNoAlg.tstr(testCtx.MsgType)
	msg = signMessage(priv, protNoAlg.buf, emptyMap, payload, testCtx.ExternalAAD())
	if _, err := VerifySign1(pub, msg, testCtx); !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("no alg: err = %v, want ErrUnsupportedAlgorithm", err)
	}
}

// external_aad binds protocol version and purpose: verifying under a
// different version or purpose must fail even with matching headers.
func TestAADBindsVersionAndPurpose(t *testing.T) {
	pub, priv := newKey(t)
	msg := signWithCtx(priv, testCtx, []byte("payload"))

	otherVersion := testCtx
	otherVersion.Version = "v2"
	if _, err := VerifySign1(pub, msg, otherVersion); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("version: err = %v, want ErrSignatureMismatch", err)
	}
	otherPurpose := testCtx
	otherPurpose.Purpose = "telemetry"
	if _, err := VerifySign1(pub, msg, otherPurpose); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("purpose: err = %v, want ErrSignatureMismatch", err)
	}
	otherMsgType := testCtx
	otherMsgType.MsgType = "status"
	if _, err := VerifySign1(pub, msg, otherMsgType); !errors.Is(err, ErrMsgTypeMismatch) {
		t.Fatalf("msgtype: err = %v, want ErrMsgTypeMismatch", err)
	}
}

func TestWrongKeyAndSignatureRejected(t *testing.T) {
	_, priv := newKey(t)
	msg := signWithCtx(priv, testCtx, []byte("payload"))

	otherPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if _, err := VerifySign1(otherPub, msg, testCtx); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("wrong key: err = %v, want ErrSignatureMismatch", err)
	}

	truncated := msg[:len(msg)-10]
	if _, err := VerifySign1(pub(priv), truncated, testCtx); !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("truncated: err = %v, want ErrMalformedMessage", err)
	}
}

func pub(priv ed25519.PrivateKey) ed25519.PublicKey {
	return priv.Public().(ed25519.PublicKey)
}

func TestTrailingDataRejected(t *testing.T) {
	pub, priv := newKey(t)
	msg := append(signWithCtx(priv, testCtx, []byte("payload")), 0x00)
	if _, err := VerifySign1(pub, msg, testCtx); !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("err = %v, want ErrMalformedMessage", err)
	}
}
