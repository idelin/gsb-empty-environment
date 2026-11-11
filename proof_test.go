package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newBuilder(t *testing.T, v *Verifier, id string) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	v.RegisterBuilder(id, pub)
	return priv
}

func signedProof(t *testing.T, priv ed25519.PrivateKey, builderID, targetEnv string, outputs ...Output) *Proof {
	t.Helper()
	p := &Proof{
		InputTreeDigest: digestOf("source-tree"),
		BuilderID:       builderID,
		Outputs:         outputs,
		TargetEnv:       targetEnv,
	}
	SignProof(p, priv)
	return p
}

func TestLegitimateReleaseSharedByMultipleFiles(t *testing.T) {
	v := NewVerifier()
	priv := newBuilder(t, v, "builder-1")

	outputs := []Output{
		{Path: "bin/server", Digest: digestOf("server-binary")},
		{Path: "bin/agent", Digest: digestOf("agent-binary")},
		{Path: "config/app.yaml", Digest: digestOf("config")},
	}
	proof := signedProof(t, priv, "builder-1", "prod", outputs...)
	plan := &Plan{ID: "rel-1", Environment: "prod", Files: outputs}

	ticket, err := v.Consume(proof, plan)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if err := v.Approve(ticket, plan); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// The same proof may be consumed again for the same plan: multiple files
	// of one legitimate release share a single proof.
	for i := 0; i < 3; i++ {
		again, err := v.Consume(proof, plan)
		if err != nil {
			t.Fatalf("re-consume %d: %v", i, err)
		}
		if again.PlanDigest != ticket.PlanDigest || again.ProofHash != ticket.ProofHash {
			t.Fatalf("re-consume %d: ticket binding changed", i)
		}
	}
}

func TestReplayAcrossPlansRejected(t *testing.T) {
	v := NewVerifier()
	priv := newBuilder(t, v, "builder-1")

	outputs := []Output{
		{Path: "bin/server", Digest: digestOf("server-binary")},
		{Path: "bin/agent", Digest: digestOf("agent-binary")},
	}
	proof := signedProof(t, priv, "builder-1", "prod", outputs...)
	planA := &Plan{ID: "rel-a", Environment: "prod", Files: outputs}

	ticketA, err := v.Consume(proof, planA)
	if err != nil {
		t.Fatalf("consume plan A: %v", err)
	}

	// Replaying the same proof against a different plan must fail, even if
	// the other plan only uses a subset of the same outputs.
	planB := &Plan{ID: "rel-b", Environment: "prod", Files: outputs[:1]}
	if _, err := v.Consume(proof, planB); !errors.Is(err, ErrCrossPlanReuse) {
		t.Fatalf("expected ErrCrossPlanReuse, got %v", err)
	}

	// A ticket issued for plan A cannot authorize plan B.
	if err := v.Approve(ticketA, planB); !errors.Is(err, ErrTicketPlanMismatch) {
		t.Fatalf("expected ErrTicketPlanMismatch, got %v", err)
	}

	// A proof attested for another target environment can never be consumed
	// by this plan in the first place.
	stagingProof := signedProof(t, priv, "builder-1", "staging", outputs...)
	if _, err := v.Consume(stagingProof, planA); !errors.Is(err, ErrEnvironmentMismatch) {
		t.Fatalf("expected ErrEnvironmentMismatch, got %v", err)
	}
}

func TestPartialFileMissingRejected(t *testing.T) {
	v := NewVerifier()
	priv := newBuilder(t, v, "builder-1")

	outputs := []Output{
		{Path: "bin/server", Digest: digestOf("server-binary")},
	}
	proof := signedProof(t, priv, "builder-1", "prod", outputs...)

	// Plan references a file the proof does not cover.
	plan := &Plan{ID: "rel-1", Environment: "prod", Files: []Output{
		outputs[0],
		{Path: "bin/extra", Digest: digestOf("extra")},
	}}
	if _, err := v.Consume(proof, plan); !errors.Is(err, ErrOutputNotCovered) {
		t.Fatalf("expected ErrOutputNotCovered, got %v", err)
	}

	// Plan tampers with a covered file's digest.
	tampered := &Plan{ID: "rel-2", Environment: "prod", Files: []Output{
		{Path: "bin/server", Digest: digestOf("forged")},
	}}
	if _, err := v.Consume(proof, tampered); !errors.Is(err, ErrOutputNotCovered) {
		t.Fatalf("expected ErrOutputNotCovered for tampered digest, got %v", err)
	}

	// Nothing was bound, so the legitimate plan still succeeds afterwards.
	good := &Plan{ID: "rel-3", Environment: "prod", Files: outputs}
	if _, err := v.Consume(proof, good); err != nil {
		t.Fatalf("legitimate plan after rejections: %v", err)
	}
}

func TestForgedProofRejected(t *testing.T) {
	v := NewVerifier()
	priv := newBuilder(t, v, "builder-1")

	outputs := []Output{{Path: "bin/server", Digest: digestOf("server-binary")}}
	plan := &Plan{ID: "rel-1", Environment: "prod", Files: outputs}

	// Unknown builder identity.
	ghost := signedProof(t, priv, "builder-ghost", "prod", outputs...)
	if _, err := v.Consume(ghost, plan); !errors.Is(err, ErrUnknownBuilder) {
		t.Fatalf("expected ErrUnknownBuilder, got %v", err)
	}

	// Tampered after signing.
	proof := signedProof(t, priv, "builder-1", "prod", outputs...)
	proof.TargetEnv = "staging"
	SignProof(proof, priv)
	proof.TargetEnv = "prod" // signature no longer matches
	if _, err := v.Consume(proof, plan); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature, got %v", err)
	}
}

func TestConcurrentPlansCannotCombine(t *testing.T) {
	v := NewVerifier()
	priv := newBuilder(t, v, "builder-1")

	outputs := []Output{
		{Path: "bin/server", Digest: digestOf("server-binary")},
		{Path: "bin/agent", Digest: digestOf("agent-binary")},
	}
	proof := signedProof(t, priv, "builder-1", "prod", outputs...)

	const numPlans = 16
	plans := make([]*Plan, numPlans)
	for i := range plans {
		// Each racing plan differs: different subsets, orders or extra files.
		files := []Output{outputs[i%len(outputs)]}
		if i%3 == 0 {
			files = append(files, outputs[(i+1)%len(outputs)])
		}
		plans[i] = &Plan{ID: fmt.Sprintf("rel-%d", i), Environment: "prod", Files: files}
	}
	// Include exact duplicates of one plan so multiple goroutines can
	// legitimately win with the same digest.
	for i := 0; i < 4; i++ {
		plans[i] = &Plan{ID: fmt.Sprintf("rel-dup-%d", i), Environment: "prod", Files: outputs}
	}

	var wg sync.WaitGroup
	digests := make([]string, numPlans)
	errs := make([]error, numPlans)
	for i := range plans {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ticket, err := v.Consume(proof, plans[i])
			if err != nil {
				errs[i] = err
				return
			}
			digests[i] = ticket.PlanDigest
		}(i)
	}
	wg.Wait()

	winning := ""
	wins := 0
	for i := range plans {
		if errs[i] != nil {
			if !errors.Is(errs[i], ErrCrossPlanReuse) {
				t.Fatalf("plan %s: unexpected error %v", plans[i].ID, errs[i])
			}
			continue
		}
		wins++
		if winning == "" {
			winning = digests[i]
		} else if digests[i] != winning {
			t.Fatalf("approvals combined across plans: %s vs %s", digests[i], winning)
		}
	}
	if wins == 0 {
		t.Fatal("no plan was approved")
	}
}
