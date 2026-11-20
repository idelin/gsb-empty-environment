package netsec

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

func TestValidateAcceptsWellFormedV2Record(t *testing.T) {
	pub, priv := newTestSigner(t)
	v := newTestValidator(pub)

	raw := buildRecord(t, defaultSpec(), priv)
	record, err := v.Validate(testKey, raw)
	if err != nil {
		t.Fatalf("expected valid record, got %v", err)
	}
	if record.Sequence != 10 {
		t.Fatalf("sequence = %d, want 10", record.Sequence)
	}
	wantEOL := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if !record.EOL().Equal(wantEOL) {
		t.Fatalf("EOL = %v, want %v", record.EOL(), wantEOL)
	}
	if !bytes.Equal(record.Value, []byte("/ipfs/bafybeihtest")) {
		t.Fatalf("unexpected value %q", record.Value)
	}
	if !bytes.Equal(record.Raw(), raw) {
		t.Fatal("Raw() must return the original bytes")
	}
}

func TestValidateRejectsV1Signatures(t *testing.T) {
	pub, priv := newTestSigner(t)
	v := newTestValidator(pub)

	t.Run("dual signature", func(t *testing.T) {
		spec := defaultSpec()
		spec.includeV1 = true
		_, err := v.Validate(testKey, buildRecord(t, spec, priv))
		if !errors.Is(err, ErrV1SignatureRejected) {
			t.Fatalf("err = %v, want ErrV1SignatureRejected", err)
		}
	})

	t.Run("v1 only", func(t *testing.T) {
		spec := defaultSpec()
		spec.includeV1 = true
		spec.omitV2 = true
		_, err := v.Validate(testKey, buildRecord(t, spec, priv))
		if !errors.Is(err, ErrV1SignatureRejected) {
			t.Fatalf("err = %v, want ErrV1SignatureRejected", err)
		}
	})

	t.Run("no signature at all", func(t *testing.T) {
		spec := defaultSpec()
		spec.omitV2 = true
		_, err := v.Validate(testKey, buildRecord(t, spec, priv))
		if !errors.Is(err, ErrMissingV2Signature) {
			t.Fatalf("err = %v, want ErrMissingV2Signature", err)
		}
	})
}

func TestValidateRejectsBadV2Signature(t *testing.T) {
	pub, _ := newTestSigner(t)
	_, attacker := newTestSignerWithOffset(t, 0x5a)
	v := newTestValidator(pub)

	_, err := v.Validate(testKey, buildRecord(t, defaultSpec(), attacker))
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("err = %v, want ErrSignature", err)
	}
}

func TestValidateRejectsCBORProtobufMismatch(t *testing.T) {
	pub, priv := newTestSigner(t)
	v := newTestValidator(pub)

	cases := map[string]func(*recordSpec){
		"Value":        func(s *recordSpec) { s.value = []byte("/ipfs/bafybeiattacker") },
		"Validity":     func(s *recordSpec) { s.validity = "2031-01-01T00:00:00Z" },
		"ValidityType": func(s *recordSpec) { s.validityType = 1 },
		"Sequence":     func(s *recordSpec) { s.sequence = 999 },
		"TTL":          func(s *recordSpec) { s.ttl = 42 },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			// The signed CBOR keeps the original spec while the protobuf
			// copy carries the attacker-controlled value.
			signedSpec := defaultSpec()
			tampered := defaultSpec()
			mutate(&tampered)

			data := encodeTestCBOR(signedSpec)
			sig := ed25519.Sign(priv, append(append([]byte(nil), V2SignaturePrefix...), data...))

			var raw []byte
			raw = appendProtoBytes(raw, fieldValue, tampered.value)
			raw = appendProtoVarint(raw, fieldValidityType, tampered.validityType)
			raw = appendProtoBytes(raw, fieldValidity, []byte(tampered.validity))
			raw = appendProtoVarint(raw, fieldSequence, tampered.sequence)
			raw = appendProtoVarint(raw, fieldTTL, tampered.ttl)
			raw = appendProtoBytes(raw, fieldV2Signature, sig)
			raw = appendProtoBytes(raw, fieldData, data)

			_, err := v.Validate(testKey, raw)
			if !errors.Is(err, ErrFieldMismatch) {
				t.Fatalf("err = %v, want ErrFieldMismatch", err)
			}
		})
	}
}

