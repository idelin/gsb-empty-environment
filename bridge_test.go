package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLegitimateProofReleasesOnceAndReplayIsRejected(t *testing.T) {
	env := newTestEnv(t, 2)
	escrow := &escrow{}
	escrow.deposit(t, 100)
	block := env.appendBlock(lockEvent("USDC", 100, "bob"))
	env.finalize(block.header.Height)

	verifier := env.newVerifier(DefaultPolicy(), nil, "")
	proof := env.proof(block, 0)

	release, err := verifier.Process(context.Background(), proof)
	if err != nil {
		t.Fatalf("legitimate Process: %v", err)
	}
	if release.Amount != 100 || release.AssetID != "USDC" || string(release.Recipient) != "bob" {
		t.Fatalf("unexpected release: %+v", release)
	}
	escrow.release(t, release.Amount)
	if got := escrow.released; got != 100 {
		t.Fatalf("released = %d, want 100", got)
	}

	_, err = verifier.Process(context.Background(), proof)
	if !errors.Is(err, ErrAlreadyProcessed) {
		t.Fatalf("replay error = %v, want ErrAlreadyProcessed", err)
	}
	var processed *AlreadyProcessedError
	if !errors.As(err, &processed) {
		t.Fatalf("replay error is not *AlreadyProcessedError: %T", err)
	}
	if processed.Existing.MessageKey != proof.MessageKey() {
		t.Fatalf("existing record key = %s, want %s", processed.Existing.MessageKey, proof.MessageKey())
	}
}

func TestForgedAndMislocatedProofsAreRejected(t *testing.T) {
	env := newTestEnv(t, 2)
	escrow := &escrow{}
	escrow.deposit(t, 15)
	block := env.appendBlock(
		lockEvent("USDC", 10, "bob"),
		lockEvent("USDC", 5, "carol"),
	)
	env.finalize(block.header.Height)
	verifier := env.newVerifier(DefaultPolicy(), nil, "")
	valid := env.proof(block, 0)

	tests := []struct {
		name   string
		mutate func(MessageProof) MessageProof
		want   error
	}{
		{
			name: "inflated amount",
			mutate: func(p MessageProof) MessageProof {
				p.Event.Amount = 1_000_000
				return p
			},
			want: ErrForgedEvent,
		},
		{
			name: "changed recipient",
			mutate: func(p MessageProof) MessageProof {
				p.Event.Recipient = []byte("mallory")
				return p
			},
			want: ErrForgedEvent,
		},
		{
			name: "changed nonce",
			mutate: func(p MessageProof) MessageProof {
				p.Event.Nonce++
				return p
			},
			want: ErrForgedEvent,
		},
		{
			name: "tampered event sibling",
			mutate: func(p MessageProof) MessageProof {
				p.EventProof.Siblings[0] = sumParts("attacker-sibling/v1")
				return p
			},
			want: ErrForgedEvent,
		},
		{
			name: "tampered transaction proof",
			mutate: func(p MessageProof) MessageProof {
				p.TxProof.Siblings[0] = sumParts("attacker-tx-sibling/v1")
				return p
			},
			want: ErrInvalidTxProof,
		},
		{
			name: "changed tx hash consistently",
			mutate: func(p MessageProof) MessageProof {
				replacement := sumParts("replacement-tx/v1")
				p.TxHash = replacement
				p.Event.TxHash = replacement
				return p
			},
			want: ErrInvalidTxProof,
		},
		{
			name: "changed block hash",
			mutate: func(p MessageProof) MessageProof {
				p.BlockHash = sumParts("fake-block/v1")
				return p
			},
			want: ErrBlockNotCanonical,
		},
		{
			name: "changed block height without event",
			mutate: func(p MessageProof) MessageProof {
				p.BlockHeight++
				return p
			},
			want: ErrProofBinding,
		},
		{
			name: "changed source chain consistently",
			mutate: func(p MessageProof) MessageProof {
				p.SourceChain = "other-source-chain"
				p.Event.SourceChain = "other-source-chain"
				return p
			},
			want: ErrProofBinding,
		},
		{
			name: "changed event block height without envelope",
			mutate: func(p MessageProof) MessageProof {
				p.Event.BlockHeight++
				return p
			},
			want: ErrProofBinding,
		},
		{
			name: "changed transaction index without event",
			mutate: func(p MessageProof) MessageProof {
				p.TxIndex++
				return p
			},
			want: ErrProofBinding,
		},
		{
			name: "changed event index without event",
			mutate: func(p MessageProof) MessageProof {
				p.EventIndex++
				return p
			},
			want: ErrProofBinding,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proof := test.mutate(cloneProof(valid))
			_, err := verifier.Verify(context.Background(), proof)
			if !errors.Is(err, test.want) {
				t.Fatalf("Verify error = %v, want errors.Is(%v)", err, test.want)
			}
			if _, err := verifier.Process(context.Background(), proof); err == nil {
				t.Fatal("invalid proof was processed")
			} else if errors.Is(err, ErrAlreadyProcessed) {
				t.Fatalf("invalid proof consumed the message key: %v", err)
			}
		})
	}

	release, err := verifier.Process(context.Background(), valid)
	if err != nil {
		t.Fatalf("valid proof after attack attempts: %v", err)
	}
	escrow.release(t, release.Amount)
	if escrow.released != 10 {
		t.Fatalf("released = %d, want 10", escrow.released)
	}
}

