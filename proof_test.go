package taskpkg

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"testing"
)

func testKey(label string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte(label))
	return ed25519.NewKeyFromSeed(seed[:])
}

func testBoot(label string) []byte {
	sum := sha256.Sum256([]byte(label))
	return sum[:]
}

func buildChain(t *testing.T, key ed25519.PrivateKey, boot []byte, start uint64, payloads ...string) ([]Record, Seal) {
	t.Helper()
	rec := mustRecorder(t, key, boot, start)
	records := make([]Record, 0, len(payloads))
	for _, payload := range payloads {
		r, err := rec.Append([]byte(payload))
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		records = append(records, r)
	}
	seal, err := rec.Seal()
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return records, seal
}

func mustRecorder(t *testing.T, key ed25519.PrivateKey, boot []byte, start uint64) *Recorder {
	t.Helper()
	rec, err := NewRecorderAt(key, boot, start)
	if err != nil {
		t.Fatalf("NewRecorderAt: %v", err)
	}
	return rec
}

func cloneRecords(records []Record) []Record {
	out := make([]Record, len(records))
	for i, r := range records {
		out[i] = cloneRecord(r)
	}
	return out
}

func TestUnorderedShardsAndOverlapSeal(t *testing.T) {
	key := testKey("device-a")
	boot := testBoot("boot-a")
	records, seal := buildChain(t, key, boot, 0, "zero", "one", "two", "three", "four", "five")

	outOfOrder := []Record{records[5], records[1], records[4], records[0], records[3], records[2]}
	proof, err := VerifyPrefix(outOfOrder, &seal)
	if err != nil {
		t.Fatalf("VerifyPrefix out of order: %v", err)
	}
	if !proof.Sealed || !proof.Complete {
		t.Fatalf("expected complete sealed proof, got sealed=%v complete=%v", proof.Sealed, proof.Complete)
	}
	if proof.StartSeq != 0 || proof.EndSeq != 5 {
		t.Fatalf("range = [%d,%d], want [0,5]", proof.StartSeq, proof.EndSeq)
	}
	if proof.Root != seal.Root {
		t.Fatalf("proof root %s, seal root %s", proof.Root, seal.Root)
	}
	for i, r := range proof.Records {
		if r.Seq != uint64(i) {
			t.Fatalf("record %d has sequence %d", i, r.Seq)
		}
	}

	shards := [][]Record{
		{records[4], records[0], records[2]},
		{records[1], records[2], records[3]},
		{records[5], records[4]},
	}
	merged := MergeShards(shards)
	if len(merged) != len(records) {
		t.Fatalf("merged %d records, want %d after deduplication", len(merged), len(records))
	}
	shardProof, err := VerifyPrefixShards(shards, &seal)
	if err != nil {
		t.Fatalf("VerifyPrefixShards: %v", err)
	}
	if !shardProof.Sealed || shardProof.Len() != len(records) {
		t.Fatalf("shard proof sealed=%v len=%d", shardProof.Sealed, shardProof.Len())
	}
}

func TestOneBasedAndNonZeroOrigins(t *testing.T) {
	key := testKey("device-start")
	boot := testBoot("boot-start")
	records, seal := buildChain(t, key, boot, 1, "first", "second", "third")

	proof, err := VerifyPrefix([]Record{records[2], records[0], records[1]}, &seal)
	if err != nil {
		t.Fatalf("VerifyPrefix one-based: %v", err)
	}
	if proof.StartSeq != 1 || proof.EndSeq != 3 || !proof.Sealed {
		t.Fatalf("one-based proof range=[%d,%d] sealed=%v", proof.StartSeq, proof.EndSeq, proof.Sealed)
	}

	sparse, sparseSeal := buildChain(t, key, boot, 7, "seven", "eight", "nine")
	proof, err = VerifyPrefix([]Record{sparse[1], sparse[2], sparse[0]}, &sparseSeal)
	if err != nil {
		t.Fatalf("VerifyPrefix arbitrary origin: %v", err)
	}
	if proof.StartSeq != 7 || proof.EndSeq != 9 || !proof.Sealed {
		t.Fatalf("arbitrary-origin proof range=[%d,%d] sealed=%v", proof.StartSeq, proof.EndSeq, proof.Sealed)
	}
}

