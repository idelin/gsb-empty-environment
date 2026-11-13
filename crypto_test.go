package netsec

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode hex: %v", err)
	}
	return decoded
}

func TestAESCMACRFC4493Vectors(t *testing.T) {
	key := mustHex(t, "2b7e151628aed2a6abf7158809cf4f3c")
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{
			name:    "empty",
			message: "",
			want:    "bb1d6929e95937287fa37d129b756746",
		},
		{
			name:    "one block",
			message: "6bc1bee22e409f96e93d7e117393172a",
			want:    "070a16b46b4d4144f79bdd9dd04a287c",
		},
		{
			name:    "forty octets",
			message: "6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e5130c81c46a35ce411",
			want:    "dfa66747de9ae63030ca32611497c827",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message, err := hex.DecodeString(test.message)
			if err != nil {
				t.Fatalf("decode message: %v", err)
			}
			got, err := aesCMAC(key, message)
			if err != nil {
				t.Fatalf("AES-CMAC: %v", err)
			}
			if !bytes.Equal(got[:], mustHex(t, test.want)) {
				t.Fatalf("AES-CMAC = %x, want %s", got, test.want)
			}
		})
	}
}

func TestComputeNASMACNIA2IsDeterministicAndTruncated(t *testing.T) {
	key := mustHex(t, "2b7e151628aed2a6abf7158809cf4f3c")
	message := mustHex(t, "00112233445566778899aabbccddeeff")

	first, err := ComputeNASMAC(IntegrityAlgorithm5GIA2, key, 0, 0, directionDownlink, message)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	second, err := ComputeNASMAC(IntegrityAlgorithm5GIA2, key, 0, 0, directionDownlink, message)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if len(first) != 4 {
		t.Fatalf("MAC length = %d, want 4", len(first))
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("MAC is not deterministic: %x != %x", first, second)
	}
}