func TestCrossChainReplayIsRejected(t *testing.T) {
	env := newTestEnv(t, 2)
	escrowB := &escrow{}
	escrowC := &escrow{}
	escrowB.deposit(t, 50)
	escrowC.deposit(t, 50)
	block := env.appendBlock(lockEvent("USDC", 50, "bob"))
	env.finalize(block.header.Height)
	proof := env.proof(block, 0)

	verifierB := env.newVerifier(DefaultPolicy(), NewMemoryStore(), "dest-chain")
	release, err := verifierB.Process(context.Background(), proof)
	if err != nil {
		t.Fatalf("legitimate destination Process: %v", err)
	}
	escrowB.release(t, release.Amount)

	verifierC := env.newVerifier(DefaultPolicy(), NewMemoryStore(), "other-chain")
	if _, err := verifierC.Process(context.Background(), proof); !errors.Is(err, ErrDestinationMismatch) {
		t.Fatalf("cross-chain replay error = %v, want ErrDestinationMismatch", err)
	}
	if escrowC.released != 0 {
		t.Fatalf("other chain released %d", escrowC.released)
	}

	flipped := proof
	flipped.DestinationChain = "other-chain"
	flipped.Event.DestinationChain = "other-chain"
	if _, err := verifierC.Process(context.Background(), flipped); !errors.Is(err, ErrForgedEvent) {
		t.Fatalf("destination-flip error = %v, want ErrForgedEvent", err)
	}
	if escrowC.released != 0 {
		t.Fatalf("destination-flip released %d", escrowC.released)
	}
}

func TestCrossBridgeReplayIsRejected(t *testing.T) {
	env := newTestEnv(t, 2)
	escrowA := &escrow{}
	escrowB := &escrow{}
	escrowA.deposit(t, 25)
	escrowB.deposit(t, 25)

	event := lockEvent("USDC", 25, "bob")
	event.BridgeID = "bridge-b"
	block := env.appendBlock(event)
	env.finalize(block.header.Height)
	proof := env.proof(block, 0)

	verifierB := env.newVerifierWithBridge(DefaultPolicy(), NewMemoryStore(), "", "bridge-b")
	release, err := verifierB.Process(context.Background(), proof)
	if err != nil {
		t.Fatalf("correct bridge Process: %v", err)
	}
	escrowB.release(t, release.Amount)

	verifierA := env.newVerifierWithBridge(DefaultPolicy(), NewMemoryStore(), "", "bridge-a")
	if _, err := verifierA.Process(context.Background(), proof); !errors.Is(err, ErrBridgeMismatch) {
		t.Fatalf("cross-bridge replay error = %v, want ErrBridgeMismatch", err)
	}
	if escrowA.released != 0 {
		t.Fatalf("wrong bridge released %d", escrowA.released)
	}

	flipped := cloneProof(proof)
	flipped.Event.BridgeID = "bridge-a"
	if _, err := verifierA.Process(context.Background(), flipped); !errors.Is(err, ErrForgedEvent) {
		t.Fatalf("bridge-id flip error = %v, want ErrForgedEvent", err)
	}
	if escrowA.released != 0 {
		t.Fatalf("bridge-id flip released %d", escrowA.released)
	}
}