func TestDeletionCannotProduceSealedProof(t *testing.T) {
	key := testKey("device-delete")
	boot := testBoot("boot-delete")
	records, seal := buildChain(t, key, boot, 0, "r0", "r1", "r2", "r3", "r4")

	missingMiddle := append(cloneRecords(records[:2]), records[3:]...)
	proof, err := VerifyPrefix(missingMiddle, &seal)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("deletion error = %v, want ErrIncomplete", err)
	}
	if proof == nil || proof.Sealed || !proof.Truncated {
		t.Fatalf("deletion proof must be present, unsealed and truncated: %+v", proof)
	}
	report := Analyze(missingMiddle, &seal)
	if report.Sealed || report.Complete || len(report.Chains) != 1 {
		t.Fatalf("deletion report sealed=%v complete=%v chains=%d",
			report.Sealed, report.Complete, len(report.Chains))
	}
	if !errors.Is(report.Chains[0].SealErr, ErrIncomplete) {
		t.Fatalf("chain seal error = %v, want ErrIncomplete", report.Chains[0].SealErr)
	}

	missingGenesis := records[1:]
	proof, err = VerifyPrefix(missingGenesis, &seal)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("missing genesis error = %v, want ErrIncomplete", err)
	}
	if proof == nil || proof.Sealed {
		t.Fatalf("missing-genesis proof unexpectedly sealed: %+v", proof)
	}
}

func TestReplacementWithValiditySignedForkKeepsEveryBranch(t *testing.T) {
	key := testKey("device-fork")
	boot := testBoot("boot-fork")
	records, seal := buildChain(t, key, boot, 0, "r0", "r1", "r2", "r3", "r4", "r5")

	replacement, err := SignRecord(key, boot, 3, records[2].Link(), []byte("replacement"))
	if err != nil {
		t.Fatalf("SignRecord replacement: %v", err)
	}
	if err := replacement.Verify(); err != nil {
		t.Fatalf("replacement signature: %v", err)
	}
	if bytes.Equal(replacement.Sig, records[3].Sig) {
		t.Fatalf("fork branches should have distinct valid signatures")
	}

	withFork := MergeShards([][]Record{
		records[:4],
		{replacement},
		records[3:],
	})
	report := Analyze(withFork, &seal)
	if report.Sealed || report.Complete {
		t.Fatalf("fork must not produce a sealed proof: sealed=%v complete=%v",
			report.Sealed, report.Complete)
	}
	if len(report.Forks) != 1 {
		t.Fatalf("got %d forks, want 1", len(report.Forks))
	}
	fork := report.Forks[0]
	if fork.Seq != 3 || len(fork.Records) != 2 || len(fork.Signatures()) != 2 {
		t.Fatalf("fork seq=%d records=%d signatures=%d, want seq 3 and two branches",
			fork.Seq, len(fork.Records), len(fork.Signatures()))
	}
	for _, branch := range fork.Records {
		if err := branch.Verify(); err != nil {
			t.Fatalf("fork branch is not validly signed: %v", err)
		}
	}

	proof, err := VerifyPrefix(withFork, &seal)
	forkErr, ok := IsFork(err)
	if !ok || !errors.Is(err, ErrFork) {
		t.Fatalf("VerifyPrefix fork error = %v, want ForkError", err)
	}
	if len(forkErr.Forks) != 1 || len(forkErr.Forks[0].Records) != 2 {
		t.Fatalf("ForkError lost a valid branch: %+v", forkErr.Forks)
	}
	if proof == nil || proof.Sealed {
		t.Fatalf("fork proof must not be sealed: %+v", proof)
	}

	replacedOnly := cloneRecords(records)
	replacedOnly[3] = replacement
	proof, err = VerifyPrefix(replacedOnly, &seal)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("replacement-only error = %v, want ErrIncomplete", err)
	}
	if proof == nil || proof.Sealed {
		t.Fatalf("replacement-only proof unexpectedly sealed: %+v", proof)
	}

	tampered := cloneRecords(records)
	tampered[2].Payload = []byte("tampered without signing")
	if _, err := VerifyPrefix(tampered, &seal); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("tampered replacement error = %v, want ErrInvalidRecord", err)
	}
}

