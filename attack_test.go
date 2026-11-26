package kt

import "testing"

// TestResurrectionByHashReuseBlocked: after the 010 subtree is revoked in
// epoch 2, reusing its old hash in a later split is rejected and locks the
// client even though the migration is otherwise well-formed.
func TestResurrectionByHashReuseBlocked(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	lC := mustPrefix(t, "110001")
	t1 := referenceTree(t, 1,
		Entry{lA, leaf("pk-A", false, 0)},
		Entry{lC, leaf("pk-C", false, 0)},
	)
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	p010 := mustPrefix(t, "010")
	w.counter = 1
	rev := w.revokeMigration(2, p010, t1)
	t2 := referenceTree(t, 2, Entry{lC, leaf("pk-C", false, 0)})
	w.ingest(2, t2, &MigrationLog{Epoch: 2, Entries: []Migration{rev}})

	buried := rev.OldHash
	fakeTree := referenceTree(t, 3, Entry{lC, leaf("pk-C", false, 0)})
	auth := signAuth(t, w.keys, 3, p010, OpSplit, buried,
		nonce(0x09, 0x09), w.nextSerial())
	fakeMig := Migration{
		Epoch:   3,
		Prefix:  p010,
		Op:      OpSplit,
		OldHash: mustSubtree(t, t2, p010),
		NewHash: buried,
		Auth:    auth,
	}
	log3 := &MigrationLog{Epoch: 3, Entries: []Migration{fakeMig}}
	sr := signRoot(t, w.keys, 3, fakeTree.RootHash(), 1, log3.Hash(), w.roots[2].Body.Hash())
	err := w.verif.IngestEpoch(sr, log3, t2, fakeTree)
	expectErrIs(t, err, ErrHashReuse)
	if !w.verif.Locked() {
		t.Fatal("verifier must hard-lock after resurrection attempt")
	}
}

// TestAuthReplayBlocked rejects reusing an old authorization record.
func TestAuthReplayBlocked(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	lC := mustPrefix(t, "110001")
	t1 := referenceTree(t, 1,
		Entry{lA, leaf("pk-A", false, 0)},
		Entry{lC, leaf("pk-C", false, 0)},
	)
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	p010 := mustPrefix(t, "010")
	w.counter = 1
	rev := w.revokeMigration(2, p010, t1)
	t2 := referenceTree(t, 2, Entry{lC, leaf("pk-C", false, 0)})
	w.ingest(2, t2, &MigrationLog{Epoch: 2, Entries: []Migration{rev}})

	t3 := referenceTree(t, 3,
		Entry{lC, leaf("pk-C", false, 0)},
		Entry{lA, leaf("pk-A2", false, 1)},
	)
	replayed := w.splitMigration(3, p010, t2, t3)
	// Reuse the exact revocation SignedAuth (same nonce and serial).
	replayed.Auth = rev.Auth
	log3 := &MigrationLog{Epoch: 3, Entries: []Migration{replayed}}
	sr := signRoot(t, w.keys, 3, t3.RootHash(), 1, log3.Hash(), w.roots[2].Body.Hash())
	err := w.verif.IngestEpoch(sr, log3, t2, t3)
	if err != ErrAuthReplay && err != ErrAuthEpoch {
		t.Fatalf("expected replay/epoch error, got %v", err)
	}
}

// TestOverlappingMigrationsRejected enforces disjoint prefixes per epoch.
func TestOverlappingMigrationsRejected(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	t1 := referenceTree(t, 1, Entry{lA, leaf("pk-A", false, 0)})
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	p010 := mustPrefix(t, "010")
	p0101 := mustPrefix(t, "0101")
	w.counter = 1
	m1 := w.revokeMigration(2, p010, t1)
	m2 := w.revokeMigration(2, p0101, t1)
	t2 := referenceTree(t, 2)
	log2 := &MigrationLog{Epoch: 2, Entries: []Migration{m1, m2}}
	sr := signRoot(t, w.keys, 2, t2.RootHash(), 1, log2.Hash(), w.roots[1].Body.Hash())
	err := w.verif.IngestEpoch(sr, log2, t1, t2)
	expectErrIs(t, err, ErrMigrationPrefixes)
}

// TestCounterMonotonic ensures an incorrect counter increment locks.
func TestCounterMonotonic(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	t1 := referenceTree(t, 1, Entry{lA, leaf("pk-A", false, 0)})
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	// No revocation in epoch 2 but counter jumps anyway.
	t2 := referenceTree(t, 2, Entry{lA, leaf("pk-A", false, 0)})
	log2 := &MigrationLog{Epoch: 2}
	sr := signRoot(t, w.keys, 2, t2.RootHash(), 5, log2.Hash(), w.roots[1].Body.Hash())
	err := w.verif.IngestEpoch(sr, log2, t1, t2)
	expectErrIs(t, err, ErrCounterNotMonotonic)
}

// TestMergeTamperingRejected mutates a sibling hash and expects failure.
func TestMergeTamperingRejected(t *testing.T) {
	w := newWorld(t)
	lC := mustPrefix(t, "110001")
	t1 := referenceTree(t, 1, Entry{lC, leaf("pk-C", false, 0)})
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	p110 := mustPrefix(t, "110")
	t2 := referenceTree(t, 2, Entry{lC, leaf("pk-C", false, 0)})
	bad := w.mergeMigration(2, p110, t1, t2)
	bad.SiblingHash[0] ^= 0xFF
	log2 := &MigrationLog{Epoch: 2, Entries: []Migration{bad}}
	sr := signRoot(t, w.keys, 2, t2.RootHash(), 0, log2.Hash(), w.roots[1].Body.Hash())
	err := w.verif.IngestEpoch(sr, log2, t1, t2)
	expectErrIs(t, err, ErrMergeSibling)
}

// TestRootChainLimitsAndBreakage covers the 1..8 bounds and broken links.
func TestRootChainLimitsAndBreakage(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	t1 := referenceTree(t, 1, Entry{lA, leaf("pk-A", false, 0)})
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	if err := VerifyRootChain(nil, w.keys.dirPub); err != ErrEpochRange {
		t.Fatalf("nil chain: %v", err)
	}
	good := w.roots[1]
	if err := VerifyRootChain([]*SignedRoot{good}, w.keys.dirPub); err != nil {
		t.Fatalf("single epoch chain: %v", err)
	}
	// Break the prev-root of a fabricated second entry.
	fake := signRoot(t, w.keys, 2, good.Body.TreeRoot, 0,
		(&MigrationLog{Epoch: 2}).Hash(), Hash{})
	if err := VerifyRootChain([]*SignedRoot{good, fake}, w.keys.dirPub); err != ErrPrevRoot {
		t.Fatalf("broken prev link: %v", err)
	}
	// Wrong signer rejected.
	other := mustKeys(t)
	if err := VerifyRootChain([]*SignedRoot{good}, other.dirPub); err != ErrUntrustedSigner {
		t.Fatalf("untrusted signer: %v", err)
	}
}
