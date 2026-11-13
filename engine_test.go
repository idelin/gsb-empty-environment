package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fixture struct {
	engine   *Engine
	store    Store
	clock    *ManualClock
	verifier HMACVerifier
	account  string
	session  string
}

func baseEntries() []Entry {
	return []Entry{
		{MethodID: "dev-1", Kind: MethodDeviceKey, MaterialDigest: "dev-material-1"},
		{MethodID: "mail-1", Kind: MethodEmail, MaterialDigest: "mail-material-1"},
		{MethodID: "contact-1", Kind: MethodRecoveryContact, MaterialDigest: "contact-material-1"},
	}
}

func emailLowRiskPolicy(delay time.Duration) Policy {
	return Policy{
		Delay: func(ChangeRequest) time.Duration { return delay },
		Risk: func(req ChangeRequest) RiskLevel {
			if req.Target.Kind == MethodEmail {
				return RiskLow
			}
			return RiskHigh
		},
		Strong: func(MethodKind) bool { return true },
	}
}

func newFixture(t *testing.T, delay time.Duration, entries []Entry) *fixture {
	t.Helper()
	store := NewMemoryStore()
	clock := NewManualClock(1_000_000)
	verifier := HMACVerifier{Secret: []byte("test-secret")}
	engine := NewEngine(
		store,
		WithClock(clock),
		WithPolicy(emailLowRiskPolicy(delay)),
		WithVerifier(verifier),
	)
	const account = "account-1"
	if _, err := engine.CreateAccount(account, entries...); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := engine.OpenSession(account, "session-1", ""); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	return &fixture{engine: engine, store: store, clock: clock, verifier: verifier, account: account, session: "session-1"}
}

func (f *fixture) request(t *testing.T, action Action, target Target, replace string) ChangeRequest {
	t.Helper()
	req, err := f.engine.NewChangeRequest(f.account, f.session, action, target, replace)
	if err != nil {
		t.Fatalf("NewChangeRequest: %v", err)
	}
	return req
}

func (f *fixture) sign(t *testing.T, req ChangeRequest, methodIDs ...string) []Confirmation {
	t.Helper()
	out := make([]Confirmation, 0, len(methodIDs))
	for _, methodID := range methodIDs {
		confirmation, err := f.verifier.Sign(req, methodID)
		if err != nil {
			t.Fatalf("Sign(%s): %v", methodID, err)
		}
		out = append(out, confirmation)
	}
	return out
}

func (f *fixture) addEmail(t *testing.T, methodID, material string, confirmer string) (ChangeRequest, PendingChange) {
	t.Helper()
	req := f.request(t, ActionAdd, Target{MethodID: methodID, Kind: MethodEmail, MaterialDigest: material}, "")
	pending, err := f.engine.Propose(req, f.sign(t, req, confirmer)...)
	if err != nil {
		t.Fatalf("Propose(%s): %v", methodID, err)
	}
	return req, pending
}

func (f *fixture) snapshot(t *testing.T) Snapshot {
	t.Helper()
	snap, err := f.engine.Snapshot(f.account)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return snap
}

func (f *fixture) mutate(t *testing.T, fn func(*AccountState)) {
	t.Helper()
	state, err := f.store.Load(f.account)
	if err != nil {
		t.Fatalf("store load: %v", err)
	}
	fn(state)
	if err := f.store.Save(state); err != nil {
		t.Fatalf("store save: %v", err)
	}
}

func TestDelayBoundary(t *testing.T) {
	const delay = 10 * time.Minute
	f := newFixture(t, delay, baseEntries())

	req, pending := f.addEmail(t, "mail-new", "new-material", "dev-1")
	if pending.Entry.Status != StatusPending {
		t.Fatalf("entry status = %s, want Pending", pending.Entry.Status)
	}
	if pending.NotBefore != MonoTime(1_000_000)+MonoTime(delay) {
		t.Fatalf("NotBefore = %d, want %d", pending.NotBefore, MonoTime(1_000_000)+MonoTime(delay))
	}
	if _, err := f.engine.Recover(f.account, "mail-new"); !errors.Is(err, ErrPendingNotUsable) {
		t.Fatalf("Recover pending: %v, want ErrPendingNotUsable", err)
	}

	f.clock.Advance(delay - time.Nanosecond)
	if _, err := f.engine.Activate(f.account, req.RequestID); !errors.Is(err, ErrDelayNotElapsed) {
		t.Fatalf("Activate one nanosecond early: %v, want ErrDelayNotElapsed", err)
	}
	f.clock.Advance(time.Nanosecond)
	entry, err := f.engine.Activate(f.account, req.RequestID)
	if err != nil {
		t.Fatalf("Activate at boundary: %v", err)
	}
	if entry.Status != StatusActive || entry.EffectiveAt != f.clock.Now() {
		t.Fatalf("activated entry = %+v", entry)
	}
	if _, err := f.engine.Recover(f.account, "mail-new"); err != nil {
		t.Fatalf("Recover active: %v", err)
	}
}

