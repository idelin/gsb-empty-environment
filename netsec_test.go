package netsec

import (
	"context"
	"errors"
	"testing"
	"time"
)

var (
	testBase   = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	testKey    = []byte("pseudo-hmac-key-for-witness-tests")
	testSigner = NewPseudoSigner(testKey)
	testVer    = NewPseudoVerifier(testKey)
)

func testOpDigest(seed string) Digest {
	return SumDigest([]byte("sensitive-operation:" + seed))
}

// signedProof builds one proof and computes its real digest from the signed
// canonical content. ParentDigest is expected to already be set.
func signedProof(t *testing.T, step, session string, op Digest, parent Digest, start time.Time, seconds int) (Proof, Digest) {
	t.Helper()
	proof := Proof{
		StepID:       step,
		OpDigest:     op,
		SessionID:    session,
		ParentDigest: parent,
		NotBefore:    start,
		NotAfter:     start.Add(time.Duration(seconds) * time.Second),
		SignerID:     "signer-" + step,
	}
	if err := testSigner.Sign(&proof); err != nil {
		t.Fatalf("sign %s: %v", step, err)
	}
	digest, err := proof.Digest()
	if err != nil {
		t.Fatalf("digest %s: %v", step, err)
	}
	return proof, digest
}

func mustDigest(t *testing.T, p Proof) Digest {
	t.Helper()
	digest, err := p.Digest()
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	return digest
}

// linearChain produces device -> user proofs sharing op, session and chain.
func linearChain(t *testing.T, op Digest, session string) []Proof {
	t.Helper()
	device, deviceDigest := signedProof(t, "device", session, op, ZeroDigest, testBase, 600)
	user, _ := signedProof(t, "user", session, op, deviceDigest, testBase.Add(time.Minute), 600)
	return []Proof{device, user}
}

func linearGraph() StepGraph {
	return StepGraph{
		Steps: []string{"device", "user"},
		Edges: map[string][]string{"user": {"device"}},
	}
}

func parallelGraph() StepGraph {
	return StepGraph{
		Steps: []string{"device", "bio", "pin", "user"},
		Edges: map[string][]string{
			"bio":  {"device"},
			"pin":  {"device"},
			"user": {"bio", "pin"},
		},
	}
}

func TestProofDigestIsRealAndStable(t *testing.T) {
	op := testOpDigest("op-1")
	proof, digest := signedProof(t, "device", "sess-1", op, ZeroDigest, testBase, 600)

	if digest.IsZero() {
		t.Fatal("digest must not be zero")
	}
	again := mustDigest(t, proof)
	if digest != again {
		t.Fatal("digest must be deterministic over canonical content")
	}

	tampered := proof
	tampered.SessionID = "sess-2"
	if mustDigest(t, tampered) == digest {
		t.Fatal("changing a bound field must change the digest")
	}

	resigned := proof
	resigned.Sig = append([]byte(nil), proof.Sig...)
	resigned.Sig[0] ^= 0xFF
	if mustDigest(t, resigned) != digest {
		t.Fatal("signature must not influence the content digest")
	}
}

func TestComplete_HappyPath(t *testing.T) {
	op := testOpDigest("op-1")
	proofs := linearChain(t, op, "sess-1")
	v := NewValidator(testVer, NewMemoryReplayCache())

	completion, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: proofs}, testBase.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if completion.OpDigest != op || completion.SessionID != "sess-1" {
		t.Fatal("completion must bind the common operation and session")
	}
	wantSteps := []string{"device", "user"}
	for i, step := range wantSteps {
		if completion.CoveredSteps[i] != step {
			t.Fatalf("chain order[%d] = %s, want %s", i, completion.CoveredSteps[i], step)
		}
	}
	if !completion.EffectiveFrom.Equal(testBase.Add(time.Minute)) {
		t.Fatalf("intersection start = %v", completion.EffectiveFrom)
	}
	if !completion.EffectiveUntil.Equal(testBase.Add(10 * time.Minute)) {
		t.Fatalf("intersection end = %v", completion.EffectiveUntil)
	}
}