func TestValidateRejectsExpiredAndMalformedEOL(t *testing.T) {
	pub, priv := newTestSigner(t)
	v := newTestValidator(pub)

	t.Run("expired", func(t *testing.T) {
		spec := defaultSpec()
		spec.validity = "2025-12-31T23:59:59Z"
		_, err := v.Validate(testKey, buildRecord(t, spec, priv))
		if !errors.Is(err, ErrExpired) {
			t.Fatalf("err = %v, want ErrExpired", err)
		}
	})

	t.Run("eol equal to now is not later", func(t *testing.T) {
		spec := defaultSpec()
		spec.validity = testClock.Format(time.RFC3339)
		_, err := v.Validate(testKey, buildRecord(t, spec, priv))
		if !errors.Is(err, ErrExpired) {
			t.Fatalf("err = %v, want ErrExpired", err)
		}
	})

	t.Run("not rfc3339", func(t *testing.T) {
		spec := defaultSpec()
		spec.validity = "2030/01/01"
		_, err := v.Validate(testKey, buildRecord(t, spec, priv))
		if !errors.Is(err, ErrInvalidEOL) {
			t.Fatalf("err = %v, want ErrInvalidEOL", err)
		}
	})

	t.Run("fractional rfc3339 accepted", func(t *testing.T) {
		spec := defaultSpec()
		spec.validity = "2030-01-01T00:00:00.123456789Z"
		if _, err := v.Validate(testKey, buildRecord(t, spec, priv)); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})
}

func TestValidateRejectsNonEOLValidityType(t *testing.T) {
	pub, priv := newTestSigner(t)
	v := newTestValidator(pub)

	spec := defaultSpec()
	spec.validityType = 1
	_, err := v.Validate(testKey, buildRecord(t, spec, priv))
	if !errors.Is(err, ErrUnsupportedValidityType) {
		t.Fatalf("err = %v, want ErrUnsupportedValidityType", err)
	}
}

func TestStoreRejectsReplayAndAppliesTieBreaks(t *testing.T) {
	pub, priv := newTestSigner(t)
	v := newTestValidator(pub)
	store := NewStore(v)

	base := defaultSpec()
	if _, err := store.Put(testKey, buildRecord(t, base, priv)); err != nil {
		t.Fatalf("initial Put: %v", err)
	}

	t.Run("older sequence replay is discarded", func(t *testing.T) {
		old := defaultSpec()
		old.sequence = 9
		_, err := store.Put(testKey, buildRecord(t, old, priv))
		if !errors.Is(err, ErrRecordSuperseded) {
			t.Fatalf("err = %v, want ErrRecordSuperseded", err)
		}
		if got := store.Current(testKey).Sequence; got != 10 {
			t.Fatalf("stored sequence = %d, want 10", got)
		}
	})

	t.Run("same sequence earlier eol is discarded", func(t *testing.T) {
		later := defaultSpec()
		later.validity = "2030-06-01T00:00:00Z"
		if _, err := store.Put(testKey, buildRecord(t, later, priv)); err != nil {
			t.Fatalf("later-EOL Put: %v", err)
		}

		earlier := defaultSpec()
		earlier.validity = "2030-02-01T00:00:00Z"
		_, err := store.Put(testKey, buildRecord(t, earlier, priv))
		if !errors.Is(err, ErrRecordSuperseded) {
			t.Fatalf("err = %v, want ErrRecordSuperseded", err)
		}
		if got := store.Current(testKey).EOL(); got.Year() != 2030 || got.Month() != time.June {
			t.Fatalf("stored EOL = %v, want 2030-06-01", got)
		}
	})

	t.Run("same sequence same eol new record discarded", func(t *testing.T) {
		duplicate := defaultSpec()
		duplicate.validity = "2030-06-01T00:00:00Z"
		duplicate.value = []byte("/ipfs/bafybeishouldnotwin")
		_, err := store.Put(testKey, buildRecord(t, duplicate, priv))
		if !errors.Is(err, ErrRecordSuperseded) {
			t.Fatalf("err = %v, want ErrRecordSuperseded", err)
		}
		if string(store.Current(testKey).Value) != string(defaultSpec().value) {
			t.Fatal("stored record must be unchanged on a full tie")
		}
	})

	t.Run("higher sequence replaces", func(t *testing.T) {
		next := defaultSpec()
		next.sequence = 11
		next.validity = "2030-03-01T00:00:00Z"
		if _, err := store.Put(testKey, buildRecord(t, next, priv)); err != nil {
			t.Fatalf("higher-sequence Put: %v", err)
		}
		if got := store.Current(testKey).Sequence; got != 11 {
			t.Fatalf("stored sequence = %d, want 11", got)
		}
		if !bytes.Equal(store.Get(testKey), buildRecord(t, next, priv)) {
			t.Fatal("Get must return the stored raw bytes")
		}
	})
}