func TestHighRiskNeedsTwoDistinctCategories(t *testing.T) {
	f := newFixture(t, time.Minute, append(baseEntries(),
		Entry{MethodID: "mail-2", Kind: MethodEmail, MaterialDigest: "mail-material-2"},
	))

	high := f.request(t, ActionAdd, Target{MethodID: "dev-2", Kind: MethodDeviceKey, MaterialDigest: "dev-material-2"}, "")
	if _, err := f.engine.Propose(high, f.sign(t, high, "mail-1", "contact-1")...); err != nil {
		t.Fatalf("high-risk with two categories: %v", err)
	}

	sameCategory := f.request(t, ActionAdd, Target{MethodID: "dev-3", Kind: MethodDeviceKey, MaterialDigest: "dev-material-3"}, "")
	if _, err := f.engine.Propose(sameCategory, f.sign(t, sameCategory, "mail-1", "mail-2")...); !errors.Is(err, ErrInsufficientConfirmation) {
		t.Fatalf("same-category confirmations: %v, want ErrInsufficientConfirmation", err)
	}

	if _, err := f.engine.OpenSession(f.account, "mail-session", "mail-1"); err != nil {
		t.Fatalf("OpenSession(mail-1): %v", err)
	}
	sessionBound := f.request(t, ActionAdd, Target{MethodID: "dev-4", Kind: MethodDeviceKey, MaterialDigest: "dev-material-4"}, "")
	sessionBound.SessionID = "mail-session"
	if _, err := f.engine.Propose(sessionBound, f.sign(t, sessionBound, "mail-1", "contact-1")...); !errors.Is(err, ErrInsufficientConfirmation) {
		t.Fatalf("session factor confirmation: %v, want ErrInsufficientConfirmation", err)
	}

	low := f.request(t, ActionAdd, Target{MethodID: "mail-new", Kind: MethodEmail, MaterialDigest: "new-mail"}, "")
	if _, err := f.engine.Propose(low, f.sign(t, low, "dev-1")...); err != nil {
		t.Fatalf("low-risk with one factor: %v", err)
	}
	noConfirmations := f.request(t, ActionAdd, Target{MethodID: "mail-none", Kind: MethodEmail, MaterialDigest: "none"}, "")
	if _, err := f.engine.Propose(noConfirmations); !errors.Is(err, ErrInsufficientConfirmation) {
		t.Fatalf("no confirmations: %v, want ErrInsufficientConfirmation", err)
	}
}

func TestSignatureCoversEveryBoundField(t *testing.T) {
	f := newFixture(t, time.Minute, baseEntries())
	req := f.request(t, ActionAdd, Target{MethodID: "mail-new", Kind: MethodEmail, MaterialDigest: "new-mail"}, "")
	signed := f.sign(t, req, "dev-1")
	base := RequestDigest(req)

	mutations := []struct {
		name string
		edit func(*ChangeRequest)
	}{
		{"account", func(r *ChangeRequest) { r.AccountID = "other-account" }},
		{"config digest", func(r *ChangeRequest) { r.ConfigDigest = "different" }},
		{"session", func(r *ChangeRequest) { r.SessionID = "other-session" }},
		{"action", func(r *ChangeRequest) { r.Action = ActionReplace; r.ReplaceMethodID = "mail-1" }},
		{"target method", func(r *ChangeRequest) { r.Target.MethodID = "mail-other" }},
		{"target kind", func(r *ChangeRequest) { r.Target.Kind = MethodDeviceKey }},
		{"target material", func(r *ChangeRequest) { r.Target.MaterialDigest = "tampered" }},
		{"replacement", func(r *ChangeRequest) { r.ReplaceMethodID = "mail-1" }},
		{"request id", func(r *ChangeRequest) { r.RequestID = "tampered" }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := req
			mutation.edit(&changed)
			if RequestDigest(changed) == base {
				t.Fatalf("%s did not change request digest", mutation.name)
			}
			if mutation.name == "target material" || mutation.name == "request id" {
				if _, err := f.engine.Propose(changed, signed...); !errors.Is(err, ErrInsufficientConfirmation) {
					t.Fatalf("tampered %s: %v, want signature failure", mutation.name, err)
				}
			}
		})
	}
}

