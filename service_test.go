package netsec

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

func TestResetCommitRotatesEpochsAndRevokesAllSessions(t *testing.T) {
	clock := newFakeClock()
	alarms := &alarmSink{}
	service := newTestService(t, nil, clock, alarms, nil)
	createTestAccount(t, service, "alice", emailPolicy())

	pair := login(t, service, "alice")
	if _, err := service.VerifyAccessToken(context.Background(), pair.AccessToken); err != nil {
		t.Fatalf("fresh access token: %v", err)
	}
	transaction := beginReset(t, service, "alice", "reset-1", "email")
	prepared := prepare(t, service, testNewPassword)

	receipt, err := service.CommitReset(context.Background(), transaction, prepared)
	if err != nil {
		t.Fatalf("CommitReset: %v", err)
	}
	if receipt.CredentialEpoch != 1 || receipt.SessionEpoch != 1 {
		t.Fatalf("receipt epochs = %d/%d, want 1/1", receipt.CredentialEpoch, receipt.SessionEpoch)
	}
	if receipt.Replayed {
		t.Fatal("first commit must not be marked replayed")
	}
	if len(receipt.RevokedFamilyIDs) != 1 || receipt.RevokedFamilyIDs[0] != pair.FamilyID {
		t.Fatalf("revoked families = %v, want [%s]", receipt.RevokedFamilyIDs, pair.FamilyID)
	}

	account, ok := service.Store().GetAccount("alice")
	if !ok {
		t.Fatal("account disappeared")
	}
	if account.CredentialEpoch != 1 || account.SessionEpoch != 1 {
		t.Fatalf("account epochs = %d/%d, want 1/1", account.CredentialEpoch, account.SessionEpoch)
	}
	if _, err := service.signer.verify(pair.AccessToken); err != nil {
		t.Fatalf("old token signature should still verify: %v", err)
	}
	if _, err := service.VerifyAccessToken(context.Background(), pair.AccessToken); !errors.Is(err, ErrTokenEpochStale) {
		t.Fatalf("old access token error = %v, want stale epoch", err)
	}
	if _, err := service.RefreshTokens(context.Background(), pair.RefreshToken); !errors.Is(err, ErrTokenEpochStale) {
		t.Fatalf("old refresh token error = %v, want stale epoch", err)
	}
	family, ok := service.Store().GetRefreshFamily(pair.FamilyID)
	if !ok || !family.Revoked {
		t.Fatalf("family = %+v, want revoked", family)
	}
	if _, err := service.BeginLogin(context.Background(), BeginLoginRequest{AccountID: "alice", Password: testOldPassword}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password login error = %v", err)
	}
	newLogin, err := service.BeginLogin(context.Background(), BeginLoginRequest{AccountID: "alice", Password: testNewPassword})
	if err != nil {
		t.Fatalf("new password login: %v", err)
	}
	newPair, err := service.CompleteLogin(context.Background(), newLogin.ID)
	if err != nil {
		t.Fatalf("new login completion: %v", err)
	}
	if _, err := service.VerifyAccessToken(context.Background(), newPair.AccessToken); err != nil {
		t.Fatalf("new access token: %v", err)
	}
	if len(alarms.all()) != 0 {
		t.Fatalf("unexpected alarms: %+v", alarms.all())
	}
}

func TestResetReplayReturnsOriginalReceiptAndRejectsDifferentPassword(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(t, nil, clock, nil, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	login(t, service, "alice")
	transaction := beginReset(t, service, "alice", "reset-replay", "email")
	prepared := prepare(t, service, testNewPassword)
	first, err := service.CommitReset(context.Background(), transaction, prepared)
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}

	// Simulate a lost response: the caller re-prepares the same password.
	replayed, err := service.CommitReset(context.Background(), transaction, prepare(t, service, testNewPassword))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replayed.Replayed {
		t.Fatal("replay was not marked")
	}
	if replayed.CredentialEpoch != first.CredentialEpoch ||
		replayed.SessionEpoch != first.SessionEpoch ||
		!replayed.CommittedAt.Equal(first.CommittedAt) {
		t.Fatalf("replay receipt = %+v, want %+v", replayed, first)
	}
	account, _ := service.Store().GetAccount("alice")
	if account.CredentialEpoch != 1 || account.SessionEpoch != 1 {
		t.Fatalf("replay bumped epochs to %d/%d", account.CredentialEpoch, account.SessionEpoch)
	}
	byID, ok := service.LookupReceipt("reset-replay")
	if !ok || byID.SessionEpoch != first.SessionEpoch || byID.CredentialEpoch != first.CredentialEpoch {
		t.Fatalf("LookupReceipt = %+v, %v", byID, ok)
	}
	byIDAgain, err := service.CommitResetByID(context.Background(), "reset-replay", prepare(t, service, testNewPassword))
	if err != nil || !byIDAgain.Replayed || byIDAgain.SessionEpoch != first.SessionEpoch {
		t.Fatalf("CommitResetByID = %+v, %v", byIDAgain, err)
	}

	before := passwordHashDigest(account.PasswordHash)
	if _, err := service.CommitReset(context.Background(), transaction, prepare(t, service, "different-password-789")); !errors.Is(err, ErrResetIDReuse) {
		t.Fatalf("different password error = %v, want reset ID reuse", err)
	}
	account, _ = service.Store().GetAccount("alice")
	if passwordHashDigest(account.PasswordHash) != before {
		t.Fatal("different-password retry changed the hash")
	}
	if account.CredentialEpoch != 1 || account.SessionEpoch != 1 {
		t.Fatal("different-password retry bumped epochs")
	}
}

