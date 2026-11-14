package netsec

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidInlineWelcome(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	admission, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if !bytes.Equal(admission.GroupID, f.groupID) {
		t.Fatalf("group id = %x, want %x", admission.GroupID, f.groupID)
	}
	if admission.Epoch != f.epoch {
		t.Fatalf("epoch = %d, want %d", admission.Epoch, f.epoch)
	}
	if !bytes.Equal(admission.KeyPackageRef, f.kp.Reference) {
		t.Fatalf("key package ref = %x, want %x", admission.KeyPackageRef, f.kp.Reference)
	}
	if !bytes.Equal(admission.TreeHash, f.treeHash) {
		t.Fatalf("tree hash = %x, want %x", admission.TreeHash, f.treeHash)
	}
	if admission.Signer != 0 {
		t.Fatalf("signer = %d, want 0", admission.Signer)
	}

	state, ok := f.store.LoadGroup(f.groupID)
	if !ok {
		t.Fatal("group state not committed")
	}
	if state.Epoch != f.epoch {
		t.Fatalf("persisted epoch = %d, want %d", state.Epoch, f.epoch)
	}
	if !bytes.Equal(state.TranscriptHash, admission.TranscriptHash) {
		t.Fatal("persisted transcript differs")
	}

	if _, err := f.admitter().Admit(context.Background(), f.welcome, f.local); !errors.Is(err, ErrWelcomeAlreadyConsumed) {
		t.Fatalf("second admission error = %v, want ErrWelcomeAlreadyConsumed", err)
	}
}

func TestMultipleRecipientSecretsBindToOneCandidate(t *testing.T) {
	f := newFixture(t, fixtureOptions{pskIDs: [][]byte{[]byte("psk-1")}, psks: map[string][]byte{"psk-1": []byte("secret-psk")}})
	foreignRef := []byte("kp-other")
	foreignSecret := cloneTestGroupSecrets(f.secret)
	foreignSecret.KeyPackageRef = cloneBytes(foreignRef)
	f.welcome.Secrets = append(f.welcome.Secrets, f.crypto.seal(foreignRef, foreignSecret))

	if _, err := f.admitter().Admit(context.Background(), f.welcome, f.local); err != nil {
		t.Fatalf("Admit with a second recipient: %v", err)
	}
}

func TestMultiplePSKsAreBound(t *testing.T) {
	f := newFixture(t, fixtureOptions{
		pskIDs:         [][]byte{[]byte("psk-b"), []byte("psk-a")},
		requiredPSKIDs: [][]byte{[]byte("psk-a"), []byte("psk-b")},
		psks: map[string][]byte{
			"psk-a": []byte("secret-a"),
			"psk-b": []byte("secret-b"),
		},
	})
	admission, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if len(admission.PSKIDs) != 2 {
		t.Fatalf("psk ids = %d, want 2", len(admission.PSKIDs))
	}
}

func TestReplacedRatchetTreeRejected(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	attacker := defaultTestTree()
	attacker.Leaves[1].Credential = Credential{Type: 1, Identity: []byte("attacker"), PublicKey: []byte("attacker-pub")}
	attacker.Extensions = []Extension{{Type: 9, Data: []byte("attacker-context")}}
	f.welcome.RatchetTree = encodeTestTree(attacker)
	f.welcome.Tree = &attacker

	_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
	if !errors.Is(err, ErrTreeHashMismatch) {
		t.Fatalf("error = %v, want ErrTreeHashMismatch", err)
	}
	assertNoAdmissionState(t, f)
}

func TestOldGroupInfoRejected(t *testing.T) {
	f := newFixture(t, fixtureOptions{epoch: 4})
	f.store.SeedGroup(GroupState{
		GroupID:        cloneBytes(f.groupID),
		Epoch:          5,
		CipherSuite:    f.suite,
		TranscriptHash: []byte("current-transcript"),
	})

	_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
	if !errors.Is(err, ErrEpochRollback) {
		t.Fatalf("error = %v, want ErrEpochRollback", err)
	}
	state, _ := f.store.LoadGroup(f.groupID)
	if state.Epoch != 5 {
		t.Fatalf("persisted epoch = %d, want 5", state.Epoch)
	}
}

