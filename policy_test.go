package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	alice = "alice@example.com"
	bob   = "bob@example.com"
)

func signed(repo *MemoryRepository, id string, size int64, parents ...OID) OID {
	oid := OID(id)
	repo.Add(&MemoryCommit{OID: oid, Parent: parents, Bytes: size, Signer: alice})
	return oid
}

func unsigned(repo *MemoryRepository, id string, size int64, parents ...OID) OID {
	oid := OID(id)
	repo.Add(&MemoryCommit{OID: oid, Parent: parents, Bytes: size})
	return oid
}

func newTestPolicy(t *testing.T, repo Repository, limit int64, allowed ...string) (*Policy, *Quota) {
	t.Helper()
	quota := NewQuota(limit)
	return NewPolicy(repo, quota, MemoryVerifier, allowed...), quota
}

// A straightforward fast-forward of correctly signed commits is admitted and
// reserves exactly the introduced object bytes.
func TestAdmitSignedHistory(t *testing.T) {
	repo := NewMemoryRepository()
	base := signed(repo, "base", 10)
	mid := signed(repo, "mid", 20, base)
	tip := signed(repo, "tip", 30, mid)

	policy, quota := newTestPolicy(t, repo, 1000, alice)
	grant, err := policy.Admit(context.Background(), []RefUpdate{
		{Ref: "refs/heads/main", Old: base, New: tip},
	})
	if err != nil {
		t.Fatalf("expected admission, got %v", err)
	}
	if grant.Bytes() != 50 {
		t.Fatalf("expected 50 reserved bytes (mid+tip), got %d", grant.Bytes())
	}
	if quota.Used() != 50 {
		t.Fatalf("expected quota used 50, got %d", quota.Used())
	}

	grant.Release()
	if quota.Used() != 0 {
		t.Fatalf("expected quota released, used=%d", quota.Used())
	}
}

