package netsec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
)

var errInjectedWrite = errors.New("injected persistence failure")

type faultNonceStore struct {
	inner  NonceStore
	failAt int

	mu     sync.Mutex
	writes int
	failed bool
}

func (s *faultNonceStore) Get(ctx context.Context, key string) (NonceRecord, error) {
	return s.inner.Get(ctx, key)
}

func (s *faultNonceStore) Create(ctx context.Context, key string, record NonceRecord) error {
	if s.shouldFail() {
		return errInjectedWrite
	}
	return s.inner.Create(ctx, key, record)
}

func (s *faultNonceStore) Update(ctx context.Context, key string, expectedVersion uint64, record NonceRecord) error {
	if s.shouldFail() {
		return errInjectedWrite
	}
	return s.inner.Update(ctx, key, expectedVersion, record)
}

func (s *faultNonceStore) shouldFail() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if !s.failed && s.failAt > 0 && s.writes == s.failAt {
		s.failed = true
		return true
	}
	return false
}

func TestPersistenceFaultInjectionAtEveryPoint(t *testing.T) {
	for failAt := 1; failAt <= 3; failAt++ {
		t.Run(fmt.Sprintf("write-%d", failAt), func(t *testing.T) {
			key1 := generateTestKey(t)
			key2 := generateTestKey(t)
			roster := mustRoster(t, key1, key2)
			sessionID := []byte(fmt.Sprintf("fault-session-%d", failAt))
			message := []byte("fault message")
			request := SignRequest{SessionID: sessionID, Message: message, Roster: roster}
			aggregateR := scalarBaseMult(big.NewInt(1)).compressed()

			store := NewMemoryNonceStore()
			faults := &faultNonceStore{inner: store, failAt: failAt}
			signer, err := NewSigner(key1, faults)
			if err != nil {
				t.Fatalf("signer: %v", err)
			}
			key, err := signer.StoreKey(request)
			if err != nil {
				t.Fatalf("store key: %v", err)
			}

			commitment, err := signer.Commit(t.Context(), request)
			if failAt == 1 {
				if !errors.Is(err, errInjectedWrite) {
					t.Fatalf("commit error = %v, want injected failure", err)
				}
				if commitment != nil {
					t.Fatal("commitment was released despite persistence failure")
				}
				if _, err := store.Get(t.Context(), key); !errors.Is(err, ErrNonceNotFound) {
					t.Fatalf("record exists after failed nonce persist: %v", err)
				}

				restarted, err := NewSigner(key1, store)
				if err != nil {
					t.Fatalf("restart signer: %v", err)
				}
				commitment, err = restarted.Commit(t.Context(), request)
				if err != nil {
					t.Fatalf("commit after restart: %v", err)
				}
				if len(commitment) != 32 {
					t.Fatalf("commitment length = %d", len(commitment))
				}
				return
			}
			if err != nil {
				t.Fatalf("commit before partial: %v", err)
			}

			partial, err := signer.Partial(t.Context(), request, aggregateR)
			if !errors.Is(err, errInjectedWrite) {
				t.Fatalf("partial error = %v, want injected failure", err)
			}
			if partial != nil {
				t.Fatal("partial signature was released before the consume transition persisted")
			}
			record, err := store.Get(t.Context(), key)
			if err != nil {
				t.Fatalf("get record: %v", err)
			}
			wantState := NoncePrepared
			if failAt == 3 {
				wantState = NonceChallenged
			}
			if record.State != wantState {
				t.Fatalf("state after failure = %s, want %s", record.State, wantState)
			}
			if failAt == 3 {
				otherR := differentPoint(t, aggregateR)
				if _, err := signer.Partial(t.Context(), request, otherR); !errors.Is(err, ErrNonceChallengeMismatch) {
					t.Fatalf("different R after persisted challenge error = %v, want ErrNonceChallengeMismatch", err)
				}
			}

			restarted, err := NewSigner(key1, store)
			if err != nil {
				t.Fatalf("restart signer: %v", err)
			}
			partial, err = restarted.Partial(t.Context(), request, aggregateR)
			if err != nil {
				t.Fatalf("partial after restart: %v", err)
			}
			if len(partial) != ScalarSize {
				t.Fatalf("partial length = %d", len(partial))
			}
			record, err = store.Get(t.Context(), key)
			if err != nil {
				t.Fatalf("get consumed record: %v", err)
			}
			if record.State != NonceConsumed {
				t.Fatalf("state after retry = %s, want consumed", record.State)
			}
			if !bytes.Equal(record.Partial, partial) {
				t.Fatal("stored partial and returned partial differ")
			}

			restartedAgain, err := NewSigner(key1, store)
			if err != nil {
				t.Fatalf("second restart: %v", err)
			}
			retry, err := restartedAgain.Partial(t.Context(), request, aggregateR)
			if err != nil {
				t.Fatalf("idempotent partial after restart: %v", err)
			}
			if !bytes.Equal(partial, retry) {
				t.Fatal("restart changed the consumed nonce's partial signature")
			}

			otherR := differentPoint(t, aggregateR)
			if _, err := restartedAgain.Partial(t.Context(), request, otherR); !errors.Is(err, ErrNonceConsumedConflict) {
				t.Fatalf("different challenge on consumed nonce error = %v, want ErrNonceConsumedConflict", err)
			}
		})
	}
}

func TestAbortPersistenceFailureIsRecoverable(t *testing.T) {
	for failAt := 1; failAt <= 2; failAt++ {
		t.Run(fmt.Sprintf("abort-write-%d", failAt), func(t *testing.T) {
			key1 := generateTestKey(t)
			key2 := generateTestKey(t)
			roster := mustRoster(t, key1, key2)
			request := SignRequest{
				SessionID: []byte(fmt.Sprintf("abort-fault-%d", failAt)),
				Message:   []byte("m"),
				Roster:    roster,
			}
			store := NewMemoryNonceStore()
			faults := &faultNonceStore{inner: store, failAt: failAt}
			signer, err := NewSigner(key1, faults)
			if err != nil {
				t.Fatalf("signer: %v", err)
			}

			if failAt == 2 {
				if _, err := signer.Commit(t.Context(), request); err != nil {
					t.Fatalf("commit: %v", err)
				}
			}
			if err := signer.Abort(t.Context(), request); !errors.Is(err, errInjectedWrite) {
				t.Fatalf("abort error = %v, want injected failure", err)
			}

			restarted, err := NewSigner(key1, store)
			if err != nil {
				t.Fatalf("restart signer: %v", err)
			}
			if err := restarted.Abort(t.Context(), request); err != nil {
				t.Fatalf("abort after restart: %v", err)
			}
			if _, err := restarted.Commit(t.Context(), request); !errors.Is(err, ErrNonceAborted) {
				t.Fatalf("commit after abort error = %v, want ErrNonceAborted", err)
			}
		})
	}
}
