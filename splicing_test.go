package netsec

import (
	"errors"
	"testing"
)

func TestTwoLegitimateChainsVerify(t *testing.T) {
	env := newChainEnv(t)
	hopsB := []string{"edge-b", "dmz-proxy-b", "internal-gateway-b"}

	chainA := buildLegitimateChain(t, env,
		"CN=client-a,OU=sales", "svc://orders/grpc", "nonce-AAAA",
		[3]Assurance{AssuranceValidChainAndIdentity, AssuranceValidChain, AssuranceValidChain})
	chainB := buildLegitimateChain(t, env,
		"CN=client-b,OU=ops", "svc://billing/grpc", "nonce-BBBB",
		[3]Assurance{AssuranceValidChainAndIdentity, AssuranceValidChainAndIdentity, AssuranceValidChain}, hopsB)

	resA, err := Verify(chainA, VerifyConfig{
		KeyRing:           env.ring,
		ExpectedChallenge: "nonce-AAAA",
		ExpectedAudience:  "svc://orders/grpc",
		ExpectedClient:    "CN=client-a,OU=sales",
		ExpectedHops:      env.hops,
		Now:               testTime,
	})
	if err != nil {
		t.Fatalf("chain A verify: %v", err)
	}
	if resA.ClientID != "CN=client-a,OU=sales" || resA.FirstVerifier != "edge" {
		t.Fatalf("unexpected root facts: %+v", resA)
	}
	if resA.EffectiveAssurance != AssuranceValidChain {
		t.Fatalf("effective assurance = %v, want valid-chain", resA.EffectiveAssurance)
	}

	resB, err := Verify(chainB, VerifyConfig{
		KeyRing:           env.ring,
		ExpectedChallenge: "nonce-BBBB",
		ExpectedAudience:  "svc://billing/grpc",
		ExpectedClient:    "CN=client-b,OU=ops",
		ExpectedHops:      hopsB,
		Now:               testTime,
	})
	if err != nil {
		t.Fatalf("chain B verify: %v", err)
	}
	if resB.EffectiveAssurance != AssuranceValidChain {
		t.Fatalf("effective assurance = %v, want valid-chain", resB.EffectiveAssurance)
	}
	if len(resA.Hops) != 3 || resA.Hops[1].VerifiedSubject != "edge" {
		t.Fatalf("hop identity binding wrong: %+v", resA.Hops)
	}
	if resB.FirstVerifier != "edge-b" || resB.ClientID != "CN=client-b,OU=ops" {
		t.Fatalf("chain B root facts wrong: %+v", resB)
	}
}

// reattest rebuilds one attestation from another chain's segment, signed by
// the same hop key but pointing at a chosen previous digest. This models an
// attacker that controls a hop trying to graft segments between connections.
func reattest(t *testing.T, env *chainEnv, template *Attestation, prev [32]byte, index int) *Attestation {
	t.Helper()
	signer := env.signers[template.HopID]
	f := Fact{
		HopID:           template.HopID,
		KeyID:           template.KeyID,
		VerifiedSubject: template.VerifiedSubject,
		Audience:        template.Audience,
		Challenge:       template.Challenge,
		Assurance:       template.Assurance,
		UpstreamMin:     template.UpstreamMin,
		NotBefore:       template.NotBefore,
		NotAfter:        template.NotAfter,
	}
	a, err := signer.Sign(f, index, prev)
	if err != nil {
		t.Fatalf("reattest: %v", err)
	}
	return a
}

func splice(parts ...[]*Attestation) *Chain {
	var all []*Attestation
	for _, p := range parts {
		all = append(all, p...)
	}
	for i, a := range all {
		a.Index = i
	}
	return &Chain{attestations: all}
}