func TestResetReplaySurvivesServiceRestartWithoutDoubleEpoch(t *testing.T) {
	clock := newFakeClock()
	store := NewMemoryStore()
	signer := testSigner(t)
	firstService := newTestService(t, store, clock, nil, signer)
	createTestAccount(t, firstService, "alice", emailPolicy())
	login(t, firstService, "alice")
	transaction := beginReset(t, firstService, "alice", "reset-restart", "email")
	prepared := prepare(t, firstService, testNewPassword)
	first, err := firstService.CommitReset(context.Background(), transaction, prepared)
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}

	// The response is lost and the process restarts over the same durable store.
	secondService := newTestService(t, store, clock, nil, signer)
	restarted, state, err := secondService.LookupReset("reset-restart")
	if err != nil {
		t.Fatalf("LookupReset: %v", err)
	}
	if state != ResetCommitted {
		t.Fatalf("state = %v, want committed", state)
	}
	replayed, err := secondService.CommitReset(context.Background(), restarted, prepare(t, secondService, testNewPassword))
	if err != nil {
		t.Fatalf("restart replay: %v", err)
	}
	if !replayed.Replayed || replayed.SessionEpoch != first.SessionEpoch || replayed.CredentialEpoch != first.CredentialEpoch {
		t.Fatalf("restart receipt = %+v, first = %+v", replayed, first)
	}
	account, _ := store.GetAccount("alice")
	if account.CredentialEpoch != 1 || account.SessionEpoch != 1 {
		t.Fatalf("restart replay bumped epochs to %d/%d", account.CredentialEpoch, account.SessionEpoch)
	}
}