func TestPendingCannotAuthenticateConfirmOrRecover(t *testing.T) {
	f := newFixture(t, time.Minute, baseEntries())
	_, pending := f.addEmail(t, "mail-new", "new-mail", "dev-1")

	if _, err := f.engine.Authenticate(f.account, "mail-new"); !errors.Is(err, ErrPendingNotUsable) {
		t.Fatalf("Authenticate pending: %v", err)
	}
	if _, err := f.engine.Login(f.account, "mail-new"); !errors.Is(err, ErrPendingNotUsable) {
		t.Fatalf("Login pending: %v", err)
	}
	if _, err := f.engine.Recover(f.account, "mail-new"); !errors.Is(err, ErrPendingNotUsable) {
		t.Fatalf("Recover pending: %v", err)
	}
	if _, err := f.engine.OpenSession(f.account, "pending-session", "mail-new"); !errors.Is(err, ErrPendingNotUsable) {
		t.Fatalf("OpenSession pending: %v", err)
	}

	second := f.request(t, ActionAdd, Target{MethodID: "mail-second", Kind: MethodEmail, MaterialDigest: "second"}, "")
	if _, err := f.engine.Propose(second, f.sign(t, second, "mail-new")...); !errors.Is(err, ErrPendingNotUsable) {
		t.Fatalf("pending confirmation: %v, want ErrPendingNotUsable", err)
	}
	if _, err := f.engine.Activate(f.account, pending.Request.RequestID); !errors.Is(err, ErrDelayNotElapsed) {
		t.Fatalf("early activation: %v", err)
	}
}

func TestConfirmerRevokedDuringDelay(t *testing.T) {
	f := newFixture(t, time.Minute, baseEntries())
	req := f.request(t, ActionAdd, Target{MethodID: "dev-2", Kind: MethodDeviceKey, MaterialDigest: "dev-material-2"}, "")
	if _, err := f.engine.Propose(req, f.sign(t, req, "mail-1", "contact-1")...); err != nil {
		t.Fatalf("Propose: %v", err)
	}

	f.mutate(t, func(state *AccountState) {
		for i := range state.Entries {
			if state.Entries[i].Status == StatusActive && state.Entries[i].MethodID == "contact-1" {
				state.Entries[i].Status = StatusRevoked
			}
		}
	})
	f.clock.Advance(time.Minute)
	if _, err := f.engine.Activate(f.account, req.RequestID); !errors.Is(err, ErrInsufficientConfirmation) {
		t.Fatalf("Activate after confirmer revocation: %v, want ErrInsufficientConfirmation", err)
	}
	snap := f.snapshot(t)
	if len(snap.Pending) != 1 {
		t.Fatalf("pending count = %d, want 1", len(snap.Pending))
	}
	if err := f.engine.Cancel(f.account, req.RequestID, "mail-1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	snap = f.snapshot(t)
	if len(snap.Pending) != 0 {
		t.Fatalf("pending count after cancel = %d", len(snap.Pending))
	}
}

func TestReplaceSameMethodIDAvoidsRevokingReplacement(t *testing.T) {
	f := newFixture(t, time.Minute, baseEntries())
	addReq, _ := f.addEmail(t, "mail-new", "v1", "dev-1")
	f.clock.Advance(time.Minute)
	if _, err := f.engine.Activate(f.account, addReq.RequestID); err != nil {
		t.Fatalf("activate add: %v", err)
	}
	before := f.snapshot(t)
	var oldID string
	for _, entry := range before.Entries {
		if entry.MethodID == "mail-new" && entry.Status == StatusActive {
			oldID = entry.ID
		}
	}
	if oldID == "" {
		t.Fatal("added method is not active")
	}

	replace := f.request(t, ActionReplace, Target{MethodID: "mail-new", Kind: MethodEmail, MaterialDigest: "v2"}, "mail-new")
	if _, err := f.engine.Propose(replace, f.sign(t, replace, "dev-1")...); err != nil {
		t.Fatalf("Propose replace: %v", err)
	}
	f.clock.Advance(time.Minute)
	activated, err := f.engine.Activate(f.account, replace.RequestID)
	if err != nil {
		t.Fatalf("Activate replace: %v", err)
	}
	if activated.MethodID != "mail-new" || activated.MaterialDigest != "v2" {
		t.Fatalf("activated replacement = %+v", activated)
	}

	snap := f.snapshot(t)
	active := 0
	for _, entry := range snap.Entries {
		if entry.MethodID != "mail-new" {
			continue
		}
		switch entry.Status {
		case StatusActive:
			active++
			if entry.MaterialDigest != "v2" {
				t.Fatalf("active material = %s, want v2", entry.MaterialDigest)
			}
		case StatusRevoked:
			if entry.ID != oldID {
				t.Fatalf("revoked unexpected entry %s, want %s", entry.ID, oldID)
			}
		}
	}
	if active != 1 {
		t.Fatalf("active entries for same methodID = %d, want 1", active)
	}
	if snap.Generation != 3 {
		t.Fatalf("generation = %d, want 3", snap.Generation)
	}
	recovered, err := f.engine.Recover(f.account, "mail-new")
	if err != nil || recovered.MaterialDigest != "v2" {
		t.Fatalf("Recover replacement: %+v, %v", recovered, err)
	}
}

func TestPasswordResetRevokesPendingSession(t *testing.T) {
	f := newFixture(t, time.Minute, baseEntries())
	req, _ := f.addEmail(t, "mail-new", "new-mail", "dev-1")

	if err := f.engine.ResetPassword(f.account); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if _, err := f.engine.OpenSession(f.account, f.session, ""); err != nil {
		t.Fatalf("OpenSession after reset: %v", err)
	}
	f.clock.Advance(time.Minute)
	if _, err := f.engine.Activate(f.account, req.RequestID); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("Activate after password reset: %v, want ErrSessionRevoked", err)
	}
	if err := f.engine.Cancel(f.account, req.RequestID, "dev-1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
}

func TestConfigChangeAbortsActivation(t *testing.T) {
	f := newFixture(t, time.Minute, baseEntries())
	req, _ := f.addEmail(t, "mail-new", "new-mail", "dev-1")
	f.mutate(t, func(state *AccountState) {
		for i := range state.Entries {
			if state.Entries[i].Status == StatusActive && state.Entries[i].MethodID == "mail-1" {
				state.Entries[i].MaterialDigest = "rotated-out-of-band"
			}
		}
	})
	f.clock.Advance(time.Minute)
	if _, err := f.engine.Activate(f.account, req.RequestID); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("Activate after config change: %v, want ErrConfigChanged", err)
	}
}