// A commit reachable only through a merge's second parent must still be
// inspected. A first-parent-only walk would miss the unsigned side commit.
func TestAdmitRejectsUnsignedSideBranch(t *testing.T) {
	repo := NewMemoryRepository()
	base := signed(repo, "base", 10)

	trunk := signed(repo, "trunk", 10, base)
	side := unsigned(repo, "side", 10, base)
	merge := signed(repo, "merge", 10, trunk, side)

	policy, quota := newTestPolicy(t, repo, 1000, alice)
	_, err := policy.Admit(context.Background(), []RefUpdate{
		{Ref: "refs/heads/main", Old: base, New: merge},
	})
	if err == nil {
		t.Fatal("expected rejection of unsigned side-branch commit")
	}

	var admissionErr *AdmissionError
	if !errors.As(err, &admissionErr) {
		t.Fatalf("expected *AdmissionError, got %T: %v", err, err)
	}
	if !errors.Is(err, ErrUnsignedCommit) {
		t.Fatalf("expected errors.Is ErrUnsignedCommit, got %v", err)
	}

	found := false
	for _, violation := range admissionErr.Violations {
		if violation.Commit == side {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected violation for side commit %s, got %+v", side, admissionErr.Violations)
	}

	if used := quota.Used(); used != 0 {
		t.Fatalf("rejected push leaked %d quota bytes", used)
	}
}

// A valid signature from an identity not on the allow list is rejected.
func TestAdmitRejectsDisallowedSigner(t *testing.T) {
	repo := NewMemoryRepository()
	base := signed(repo, "base", 10)
	oid := OID("bob-commit")
	repo.Add(&MemoryCommit{OID: oid, Parent: []OID{base}, Bytes: 10, Signer: bob})

	policy, quota := newTestPolicy(t, repo, 1000, alice)
	_, err := policy.Admit(context.Background(), []RefUpdate{
		{Ref: "refs/heads/main", Old: base, New: oid},
	})
	if !errors.Is(err, ErrSignerNotAllowed) {
		t.Fatalf("expected ErrSignerNotAllowed, got %v", err)
	}
	if quota.Used() != 0 {
		t.Fatalf("rejected push leaked %d quota bytes", quota.Used())
	}
}

// A signed commit whose cryptographic verification fails is treated as
// unsigned and rejected through the verifier callback.
func TestAdmitRejectsBadSignature(t *testing.T) {
	repo := NewMemoryRepository()
	base := signed(repo, "base", 10)
	oid := OID("forged")
	repo.Add(&MemoryCommit{OID: oid, Parent: []OID{base}, Bytes: 10, Signer: alice, BadSignature: true})

	policy, quota := newTestPolicy(t, repo, 1000, alice)
	_, err := policy.Admit(context.Background(), []RefUpdate{
		{Ref: "refs/heads/main", Old: base, New: oid},
	})
	if !errors.Is(err, ErrUnsignedCommit) {
		t.Fatalf("expected ErrUnsignedCommit for failed verification, got %v", err)
	}
	if quota.Used() != 0 {
		t.Fatalf("rejected push leaked %d quota bytes", quota.Used())
	}
}

// A push whose introduced objects exceed the quota is rejected atomically and
// no partial reservation remains.
func TestAdmitRejectsOverQuotaAndReturnsBytes(t *testing.T) {
	repo := NewMemoryRepository()
	base := signed(repo, "base", 10)
	a := signed(repo, "a", 40, base)
	b := signed(repo, "b", 40, a)

	policy, quota := newTestPolicy(t, repo, 60, alice)

	// A first, fitting push consumes part of the shared budget.
	first, err := policy.Admit(context.Background(), []RefUpdate{
		{Ref: "refs/heads/main", Old: base, New: a},
	})
	if err != nil {
		t.Fatalf("first push should fit: %v", err)
	}
	if quota.Used() != 40 {
		t.Fatalf("expected 40 used, got %d", quota.Used())
	}

	// The second push needs 40 more but only 20 remain.
	_, err = policy.Admit(context.Background(), []RefUpdate{
		{Ref: "refs/heads/main", Old: a, New: b},
	})
	if !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("expected ErrQuotaExhausted, got %v", err)
	}

	// The failed reservation must not have changed the quota.
	if quota.Used() != 40 {
		t.Fatalf("over-quota push mutated quota, used=%d", quota.Used())
	}

	// Once the first push aborts and releases, the full budget is available
	// again for a retry.
	first.Release()
	if quota.Used() != 0 || quota.Available() != 60 {
		t.Fatalf("expected full quota restored, used=%d available=%d", quota.Used(), quota.Available())
	}

	retry, err := policy.Admit(context.Background(), []RefUpdate{
		{Ref: "refs/heads/main", Old: base, New: a},
	})
	if err != nil {
		t.Fatalf("retry after release should fit: %v", err)
	}
	retry.Commit()
	if quota.Used() != 40 {
		t.Fatalf("expected committed 40 bytes, used=%d", quota.Used())
	}

	// Release after Commit must be a no-op.
	retry.Release()
	if quota.Used() != 40 {
		t.Fatalf("release after commit must not return bytes, used=%d", quota.Used())
	}
}

