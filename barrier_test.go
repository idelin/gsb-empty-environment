package netsec

import (
	"errors"
	"sync"
	"testing"
)

// chain builds a continuous seal chain of length bound for one partition.
func chain(window WindowID, partition PartitionID, bound Seq, anchor Digest) []Seal {
	seals := make([]Seal, 0, bound)
	prev := anchor
	for seq := Seq(1); seq <= bound; seq++ {
		seal := NewSeal(window, partition, seq, prev)
		seals = append(seals, seal)
		prev = seal.Digest
	}
	return seals
}

func genesisManifest() Manifest {
	return Manifest{
		Window: 1,
		Partitions: []PartitionSpec{
			{Partition: "alpha", UpperBound: 3},
			{Partition: "beta", UpperBound: 2},
		},
	}
}

func mustManifestDigest(t *testing.T, manifest Manifest) Digest {
	t.Helper()
	digest, err := manifest.Digest()
	if err != nil {
		t.Fatalf("manifest digest: %v", err)
	}
	return digest
}

func TestBarrierHappyPathSignsAggregateRoot(t *testing.T) {
	manifest := genesisManifest()
	barrier, err := NewBarrier(manifest)
	if err != nil {
		t.Fatalf("NewBarrier: %v", err)
	}

	alpha := chain(1, "alpha", 3, Digest{})
	beta := chain(1, "beta", 2, Digest{})

	for _, seal := range alpha {
		added, err := barrier.Submit(seal)
		if err != nil || !added {
			t.Fatalf("alpha submit seq %d: added=%v err=%v", seal.Seq, added, err)
		}
	}
	if barrier.Ready() {
		t.Fatal("barrier reports ready with partition beta missing")
	}
	if _, err := barrier.Finalize(nil); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Finalize with missing partition: want ErrIncomplete, got %v", err)
	}

	for _, seal := range beta {
		if _, err := barrier.Submit(seal); err != nil {
			t.Fatalf("beta submit: %v", err)
		}
	}
	if !barrier.Ready() {
		t.Fatalf("barrier not ready; missing=%v", barrier.Missing())
	}

	att, err := barrier.Finalize(nil)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if att.Window != 1 || len(att.Roots) != 2 {
		t.Fatalf("unexpected attestation: %+v", att)
	}
	if att.Signed() {
		t.Fatal("unsigned attestation reports a signature")
	}
	if err := att.Verify(&manifest, nil); err != nil {
		t.Fatalf("attestation Verify: %v", err)
	}

	again, err := barrier.Finalize(nil)
	if err != nil || again.Root != att.Root {
		t.Fatalf("idempotent Finalize mismatch: %v %v", err, again)
	}

	if att.Roots[0].Partition != "alpha" || att.Roots[1].Partition != "beta" {
		t.Fatalf("roots not sorted: %+v", att.Roots)
	}
	wantRoot := AggregateDigest(1, mustManifestDigest(t, manifest), Digest{}, att.Roots)
	if att.Root != wantRoot {
		t.Fatal("aggregate root does not match canonical computation")
	}
}

func TestBarrierRejectsOutOfOrderGapAndAcceptsFill(t *testing.T) {
	barrier, _ := NewBarrier(genesisManifest())
	alpha := chain(1, "alpha", 3, Digest{})

	if _, err := barrier.Submit(alpha[1]); !errors.Is(err, ErrSequenceGap) {
		t.Fatalf("gap submit: want ErrSequenceGap, got %v", err)
	}
	if _, err := barrier.Submit(alpha[0]); err != nil {
		t.Fatalf("gap fill seq 1: %v", err)
	}
	if _, err := barrier.Submit(alpha[1]); err != nil {
		t.Fatalf("resubmit seq 2 after fill: %v", err)
	}
	if _, err := barrier.Submit(alpha[2]); err != nil {
		t.Fatalf("submit seq 3: %v", err)
	}

	accepted, bound, err := barrier.Progress("alpha")
	if err != nil || accepted != 3 || bound != 3 {
		t.Fatalf("Progress alpha = (%d,%d,%v)", accepted, bound, err)
	}
	missing := barrier.Missing()
	if missing["alpha"] != 0 || missing["beta"] != 1 {
		t.Fatalf("unexpected missing map: %v", missing)
	}
}

