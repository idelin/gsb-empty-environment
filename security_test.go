package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func baseVerifyCfg(env *chainEnv) VerifyConfig {
	return VerifyConfig{
		KeyRing:           env.ring,
		ExpectedChallenge: "nonce-AAAA",
		ExpectedAudience:  "svc://orders/grpc",
		ExpectedClient:    "CN=client-a,OU=sales",
		Now:               testTime,
	}
}

func TestTamperedSignatureRejected(t *testing.T) {
	env := newChainEnv(t)
	chain := buildLegitimateChain(t, env,
		"CN=client-a,OU=sales", "svc://orders/grpc", "nonce-AAAA",
		[3]Assurance{AssuranceValidChainAndIdentity, AssuranceValidChain, AssuranceValidChain})

	tampered := chain.Attestations()
	tampered[1].Signature[0] ^= 0xFF
	_, err := Verify(&Chain{attestations: tampered}, baseVerifyCfg(env))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestUnknownAndMismatchedKeyRejected(t *testing.T) {
	env := newChainEnv(t)
	chain := buildLegitimateChain(t, env,
		"CN=client-a,OU=sales", "svc://orders/grpc", "nonce-AAAA",
		[3]Assurance{AssuranceValidChainAndIdentity, AssuranceValidChain, AssuranceValidChain})

	t.Run("unknown key id", func(t *testing.T) {
		att := chain.Attestations()
		att[2].KeyID = "rogue-key"
		_, err := Verify(&Chain{attestations: att}, baseVerifyCfg(env))
		if !errors.Is(err, ErrUnknownKey) {
			t.Fatalf("want ErrUnknownKey, got %v", err)
		}
	})

	t.Run("key id bound to another hop", func(t *testing.T) {
		other := NewKeyRing()
		for hop, s := range env.signers {
			if err := other.Add(hop+"-impersonated", s.KeyID(), s.Alg(), s.Public()); err != nil {
				t.Fatal(err)
			}
		}
		cfg := baseVerifyCfg(env)
		cfg.KeyRing = other
		_, err := Verify(chain, cfg)
		if !errors.Is(err, ErrHopIdentityMismatch) {
			t.Fatalf("want ErrHopIdentityMismatch, got %v", err)
		}
	})
}

func TestValidityWindowEnforced(t *testing.T) {
	env := newChainEnv(t)
	chain := buildLegitimateChain(t, env,
		"CN=client-a,OU=sales", "svc://orders/grpc", "nonce-AAAA",
		[3]Assurance{AssuranceValidChainAndIdentity, AssuranceValidChain, AssuranceValidChain})

	cfg := baseVerifyCfg(env)
	cfg.Now = windowEnd.Add(time.Minute)
	if _, err := Verify(chain, cfg); !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}

	cfg.Now = windowStart.Add(-time.Minute)
	if _, err := Verify(chain, cfg); !errors.Is(err, ErrNotYetValid) {
		t.Fatalf("want ErrNotYetValid, got %v", err)
	}
}

func TestAssuranceCannotBeElevated(t *testing.T) {
	env := newChainEnv(t)
	// Middle hop only saw a presented certificate, so the effective level
	// drops. A later hop must not claim a stronger upstream minimum.
	chain := buildLegitimateChain(t, env,
		"CN=client-a,OU=sales", "svc://orders/grpc", "nonce-AAAA",
		[3]Assurance{AssuranceValidChainAndIdentity, AssurancePresent, AssuranceValidChainAndIdentity})

	res, err := Verify(chain, baseVerifyCfg(env))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.EffectiveAssurance != AssurancePresent {
		t.Fatalf("effective = %v, want cert-present", res.EffectiveAssurance)
	}

	att := chain.Attestations()
	digest, err := AttestationDigest(att[1])
	if err != nil {
		t.Fatal(err)
	}
	rogueFact := Fact{
		HopID:           att[2].HopID,
		KeyID:           att[2].KeyID,
		VerifiedSubject: att[2].VerifiedSubject,
		Audience:        att[2].Audience,
		Challenge:       att[2].Challenge,
		Assurance:       AssuranceValidChainAndIdentity,
		UpstreamMin:     AssuranceValidChainAndIdentity, // inflates the weak hop
		NotBefore:       att[2].NotBefore,
		NotAfter:        att[2].NotAfter,
	}
	rogue, err := env.signers["internal-gateway"].Sign(rogueFact, 2, digest)
	if err != nil {
		t.Fatal(err)
	}
	att[2] = rogue
	_, err = Verify(&Chain{attestations: att}, baseVerifyCfg(env))
	if !errors.Is(err, ErrAssuranceElevation) {
		t.Fatalf("want ErrAssuranceElevation, got %v", err)
	}

	// MinAssurance gate must reject the weakened chain even when honestly
	// reported.
	honest := buildLegitimateChain(t, env,
		"CN=client-a,OU=sales", "svc://orders/grpc", "nonce-AAAA",
		[3]Assurance{AssuranceValidChainAndIdentity, AssurancePresent, AssuranceValidChainAndIdentity})
	cfg := baseVerifyCfg(env)
	cfg.MinAssurance = AssuranceValidChain
	if _, err := Verify(honest, cfg); !errors.Is(err, ErrAssuranceElevation) {
		t.Fatalf("want minimum-assurance failure, got %v", err)
	}
}