func TestOldTranscriptRejected(t *testing.T) {
	f := newFixture(t, fixtureOptions{epoch: 5, priorTranscript: []byte("stale-transcript")})
	f.store.SeedGroup(GroupState{
		GroupID:        cloneBytes(f.groupID),
		Epoch:          2,
		CipherSuite:    f.suite,
		TranscriptHash: []byte("current-transcript"),
	})

	_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
	if !errors.Is(err, ErrTranscriptHashMismatch) {
		t.Fatalf("error = %v, want ErrTranscriptHashMismatch", err)
	}
	state, _ := f.store.LoadGroup(f.groupID)
	if state.Epoch != 2 {
		t.Fatalf("persisted epoch = %d, want 2", state.Epoch)
	}
}

func TestSameKeyPackageAcrossGroupsRejected(t *testing.T) {
	fA := newFixture(t, fixtureOptions{groupID: []byte("group-A"), epoch: 1})
	fB := newFixture(t, fixtureOptions{groupID: []byte("group-B"), epoch: 1, keyPackageRef: fA.kp.Reference})
	shared := NewMemoryStore()
	admitterA := NewAdmitter(fA.crypto, WithStore(shared), WithClock(testNow))
	admitterB := NewAdmitter(fB.crypto, WithStore(shared), WithClock(testNow))

	if _, err := admitterA.Admit(context.Background(), fA.welcome, fA.local); err != nil {
		t.Fatalf("first group admission: %v", err)
	}
	if _, err := admitterB.Admit(context.Background(), fB.welcome, fB.local); !errors.Is(err, ErrKeyPackageConsumed) {
		t.Fatalf("cross-group error = %v, want ErrKeyPackageConsumed", err)
	}
	if _, ok := shared.LoadGroup(fB.groupID); ok {
		t.Fatal("second group state was committed")
	}
}

func TestLocalGroupBindingRejected(t *testing.T) {
	t.Run("expected group id", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{groupID: []byte("group-A")})
		f.local.ExpectedGroupID = []byte("group-B")
		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrGroupMismatch) {
			t.Fatalf("error = %v, want ErrGroupMismatch", err)
		}
		assertNoAdmissionState(t, f)
	})

	t.Run("key package target group", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{groupID: []byte("group-A")})
		f.kp.GroupID = []byte("group-B")
		f.local.KeyPackage = f.kp
		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrGroupMismatch) {
			t.Fatalf("error = %v, want ErrGroupMismatch", err)
		}
		assertNoAdmissionState(t, f)
	})
}

func TestMissingPSKRejected(t *testing.T) {
	t.Run("secret references missing local psk", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{pskIDs: [][]byte{[]byte("psk-1")}})
		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrMissingPSK) {
			t.Fatalf("error = %v, want ErrMissingPSK", err)
		}
		assertNoAdmissionState(t, f)
	})

	t.Run("group info requires psk absent from secret", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{requiredPSKIDs: [][]byte{[]byte("psk-required")}})
		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrMissingPSK) {
			t.Fatalf("error = %v, want ErrMissingPSK", err)
		}
		assertNoAdmissionState(t, f)
	})
}