func TestBarrierDuplicateSealSemantics(t *testing.T) {
	barrier, _ := NewBarrier(genesisManifest())
	seal := NewSeal(1, "alpha", 1, Digest{})

	added, err := barrier.Submit(seal)
	if err != nil || !added {
		t.Fatalf("first submit: added=%v err=%v", added, err)
	}
	added, err = barrier.Submit(seal)
	if err != nil || added {
		t.Fatalf("identical duplicate: added=%v err=%v", added, err)
	}

	conflict := NewSeal(1, "alpha", 1, Digest{0x01})
	if _, err := barrier.Submit(conflict); !errors.Is(err, ErrDuplicateConflict) {
		t.Fatalf("conflicting duplicate: want ErrDuplicateConflict, got %v", err)
	}
}

func TestBarrierRejectsForeignWindowAndOverBound(t *testing.T) {
	barrier, _ := NewBarrier(genesisManifest())

	oldSeal := NewSeal(9, "alpha", 1, Digest{})
	if _, err := barrier.Submit(oldSeal); !errors.Is(err, ErrForeignWindow) {
		t.Fatalf("foreign window: want ErrForeignWindow, got %v", err)
	}

	unknown := NewSeal(1, "gamma", 1, Digest{})
	if _, err := barrier.Submit(unknown); !errors.Is(err, ErrUnknownPartition) {
		t.Fatalf("unknown partition: want ErrUnknownPartition, got %v", err)
	}

	overBound := NewSeal(1, "alpha", 4, Digest{})
	if _, err := barrier.Submit(overBound); !errors.Is(err, ErrSequenceAboveBound) {
		t.Fatalf("over bound: want ErrSequenceAboveBound, got %v", err)
	}

	zeroSeq := Seal{Window: 1, Partition: "alpha", Seq: 0}
	if _, err := barrier.Submit(zeroSeq); !errors.Is(err, ErrSequenceAboveBound) {
		t.Fatalf("zero seq: want ErrSequenceAboveBound, got %v", err)
	}
}

func TestBarrierDetectsBrokenChainAndBadDigest(t *testing.T) {
	barrier, _ := NewBarrier(genesisManifest())

	badAnchor := NewSeal(1, "alpha", 1, Digest{0xAA})
	if _, err := barrier.Submit(badAnchor); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("anchor mismatch: want ErrChainBroken, got %v", err)
	}

	first := NewSeal(1, "alpha", 1, Digest{})
	if _, err := barrier.Submit(first); err != nil {
		t.Fatalf("first seal: %v", err)
	}
	broken := NewSeal(1, "alpha", 2, Digest{0xBB})
	if _, err := barrier.Submit(broken); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("broken chain: want ErrChainBroken, got %v", err)
	}

	tampered := NewSeal(1, "alpha", 2, first.Digest)
	tampered.Digest[0] ^= 0xFF
	if _, err := barrier.Submit(tampered); !errors.Is(err, ErrBadDigest) {
		t.Fatalf("tampered digest: want ErrBadDigest, got %v", err)
	}
}

func TestBarrierSubmitPrefix(t *testing.T) {
	manifest := genesisManifest()
	barrier, _ := NewBarrier(manifest)

	proof := PrefixProof{Partition: "beta", Seals: chain(1, "beta", 2, Digest{})}
	if err := barrier.SubmitPrefix(proof); err != nil {
		t.Fatalf("SubmitPrefix: %v", err)
	}

	short := PrefixProof{Partition: "alpha", Seals: chain(1, "alpha", 2, Digest{})}
	if err := barrier.SubmitPrefix(short); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("short prefix: want ErrIncomplete, got %v", err)
	}

	wrongPartition := PrefixProof{
		Partition: "alpha",
		Seals:     chain(1, "beta", 3, Digest{}),
	}
	if err := barrier.SubmitPrefix(wrongPartition); !errors.Is(err, ErrUnknownPartition) {
		t.Fatalf("mismatched proof partition: want ErrUnknownPartition, got %v", err)
	}
}

