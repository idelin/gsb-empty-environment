package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

type testTB interface {
	Helper()
	Fatalf(string, ...any)
}

// testKey is a minisign-compatible Ed25519 key pair used only by tests.
type testKey struct {
	keyID [8]byte
	pub   ed25519.PublicKey
	priv  ed25519.PrivateKey
}

func newTestKey(t testTB) *testKey {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	k := &testKey{pub: pub, priv: priv}
	if _, err := io.ReadFull(rand.Reader, k.keyID[:]); err != nil {
		t.Fatalf("generate key id: %v", err)
	}
	return k
}

func (k *testKey) publicKeyFile() []byte {
	line := make([]byte, 0, pubKeyLineLen)
	line = append(line, AlgPrehashed...)
	line = append(line, k.keyID[:]...)
	line = append(line, k.pub...)
	return []byte(fmt.Sprintf("untrusted comment: minisign public key %X\n%s\n",
		k.keyID, base64.StdEncoding.EncodeToString(line)))
}

// sign produces a minisign signature file over content with the given
// trusted comment. The wire format mirrors the minisign implementation:
// file signature over "<alg><digest|content>", then a global signature over
// "<file signature><trusted comment>".
func (k *testKey) sign(alg string, content []byte, trustedComment, untrusted string) []byte {
	var preimage []byte
	switch alg {
	case AlgPrehashed:
		hasher := newBlake2b(64)
		hasher.Write(content)
		preimage = append(append([]byte{}, AlgPrehashed...), hasher.Sum(nil)...)
	case AlgLegacy:
		preimage = append(append([]byte{}, AlgLegacy...), content...)
	default:
		panic("unknown test algorithm: " + alg)
	}

	fileSig := ed25519.Sign(k.priv, preimage)
	globalMsg := append(append([]byte{}, fileSig...), trustedComment...)
	globalSig := ed25519.Sign(k.priv, globalMsg)

	sigLine := make([]byte, 0, signatureLineLen)
	sigLine = append(sigLine, alg...)
	sigLine = append(sigLine, k.keyID[:]...)
	sigLine = append(sigLine, fileSig...)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "untrusted comment: %s\n", untrusted)
	fmt.Fprintf(&buf, "%s\n", base64.StdEncoding.EncodeToString(sigLine))
	fmt.Fprintf(&buf, "trusted comment: %s\n", trustedComment)
	fmt.Fprintf(&buf, "%s\n", base64.StdEncoding.EncodeToString(globalSig))
	return buf.Bytes()
}

func mustParsePublicKey(t testTB, data []byte) *PublicKey {
	t.Helper()
	pk, err := ParsePublicKey(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	return pk
}

func mustParseSignature(t testTB, data []byte) *Signature {
	t.Helper()
	sig, err := ParseSignature(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse signature: %v", err)
	}
	return sig
}

// trustedComment builds the canonical authenticated metadata line.
func trustedComment(file string, timestamp int64, version string) string {
	return fmt.Sprintf("file=%s\ttimestamp=%d\tversion=%s", file, timestamp, version)
}