func TestValidExternalTreeJoin(t *testing.T) {
	f := newFixture(t, fixtureOptions{external: true, binding: []byte("welcome-nonce")})
	var request TreeFetchRequest
	fetcher := RatchetTreeFetcherFunc(func(ctx context.Context, req TreeFetchRequest) (TreeMaterial, error) {
		request = req
		tree, err := f.crypto.DecodeRatchetTree(f.treeBytes)
		if err != nil {
			return TreeMaterial{}, err
		}
		return TreeMaterial{
			Bytes:   cloneBytes(f.treeBytes),
			Digest:  cloneBytes(f.treeHash),
			Binding: cloneBytes(req.Binding),
			Tree:    &tree,
		}, nil
	})

	admission, err := f.admitter(WithTreeFetcher(fetcher)).Admit(context.Background(), f.welcome, f.local)
	if err != nil {
		t.Fatalf("Admit external tree: %v", err)
	}
	if !bytes.Equal(request.TreeHash, f.treeHash) {
		t.Fatalf("fetch tree hash = %x, want %x", request.TreeHash, f.treeHash)
	}
	if !bytes.Equal(request.WelcomeID, admission.WelcomeID) {
		t.Fatal("fetch request is not bound to the admitted welcome id")
	}
	if !bytes.Equal(request.Binding, []byte("welcome-nonce")) {
		t.Fatalf("fetch binding = %q", request.Binding)
	}
	state, ok := f.store.LoadGroup(f.groupID)
	if !ok || state.Epoch != f.epoch {
		t.Fatalf("external join state = %+v, ok=%t", state, ok)
	}
}

func TestExternalTreeResponseMustBeWelcomeBound(t *testing.T) {
	f := newFixture(t, fixtureOptions{external: true, binding: []byte("welcome-nonce")})
	attacker := defaultTestTree()
	attacker.Leaves[1].Credential.PublicKey = []byte("attacker-pub")
	attackerBytes := encodeTestTree(attacker)

	cases := []struct {
		name     string
		material TreeMaterial
		want     error
	}{
		{
			name: "wrong binding",
			material: TreeMaterial{
				Bytes:   cloneBytes(f.treeBytes),
				Digest:  cloneBytes(f.treeHash),
				Binding: []byte("another-welcome"),
			},
			want: ErrTreeBinding,
		},
		{
			name: "wrong digest",
			material: TreeMaterial{
				Bytes:   cloneBytes(f.treeBytes),
				Digest:  []byte("another-tree"),
				Binding: []byte("welcome-nonce"),
			},
			want: ErrTreeHashMismatch,
		},
		{
			name: "cached bytes replaced",
			material: TreeMaterial{
				Bytes:   attackerBytes,
				Digest:  cloneBytes(f.treeHash),
				Binding: []byte("welcome-nonce"),
			},
			want: ErrTreeHashMismatch,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fetcher := RatchetTreeFetcherFunc(func(ctx context.Context, req TreeFetchRequest) (TreeMaterial, error) {
				material := tc.material
				return material, nil
			})
			_, err := f.admitter(WithTreeFetcher(fetcher)).Admit(context.Background(), f.welcome, f.local)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			assertNoAdmissionState(t, f)
		})
	}

	fetcher := RatchetTreeFetcherFunc(func(ctx context.Context, req TreeFetchRequest) (TreeMaterial, error) {
		return TreeMaterial{
			Bytes:   cloneBytes(f.treeBytes),
			Digest:  cloneBytes(f.treeHash),
			Binding: cloneBytes(req.Binding),
		}, nil
	})
	if _, err := f.admitter(WithTreeFetcher(fetcher)).Admit(context.Background(), f.welcome, f.local); err != nil {
		t.Fatalf("valid fetch after failures: %v", err)
	}
}

func TestExternalTreeDecodedFormMustMatchBytes(t *testing.T) {
	f := newFixture(t, fixtureOptions{external: true, binding: []byte("welcome-nonce")})
	predecoded := cloneTree(f.tree)
	predecoded.Leaves[1].Credential.PublicKey = []byte("attacker-pub")
	fetcher := RatchetTreeFetcherFunc(func(ctx context.Context, req TreeFetchRequest) (TreeMaterial, error) {
		return TreeMaterial{
			Bytes:   cloneBytes(f.treeBytes),
			Digest:  cloneBytes(f.treeHash),
			Binding: cloneBytes(req.Binding),
			Tree:    &predecoded,
		}, nil
	})

	_, err := f.admitter(WithTreeFetcher(fetcher)).Admit(context.Background(), f.welcome, f.local)
	if !errors.Is(err, ErrTreeDecode) {
		t.Fatalf("error = %v, want ErrTreeDecode", err)
	}
	assertNoAdmissionState(t, f)
}

