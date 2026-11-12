package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func mustChain(t *testing.T, name string) *testChain {
	t.Helper()
	chain, err := newTestChain(name)
	if err != nil {
		t.Fatalf("create test chain: %v", err)
	}
	return chain
}

func highOpts(chain *testChain) Options {
	return Options{Policy: HighSecurity, Trust: &TrustStore{Roots: chain.trustPool()}}
}

func TestVerifyValidSignedGem(t *testing.T) {
	chain := mustChain(t, "valid")
	gem := newGemFixture().build(buildConfig{signer: chain})

	medium, err := Verify(bytes.NewReader(gem), Options{Policy: MediumSecurity})
	if err != nil {
		t.Fatalf("medium verify: %v", err)
	}
	if !medium.Signed || medium.Signer == nil {
		t.Fatalf("medium result should report signed gem: %+v", medium)
	}

	high, err := Verify(bytes.NewReader(gem), highOpts(chain))
	if err != nil {
		t.Fatalf("high verify: %v", err)
	}
	if !high.Signed || !bytes.Equal(high.Signer.Raw, chain.Leaf.Raw) {
		t.Fatalf("high result should expose the signer leaf")
	}
}

func TestVerifyUnsignedGem(t *testing.T) {
	gem := newGemFixture().build(buildConfig{})

	medium, err := Verify(bytes.NewReader(gem), Options{Policy: MediumSecurity})
	if err != nil {
		t.Fatalf("medium should allow unsigned gems: %v", err)
	}
	if medium.Signed {
		t.Fatalf("unsigned gem must report Signed=false")
	}

	if _, err := Verify(bytes.NewReader(gem), highOpts(mustChain(t, "r"))); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("high should reject unsigned gem, got %v", err)
	}
}

func TestVerifyStrippedSignature(t *testing.T) {
	chain := mustChain(t, "strip")
	stripped := newGemFixture().build(buildConfig{
		signer:   chain,
		omitSigs: []string{MemberData + ".sig"},
	})

	for _, opts := range []Options{
		{Policy: MediumSecurity},
		highOpts(chain),
	} {
		if _, err := Verify(bytes.NewReader(stripped), opts); !errors.Is(err, ErrPartialSignature) {
			t.Fatalf("stripped single .sig must fail with ErrPartialSignature, got %v", err)
		}
	}
}

func TestVerifyHalfSignedEveryCombination(t *testing.T) {
	chain := mustChain(t, "half")

	t.Run("metadata only", func(t *testing.T) {
		gem := newGemFixture().build(buildConfig{
			signer:   chain,
			omitSigs: []string{MemberData + ".sig", MemberChecksums + ".sig"},
		})
		assertPartial(t, gem, chain)
	})

	t.Run("checksums only", func(t *testing.T) {
		gem := newGemFixture().build(buildConfig{
			signer:   chain,
			omitSigs: []string{MemberMetadata + ".sig", MemberData + ".sig"},
		})
		assertPartial(t, gem, chain)
	})

	t.Run("two of three", func(t *testing.T) {
		gem := newGemFixture().build(buildConfig{
			signer:   chain,
			omitSigs: []string{MemberChecksums + ".sig"},
		})
		assertPartial(t, gem, chain)
	})
}

func assertPartial(t *testing.T, gem []byte, chain *testChain) {
	t.Helper()
	for _, opts := range []Options{{Policy: MediumSecurity}, highOpts(chain)} {
		if _, err := Verify(bytes.NewReader(gem), opts); !errors.Is(err, ErrPartialSignature) {
			t.Fatalf("expected ErrPartialSignature, got %v", err)
		}
	}
}