// Concurrent pushes contend for one shared quota. The invariant used <= limit
// must hold throughout, and reservations must balance exactly.
func TestAdmitConcurrentPushesReserveAtomically(t *testing.T) {
	repo := NewMemoryRepository()
	base := signed(repo, "base", 1)

	const pushCount = 60
	policy, quota := newTestPolicy(t, repo, 100, alice)

	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := []*Grant{}
	var rejectedQuota, rejectedSig int64

	for i := 0; i < pushCount; i++ {
		// Each push introduces one independent 25-byte commit off base, so at
		// most four can ever be outstanding under a 100-byte quota.
		oid := OID(fmt.Sprintf("c%d", i))
		repo.Add(&MemoryCommit{OID: oid, Parent: []OID{base}, Bytes: 25, Signer: alice})

		wg.Add(1)
		go func(id OID) {
			defer wg.Done()
			grant, err := policy.Admit(context.Background(), []RefUpdate{
				{Ref: "refs/heads/x/" + string(id), Old: ZeroOID, New: id},
			})
			switch {
			case err == nil:
				mu.Lock()
				admitted = append(admitted, grant)
				mu.Unlock()
			case errors.Is(err, ErrQuotaExhausted):
				atomic.AddInt64(&rejectedQuota, 1)
			default:
				atomic.AddInt64(&rejectedSig, 1)
			}

			if quota.Used() > quota.Limit() {
				t.Errorf("quota oversubscribed: used=%d limit=%d", quota.Used(), quota.Limit())
			}
		}(oid)
	}
	wg.Wait()

	if rejectedSig != 0 {
		t.Fatalf("all commits are signed, unexpected signature rejections=%d", rejectedSig)
	}
	if int64(len(admitted))+rejectedQuota != pushCount {
		t.Fatalf("outcome mismatch: admitted=%d quotaRejected=%d total=%d",
			len(admitted), rejectedQuota, pushCount)
	}
	if len(admitted) == 0 || len(admitted) > 4 {
		t.Fatalf("expected 1..4 concurrent grants of 25 bytes under 100, got %d", len(admitted))
	}

	// Outstanding reservations must account for every used byte exactly.
	var held int64
	for _, grant := range admitted {
		held += grant.Bytes()
	}
	if held != quota.Used() {
		t.Fatalf("reservation mismatch: grants=%d used=%d", held, quota.Used())
	}

	// Releasing everything restores the entire budget, proving no leaks.
	for _, grant := range admitted {
		grant.Release()
	}
	if quota.Used() != 0 {
		t.Fatalf("expected all bytes returned, used=%d", quota.Used())
	}
}

// Multiple ref updates in one push share an introduced set: a commit reachable
// from two refs is counted once, and deletions introduce no objects.
func TestAdmitMultipleRefsDedupAndDeletion(t *testing.T) {
	repo := NewMemoryRepository()
	base := signed(repo, "base", 10)
	shared := signed(repo, "shared", 30, base)
	tipA := signed(repo, "tipA", 5, shared)
	tipB := signed(repo, "tipB", 7, shared)

	policy, _ := newTestPolicy(t, repo, 1000, alice)
	grant, err := policy.Admit(context.Background(), []RefUpdate{
		{Ref: "refs/heads/a", Old: base, New: tipA},
		{Ref: "refs/heads/b", Old: base, New: tipB},
		{Ref: "refs/heads/obsolete", Old: base, New: ZeroOID},
	})
	if err != nil {
		t.Fatalf("expected admission, got %v", err)
	}

	// shared(30) counted once + tipA(5) + tipB(7) = 42; base is in the cut.
	if grant.Bytes() != 42 {
		t.Fatalf("expected deduplicated 42 bytes, got %d", grant.Bytes())
	}
	grant.Release()
}

// A force-push whose old value is not an ancestor still only inspects commits
// not reachable from the old tip.
func TestAdmitForcePushCut(t *testing.T) {
	repo := NewMemoryRepository()
	root := signed(repo, "root", 10)

	oldLine := signed(repo, "old1", 10, root)
	oldTip := signed(repo, "old2", 10, oldLine)

	newLine := signed(repo, "new1", 10, root)
	newTip := signed(repo, "new2", 10, newLine)

	policy, _ := newTestPolicy(t, repo, 1000, alice)
	grant, err := policy.Admit(context.Background(), []RefUpdate{
		{Ref: "refs/heads/main", Old: oldTip, New: newTip},
	})
	if err != nil {
		t.Fatalf("expected admission, got %v", err)
	}
	// Only new1 and new2 are outside the old-tip cut.
	if grant.Bytes() != 20 {
		t.Fatalf("expected 20 new bytes, got %d", grant.Bytes())
	}
	grant.Release()
}

// A missing object fails closed rather than silently skipping the commit.
func TestAdmitMissingObjectFailsClosed(t *testing.T) {
	repo := NewMemoryRepository()
	ghost := OID("ghost")

	policy, quota := newTestPolicy(t, repo, 1000, alice)
	_, err := policy.Admit(context.Background(), []RefUpdate{
		{Ref: "refs/heads/main", Old: ZeroOID, New: ghost},
	})
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("expected ErrObjectNotFound, got %v", err)
	}
	if quota.Used() != 0 {
		t.Fatalf("failed admission leaked quota, used=%d", quota.Used())
	}
}