func TestActivationInvalidatesOtherPendingChanges(t *testing.T) {
	f := newFixture(t, time.Minute, baseEntries())
	reqA, _ := f.addEmail(t, "mail-a", "a", "dev-1")
	reqB, _ := f.addEmail(t, "mail-b", "b", "dev-1")

	f.clock.Advance(time.Minute)
	if _, err := f.engine.Activate(f.account, reqA.RequestID); err != nil {
		t.Fatalf("Activate A: %v", err)
	}
	if _, err := f.engine.Propose(reqA, f.sign(t, reqA, "dev-1")...); !errors.Is(err, ErrRequestExists) {
		t.Fatalf("replay of activated request: %v, want ErrRequestExists", err)
	}
	snap := f.snapshot(t)
	if snap.Generation != 2 {
		t.Fatalf("generation = %d, want 2", snap.Generation)
	}
	if len(snap.Pending) != 0 {
		t.Fatalf("other pending change survived, pending=%+v", snap.Pending)
	}
	var bStatus Status
	for _, entry := range snap.Entries {
		if entry.ID == reqB.RequestID {
			bStatus = entry.Status
		}
	}
	if bStatus != StatusRevoked {
		t.Fatalf("B status = %s, want Revoked", bStatus)
	}
	if _, err := f.engine.Activate(f.account, reqB.RequestID); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("Activate B after invalidation: %v, want ErrRequestNotFound", err)
	}

	stale := ChangeRequest{
		AccountID:    f.account,
		ConfigDigest: reqB.ConfigDigest,
		SessionID:    f.session,
		Action:       ActionAdd,
		Target:       Target{MethodID: "mail-c", Kind: MethodEmail, MaterialDigest: "c"},
		RequestID:    "stale-request",
	}
	if _, err := f.engine.Propose(stale, f.sign(t, stale, "dev-1")...); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("stale config digest: %v, want ErrConfigChanged", err)
	}
}