func TestFinalityAndDeepReorgFreezeReleases(t *testing.T) {
	env := newTestEnv(t, 2)
	escrow := &escrow{}
	escrow.deposit(t, 40)
	block := env.appendBlock(lockEvent("USDC", 40, "bob"))
	verifier := env.newVerifier(DefaultPolicy(), nil, "")
	proof := env.proof(block, 0)

	if _, err := verifier.Process(context.Background(), proof); !errors.Is(err, ErrNotFinal) {
		t.Fatalf("unfinalized error = %v, want ErrNotFinal", err)
	}
	env.appendBlock() // height 3; finalized height remains 1
	if _, err := verifier.Process(context.Background(), proof); !errors.Is(err, ErrNotFinal) {
		t.Fatalf("still-unfinalized error = %v, want ErrNotFinal", err)
	}
	env.appendBlock() // height 4; height 2 is finalized
	release, err := verifier.Process(context.Background(), proof)
	if err != nil {
		t.Fatalf("finalized Process: %v", err)
	}
	escrow.release(t, release.Amount)

	// Build a longer branch from the trusted checkpoint, replacing the
	// finalized block at height 2. It contains an unbacked 1000-unit event.
	fork2 := env.buildOn(env.genesis, []Event{lockEvent("USDC", 1000, "mallory")}, 2000)
	if err := env.submit(fork2); err != nil {
		t.Fatalf("submit fork height 2: %v", err)
	}
	parent := fork2.header
	for height := uint64(3); height <= 5; height++ {
		forkBlock := env.buildOn(parent, nil, int64(2000+height))
		err := env.submit(forkBlock)
		if height == 5 {
			if !errors.Is(err, ErrFinalityViolation) || !errors.Is(err, ErrFrozen) {
				t.Fatalf("deep reorg error = %v, want ErrFinalityViolation and ErrFrozen", err)
			}
			break
		}
		if err != nil {
			t.Fatalf("submit fork height %d: %v", height, err)
		}
		parent = forkBlock.header
	}
	if env.lightClient.Violation() == nil {
		t.Fatal("light client did not record a finality violation")
	}
	if _, err := verifier.Process(context.Background(), proof); !errors.Is(err, ErrFrozen) {
		t.Fatalf("release while frozen = %v, want ErrFrozen", err)
	}
	if _, err := verifier.Process(context.Background(), env.proof(fork2, 0)); !errors.Is(err, ErrFrozen) {
		t.Fatalf("fork release while frozen = %v, want ErrFrozen", err)
	}
	if escrow.released != 40 || escrow.released > escrow.locked {
		t.Fatalf("escrow after reorg: released=%d locked=%d", escrow.released, escrow.locked)
	}
}

