package netsec

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	clock := NewManualClock(1_000_000)
	verifier := HMACVerifier{Secret: []byte("test-secret")}
	engine := NewEngine(store,
		WithClock(clock),
		WithPolicy(emailLowRiskPolicy(time.Minute)),
		WithVerifier(verifier),
	)
	const account = "account-1"
	if _, err := engine.CreateAccount(account, baseEntries()...); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := engine.OpenSession(account, "session-1", ""); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	req, err := engine.NewChangeRequest(account, "session-1", ActionAdd,
		Target{MethodID: "mail-new", Kind: MethodEmail, MaterialDigest: "new"}, "")
	if err != nil {
		t.Fatalf("NewChangeRequest: %v", err)
	}
	confirmation, err := verifier.Sign(req, "dev-1")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := engine.Propose(req, confirmation); err != nil {
		t.Fatalf("Propose: %v", err)
	}

	reopened, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	second := NewEngine(reopened,
		WithClock(clock),
		WithPolicy(emailLowRiskPolicy(time.Minute)),
		WithVerifier(verifier),
	)
	snap, err := second.Snapshot(account)
	if err != nil {
		t.Fatalf("Snapshot from disk: %v", err)
	}
	if len(snap.Pending) != 1 || snap.Pending[0].Request.RequestID != req.RequestID {
		t.Fatalf("pending not persisted: %+v", snap.Pending)
	}

	clock.Advance(time.Minute)
	if _, err := second.Activate(account, req.RequestID); err != nil {
		t.Fatalf("Activate from disk: %v", err)
	}
	if _, err := second.Recover(account, "mail-new"); err != nil {
		t.Fatalf("Recover from disk: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, ".account-*.tmp"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files left behind: %v", matches)
	}
}
