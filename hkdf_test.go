package netsec

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

type hkdfSHA256Vector struct {
	name   string
	ikm    string
	salt   string
	info   string
	length int
	prk    string
	okm    string
}

// RFC 5869, Appendix A, test cases 1-3.
var rfc5869SHA256Vectors = []hkdfSHA256Vector{
	{
		name:   "case 1 basic",
		ikm:    strings.Repeat("0b", 22),
		salt:   "000102030405060708090a0b0c",
		info:   "f0f1f2f3f4f5f6f7f8f9",
		length: 42,
		prk:    "077709362c2e32df0ddc3f0dc47bba6390b6c73bb50f9c3122ec844ad7c2b3e5",
		okm:    "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865",
	},
	{
		name:   "case 2 long inputs",
		ikm:    "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f404142434445464748494a4b4c4d4e4f",
		salt:   "606162636465666768696a6b6c6d6e6f707172737475767778797a7b7c7d7e7f808182838485868788898a8b8c8d8e8f909192939495969798999a9b9c9d9e9fa0a1a2a3a4a5a6a7a8a9aaabacadaeaf",
		info:   "b0b1b2b3b4b5b6b7b8b9babbbcbdbebfc0c1c2c3c4c5c6c7c8c9cacbcccdcecfd0d1d2d3d4d5d6d7d8d9dadbdcdddedfe0e1e2e3e4e5e6e7e8e9eaebecedeeeff0f1f2f3f4f5f6f7f8f9fafbfcfdfeff",
		length: 82,
		prk:    "06a6b88c5853361a06104c9ceb35b45cef760014904671014a193f40c15fc244",
		okm:    "b11e398dc80327a1c8e7f78c596a49344f012eda2d4efad8a050cc4c19afa97c59045a99cac7827271cb41c65e590e09da3275600c2f09b8367793a9aca3db71cc30c58179ec3e87c14c01d5c1f3434f1d87",
	},
	{
		name:   "case 3 zero salt and info",
		ikm:    strings.Repeat("0b", 22),
		salt:   "",
		info:   "",
		length: 42,
		prk:    "19ef24a32c717b167f33a91d6f648bdf96596776afdb6377ac434c1c293ccb04",
		okm:    "8da4e775a563c18f715f802a063c5a31b8a11f5c5ee1879ec3454e5f3c738d2d9d201395faa4b61a96c8",
	},
}

func TestRFC5869SHA256Vectors(t *testing.T) {
	for _, vector := range rfc5869SHA256Vectors {
		t.Run(vector.name, func(t *testing.T) {
			ikm := decodeHex(t, vector.ikm)
			salt := decodeHex(t, vector.salt)
			info := decodeHex(t, vector.info)
			wantPRK := decodeHex(t, vector.prk)
			wantOKM := decodeHex(t, vector.okm)

			prk := Extract(salt, ikm)
			if !bytes.Equal(prk, wantPRK) {
				t.Fatalf("Extract mismatch\ngot  %x\nwant %x", prk, wantPRK)
			}
			if !bytes.Equal(ExtractSHA256(salt, ikm), wantPRK) {
				t.Fatal("ExtractSHA256 differs from Extract")
			}

			okm, err := Expand(prk, info, vector.length)
			if err != nil {
				t.Fatalf("Expand: %v", err)
			}
			if !bytes.Equal(okm, wantOKM) {
				t.Fatalf("Expand mismatch\ngot  %x\nwant %x", okm, wantOKM)
			}

			okmFromAlias, err := ExpandSHA256(prk, info, vector.length)
			if err != nil {
				t.Fatalf("ExpandSHA256: %v", err)
			}
			if !bytes.Equal(okmFromAlias, wantOKM) {
				t.Fatal("ExpandSHA256 differs from Expand")
			}

			derived, err := Derive(ikm, salt, info, vector.length)
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if !bytes.Equal(derived, wantOKM) {
				t.Fatalf("Derive mismatch\ngot  %x\nwant %x", derived, wantOKM)
			}
		})
	}
}

func TestExpandLengthBounds(t *testing.T) {
	prk := Extract(nil, []byte("input"))

	for _, length := range []int{-1, MaxExpandLength + 1, 1 << 20} {
		if _, err := Expand(prk, nil, length); !errors.Is(err, ErrInvalidExpandLength) {
			t.Errorf("Expand length %d: got %v, want ErrInvalidExpandLength", length, err)
		}
	}

	empty, err := Expand(prk, nil, 0)
	if err != nil {
		t.Fatalf("Expand zero length: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("Expand zero length returned %d bytes", len(empty))
	}

	maximum, err := Expand(prk, []byte("info"), MaxExpandLength)
	if err != nil {
		t.Fatalf("Expand maximum length: %v", err)
	}
	if len(maximum) != MaxExpandLength {
		t.Fatalf("Expand maximum length returned %d bytes", len(maximum))
	}
}

func decodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode hex: %v", err)
	}
	return decoded
}
