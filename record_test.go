package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

var (
	now    = time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC)
	future = now.Add(time.Hour).Format(time.RFC3339Nano)
	past   = now.Add(-time.Hour).Format(time.RFC3339Nano)
)

type keyPair struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newKeyPair(t *testing.T) keyPair {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return keyPair{pub: pub, priv: priv}
}

func makeRecord(t *testing.T, key keyPair, value []byte, validity string, sequence, ttl uint64, validityType ValidityType) *Record {
	t.Helper()
	data := encodeData(value, []byte(validity), validityType, sequence, ttl)
	message := append([]byte("ipns-signature:"), data...)
	return &Record{
		Value:        append([]byte(nil), value...),
		ValidityType: validityType,
		Validity:     []byte(validity),
		Sequence:     sequence,
		TTL:          ttl,
		PubKey:       append([]byte(nil), key.pub...),
		SignatureV2:  ed25519.Sign(key.priv, message),
		Data:         data,
	}
}

func verifyWithKey(t *testing.T, key keyPair) func([]byte, []byte, []byte) error {
	t.Helper()
	return func(pubKey, data, signature []byte) error {
		if !bytes.Equal(pubKey, key.pub) {
			return errors.New("unexpected public key")
		}
		if !ed25519.Verify(key.pub, data, signature) {
			return errors.New("bad signature")
		}
		return nil
	}
}

func testOptions(t *testing.T, key keyPair) VerifyOptions {
	t.Helper()
	return VerifyOptions{Verifier: verifyWithKey(t, key), Now: now}
}

func encodeRecord(t *testing.T, r *Record) []byte {
	t.Helper()
	raw, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestValidateAcceptsV2Record(t *testing.T) {
	key := newKeyPair(t)
	r := makeRecord(t, key, []byte("/ipfs/bafy-test"), future, 7, uint64(60*time.Second), ValidityEOL)
	raw := encodeRecord(t, r)

	got, err := Validate(raw, testOptions(t, key))
	if err != nil {
		t.Fatalf("Validate rejected a valid record: %v", err)
	}
	if got.Sequence != r.Sequence || !bytes.Equal(got.Data, r.Data) {
		t.Fatalf("decoded record differs from input")
	}
}

func TestV1SignatureIsRejected(t *testing.T) {
	key := newKeyPair(t)
	validity := []byte(future)
	// A syntactically valid legacy V1 signature is still not enough.
	v1Signed := append(append([]byte(nil), []byte("/ipfs/old")...), validity...)
	r := &Record{
		Value:     []byte("/ipfs/old"),
		Signature: ed25519.Sign(key.priv, v1Signed),
		Validity:  validity,
		PubKey:    key.pub,
	}
	raw := encodeRecord(t, r)

	_, err := Validate(raw, testOptions(t, key))
	if !errors.Is(err, ErrV1Signature) {
		t.Fatalf("expected ErrV1Signature, got %v", err)
	}
}

func TestMissingV2SignatureIsRejected(t *testing.T) {
	key := newKeyPair(t)
	r := &Record{
		Value:    []byte("/ipfs/x"),
		Validity: []byte(future),
		PubKey:   key.pub,
	}
	_, err := Validate(encodeRecord(t, r), testOptions(t, key))
	if !errors.Is(err, ErrMissingV2Signature) {
		t.Fatalf("expected ErrMissingV2Signature, got %v", err)
	}
}

func TestV2RecordMayCarryDeprecatedV1Signature(t *testing.T) {
	key := newKeyPair(t)
	r := makeRecord(t, key, []byte("/ipfs/x"), future, 1, 1, ValidityEOL)
	r.Signature = ed25519.Sign(key.priv, append(append([]byte(nil), r.Value...), r.Validity...))
	if _, err := Validate(encodeRecord(t, r), testOptions(t, key)); err != nil {
		t.Fatalf("valid V2 record with legacy V1 field was rejected: %v", err)
	}
}

func TestSignedDataMustMatchProtobufFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{"Value", func(r *Record) { r.Value = []byte("/ipfs/other") }},
		{"Validity", func(r *Record) {
			r.Validity = []byte(now.Add(2 * time.Hour).Format(time.RFC3339Nano))
		}},
		{"ValidityType", func(r *Record) { r.ValidityType = ValidityType(1) }},
		{"Sequence", func(r *Record) { r.Sequence++ }},
		{"TTL", func(r *Record) { r.TTL++ }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key := newKeyPair(t)
			r := makeRecord(t, key, []byte("/ipfs/value"), future, 3, 42, ValidityEOL)
			tc.mutate(r)
			// The V2 signature still covers the original Data, so a
			// permissive verifier will accept it. The binding check must
			// reject the divergence.
			raw := encodeRecord(t, r)
			_, err := Validate(raw, testOptions(t, key))
			if !errors.Is(err, ErrFieldMismatch) {
				t.Fatalf("expected ErrFieldMismatch for %s, got %v", tc.name, err)
			}
		})
	}
}