func TestExternalTreeCannotCrossWireWelcomes(t *testing.T) {
	fA := newFixture(t, fixtureOptions{groupID: []byte("group-A"), external: true, binding: []byte("nonce-A")})
	fB := newFixture(t, fixtureOptions{groupID: []byte("group-B"), epoch: 2, external: true, binding: []byte("nonce-B")})
	treeA := defaultTestTree()
	treeA.Extensions[0].Data = []byte("context-A")
	fA.setTree(treeA)
	treeB := defaultTestTree()
	treeB.Extensions[0].Data = []byte("context-B")
	fB.setTree(treeB)

	// A buggy cache returns the other Welcome's tree bytes while echoing the
	// requested digest. Neither admission may accept mixed state.
	fetcher := RatchetTreeFetcherFunc(func(ctx context.Context, req TreeFetchRequest) (TreeMaterial, error) {
		if bytes.Equal(req.GroupID, fA.groupID) {
			return TreeMaterial{Bytes: fB.treeBytes, Digest: req.TreeHash, Binding: req.Binding}, nil
		}
		return TreeMaterial{Bytes: fA.treeBytes, Digest: req.TreeHash, Binding: req.Binding}, nil
	})

	admitterA := fA.admitter(WithTreeFetcher(fetcher))
	admitterB := fB.admitter(WithTreeFetcher(fetcher))
	if _, err := admitterA.Admit(context.Background(), fA.welcome, fA.local); !errors.Is(err, ErrTreeHashMismatch) {
		t.Fatalf("A error = %v, want ErrTreeHashMismatch", err)
	}
	if _, err := admitterB.Admit(context.Background(), fB.welcome, fB.local); !errors.Is(err, ErrTreeHashMismatch) {
		t.Fatalf("B error = %v, want ErrTreeHashMismatch", err)
	}
	if _, ok := fA.store.LoadGroup(fA.groupID); ok {
		t.Fatal("group A accepted a cross-wired tree")
	}
	if _, ok := fB.store.LoadGroup(fB.groupID); ok {
		t.Fatal("group B accepted a cross-wired tree")
	}
}

func TestConcurrentSameWelcomeConsumedOnce(t *testing.T) {
	f := newFixture(t, fixtureOptions{external: true, binding: []byte("concurrent-nonce")})
	var fetchCount int32
	fetcher := RatchetTreeFetcherFunc(func(ctx context.Context, req TreeFetchRequest) (TreeMaterial, error) {
		atomic.AddInt32(&fetchCount, 1)
		time.Sleep(5 * time.Millisecond)
		return TreeMaterial{
			Bytes:   cloneBytes(f.treeBytes),
			Digest:  cloneBytes(f.treeHash),
			Binding: cloneBytes(req.Binding),
		}, nil
	})
	admitter := f.admitter(WithTreeFetcher(fetcher))

	const workers = 24
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, errs[index] = admitter.Admit(context.Background(), f.welcome, f.local)
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrWelcomeAlreadyConsumed) {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want 1", successes)
	}
	if got := atomic.LoadInt32(&fetchCount); got != 1 {
		t.Fatalf("tree fetch count = %d, want 1", got)
	}
	state, ok := f.store.LoadGroup(f.groupID)
	if !ok || state.Epoch != f.epoch {
		t.Fatalf("state = %+v, ok=%t", state, ok)
	}
}

func TestConcurrentWelcomesCannotShareKeyPackage(t *testing.T) {
	fA := newFixture(t, fixtureOptions{groupID: []byte("group-A"), epoch: 1})
	fB := newFixture(t, fixtureOptions{groupID: []byte("group-B"), epoch: 1, keyPackageRef: fA.kp.Reference})
	shared := NewMemoryStore()
	admitterA := NewAdmitter(fA.crypto, WithStore(shared), WithClock(testNow))
	admitterB := NewAdmitter(fB.crypto, WithStore(shared), WithClock(testNow))

	start := make(chan struct{})
	var wg sync.WaitGroup
	var errA, errB error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, errA = admitterA.Admit(context.Background(), fA.welcome, fA.local)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, errB = admitterB.Admit(context.Background(), fB.welcome, fB.local)
	}()
	close(start)
	wg.Wait()

	successes := 0
	consumed := 0
	for _, err := range []error{errA, errB} {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrKeyPackageConsumed):
			consumed++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || consumed != 1 {
		t.Fatalf("successes=%d consumed=%d, want 1/1", successes, consumed)
	}
}

