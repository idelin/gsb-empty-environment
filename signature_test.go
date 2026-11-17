package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"testing"
)

func hmacDigest(key []byte) DigestFunc {
	return func(data []byte) []byte {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write(data)
		return mac.Sum(nil)
	}
}

func mustSign(t *testing.T, header, trailers Section, digest DigestFunc) []byte {
	t.Helper()
	signature, err := Sign(header, trailers, digest)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	return signature
}

func TestSigningInputBindsFieldOrder(t *testing.T) {
	digest := hmacDigest([]byte("test-key"))
	header := Section{
		{Name: "Content-Type", Value: "application/json"},
		{Name: "X-Trace", Value: "one"},
		{Name: "X-Trace", Value: "two"},
	}
	trailers := Section{
		{Name: "Digest", Value: "sha-256=:first:"},
		{Name: "Digest", Value: "sha-256=:second:"},
	}
	signature := mustSign(t, header, trailers, digest)

	tests := []struct {
		name     string
		header   Section
		trailers Section
	}{
		{
			name:     "header order changed",
			header:   Section{header[1], header[0], header[2]},
			trailers: trailers,
		},
		{
			name:     "duplicate header instances swapped",
			header:   Section{header[0], header[2], header[1]},
			trailers: trailers,
		},
		{
			name:     "trailer order changed",
			header:   header,
			trailers: Section{trailers[1], trailers[0]},
		},
		{
			name:     "duplicate trailer collapsed",
			header:   header,
			trailers: Section{{Name: "Digest", Value: "sha-256=:first:sha-256=:second:"}},
		},
		{
			name:     "field moved from header to trailer",
			header:   Section{header[0], header[1]},
			trailers: append(append(Section(nil), trailers...), header[2]),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Verify(test.header, test.trailers, signature, digest); !errors.Is(err, ErrSignatureMismatch) {
				t.Fatalf("Verify() error = %v, want %v", err, ErrSignatureMismatch)
			}
		})
	}
}

func TestSigningInputCanonicalizesFieldNames(t *testing.T) {
	digest := hmacDigest([]byte("test-key"))
	header := Section{{Name: "X-Custom", Value: "value"}}
	trailers := Section{{Name: "Digest", Value: "abc"}}
	signature := mustSign(t, header, trailers, digest)

	lowerHeader := Section{{Name: "x-custom", Value: "value"}}
	lowerTrailers := Section{{Name: "digest", Value: "abc"}}
	if err := Verify(lowerHeader, lowerTrailers, signature, digest); err != nil {
		t.Fatalf("Verify() with lower-case names error = %v, want nil", err)
	}
}

func TestSigningInputIsUnambiguousAcrossSectionBoundaries(t *testing.T) {
	digest := hmacDigest([]byte("test-key"))
	header := Section{{Name: "X-A", Value: "1"}}
	trailers := Section{{Name: "X-B", Value: "2"}}
	signature := mustSign(t, header, trailers, digest)

	// A field name/value pair cannot be smuggled into another position by
	// concatenating adjacent values.
	ambiguous := Section{{Name: "X-A", Value: "1X-B"}}
	if err := Verify(ambiguous, nil, signature, digest); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("Verify() error = %v, want %v", err, ErrSignatureMismatch)
	}
}

func TestSignRejectsBadInputs(t *testing.T) {
	digest := hmacDigest([]byte("test-key"))

	if _, err := Sign(nil, nil, nil); !errors.Is(err, ErrNilDigest) {
		t.Fatalf("Sign(nil digest) error = %v, want %v", err, ErrNilDigest)
	}

	if _, err := Sign(nil, nil, func([]byte) []byte { return nil }); !errors.Is(err, ErrEmptySignature) {
		t.Fatalf("Sign(empty digest result) error = %v, want %v", err, ErrEmptySignature)
	}

	if err := Verify(nil, nil, nil, digest); !errors.Is(err, ErrEmptySignature) {
		t.Fatalf("Verify(empty signature) error = %v, want %v", err, ErrEmptySignature)
	}

	badName := Section{{Name: "Bad Name", Value: "value"}}
	if _, err := Sign(badName, nil, digest); !errors.Is(err, ErrInvalidFieldName) {
		t.Fatalf("Sign(invalid name) error = %v, want %v", err, ErrInvalidFieldName)
	}

	badValue := Section{{Name: "X-Test", Value: "line\r\ninjection"}}
	if _, err := Sign(badValue, nil, digest); !errors.Is(err, ErrInvalidFieldValue) {
		t.Fatalf("Sign(invalid value) error = %v, want %v", err, ErrInvalidFieldValue)
	}

	control := Section{{Name: "X-Test", Value: "bad\x01value"}}
	if _, err := Sign(control, nil, digest); !errors.Is(err, ErrInvalidFieldValue) {
		t.Fatalf("Sign(control value) error = %v, want %v", err, ErrInvalidFieldValue)
	}
}

func TestSigningInputIsStable(t *testing.T) {
	header := Section{
		{Name: "X-B", Value: "2"},
		{Name: "X-A", Value: "1"},
	}
	trailers := Section{
		{Name: "Digest", Value: "one"},
		{Name: "Digest", Value: "two"},
	}

	first, err := SigningInput(header, trailers)
	if err != nil {
		t.Fatalf("SigningInput() error = %v", err)
	}
	second, err := SigningInput(header, trailers)
	if err != nil {
		t.Fatalf("SigningInput() error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("SigningInput() is not deterministic")
	}
}

func TestSignatureEncoding(t *testing.T) {
	signature := hmacDigest([]byte("key"))([]byte("data"))
	encoded := EncodeSignature(signature)
	decoded, err := DecodeSignature(encoded)
	if err != nil {
		t.Fatalf("DecodeSignature() error = %v", err)
	}
	if !bytes.Equal(decoded, signature) {
		t.Fatalf("DecodeSignature() = %x, want %x", decoded, signature)
	}
	if _, err := DecodeSignature("not base64!"); !errors.Is(err, ErrInvalidSignatureEncoding) {
		t.Fatalf("DecodeSignature(invalid) error = %v, want %v", err, ErrInvalidSignatureEncoding)
	}
}
