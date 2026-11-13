package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

const testAccount AccountID = "account-1"

var testStart = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)

type testSigner struct {
	id     ContactID
	person PersonID
	pub    ed25519.PublicKey
	priv   ed25519.PrivateKey
	weight uint32
}

func newTestSigner(t *testing.T, id ContactID, person PersonID, weight uint32) testSigner {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return testSigner{id: id, person: person, pub: pub, priv: priv, weight: weight}
}

func (s testSigner) contact() Contact {
	return Contact{
		ContactID: s.id,
		PersonID:  s.person,
		PublicKey: append(ed25519.PublicKey(nil), s.pub...),
		Weight:    s.weight,
	}
}

func (s testSigner) contactWithKey(pub ed25519.PublicKey) Contact {
	contact := s.contact()
	contact.PublicKey = append(ed25519.PublicKey(nil), pub...)
	return contact
}

func createTestAccount(t *testing.T, host *Host, cfg Config, factors []StrongFactor, sessions []SessionID) {
	t.Helper()
	cfg.AccountID = testAccount
	err := host.CreateAccount(Account{
		ID:            testAccount,
		Config:        cfg,
		StrongFactors: factors,
		Sessions:      sessions,
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
}

func startTestRecovery(t *testing.T, host *Host, at time.Time) *Recovery {
	t.Helper()
	recovery, err := host.StartRecovery(testAccount, HashDigest([]byte("target operation")), HashDigest([]byte("initiating device")), at)
	if err != nil {
		t.Fatalf("start recovery: %v", err)
	}
	return recovery
}

func configContact(t *testing.T, host *Host, id ContactID) Contact {
	t.Helper()
	state, err := host.GetAccount(testAccount)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	contact, ok := state.Config.Contact(id)
	if !ok {
		t.Fatalf("contact %q not found", id)
	}
	return contact
}

func approveSigner(t *testing.T, host *Host, recovery *Recovery, signer testSigner, at time.Time) {
	t.Helper()
	contact := configContact(t, host, signer.id)
	signature, err := SignApproval(signer.priv, recovery, contact)
	if err != nil {
		t.Fatalf("sign approval: %v", err)
	}
	if err := host.Approve(testAccount, recovery.ID, signer.id, signature, at); err != nil {
		t.Fatalf("approve %q: %v", signer.id, err)
	}
}

func TestSamePersonContactsDoNotSum(t *testing.T) {
	alice1 := newTestSigner(t, "alice-1", "alice", 3)
	alice2 := newTestSigner(t, "alice-2", "alice", 3)
	bob := newTestSigner(t, "bob", "bob", 3)
	host := NewMemoryHost()
	createTestAccount(t, host, Config{
		Contacts:           []Contact{alice1.contact(), alice2.contact(), bob.contact()},
		Threshold:          5,
		MinDistinctPersons: 2,
		CoolingPeriod:      24 * time.Hour,
	}, nil, nil)

	recovery := startTestRecovery(t, host, testStart)
	approveSigner(t, host, recovery, alice1, testStart.Add(time.Minute))
	approveSigner(t, host, recovery, alice2, testStart.Add(2*time.Minute))

	evaluation, err := host.EvaluateRecovery(testAccount, recovery.ID, testStart.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if evaluation.Weight != 3 || evaluation.DistinctPersons != 1 || evaluation.Eligible {
		t.Fatalf("same-person aliases summed: %+v", evaluation)
	}
	current, _ := host.GetRecovery(testAccount, recovery.ID)
	if current.Status != StatusPending {
		t.Fatalf("aliases unexpectedly entered cooling: %s", current.Status)
	}

	approveSigner(t, host, recovery, bob, testStart.Add(4*time.Minute))
	evaluation, err = host.EvaluateRecovery(testAccount, recovery.ID, testStart.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if evaluation.Weight != 6 || evaluation.DistinctPersons != 2 || !evaluation.Eligible {
		t.Fatalf("unexpected deduplicated total: %+v", evaluation)
	}
}

func TestSamePublicKeyAliasesDoNotSum(t *testing.T) {
	shared := newTestSigner(t, "shared-key-1", "person-a", 3)
	alias := testSigner{id: "shared-key-2", person: "person-b", pub: shared.pub, priv: shared.priv, weight: 3}
	host := NewMemoryHost()
	createTestAccount(t, host, Config{
		Contacts:           []Contact{shared.contact(), alias.contact()},
		Threshold:          5,
		MinDistinctPersons: 2,
		CoolingPeriod:      24 * time.Hour,
	}, nil, nil)

	recovery := startTestRecovery(t, host, testStart)
	approveSigner(t, host, recovery, shared, testStart.Add(time.Minute))
	approveSigner(t, host, recovery, alias, testStart.Add(2*time.Minute))

	evaluation, err := host.EvaluateRecovery(testAccount, recovery.ID, testStart.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if evaluation.Weight != 3 || evaluation.DistinctPersons != 1 || evaluation.Eligible {
		t.Fatalf("same-key aliases were counted separately: %+v", evaluation)
	}
}

func TestPersonAndKeyGroupsMergeTransitively(t *testing.T) {
	first := newTestSigner(t, "first", "person-a", 4)
	shared := testSigner{id: "shared", person: "person-b", pub: first.pub, priv: first.priv, weight: 2}
	third := newTestSigner(t, "third", "person-b", 6)
	host := NewMemoryHost()
	createTestAccount(t, host, Config{
		Contacts:           []Contact{first.contact(), shared.contact(), third.contact()},
		Threshold:          5,
		MinDistinctPersons: 2,
		CoolingPeriod:      24 * time.Hour,
	}, nil, nil)

	recovery := startTestRecovery(t, host, testStart)
	approveSigner(t, host, recovery, first, testStart.Add(time.Minute))
	approveSigner(t, host, recovery, third, testStart.Add(2*time.Minute))
	evaluation, err := host.EvaluateRecovery(testAccount, recovery.ID, testStart.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("evaluate transitive aliases: %v", err)
	}
	if evaluation.Weight != 6 || evaluation.DistinctPersons != 1 || evaluation.Eligible {
		t.Fatalf("transitive alias groups were counted separately: %+v", evaluation)
	}
}

func TestMinDistinctPersonsGateCooling(t *testing.T) {
	one := newTestSigner(t, "one", "person-1", 10)
	two := newTestSigner(t, "two", "person-2", 10)
	three := newTestSigner(t, "three", "person-3", 10)
	host := NewMemoryHost()
	createTestAccount(t, host, Config{
		Contacts:           []Contact{one.contact(), two.contact(), three.contact()},
		Threshold:          10,
		MinDistinctPersons: 3,
		CoolingPeriod:      24 * time.Hour,
	}, nil, nil)

	recovery := startTestRecovery(t, host, testStart)
	approveSigner(t, host, recovery, one, testStart.Add(time.Minute))
	approveSigner(t, host, recovery, two, testStart.Add(2*time.Minute))
	current, _ := host.GetRecovery(testAccount, recovery.ID)
	if current.Status != StatusPending {
		t.Fatalf("cooling started with too few persons: %s", current.Status)
	}
	approveSigner(t, host, recovery, three, testStart.Add(3*time.Minute))
	current, _ = host.GetRecovery(testAccount, recovery.ID)
	if current.Status != StatusCooling {
		t.Fatalf("cooling did not start after third person: %s", current.Status)
	}
}

func TestKeyRotationInvalidatesOldApproval(t *testing.T) {
	contactSigner := newTestSigner(t, "contact", "alice", 5)
	host := NewMemoryHost()
	createTestAccount(t, host, Config{
		Contacts:           []Contact{contactSigner.contact()},
		Threshold:          5,
		MinDistinctPersons: 1,
		CoolingPeriod:      24 * time.Hour,
	}, nil, nil)

	first := startTestRecovery(t, host, testStart)
	approveSigner(t, host, first, contactSigner, testStart.Add(time.Minute))

	newPub, newPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate rotated key: %v", err)
	}
	_, err = host.UpdateConfig(testAccount, func(config *Config) error {
		config.Contacts[0].PublicKey = append(ed25519.PublicKey(nil), newPub...)
		config.Contacts[0].Fingerprint = Fingerprint{}
		return nil
	})
	if err != nil {
		t.Fatalf("rotate contact key: %v", err)
	}
	if _, err := host.CompleteRecovery(testAccount, first.ID, first.CoolingEndsAt); !errors.Is(err, ErrRecoveryInvalidated) {
		t.Fatalf("old recovery survived key change: %v", err)
	}

	second := startTestRecovery(t, host, testStart.Add(2*time.Hour))
	oldContact := contactSigner.contactWithKey(newPub)
	oldContact.Fingerprint = PublicKeyFingerprint(newPub)
	oldSignature, err := SignApproval(contactSigner.priv, second, oldContact)
	if err != nil {
		t.Fatalf("sign old-key approval: %v", err)
	}
	if err := host.Approve(testAccount, second.ID, contactSigner.id, oldSignature, testStart.Add(3*time.Hour)); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("old key approval was accepted: %v", err)
	}
	newSignature, err := SignApproval(newPriv, second, oldContact)
	if err != nil {
		t.Fatalf("sign new-key approval: %v", err)
	}
	if err := host.Approve(testAccount, second.ID, contactSigner.id, newSignature, testStart.Add(3*time.Hour)); err != nil {
		t.Fatalf("new key approval rejected: %v", err)
	}
}

func TestThresholdLoweringAdvancesEpochAndInvalidatesOldRecovery(t *testing.T) {
	signer := newTestSigner(t, "contact", "alice", 3)
	host := NewMemoryHost()
	createTestAccount(t, host, Config{
		Contacts:           []Contact{signer.contact()},
		Threshold:          5,
		MinDistinctPersons: 1,
		CoolingPeriod:      24 * time.Hour,
	}, nil, nil)

	first := startTestRecovery(t, host, testStart)
	firstContact := configContact(t, host, signer.id)
	firstSignature, err := SignApproval(signer.priv, first, firstContact)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := host.Approve(testAccount, first.ID, signer.id, firstSignature, testStart.Add(time.Minute)); err != nil {
		t.Fatalf("approve first: %v", err)
	}

	config, err := host.UpdateConfig(testAccount, func(config *Config) error {
		config.Threshold = 2
		return nil
	})
	if err != nil {
		t.Fatalf("lower threshold: %v", err)
	}
	if config.RecoveryEpoch != 3 {
		t.Fatalf("threshold change did not advance epoch: %d", config.RecoveryEpoch)
	}
	if _, err := host.CompleteRecovery(testAccount, first.ID, first.CoolingEndsAt); !errors.Is(err, ErrRecoveryInvalidated) {
		t.Fatalf("old epoch recovery completed after threshold change: %v", err)
	}
	if err := host.Approve(testAccount, first.ID, signer.id, firstSignature, testStart.Add(2*time.Minute)); !errors.Is(err, ErrRecoveryInvalidated) {
		t.Fatalf("old recovery accepted approval after config change: %v", err)
	}

	second := startTestRecovery(t, host, testStart.Add(3*time.Minute))
	if second.Epoch != 4 {
		t.Fatalf("new recovery used stale epoch: %d", second.Epoch)
	}
	if err := host.Approve(testAccount, second.ID, signer.id, firstSignature, testStart.Add(4*time.Minute)); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("old-epoch signature accepted for new recovery: %v", err)
	}
	approveSigner(t, host, second, signer, testStart.Add(4*time.Minute))
	current, _ := host.GetRecovery(testAccount, second.ID)
	completion, err := host.CompleteRecovery(testAccount, second.ID, current.CoolingEndsAt)
	if err != nil {
		t.Fatalf("complete recovery with lowered threshold: %v", err)
	}
	if completion.NewCredential.Generation != 2 {
		t.Fatalf("credential was not rotated: %+v", completion.NewCredential)
	}
}

func TestVetoAtCoolingBoundary(t *testing.T) {
	contactSigner := newTestSigner(t, "contact", "alice", 5)
	factorPub, factorPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate factor: %v", err)
	}
	host := NewMemoryHost()
	createTestAccount(t, host, Config{
		Contacts:           []Contact{contactSigner.contact()},
		Threshold:          5,
		MinDistinctPersons: 1,
		CoolingPeriod:      24 * time.Hour,
	}, []StrongFactor{{ID: "strong-factor", PublicKey: factorPub}}, nil)

	first := startTestRecovery(t, host, testStart)
	approveSigner(t, host, first, contactSigner, testStart.Add(time.Minute))
	current, _ := host.GetRecovery(testAccount, first.ID)
	if current.Status != StatusCooling {
		t.Fatalf("recovery did not enter cooling: %s", current.Status)
	}
	lastInstant := current.CoolingEndsAt.Add(-time.Nanosecond)
	vetoSignature, err := SignVeto(factorPriv, current, "strong-factor")
	if err != nil {
		t.Fatalf("sign veto: %v", err)
	}
	if err := host.Veto(testAccount, first.ID, "strong-factor", vetoSignature, lastInstant); err != nil {
		t.Fatalf("last-instant veto rejected: %v", err)
	}
	if _, err := host.CompleteRecovery(testAccount, first.ID, current.CoolingEndsAt); !errors.Is(err, ErrRecoveryVetoed) {
		t.Fatalf("vetoed recovery completed: %v", err)
	}

	secondStart := current.CoolingEndsAt.Add(time.Minute)
	second := startTestRecovery(t, host, secondStart)
	approveSigner(t, host, second, contactSigner, secondStart.Add(time.Minute))
	current, _ = host.GetRecovery(testAccount, second.ID)
	vetoSignature, err = SignVeto(factorPriv, current, "strong-factor")
	if err != nil {
		t.Fatalf("sign boundary veto: %v", err)
	}
	if err := host.Veto(testAccount, second.ID, "strong-factor", vetoSignature, current.CoolingEndsAt); !errors.Is(err, ErrVetoNotAllowed) {
		t.Fatalf("veto at cooling end was allowed: %v", err)
	}
	if _, err := host.CompleteRecovery(testAccount, second.ID, current.CoolingEndsAt); err != nil {
		t.Fatalf("completion at cooling end failed: %v", err)
	}
}

func TestCompletionWriteFailureIsAtomicAndIdempotent(t *testing.T) {
	contactSigner := newTestSigner(t, "contact", "alice", 5)
	store := NewMemoryStore()
	host := NewHost(store)
	createTestAccount(t, host, Config{
		Contacts:           []Contact{contactSigner.contact()},
		Threshold:          5,
		MinDistinctPersons: 1,
		CoolingPeriod:      24 * time.Hour,
	}, nil, []SessionID{"session-1", "session-2"})

	recovery := startTestRecovery(t, host, testStart)
	approveSigner(t, host, recovery, contactSigner, testStart.Add(time.Minute))
	current, _ := host.GetRecovery(testAccount, recovery.ID)

	store.FailNextCommit(errors.New("injected disk failure"))
	if _, err := host.CompleteRecovery(testAccount, recovery.ID, current.CoolingEndsAt); err == nil {
		t.Fatalf("completion unexpectedly succeeded despite commit failure")
	}
	state, _ := host.GetAccount(testAccount)
	recoveryState := state.Recoveries[recovery.ID]
	if recoveryState.Status != StatusCooling {
		t.Fatalf("partial completion changed recovery status: %s", recoveryState.Status)
	}
	if state.Credential.Generation != 1 || len(state.Sessions) != 2 {
		t.Fatalf("partial completion leaked credential/session changes: %+v sessions=%d", state.Credential, len(state.Sessions))
	}

	completion, err := host.CompleteRecovery(testAccount, recovery.ID, current.CoolingEndsAt)
	if err != nil {
		t.Fatalf("retry completion: %v", err)
	}
	state, _ = host.GetAccount(testAccount)
	if state.Credential.Generation != 2 || len(state.Sessions) != 0 {
		t.Fatalf("retry did not rotate credential and revoke sessions: %+v sessions=%d", state.Credential, len(state.Sessions))
	}
	if state.Recoveries[recovery.ID].Status != StatusCompleted || state.Config.RecoveryEpoch != completion.NewEpoch {
		t.Fatalf("completion was not recorded atomically: %+v", state.Recoveries[recovery.ID])
	}

	if _, err := host.CompleteRecovery(testAccount, recovery.ID, current.CoolingEndsAt.Add(time.Hour)); err != nil {
		t.Fatalf("idempotent completion retry: %v", err)
	}
	stateAgain, _ := host.GetAccount(testAccount)
	if stateAgain.Credential.Generation != 2 {
		t.Fatalf("idempotent retry rotated credential again: %+v", stateAgain.Credential)
	}
}

func TestCompletionPersistedBeforeErrorDoesNotRotateTwice(t *testing.T) {
	contactSigner := newTestSigner(t, "contact", "alice", 5)
	store := NewMemoryStore()
	host := NewHost(store)
	createTestAccount(t, host, Config{
		Contacts:           []Contact{contactSigner.contact()},
		Threshold:          5,
		MinDistinctPersons: 1,
		CoolingPeriod:      24 * time.Hour,
	}, nil, []SessionID{"session-1"})

	recovery := startTestRecovery(t, host, testStart)
	approveSigner(t, host, recovery, contactSigner, testStart.Add(time.Minute))
	current, _ := host.GetRecovery(testAccount, recovery.ID)

	store.FailNextCommitAfterPersist(errors.New("response lost after durable write"))
	if _, err := host.CompleteRecovery(testAccount, recovery.ID, current.CoolingEndsAt); err == nil {
		t.Fatalf("completion unexpectedly reported success despite injected error")
	}
	persisted, _ := host.GetAccount(testAccount)
	if persisted.Recoveries[recovery.ID].Status != StatusCompleted {
		t.Fatalf("durable completion was lost: %s", persisted.Recoveries[recovery.ID].Status)
	}
	firstGeneration := persisted.Credential.Generation
	firstCredential := persisted.Credential.ID
	firstVersion := persisted.Version

	completion, err := host.CompleteRecovery(testAccount, recovery.ID, current.CoolingEndsAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("retry after uncertain commit: %v", err)
	}
	retried, _ := host.GetAccount(testAccount)
	if retried.Credential.Generation != firstGeneration || retried.Credential.ID != firstCredential {
		t.Fatalf("credential rotated twice after crash: first=%+v retried=%+v", persisted.Credential, retried.Credential)
	}
	if retried.Version != firstVersion {
		t.Fatalf("idempotent retry rewrote state: version %d -> %d", firstVersion, retried.Version)
	}
	if completion.NewCredential.ID != firstCredential {
		t.Fatalf("retry returned a different completion: %+v", completion.NewCredential)
	}
}

func TestRevocationAndTargetChangeInvalidateOldEpoch(t *testing.T) {
	contactSigner := newTestSigner(t, "contact", "alice", 5)
	host := NewMemoryHost()
	createTestAccount(t, host, Config{
		Contacts:           []Contact{contactSigner.contact()},
		Threshold:          5,
		MinDistinctPersons: 1,
		CoolingPeriod:      24 * time.Hour,
	}, nil, nil)

	recovery := startTestRecovery(t, host, testStart)
	approveSigner(t, host, recovery, contactSigner, testStart.Add(time.Minute))
	if err := host.RevokeContact(testAccount, contactSigner.id); err != nil {
		t.Fatalf("revoke contact: %v", err)
	}
	if _, err := host.CompleteRecovery(testAccount, recovery.ID, recovery.CoolingEndsAt); !errors.Is(err, ErrRecoveryInvalidated) {
		t.Fatalf("revocation did not invalidate recovery: %v", err)
	}
	second := startTestRecovery(t, host, testStart.Add(2*time.Hour))
	contact := configContact(t, host, contactSigner.id)
	signature, err := SignApproval(contactSigner.priv, second, contact)
	if err != nil {
		t.Fatalf("sign after revoke: %v", err)
	}
	if err := host.Approve(testAccount, second.ID, contactSigner.id, signature, testStart.Add(3*time.Hour)); !errors.Is(err, ErrContactRevoked) {
		t.Fatalf("revoked contact approved recovery: %v", err)
	}

	thirdSigner := newTestSigner(t, "third", "bob", 5)
	if _, err := host.UpdateConfig(testAccount, func(config *Config) error {
		config.Contacts = []Contact{thirdSigner.contact()}
		return nil
	}); err != nil {
		t.Fatalf("replace contact: %v", err)
	}
	third := startTestRecovery(t, host, testStart.Add(4*time.Hour))
	approveSigner(t, host, third, thirdSigner, testStart.Add(5*time.Hour))
	newConfig, err := host.ChangeTarget(testAccount, third.ID, HashDigest([]byte("different target")), testStart.Add(6*time.Hour))
	if err != nil {
		t.Fatalf("change target: %v", err)
	}
	if newConfig.RecoveryEpoch != third.Epoch+1 {
		t.Fatalf("target change did not advance epoch: %d", newConfig.RecoveryEpoch)
	}
	if _, err := host.CompleteRecovery(testAccount, third.ID, third.CoolingEndsAt); !errors.Is(err, ErrRecoveryInvalidated) {
		t.Fatalf("target change did not invalidate approvals: %v", err)
	}
}

func TestRemovedContactCannotContinueApprovals(t *testing.T) {
	contactSigner := newTestSigner(t, "contact", "alice", 5)
	host := NewMemoryHost()
	createTestAccount(t, host, Config{
		Contacts:           []Contact{contactSigner.contact()},
		Threshold:          5,
		MinDistinctPersons: 1,
		CoolingPeriod:      24 * time.Hour,
	}, nil, nil)

	first := startTestRecovery(t, host, testStart)
	approveSigner(t, host, first, contactSigner, testStart.Add(time.Minute))
	if _, err := host.RemoveContact(testAccount, contactSigner.id); err != nil {
		t.Fatalf("remove contact: %v", err)
	}
	if _, err := host.CompleteRecovery(testAccount, first.ID, first.CoolingEndsAt); !errors.Is(err, ErrRecoveryInvalidated) {
		t.Fatalf("removal did not invalidate old recovery: %v", err)
	}
	second := startTestRecovery(t, host, testStart.Add(2*time.Hour))
	oldContact := contactSigner.contact()
	oldContact.Fingerprint = PublicKeyFingerprint(contactSigner.pub)
	signature, err := SignApproval(contactSigner.priv, second, oldContact)
	if err != nil {
		t.Fatalf("sign removed contact approval: %v", err)
	}
	if err := host.Approve(testAccount, second.ID, contactSigner.id, signature, testStart.Add(3*time.Hour)); !errors.Is(err, ErrContactNotFound) {
		t.Fatalf("removed contact still counted: %v", err)
	}
}

func TestDeadlineExpiresPendingRecovery(t *testing.T) {
	contactSigner := newTestSigner(t, "contact", "alice", 5)
	host := NewMemoryHost()
	createTestAccount(t, host, Config{
		Contacts:           []Contact{contactSigner.contact()},
		Threshold:          5,
		MinDistinctPersons: 1,
		CoolingPeriod:      24 * time.Hour,
		ApprovalWindow:     time.Hour,
	}, nil, nil)

	recovery := startTestRecovery(t, host, testStart)
	late := testStart.Add(2 * time.Hour)
	contact := configContact(t, host, contactSigner.id)
	signature, err := SignApproval(contactSigner.priv, recovery, contact)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := host.Approve(testAccount, recovery.ID, contactSigner.id, signature, late); !errors.Is(err, ErrRecoveryExpired) {
		t.Fatalf("late approval accepted: %v", err)
	}
	current, _ := host.GetRecovery(testAccount, recovery.ID)
	if current.Status != StatusExpired {
		t.Fatalf("expiry was not persisted: %s", current.Status)
	}
	if _, err := host.CompleteRecovery(testAccount, recovery.ID, late); !errors.Is(err, ErrRecoveryExpired) {
		t.Fatalf("expired recovery completed: %v", err)
	}
}

func TestLateApprovalDoesNotExpireCoolingRecovery(t *testing.T) {
	first := newTestSigner(t, "first", "alice", 5)
	second := newTestSigner(t, "second", "bob", 5)
	host := NewMemoryHost()
	createTestAccount(t, host, Config{
		Contacts:           []Contact{first.contact(), second.contact()},
		Threshold:          5,
		MinDistinctPersons: 1,
		CoolingPeriod:      24 * time.Hour,
		ApprovalWindow:     time.Hour,
	}, nil, nil)

	recovery := startTestRecovery(t, host, testStart)
	approveSigner(t, host, recovery, first, testStart.Add(time.Minute))
	lateContact := configContact(t, host, second.id)
	signature, err := SignApproval(second.priv, recovery, lateContact)
	if err != nil {
		t.Fatalf("sign late approval: %v", err)
	}
	if err := host.Approve(testAccount, recovery.ID, second.id, signature, testStart.Add(2*time.Hour)); !errors.Is(err, ErrRecoveryExpired) {
		t.Fatalf("late cooling approval accepted: %v", err)
	}
	current, _ := host.GetRecovery(testAccount, recovery.ID)
	if current.Status != StatusCooling {
		t.Fatalf("late approval changed cooling state to %s", current.Status)
	}
	if _, err := host.CompleteRecovery(testAccount, recovery.ID, current.CoolingEndsAt); err != nil {
		t.Fatalf("cooling recovery no longer completable after late approval: %v", err)
	}
}

func TestContactEpochWindowIsEnforced(t *testing.T) {
	contactSigner := newTestSigner(t, "contact", "alice", 5)
	host := NewMemoryHost()
	contact := contactSigner.contact()
	contact.StartEpoch = 3
	createTestAccount(t, host, Config{
		Contacts:           []Contact{contact},
		Threshold:          5,
		MinDistinctPersons: 1,
		CoolingPeriod:      24 * time.Hour,
	}, nil, nil)

	recovery := startTestRecovery(t, host, testStart)
	if recovery.Epoch != 2 {
		t.Fatalf("unexpected first recovery epoch: %d", recovery.Epoch)
	}
	contact = configContact(t, host, contactSigner.id)
	signature, err := SignApproval(contactSigner.priv, recovery, contact)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := host.Approve(testAccount, recovery.ID, contactSigner.id, signature, testStart.Add(time.Minute)); !errors.Is(err, ErrContactOutOfEpoch) {
		t.Fatalf("contact approved outside epoch window: %v", err)
	}
}

func makeCandidateApproval(t *testing.T, signer testSigner, contact Contact, recovery Recovery, epoch uint64, received time.Time) Approval {
	t.Helper()
	message := ApprovalSigningBytes(
		recovery.AccountID,
		recovery.ID,
		epoch,
		recovery.TargetDigest,
		recovery.DeviceDigest,
		recovery.Deadline.UnixNano(),
		contact.ContactID,
		contact.Fingerprint,
	)
	return Approval{
		ContactID:          contact.ContactID,
		ContactFingerprint: contact.Fingerprint,
		AccountID:          recovery.AccountID,
		RecoveryID:         recovery.ID,
		Epoch:              epoch,
		TargetDigest:       recovery.TargetDigest,
		DeviceDigest:       recovery.DeviceDigest,
		Deadline:           recovery.Deadline,
		Signature:          ed25519.Sign(signer.priv, message),
		ReceivedAt:         received,
	}
}

func TestDifferentialSearchFindsMinimalAliasSet(t *testing.T) {
	alice1 := newTestSigner(t, "alice-1", "alice", 3)
	alice2 := newTestSigner(t, "alice-2", "alice", 3)
	host := NewMemoryHost()
	createTestAccount(t, host, Config{
		Contacts:           []Contact{alice1.contact(), alice2.contact()},
		Threshold:          5,
		MinDistinctPersons: 1,
		CoolingPeriod:      24 * time.Hour,
	}, nil, nil)
	recovery := startTestRecovery(t, host, testStart)

	firstContact := configContact(t, host, alice1.id)
	secondContact := configContact(t, host, alice2.id)
	candidates := []Approval{
		makeCandidateApproval(t, alice1, firstContact, *recovery, recovery.Epoch, testStart.Add(time.Minute)),
		makeCandidateApproval(t, alice2, secondContact, *recovery, recovery.Epoch, testStart.Add(2*time.Minute)),
	}
	now := testStart.Add(2*time.Minute + 25*time.Hour)
	divergence, err := FindMinimalPendingDedupEpochDivergence(configContactConfig(t, host), *recovery, candidates, now)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(divergence.Approvals) != 2 {
		t.Fatalf("expected both aliases in minimal witness, got %d", len(divergence.Approvals))
	}
	if divergence.Correct.Eligible || divergence.Correct.CanComplete {
		t.Fatalf("correct model accepted alias inflation: %+v", divergence.Correct)
	}
	if !divergence.Pending.CanComplete {
		t.Fatalf("pending model did not complete: %+v", divergence.Pending)
	}
}

func TestDifferentialSearchFindsMinimalStaleEpochSet(t *testing.T) {
	contactSigner := newTestSigner(t, "contact", "alice", 5)
	host := NewMemoryHost()
	createTestAccount(t, host, Config{
		Contacts:           []Contact{contactSigner.contact()},
		Threshold:          5,
		MinDistinctPersons: 1,
		CoolingPeriod:      24 * time.Hour,
	}, nil, nil)
	recovery := startTestRecovery(t, host, testStart)
	contact := configContact(t, host, contactSigner.id)
	stale := makeCandidateApproval(t, contactSigner, contact, *recovery, recovery.Epoch-1, testStart.Add(time.Minute))

	now := testStart.Add(25 * time.Hour)
	divergence, err := FindMinimalDivergence(configContactConfig(t, host), *recovery, []Approval{stale}, now, PendingNoEpochPolicy)
	if err != nil {
		t.Fatalf("search stale epoch: %v", err)
	}
	if len(divergence.Approvals) != 1 {
		t.Fatalf("minimal stale-epoch witness should have one item, got %d", len(divergence.Approvals))
	}
	if divergence.Correct.Eligible || divergence.Correct.CanComplete {
		t.Fatalf("correct model accepted stale epoch: %+v", divergence.Correct)
	}
	if !divergence.Pending.CanComplete {
		t.Fatalf("pending model did not accept stale epoch: %+v", divergence.Pending)
	}
}

func configContactConfig(t *testing.T, host *Host) Config {
	t.Helper()
	state, err := host.GetAccount(testAccount)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	return state.Config
}