func TestVerifyDuplicateMember(t *testing.T) {
	chain := mustChain(t, "dup")
	gem := newGemFixture().build(buildConfig{signer: chain, duplicateData: true})

	for _, opts := range []Options{{Policy: MediumSecurity}, highOpts(chain)} {
		if _, err := Verify(bytes.NewReader(gem), opts); !errors.Is(err, ErrDuplicateMember) {
			t.Fatalf("duplicate data.tar.gz must fail with ErrDuplicateMember, got %v", err)
		}
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	chain := mustChain(t, "sum")

	t.Run("forged checksum entry", func(t *testing.T) {
		gem := newGemFixture().build(buildConfig{signer: chain, corruptChecksum: true})
		for _, opts := range []Options{{Policy: MediumSecurity}, highOpts(chain)} {
			if _, err := Verify(bytes.NewReader(gem), opts); !errors.Is(err, ErrChecksumMismatch) {
				t.Fatalf("forged checksum must fail with ErrChecksumMismatch, got %v", err)
			}
		}
	})

	t.Run("tampered member bytes", func(t *testing.T) {
		gem := newGemFixture().build(buildConfig{signer: chain, tamperMetadataAfterBuild: true})
		for _, opts := range []Options{{Policy: MediumSecurity}, highOpts(chain)} {
			_, err := Verify(bytes.NewReader(gem), opts)
			if !errors.Is(err, ErrChecksumMismatch) && !errors.Is(err, ErrInvalidSignature) {
				t.Fatalf("tampered member must fail checksum or signature check, got %v", err)
			}
		}
	})

	t.Run("duplicate checksum key", func(t *testing.T) {
		gem := newGemFixture().build(buildConfig{signer: chain, duplicateChecksumKey: true})
		for _, opts := range []Options{{Policy: MediumSecurity}, highOpts(chain)} {
			if _, err := Verify(bytes.NewReader(gem), opts); !errors.Is(err, ErrMalformedChecksums) {
				t.Fatalf("duplicate YAML key must fail, got %v", err)
			}
		}
	})
}

func TestVerifyWrongChain(t *testing.T) {
	signer := mustChain(t, "signer")
	other := mustChain(t, "other")

	t.Run("root not trusted under high security", func(t *testing.T) {
		gem := newGemFixture().build(buildConfig{signer: signer})
		if _, err := Verify(bytes.NewReader(gem), highOpts(other)); !errors.Is(err, ErrUntrustedChain) {
			t.Fatalf("untrusted root must fail under high security, got %v", err)
		}
	})

	t.Run("missing trust store under high security", func(t *testing.T) {
		gem := newGemFixture().build(buildConfig{signer: signer})
		if _, err := Verify(bytes.NewReader(gem), Options{Policy: HighSecurity}); !errors.Is(err, ErrNoTrustRoots) {
			t.Fatalf("high security without roots must fail, got %v", err)
		}
	})

	t.Run("structurally valid chain accepted by medium security", func(t *testing.T) {
		gem := newGemFixture().build(buildConfig{signer: signer})
		if _, err := Verify(bytes.NewReader(gem), Options{Policy: MediumSecurity}); err != nil {
			t.Fatalf("medium security verifies present signatures without a trust root: %v", err)
		}
	})
}

func TestVerifyBrokenChain(t *testing.T) {
	signer := mustChain(t, "signer")
	externalCA := mustChain(t, "external")

	// Re-issue the signer leaf under an intermediate whose issuer (the
	// external root) is deliberately omitted from the signature. The embedded
	// chain therefore cannot close, even though the RSA signature itself is
	// cryptographically valid.
	foreignInter, foreignInterKey, err := makeIntermediate(externalCA, "orphan")
	if err != nil {
		t.Fatal(err)
	}
	reissuedLeaf, err := makeLeaf(foreignInter, foreignInterKey, &signer.leafKey.PublicKey, "reissued")
	if err != nil {
		t.Fatal(err)
	}
	broken := &testChain{
		Intermediate: foreignInter,
		Leaf:         reissuedLeaf,
		leafKey:      signer.leafKey,
	}
	gem := newGemFixture().build(buildConfig{signer: broken})

	for _, opts := range []Options{{Policy: MediumSecurity}, highOpts(signer)} {
		_, err := Verify(bytes.NewReader(gem), opts)
		if !errors.Is(err, ErrInvalidSignature) && !errors.Is(err, ErrUntrustedChain) {
			t.Fatalf("wrong chain must fail signature or chain validation, got %v", err)
		}
	}
}

func TestVerifyWeakAlgorithmRejected(t *testing.T) {
	chain := mustChain(t, "weak")
	gem := newGemFixture().build(buildConfig{signer: chain, algo: rsaSHA1Algo})

	for _, opts := range []Options{{Policy: MediumSecurity}, highOpts(chain)} {
		if _, err := Verify(bytes.NewReader(gem), opts); !errors.Is(err, ErrUnsupportedAlgorithm) {
			t.Fatalf("RSA-SHA1 must fail with ErrUnsupportedAlgorithm, got %v", err)
		}
	}
}

func TestVerifyMixedSigners(t *testing.T) {
	chainA := mustChain(t, "a")
	chainB := mustChain(t, "b")

	members := newGemFixture().build(buildConfig{signer: chainA})
	mixed := rebuildWithForeignDataSig(t, members, chainA, chainB)

	for _, opts := range []Options{{Policy: MediumSecurity}, highOpts(chainA)} {
		if _, err := Verify(bytes.NewReader(mixed), opts); !errors.Is(err, ErrSignerMismatch) {
			t.Fatalf("mixed signers must fail with ErrSignerMismatch, got %v", err)
		}
	}
}

func TestVerifyUnknownMember(t *testing.T) {
	chain := mustChain(t, "extra")
	gem := newGemFixture().build(buildConfig{signer: chain, extraMember: true})

	for _, opts := range []Options{{Policy: MediumSecurity}, highOpts(chain)} {
		if _, err := Verify(bytes.NewReader(gem), opts); !errors.Is(err, ErrUnknownMember) {
			t.Fatalf("unknown member must fail with ErrUnknownMember, got %v", err)
		}
	}
}

func TestReadMembersRejectsDuplicateDirectly(t *testing.T) {
	chain := mustChain(t, "direct")
	gem := newGemFixture().build(buildConfig{signer: chain, duplicateData: true})

	if _, err := ReadMembers(bytes.NewReader(gem), 0); !errors.Is(err, ErrDuplicateMember) {
		t.Fatalf("ReadMembers must reject duplicate names, got %v", err)
	}
}
