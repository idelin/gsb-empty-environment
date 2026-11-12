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
		t.Fatalf("hex decode: %v", err)
	}
	return b
}

func TestAES128CMACRFC4493Vectors(t *testing.T) {
	key := mustHex(t, "2b7e151628aed2a6abf7158809cf4f3c")
	tests := []struct {
		name string
		msg  string
		want string
	}{
		{"empty", "", "bb1d6929e95937287fa37d129b756746"},
		{
			"16 bytes",
			"6bc1bee22e409f96e93d7e117393172a",
			"070a16b46b4d4144f79bdd9dd04a287c",
		},
		{
			"40 bytes",
			"6bc1bee22e409f96e93d7e117393172a" +
				"ae2d8a571e03ac9c9eb76fac45af8e51" +
				"30c81c46a35ce411",
			"dfa66747de9ae63030ca32611497c827",
		},
		{
			"64 bytes",
			"6bc1bee22e409f96e93d7e117393172a" +
				"ae2d8a571e03ac9c9eb76fac45af8e51" +
				"30c81c46a35ce411e5fbc1191a0a52ef" +
				"f69f2445df4f9b17ad2b417be66c3710",
			"51f0bebf7e3b9d92fc49741779363cfe",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AES128CMAC(key, mustHex(t, tc.msg))
			if err != nil {
				t.Fatalf("AES128CMAC: %v", err)
			}
			if want := mustHex(t, tc.want); !bytes.Equal(got, want) {
				t.Fatalf("CMAC = %x, want %x", got, want)
			}
		})
	}
}

func TestAESKeyWrapRFC3394Vector(t *testing.T) {
	kek := mustHex(t, "000102030405060708090a0b0c0d0e0f")
	plaintext := mustHex(t, "00112233445566778899aabbccddeeff")
	want := mustHex(t, "1fa68b0a8112b447aef34bd8fb5a7b829d3e862371d2cfe5")

	wrapped, err := AESKeyWrap(kek, plaintext)
	if err != nil {
		t.Fatalf("AESKeyWrap: %v", err)
	}
	if !bytes.Equal(wrapped, want) {
		t.Fatalf("wrapped = %x, want %x", wrapped, want)
	}

	unwrapped, err := AESKeyUnwrap(kek, wrapped)
	if err != nil {
		t.Fatalf("AESKeyUnwrap: %v", err)
	}
	if !bytes.Equal(unwrapped, plaintext) {
		t.Fatalf("unwrapped = %x, want %x", unwrapped, plaintext)
	}
}

func TestAESKeyUnwrapRejectsTamperedData(t *testing.T) {
	kek := mustHex(t, "000102030405060708090a0b0c0d0e0f")
	plaintext := mustHex(t, "00112233445566778899aabbccddeeff")
	wrapped, err := AESKeyWrap(kek, plaintext)
	if err != nil {
		t.Fatalf("AESKeyWrap: %v", err)
	}
	wrapped[0] ^= 0xff
	if _, err := AESKeyUnwrap(kek, wrapped); err == nil {
		t.Fatal("AESKeyUnwrap accepted tampered ciphertext")
	}
}

func TestDerivePTKLayout(t *testing.T) {
	pmk := mustHex(t, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	anonce := mustHex(t, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	snonce := mustHex(t, "ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100")
	aa := mustHex(t, "020000000001")
	spa := mustHex(t, "020000000002")

	ptk, err := DerivePTK(pmk, anonce, snonce, aa, spa)
	if err != nil {
		t.Fatalf("DerivePTK: %v", err)
	}
	if len(ptk.Key) != 48 || len(ptk.KCK) != 16 || len(ptk.KEK) != 16 || len(ptk.TK) != 16 {
		t.Fatalf("unexpected PTK lengths: key=%d kck=%d kek=%d tk=%d",
			len(ptk.Key), len(ptk.KCK), len(ptk.KEK), len(ptk.TK))
	}
	if !bytes.Equal(ptk.Key[:16], ptk.KCK) ||
		!bytes.Equal(ptk.Key[16:32], ptk.KEK) ||
		!bytes.Equal(ptk.Key[32:48], ptk.TK) {
		t.Fatal("PTK slices do not match the complete key")
	}

	again, err := DerivePTK(pmk, anonce, snonce, aa, spa)
	if err != nil {
		t.Fatalf("DerivePTK second call: %v", err)
	}
	if !bytes.Equal(ptk.Key, again.Key) {
		t.Fatal("PTK derivation is not deterministic")
	}
}
