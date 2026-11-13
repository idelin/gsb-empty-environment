package netsec

import (
	"bytes"
	"encoding/asn1"
	"errors"
	"testing"
	"time"
)

type testResponse struct {
	nonce        []byte
	includeNonce bool
	producedAt   time.Time
	thisUpdate   time.Time
	nextUpdate   time.Time
	extensions   [][]byte
	certStatus   []byte
}

func TestValidateStapledResponseAcceptsMatchingNonce(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	nonce := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	response := buildOCSPResponse(t, testResponse{
		nonce:        nonce,
		includeNonce: true,
		producedAt:   now.Add(-2 * time.Minute),
		thisUpdate:   now.Add(-time.Minute),
		nextUpdate:   now.Add(time.Hour),
	})

	info, err := ParseOCSPResponse(response)
	if err != nil {
		t.Fatalf("ParseOCSPResponse() error = %v", err)
	}
	if !info.NoncePresent || !bytes.Equal(info.Nonce, nonce) {
		t.Fatalf("ParseOCSPResponse() nonce = %x, present = %v; want %x, true", info.Nonce, info.NoncePresent, nonce)
	}

	extracted, err := ExtractOCSPNonce(response)
	if err != nil {
		t.Fatalf("ExtractOCSPNonce() error = %v", err)
	}
	if !bytes.Equal(extracted, nonce) {
		t.Fatalf("ExtractOCSPNonce() = %x; want %x", extracted, nonce)
	}

	if err := ValidateOCSPNonce(response, nonce); err != nil {
		t.Fatalf("ValidateOCSPNonce() error = %v", err)
	}
	if err := ValidateStapledResponse(response, nonce, now); err != nil {
		t.Fatalf("ValidateStapledResponse() error = %v", err)
	}
}

func TestValidateStapledResponseRejectsMismatchedNonce(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	response := buildOCSPResponse(t, testResponse{
		nonce:        []byte{9, 9, 9, 9},
		includeNonce: true,
		producedAt:   now.Add(-2 * time.Minute),
		thisUpdate:   now.Add(-time.Minute),
		nextUpdate:   now.Add(time.Hour),
	})

	err := ValidateStapledResponse(response, []byte{1, 2, 3, 4}, now)
	if !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("ValidateStapledResponse() error = %v; want ErrNonceMismatch", err)
	}
}

func TestValidateStapledResponseRejectsMissingNonce(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	response := buildOCSPResponse(t, testResponse{
		producedAt: now.Add(-2 * time.Minute),
		thisUpdate: now.Add(-time.Minute),
		nextUpdate: now.Add(time.Hour),
	})

	err := ValidateStapledResponse(response, []byte{1, 2, 3, 4}, now)
	if !errors.Is(err, ErrNonceMissing) {
		t.Fatalf("ValidateStapledResponse() error = %v; want ErrNonceMissing", err)
	}
	if _, err := ExtractOCSPNonce(response); !errors.Is(err, ErrNonceMissing) {
		t.Fatalf("ExtractOCSPNonce() error = %v; want ErrNonceMissing", err)
	}
	if err := ValidateOCSPNonce(response, nil); err != nil {
		t.Fatalf("ValidateOCSPNonce() without request nonce error = %v", err)
	}
}

func TestFlipOCSPResponseNonce(t *testing.T) {
	nonce := []byte{0x10, 0x20, 0x30, 0x40}
	original := buildOCSPResponse(t, testResponse{
		nonce:        nonce,
		includeNonce: true,
		producedAt:   time.Now().Add(-2 * time.Minute),
		thisUpdate:   time.Now().Add(-time.Minute),
		nextUpdate:   time.Now().Add(time.Hour),
	})
	before := bytes.Clone(original)

	flipped, err := FlipOCSPResponseNonce(original)
	if err != nil {
		t.Fatalf("FlipOCSPResponseNonce() error = %v", err)
	}
	if !bytes.Equal(original, before) {
		t.Fatal("FlipOCSPResponseNonce() modified its input")
	}
	if bytes.Equal(flipped, original) {
		t.Fatal("FlipOCSPResponseNonce() returned an unchanged response")
	}
	flippedNonce, err := ExtractOCSPNonce(flipped)
	if err != nil {
		t.Fatalf("ExtractOCSPNonce(flipped) error = %v", err)
	}
	if bytes.Equal(flippedNonce, nonce) {
		t.Fatalf("flipped nonce = %x; want a changed value", flippedNonce)
	}
	if err := ValidateOCSPNonce(flipped, nonce); !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("ValidateOCSPNonce(flipped) error = %v; want ErrNonceMismatch", err)
	}
}