func TestReorgAboveFinalizedSwitchesCanonicalChainSafely(t *testing.T) {
	env := newTestEnv(t, 2)
	escrow := &escrow{}
	escrow.deposit(t, 30)
	escrow.deposit(t, 70)

	block2 := env.appendBlock(lockEvent("USDC", 30, "bob"))
	block3 := env.appendBlock()
	oldBlock4 := env.appendBlock(lockEvent("USDC", 99, "mallory"))
	verifier := env.newVerifier(DefaultPolicy(), NewMemoryStore(), "")

	// height 2 is finalized at head 4; height 4 is not.
	release, err := verifier.Process(context.Background(), env.proof(block2, 0))
	if err != nil {
		t.Fatalf("process finalized height 2: %v", err)
	}
	escrow.release(t, release.Amount)

	// Reorg from height 3, which is at the finalized boundary. A fork that
	// starts above finalized height is permitted.
	fork4 := env.buildOn(block3.header, nil, 3000)
	if err := env.submit(fork4); err != nil {
		t.Fatalf("submit fork height 4: %v", err)
	}
	fork5 := env.buildOn(fork4.header, []Event{lockEvent("USDC", 70, "carol")}, 4000)
	if err := env.submit(fork5); err != nil {
		t.Fatalf("switch to fork at height 5: %v", err)
	}
	fork6 := env.buildOn(fork5.header, nil, 5000)
	if err := env.submit(fork6); err != nil {
		t.Fatalf("submit fork height 6: %v", err)
	}
	append7 := env.buildOn(fork6.header, nil, 6000)
	if err := env.submit(append7); err != nil {
		t.Fatalf("submit fork height 7: %v", err)
	}

	if _, err := verifier.Process(context.Background(), env.proof(oldBlock4, 0)); !errors.Is(err, ErrBlockNotCanonical) {
		t.Fatalf("orphaned event error = %v, want ErrBlockNotCanonical", err)
	}
	release, err = verifier.Process(context.Background(), env.proof(fork5, 0))
	if err != nil {
		t.Fatalf("process new canonical event: %v", err)
	}
	escrow.release(t, release.Amount)
	if _, err := verifier.Process(context.Background(), env.proof(block2, 0)); !errors.Is(err, ErrAlreadyProcessed) {
		t.Fatalf("replay after reorg error = %v, want ErrAlreadyProcessed", err)
	}
	if escrow.released != 100 || escrow.released > escrow.locked {
		t.Fatalf("escrow after reorg: released=%d locked=%d", escrow.released, escrow.locked)
	}
}

func TestConsensusSignatureChecks(t *testing.T) {
	event := lockEvent("USDC", 1, "bob")

	tests := []struct {
		name      string
		mutate    func(*testEnv, *blockData)
		want      error
		headAfter uint64
	}{
		{
			name: "valid quorum",
			mutate: func(e *testEnv, b *blockData) {
				b.signatures = e.signAll(b.header)
			},
			headAfter: 2,
		},
		{
			name: "insufficient signatures",
			mutate: func(e *testEnv, b *blockData) {
				b.signatures = b.signatures[:2]
			},
			want:      ErrInsufficientPower,
			headAfter: 1,
		},
		{
			name: "duplicate signer",
			mutate: func(e *testEnv, b *blockData) {
				b.signatures = []HeaderSignature{b.signatures[0], b.signatures[0], b.signatures[1]}
			},
			want:      ErrInvalidConsensus,
			headAfter: 1,
		},
		{
			name: "forged signature",
			mutate: func(e *testEnv, b *blockData) {
				b.signatures[0].Signature = append([]byte(nil), b.signatures[0].Signature...)
				b.signatures[0].Signature[0] ^= 0xff
			},
			want:      ErrInvalidConsensus,
			headAfter: 1,
		},
		{
			name: "unknown validator index",
			mutate: func(e *testEnv, b *blockData) {
				b.signatures[0].ValidatorIndex = uint32(len(e.validators))
			},
			want:      ErrInvalidConsensus,
			headAfter: 1,
		},
		{
			name: "unknown validator set",
			mutate: func(e *testEnv, b *blockData) {
				b.header.ValidatorsHash = sumParts("attacker-validator-set/v1")
				b.header.BlockHash = b.header.ComputeHash()
				b.signatures = e.signAll(b.header)
			},
			want:      ErrUnknownValidatorSet,
			headAfter: 1,
		},
		{
			name: "header hash does not commit to fields",
			mutate: func(e *testEnv, b *blockData) {
				b.header.TxRoot = sumParts("attacker-tx-root/v1")
			},
			want:      ErrInvalidHeader,
			headAfter: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := newTestEnv(t, 0)
			block := env.buildOn(env.head, []Event{event}, 0)
			if test.mutate != nil {
				test.mutate(env, block)
			}
			err := env.lightClient.AddHeader(block.header, block.signatures)
			if test.want != nil {
				if !errors.Is(err, test.want) {
					t.Fatalf("AddHeader error = %v, want errors.Is(%v)", err, test.want)
				}
			} else if err != nil {
				t.Fatalf("AddHeader: %v", err)
			}
			if got := env.lightClient.Head().Height; got != test.headAfter {
				t.Fatalf("head height = %d, want %d", got, test.headAfter)
			}
		})
	}
}