func TestBarrierZeroUpperBoundPartition(t *testing.T) {
	manifest := Manifest{
		Window: 7,
		Partitions: []PartitionSpec{
			{Partition: "idle", UpperBound: 0},
			{Partition: "work", UpperBound: 1},
		},
	}
	barrier, err := NewBarrier(manifest)
	if err != nil {
		t.Fatalf("NewBarrier: %v", err)
	}
	if missing := barrier.Missing(); missing["idle"] != 0 || missing["work"] != 1 {
		t.Fatalf("unexpected missing: %v", missing)
	}
	if _, err := barrier.Submit(NewSeal(7, "work", 1, Digest{})); err != nil {
		t.Fatalf("work submit: %v", err)
	}
	att, err := barrier.Finalize(nil)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if err := att.Verify(&manifest, nil); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestBarrierConcurrentSubmitters(t *testing.T) {
	manifest := Manifest{
		Window:     3,
		Partitions: []PartitionSpec{{Partition: "p", UpperBound: 200}},
	}
	barrier, _ := NewBarrier(manifest)
	seals := chain(3, "p", 200, Digest{})

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each worker owns disjoint sequence numbers but races with the
			// others, so a prefix gap may be transient: retry until admitted.
			for idx := worker; idx < len(seals); idx += 8 {
				seal := seals[idx]
				for {
					_, err := barrier.Submit(seal)
					if err == nil {
						break
					}
					if !errors.Is(err, ErrSequenceGap) {
						t.Errorf("concurrent submit seq %d: %v", seal.Seq, err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()

	if !barrier.Ready() {
		t.Fatalf("concurrent prefix incomplete: %v", barrier.Missing())
	}
}

func TestInvalidManifests(t *testing.T) {
	cases := []Manifest{
		{Window: 1},
		{Window: 1, Partitions: []PartitionSpec{{Partition: "", UpperBound: 1}}},
		{Window: 1, Partitions: []PartitionSpec{
			{Partition: "dup", UpperBound: 1},
			{Partition: "dup", UpperBound: 2},
		}},
	}
	for idx, manifest := range cases {
		if _, err := NewBarrier(manifest); !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("case %d: want ErrInvalidManifest, got %v", idx, err)
		}
	}

	one := Manifest{Window: 1, Partitions: []PartitionSpec{{Partition: "a", UpperBound: 1}}}
	two := Manifest{Window: 1, Partitions: []PartitionSpec{{Partition: "a", UpperBound: 2}}}
	d1, _ := one.Digest()
	d2, _ := two.Digest()
	if d1 == d2 {
		t.Fatal("distinct manifests share a digest")
	}
}

func TestSignedAttestationRoundTrip(t *testing.T) {
	manifest := genesisManifest()
	barrier, _ := NewBarrier(manifest)
	key, err := GenerateEd25519Key("sealing-key-1")
	if err != nil {
		t.Fatalf("GenerateEd25519Key: %v", err)
	}

	if err := barrier.SubmitPrefix(PrefixProof{Partition: "alpha", Seals: chain(1, "alpha", 3, Digest{})}); err != nil {
		t.Fatal(err)
	}
	if err := barrier.SubmitPrefix(PrefixProof{Partition: "beta", Seals: chain(1, "beta", 2, Digest{})}); err != nil {
		t.Fatal(err)
	}
	att, err := barrier.Finalize(key)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if !att.Signed() || att.KeyID != "sealing-key-1" {
		t.Fatalf("attestation not signed as expected: %+v", att)
	}
	verifier, err := NewEd25519Verifier(key.KeyID(), key.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if err := att.Verify(&manifest, verifier); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	tamperedSig := *att
	tamperedSig.Signature = append([]byte(nil), att.Signature...)
	tamperedSig.Signature[0] ^= 0x01
	if err := tamperedSig.Verify(&manifest, verifier); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered signature: want ErrSignature, got %v", err)
	}

	tamperedRoot := *att
	tamperedRoot.Roots[0].LastDigest[0] ^= 0x01
	if err := tamperedRoot.Verify(&manifest, verifier); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered root: want ErrSignature, got %v", err)
	}

	other, _ := GenerateEd25519Key("other-key")
	otherVerifier, _ := NewEd25519Verifier(other.KeyID(), other.PublicKey())
	if err := att.Verify(&manifest, otherVerifier); !errors.Is(err, ErrSignature) {
		t.Fatalf("foreign key: want ErrSignature, got %v", err)
	}
}
