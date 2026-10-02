package sharded

import (
	"bytes"
	"testing"
)

// seedAggregator builds the 4-shard system with sample accounts.
func seedAggregator() *Aggregator {
	a := NewAggregator()
	a.Shard(0).Set("alice", 100)
	a.Shard(0).Set("bob", 50)
	a.Shard(1).Set("carol", 200)
	a.Shard(2).Set("dave", 500)
	a.Shard(2).Set("erin", 300)
	a.Shard(2).Set("frank", 10)
	a.Shard(3).Set("grace", 1000)
	a.Refresh()
	return a
}

// TestAcceptanceTransferInShard2 is the acceptance scenario:
// an account in shard 2 makes a transfer; the shard-2 inclusion proof
// plus the meta position proof combine into a proof that any external
// verifier can check independently against the meta root.
func TestAcceptanceTransferInShard2(t *testing.T) {
	a := seedAggregator()
	oldMeta := a.MetaRoot()

	// Account in shard 2 initiates a transfer.
	if err := a.Shard(2).Transfer("dave", "erin", 120); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	a.Refresh()

	// Local shard root and meta root must both have moved.
	if bytes.Equal(oldMeta, a.MetaRoot()) {
		t.Fatal("meta root did not change after shard-2 transfer")
	}
	if bal, _ := a.Shard(2).Balance("dave"); bal != 380 {
		t.Fatalf("dave balance = %d, want 380", bal)
	}
	if bal, _ := a.Shard(2).Balance("erin"); bal != 420 {
		t.Fatalf("erin balance = %d, want 420", bal)
	}

	// Build the combined cross-shard proof for the sender.
	cp, err := a.ProveCrossShard(2, "dave")
	if err != nil {
		t.Fatalf("ProveCrossShard: %v", err)
	}
	if cp.Balance != 380 {
		t.Fatalf("proof carries balance %d, want 380", cp.Balance)
	}

	// External verifier: knows ONLY the meta root and the proof.
	if !VerifyCombinedProof(a.MetaRoot(), cp) {
		t.Fatal("external verification of combined proof failed")
	}

	// Stale meta root (pre-transfer) must not validate the new proof.
	if VerifyCombinedProof(oldMeta, cp) {
		t.Fatal("combined proof validated against stale meta root")
	}
}

func TestCrossShardProofAllShards(t *testing.T) {
	a := seedAggregator()
	for shardID, acct := range map[int]string{0: "alice", 1: "carol", 2: "erin", 3: "grace"} {
		cp, err := a.ProveCrossShard(shardID, acct)
		if err != nil {
			t.Fatalf("shard %d: %v", shardID, err)
		}
		if cp.MetaProof.Index != uint64(shardID) {
			t.Fatalf("shard %d: meta proof index = %d", shardID, cp.MetaProof.Index)
		}
		if !VerifyCombinedProof(a.MetaRoot(), cp) {
			t.Fatalf("shard %d: combined proof failed", shardID)
		}
	}
}

func TestCombinedProofRejectsForgery(t *testing.T) {
	a := seedAggregator()
	cp, err := a.ProveCrossShard(2, "dave")
	if err != nil {
		t.Fatal(err)
	}
	root := a.MetaRoot()

	// Inflated balance.
	forged := *cp
	forged.Balance = cp.Balance + 1
	if VerifyCombinedProof(root, &forged) {
		t.Fatal("accepted inflated balance")
	}
	// Wrong account name.
	forged = *cp
	forged.Account = "dave2"
	if VerifyCombinedProof(root, &forged) {
		t.Fatal("accepted wrong account")
	}
	// Wrong claimed shard position.
	forged = *cp
	forged.ShardID = 1
	if VerifyCombinedProof(root, &forged) {
		t.Fatal("accepted wrong shard id")
	}
	// Meta proof of a different shard.
	other, err := a.ProveCrossShard(0, "alice")
	if err != nil {
		t.Fatal(err)
	}
	forged = *cp
	forged.MetaProof = other.MetaProof
	if VerifyCombinedProof(root, &forged) {
		t.Fatal("accepted meta proof of another shard")
	}
}

func TestShardLocalRootIsolation(t *testing.T) {
	a := seedAggregator()
	r0 := append([]byte(nil), a.Shard(0).Root()...)
	r1 := append([]byte(nil), a.Shard(1).Root()...)
	r3 := append([]byte(nil), a.Shard(3).Root()...)

	if err := a.Shard(2).Transfer("erin", "frank", 5); err != nil {
		t.Fatal(err)
	}
	// Other shards' local roots are untouched by a shard-2 transfer.
	if !bytes.Equal(r0, a.Shard(0).Root()) || !bytes.Equal(r1, a.Shard(1).Root()) ||
		!bytes.Equal(r3, a.Shard(3).Root()) {
		t.Fatal("shard-2 transfer leaked into other shards' roots")
	}
}

func TestTransferErrors(t *testing.T) {
	a := seedAggregator()
	if err := a.Shard(2).Transfer("nobody", "erin", 1); err == nil {
		t.Fatal("expected error for unknown sender")
	}
	if err := a.Shard(2).Transfer("frank", "erin", 1000000); err == nil {
		t.Fatal("expected insufficient-balance error")
	}
	if _, err := a.ProveCrossShard(2, "nobody"); err == nil {
		t.Fatal("expected error proving unknown account")
	}
	if _, err := a.ProveCrossShard(7, "dave"); err == nil {
		t.Fatal("expected error for invalid shard id")
	}
}
