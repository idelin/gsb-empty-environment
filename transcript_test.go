package netsec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

var (
	testHandoff = bytes.Repeat([]byte{0xAA}, 64)
	testReader  = bytes.Repeat([]byte{0x02}, 65)
	testDevice  = bytes.Repeat([]byte{0x03}, 65)
)

func mustTranscript(t *testing.T) *SessionTranscript {
	t.Helper()
	tr, err := NewSessionTranscript(testHandoff, testReader, testDevice)
	if err != nil {
		t.Fatalf("NewSessionTranscript returned error: %v", err)
	}
	return tr
}

func TestNewSessionTranscriptHashesHandoffAndCompletes(t *testing.T) {
	tr := mustTranscript(t)

	if !tr.Complete() {
		t.Fatal("all-three-present transcript must be accepted")
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("valid transcript failed validation: %v", err)
	}

	want := sha256.Sum256(testHandoff)
	if !bytes.Equal(tr.HandoffHash, want[:]) {
		t.Fatal("HandoffHash must be SHA-256 of the handoff bytes")
	}
	// The transcript holds only the digest, never the raw handoff bytes.
	if bytes.Contains(bytes.Join([][]byte{tr.HandoffHash, tr.ReaderPublicKey, tr.DevicePublicKey}, nil), testHandoff) {
		t.Fatal("raw handoff bytes must not be embedded in the transcript")
	}
}

func TestMissingAnyOneItemFails(t *testing.T) {
	cases := []struct {
		name   string
		modify func(*SessionTranscript)
		want   error
	}{
		{"missing handoff hash", func(tr *SessionTranscript) { tr.HandoffHash = nil }, ErrEmptyHandoffHash},
		{"missing reader public key", func(tr *SessionTranscript) { tr.ReaderPublicKey = nil }, ErrEmptyReaderKey},
		{"missing device public key", func(tr *SessionTranscript) { tr.DevicePublicKey = nil }, ErrEmptyDeviceKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := mustTranscript(t)
			tc.modify(tr)

			if tr.Complete() {
				t.Fatal("incomplete transcript must not be complete")
			}
			if !errors.Is(tr.Validate(), tc.want) {
				t.Fatalf("Validate() = %v, want %v", tr.Validate(), tc.want)
			}
			if _, err := tr.Encode(); !errors.Is(err, tc.want) {
				t.Fatalf("Encode() = %v, want %v", err, tc.want)
			}
			if _, err := tr.SigningInput(); !errors.Is(err, tc.want) {
				t.Fatalf("SigningInput() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestConstructorRejectsMissingInputs(t *testing.T) {
	if _, err := NewSessionTranscript(nil, testReader, testDevice); !errors.Is(err, ErrEmptyHandoff) {
		t.Fatalf("empty handoff: got %v, want %v", err, ErrEmptyHandoff)
	}
	if _, err := NewSessionTranscript(testHandoff, nil, testDevice); !errors.Is(err, ErrEmptyReaderKey) {
		t.Fatalf("empty reader key: got %v, want %v", err, ErrEmptyReaderKey)
	}
	if _, err := NewSessionTranscript(testHandoff, testReader, nil); !errors.Is(err, ErrEmptyDeviceKey) {
		t.Fatalf("empty device key: got %v, want %v", err, ErrEmptyDeviceKey)
	}
}

func TestHandoffHashMustBeSHA256(t *testing.T) {
	if _, err := NewSessionTranscriptFromHash([]byte{0x01, 0x02}, testReader, testDevice); !errors.Is(err, ErrHandoffHashLength) {
		t.Fatalf("short hash: got %v, want %v", err, ErrHandoffHashLength)
	}
	digest := sha256.Sum256(testHandoff)
	tr, err := NewSessionTranscriptFromHash(digest[:], testReader, testDevice)
	if err != nil {
		t.Fatalf("valid digest rejected: %v", err)
	}
	if !tr.Complete() {
		t.Fatal("transcript built from a valid digest must be accepted")
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	tr := mustTranscript(t)
	encoded, err := tr.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !decoded.Complete() {
		t.Fatal("decoded transcript must be complete")
	}
	if !bytes.Equal(decoded.HandoffHash, tr.HandoffHash) ||
		!bytes.Equal(decoded.ReaderPublicKey, tr.ReaderPublicKey) ||
		!bytes.Equal(decoded.DevicePublicKey, tr.DevicePublicKey) {
		t.Fatal("decoded transcript does not match the original")
	}

	// Deterministic: re-encoding yields identical bytes.
	reEncoded, _ := decoded.Encode()
	if !bytes.Equal(reEncoded, encoded) {
		t.Fatal("encoding must be deterministic")
	}
}

func TestDecodeRejectsMalformedAndIncomplete(t *testing.T) {
	valid, _ := mustTranscript(t).Encode()

	// Truncated.
	if _, err := Decode(valid[:len(valid)-3]); !errors.Is(err, ErrMalformedEncoding) {
		t.Fatalf("truncated: got %v, want %v", err, ErrMalformedEncoding)
	}
	// Trailing garbage.
	if _, err := Decode(append(append([]byte{}, valid...), 0xFF)); !errors.Is(err, ErrMalformedEncoding) {
		t.Fatalf("trailing bytes: got %v, want %v", err, ErrMalformedEncoding)
	}
	// Wrong magic.
	badMagic := append([]byte{}, valid...)
	badMagic[0] = 'X'
	if _, err := Decode(badMagic); !errors.Is(err, ErrMalformedEncoding) {
		t.Fatalf("bad magic: got %v, want %v", err, ErrMalformedEncoding)
	}
	// Too short.
	if _, err := Decode([]byte{0x01}); !errors.Is(err, ErrMalformedEncoding) {
		t.Fatalf("short input: got %v, want %v", err, ErrMalformedEncoding)
	}
}

func TestSigningInputBindsAllThreeFields(t *testing.T) {
	base := mustTranscript(t)
	baseInput, err := base.SigningInput()
	if err != nil {
		t.Fatalf("SigningInput: %v", err)
	}

	// Altering the handoff changes the signed bytes, proving the handoff hash
	// is bound into what the device signs.
	alt, err := NewSessionTranscript(bytes.Repeat([]byte{0xBB}, 64), testReader, testDevice)
	if err != nil {
		t.Fatalf("NewSessionTranscript: %v", err)
	}
	altInput, _ := alt.SigningInput()
	if bytes.Equal(baseInput, altInput) {
		t.Fatal("different handoff must produce different signing input")
	}

	altReader, _ := NewSessionTranscript(testHandoff, bytes.Repeat([]byte{0x04}, 65), testDevice)
	altReaderInput, _ := altReader.SigningInput()
	if bytes.Equal(baseInput, altReaderInput) {
		t.Fatal("different reader key must produce different signing input")
	}

	altDevice, _ := NewSessionTranscript(testHandoff, testReader, bytes.Repeat([]byte{0x05}, 65))
	altDeviceInput, _ := altDevice.SigningInput()
	if bytes.Equal(baseInput, altDeviceInput) {
		t.Fatal("different device key must produce different signing input")
	}
}

func TestNilTranscriptRejected(t *testing.T) {
	var tr *SessionTranscript
	if tr.Complete() {
		t.Fatal("nil transcript must not be complete")
	}
	if !errors.Is(tr.Validate(), ErrMalformedEncoding) {
		t.Fatalf("nil Validate: want %v, got %v", ErrMalformedEncoding, tr.Validate())
	}
}