func TestPolicyTighteningIsReevaluatedAtCommit(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(t, nil, clock, nil, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	pair := login(t, service, "alice")
	transaction := beginReset(t, service, "alice", "reset-policy", "email")
	digest, err := service.SetRecoveryPolicy("alice", emailTOTPPolicy())
	if err != nil {
		t.Fatalf("SetRecoveryPolicy: %v", err)
	}
	if transaction.StartRecoveryPolicyDigest == digest {
		t.Fatal("transaction start policy should be the old policy")
	}

	if _, err := service.CommitReset(context.Background(), transaction, prepare(t, service, testNewPassword)); !errors.Is(err, ErrRecoveryPolicyUnsatisfied) {
		t.Fatalf("commit error = %v, want policy unsatisfied", err)
	}
	account, _ := service.Store().GetAccount("alice")
	if account.CredentialEpoch != 0 || account.SessionEpoch != 0 {
		t.Fatalf("failed policy check bumped epochs to %d/%d", account.CredentialEpoch, account.SessionEpoch)
	}
	if _, err := service.VerifyAccessToken(context.Background(), pair.AccessToken); err != nil {
		t.Fatalf("failed reset invalidated an old access token: %v", err)
	}
	info, _ := service.Store().GetReset("reset-policy")
	if info.State != ResetPending {
		t.Fatalf("state = %v, want pending", info.State)
	}

	second := beginReset(t, service, "alice", "reset-policy-2", "email+totp")
	receipt, err := service.CommitReset(context.Background(), second, prepare(t, service, testNewPassword))
	if err != nil {
		t.Fatalf("second commit: %v", err)
	}
	if receipt.PolicyDigest != digest {
		t.Fatalf("receipt policy = %s, want %s", receipt.PolicyDigest, digest)
	}
}

func TestPolicyTighteningAllowsProofThatSatisfiesCurrentPolicy(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(t, nil, clock, nil, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	transaction := beginReset(t, service, "alice", "reset-policy-sufficient", "email+totp")
	digest, err := service.SetRecoveryPolicy("alice", emailTOTPPolicy())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := service.CommitReset(context.Background(), transaction, prepare(t, service, testNewPassword))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if receipt.PolicyDigest != digest {
		t.Fatalf("receipt policy = %s, want %s", receipt.PolicyDigest, digest)
	}
}

type toggleHasher struct {
	mu   sync.Mutex
	fail bool
}

func (h *toggleHasher) setFail(fail bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fail = fail
}

func (h *toggleHasher) Hash(ctx context.Context, password []byte, params PasswordParams) (PasswordHash, error) {
	h.mu.Lock()
	fail := h.fail
	h.mu.Unlock()
	if fail {
		return PasswordHash{}, errors.New("injected hash failure")
	}
	return PBKDF2Hasher{}.Hash(ctx, password, params)
}

func (h *toggleHasher) Verify(ctx context.Context, password []byte, hash PasswordHash) (bool, error) {
	return PBKDF2Hasher{}.Verify(ctx, password, hash)
}

type blockingVerifyHasher struct {
	mu      sync.Mutex
	block   bool
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (h *blockingVerifyHasher) setBlock(block bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.block = block
}

func (h *blockingVerifyHasher) Hash(ctx context.Context, password []byte, params PasswordParams) (PasswordHash, error) {
	return PBKDF2Hasher{}.Hash(ctx, password, params)
}

func (h *blockingVerifyHasher) Verify(ctx context.Context, password []byte, hash PasswordHash) (bool, error) {
	h.mu.Lock()
	block := h.block
	h.mu.Unlock()
	if block {
		h.once.Do(func() { close(h.entered) })
		<-h.release
	}
	return PBKDF2Hasher{}.Verify(ctx, password, hash)
}

func TestPasswordHashFailureHasZeroEffect(t *testing.T) {
	clock := newFakeClock()
	hasher := &toggleHasher{}
	service, err := NewService(Config{
		Store:    NewMemoryStore(),
		Verifier: testVerifier,
		Hasher:   hasher,
		Policy:   testPolicy(),
		Params:   testParams(),
		Signer:   testSigner(t),
		Lifetime: TokenLifetime{Access: time.Hour, Refresh: 24 * time.Hour},
		Clock:    clock,
		Rand:     &sequenceReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	createTestAccount(t, service, "alice", emailPolicy())
	pair := login(t, service, "alice")
	transaction := beginReset(t, service, "alice", "reset-hash-fail", "email")
	before, _ := service.Store().GetAccount("alice")
	beforeDigest := passwordHashDigest(before.PasswordHash)

	hasher.setFail(true)
	if _, err := service.PreparePassword(context.Background(), testNewPassword, testParams()); err == nil {
		t.Fatal("PreparePassword succeeded with failing hasher")
	}
	if _, err := service.CommitReset(context.Background(), transaction, nil); err == nil {
		t.Fatal("CommitReset accepted a nil prepared password")
	}
	after, _ := service.Store().GetAccount("alice")
	if passwordHashDigest(after.PasswordHash) != beforeDigest {
		t.Fatal("hash failure changed the stored password")
	}
	if after.CredentialEpoch != 0 || after.SessionEpoch != 0 {
		t.Fatalf("hash failure bumped epochs to %d/%d", after.CredentialEpoch, after.SessionEpoch)
	}
	family, _ := service.Store().GetRefreshFamily(pair.FamilyID)
	if family.Revoked {
		t.Fatal("hash failure revoked the refresh family")
	}
}

func TestSessionEpochMaximumIsRejectedAndAlarmedWithoutWrap(t *testing.T) {
	clock := newFakeClock()
	alarms := &alarmSink{}
	service := newTestService(t, nil, clock, alarms, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	account := service.Store().accounts["alice"]
	service.Store().mu.Lock()
	account.SessionEpoch = math.MaxUint64
	service.Store().mu.Unlock()
	transaction := beginReset(t, service, "alice", "reset-max", "email")
	before := passwordHashDigest(account.PasswordHash)

	if _, err := service.CommitReset(context.Background(), transaction, prepare(t, service, testNewPassword)); !errors.Is(err, ErrEpochExhausted) {
		t.Fatalf("commit error = %v, want epoch exhausted", err)
	}
	service.Store().mu.Lock()
	session := account.SessionEpoch
	credential := account.CredentialEpoch
	after := passwordHashDigest(account.PasswordHash)
	service.Store().mu.Unlock()
	if session != math.MaxUint64 || credential != 0 {
		t.Fatalf("epochs wrapped to %d/%d", credential, session)
	}
	if after != before {
		t.Fatal("max-epoch rejection changed the password")
	}
	all := alarms.all()
	if len(all) != 1 || all[0].Code != AlarmSessionEpochExhausted {
		t.Fatalf("alarms = %+v, want session epoch alarm", all)
	}
}

func TestCredentialEpochMaximumIsRejectedAndAlarmedWithoutWrap(t *testing.T) {
	clock := newFakeClock()
	alarms := &alarmSink{}
	service := newTestService(t, nil, clock, alarms, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	service.Store().mu.Lock()
	account := service.Store().accounts["alice"]
	account.CredentialEpoch = math.MaxUint64
	service.Store().mu.Unlock()
	transaction := beginReset(t, service, "alice", "reset-ce-max", "email")

	if _, err := service.CommitReset(context.Background(), transaction, prepare(t, service, testNewPassword)); !errors.Is(err, ErrEpochExhausted) {
		t.Fatalf("commit error = %v, want epoch exhausted", err)
	}
	service.Store().mu.Lock()
	credential := account.CredentialEpoch
	session := account.SessionEpoch
	service.Store().mu.Unlock()
	if credential != math.MaxUint64 || session != 0 {
		t.Fatalf("epochs wrapped to %d/%d", credential, session)
	}
	all := alarms.all()
	if len(all) != 1 || all[0].Code != AlarmCredentialEpochExhausted {
		t.Fatalf("alarms = %+v, want credential epoch alarm", all)
	}
}

func TestSessionEpochCanReachMaximumWithoutWrapping(t *testing.T) {
	clock := newFakeClock()
	alarms := &alarmSink{}
	service := newTestService(t, nil, clock, alarms, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	service.Store().mu.Lock()
	service.Store().accounts["alice"].SessionEpoch = math.MaxUint64 - 1
	service.Store().mu.Unlock()
	transaction := beginReset(t, service, "alice", "reset-max-minus-one", "email")
	receipt, err := service.CommitReset(context.Background(), transaction, prepare(t, service, testNewPassword))
	if err != nil {
		t.Fatalf("commit at max-1: %v", err)
	}
	if receipt.SessionEpoch != math.MaxUint64 {
		t.Fatalf("session epoch = %d, want max", receipt.SessionEpoch)
	}
	blocked := beginReset(t, service, "alice", "reset-after-max", "email")
	if _, err := service.CommitReset(context.Background(), blocked, prepare(t, service, "another-password-999")); !errors.Is(err, ErrEpochExhausted) {
		t.Fatalf("reset at max error = %v, want epoch exhausted", err)
	}
	if len(alarms.all()) != 1 || alarms.all()[0].Code != AlarmSessionEpochExhausted {
		t.Fatalf("alarms = %+v", alarms.all())
	}
}

func TestSessionEpochUsesFullUint64InTokens(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(t, nil, clock, nil, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	const start = uint64(1)<<60 + 17
	service.Store().mu.Lock()
	service.Store().accounts["alice"].SessionEpoch = start
	service.Store().accounts["alice"].CredentialEpoch = start / 2
	service.Store().mu.Unlock()

	pair := login(t, service, "alice")
	claims, err := service.VerifyAccessToken(context.Background(), pair.AccessToken)
	if err != nil {
		t.Fatalf("access token with large epoch: %v", err)
	}
	if claims.SessionEpoch != start {
		t.Fatalf("token session epoch = %d, want %d", claims.SessionEpoch, start)
	}
	transaction := beginReset(t, service, "alice", "reset-large-epoch", "email")
	receipt, err := service.CommitReset(context.Background(), transaction, prepare(t, service, testNewPassword))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if receipt.SessionEpoch != start+1 {
		t.Fatalf("receipt session epoch = %d, want %d", receipt.SessionEpoch, start+1)
	}
	if _, err := service.VerifyAccessToken(context.Background(), pair.AccessToken); !errors.Is(err, ErrTokenEpochStale) {
		t.Fatalf("old large-epoch token error = %v, want stale", err)
	}
}

func TestRefreshTokensAreSingleUseAndRotateGeneration(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(t, nil, clock, nil, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	pair := login(t, service, "alice")

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]TokenPair, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = service.RefreshTokens(context.Background(), pair.RefreshToken)
		}(i)
	}
	close(start)
	wg.Wait()
	successes := 0
	for i := 0; i < 2; i++ {
		if errs[i] == nil {
			successes++
		} else if !errors.Is(errs[i], ErrTokenReused) {
			t.Fatalf("loser error = %v, want token reused", errs[i])
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want exactly 1", successes)
	}
	family, _ := service.Store().GetRefreshFamily(pair.FamilyID)
	if family.Generation != 2 {
		t.Fatalf("generation = %d, want 2", family.Generation)
	}
	if _, err := service.RefreshTokens(context.Background(), pair.RefreshToken); !errors.Is(err, ErrTokenReused) {
		t.Fatalf("old refresh token error = %v, want reused", err)
	}
	var winner TokenPair
	for i := range results {
		if errs[i] == nil {
			winner = results[i]
		}
	}
	next, err := service.RefreshTokens(context.Background(), winner.RefreshToken)
	if err != nil {
		t.Fatalf("new refresh token: %v", err)
	}
	if next.Generation != 3 {
		t.Fatalf("next generation = %d, want 3", next.Generation)
	}
	if _, err := service.VerifyAccessToken(context.Background(), winner.AccessToken); err != nil {
		t.Fatalf("rotated access token: %v", err)
	}
}

func TestConcurrentResetAndRefreshCannotIssueLiveOldSession(t *testing.T) {
	for iteration := 0; iteration < 40; iteration++ {
		clock := newFakeClock()
		service := newTestService(t, nil, clock, nil, nil)
		createTestAccount(t, service, "alice", emailPolicy())
		pair := login(t, service, "alice")
		transaction := beginReset(t, service, "alice", ResetID("reset-race-"+itoa(iteration)), "email")
		prepared := prepare(t, service, testNewPassword)

		var wg sync.WaitGroup
		start := make(chan struct{})
		var refreshPair TokenPair
		var refreshErr error
		var resetErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			refreshPair, refreshErr = service.RefreshTokens(context.Background(), pair.RefreshToken)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, resetErr = service.CommitReset(context.Background(), transaction, prepared)
		}()
		close(start)
		wg.Wait()
		if resetErr != nil {
			t.Fatalf("iteration %d reset failed: %v", iteration, resetErr)
		}
		account, _ := service.Store().GetAccount("alice")
		if account.CredentialEpoch != 1 || account.SessionEpoch != 1 {
			t.Fatalf("iteration %d epochs = %d/%d", iteration, account.CredentialEpoch, account.SessionEpoch)
		}
		if refreshErr == nil {
			if _, err := service.VerifyAccessToken(context.Background(), refreshPair.AccessToken); !errors.Is(err, ErrTokenEpochStale) {
				t.Fatalf("iteration %d refreshed access error = %v, want stale", iteration, err)
			}
			if _, err := service.RefreshTokens(context.Background(), refreshPair.RefreshToken); !errors.Is(err, ErrTokenEpochStale) {
				t.Fatalf("iteration %d refreshed refresh error = %v, want stale", iteration, err)
			}
		} else if !errors.Is(refreshErr, ErrTokenEpochStale) && !errors.Is(refreshErr, ErrTokenRevoked) {
			t.Fatalf("iteration %d refresh error = %v", iteration, refreshErr)
		}
		if _, err := service.VerifyAccessToken(context.Background(), pair.AccessToken); !errors.Is(err, ErrTokenEpochStale) {
			t.Fatalf("iteration %d old access survived: %v", iteration, err)
		}
	}
}

func TestConcurrentResetAndLoginCompletionCancelsOldProof(t *testing.T) {
	for iteration := 0; iteration < 40; iteration++ {
		clock := newFakeClock()
		service := newTestService(t, nil, clock, nil, nil)
		createTestAccount(t, service, "alice", emailPolicy())
		loginTransaction, err := service.BeginLogin(context.Background(), BeginLoginRequest{
			AccountID: "alice",
			Password:  testOldPassword,
		})
		if err != nil {
			t.Fatalf("BeginLogin: %v", err)
		}
		resetTransaction := beginReset(t, service, "alice", ResetID("reset-login-"+itoa(iteration)), "email")
		prepared := prepare(t, service, testNewPassword)

		var wg sync.WaitGroup
		start := make(chan struct{})
		var loginPair TokenPair
		var loginErr error
		var resetErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			loginPair, loginErr = service.CompleteLogin(context.Background(), loginTransaction.ID)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, resetErr = service.CommitReset(context.Background(), resetTransaction, prepared)
		}()
		close(start)
		wg.Wait()
		if resetErr != nil {
			t.Fatalf("iteration %d reset failed: %v", iteration, resetErr)
		}
		if loginErr == nil {
			if _, err := service.VerifyAccessToken(context.Background(), loginPair.AccessToken); !errors.Is(err, ErrTokenEpochStale) {
				t.Fatalf("iteration %d login access error = %v, want stale", iteration, err)
			}
			if _, err := service.RefreshTokens(context.Background(), loginPair.RefreshToken); !errors.Is(err, ErrTokenEpochStale) {
				t.Fatalf("iteration %d login refresh error = %v, want stale", iteration, err)
			}
		} else if !errors.Is(loginErr, ErrLoginCanceled) {
			t.Fatalf("iteration %d login error = %v", iteration, loginErr)
		}
	}
}

func TestTwoResetsAtSameEpochsOnlyOneCommits(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(t, nil, clock, nil, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	login(t, service, "alice")
	first := beginReset(t, service, "alice", "reset-a", "email")
	second := beginReset(t, service, "alice", "reset-b", "email+totp")
	firstPrepared := prepare(t, service, "new-password-aaa")
	secondPrepared := prepare(t, service, "new-password-bbb")

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, errs[0] = service.CommitReset(context.Background(), first, firstPrepared)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, errs[1] = service.CommitReset(context.Background(), second, secondPrepared)
	}()
	close(start)
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrResetSuperseded) && !errors.Is(err, ErrEpochConflict) {
			t.Fatalf("loser error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want 1", successes)
	}
	account, _ := service.Store().GetAccount("alice")
	if account.CredentialEpoch != 1 || account.SessionEpoch != 1 {
		t.Fatalf("epochs = %d/%d, want 1/1", account.CredentialEpoch, account.SessionEpoch)
	}
}

func TestConcurrentSameResetCommitIsIdempotent(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(t, nil, clock, nil, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	login(t, service, "alice")
	transaction := beginReset(t, service, "alice", "reset-concurrent-same", "email")
	prepared := prepare(t, service, testNewPassword)

	var wg sync.WaitGroup
	start := make(chan struct{})
	receipts := make([]ResetReceipt, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			receipts[i], errs[i] = service.CommitReset(context.Background(), transaction, prepared)
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("commit %d: %v", i, errs[i])
		}
	}
	if receipts[0].SessionEpoch != 1 || receipts[1].SessionEpoch != 1 {
		t.Fatalf("receipt epochs = %d/%d", receipts[0].SessionEpoch, receipts[1].SessionEpoch)
	}
	if receipts[0].Replayed == receipts[1].Replayed {
		t.Fatalf("exactly one commit should be a replay: %+v", receipts)
	}
	account, _ := service.Store().GetAccount("alice")
	if account.CredentialEpoch != 1 || account.SessionEpoch != 1 {
		t.Fatalf("epochs = %d/%d, want 1/1", account.CredentialEpoch, account.SessionEpoch)
	}
}

func TestResetBegunBeforeAnotherResetCommitIsStale(t *testing.T) {
	clock := newFakeClock()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	verifier := func(ctx context.Context, accountID AccountID, proof []byte) (VerifiedRecovery, error) {
		if string(proof) == "slow" {
			once.Do(func() { close(entered) })
			<-release
			return VerifiedRecovery{Methods: []RecoveryMethod{RecoveryMethodEmail}}, nil
		}
		return testVerifier(ctx, accountID, proof)
	}
	service, err := NewService(Config{
		Store:    NewMemoryStore(),
		Verifier: verifier,
		Hasher:   PBKDF2Hasher{},
		Policy:   testPolicy(),
		Params:   testParams(),
		Signer:   testSigner(t),
		Lifetime: TokenLifetime{Access: time.Hour, Refresh: 24 * time.Hour},
		Clock:    clock,
		Rand:     &sequenceReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	createTestAccount(t, service, "alice", emailPolicy())

	type beginResult struct {
		transaction *ResetTransaction
		err         error
	}
	slowResult := make(chan beginResult, 1)
	go func() {
		transaction, err := service.BeginReset(context.Background(), BeginResetRequest{
			AccountID:            "alice",
			ResetID:              "reset-slow",
			TargetPasswordParams: testParams(),
			Proof:                []byte("slow"),
			Deadline:             clock.Now().Add(5 * time.Minute),
		})
		slowResult <- beginResult{transaction: transaction, err: err}
	}()
	<-entered

	fast := beginReset(t, service, "alice", "reset-fast", "email")
	if _, err := service.CommitReset(context.Background(), fast, prepare(t, service, testNewPassword)); err != nil {
		t.Fatalf("fast commit: %v", err)
	}
	close(release)
	slow := <-slowResult
	if slow.err != nil {
		t.Fatalf("slow BeginReset: %v", slow.err)
	}
	if slow.transaction.StartSessionEpoch != 0 || slow.transaction.StartCredentialEpoch != 0 {
		t.Fatalf("slow txn start epochs = %d/%d, want 0/0",
			slow.transaction.StartCredentialEpoch, slow.transaction.StartSessionEpoch)
	}
	if _, err := service.CommitReset(context.Background(), slow.transaction, prepare(t, service, "slow-password-789")); !errors.Is(err, ErrEpochConflict) {
		t.Fatalf("slow commit error = %v, want epoch conflict", err)
	}
	account, _ := service.Store().GetAccount("alice")
	if account.CredentialEpoch != 1 || account.SessionEpoch != 1 {
		t.Fatalf("epochs = %d/%d, want 1/1", account.CredentialEpoch, account.SessionEpoch)
	}
}

func TestBeginLoginDuringResetIsStale(t *testing.T) {
	clock := newFakeClock()
	hasher := &blockingVerifyHasher{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	service, err := NewService(Config{
		Store:    NewMemoryStore(),
		Verifier: testVerifier,
		Hasher:   hasher,
		Policy:   testPolicy(),
		Params:   testParams(),
		Signer:   testSigner(t),
		Lifetime: TokenLifetime{Access: time.Hour, Refresh: 24 * time.Hour},
		Clock:    clock,
		Rand:     &sequenceReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	createTestAccount(t, service, "alice", emailPolicy())
	transaction := beginReset(t, service, "alice", "reset-vs-login-begin", "email")
	prepared := prepare(t, service, testNewPassword)

	hasher.setBlock(true)
	loginResult := make(chan error, 1)
	go func() {
		_, err := service.BeginLogin(context.Background(), BeginLoginRequest{
			AccountID: "alice",
			Password:  testOldPassword,
		})
		loginResult <- err
	}()
	<-hasher.entered
	if _, err := service.CommitReset(context.Background(), transaction, prepared); err != nil {
		t.Fatalf("reset commit: %v", err)
	}
	close(hasher.release)
	if err := <-loginResult; !errors.Is(err, ErrLoginStale) {
		t.Fatalf("BeginLogin error = %v, want stale", err)
	}
	account, _ := service.Store().GetAccount("alice")
	if account.CredentialEpoch != 1 || account.SessionEpoch != 1 {
		t.Fatalf("epochs = %d/%d, want 1/1", account.CredentialEpoch, account.SessionEpoch)
	}
}

func TestDeadlineCancelsResetAndLogin(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(t, nil, clock, nil, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	transaction, err := service.BeginReset(context.Background(), BeginResetRequest{
		AccountID:            "alice",
		ResetID:              "reset-deadline",
		TargetPasswordParams: testParams(),
		Proof:                []byte("email"),
		Deadline:             clock.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	loginTransaction, err := service.BeginLogin(context.Background(), BeginLoginRequest{
		AccountID: "alice",
		Password:  testOldPassword,
		Deadline:  clock.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Minute)
	if _, err := service.CommitReset(context.Background(), transaction, prepare(t, service, testNewPassword)); !errors.Is(err, ErrDeadlineExceeded) {
		t.Fatalf("reset error = %v, want deadline", err)
	}
	if _, err := service.CompleteLogin(context.Background(), loginTransaction.ID); !errors.Is(err, ErrDeadlineExceeded) {
		t.Fatalf("login error = %v, want deadline", err)
	}
	account, _ := service.Store().GetAccount("alice")
	if account.CredentialEpoch != 0 || account.SessionEpoch != 0 {
		t.Fatal("expired transactions changed epochs")
	}
}

func TestMissingCurrentPolicyFailsClosed(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(t, nil, clock, nil, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	transaction := beginReset(t, service, "alice", "reset-policy-missing", "email")
	service.Store().mu.Lock()
	service.Store().accounts["alice"].RecoveryPolicyDigest = PolicyDigest{0xAA}
	service.Store().mu.Unlock()
	if _, err := service.CommitReset(context.Background(), transaction, prepare(t, service, testNewPassword)); !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("commit error = %v, want policy unavailable", err)
	}
	account, _ := service.Store().GetAccount("alice")
	if account.CredentialEpoch != 0 || account.SessionEpoch != 0 {
		t.Fatal("missing policy changed epochs")
	}
}

func TestWeakPasswordParamsAndPasswordRejected(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(t, nil, clock, nil, nil)
	weak := testParams()
	weak.Iterations = 10
	if _, err := service.PreparePassword(context.Background(), "long-enough-password", weak); !errors.Is(err, ErrWeakPasswordParams) {
		t.Fatalf("weak params error = %v", err)
	}
	if _, err := service.PreparePassword(context.Background(), "short", testParams()); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("short password error = %v", err)
	}
}

func TestRandomResetIDsAreUnique(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(t, nil, clock, nil, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	deadline := clock.Now().Add(time.Minute)
	first, err := service.BeginReset(context.Background(), BeginResetRequest{
		AccountID: "alice", TargetPasswordParams: testParams(), Proof: []byte("email"), Deadline: deadline,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.BeginReset(context.Background(), BeginResetRequest{
		AccountID: "alice", TargetPasswordParams: testParams(), Proof: []byte("email"), Deadline: deadline,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ResetID == "" || second.ResetID == "" || first.ResetID == second.ResetID {
		t.Fatalf("reset IDs = %q, %q", first.ResetID, second.ResetID)
	}
	if len(first.ResetID) != len("rst_")+32 {
		t.Fatalf("reset ID length = %d, want 36", len(first.ResetID))
	}
}

func TestRefreshFamilyGenerationMaximumRevokesAndAlarms(t *testing.T) {
	clock := newFakeClock()
	alarms := &alarmSink{}
	service := newTestService(t, nil, clock, alarms, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	pair := login(t, service, "alice")
	account, _ := service.Store().GetAccount("alice")
	service.Store().mu.Lock()
	service.Store().families[pair.FamilyID].Generation = math.MaxUint64
	service.Store().mu.Unlock()
	exhausted, err := service.signer.issuePair(clock.Now(), service.lifetime, account, pair.FamilyID, math.MaxUint64, service.newTokenID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RefreshTokens(context.Background(), exhausted.RefreshToken); !errors.Is(err, ErrFamilyGenerationExhausted) {
		t.Fatalf("error = %v, want generation exhausted", err)
	}
	all := alarms.all()
	if len(all) != 1 || all[0].Code != AlarmFamilyGenerationExhausted {
		t.Fatalf("alarms = %+v", all)
	}
	family, _ := service.Store().GetRefreshFamily(pair.FamilyID)
	if !family.Revoked {
		t.Fatal("exhausted family was not revoked")
	}
}

func TestDisabledAccountRejectsAllSessionOperations(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(t, nil, clock, nil, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	pair := login(t, service, "alice")
	if err := service.SetAccountStatus("alice", AccountDisabled); err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyAccessToken(context.Background(), pair.AccessToken); !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("access error = %v, want inactive", err)
	}
	if _, err := service.RefreshTokens(context.Background(), pair.RefreshToken); !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("refresh error = %v, want inactive", err)
	}
	if _, err := service.BeginLogin(context.Background(), BeginLoginRequest{AccountID: "alice", Password: testOldPassword}); !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("login error = %v, want inactive", err)
	}
	if _, err := service.BeginReset(context.Background(), BeginResetRequest{
		AccountID: "alice", TargetPasswordParams: testParams(), Proof: []byte("email"),
	}); !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("reset error = %v, want inactive", err)
	}
}

func TestTokenKindAndExpiryAreEnforced(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(t, nil, clock, nil, nil)
	createTestAccount(t, service, "alice", emailPolicy())
	pair := login(t, service, "alice")
	if _, err := service.VerifyAccessToken(context.Background(), pair.RefreshToken); !errors.Is(err, ErrTokenKind) {
		t.Fatalf("refresh-as-access error = %v, want kind", err)
	}
	if _, err := service.RefreshTokens(context.Background(), pair.AccessToken); !errors.Is(err, ErrTokenKind) {
		t.Fatalf("access-as-refresh error = %v, want kind", err)
	}
	clock.Advance(2 * time.Hour)
	if _, err := service.VerifyAccessToken(context.Background(), pair.AccessToken); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired access error = %v, want expired", err)
	}
	clock.Advance(25 * time.Hour)
	if _, err := service.RefreshTokens(context.Background(), pair.RefreshToken); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired refresh error = %v, want expired", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buffer [20]byte
	i := len(buffer)
	for n > 0 {
		i--
		buffer[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buffer[i:])
}
