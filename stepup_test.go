package stepup

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testUser       = "user-1"
	testSession    = "session-a"
	testSessionB   = "session-b"
	testOpTransfer = "transfer:100"
	testOpDelete   = "account:delete"
	testTTL        = time.Minute
	goodCode       = "123456"
	badCode        = "000000"
)

func testService(clock Clock) *Service {
	verifier := FactorVerifierFunc(func(userID, code string) bool {
		return userID == testUser && code == goodCode
	})
	return NewService(clock, verifier, testTTL)
}

func mustBegin(t *testing.T, svc *Service, session, op string) string {
	t.Helper()
	id := svc.BeginChallenge(testUser, session, op)
	if id == "" {
		t.Fatal("BeginChallenge returned empty id")
	}
	return id
}

func mustVerify(t *testing.T, svc *Service, challengeID, session string) string {
	t.Helper()
	receipt, err := svc.SubmitFactor(challengeID, session, goodCode)
	if err != nil {
		t.Fatalf("SubmitFactor: %v", err)
	}
	return receipt
}

func TestHappyPathAndBindings(t *testing.T) {
	clk := NewFakeClock(time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC))
	svc := testService(clk)

	id := mustBegin(t, svc, testSession, testOpTransfer)
	if got := svc.Generation(testUser); got != 0 {
		t.Fatalf("initial generation = %d, want 0", got)
	}

	if _, err := svc.SubmitFactor(id, testSession, badCode); !errors.Is(err, ErrInvalidFactor) {
		t.Fatalf("wrong code error = %v, want ErrInvalidFactor", err)
	}

	receipt := mustVerify(t, svc, id, testSession)

	// The receipt is bound to one target operation.
	if err := svc.Redeem(receipt, testSession, testOpDelete); !errors.Is(err, ErrOperationMismatch) {
		t.Fatalf("cross-operation redeem error = %v, want ErrOperationMismatch", err)
	}
	// The receipt is bound to one login session.
	if err := svc.Redeem(receipt, testSessionB, testOpTransfer); !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("cross-session redeem error = %v, want ErrSessionMismatch", err)
	}
	// Unknown ids are rejected.
	if err := svc.Redeem("nope", testSession, testOpTransfer); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown receipt error = %v, want ErrNotFound", err)
	}

	if err := svc.Redeem(receipt, testSession, testOpTransfer); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	// Single-use: the same receipt cannot be exchanged a second time.
	if err := svc.Redeem(receipt, testSession, testOpTransfer); !errors.Is(err, ErrAlreadyRedeemed) {
		t.Fatalf("second redeem error = %v, want ErrAlreadyRedeemed", err)
	}
	// A redeemed receipt can never authorize a different operation either.
	if err := svc.Redeem(receipt, testSession, testOpDelete); !errors.Is(err, ErrAlreadyRedeemed) {
		t.Fatalf("redeemed receipt reused for other op error = %v, want ErrAlreadyRedeemed", err)
	}
}

func TestVerifyThenRebind(t *testing.T) {
	// 先验证后换绑：成功回执在换绑完成后不得兑换。
	clk := NewFakeClock(time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC))
	svc := testService(clk)

	id := mustBegin(t, svc, testSession, testOpTransfer)
	receipt := mustVerify(t, svc, id, testSession)

	gen := svc.RebindDevice(testUser)
	if gen != 1 {
		t.Fatalf("generation after rebind = %d, want 1", gen)
	}

	if err := svc.Redeem(receipt, testSession, testOpTransfer); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("redeem after rebind error = %v, want ErrGenerationMismatch", err)
	}
	// The old pending challenge is also dead; a fresh challenge on the new
	// device generation works end to end.
	if _, err := svc.SubmitFactor(id, testSession, goodCode); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("verify old challenge after rebind error = %v, want ErrGenerationMismatch", err)
	}

	newID := mustBegin(t, svc, testSession, testOpTransfer)
	newReceipt := mustVerify(t, svc, newID, testSession)
	if err := svc.Redeem(newReceipt, testSession, testOpTransfer); err != nil {
		t.Fatalf("redeem on new generation: %v", err)
	}
}