func TestValidityTypeMustBeEOL(t *testing.T) {
	key := newKeyPair(t)
	r := makeRecord(t, key, []byte("/ipfs/x"), future, 1, 1, ValidityType(1))
	_, err := Validate(encodeRecord(t, r), testOptions(t, key))
	if !errors.Is(err, ErrInvalidValidityType) {
		t.Fatalf("expected ErrInvalidValidityType, got %v", err)
	}
}

func TestExpiredEOLIsRejected(t *testing.T) {
	key := newKeyPair(t)
	r := makeRecord(t, key, []byte("/ipfs/x"), past, 1, 1, ValidityEOL)
	_, err := Validate(encodeRecord(t, r), testOptions(t, key))
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("expected ErrExpired, got %v", err)
	}
}

func TestEOLMustBeStrictRFC3339(t *testing.T) {
	key := newKeyPair(t)
	r := makeRecord(t, key, []byte("/ipfs/x"), "2026-11-13 02:00:00", 1, 1, ValidityEOL)
	_, err := Validate(encodeRecord(t, r), testOptions(t, key))
	if !errors.Is(err, ErrInvalidValidity) {
		t.Fatalf("expected ErrInvalidValidity, got %v", err)
	}
}

func TestSignatureMustVerify(t *testing.T) {
	key := newKeyPair(t)
	r := makeRecord(t, key, []byte("/ipfs/x"), future, 1, 1, ValidityEOL)
	// Re-sign the Data with a different key while leaving the record's
	// claimed public key untouched.
	other := newKeyPair(t)
	message := append([]byte("ipns-signature:"), r.Data...)
	r.SignatureV2 = ed25519.Sign(other.priv, message)
	_, err := Validate(encodeRecord(t, r), testOptions(t, key))
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("expected ErrSignatureInvalid, got %v", err)
	}
}

func TestCallerSuppliedVerifierSeesSignedPayload(t *testing.T) {
	key := newKeyPair(t)
	r := makeRecord(t, key, []byte("/ipfs/x"), future, 1, 1, ValidityEOL)
	raw := encodeRecord(t, r)

	var gotMessage []byte
	opts := VerifyOptions{
		Now: now,
		Verifier: func(pubKey, data, signature []byte) error {
			gotMessage = append([]byte(nil), data...)
			if !ed25519.Verify(key.pub, data, signature) {
				return errors.New("bad signature")
			}
			return nil
		},
	}
	if _, err := Validate(raw, opts); err != nil {
		t.Fatal(err)
	}
	want := append([]byte("ipns-signature:"), r.Data...)
	if !bytes.Equal(gotMessage, want) {
		t.Fatalf("verifier did not receive the standard signed payload")
	}
}

func TestShouldReplaceRejectsOlderSequenceReplay(t *testing.T) {
	key := newKeyPair(t)
	existing := makeRecord(t, key, []byte("/ipfs/current"), future, 5, 1, ValidityEOL)
	replayed := makeRecord(t, key, []byte("/ipfs/old"), future, 4, 1, ValidityEOL)

	replace, err := ShouldReplace(existing, replayed)
	if err != nil {
		t.Fatal(err)
	}
	if replace {
		t.Fatal("older sequence must not replace the current record")
	}
}