func TestFailureDoesNotAdvancePersistentEpoch(t *testing.T) {
	f := newFixture(t, fixtureOptions{epoch: 5, priorTranscript: []byte("prior-4")})
	f.store.SeedGroup(GroupState{
		GroupID:        cloneBytes(f.groupID),
		Epoch:          4,
		CipherSuite:    f.suite,
		TranscriptHash: []byte("prior-4"),
	})
	admitter := f.admitter()

	goodTree := cloneBytes(f.welcome.RatchetTree)
	goodDecoded := cloneTree(*f.welcome.Tree)
	attacker := defaultTestTree()
	attacker.Leaves[1].Credential.PublicKey = []byte("attacker-pub")
	f.welcome.RatchetTree = encodeTestTree(attacker)
	f.welcome.Tree = &attacker

	if _, err := admitter.Admit(context.Background(), f.welcome, f.local); !errors.Is(err, ErrTreeHashMismatch) {
		t.Fatalf("tampered error = %v, want ErrTreeHashMismatch", err)
	}
	state, _ := f.store.LoadGroup(f.groupID)
	if state.Epoch != 4 {
		t.Fatalf("persisted epoch advanced to %d after failure", state.Epoch)
	}
	if !bytes.Equal(state.TranscriptHash, []byte("prior-4")) {
		t.Fatalf("persisted transcript changed after failure: %x", state.TranscriptHash)
	}
	if f.store.KeyPackageConsumed(f.kp.Reference) {
		t.Fatal("key package consumed after failed admission")
	}

	f.welcome.RatchetTree = goodTree
	f.welcome.Tree = &goodDecoded
	if _, err := admitter.Admit(context.Background(), f.welcome, f.local); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	state, _ = f.store.LoadGroup(f.groupID)
	if state.Epoch != 5 {
		t.Fatalf("persisted epoch = %d, want 5", state.Epoch)
	}
}

func TestGroupInfoSignerAuthorization(t *testing.T) {
	t.Run("signer outside tree", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{signer: 9})
		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrSignerNotMember) {
			t.Fatalf("error = %v, want ErrSignerNotMember", err)
		}
	})

	t.Run("signer leaf has no signature key", func(t *testing.T) {
		tree := defaultTestTree()
		tree.Leaves[0].SignatureKey = nil
		f := newFixture(t, fixtureOptions{tree: &tree, signer: 0, leafIndex: 1})
		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrSignerNotAuthorized) {
			t.Fatalf("error = %v, want ErrSignerNotAuthorized", err)
		}
	})

	t.Run("signer credential mismatch", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{})
		wrong := Credential{Type: 1, Identity: []byte("other"), PublicKey: []byte("other-pub")}
		f.welcome.GroupInfo.SignerCredential = &wrong
		f.refresh()
		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrSignerNotAuthorized) {
			t.Fatalf("error = %v, want ErrSignerNotAuthorized", err)
		}
	})

	t.Run("bad signature", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{})
		f.welcome.GroupInfo.Signature = []byte("bad-signature")
		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrGroupInfoSignature) {
			t.Fatalf("error = %v, want ErrGroupInfoSignature", err)
		}
	})

	t.Run("application policy", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{})
		policy := GroupInfoSignerPolicyFunc(func(gi GroupInfo, tree RatchetTree, signer LeafNode) error {
			return errors.New("signer is not an administrator")
		})
		_, err := f.admitter(WithSignerPolicy(policy)).Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrSignerNotAuthorized) {
			t.Fatalf("error = %v, want ErrSignerNotAuthorized", err)
		}
	})
}