func TestComplete_ParallelStepsLinearizedInOneChain(t *testing.T) {
	op := testOpDigest("op-parallel")
	session := "sess-parallel"

	device, dd := signedProof(t, "device", session, op, ZeroDigest, testBase, 1200)
	// bio and pin are parallel; this linearization orders bio then pin.
	bio, bd := signedProof(t, "bio", session, op, dd, testBase.Add(time.Minute), 1200)
	pin, pd := signedProof(t, "pin", session, op, bd, testBase.Add(2*time.Minute), 1200)
	user, _ := signedProof(t, "user", session, op, pd, testBase.Add(3*time.Minute), 1200)

	v := NewValidator(testVer, NewMemoryReplayCache())
	completion, err := v.Complete(context.Background(), parallelGraph(),
		Bundle{Proofs: []Proof{pin, device, user, bio}}, testBase.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("parallel completion: %v", err)
	}
	want := []string{"device", "bio", "pin", "user"}
	for i, step := range want {
		if completion.CoveredSteps[i] != step {
			t.Fatalf("order[%d] = %s, want %s", i, completion.CoveredSteps[i], step)
		}
	}
}

func TestComplete_ParallelAlternativeLinearization(t *testing.T) {
	op := testOpDigest("op-parallel-2")
	session := "sess-parallel-2"

	device, dd := signedProof(t, "device", session, op, ZeroDigest, testBase, 1200)
	pin, pd := signedProof(t, "pin", session, op, dd, testBase.Add(time.Minute), 1200)
	bio, bd := signedProof(t, "bio", session, op, pd, testBase.Add(2*time.Minute), 1200)
	user, _ := signedProof(t, "user", session, op, bd, testBase.Add(3*time.Minute), 1200)

	v := NewValidator(testVer, NewMemoryReplayCache())
	if _, err := v.Complete(context.Background(), parallelGraph(),
		Bundle{Proofs: []Proof{device, pin, bio, user}}, testBase.Add(5*time.Minute)); err != nil {
		t.Fatalf("pin-before-bio linearization should satisfy the DAG: %v", err)
	}
}

func TestComplete_MissingStep(t *testing.T) {
	op := testOpDigest("op-missing")
	device, _ := signedProof(t, "device", "sess-m", op, ZeroDigest, testBase, 600)
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: []Proof{device}}, time.Time{})
	if !errors.Is(err, ErrMissingStep) {
		t.Fatalf("err = %v, want ErrMissingStep", err)
	}
}

func TestComplete_UnknownStep(t *testing.T) {
	op := testOpDigest("op-unknown")
	proofs := linearChain(t, op, "sess-u")
	extra, _ := signedProof(t, "payment", "sess-u", op,
		mustDigest(t, proofs[1]), testBase.Add(2*time.Minute), 600)
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: append(proofs, extra)}, time.Time{})
	if !errors.Is(err, ErrUnknownStep) {
		t.Fatalf("err = %v, want ErrUnknownStep", err)
	}
}

func TestComplete_DuplicateStep(t *testing.T) {
	op := testOpDigest("op-dup")
	proofs := linearChain(t, op, "sess-d")
	dup, _ := signedProof(t, "device", "sess-d", op,
		mustDigest(t, proofs[1]), testBase.Add(3*time.Minute), 600)
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: append(proofs, dup)}, time.Time{})
	if !errors.Is(err, ErrDuplicateStep) {
		t.Fatalf("err = %v, want ErrDuplicateStep", err)
	}
}

func TestComplete_ReplayAcrossBundles(t *testing.T) {
	op := testOpDigest("op-replay")
	proofs := linearChain(t, op, "sess-r")
	cache := NewMemoryReplayCache()
	v := NewValidator(testVer, cache)

	if _, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: proofs}, time.Time{}); err != nil {
		t.Fatalf("first completion: %v", err)
	}
	// The same signed events are presented again as a new completion.
	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: proofs}, time.Time{})
	if !errors.Is(err, ErrReplayedProof) {
		t.Fatalf("err = %v, want ErrReplayedProof", err)
	}

	// An unrelated, valid bundle must still succeed: failed/replayed checks
	// never poison the cache with digests that were not consumed.
	fresh := linearChain(t, testOpDigest("op-fresh"), "sess-fresh")
	if _, err := NewValidator(testVer, cache).Complete(context.Background(),
		linearGraph(), Bundle{Proofs: fresh}, time.Time{}); err != nil {
		t.Fatalf("unrelated bundle after replay: %v", err)
	}
}

func TestComplete_DuplicateDigestInsideBundle(t *testing.T) {
	op := testOpDigest("op-inner-dup")
	proofs := linearChain(t, op, "sess-i")
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: []Proof{proofs[0], proofs[1], proofs[0]}}, time.Time{})
	if !errors.Is(err, ErrReplayedProof) {
		t.Fatalf("err = %v, want ErrReplayedProof", err)
	}
}

func TestComplete_WithoutReplayCacheAllowsReuse(t *testing.T) {
	op := testOpDigest("op-no-cache")
	proofs := linearChain(t, op, "sess-nc")
	v := NewValidator(testVer, nil)

	for i := 0; i < 2; i++ {
		if _, err := v.Complete(context.Background(), linearGraph(),
			Bundle{Proofs: proofs}, time.Time{}); err != nil {
			t.Fatalf("completion %d without replay cache: %v", i+1, err)
		}
	}
}

