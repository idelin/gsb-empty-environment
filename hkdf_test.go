package netsec

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex fixture %q: %v", s, err)
	}
	return b
}

func repeatByte(v byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func ascending(start byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = start + byte(i)
	}
	return out
}

// hkdfCase mirrors the structure of the RFC 5869 appendix A test vectors.
type hkdfCase struct {
	name   string
	ikm    []byte
	salt   []byte
	info   []byte
	length int
	prk    string
	okm    string
}

func rfc5869Cases() []hkdfCase {
	return []hkdfCase{
		{
			name:   "rfc5869-A1-basic",
			ikm:    repeatByte(0x0b, 22),
			salt:   ascending(0x00, 13),
			info:   ascending(0xf0, 10),
			length: 42,
			prk:    "077709362c2e32df0ddc3f0dc47bba6390b6c73bb50f9c3122ec844ad7c2b3e5",
			okm:    "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865",
		},
		{
			name:   "rfc5869-A2-long-inputs",
			ikm:    ascending(0x00, 80),
			salt:   ascending(0x60, 80),
			info:   ascending(0xb0, 80),
			length: 82,
			prk:    "06a6b88c5853361a06104c9ceb35b45cef760014904671014a193f40c15fc244",
			okm:    "b11e398dc80327a1c8e7f78c596a49344f012eda2d4efad8a050cc4c19afa97c59045a99cac7827271cb41c65e590e09da3275600c2f09b8367793a9aca3db71cc30c58179ec3e87c14c01d5c1f3434f1d87",
		},
		{
			name:   "rfc5869-A3-empty-salt-info",
			ikm:    repeatByte(0x0b, 22),
			salt:   []byte{},
			info:   []byte{},
			length: 42,
			prk:    "19ef24a32c717b167f33a91d6f648bdf96596776afdb6377ac434c1c293ccb04",
			okm:    "8da4e775a563c18f715f802a063c5a31b8a11f5c5ee1879ec3454e5f3c738d2d9d201395faa4b61a96c8",
		},
	}
}

func TestHKDFRFC5869Vectors(t *testing.T) {
	for _, tc := range rfc5869Cases() {
		t.Run(tc.name, func(t *testing.T) {
			prk := Extract(tc.salt, tc.ikm)
			if want := mustHex(t, tc.prk); !bytes.Equal(prk, want) {
				t.Fatalf("Extract mismatch:\n got %x\nwant %x", prk, want)
			}
			okm, err := Expand(prk, tc.info, tc.length)
			if err != nil {
				t.Fatalf("Expand returned error: %v", err)
			}
			if want := mustHex(t, tc.okm); !bytes.Equal(okm, want) {
				t.Fatalf("Expand mismatch:\n got %x\nwant %x", okm, want)
			}
			// The one-shot helper must reproduce the same output.
			oneShot, err := DeriveKey(tc.salt, tc.ikm, tc.info, tc.length)
			if err != nil {
				t.Fatalf("DeriveKey returned error: %v", err)
			}
			if !bytes.Equal(oneShot, mustHex(t, tc.okm)) {
				t.Fatalf("DeriveKey mismatch:\n got %x", oneShot)
			}
		})
	}
}

func TestHKDFNilSaltEqualsZeroSalt(t *testing.T) {
	ikm := repeatByte(0x0b, 22)
	nilPRK := Extract(nil, ikm)
	zeroPRK := Extract(make([]byte, sha256Size), ikm)
	if !bytes.Equal(nilPRK, zeroPRK) {
		t.Fatalf("nil salt and zero salt must be equivalent per RFC 5869")
	}
}

func TestHKDFExpandLengthBounds(t *testing.T) {
	prk := Extract(nil, repeatByte(0x11, 32))

	max, err := Expand(prk, nil, 255*sha256Size)
	if err != nil {
		t.Fatalf("Expand at 255*HashLen failed: %v", err)
	}
	if len(max) != 255*sha256Size {
		t.Fatalf("Expand length = %d, want %d", len(max), 255*sha256Size)
	}
	if _, err := Expand(prk, nil, 255*sha256Size+1); err != errHKDFLength {
		t.Fatalf("Expand over limit error = %v, want %v", err, errHKDFLength)
	}
	if _, err := Expand(prk, nil, -1); err != errHKDFLength {
		t.Fatalf("Expand negative length error = %v, want %v", err, errHKDFLength)
	}

	empty, err := Expand(prk, nil, 0)
	if err != nil {
		t.Fatalf("Expand zero length failed: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("Expand zero length returned %d bytes", len(empty))
	}
}