func TestConcurrentActivationOnlyOneWins(t *testing.T) {
	f := newFixture(t, time.Minute, baseEntries())
	reqA, _ := f.addEmail(t, "mail-a", "a", "dev-1")
	reqB, _ := f.addEmail(t, "mail-b", "b", "dev-1")
	f.clock.Advance(time.Minute)

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, requestID := range []string{reqA.RequestID, reqB.RequestID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := f.engine.Activate(f.account, id)
			errs <- err
		}(requestID)
	}
	wg.Wait()
	close(errs)

	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrRequestNotFound) && !errors.Is(err, ErrConfigChanged) {
			t.Fatalf("unexpected activation error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful activations = %d, want 1", successes)
	}
	snap := f.snapshot(t)
	if snap.Generation != 2 {
		t.Fatalf("generation = %d, want 2", snap.Generation)
	}
}

type failingStore struct {
	Store
	mu       sync.Mutex
	failNext int
}

func (s *failingStore) Save(state *AccountState) error {
	s.mu.Lock()
	if s.failNext > 0 {
		s.failNext--
		s.mu.Unlock()
		return errors.New("simulated disk full")
	}
	s.mu.Unlock()
	return s.Store.Save(state)
}

func (s *failingStore) failSaves(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = n
}

func TestStorageFailureLeavesStateUnchanged(t *testing.T) {
	memory := NewMemoryStore()
	store := &failingStore{Store: memory}
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

	store.failSaves(1)
	if _, err := engine.Propose(req, confirmation); !errors.Is(err, ErrStorage) {
		t.Fatalf("Propose with failing store: %v, want ErrStorage", err)
	}
	snap, err := engine.Snapshot(account)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Pending) != 0 {
		t.Fatalf("pending change survived failed save: %+v", snap.Pending)
	}

	if _, err := engine.Propose(req, confirmation); err != nil {
		t.Fatalf("Propose after recovery: %v", err)
	}
	clock.Advance(time.Minute)
	store.failSaves(1)
	if _, err := engine.Activate(account, req.RequestID); !errors.Is(err, ErrStorage) {
		t.Fatalf("Activate with failing store: %v, want ErrStorage", err)
	}
	snap, err = engine.Snapshot(account)
	if err != nil {
		t.Fatalf("Snapshot after failed activation: %v", err)
	}
	if snap.Generation != 1 {
		t.Fatalf("generation after failed activation = %d, want 1", snap.Generation)
	}
	if len(snap.Pending) != 1 {
		t.Fatalf("pending count after failed activation = %d, want 1", len(snap.Pending))
	}
	if _, err := engine.Recover(account, "mail-new"); !errors.Is(err, ErrPendingNotUsable) {
		t.Fatalf("Recover after failed activation: %v", err)
	}
	if _, err := engine.Activate(account, req.RequestID); err != nil {
		t.Fatalf("Activate after store recovery: %v", err)
	}
}

func TestDeleteMethodAndLastStrongGuard(t *testing.T) {
	f := newFixture(t, time.Minute, baseEntries())
	req := f.request(t, ActionDelete, Target{MethodID: "mail-1", Kind: MethodEmail}, "")
	if _, err := f.engine.Propose(req, f.sign(t, req, "dev-1")...); err != nil {
		t.Fatalf("Propose delete: %v", err)
	}
	f.clock.Advance(time.Minute)
	if _, err := f.engine.Activate(f.account, req.RequestID); err != nil {
		t.Fatalf("Activate delete: %v", err)
	}
	if _, err := f.engine.Recover(f.account, "mail-1"); !errors.Is(err, ErrMethodNotActive) {
		t.Fatalf("Recover deleted method: %v", err)
	}

	only := newFixture(t, time.Minute, []Entry{
		{MethodID: "dev-only", Kind: MethodDeviceKey, MaterialDigest: "only"},
	})
	deleteLast := only.request(t, ActionDelete, Target{MethodID: "dev-only", Kind: MethodDeviceKey}, "")
	if _, err := only.engine.Propose(deleteLast); !errors.Is(err, ErrLastStrongFactor) {
		t.Fatalf("Propose delete of last strong factor: %v, want ErrLastStrongFactor", err)
	}

	state := &AccountState{
		ID:         "last-strong",
		Generation: 1,
		Entries: []Entry{{
			ID: "only", MethodID: "dev-only", Kind: MethodDeviceKey,
			Status: StatusActive, Strong: true, CreatedGeneration: 1,
		}},
	}
	engine := NewInMemoryEngine()
	err := engine.checkLastStrong(state,
		ChangeRequest{Action: ActionDelete, Target: Target{MethodID: "dev-only", Kind: MethodDeviceKey}},
		state.Entries[0], true)
	if !errors.Is(err, ErrLastStrongFactor) {
		t.Fatalf("delete last strong: %v, want ErrLastStrongFactor", err)
	}
}
