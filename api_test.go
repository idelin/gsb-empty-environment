package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"io"
	"testing"
	"time"
)

func TestNewSignerRejectsBadInputs(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSigner("", "k", key); !errors.Is(err, ErrInvalidFact) {
		t.Fatalf("empty hop: %v", err)
	}
	if _, err := NewSigner("h", "k", nil); !errors.Is(err, ErrInvalidFact) {
		t.Fatalf("nil key: %v", err)
	}
	if _, err := NewSigner("h", "k", dummySigner{}); !errors.Is(err, ErrUnsupportedAlg) {
		t.Fatalf("unsupported key: %v", err)
	}
}

type dummySigner struct{}

func (dummySigner) Public() crypto.PublicKey { return nil }
func (dummySigner) Sign(_ io.Reader, _ []byte, _ crypto.SignerOpts) ([]byte, error) {
	return nil, nil
}

func TestFactValidationAndSignRejection(t *testing.T) {
	env := newChainEnv(t)
	s := env.signers["edge"]
	genesis := GenesisDigest("svc://orders/grpc", "nonce-AAAA")

	good := fact("edge", s.KeyID(), "CN=x", "svc://orders/grpc", "nonce-AAAA",
		AssuranceValidChain, AssuranceNone)

	empty := good
	empty.Challenge = ""
	if _, err := s.Sign(empty, 0, genesis); !errors.Is(err, ErrInvalidFact) {
		t.Fatalf("empty challenge: %v", err)
	}

	badWindow := good
	badWindow.NotAfter = good.NotBefore.Add(-time.Second)
	if _, err := s.Sign(badWindow, 0, genesis); !errors.Is(err, ErrInvalidFact) {
		t.Fatalf("bad window: %v", err)
	}

	wrongKey := good
	wrongKey.KeyID = "other-key"
	if _, err := s.Sign(wrongKey, 0, genesis); !errors.Is(err, ErrInvalidFact) {
		t.Fatalf("wrong key id: %v", err)
	}

	if _, err := s.Sign(good, -1, genesis); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("negative index: %v", err)
	}
}

func TestKeyRingValidation(t *testing.T) {
	ring := NewKeyRing()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := ring.Add("", "k", AlgECDSAP256SHA256, &key.PublicKey); !errors.Is(err, ErrInvalidFact) {
		t.Fatalf("empty hop: %v", err)
	}
	if err := ring.Add("h", "k", AlgEd25519, &key.PublicKey); !errors.Is(err, ErrUnsupportedAlg) {
		t.Fatalf("wrong key type: %v", err)
	}
	if err := ring.Add("h", "k", "bogus-alg", &key.PublicKey); !errors.Is(err, ErrUnsupportedAlg) {
		t.Fatalf("bogus alg: %v", err)
	}
	if err := ring.Add("h", "k", AlgECDSAP256SHA256, &key.PublicKey); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := ring.Add("other", "k", AlgECDSAP256SHA256, &key.PublicKey); !errors.Is(err, ErrInvalidFact) {
		t.Fatalf("duplicate key id: %v", err)
	}
}

func TestChainAndParseErrors(t *testing.T) {
	if _, err := NewChain(nil); !errors.Is(err, ErrEmptyChain) {
		t.Fatalf("nil root: %v", err)
	}
	if _, err := ParseChain([]byte("{not json")); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("bad json: %v", err)
	}
	if _, err := ParseChain(nil); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("nil raw: %v", err)
	}
	if _, err := ParseChain([]byte("[]")); !errors.Is(err, ErrEmptyChain) {
		t.Fatalf("empty array: %v", err)
	}

	var c *Chain
	if c.Len() != 0 {
		t.Fatal("nil chain len must be 0")
	}
	if _, err := c.Encode(); !errors.Is(err, ErrEmptyChain) {
		t.Fatalf("nil encode: %v", err)
	}
	if _, err := c.At(0); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("nil at: %v", err)
	}

	env := newChainEnv(t)
	chain := buildLegitimateChain(t, env,
		"CN=client-a,OU=sales", "svc://orders/grpc", "nonce-AAAA",
		[3]Assurance{AssuranceValidChainAndIdentity, AssuranceValidChain, AssuranceValidChain})
	if _, err := chain.Append(nil, env.signers["edge"]); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("nil append: %v", err)
	}

	raw, _ := chain.Encode()
	parsed, err := ParseChain(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parsed.At(99); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("out of range: %v", err)
	}
}