func TestRebindBeforeRedeemAcrossWindow(t *testing.T) {
	// 先换绑后兑换：换绑发生在挑战等待期间，未完成挑战立即失效。
	clk := NewFakeClock(time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC))
	svc := testService(clk)

	id := mustBegin(t, svc, testSession, testOpTransfer)

	clk.Advance(30 * time.Second)
	svc.RebindDevice(testUser)

	// Factor submission against the pre-rebind challenge must fail.
	if _, err := svc.SubmitFactor(id, testSession, goodCode); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("submit after rebind error = %v, want ErrGenerationMismatch", err)
	}

	// A second rebind invalidates challenges created on the interim generation.
	midID := mustBegin(t, svc, testSession, testOpDelete)
	svc.RebindDevice(testUser)
	if _, err := svc.SubmitFactor(midID, testSession, goodCode); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("submit after second rebind error = %v, want ErrGenerationMismatch", err)
	}

	// Already-redeemed receipts stay valid historical records after rebind:
	// re-presenting them reports reuse, not invalidation.
	doneID := mustBegin(t, svc, testSession, testOpTransfer)
	doneReceipt := mustVerify(t, svc, doneID, testSession)
	if err := svc.Redeem(doneReceipt, testSession, testOpTransfer); err != nil {
		t.Fatalf("redeem before rebind: %v", err)
	}
	svc.RebindDevice(testUser)
	if err := svc.Redeem(doneReceipt, testSession, testOpTransfer); !errors.Is(err, ErrAlreadyRedeemed) {
		t.Fatalf("reuse after rebind error = %v, want ErrAlreadyRedeemed", err)
	}
}

func TestExpiryBoundary(t *testing.T) {
	start := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)

	t.Run("redeem just before expiry", func(t *testing.T) {
		clk := NewFakeClock(start)
		svc := testService(clk)
		id := mustBegin(t, svc, testSession, testOpTransfer)
		receipt := mustVerify(t, svc, id, testSession)

		clk.Advance(testTTL - time.Nanosecond)
		if err := svc.Redeem(receipt, testSession, testOpTransfer); err != nil {
			t.Fatalf("redeem one nanosecond before expiry: %v", err)
		}
	})

	t.Run("expiry and redemption at the same instant", func(t *testing.T) {
		// 过期与兑换同刻：到期时刻的兑换必须判负，expiry is inclusive.
		clk := NewFakeClock(start)
		svc := testService(clk)
		id := mustBegin(t, svc, testSession, testOpTransfer)
		receipt := mustVerify(t, svc, id, testSession)

		clk.Advance(testTTL)
		if err := svc.Redeem(receipt, testSession, testOpTransfer); !errors.Is(err, ErrChallengeExpired) {
			t.Fatalf("redeem exactly at expiry error = %v, want ErrChallengeExpired", err)
		}
	})

	t.Run("factor submission at expiry instant", func(t *testing.T) {
		clk := NewFakeClock(start)
		svc := testService(clk)
		id := mustBegin(t, svc, testSession, testOpTransfer)

		clk.Advance(testTTL)
		if _, err := svc.SubmitFactor(id, testSession, goodCode); !errors.Is(err, ErrChallengeExpired) {
			t.Fatalf("submit at expiry error = %v, want ErrChallengeExpired", err)
		}
	})
}