func TestComplete_BranchMerge_ForkedParent(t *testing.T) {
	op := testOpDigest("op-fork")
	session := "sess-fork"

	device, dd := signedProof(t, "device", session, op, ZeroDigest, testBase, 600)
	userA, _ := signedProof(t, "user", session, op, dd, testBase.Add(time.Minute), 600)
	// A second user proof also claims device as parent: two siblings cannot
	// be merged into one chain.
	userB, _ := signedProof(t, "user", session, op, dd, testBase.Add(2*time.Minute), 600)
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: []Proof{device, userA, userB}}, time.Time{})
	if !errors.Is(err, ErrForkedChain) {
		t.Fatalf("err = %v, want ErrForkedChain", err)
	}
}

func TestComplete_BranchMerge_TwoRoots(t *testing.T) {
	op := testOpDigest("op-two-roots")
	session := "sess-roots"

	device, _ := signedProof(t, "device", session, op, ZeroDigest, testBase, 600)
	// A separately rooted user event cannot be stitched onto the device chain.
	user, _ := signedProof(t, "user", session, op, ZeroDigest, testBase.Add(time.Minute), 600)
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: []Proof{device, user}}, time.Time{})
	if !errors.Is(err, ErrMultipleRoots) {
		t.Fatalf("err = %v, want ErrMultipleRoots", err)
	}
}

func TestComplete_BranchMerge_DanglingParent(t *testing.T) {
	op := testOpDigest("op-dangle")
	session := "sess-dangle"

	device, _ := signedProof(t, "device", session, op, ZeroDigest, testBase, 600)
	_, foreignDigest := signedProof(t, "device", "other-session", op, ZeroDigest, testBase, 600)
	// user points at a proof from a different ancestry that is not included.
	user, _ := signedProof(t, "user", session, op, foreignDigest, testBase.Add(time.Minute), 600)
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: []Proof{device, user}}, time.Time{})
	if !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("err = %v, want ErrBrokenChain", err)
	}
}

func TestComplete_BadSignature(t *testing.T) {
	op := testOpDigest("op-badsig")
	proofs := linearChain(t, op, "sess-bad")
	proofs[1].Sig[0] ^= 0xFF
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: proofs}, time.Time{})
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestComplete_WrongKeyVerifier(t *testing.T) {
	op := testOpDigest("op-wrongkey")
	proofs := linearChain(t, op, "sess-key")
	v := NewValidator(NewPseudoVerifier([]byte("attacker-key")), NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: proofs}, time.Time{})
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestComplete_OpDigestMismatch(t *testing.T) {
	session := "sess-op"
	device, dd := signedProof(t, "device", session, testOpDigest("op-a"),
		ZeroDigest, testBase, 600)
	user, _ := signedProof(t, "user", session, testOpDigest("op-b"),
		dd, testBase.Add(time.Minute), 600)
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: []Proof{device, user}}, time.Time{})
	if !errors.Is(err, ErrOpMismatch) {
		t.Fatalf("err = %v, want ErrOpMismatch", err)
	}
}

func TestComplete_SessionMismatch(t *testing.T) {
	op := testOpDigest("op-session")
	device, dd := signedProof(t, "device", "sess-a", op, ZeroDigest, testBase, 600)
	user, _ := signedProof(t, "user", "sess-b", op, dd, testBase.Add(time.Minute), 600)
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: []Proof{device, user}}, time.Time{})
	if !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("err = %v, want ErrSessionMismatch", err)
	}
}

func TestComplete_TamperedParentDigest(t *testing.T) {
	op := testOpDigest("op-tamper-parent")
	proofs := linearChain(t, op, "sess-tp")

	// Tamper with the linkage, then re-sign so the signature itself is valid:
	// the parent reference no longer resolves inside the bundle.
	proofs[1].ParentDigest = SumDigest([]byte("forged-parent"))
	if err := testSigner.Sign(&proofs[1]); err != nil {
		t.Fatalf("resign: %v", err)
	}
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: proofs}, time.Time{})
	if !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("err = %v, want ErrBrokenChain", err)
	}
}

func TestComplete_NoWindowOverlap(t *testing.T) {
	op := testOpDigest("op-no-overlap")
	session := "sess-no-overlap"
	device, dd := signedProof(t, "device", session, op, ZeroDigest, testBase, 60)
	user, _ := signedProof(t, "user", session, op, dd, testBase.Add(2*time.Minute), 60)
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: []Proof{device, user}}, time.Time{})
	if !errors.Is(err, ErrNoWindowOverlap) {
		t.Fatalf("err = %v, want ErrNoWindowOverlap", err)
	}
}

