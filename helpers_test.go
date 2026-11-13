package netsec

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func mustDecodeHexArray(t *testing.T, s string) [32]byte {
	t.Helper()
	var out [32]byte
	b := mustDecodeHex(t, s)
	if len(b) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(b))
	}
	copy(out[:], b)
	return out
}

// fakeAdapter is a deterministic adapter. It accepts canonical ciphertexts
// whose first byte is non-zero and implicit-rejects the rest. It panics on a
// non-canonical length so tests can prove the package pre-validates lengths
// before invoking an adapter.
type fakeAdapter struct {
	canonicalLen int
	idByte       byte
	calls        int
}

func (f *fakeAdapter) Decapsulate(ct []byte) ([32]byte, bool) {
	f.calls++
	if len(ct) != f.canonicalLen {
		panic("netsec test: adapter received non-canonical ciphertext length")
	}
	var ss [32]byte
	valid := ct[0] != 0
	if valid {
		for i := range ss {
			ss[i] = f.idByte ^ byte(i)
		}
	} else {
		for i := range ss {
			ss[i] = f.idByte ^ 0xFF ^ byte(i)
		}
	}
	return ss, valid
}

func katRequest(t *testing.T) Request {
	t.Helper()
	req := Request{}
	for i := range req.SuiteID {
		req.SuiteID[i] = byte(i + 1)
	}
	req.Ciphertext1 = []byte("ct1-canonical-bytes-0001")
	req.Ciphertext2 = []byte("ct2-canonical-bytes-0002")
	for i := range req.ClientPublicKeyHash {
		req.ClientPublicKeyHash[i] = 0x11
	}
	for i := range req.ServerPublicKeyHash {
		req.ServerPublicKeyHash[i] = 0x22
	}
	for i := range req.HandshakeContextHash {
		req.HandshakeContextHash[i] = 0x33
	}
	return req
}

func putUint16(b []byte, v uint16) {
	binary.BigEndian.PutUint16(b, v)
}
