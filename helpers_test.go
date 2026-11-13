package netsec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	testOldPassword = "old-password-123"
	testNewPassword = "new-password-456"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type sequenceReader struct {
	mu   sync.Mutex
	next byte
}

func (r *sequenceReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range p {
		p[i] = r.next
		r.next++
	}
	return len(p), nil
}

type alarmSink struct {
	mu     sync.Mutex
	alarms []Alarm
}

func (s *alarmSink) Raise(alarm Alarm) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alarms = append(s.alarms, alarm)
}

func (s *alarmSink) all() []Alarm {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Alarm(nil), s.alarms...)
}

func testPolicy() PasswordPolicy {
	return PasswordPolicy{
		MinIterations:    1_000,
		MinSaltBytes:     16,
		MinKeyBytes:      32,
		MinPasswordBytes: 8,
	}
}

func testParams() PasswordParams {
	return PasswordParams{
		Algorithm:  PBKDF2SHA256,
		Iterations: 1_000,
		SaltBytes:  16,
		KeyBytes:   32,
	}
}

func testSigner(t *testing.T) *TokenSigner {
	t.Helper()
	signer, err := NewTokenSigner([]byte("test-signing-key-that-is-32-bytes!"))
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func testVerifier(_ context.Context, _ AccountID, proof []byte) (VerifiedRecovery, error) {
	switch string(proof) {
	case "email":
		return VerifiedRecovery{Methods: []RecoveryMethod{RecoveryMethodEmail}}, nil
	case "email+totp":
		return VerifiedRecovery{Methods: []RecoveryMethod{RecoveryMethodEmail, RecoveryMethodTOTP}}, nil
	case "totp":
		return VerifiedRecovery{Methods: []RecoveryMethod{RecoveryMethodTOTP}}, nil
	default:
		return VerifiedRecovery{}, errors.New("unknown proof")
	}
}

func newTestService(t *testing.T, store *MemoryStore, clock Clock, alarms *alarmSink, signer *TokenSigner) *Service {
	t.Helper()
	if store == nil {
		store = NewMemoryStore()
	}
	if clock == nil {
		clock = newFakeClock()
	}
	if signer == nil {
		signer = testSigner(t)
	}
	var onAlarm AlarmFunc
	if alarms != nil {
		onAlarm = alarms.Raise
	}
	service, err := NewService(Config{
		Store:    store,
		Verifier: testVerifier,
		Hasher:   PBKDF2Hasher{},
		Policy:   testPolicy(),
		Params:   testParams(),
		Signer:   signer,
		Lifetime: TokenLifetime{Access: time.Hour, Refresh: 24 * time.Hour},
		Clock:    clock,
		Rand:     &sequenceReader{},
		OnAlarm:  onAlarm,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func createTestAccount(t *testing.T, service *Service, id AccountID, policy RecoveryPolicy) {
	t.Helper()
	if err := service.CreateAccount(context.Background(), id, testOldPassword, testParams(), policy); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
}

func emailPolicy() RecoveryPolicy {
	return RecoveryPolicy{Required: []RecoveryMethod{RecoveryMethodEmail}}
}

func emailTOTPPolicy() RecoveryPolicy {
	return RecoveryPolicy{Required: []RecoveryMethod{RecoveryMethodEmail, RecoveryMethodTOTP}}
}

func login(t *testing.T, service *Service, id AccountID) TokenPair {
	t.Helper()
	transaction, err := service.BeginLogin(context.Background(), BeginLoginRequest{
		AccountID: id,
		Password:  testOldPassword,
	})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	pair, err := service.CompleteLogin(context.Background(), transaction.ID)
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	return pair
}

func beginReset(t *testing.T, service *Service, id AccountID, resetID ResetID, proof string) *ResetTransaction {
	t.Helper()
	transaction, err := service.BeginReset(context.Background(), BeginResetRequest{
		AccountID:            id,
		ResetID:              resetID,
		TargetPasswordParams: testParams(),
		Proof:                []byte(proof),
		Deadline:             service.NowForTest().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("BeginReset: %v", err)
	}
	return transaction
}

func prepare(t *testing.T, service *Service, password string) *PreparedPassword {
	t.Helper()
	prepared, err := service.PreparePassword(context.Background(), password, testParams())
	if err != nil {
		t.Fatalf("PreparePassword: %v", err)
	}
	return prepared
}

func requireErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

// NowForTest exists only in the test build.
func (s *Service) NowForTest() time.Time { return s.now() }