func TestComplete_NotEffectiveAtGivenTime(t *testing.T) {
	op := testOpDigest("op-not-effective")
	proofs := linearChain(t, op, "sess-ne")
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: proofs}, testBase.Add(time.Hour))
	if !errors.Is(err, ErrNotEffective) {
		t.Fatalf("err = %v, want ErrNotEffective", err)
	}
}

func TestComplete_InvalidWindow(t *testing.T) {
	op := testOpDigest("op-bad-window")
	proof := Proof{
		StepID:    "device",
		OpDigest:  op,
		SessionID: "sess-iw",
		NotBefore: testBase.Add(time.Minute),
		NotAfter:  testBase,
		SignerID:  "signer-device",
	}
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: []Proof{proof}}, time.Time{})
	if !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("err = %v, want ErrInvalidWindow", err)
	}
}

func TestComplete_EdgeOrderViolated(t *testing.T) {
	op := testOpDigest("op-edge")
	session := "sess-edge"

	// user is the root and device chains after it: both steps are covered,
	// but the graph requires device before user.
	user, ud := signedProof(t, "user", session, op, ZeroDigest, testBase, 600)
	device, _ := signedProof(t, "device", session, op, ud, testBase.Add(time.Minute), 600)
	v := NewValidator(testVer, NewMemoryReplayCache())

	_, err := v.Complete(context.Background(), linearGraph(),
		Bundle{Proofs: []Proof{user, device}}, time.Time{})
	if !errors.Is(err, ErrEdgeOrderViolated) {
		t.Fatalf("err = %v, want ErrEdgeOrderViolated", err)
	}
}

func TestStepGraph_RejectsCycleAndUnknownRefs(t *testing.T) {
	cyclic := StepGraph{
		Steps: []string{"device", "user"},
		Edges: map[string][]string{
			"device": {"user"},
			"user":   {"device"},
		},
	}
	if err := cyclic.Validate(); !errors.Is(err, ErrGraphCycle) {
		t.Fatalf("cycle err = %v, want ErrGraphCycle", err)
	}

	badRef := StepGraph{
		Steps: []string{"device", "user"},
		Edges: map[string][]string{"user": {"ghost"}},
	}
	if err := badRef.Validate(); !errors.Is(err, ErrUnknownStep) {
		t.Fatalf("bad ref err = %v, want ErrUnknownStep", err)
	}
}

func TestComplete_InputGuards(t *testing.T) {
	op := testOpDigest("op-guards")
	proofs := linearChain(t, op, "sess-guards")

	if _, err := NewValidator(nil, nil).Complete(context.Background(),
		linearGraph(), Bundle{Proofs: proofs}, time.Time{}); !errors.Is(err, ErrNilVerifier) {
		t.Fatalf("nil verifier err = %v, want ErrNilVerifier", err)
	}
	if _, err := NewValidator(testVer, nil).Complete(context.Background(),
		linearGraph(), Bundle{}, time.Time{}); !errors.Is(err, ErrEmptyBundle) {
		t.Fatalf("empty bundle err = %v, want ErrEmptyBundle", err)
	}
}

func ExampleValidator() {
	op := SumDigest([]byte("sensitive:rotate-root-key"))
	signer := NewPseudoSigner([]byte("demo-key"))

	deviceProof := Proof{
		StepID:    "device",
		OpDigest:  op,
		SessionID: "sess-42",
		NotBefore: testBase,
		NotAfter:  testBase.Add(10 * time.Minute),
		SignerID:  "device-key",
	}
	_ = signer.Sign(&deviceProof)
	deviceDigest, _ := deviceProof.Digest()

	userProof := Proof{
		StepID:       "user",
		OpDigest:     op,
		SessionID:    "sess-42",
		ParentDigest: deviceDigest,
		NotBefore:    testBase,
		NotAfter:     testBase.Add(10 * time.Minute),
		SignerID:     "user-key",
	}
	_ = signer.Sign(&userProof)

	graph := StepGraph{
		Steps: []string{"device", "user"},
		Edges: map[string][]string{"user": {"device"}},
	}
	validator := NewValidator(NewPseudoVerifier([]byte("demo-key")), NewMemoryReplayCache())
	completion, err := validator.Complete(context.Background(), graph,
		Bundle{Proofs: []Proof{deviceProof, userProof}}, testBase.Add(time.Minute))
	if err != nil {
		panic(err)
	}
	_ = completion
	// Output:
}