func TestVerifyRequiresKeyRingAndContextMismatches(t *testing.T) {
	env := newChainEnv(t)
	chain := buildLegitimateChain(t, env,
		"CN=client-a,OU=sales", "svc://orders/grpc", "nonce-AAAA",
		[3]Assurance{AssuranceValidChainAndIdentity, AssuranceValidChain, AssuranceValidChain})

	if _, err := Verify(chain, VerifyConfig{Now: testTime}); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("missing ring: %v", err)
	}
	base := baseVerifyCfg(env)

	wrongChallenge := base
	wrongChallenge.ExpectedChallenge = "nonce-OTHER"
	if _, err := Verify(chain, wrongChallenge); !errors.Is(err, ErrChallengeMismatch) {
		t.Fatalf("challenge: %v", err)
	}

	wrongAudience := base
	wrongAudience.ExpectedAudience = "svc://other"
	if _, err := Verify(chain, wrongAudience); !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("audience: %v", err)
	}

	wrongClient := base
	wrongClient.ExpectedClient = "CN=attacker"
	if _, err := Verify(chain, wrongClient); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("client: %v", err)
	}

	wrongHops := base
	wrongHops.ExpectedHops = []string{"edge", "dmz-proxy"}
	if _, err := Verify(chain, wrongHops); !errors.Is(err, ErrHopIdentityMismatch) {
		t.Fatalf("hop topology: %v", err)
	}

	wrongHopAt := base
	wrongHopAt.ExpectedHops = []string{"edge", "internal-gateway", "dmz-proxy"}
	if _, err := Verify(chain, wrongHopAt); !errors.Is(err, ErrHopIdentityMismatch) {
		t.Fatalf("hop order: %v", err)
	}
}

func TestMalformedAttestationShapesRejected(t *testing.T) {
	env := newChainEnv(t)
	chain := buildLegitimateChain(t, env,
		"CN=client-a,OU=sales", "svc://orders/grpc", "nonce-AAAA",
		[3]Assurance{AssuranceValidChainAndIdentity, AssuranceValidChain, AssuranceValidChain})

	t.Run("duplicate hop id rejected", func(t *testing.T) {
		att := chain.Attestations()
		edgeDigest, _ := AttestationDigest(att[0])
		// Re-sign the edge attestation as if it were a second hop: even a
		// valid signature cannot make the same hop appear twice.
		dup, err := env.signers["edge"].Sign(Fact{
			HopID:           "edge",
			KeyID:           env.signers["edge"].KeyID(),
			VerifiedSubject: "edge",
			Audience:        att[0].Audience,
			Challenge:       att[0].Challenge,
			Assurance:       AssuranceValidChain,
			UpstreamMin:     AssuranceValidChainAndIdentity,
			NotBefore:       att[0].NotBefore,
			NotAfter:        att[0].NotAfter,
		}, 1, edgeDigest)
		if err != nil {
			t.Fatal(err)
		}
		att[1] = dup
		_, err = Verify(&Chain{attestations: att[:2]}, baseVerifyCfg(env))
		if !errors.Is(err, ErrHopIdentityMismatch) {
			t.Fatalf("want ErrHopIdentityMismatch, got %v", err)
		}
	})

	cases := func(mut func(a *Attestation)) *Chain {
		att := chain.Attestations()
		mut(att[1])
		return &Chain{attestations: att}
	}

	mutated := cases(func(a *Attestation) { a.Version = 99 })
	if _, err := Verify(mutated, baseVerifyCfg(env)); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("version: %v", err)
	}

	mutated = cases(func(a *Attestation) { a.HopID = "" })
	if _, err := Verify(mutated, baseVerifyCfg(env)); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("empty hop: %v", err)
	}

	mutated = cases(func(a *Attestation) { a.PrevDigest = []byte{1, 2, 3} })
	if _, err := Verify(mutated, baseVerifyCfg(env)); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("short digest: %v", err)
	}

	mutated = cases(func(a *Attestation) { a.Signature = nil })
	if _, err := Verify(mutated, baseVerifyCfg(env)); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("empty signature: %v", err)
	}
}
