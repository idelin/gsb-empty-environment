package taskpkg

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

type testKey struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
	fp   Fingerprint
}

func genTestKey(t *testing.T) testKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return testKey{pub: pub, priv: priv, fp: FingerprintOf(pub)}
}

func keyMap(keys ...testKey) map[Fingerprint]ed25519.PublicKey {
	m := make(map[Fingerprint]ed25519.PublicKey, len(keys))
	for _, k := range keys {
		m[k.fp] = k.pub
	}
	return m
}

func signAnswer(t *testing.T, key testKey, zone string, id uint64, qname string, version uint64, rrdata []byte) *SignedAnswer {
	t.Helper()
	ans := &SignedAnswer{
		Zone:    zone,
		QueryID: id,
		QName:   qname,
		Version: version,
		RRData:  rrdata,
		Signer:  key.fp,
	}
	ans.Signature = ed25519.Sign(key.priv, signedPayload(ans))
	return ans
}

func mustVerify(t *testing.T, r *Resolver, q Query, ans *SignedAnswer) *VerifiedAnswer {
	t.Helper()
	va, err := r.Verify(q, ans)
	if err != nil {
		t.Fatalf("Verify: unexpected error: %v", err)
	}
	return va
}
