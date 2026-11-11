package fedid

import (
	"errors"
	"sync"
	"testing"
)

var (
	aliceIssuerA = IdentityKey{Issuer: "https://idp-a.example", Sub: "alice-a"}
	aliceIssuerB = IdentityKey{Issuer: "https://idp-b.example", Sub: "alice-b"}
	bobIssuerB   = IdentityKey{Issuer: "https://idp-b.example", Sub: "bob-b"}
)

func setup(t *testing.T) (*MemStore, *Service) {
	t.Helper()
	store := NewMemStore()
	svc := NewService(store)
	// Both IdPs happen to report the same email for different people.
	if err := svc.CreateAccount("acct-alice", aliceIssuerA, "shared@example.com"); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := svc.CreateAccount("acct-bob", bobIssuerB, "shared@example.com"); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	return store, svc
}

func seedProofs(store *MemStore, acctToken, idToken, accountID string, key IdentityKey, email string) {
	store.SeedProof(Proof{Token: acctToken, Kind: ProofAccount, AccountID: accountID})
	store.SeedProof(Proof{Token: idToken, Kind: ProofIdentity, Key: key, Email: email})
}

// 串号防护: same email at two IdPs must not merge accounts; binding an
// identity already owned by another account is rejected.
func TestCrossAccountBindingRejected(t *testing.T) {
	store, svc := setup(t)

	// Alice logs in via IdP A, Bob via IdP B, identical email: distinct accounts.
	a, err := svc.Login(aliceIssuerA, "shared@example.com")
	if err != nil || a.ID != "acct-alice" {
		t.Fatalf("alice login: %v %v", a, err)
	}
	b, err := svc.Login(bobIssuerB, "shared@example.com")
	if err != nil || b.ID != "acct-bob" {
		t.Fatalf("bob login: %v %v", b, err)
	}

	// Alice tries to bind Bob's IdP-B identity (same email) to her account.
	seedProofs(store, "pa1", "pi1", "acct-alice", bobIssuerB, "shared@example.com")
	err = svc.BindIdentity("acct-alice", "pa1", "pi1")
	if !errors.Is(err, ErrIdentityBound) {
		t.Fatalf("want ErrIdentityBound, got %v", err)
	}

	// Ownership unchanged; failed attempt rolled the proofs back for reuse.
	if id, _ := store.Identity(bobIssuerB); id.AccountID != "acct-bob" {
		t.Fatalf("bob identity hijacked: %+v", id)
	}
	if consumed, _ := store.ProofState("pa1"); consumed {
		t.Fatal("account proof should be restored after rollback")
	}
}

// 正常绑定: second identity binds with both one-time proofs; proofs are
// single-use afterwards.
func TestBindSecondIdentity(t *testing.T) {
	store, svc := setup(t)
	seedProofs(store, "pa2", "pi2", "acct-alice", aliceIssuerB, "shared@example.com")

	if err := svc.BindIdentity("acct-alice", "pa2", "pi2"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	acct, err := svc.Login(aliceIssuerB, "shared@example.com")
	if err != nil || acct.ID != "acct-alice" {
		t.Fatalf("login via second identity: %v %v", acct, err)
	}
	if len(acct.Identities) != 2 {
		t.Fatalf("want 2 identities, got %v", acct.Identities)
	}
	if consumed, _ := store.ProofState("pi2"); !consumed {
		t.Fatal("identity proof must be consumed")
	}
	// Replay of the same proof fails.
	if err := svc.BindIdentity("acct-alice", "pa2", "pi2"); !errors.Is(err, ErrProofInvalid) {
		t.Fatalf("replay: want ErrProofInvalid, got %v", err)
	}
}

// 并发: the same proof submitted concurrently succeeds exactly once.
func TestConcurrentSameProofSucceedsOnce(t *testing.T) {
	store, svc := setup(t)
	seedProofs(store, "pa3", "pi3", "acct-alice", aliceIssuerB, "shared@example.com")

	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = svc.BindIdentity("acct-alice", "pa3", "pi3")
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("want exactly 1 success, got %d", succeeded)
	}
	id, _ := store.Identity(aliceIssuerB)
	if id.AccountID != "acct-alice" {
		t.Fatalf("identity owner: %+v", id)
	}
	acct, _ := store.Account("acct-alice")
	if len(acct.Identities) != 2 {
		t.Fatalf("account identities duplicated: %v", acct.Identities)
	}
}