func TestPolicyUnfinalizedOptInStillRequiresConfirmations(t *testing.T) {
	env := newTestEnv(t, 2)
	block := env.appendBlock(lockEvent("USDC", 5, "bob"))
	proof := env.proof(block, 0)
	verifier := env.newVerifier(Policy{
		MinConfirmations: 2,
		AllowUnfinalized: true,
		AcceptedKinds:    []EventKind{EventLock},
	}, nil, "")

	if _, err := verifier.Process(context.Background(), proof); !errors.Is(err, ErrInsufficientConfirmations) {
		t.Fatalf("one confirmation error = %v, want ErrInsufficientConfirmations", err)
	}
	env.appendBlock()
	release, err := verifier.Process(context.Background(), proof)
	if err != nil {
		t.Fatalf("two-confirmation opt-in Process: %v", err)
	}
	if release.Amount != 5 {
		t.Fatalf("amount = %d", release.Amount)
	}
}

func TestMerkleProofs(t *testing.T) {
	leaves := make([]Hash, 7)
	for i := range leaves {
		leaves[i] = sumParts("test-leaf/v1", uint64Bytes(uint64(i)))
	}
	root, err := BuildMerkleRoot(leaves)
	if err != nil {
		t.Fatalf("BuildMerkleRoot: %v", err)
	}
	for index := range leaves {
		proof, err := BuildMerkleProof(leaves, uint64(index))
		if err != nil {
			t.Fatalf("BuildMerkleProof(%d): %v", index, err)
		}
		if err := VerifyMerkleProof(root, leaves[index], proof); err != nil {
			t.Fatalf("VerifyMerkleProof(%d): %v", index, err)
		}
		bad := proof
		bad.Siblings = append([]Hash(nil), proof.Siblings...)
		bad.Siblings[0] = sumParts("bad-sibling/v1")
		if err := VerifyMerkleProof(root, leaves[index], bad); !errors.Is(err, ErrInvalidMerkleProof) {
			t.Fatalf("tampered proof error = %v", err)
		}
		if err := VerifyMerkleProof(root, sumParts("bad-leaf/v1"), proof); !errors.Is(err, ErrInvalidMerkleProof) {
			t.Fatalf("wrong leaf error = %v", err)
		}
	}
	if err := VerifyMerkleProof(EmptyMerkleRoot(), sumParts("non-empty/v1"), MerkleProof{}); err == nil {
		t.Fatal("empty root accepted a proof")
	}
}

func TestProofJSONRoundTrip(t *testing.T) {
	env := newTestEnv(t, 2)
	block := env.appendBlock(lockEvent("USDC", 7, "bob"))
	env.finalize(block.header.Height)
	proof := env.proof(block, 0)

	encoded, err := json.Marshal(proof)
	if err != nil {
		t.Fatalf("marshal proof: %v", err)
	}
	var decoded MessageProof
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal proof: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded proof validation: %v", err)
	}
	verifier := env.newVerifier(DefaultPolicy(), NewMemoryStore(), "")
	release, err := verifier.Process(context.Background(), decoded)
	if err != nil {
		t.Fatalf("process decoded proof: %v", err)
	}
	if release.Amount != 7 {
		t.Fatalf("decoded release amount = %d", release.Amount)
	}
}