func TestValidateStapledResponseChecksTime(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		producedAt time.Time
		thisUpdate time.Time
		nextUpdate time.Time
		wantErr    error
	}{
		{
			name:       "expired",
			producedAt: now.Add(-3 * time.Hour),
			thisUpdate: now.Add(-2 * time.Hour),
			nextUpdate: now.Add(-time.Hour),
			wantErr:    ErrResponseExpired,
		},
		{
			name:       "not yet valid",
			producedAt: now,
			thisUpdate: now.Add(time.Hour),
			nextUpdate: now.Add(2 * time.Hour),
			wantErr:    ErrResponseNotYetValid,
		},
		{
			name:       "missing nextUpdate and too old",
			producedAt: now.Add(-25 * time.Hour),
			thisUpdate: now.Add(-25 * time.Hour),
			wantErr:    ErrResponseExpired,
		},
		{
			name:       "missing nextUpdate and recent",
			producedAt: now.Add(-2 * time.Hour),
			thisUpdate: now.Add(-time.Hour),
		},
		{
			name:       "nextUpdate just passed",
			producedAt: now.Add(-2 * time.Hour),
			thisUpdate: now.Add(-time.Hour),
			nextUpdate: now.Add(-time.Nanosecond),
			wantErr:    ErrResponseExpired,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := buildOCSPResponse(t, testResponse{
				producedAt: test.producedAt,
				thisUpdate: test.thisUpdate,
				nextUpdate: test.nextUpdate,
			})
			err := ValidateStapledResponse(response, nil, now)
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateStapledResponse() error = %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ValidateStapledResponse() error = %v; want %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateStapledResponseRejectsUnsuccessfulResponse(t *testing.T) {
	response := derSequence(derEnumValue(3))
	err := ValidateStapledResponse(response, nil)
	if !errors.Is(err, ErrUnsuccessfulResponse) {
		t.Fatalf("ValidateStapledResponse() error = %v; want ErrUnsuccessfulResponse", err)
	}
}

func TestValidateStapledResponseRejectsDuplicateNonce(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	nonceExtension := derSequence(
		derOID(OIDOCSPNonce),
		derOctetString(derOctetString([]byte{1, 2, 3, 4})),
	)
	response := buildOCSPResponse(t, testResponse{
		producedAt: now.Add(-2 * time.Minute),
		thisUpdate: now.Add(-time.Minute),
		nextUpdate: now.Add(time.Hour),
		extensions: [][]byte{nonceExtension, nonceExtension},
	})

	err := ValidateOCSPNonce(response, []byte{1, 2, 3, 4})
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("ValidateOCSPNonce() error = %v; want ErrInvalidResponse", err)
	}
}

func TestParseOCSPResponseAcceptsRevokedStatus(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	response := buildOCSPResponse(t, testResponse{
		nonce:        []byte{1, 2, 3, 4},
		includeNonce: true,
		producedAt:   now.Add(-2 * time.Minute),
		thisUpdate:   now.Add(-time.Minute),
		nextUpdate:   now.Add(time.Hour),
		certStatus:   derTLV(0xa1, derGeneralizedTime(t, now.Add(-time.Hour))),
	})

	if _, err := ParseOCSPResponse(response); err != nil {
		t.Fatalf("ParseOCSPResponse() revoked error = %v", err)
	}
	if err := ValidateStapledResponse(response, []byte{1, 2, 3, 4}, now); err != nil {
		t.Fatalf("ValidateStapledResponse() revoked error = %v", err)
	}
}

func buildOCSPResponse(t *testing.T, config testResponse) []byte {
	t.Helper()

	certID := derSequence(
		derSequence(derOID(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}), derNull()),
		derOctetString([]byte("issuer-name-hash")),
		derOctetString([]byte("issuer-key-hash")),
		derInteger(1),
	)
	certStatus := config.certStatus
	if len(certStatus) == 0 {
		certStatus = []byte{0x80, 0x00}
	}
	singleResponseFields := [][]byte{
		certID,
		certStatus,
		derGeneralizedTime(t, config.thisUpdate),
	}
	if !config.nextUpdate.IsZero() {
		singleResponseFields = append(singleResponseFields, derContext(0, derGeneralizedTime(t, config.nextUpdate)))
	}
	singleResponse := derSequence(singleResponseFields...)

	responseDataFields := [][]byte{
		derContext(2, derOctetString([]byte("responder-key-hash"))),
		derGeneralizedTime(t, config.producedAt),
		derSequence(singleResponse),
	}

	extensions := append([][]byte(nil), config.extensions...)
	if config.includeNonce {
		extensions = append(extensions, derSequence(
			derOID(OIDOCSPNonce),
			derOctetString(derOctetString(config.nonce)),
		))
	}
	if len(extensions) > 0 {
		responseDataFields = append(responseDataFields, derContext(1, derSequence(extensions...)))
	}
	responseData := derSequence(responseDataFields...)

	basicResponse := derSequence(
		responseData,
		derSequence(derOID(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}), derNull()),
		derBitString([]byte{0x01}),
	)
	responseBytes := derSequence(derOID(OIDOCSPBasic), derOctetString(basicResponse))
	return derSequence(derEnumValue(0), derContext(0, responseBytes))
}

func derSequence(parts ...[]byte) []byte {
	return derTLV(0x30, parts...)
}

func derContext(tag byte, content ...[]byte) []byte {
	return derTLV(0xa0|tag, content...)
}

func derOctetString(content []byte) []byte {
	return derTLV(0x04, content)
}

func derBitString(content []byte) []byte {
	return derTLV(0x03, content)
}

func derNull() []byte {
	return []byte{0x05, 0x00}
}

func derEnumValue(value byte) []byte {
	return []byte{0x0a, 0x01, value}
}

func derInteger(value byte) []byte {
	return []byte{0x02, 0x01, value}
}

func derOID(oid asn1.ObjectIdentifier) []byte {
	encoded, err := asn1.Marshal(oid)
	if err != nil {
		panic(err)
	}
	return encoded
}

func derGeneralizedTime(t *testing.T, value time.Time) []byte {
	t.Helper()
	encoded, err := asn1.MarshalWithParams(value.UTC(), "generalized")
	if err != nil {
		t.Fatalf("marshal GeneralizedTime: %v", err)
	}
	return encoded
}

func derTLV(tag byte, parts ...[]byte) []byte {
	var content []byte
	for _, part := range parts {
		content = append(content, part...)
	}

	result := []byte{tag}
	switch {
	case len(content) < 0x80:
		result = append(result, byte(len(content)))
	case len(content) <= 0xff:
		result = append(result, 0x81, byte(len(content)))
	case len(content) <= 0xffff:
		result = append(result, 0x82, byte(len(content)>>8), byte(len(content)))
	default:
		result = append(result, 0x83, byte(len(content)>>16), byte(len(content)>>8), byte(len(content)))
	}
	return append(result, content...)
}