func TestCrossDeviceAndCrossBootCannotFillGap(t *testing.T) {
	keyA := testKey("device-a")
	keyB := testKey("device-b")
	bootA := testBoot("boot-a")
	bootB := testBoot("boot-b")
	aRecords, aSeal := buildChain(t, keyA, bootA, 0, "a0", "a1", "a2", "a3", "a4")

	bRecords, _ := buildChain(t, keyB, bootA, 0, "b0", "b1", "b2", "b3")
	fillFromB := bRecords[3]
	if err := fillFromB.Verify(); err != nil {
		t.Fatalf("cross-device filler is not validly signed: %v", err)
	}

	mixedDevices := append(cloneRecords(aRecords[:3]), fillFromB)
	mixedDevices = append(mixedDevices, aRecords[4:]...)
	if _, err := VerifyPrefix(mixedDevices, &aSeal); !errors.Is(err, ErrMixedContext) {
		t.Fatalf("mixed-device error = %v, want ErrMixedContext", err)
	}
	report := Analyze(mixedDevices, &aSeal)
	if report.Sealed || report.Complete {
		t.Fatalf("mixed-device report sealed=%v complete=%v", report.Sealed, report.Complete)
	}
	for _, chain := range report.Chains {
		if chain.Sealed {
			t.Fatalf("mixed-device chain %x unexpectedly sealed", chain.Device)
		}
	}

	bootFill, err := SignRecord(keyA, bootB, 3, aRecords[2].Link(), []byte("different boot"))
	if err != nil {
		t.Fatalf("SignRecord cross-boot: %v", err)
	}
	mixedBoots := append(cloneRecords(aRecords[:3]), bootFill)
	mixedBoots = append(mixedBoots, aRecords[4:]...)
	if _, err := VerifyPrefix(mixedBoots, &aSeal); !errors.Is(err, ErrMixedContext) {
		t.Fatalf("mixed-boot error = %v, want ErrMixedContext", err)
	}
	report = Analyze(mixedBoots, &aSeal)
	if report.Sealed || report.Complete {
		t.Fatalf("mixed-boot report sealed=%v complete=%v", report.Sealed, report.Complete)
	}
}

func TestSealBindsEndpointAndContext(t *testing.T) {
	key := testKey("device-seal")
	boot := testBoot("boot-seal")
	records, seal := buildChain(t, key, boot, 0, "r0", "r1", "r2", "r3")

	if _, err := VerifyPrefix(records, &seal); err != nil {
		t.Fatalf("baseline sealed proof: %v", err)
	}

	wrongSeq, err := SignSeal(key, boot, 2, seal.Root)
	if err != nil {
		t.Fatalf("SignSeal wrong seq: %v", err)
	}
	if _, err := VerifyPrefix(records, &wrongSeq); !errors.Is(err, ErrSealMismatch) {
		t.Fatalf("wrong sequence error = %v, want ErrSealMismatch", err)
	}

	wrongRoot, err := SignSeal(key, boot, seal.Seq, records[2].Link())
	if err != nil {
		t.Fatalf("SignSeal wrong root: %v", err)
	}
	if _, err := VerifyPrefix(records, &wrongRoot); !errors.Is(err, ErrSealMismatch) {
		t.Fatalf("wrong root error = %v, want ErrSealMismatch", err)
	}

	otherKey := testKey("device-other")
	otherSeal, err := SignSeal(otherKey, boot, seal.Seq, seal.Root)
	if err != nil {
		t.Fatalf("SignSeal other device: %v", err)
	}
	if _, err := VerifyPrefix(records, &otherSeal); !errors.Is(err, ErrSealContext) {
		t.Fatalf("cross-device seal error = %v, want ErrSealContext", err)
	}

	tamperedSeal := seal
	tamperedSeal.Root = records[2].Link()
	if _, err := VerifyPrefix(records, &tamperedSeal); !errors.Is(err, ErrInvalidSeal) {
		t.Fatalf("tampered seal error = %v, want ErrInvalidSeal", err)
	}
}

func TestShorterPrefixCanBeSealedDespiteLaterGap(t *testing.T) {
	key := testKey("device-prefix")
	boot := testBoot("boot-prefix")
	records, fullSeal := buildChain(t, key, boot, 0, "r0", "r1", "r2", "r3", "r4")

	prefixSeal, err := SignSeal(key, boot, 2, records[2].Link())
	if err != nil {
		t.Fatalf("SignSeal prefix: %v", err)
	}
	withLaterGap := []Record{records[0], records[1], records[2], records[4]}

	proof, err := ProvePrefix(withLaterGap, 2, &prefixSeal)
	if err != nil {
		t.Fatalf("ProvePrefix: %v", err)
	}
	if !proof.Sealed || proof.EndSeq != 2 || proof.Len() != 3 {
		t.Fatalf("prefix proof end=%d len=%d sealed=%v", proof.EndSeq, proof.Len(), proof.Sealed)
	}

	if _, err := VerifyPrefix(withLaterGap, &fullSeal); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("full-chain gap error = %v, want ErrIncomplete", err)
	}
	if _, err := VerifyChain(withLaterGap); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("VerifyChain gap error = %v, want ErrIncomplete", err)
	}
}