func TestEqualLengthForkDoesNotReplaceHead(t *testing.T) {
	env := newTestEnv(t, 2)
	env.appendBlock(lockEvent("USDC", 1, "bob"))
	env.appendBlock()
	originalHead := env.appendBlock().header

	fork2 := env.buildOn(env.genesis, nil, 2000)
	if err := env.submit(fork2); err != nil {
		t.Fatalf("submit equal fork height 2: %v", err)
	}
	fork3 := env.buildOn(fork2.header, nil, 3000)
	if err := env.submit(fork3); err != nil {
		t.Fatalf("submit equal fork height 3: %v", err)
	}
	fork4 := env.buildOn(fork3.header, nil, 4000)
	if err := env.submit(fork4); err != nil {
		t.Fatalf("submit equal fork height 4: %v", err)
	}
	if got := env.lightClient.Head(); got.BlockHash != originalHead.BlockHash {
		t.Fatalf("equal-height fork replaced head: got %s, want %s", got.BlockHash, originalHead.BlockHash)
	}
	canonical, ok := env.lightClient.CanonicalHeader(4)
	if !ok || canonical.BlockHash != originalHead.BlockHash {
		t.Fatal("canonical height 4 changed after equal-height fork")
	}
}

func TestPersistentStorePreventsReplayAfterRestart(t *testing.T) {
	env := newTestEnv(t, 2)
	block := env.appendBlock(lockEvent("USDC", 12, "bob"))
	env.finalize(block.header.Height)
	proof := env.proof(block, 0)

	path := filepath.Join(t.TempDir(), "replay.jsonl")
	store1, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	verifier1 := env.newVerifier(DefaultPolicy(), store1, "")
	if _, err := verifier1.Process(context.Background(), proof); err != nil {
		t.Fatalf("Process with file store: %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Simulate a crash during an append: a torn final line must be discarded.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open for torn write: %v", err)
	}
	if _, err := file.WriteString(`{"message_key":"torn`); err != nil {
		t.Fatalf("write torn record: %v", err)
	}
	_ = file.Close()

	store2, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer store2.Close()
	if store2.Len() != 1 {
		t.Fatalf("reloaded records = %d, want 1", store2.Len())
	}
	verifier2 := env.newVerifier(DefaultPolicy(), store2, "")
	if _, err := verifier2.Process(context.Background(), proof); !errors.Is(err, ErrAlreadyProcessed) {
		t.Fatalf("replay after restart = %v, want ErrAlreadyProcessed", err)
	}
}

func TestResetAfterFinalityViolationAllowsExplicitRecovery(t *testing.T) {
	env := newTestEnv(t, 2)
	block := env.appendBlock(lockEvent("USDC", 4, "bob"))
	env.finalize(block.header.Height)
	verifier := env.newVerifier(DefaultPolicy(), NewMemoryStore(), "")
	if _, err := verifier.Process(context.Background(), env.proof(block, 0)); err != nil {
		t.Fatalf("initial Process: %v", err)
	}

	fork2 := env.buildOn(env.genesis, nil, 2000)
	if err := env.submit(fork2); err != nil {
		t.Fatalf("submit fork height 2: %v", err)
	}
	parent := fork2.header
	for height := uint64(3); height <= 5; height++ {
		next := env.buildOn(parent, nil, int64(2000+height))
		err := env.submit(next)
		if height == 5 {
			if !errors.Is(err, ErrFrozen) {
				t.Fatalf("finality violation error = %v", err)
			}
			parent = next.header
			break
		}
		if err != nil {
			t.Fatalf("submit fork height %d: %v", height, err)
		}
		parent = next.header
	}

	if err := verifier.Reset(TrustedState{Header: parent, Validators: env.validatorSet}); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if env.lightClient.Violation() != nil {
		t.Fatal("Reset did not clear the finality violation")
	}

	recoveryEvent := env.buildOn(parent, []Event{lockEvent("USDC", 6, "carol")}, 6000)
	if err := env.submit(recoveryEvent); err != nil {
		t.Fatalf("submit recovery event: %v", err)
	}
	recoveryHead6 := env.buildOn(recoveryEvent.header, nil, 7000)
	if err := env.submit(recoveryHead6); err != nil {
		t.Fatalf("submit recovery height 6: %v", err)
	}
	recoveryHead7 := env.buildOn(recoveryHead6.header, nil, 8000)
	if err := env.submit(recoveryHead7); err != nil {
		t.Fatalf("submit recovery height 7: %v", err)
	}
	release, err := verifier.Process(context.Background(), env.proof(recoveryEvent, 0))
	if err != nil {
		t.Fatalf("Process after Reset: %v", err)
	}
	if release.Amount != 6 {
		t.Fatalf("recovery release amount = %d", release.Amount)
	}
}