func TestTreeExtensionsMustMatchGroupInfo(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	f.welcome.GroupInfo.Extensions = []Extension{{Type: 2, Data: []byte("different")}}
	f.refresh()

	_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
	if !errors.Is(err, ErrExtensionsMismatch) {
		t.Fatalf("error = %v, want ErrExtensionsMismatch", err)
	}
	assertNoAdmissionState(t, f)
}

func TestKeyPackagePreconditions(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{notAfter: testNow().Add(-time.Second)})
		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrKeyPackageExpired) {
			t.Fatalf("error = %v, want ErrKeyPackageExpired", err)
		}
		assertNoAdmissionState(t, f)
	})

	t.Run("credential does not match private key", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{})
		f.local.PrivateKey = []byte("private:not-the-key")
		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrKeyPackageCredential) {
			t.Fatalf("error = %v, want ErrKeyPackageCredential", err)
		}
		assertNoAdmissionState(t, f)
	})

	t.Run("leaf credential differs from key package", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{})
		tree := cloneTree(f.tree)
		tree.Leaves[1].Credential = Credential{Type: 1, Identity: []byte("attacker"), PublicKey: []byte("attacker-pub")}
		f.setTree(tree)
		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrKeyPackageLeafMismatch) {
			t.Fatalf("error = %v, want ErrKeyPackageLeafMismatch", err)
		}
	})
}

func TestMixedEncryptedSecretsRejected(t *testing.T) {
	t.Run("different group in another decryptable entry", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{})
		foreignRef := []byte("kp-foreign")
		foreign := cloneTestGroupSecrets(f.secret)
		foreign.KeyPackageRef = cloneBytes(foreignRef)
		foreign.GroupID = []byte("group-B")
		f.welcome.Secrets = append(f.welcome.Secrets, f.crypto.seal(foreignRef, foreign))

		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrMixedCandidate) {
			t.Fatalf("error = %v, want ErrMixedCandidate", err)
		}
	})

	t.Run("different joiner secret in another entry", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{})
		foreignRef := []byte("kp-foreign")
		foreign := cloneTestGroupSecrets(f.secret)
		foreign.KeyPackageRef = cloneBytes(foreignRef)
		foreign.JoinerSecret = []byte("different-joiner-secret")
		f.welcome.Secrets = append(f.welcome.Secrets, f.crypto.seal(foreignRef, foreign))

		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrMixedCandidate) {
			t.Fatalf("error = %v, want ErrMixedCandidate", err)
		}
	})

	t.Run("duplicate key package reference", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{})
		f.welcome.Secrets = append(f.welcome.Secrets, f.crypto.seal(f.kp.Reference, f.secret))

		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrAmbiguousCandidate) {
			t.Fatalf("error = %v, want ErrAmbiguousCandidate", err)
		}
	})
}

func TestRatchetTreeSourceValidity(t *testing.T) {
	t.Run("both sources", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{})
		f.welcome.TreeReference = &RatchetTreeReference{
			GroupID:  cloneBytes(f.groupID),
			Epoch:    f.epoch,
			TreeHash: cloneBytes(f.treeHash),
			Binding:  []byte("binding"),
		}
		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrTreeSource) {
			t.Fatalf("error = %v, want ErrTreeSource", err)
		}
	})

	t.Run("no source", func(t *testing.T) {
		f := newFixture(t, fixtureOptions{})
		f.welcome.RatchetTree = nil
		f.welcome.Tree = nil
		_, err := f.admitter().Admit(context.Background(), f.welcome, f.local)
		if !errors.Is(err, ErrTreeSource) {
			t.Fatalf("error = %v, want ErrTreeSource", err)
		}
	})
}

func assertNoAdmissionState(t *testing.T, f *fixture) {
	t.Helper()
	if _, ok := f.store.LoadGroup(f.groupID); ok {
		t.Fatal("unexpected group state committed")
	}
	if f.store.KeyPackageConsumed(f.kp.Reference) {
		t.Fatal("key package unexpectedly consumed")
	}
	welcomeID := f.crypto.Digest("MLS-Welcome-ID", canonicalWelcome(f.welcome))
	if f.store.WelcomeConsumed(welcomeID) {
		t.Fatal("welcome unexpectedly consumed")
	}
}
