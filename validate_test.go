package netsec

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateAcceptsWellFormedV2Record(t *testing.T) {
	pub, priv := newKeyPair(t)
	verifier := &captureVerifier{pub: pub}
	v := fixedValidator(verifier.verify)

	rec, err := v.Validate(buildRecord(t, baseRecord(), priv))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if rec.Sequence != 10 || rec.TTL != uint64(time.Minute) {
		t.Fatalf("unexpected record fields: %+v", rec)
	}
	if rec.ValidityType != ValidityTypeEOL {
		t.Fatalf("unexpected validity type %d", rec.ValidityType)
	}
	if !rec.EOL().Equal(fixedNow.Add(2 * time.Hour)) {
		t.Fatalf("unexpected EOL %s", rec.EOL())
	}
	wantInput := append([]byte(V2SignaturePrefix), rec.CBORData...)
	if string(verifier.gotData) != string(wantInput) {
		t.Fatalf("verifier did not receive prefixed CBOR input")
	}
	if !strings.HasPrefix(string(verifier.gotData), V2SignaturePrefix) {
		t.Fatalf("signature input missing V2 domain prefix")
	}
	if string(verifier.gotSig) != string(rec.SignatureV2) {
		t.Fatalf("verifier received wrong signature bytes")
	}
}

func TestValidateRejectsV1Signatures(t *testing.T) {
	pub, priv := newKeyPair(t)
	v := fixedValidator((&captureVerifier{pub: pub}).verify)

	t.Run("v1 only", func(t *testing.T) {
		r := baseRecord()
		r.signV1 = true
		r.signV2 = false
		r.omitV2 = true
		_, err := v.Validate(buildRecord(t, r, priv))
		wantErrIs(t, err, ErrV1Signature)
	})

	t.Run("v1 alongside v2", func(t *testing.T) {
		r := baseRecord()
		r.signV1 = true
		_, err := v.Validate(buildRecord(t, r, priv))
		wantErrIs(t, err, ErrV1Signature)
	})
}

func TestValidateRejectsMissingOrBadV2Signature(t *testing.T) {
	pub, priv := newKeyPair(t)

	t.Run("missing v2 signature", func(t *testing.T) {
		r := baseRecord()
		r.signV2 = false
		r.omitV2 = true
		v := fixedValidator((&captureVerifier{pub: pub}).verify)
		_, err := v.Validate(buildRecord(t, r, priv))
		wantErrIs(t, err, ErrMissingV2Signature)
	})

	t.Run("corrupt v2 signature", func(t *testing.T) {
		r := baseRecord()
		r.badV2Sig = true
		v := fixedValidator((&captureVerifier{pub: pub}).verify)
		_, err := v.Validate(buildRecord(t, r, priv))
		wantErrIs(t, err, ErrInvalidSignature)
	})

	t.Run("verifier rejects", func(t *testing.T) {
		v := fixedValidator((&captureVerifier{pub: pub, fail: true}).verify)
		_, err := v.Validate(buildRecord(t, baseRecord(), priv))
		wantErrIs(t, err, ErrInvalidSignature)
	})
}

