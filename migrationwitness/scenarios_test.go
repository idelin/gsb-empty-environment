package migrationwitness

import (
	"crypto/ed25519"
	"testing"
)

// crossCheck runs both the budgeted evaluator and the independent brute-force
// enumerator and asserts that the sets of distinct root identities agree.
func crossCheck(t *testing.T, env *testEnv, root NodeID, budget Budget, want Status) *Verdict {
	t.Helper()
	snap := env.log.Current()
	v, err := Evaluate(snap, root, budget)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if v.Status != want {
		t.Fatalf("status = %d, want %d (ids:%s)", v.Status, want, fmtIDs(v.DistinctIDs))
	}

	if snap.Closed && want != StatusBudgetExhausted && want != StatusPending {
		ids, _ := bruteEnumerate(snap, root)
		gotCount := len(v.DistinctIDs)
		bfCount := len(ids)
		if bfCount > 2 {
			bfCount = 2
		}
		if gotCount != bfCount {
			t.Fatalf("distinct count = %d, brute force = %d", gotCount, len(ids))
		}
		for _, id := range v.DistinctIDs {
			if _, ok := ids[id.String()]; !ok && len(ids) > 0 {
				t.Fatalf("evaluator id %s absent from brute force set", id)
			}
		}
	}
	return v
}

// TestSharedChoiceConsistency: one concat node is referenced through two
// edges; its own ambiguous edge must be chosen identically in both places.
func TestSharedChoiceConsistency(t *testing.T) {
	env := newTestEnv(t)
	env.blob("x1", "x", []byte("a"))
	env.blob("x2", "x", []byte("b"))
	env.concat("S", "s", 1, []Ref{ref("x", 1)})
	env.concat("R", "r", 2, []Ref{ref("s", 1), ref("s", 1)})
	env.close()

	v := crossCheck(t, env, "R", 0, StatusUnique)
	if len(v.Certificates) != 1 {
		t.Fatalf("certificates = %d, want 1", len(v.Certificates))
	}
	cert := v.Certificates[0]
	if err := VerifyCertificate(env.log, cert); err != nil {
		t.Fatalf("verify: %v", err)
	}
	c0 := cert.Choices[EdgeKey{Manifest: "S", Edge: 0}]
	if c0 != CandidateID("x1") && c0 != CandidateID("x2") {
		t.Fatalf("unexpected shared choice %s", c0)
	}
	if cert.Choices[EdgeKey{Manifest: "R", Edge: 0}] != "S" ||
		cert.Choices[EdgeKey{Manifest: "R", Edge: 1}] != "S" {
		t.Fatalf("root edges do not share node S: %+v", cert.Choices)
	}
}

// TestSameLengthDifferentContent: equal logical length, distinct payloads.
func TestSameLengthDifferentContent(t *testing.T) {
	env := newTestEnv(t)
	env.blob("x1", "x", []byte("aa"))
	env.blob("x2", "x", []byte("bb"))
	env.concat("R", "r", 2, []Ref{ref("x", 2)})

	v, _ := Evaluate(env.log.Current(), "R", 0)
	assertStatus(t, v.Status, StatusPending)

	env.close()
	v = crossCheck(t, env, "R", 0, StatusMultiple)
	if len(v.Certificates) != 2 {
		t.Fatalf("certificates = %d, want 2", len(v.Certificates))
	}
	for _, c := range v.Certificates {
		if err := VerifyCertificate(env.log, c); err != nil {
			t.Fatalf("verify: %v", err)
		}
	}
	if v.DistinctIDs[0].Equal(v.DistinctIDs[1]) {
		t.Fatal("expected two distinct root identities")
	}
}

// TestReceiptDisambiguatesAndWithdraw.
func TestReceiptDisambiguatesAndWithdraw(t *testing.T) {
	env := newTestEnv(t)
	env.blob("x1", "x", []byte("aa"))
	env.blob("x2", "x", []byte("bb"))
	env.concat("R", "r", 2, []Ref{ref("x", 2)})
	env.close()

	env.receipt("rc1", "R", 0, "x1")
	v, _ := Evaluate(env.log.Current(), "R", 0)
	assertStatus(t, v.Status, StatusUnique)
	if err := VerifyCertificate(env.log, v.Certificates[0]); err != nil {
		t.Fatalf("verify before withdraw: %v", err)
	}
	pinnedVersion := env.log.Version()
	oldCert := v.Certificates[0]

	env.withdraw("rc1")
	crossCheck(t, env, "R", 0, StatusMultiple)
	if err := VerifyCertificate(env.log, oldCert); err != nil {
		t.Fatalf("old certificate failed after withdraw: %v", err)
	}
	if !StillValid(env.log, oldCert) {
		t.Fatal("certificate unexpectedly invalid after withdrawal")
	}
	if env.log.Version() <= pinnedVersion {
		t.Fatal("version did not advance after withdrawal")
	}

	env.receipt("rc2", "R", 0, "x2")
	if StillValid(env.log, oldCert) {
		t.Fatal("certificate must not remain current under a conflicting receipt")
	}
	if err := VerifyCertificate(env.log, oldCert); err != nil {
		t.Fatalf("pinned certificate must still verify: %v", err)
	}
}

