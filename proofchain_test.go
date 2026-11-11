package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"
)

type hopKeys struct {
	signer *Signer
	alg    string
}

func newHopKeys(t *testing.T, hopID, keyID string, alg string) hopKeys {
	t.Helper()
	var key crypto.Signer
	switch alg {
	case AlgECDSAP256SHA256:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("ecdsa key: %v", err)
		}
		key = k
	case AlgEd25519:
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("ed25519 key: %v", err)
		}
		key = priv
	case AlgRSAPSSSHA256:
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("rsa key: %v", err)
		}
		key = k
	default:
		t.Fatalf("unknown alg %q", alg)
	}
	s, err := NewSigner(hopID, keyID, key)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return hopKeys{signer: s, alg: alg}
}

var (
	testTime    = time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	windowStart = testTime.Add(-time.Minute)
	windowEnd   = testTime.Add(10 * time.Minute)
)

type chainEnv struct {
	ring     *KeyRing
	signers  map[string]*Signer
	hops     []string
	audience string
}

func registerHops(t *testing.T, ring *KeyRing, signers map[string]*Signer, hops, algs []string) {
	t.Helper()
	for i, hop := range hops {
		hk := newHopKeys(t, hop, hop+"-key-1", algs[i])
		signers[hop] = hk.signer
		if err := ring.Add(hop, hk.signer.KeyID(), hk.alg, hk.signer.Public()); err != nil {
			t.Fatalf("ring add: %v", err)
		}
	}
}

func newChainEnv(t *testing.T) *chainEnv {
	t.Helper()
	algs := []string{AlgECDSAP256SHA256, AlgEd25519, AlgRSAPSSSHA256}
	hops := []string{"edge", "dmz-proxy", "internal-gateway"}
	env := &chainEnv{
		ring:    NewKeyRing(),
		signers: make(map[string]*Signer),
		hops:    hops,
	}
	registerHops(t, env.ring, env.signers, hops, algs)
	registerHops(t, env.ring, env.signers,
		[]string{"edge-b", "dmz-proxy-b", "internal-gateway-b"}, algs)
	return env
}

func fact(hopID, keyID, subject, audience, challenge string, assurance, upstream Assurance) Fact {
	return Fact{
		HopID:           hopID,
		KeyID:           keyID,
		VerifiedSubject: subject,
		Audience:        audience,
		Challenge:       challenge,
		Assurance:       assurance,
		UpstreamMin:     upstream,
		NotBefore:       windowStart,
		NotAfter:        windowEnd,
	}
}

// buildLegitimateChain constructs a chain where each hop signs the digest of
// the previous attestation. It uses env.hops unless customHops is supplied.
func buildLegitimateChain(t *testing.T, env *chainEnv, client, audience, challenge string,
	assurances [3]Assurance, customHops ...[]string) *Chain {
	t.Helper()
	hops := env.hops
	if len(customHops) > 0 {
		hops = customHops[0]
	}
	genesis := GenesisDigest(audience, challenge)
	root, err := env.signers[hops[0]].Sign(
		fact(hops[0], env.signers[hops[0]].KeyID(), client, audience, challenge,
			assurances[0], AssuranceNone), 0, genesis)
	if err != nil {
		t.Fatalf("root sign: %v", err)
	}
	chain, err := NewChain(root)
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}
	runningMin := assurances[0]
	for i := 1; i < len(hops); i++ {
		hop := hops[i]
		tailDigest, err := AttestationDigest(chain.attestations[i-1])
		if err != nil {
			t.Fatalf("digest: %v", err)
		}
		a, err := env.signers[hop].Sign(
			fact(hop, env.signers[hop].KeyID(), hops[i-1], audience, challenge,
				assurances[i], runningMin), i, tailDigest)
		if err != nil {
			t.Fatalf("hop %s sign: %v", hop, err)
		}
		chain, err = chain.Append(a, env.signers[hop])
		if err != nil {
			t.Fatalf("append %s: %v", hop, err)
		}
		if assurances[i] < runningMin {
			runningMin = assurances[i]
		}
	}
	return chain
}