func TestValidateRejectsCBORFieldMismatch(t *testing.T) {
	pub, priv := newKeyPair(t)
	v := fixedValidator((&captureVerifier{pub: pub}).verify)

	// Pin the signed CBOR payload to the original base values so a protobuf
	// field mutation cannot be hidden by rebuilding CBOR from it.
	pinBaseCBOR := func(r testRecord) testRecord {
		r.cborFields = defaultCBOR(baseRecord())
		return r
	}

	cases := []struct {
		name   string
		tamper func(r testRecord) testRecord
	}{
		{
			name: "cbor value differs",
			tamper: func(r testRecord) testRecord {
				r.cborFields = defaultCBOR(r)
				r.cborFields["Value"] = []byte("/ipfs/bafyreplaced")
				return r
			},
		},
		{
			name: "pb value differs",
			tamper: func(r testRecord) testRecord {
				r = pinBaseCBOR(r)
				r.value = []byte("/ipfs/bafytampered")
				return r
			},
		},
		{
			name: "cbor validity differs",
			tamper: func(r testRecord) testRecord {
				r.cborFields = defaultCBOR(r)
				r.cborFields["Validity"] = []byte("2030-01-01T00:00:00Z")
				return r
			},
		},
		{
			name: "pb validity differs",
			tamper: func(r testRecord) testRecord {
				r = pinBaseCBOR(r)
				r.validity = []byte("2030-01-01T00:00:00Z")
				return r
			},
		},
		{
			name: "cbor validity type differs",
			tamper: func(r testRecord) testRecord {
				r.cborFields = defaultCBOR(r)
				r.cborFields["ValidityType"] = uint64(1)
				return r
			},
		},
		{
			name: "pb validity type differs",
			tamper: func(r testRecord) testRecord {
				r = pinBaseCBOR(r)
				r.validityType = 1
				return r
			},
		},
		{
			name: "cbor sequence differs",
			tamper: func(r testRecord) testRecord {
				r.cborFields = defaultCBOR(r)
				r.cborFields["Sequence"] = r.sequence + 1
				return r
			},
		},
		{
			name: "pb sequence differs",
			tamper: func(r testRecord) testRecord {
				r = pinBaseCBOR(r)
				r.sequence++
				return r
			},
		},
		{
			name: "cbor ttl differs",
			tamper: func(r testRecord) testRecord {
				r.cborFields = defaultCBOR(r)
				r.cborFields["TTL"] = r.ttl + 1
				return r
			},
		},
		{
			name: "pb ttl differs",
			tamper: func(r testRecord) testRecord {
				r = pinBaseCBOR(r)
				r.ttl++
				return r
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.Validate(buildRecord(t, tc.tamper(baseRecord()), priv))
			wantErrIs(t, err, ErrFieldMismatch)
		})
	}
}

func TestValidateRejectsExpiredOrMalformedEOL(t *testing.T) {
	pub, priv := newKeyPair(t)
	v := fixedValidator((&captureVerifier{pub: pub}).verify)

	t.Run("eol in the past", func(t *testing.T) {
		r := baseRecord()
		r.validity = []byte(fixedNow.Add(-time.Minute).Format(time.RFC3339Nano))
		_, err := v.Validate(buildRecord(t, r, priv))
		wantErrIs(t, err, ErrRecordExpired)
	})

	t.Run("eol equal to now", func(t *testing.T) {
		r := baseRecord()
		r.validity = []byte(fixedNow.Format(time.RFC3339Nano))
		_, err := v.Validate(buildRecord(t, r, priv))
		wantErrIs(t, err, ErrRecordExpired)
	})

	t.Run("eol not rfc3339", func(t *testing.T) {
		r := baseRecord()
		r.validity = []byte("not-a-timestamp")
		_, err := v.Validate(buildRecord(t, r, priv))
		wantErrIs(t, err, ErrInvalidValidity)
	})
}

func TestValidateRejectsNonEOLValidityType(t *testing.T) {
	pub, priv := newKeyPair(t)
	v := fixedValidator((&captureVerifier{pub: pub}).verify)

	r := baseRecord()
	r.validityType = 1
	r.cborFields = defaultCBOR(r)
	_, err := v.Validate(buildRecord(t, r, priv))
	wantErrIs(t, err, ErrUnsupportedValidity)
}

func TestValidateRejectsMalformedInput(t *testing.T) {
	pub, _ := newKeyPair(t)
	v := fixedValidator((&captureVerifier{pub: pub}).verify)

	if _, err := v.Validate([]byte{0x08, 0xff}); !errors.Is(err, ErrMalformedRecord) {
		t.Fatalf("expected malformed record, got %v", err)
	}
	if _, err := v.Validate(nil); !errors.Is(err, ErrMalformedRecord) {
		t.Fatalf("expected malformed empty record, got %v", err)
	}
}