func TestExpiryRedeemSameInstantUnderContention(t *testing.T) {
	// 并发屏障：兑换进入临界区后时钟被推到到期时刻，兑换仍必须判负。
	start := time.Date(2026, 11, 11, 11, 0, 0, 0, time.UTC)

	t.Run("expiry lands inside redeem critical section", func(t *testing.T) {
		clk := NewFakeClock(start)
		svc := testService(clk)
		receipt := mustVerify(t, svc, mustBegin(t, svc, testSession, testOpTransfer), testSession)

		var entered, expired atomic.Int64
		svc.OnBeforeRedeem = func(string) {
			entered.Store(1)
			for expired.Load() == 0 {
			}
		}

		errCh := make(chan error, 1)
		go func() {
			errCh <- svc.Redeem(receipt, testSession, testOpTransfer)
		}()
		for entered.Load() == 0 {
		}
		// Redeem is holding the service lock inside OnBeforeRedeem; move the
		// clock to exactly the expiry instant while it is parked there.
		clk.Advance(testTTL) // expire while redemption holds the lock
		expired.Store(1)

		if err := <-errCh; !errors.Is(err, ErrChallengeExpired) {
			t.Fatalf("contended redeem error = %v, want ErrChallengeExpired", err)
		}
	})

	t.Run("parallel redeem with simultaneous expiry, all lose", func(t *testing.T) {
		clk := NewFakeClock(start)
		svc := testService(clk)

		const n = 16
		receipts := make([]string, n)
		for i := 0; i < n; i++ {
			receipts[i] = mustVerify(t, svc, mustBegin(t, svc, testSession, testOpTransfer), testSession)
		}

		// Barrier: every goroutine parks OUTSIDE the service lock until the
		// clock has been moved to the expiry instant, then all race into
		// Redeem together.
		var gate atomic.Int64
		var ready atomic.Int64
		var wg sync.WaitGroup
		errs := make([]error, n)

		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int, receipt string) {
				defer wg.Done()
				ready.Add(1)
				for gate.Load() == 0 {
				}
				errs[i] = svc.Redeem(receipt, testSession, testOpTransfer)
			}(i, receipts[i])
		}

		// Wait until every goroutine has parked at the barrier, then expire
		// all receipts at once before releasing it.
		for ready.Load() < n {
		}
		clk.Advance(testTTL) // every receipt expires at the same instant
		gate.Store(1)
		wg.Wait()

		for i, err := range errs {
			if !errors.Is(err, ErrChallengeExpired) {
				t.Fatalf("redeem #%d error = %v, want ErrChallengeExpired", i, err)
			}
		}
	})
}

func TestConcurrentSingleUseRedeem(t *testing.T) {
	// 并发屏障：同一回执被大量 goroutine 同时兑换，至多一次成功。
	clk := NewFakeClock(time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC))
	svc := testService(clk)
	receipt := mustVerify(t, svc, mustBegin(t, svc, testSession, testOpTransfer), testSession)

	const n = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	var successes, reused int

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := svc.Redeem(receipt, testSession, testOpTransfer)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrAlreadyRedeemed):
				reused++
			default:
				t.Errorf("unexpected redeem error: %v", err)
			}
		}()
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("successful redemptions = %d, want exactly 1", successes)
	}
	if reused != n-1 {
		t.Fatalf("reused rejections = %d, want %d", reused, n-1)
	}
}

func TestConcurrentRebindVersusRedeem(t *testing.T) {
	// 并发屏障：换绑与兑换同时进行，任何代次下至多一次成功，且绝不出现
	// 跨代次兑换。
	clk := NewFakeClock(time.Date(2026, 11, 11, 13, 0, 0, 0, time.UTC))

	for iter := 0; iter < 25; iter++ {
		svc := testService(clk)
		receipt := mustVerify(t, svc, mustBegin(t, svc, testSession, testOpTransfer), testSession)

		const redeemers = 8
		var wg sync.WaitGroup
		var mu sync.Mutex
		var successes int

		start := make(chan struct{})
		for i := 0; i < redeemers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				err := svc.Redeem(receipt, testSession, testOpTransfer)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					successes++
				case errors.Is(err, ErrGenerationMismatch),
					errors.Is(err, ErrChallengeInvalid),
					errors.Is(err, ErrAlreadyRedeemed):
				default:
					t.Errorf("iter %d: unexpected redeem error: %v", iter, err)
				}
			}()
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			close(start)
			svc.RebindDevice(testUser)
		}()
		wg.Wait()

		if successes > 1 {
			t.Fatalf("iter %d: successes = %d, want at most 1", iter, successes)
		}
	}
}