func TestConcurrentProcessReleasesExactlyOnce(t *testing.T) {
	env := newTestEnv(t, 2)
	block := env.appendBlock(lockEvent("USDC", 9, "bob"))
	env.finalize(block.header.Height)
	verifier := env.newVerifier(DefaultPolicy(), NewMemoryStore(), "")
	proof := env.proof(block, 0)

	const goroutines = 32
	var successes atomic.Int32
	var processed atomic.Int32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := verifier.Process(context.Background(), proof)
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrAlreadyProcessed):
				processed.Add(1)
			default:
				t.Errorf("concurrent Process: %v", err)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("successful releases = %d, want 1", successes.Load())
	}
	if processed.Load() != goroutines-1 {
		t.Fatalf("already-processed results = %d, want %d", processed.Load(), goroutines-1)
	}
}

func TestAssetConservationAcrossAttackAttempts(t *testing.T) {
	env := newTestEnv(t, 2)
	escrow := &escrow{}
	escrow.deposit(t, 100)

	block1 := env.appendBlock(lockEvent("USDC", 40, "bob"))
	env.finalize(block1.header.Height)
	block2 := env.appendBlock(lockEvent("USDC", 60, "carol"))
	env.finalize(block2.header.Height)

	verifier := env.newVerifier(DefaultPolicy(), NewMemoryStore(), "")
	proof1 := env.proof(block1, 0)
	proof2 := env.proof(block2, 0)

	forged := cloneProof(proof2)
	forged.Event.Amount = 1_000_000
	if _, err := verifier.Process(context.Background(), forged); !errors.Is(err, ErrForgedEvent) {
		t.Fatalf("forged amount error = %v, want ErrForgedEvent", err)
	}

	for _, proof := range []MessageProof{proof1, proof2} {
		release, err := verifier.Process(context.Background(), proof)
		if err != nil {
			t.Fatalf("legitimate Process: %v", err)
		}
		escrow.release(t, release.Amount)
	}

	if _, err := verifier.Process(context.Background(), proof1); !errors.Is(err, ErrAlreadyProcessed) {
		t.Fatalf("old proof error = %v", err)
	}
	otherChain := env.newVerifier(DefaultPolicy(), NewMemoryStore(), "other-chain")
	if _, err := otherChain.Process(context.Background(), proof2); !errors.Is(err, ErrDestinationMismatch) {
		t.Fatalf("cross-chain error = %v", err)
	}

	if escrow.released != 100 {
		t.Fatalf("released = %d, want exactly 100", escrow.released)
	}
	if escrow.released > escrow.locked {
		t.Fatalf("released %d exceeds locked %d", escrow.released, escrow.locked)
	}
}

func TestRejectedProofsDoNotConsumeMessageKey(t *testing.T) {
	env := newTestEnv(t, 2)
	block := env.appendBlock(lockEvent("USDC", 3, "bob"))
	env.finalize(block.header.Height)
	verifier := env.newVerifier(DefaultPolicy(), NewMemoryStore(), "")
	proof := env.proof(block, 0)
	forged := proof
	forged.Event.Amount = 99

	if _, err := verifier.Process(context.Background(), forged); !errors.Is(err, ErrForgedEvent) {
		t.Fatalf("forged error = %v", err)
	}
	if store, ok := verifier.Store().(*MemoryStore); ok && store.Len() != 0 {
		t.Fatalf("rejected proof consumed %d keys", store.Len())
	}
	if _, err := verifier.Process(context.Background(), proof); err != nil {
		t.Fatalf("valid proof after failed attempt: %v", err)
	}
}