func TestShouldReplaceSequenceAndEOLTieBreaks(t *testing.T) {
	key := newKeyPair(t)
	baseEOL := now.Add(time.Hour)
	existing := makeRecord(t, key, []byte("/ipfs/a"), baseEOL.Format(time.RFC3339Nano), 5, 1, ValidityEOL)

	tests := []struct {
		name     string
		incoming *Record
		want     bool
	}{
		{
			name:     "higher sequence wins even with earlier EOL",
			incoming: makeRecord(t, key, []byte("/ipfs/b"), baseEOL.Add(-time.Minute).Format(time.RFC3339Nano), 6, 1, ValidityEOL),
			want:     true,
		},
		{
			name:     "equal sequence later EOL wins",
			incoming: makeRecord(t, key, []byte("/ipfs/b"), baseEOL.Add(time.Minute).Format(time.RFC3339Nano), 5, 1, ValidityEOL),
			want:     true,
		},
		{
			name:     "equal sequence earlier EOL loses",
			incoming: makeRecord(t, key, []byte("/ipfs/b"), baseEOL.Add(-time.Minute).Format(time.RFC3339Nano), 5, 1, ValidityEOL),
			want:     false,
		},
		{
			name:     "equal sequence and equal EOL drops incoming",
			incoming: makeRecord(t, key, []byte("/ipfs/other-value"), baseEOL.Format(time.RFC3339Nano), 5, 2, ValidityEOL),
			want:     false,
		},
		{
			name:     "equal sequence and equivalent EOL representation drops incoming",
			incoming: makeRecord(t, key, []byte("/ipfs/b"), baseEOL.In(time.FixedZone("UTC+0", 0)).Format("2006-01-02T15:04:05Z07:00"), 5, 1, ValidityEOL),
			want:     false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ShouldReplace(existing, tc.incoming)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("ShouldReplace = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDuplicateSignedDataFieldIsRejected(t *testing.T) {
	key := newKeyPair(t)
	validity := []byte(future)
	// Build a map with duplicate "Value" keys. The CBOR is otherwise valid
	// and would be signed by the record's key.
	data := []byte{0xa2, 0x65, 'V', 'a', 'l', 'u', 'e', 0x62, 'a', 'b',
		0x65, 'V', 'a', 'l', 'u', 'e', 0x62, 'c', 'd'}
	message := append([]byte("ipns-signature:"), data...)
	r := &Record{
		Value:        []byte("ab"),
		ValidityType: ValidityEOL,
		Validity:     validity,
		Sequence:     1,
		TTL:          1,
		PubKey:       key.pub,
		SignatureV2:  ed25519.Sign(key.priv, message),
		Data:         data,
	}
	_, err := Validate(encodeRecord(t, r), testOptions(t, key))
	if !errors.Is(err, ErrMalformedData) {
		t.Fatalf("expected ErrMalformedData, got %v", err)
	}
}

func TestSignedDataValueMustBeBytes(t *testing.T) {
	key := newKeyPair(t)
	validity := []byte(future)
	// Replace Value with a text string. decodeData must reject the type even
	// though the bytes could look like the same value.
	data := encodeData([]byte("/ipfs/x"), validity, ValidityEOL, 1, 1)
	index := bytes.Index(data, []byte("/ipfs/x"))
	if index < 0 {
		t.Fatal("test data does not contain value")
	}
	data[index-1] = 0x60 | 0x07 // text string of length 7
	message := append([]byte("ipns-signature:"), data...)
	r := &Record{
		Value:        []byte("/ipfs/x"),
		ValidityType: ValidityEOL,
		Validity:     validity,
		Sequence:     1,
		TTL:          1,
		PubKey:       key.pub,
		SignatureV2:  ed25519.Sign(key.priv, message),
		Data:         data,
	}
	_, err := Validate(encodeRecord(t, r), testOptions(t, key))
	if !errors.Is(err, ErrMalformedData) {
		t.Fatalf("expected ErrMalformedData, got %v", err)
	}
}
