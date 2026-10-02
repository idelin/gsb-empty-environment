package merkle

import (
	"encoding/binary"
	"fmt"
	"testing"
)

func balance(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

func seedSystem(t *testing.T, shards, accountsPerShard int) *System {
	t.Helper()
	s := NewSystem(shards)
	for shard := 0; shard < shards; shard++ {
		for i := 0; i < accountsPerShard; i++ {
			key := []byte(fmt.Sprintf("shard%d/account%03d", shard, i))
			if err := s.Update(shard, key, balance(uint64(1000+shard*100+i))); err != nil {
				t.Fatalf("seed update shard=%d i=%d: %v", shard, i, err)
			}
		}
	}
	return s
}

func TestShardInclusionProof(t *testing.T) {
	tree := NewShardTree(0)
	tree.Put([]byte("alice"), balance(50))
	tree.Put([]byte("bob"), balance(70))
	tree.Put([]byte("carol"), balance(90))

	path, err := tree.Prove([]byte("bob"))
	if err != nil {
		t.Fatalf("Prove: %v", err)
	}
	if !VerifyShardProof(tree.Root(), []byte("bob"), balance(70), path) {
		t.Fatal("valid shard proof rejected")
	}
	if VerifyShardProof(tree.Root(), []byte("bob"), balance(71), path) {
		t.Fatal("proof with tampered value accepted")
	}
	if VerifyShardProof(tree.Root(), []byte("mallory"), balance(70), path) {
		t.Fatal("proof with tampered key accepted")
	}
	if _, err := tree.Prove([]byte("dave")); err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestDynamicUpdates(t *testing.T) {
	tree := NewShardTree(1)
	tree.Put([]byte("alice"), balance(50))
	rootBefore := tree.Root()
	pathBefore, err := tree.Prove([]byte("alice"))
	if err != nil {
		t.Fatalf("Prove: %v", err)
	}

	tree.Put([]byte("alice"), balance(80)) // update existing
	tree.Put([]byte("bob"), balance(10))   // insert new
	rootAfter := tree.Root()

	if rootBefore == rootAfter {
		t.Fatal("root did not change after mutation")
	}
	if VerifyShardProof(rootAfter, []byte("alice"), balance(50), pathBefore) {
		t.Fatal("stale proof still valid after update")
	}
	pathAfter, err := tree.Prove([]byte("alice"))
	if err != nil {
		t.Fatalf("Prove after update: %v", err)
	}
	if !VerifyShardProof(rootAfter, []byte("alice"), balance(80), pathAfter) {
		t.Fatal("fresh proof rejected after update")
	}

	if err := tree.Delete([]byte("bob")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := tree.Prove([]byte("bob")); err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound after delete, got %v", err)
	}
	if err := tree.Delete([]byte("bob")); err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound deleting twice, got %v", err)
	}
}

func TestMetaTreeTracksShardRoots(t *testing.T) {
	s := NewSystem(DefaultShardCount)
	metaBefore := s.MetaRoot()

	if err := s.Update(2, []byte("acct"), balance(1)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	metaAfter := s.MetaRoot()
	if metaBefore == metaAfter {
		t.Fatal("meta root did not change after shard update")
	}

	shard, err := s.Shard(2)
	if err != nil {
		t.Fatalf("Shard: %v", err)
	}
	committed, err := s.ShardRoot(2)
	if err != nil {
		t.Fatalf("ShardRoot: %v", err)
	}
	if committed != shard.Root() {
		t.Fatal("meta tree does not commit to latest shard root")
	}
	if _, err := s.ShardRoot(DefaultShardCount); err != ErrShardOutOfRange {
		t.Fatalf("expected ErrShardOutOfRange, got %v", err)
	}
}

// Acceptance scenario: an account in shard 2 makes a transfer; the
// shard-2 inclusion proof plus the meta-tree position proof combine
// into an aggregate proof that an external verifier checks against
// the published meta root alone.
func TestCrossShardTransferProof(t *testing.T) {
	s := seedSystem(t, DefaultShardCount, 8)

	sender := []byte("shard2/account003")
	receiver := []byte("shard2/account004")
	oldSender, _ := mustGet(t, s, 2, sender)
	oldReceiver, _ := mustGet(t, s, 2, receiver)

	// Execute the transfer inside shard 2.
	amount := uint64(125)
	newSender := balance(binary.BigEndian.Uint64(oldSender) - amount)
	newReceiver := balance(binary.BigEndian.Uint64(oldReceiver) + amount)
	if err := s.Update(2, sender, newSender); err != nil {
		t.Fatalf("debit: %v", err)
	}
	if err := s.Update(2, receiver, newReceiver); err != nil {
		t.Fatalf("credit: %v", err)
	}

	metaRoot := s.MetaRoot()

	// Shard 2 produces the local inclusion proof; the aggregator
	// attaches shard 2's position proof in the meta tree.
	proof, err := s.ProveCrossShard(2, sender)
	if err != nil {
		t.Fatalf("ProveCrossShard: %v", err)
	}
	if proof.ShardIndex != 2 {
		t.Fatalf("proof shard index = %d, want 2", proof.ShardIndex)
	}

	// External verifier: knows only the published meta root.
	if !VerifyCrossShardProof(metaRoot, proof) {
		t.Fatal("external verifier rejected valid cross-shard proof")
	}

	// The receiver's post-transfer balance is provable the same way.
	receiverProof, err := s.ProveCrossShard(2, receiver)
	if err != nil {
		t.Fatalf("ProveCrossShard receiver: %v", err)
	}
	if !VerifyCrossShardProof(metaRoot, receiverProof) {
		t.Fatal("receiver proof rejected")
	}

	// Negative cases: any tampering must fail.
	tamperedValue := *proof
	tamperedValue.Value = balance(999999)
	if VerifyCrossShardProof(metaRoot, &tamperedValue) {
		t.Fatal("tampered value accepted")
	}

	tamperedKey := *proof
	tamperedKey.Key = []byte("shard2/account005")
	if VerifyCrossShardProof(metaRoot, &tamperedKey) {
		t.Fatal("tampered key accepted")
	}

	tamperedMeta := *proof
	tamperedMeta.MetaPath = append([]ProofStep(nil), proof.MetaPath...)
	tamperedMeta.MetaPath[0].Sibling[0] ^= 0xff
	if VerifyCrossShardProof(metaRoot, &tamperedMeta) {
		t.Fatal("tampered meta path accepted")
	}

	tamperedShard := *proof
	tamperedShard.ShardPath = append([]ProofStep(nil), proof.ShardPath...)
	tamperedShard.ShardPath[0].Sibling[0] ^= 0xff
	if VerifyCrossShardProof(metaRoot, &tamperedShard) {
		t.Fatal("tampered shard path accepted")
	}

	// Proof must not verify against a stale meta root.
	if err := s.Update(2, sender, balance(0)); err != nil {
		t.Fatalf("post-proof update: %v", err)
	}
	if VerifyCrossShardProof(s.MetaRoot(), proof) {
		t.Fatal("proof accepted against a different meta root")
	}
}

func TestCrossShardProofAllShards(t *testing.T) {
	s := seedSystem(t, DefaultShardCount, 5)
	metaRoot := s.MetaRoot()
	for shard := 0; shard < DefaultShardCount; shard++ {
		key := []byte(fmt.Sprintf("shard%d/account002", shard))
		proof, err := s.ProveCrossShard(shard, key)
		if err != nil {
			t.Fatalf("ProveCrossShard shard=%d: %v", shard, err)
		}
		if !VerifyCrossShardProof(metaRoot, proof) {
			t.Fatalf("proof rejected for shard %d", shard)
		}
	}
	if _, err := s.ProveCrossShard(DefaultShardCount, []byte("x")); err != ErrShardOutOfRange {
		t.Fatalf("expected ErrShardOutOfRange, got %v", err)
	}
	if _, err := s.ProveCrossShard(0, []byte("missing")); err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestEmptySystemDeterministic(t *testing.T) {
	a := NewSystem(DefaultShardCount)
	b := NewSystem(DefaultShardCount)
	if a.MetaRoot() != b.MetaRoot() {
		t.Fatal("empty systems have different meta roots")
	}
	// Insertion order must not affect the roots.
	if err := a.Update(0, []byte("k1"), balance(1)); err != nil {
		t.Fatal(err)
	}
	if err := a.Update(0, []byte("k2"), balance(2)); err != nil {
		t.Fatal(err)
	}
	if err := b.Update(0, []byte("k2"), balance(2)); err != nil {
		t.Fatal(err)
	}
	if err := b.Update(0, []byte("k1"), balance(1)); err != nil {
		t.Fatal(err)
	}
	if a.MetaRoot() != b.MetaRoot() {
		t.Fatal("roots depend on insertion order")
	}
}

func mustGet(t *testing.T, s *System, shard int, key []byte) ([]byte, bool) {
	t.Helper()
	tree, err := s.Shard(shard)
	if err != nil {
		t.Fatalf("Shard: %v", err)
	}
	v, ok := tree.Get(key)
	if !ok {
		t.Fatalf("key %q missing in shard %d", key, shard)
	}
	return v, ok
}