func TestSelectPicksWinningValidRecord(t *testing.T) {
	pub, priv := newTestSigner(t)
	v := newTestValidator(pub)

	low := defaultSpec()
	low.sequence = 1
	low.validity = "2030-01-01T00:00:00Z"

	high := defaultSpec()
	high.sequence = 2
	high.validity = "2030-01-01T00:00:00Z"

	sameSeqLaterEOL := defaultSpec()
	sameSeqLaterEOL.sequence = 2
	sameSeqLaterEOL.validity = "2031-01-01T00:00:00Z"

	expired := defaultSpec()
	expired.sequence = 100
	expired.validity = "2020-01-01T00:00:00Z"

	candidates := [][]byte{
		buildRecord(t, low, priv),
		buildRecord(t, high, priv),
		buildRecord(t, sameSeqLaterEOL, priv),
		buildRecord(t, expired, priv),
		nil,
	}

	idx, err := v.Select(testKey, candidates)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if idx != 2 {
		t.Fatalf("selected index = %d, want 2", idx)
	}

	if _, err := v.Select(testKey, [][]byte{nil, {0xff}}); !errors.Is(err, ErrNoValidRecord) {
		t.Fatalf("err = %v, want ErrNoValidRecord", err)
	}
}

func TestMalformedRecordsRejected(t *testing.T) {
	pub, priv := newTestSigner(t)
	v := newTestValidator(pub)

	good := buildRecord(t, defaultSpec(), priv)

	cases := map[string][]byte{
		"empty":                   nil,
		"truncated varint":        good[:3],
		"truncated length prefix": append(append([]byte(nil), good[:1]...), 0xff),
		"unknown wire type":       {0x0f}, // field 1, wire type 7
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := v.Validate(testKey, raw); !errors.Is(err, ErrMalformedRecord) {
				t.Fatalf("err = %v, want ErrMalformedRecord", err)
			}
		})
	}
}

func TestDuplicateProtobufFieldsRejected(t *testing.T) {
	pub, priv := newTestSigner(t)
	v := newTestValidator(pub)

	good := buildRecord(t, defaultSpec(), priv)
	// Append a second sequence field (field 5, varint, value 1).
	duplicated := append(append([]byte(nil), good...), 0x28, 0x01)

	_, err := v.Validate(testKey, duplicated)
	if !errors.Is(err, ErrMalformedRecord) {
		t.Fatalf("err = %v, want ErrMalformedRecord", err)
	}
}

func TestNilVerifierRejected(t *testing.T) {
	v := NewValidator(Config{})
	if _, err := v.Validate(testKey, []byte{0x08, 0x01}); !errors.Is(err, ErrNilVerifier) {
		t.Fatalf("err = %v, want ErrNilVerifier", err)
	}
}