// 并发竞争: two accounts race to bind the same new identity; only one wins.
func TestConcurrentCrossBindOnlyOneWins(t *testing.T) {
	store, svc := setup(t)
	seedProofs(store, "pa4", "pi4", "acct-alice", aliceIssuerB, "shared@example.com")
	seedProofs(store, "pb4", "pj4", "acct-bob", aliceIssuerB, "shared@example.com")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	targets := [][3]string{
		{"acct-alice", "pa4", "pi4"},
		{"acct-bob", "pb4", "pj4"},
	}
	for i, args := range targets {
		wg.Add(1)
		go func(i int, accountID, pa, pi string) {
			defer wg.Done()
			errs[i] = svc.BindIdentity(accountID, pa, pi)
		}(i, args[0], args[1], args[2])
	}
	wg.Wait()

	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("exactly one bind must win: %v %v", errs[0], errs[1])
	}
	id, _ := store.Identity(aliceIssuerB)
	want := "acct-alice"
	if errs[0] != nil {
		want = "acct-bob"
	}
	if id.AccountID != want {
		t.Fatalf("identity owned by %s, want %s", id.AccountID, want)
	}
}

// 迁移失败回滚: commit failure leaves no partial state and restores proofs.
func TestMigrationFailureRollsBack(t *testing.T) {
	store, svc := setup(t)
	seedProofs(store, "pa5", "pi5", "acct-alice", aliceIssuerB, "shared@example.com")

	store.FailCommit = true
	err := svc.BindIdentity("acct-alice", "pa5", "pi5")
	if !errors.Is(err, ErrCommitFailed) {
		t.Fatalf("want ErrCommitFailed, got %v", err)
	}

	// No partial migration: identity absent, account untouched.
	if _, exists := store.Identity(aliceIssuerB); exists {
		t.Fatal("identity leaked despite rollback")
	}
	acct, _ := store.Account("acct-alice")
	if len(acct.Identities) != 1 {
		t.Fatalf("account mutated despite rollback: %v", acct.Identities)
	}
	// Proofs restored: retry succeeds once the failure clears.
	for _, token := range []string{"pa5", "pi5"} {
		if consumed, _ := store.ProofState(token); consumed {
			t.Fatalf("proof %s not restored", token)
		}
	}
	store.FailCommit = false
	if err := svc.BindIdentity("acct-alice", "pa5", "pi5"); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	if id, _ := store.Identity(aliceIssuerB); id.AccountID != "acct-alice" {
		t.Fatalf("retry ownership: %+v", id)
	}
}

// 邮箱变更: login keys on issuer+sub; a changed email updates the record
// and still resolves to the same account.
func TestLoginAfterEmailChange(t *testing.T) {
	store, svc := setup(t)
	seedProofs(store, "pa6", "pi6", "acct-alice", aliceIssuerB, "shared@example.com")
	if err := svc.BindIdentity("acct-alice", "pa6", "pi6"); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// IdP B now reports a new verified email for the same sub.
	acct, err := svc.Login(aliceIssuerB, "alice-new@example.net")
	if err != nil || acct.ID != "acct-alice" {
		t.Fatalf("login after email change: %v %v", acct, err)
	}
	id, _ := store.Identity(aliceIssuerB)
	if id.Email != "alice-new@example.net" || id.AccountID != "acct-alice" {
		t.Fatalf("email not updated in place: %+v", id)
	}

	// Bob's identity at the same IdP is untouched by Alice's email change.
	bob, err := svc.Login(bobIssuerB, "shared@example.com")
	if err != nil || bob.ID != "acct-bob" {
		t.Fatalf("bob login: %v %v", bob, err)
	}
}