func TestCrossChainSegmentSplicingIsRejected(t *testing.T) {
	env := newChainEnv(t)
	hopsB := []string{"edge-b", "dmz-proxy-b", "internal-gateway-b"}
	chainA := buildLegitimateChain(t, env,
		"CN=client-a,OU=sales", "svc://orders/grpc", "nonce-AAAA",
		[3]Assurance{AssuranceValidChainAndIdentity, AssuranceValidChain, AssuranceValidChain})
	chainB := buildLegitimateChain(t, env,
		"CN=client-b,OU=ops", "svc://billing/grpc", "nonce-BBBB",
		[3]Assurance{AssuranceValidChainAndIdentity, AssuranceValidChainAndIdentity, AssuranceValidChain}, hopsB)

	A := chainA.Attestations()
	B := chainB.Attestations()

	cfg := VerifyConfig{
		KeyRing:           env.ring,
		ExpectedChallenge: "nonce-AAAA",
		ExpectedAudience:  "svc://orders/grpc",
		ExpectedClient:    "CN=client-a,OU=sales",
		Now:               testTime,
	}

	t.Run("raw segment swap breaks PrevDigest", func(t *testing.T) {
		// B's middle attestation cannot follow A's edge attestation.
		mixed := splice(A[:1], B[1:2], A[2:])
		_, err := Verify(mixed, cfg)
		if !errors.Is(err, ErrBrokenChain) {
			t.Fatalf("want ErrBrokenChain, got %v", err)
		}
	})

	t.Run("reattested graft still carries foreign challenge", func(t *testing.T) {
		edgeDigest, err := AttestationDigest(A[0])
		if err != nil {
			t.Fatal(err)
		}
		// Re-sign B's middle hop against A's edge digest. The signature is
		// valid and the link is intact, but B's challenge/audience remain
		// signed inside the payload and cannot be rewritten.
		graftedMid := reattest(t, env, B[1], edgeDigest, 1)
		tailDigest, err := AttestationDigest(graftedMid)
		if err != nil {
			t.Fatal(err)
		}
		graftedTail := reattest(t, env, A[2], tailDigest, 2)
		// A's tail still has A's audience/challenge; fix its subject to keep
		// the test focused on the context binding rather than identity.
		mixed := splice(A[:1], []*Attestation{graftedMid}, []*Attestation{graftedTail})
		_, err = Verify(mixed, cfg)
		if !errors.Is(err, ErrChallengeMismatch) {
			t.Fatalf("want ErrChallengeMismatch, got %v", err)
		}
	})

	t.Run("same challenge different audience is rejected", func(t *testing.T) {
		chainC := buildLegitimateChain(t, env,
			"CN=client-a,OU=sales", "svc://other/grpc", "nonce-AAAA",
			[3]Assurance{AssuranceValidChainAndIdentity, AssuranceValidChain, AssuranceValidChain})
		C := chainC.Attestations()
		mixed := splice(A[:1], C[1:])
		_, err := Verify(mixed, cfg)
		if !errors.Is(err, ErrBrokenChain) && !errors.Is(err, ErrAudienceMismatch) {
			t.Fatalf("want link or audience error, got %v", err)
		}

		// With a re-signed link the audience binding itself must fire.
		edgeDigest, _ := AttestationDigest(A[0])
		mid := reattest(t, env, C[1], edgeDigest, 1)
		midDigest, _ := AttestationDigest(mid)
		tail := reattest(t, env, C[2], midDigest, 2)
		relinked := splice(A[:1], []*Attestation{mid}, []*Attestation{tail})
		_, err = Verify(relinked, cfg)
		if !errors.Is(err, ErrAudienceMismatch) {
			t.Fatalf("want ErrAudienceMismatch, got %v", err)
		}
	})

	t.Run("same context different client identity cannot be grafted", func(t *testing.T) {
		chainD := buildLegitimateChain(t, env,
			"CN=client-b,OU=ops", "svc://orders/grpc", "nonce-AAAA",
			[3]Assurance{AssuranceValidChainAndIdentity, AssuranceValidChain, AssuranceValidChain}, hopsB)
		D := chainD.Attestations()
		edgeDigest, _ := AttestationDigest(A[0])
		mid := reattest(t, env, D[1], edgeDigest, 1)
		midDigest, _ := AttestationDigest(mid)
		tail := reattest(t, env, D[2], midDigest, 2)
		relinked := splice(A[:1], []*Attestation{mid}, []*Attestation{tail})
		_, err := Verify(relinked, cfg)
		// D's middle hop verified edge-b, which never appeared in this chain:
		// the subject binding detects the identity mix.
		if !errors.Is(err, ErrIdentityMismatch) {
			t.Fatalf("want ErrIdentityMismatch, got %v", err)
		}
	})

	t.Run("foreign root presented to connection context", func(t *testing.T) {
		_, err := Verify(chainB, cfg)
		if !errors.Is(err, ErrChallengeMismatch) && !errors.Is(err, ErrAudienceMismatch) &&
			!errors.Is(err, ErrIdentityMismatch) {
			t.Fatalf("want context mismatch, got %v", err)
		}
	})
}