func TestRootUpstreamAndGenesisBinding(t *testing.T) {
	env := newChainEnv(t)
	s := env.signers["edge"]
	genesis := GenesisDigest("svc://orders/grpc", "nonce-AAAA")

	rogue, err := s.Sign(fact("edge", s.KeyID(), "CN=client-a,OU=sales",
		"svc://orders/grpc", "nonce-AAAA", AssuranceValidChainAndIdentity,
		AssuranceValidChain), 0, genesis)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewChain(rogue); !errors.Is(err, ErrAssuranceElevation) {
		t.Fatalf("want root elevation error, got %v", err)
	}

	wrongGenesis := GenesisDigest("svc://orders/grpc", "nonce-other")
	bad, err := s.Sign(fact("edge", s.KeyID(), "CN=client-a,OU=sales",
		"svc://orders/grpc", "nonce-AAAA", AssuranceValidChainAndIdentity,
		AssuranceNone), 0, wrongGenesis)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewChain(bad); !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("want genesis link error, got %v", err)
	}
}

func TestSignerIsHopBound(t *testing.T) {
	env := newChainEnv(t)
	edge := env.signers["edge"]
	genesis := GenesisDigest("svc://orders/grpc", "nonce-AAAA")
	_, err := edge.Sign(fact("dmz-proxy", edge.KeyID(), "CN=x",
		"svc://orders/grpc", "nonce-AAAA", AssuranceValidChain, AssuranceNone), 0, genesis)
	if !errors.Is(err, ErrHopIdentityMismatch) {
		t.Fatalf("want ErrHopIdentityMismatch, got %v", err)
	}

	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSigner("edge", "edge-key-1", other); err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
}

func TestEncodeParseRoundTrip(t *testing.T) {
	env := newChainEnv(t)
	chain := buildLegitimateChain(t, env,
		"CN=client-a,OU=sales", "svc://orders/grpc", "nonce-AAAA",
		[3]Assurance{AssuranceValidChainAndIdentity, AssuranceValidChain, AssuranceValidChain})

	raw, err := chain.Encode()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseChain(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Len() != chain.Len() {
		t.Fatalf("len = %d, want %d", parsed.Len(), chain.Len())
	}
	for i := 0; i < chain.Len(); i++ {
		want, _ := chain.At(i)
		got, _ := parsed.At(i)
		wd, _ := AttestationDigest(want)
		gd, _ := AttestationDigest(got)
		if !bytes.Equal(wd[:], gd[:]) {
			t.Fatalf("attestation %d digest changed after round trip", i)
		}
	}
	if _, err := Verify(parsed, baseVerifyCfg(env)); err != nil {
		t.Fatalf("parsed chain verify: %v", err)
	}
}

func TestImmutableAppendDoesNotMutateReceiver(t *testing.T) {
	env := newChainEnv(t)
	chain := buildLegitimateChain(t, env,
		"CN=client-a,OU=sales", "svc://orders/grpc", "nonce-AAAA",
		[3]Assurance{AssuranceValidChainAndIdentity, AssuranceValidChain, AssuranceValidChain})

	att := chain.Attestations()
	att[0].HopID = "mutated"
	again, err := Verify(chain, baseVerifyCfg(env))
	if err != nil {
		t.Fatalf("chain must stay immutable after caller copy mutation: %v", err)
	}
	if again.Hops[0].HopID != "edge" {
		t.Fatalf("chain state leaked from caller mutation")
	}
}