// TestCycleBesideLegalBranch.
func TestCycleBesideLegalBranch(t *testing.T) {
	env := newTestEnv(t)
	env.blob("leaf", "l", []byte("l"))
	env.concat("C", "c", 1, []Ref{ref("c", 1)})
	env.blob("leafC", "c", []byte("z"))
	env.close()

	v := crossCheck(t, env, "C", 0, StatusUnique)
	if err := VerifyCertificate(env.log, v.Certificates[0]); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.Certificates[0].Choices[EdgeKey{Manifest: "C", Edge: 0}] != "leafC" {
		t.Fatal("legal branch must select the acyclic leaf candidate")
	}
}

// TestOnlyCycleIsNoResolution.
func TestOnlyCycleIsNoResolution(t *testing.T) {
	env := newTestEnv(t)
	env.concat("C", "c", 1, []Ref{ref("c", 1)})
	env.close()
	crossCheck(t, env, "C", 0, StatusNoResolution)
}

// TestDedupDifferentTreeShapes.
func TestDedupDifferentTreeShapes(t *testing.T) {
	env := newTestEnv(t)
	env.blob("p1", "p", []byte("pp"))
	env.blob("p2", "p", []byte("pp"))
	env.concat("A", "a", 2, []Ref{ref("p", 2)})
	env.concat("B", "a", 2, []Ref{ref("p", 2)})
	env.close()

	va := crossCheck(t, env, "A", 0, StatusUnique)
	vb := crossCheck(t, env, "B", 0, StatusUnique)
	if !va.DistinctIDs[0].Equal(vb.DistinctIDs[0]) {
		t.Fatal("different tree shapes with same content must share identity")
	}
}

