package netsec

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// RFC 3961 appendix A n-fold test vectors.
func TestNFoldVectors(t *testing.T) {
	input := []byte("kerberos")
	tests := []struct {
		bits int
		want string
	}{
		{64, "6b65726265726f73"},
		{128, "6b65726265726f737b9b5b2b93132b93"},
		{168, "8372c236344e5f1550cd0747e15d62ca7a5a3bcea4"},
	}
	for _, tt := range tests {
		got := nfold(input, tt.bits)
		want, err := hex.DecodeString(tt.want)
		if err != nil {
			t.Fatalf("bad test vector: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("nfold(%q, %d) = %x, want %x", input, tt.bits, got, want)
		}
	}
}
