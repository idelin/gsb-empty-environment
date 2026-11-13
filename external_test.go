package netsec_test

import (
	"crypto/sha256"
	"sync"
	"testing"

	"example.org/netsec/q0062"
)

// recordingAdapter counts invocations and derives a deterministic 32-byte
// secret from its component id and the exact ciphertext bytes. A ciphertext
// starting with 0x00 triggers implicit rejection: a 32-byte secret is still
// returned, but valid is false.
type recordingAdapter struct {
	mu           sync.Mutex
	calls        int
	canonicalLen int
	componentID  byte
}

func (a *recordingAdapter) Calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func (a *recordingAdapter) Decapsulate(ct []byte) ([32]byte, bool) {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	if len(ct) != a.canonicalLen {
		panic("netsec test: adapter got non-canonical ciphertext length")
	}

	valid := ct[0] != 0
	prefix := a.componentID
	if !valid {
		prefix = a.componentID ^ 0xFF
	}
	h := sha256.New()
	h.Write([]byte{prefix})
	h.Write(ct)
	var ss [32]byte
	copy(ss[:], h.Sum(nil))
	return ss, valid
}

// aliasAdapter maps several canonical ciphertexts to the same shared secret,
// used to prove transcript binding distinguishes ciphertexts even when their
// secrets are identical.
type aliasAdapter struct {
	mu           sync.Mutex
	calls        int
	canonicalLen int
	secrets      map[string][32]byte
}

func (a *aliasAdapter) Calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func (a *aliasAdapter) Decapsulate(ct []byte) ([32]byte, bool) {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	if len(ct) != a.canonicalLen {
		panic("netsec test: adapter got non-canonical ciphertext length")
	}
	ss, ok := a.secrets[string(ct)]
	if !ok {
		panic("netsec test: unexpected ciphertext for alias adapter")
	}
	return ss, true
}

func newTestRegistry(t *testing.T, ct1Len, ct2Len int) (*netsec.Registry, [8]byte, *recordingAdapter, *recordingAdapter) {
	t.Helper()
	reg := netsec.NewRegistry()
	var suiteID [8]byte
	suiteID[0] = 0x42
	a1 := &recordingAdapter{canonicalLen: ct1Len, componentID: 0x01}
	a2 := &recordingAdapter{canonicalLen: ct2Len, componentID: 0x02}
	if err := reg.RegisterSuite(suiteID, netsec.SuiteConfig{
		Adapter1:       a1,
		Adapter2:       a2,
		Ciphertext1Len: ct1Len,
		Ciphertext2Len: ct2Len,
	}); err != nil {
		t.Fatalf("register suite: %v", err)
	}
	return reg, suiteID, a1, a2
}

func baseRequest(suiteID [8]byte, ct1, ct2 []byte) netsec.Request {
	req := netsec.Request{SuiteID: suiteID}
	req.Ciphertext1 = ct1
	req.Ciphertext2 = ct2
	for i := range req.ClientPublicKeyHash {
		req.ClientPublicKeyHash[i] = 0xAA
	}
	for i := range req.ServerPublicKeyHash {
		req.ServerPublicKeyHash[i] = 0xBB
	}
	for i := range req.HandshakeContextHash {
		req.HandshakeContextHash[i] = 0xCC
	}
	return req
}

func mustEncode(t *testing.T, req netsec.Request) []byte {
	t.Helper()
	wire, err := req.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return wire
}

func mustDecapsulate(t *testing.T, reg *netsec.Registry, wire []byte) [32]byte {
	t.Helper()
	key, err := reg.Decapsulate(wire)
	if err != nil {
		t.Fatalf("decapsulate: %v", err)
	}
	return key
}

func TestHappyPathAndDeterminism(t *testing.T) {
	reg, suiteID, a1, a2 := newTestRegistry(t, 8, 8)
	ct1 := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	ct2 := []byte{9, 10, 11, 12, 13, 14, 15, 16}
	wire := mustEncode(t, baseRequest(suiteID, ct1, ct2))

	key1 := mustDecapsulate(t, reg, wire)
	key2 := mustDecapsulate(t, reg, wire)
	if key1 != key2 {
		t.Fatal("valid derivation is not deterministic for identical input")
	}
	if a1.Calls() != 2 || a2.Calls() != 2 {
		t.Fatalf("both adapters must run each call: a1=%d a2=%d", a1.Calls(), a2.Calls())
	}

	var zero [32]byte
	if key1 == zero {
		t.Fatal("derived key is all zeros")
	}
}

func TestSwapCiphertextOrderChangesKey(t *testing.T) {
	reg, suiteID, _, _ := newTestRegistry(t, 8, 8)
	ct1 := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	ct2 := []byte{9, 10, 11, 12, 13, 14, 15, 16}

	keyA := mustDecapsulate(t, reg, mustEncode(t, baseRequest(suiteID, ct1, ct2)))
	keyB := mustDecapsulate(t, reg, mustEncode(t, baseRequest(suiteID, ct2, ct1)))
	if keyA == keyB {
		t.Fatal("swapping ciphertext order did not change the derived key")
	}
}

func TestReplacingEitherComponentChangesKey(t *testing.T) {
	reg, suiteID, _, _ := newTestRegistry(t, 8, 8)
	ct1 := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	ct2 := []byte{9, 10, 11, 12, 13, 14, 15, 16}
	base := mustDecapsulate(t, reg, mustEncode(t, baseRequest(suiteID, ct1, ct2)))

	ct1Alt := []byte{1, 2, 3, 4, 5, 6, 7, 99}
	ct2Alt := []byte{9, 10, 11, 12, 13, 14, 15, 77}

	if got := mustDecapsulate(t, reg, mustEncode(t, baseRequest(suiteID, ct1Alt, ct2))); got == base {
		t.Fatal("replacing component 1 did not change the derived key")
	}
	if got := mustDecapsulate(t, reg, mustEncode(t, baseRequest(suiteID, ct1, ct2Alt))); got == base {
		t.Fatal("replacing component 2 did not change the derived key")
	}
}

func TestSameSecretDifferentCiphertextChangesKey(t *testing.T) {
	reg := netsec.NewRegistry()
	var suiteID [8]byte
	suiteID[0] = 0x77

	var shared [32]byte
	for i := range shared {
		shared[i] = byte(i + 1)
	}
	ctA := []byte{1, 1, 1, 1}
	ctB := []byte{2, 2, 2, 2}
	a1 := &aliasAdapter{
		canonicalLen: 4,
		secrets: map[string][32]byte{
			string(ctA): shared,
			string(ctB): shared,
		},
	}
	ct2 := []byte{5, 5, 5, 5}
	var ss2 [32]byte
	for i := range ss2 {
		ss2[i] = byte(0x80 + i)
	}
	a2 := &aliasAdapter{
		canonicalLen: 4,
		secrets:      map[string][32]byte{string(ct2): ss2},
	}
	if err := reg.RegisterSuite(suiteID, netsec.SuiteConfig{
		Adapter1:       a1,
		Adapter2:       a2,
		Ciphertext1Len: 4,
		Ciphertext2Len: 4,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	keyA := mustDecapsulate(t, reg, mustEncode(t, baseRequest(suiteID, ctA, ct2)))
	keyB := mustDecapsulate(t, reg, mustEncode(t, baseRequest(suiteID, ctB, ct2)))
	if keyA == keyB {
		t.Fatal("identical secrets with different ciphertexts must yield different keys")
	}
}