// TestAliasChain.
func TestAliasChain(t *testing.T) {
	env := newTestEnv(t)
	env.blob("real", "real", []byte("data"))
	env.alias(ref("name", 4), ref("mid", 4))
	env.alias(ref("mid", 4), ref("real", 4))
	env.concat("R", "r", 4, []Ref{ref("name", 4)})

	v, _ := Evaluate(env.log.Current(), "R", 0)
	assertStatus(t, v.Status, StatusPending)

	env.close()
	v = crossCheck(t, env, "R", 0, StatusUnique)
	if v.Certificates[0].Choices[EdgeKey{Manifest: "R", Edge: 0}] != "real" {
		t.Fatal("alias chain must resolve to the real candidate")
	}
	if err := VerifyCertificate(env.log, v.Certificates[0]); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestAliasCycleDoesNotKillLegalBranch.
func TestAliasCycleDoesNotKillLegalBranch(t *testing.T) {
	env := newTestEnv(t)
	env.blob("good", "g", []byte("g"))
	env.alias(ref("name", 1), ref("name", 1))
	env.alias(ref("name", 1), ref("g", 1))
	env.concat("R", "r", 1, []Ref{ref("name", 1)})
	env.close()

	v := crossCheck(t, env, "R", 0, StatusUnique)
	if v.Certificates[0].Choices[EdgeKey{Manifest: "R", Edge: 0}] != "good" {
		t.Fatal("legal alias path must survive beside cycle")
	}
}

// TestBudgetExhausted.
func TestBudgetExhausted(t *testing.T) {
	env := newTestEnv(t)
	payloads := [][]byte{[]byte("a1"), []byte("b2"), []byte("c3"), []byte("d4")}
	for i, p := range payloads {
		env.blob("x"+string(rune('1'+i)), "x", p)
	}
	env.concat("R", "r", 2, []Ref{ref("x", 2)})
	env.close()

	v, err := Evaluate(env.log.Current(), "R", Budget(1))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	assertStatus(t, v.Status, StatusBudgetExhausted)
	if len(v.Certificates) != 0 {
		t.Fatal("must not publish certificates when budget is exhausted")
	}
	crossCheck(t, env, "R", 0, StatusMultiple)
}

// TestTamperedCertificateRejected.
func TestTamperedCertificateRejected(t *testing.T) {
	env := newTestEnv(t)
	env.blob("x1", "x", []byte("aa"))
	env.blob("x2", "x", []byte("bb"))
	env.concat("R", "r", 2, []Ref{ref("x", 2)})
	env.close()
	v, _ := Evaluate(env.log.Current(), "R", 0)
	cert := v.Certificates[0]

	tampered := cert
	tampered.RootNewID = NewID{Algo: cert.RootNewID.Algo, Sum: append([]byte(nil), cert.RootNewID.Sum...)}
	tampered.RootNewID.Sum[0] ^= 0xff
	if err := VerifyCertificate(env.log, tampered); err == nil {
		t.Fatal("tampered identity must be rejected")
	}

	badChoice := cert
	badChoice.Choices = map[EdgeKey]CandidateID{}
	for k, c := range cert.Choices {
		badChoice.Choices[k] = c
	}
	badChoice.Choices[EdgeKey{Manifest: "R", Edge: 0}] = "x2"
	if err := VerifyCertificate(env.log, badChoice); err == nil {
		t.Fatal("certificate with swapped choice must be rejected")
	}
}

// TestLengthMismatchRejectsNode.
func TestLengthMismatchRejectsNode(t *testing.T) {
	env := newTestEnv(t)
	env.blob("x1", "x", []byte("aa"))
	env.concat("R", "r", 9, []Ref{ref("x", 2)})
	env.close()
	crossCheck(t, env, "R", 0, StatusNoResolution)
}

// TestUntrustedSignatureRejected.
func TestUntrustedSignatureRejected(t *testing.T) {
	env := newTestEnv(t)
	_, otherPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	bad := Sign(BlobStatement{Node: "x1", Ref: ref("x", 1), Payload: []byte("a")}, "unknown", otherPriv)
	if _, err := env.log.AddBlob(bad); err == nil {
		t.Fatal("untrusted signature must be rejected")
	}
}

// TestClosureStopsTableGrowth.
func TestClosureStopsTableGrowth(t *testing.T) {
	env := newTestEnv(t)
	env.close()
	_, err := env.log.AddBlob(Sign(BlobStatement{
		Node: "late", Ref: ref("x", 1), Payload: []byte("a"),
	}, env.keyID, env.priv))
	if err != ErrTableClosed {
		t.Fatalf("err = %v, want ErrTableClosed", err)
	}
}

// TestUnknownRootAndVersion.
func TestUnknownRootAndVersion(t *testing.T) {
	env := newTestEnv(t)
	env.close()
	if _, err := Evaluate(env.log.Current(), "missing", 0); err == nil {
		t.Fatal("unknown root must error")
	}
	if _, err := env.log.SnapshotAt(env.log.Version() + 1); err == nil {
		t.Fatal("out-of-range version must error")
	}
}

// TestWithdrawUnknownReceipt.
func TestWithdrawUnknownReceipt(t *testing.T) {
	env := newTestEnv(t)
	_, err := env.log.Withdraw(Sign(WithdrawalStatement{Receipt: "nope"}, env.keyID, env.priv))
	if err != ErrUnknownReceipt {
		t.Fatalf("err = %v, want ErrUnknownReceipt", err)
	}
}

// TestDanglingAliasAfterClosureIsDead: after closure an alias chain ending at
// nothing is a dead branch (no resolution), not a publishable completion.
func TestDanglingAliasAfterClosureIsDead(t *testing.T) {
	env := newTestEnv(t)
	env.alias(ref("name", 1), ref("ghost", 1))
	env.concat("R", "r", 1, []Ref{ref("name", 1)})

	// Before closure it is pending.
	v, _ := Evaluate(env.log.Current(), "R", 0)
	assertStatus(t, v.Status, StatusPending)

	env.close()
	v, _ = Evaluate(env.log.Current(), "R", 0)
	assertStatus(t, v.Status, StatusNoResolution)
}

// TestPublicCandidates.
func TestPublicCandidates(t *testing.T) {
	env := newTestEnv(t)
	env.blob("a1", "a", []byte("aa"))
	env.blob("a2", "a", []byte("bb"))
	cands := env.log.Current().Candidates(ref("a", 2))
	if len(cands) != 2 {
		t.Fatalf("candidates = %d, want 2", len(cands))
	}
	if !cands[0].NewID.Equal(ComputeBlobID([]byte("aa"))) {
		t.Fatal("blob candidate new identity mismatch")
	}
}

// TestDuplicateNodeAndReceiptRejected.
func TestDuplicateNodeAndReceiptRejected(t *testing.T) {
	env := newTestEnv(t)
	env.blob("a1", "a", []byte("aa"))
	if _, err := env.log.AddBlob(Sign(BlobStatement{
		Node: "a1", Ref: ref("a", 2), Payload: []byte("cc"),
	}, env.keyID, env.priv)); err != ErrDuplicateNode {
		t.Fatalf("err = %v, want ErrDuplicateNode", err)
	}
	env.close()
	env.receipt("r1", "R", 0, "a1")
	if _, err := env.log.AddReceipt(Sign(DisambiguationReceipt{
		ID: "r1", Manifest: "R", Edge: 0, Candidate: "a1",
	}, env.keyID, env.priv)); err != ErrDuplicateReceipt {
		t.Fatalf("err = %v, want ErrDuplicateReceipt", err)
	}
}
