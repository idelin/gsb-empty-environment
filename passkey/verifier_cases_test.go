package passkey

import (
	"sync"
	"testing"
	"time"
)

func TestFinishAssertion_SuccessSingleDevice(t *testing.T) {
	f := newFixture(t, false, 5)
	in := f.buildAssertion(t, f.defaultOpt(false, 6))

	res, err := f.svc.FinishAssertion(in)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if res.Counter != CounterAdvanced {
		t.Fatalf("counter = %v, want advanced", res.Counter)
	}
	if res.SignCount != 6 {
		t.Fatalf("sign count = %d, want 6", res.SignCount)
	}
	if f.svc.Sessions.Len() != 1 {
		t.Fatalf("sessions = %d, want 1", f.svc.Sessions.Len())
	}
	if _, ok := f.svc.Sessions.Get(res.SessionToken); !ok {
		t.Fatal("issued session token not stored")
	}
}

func TestFinishAssertion_InvalidSignatureDoesNotBurnChallenge(t *testing.T) {
	f := newFixture(t, false, 5)

	bad := f.defaultOpt(false, 6)
	bad.damageSig = true
	if _, err := f.svc.FinishAssertion(f.buildAssertion(t, bad)); err != ErrInvalidSignature {
		t.Fatalf("err = %v, want ErrInvalidSignature", err)
	}

	// The challenge must still be present and a valid retry must succeed.
	if _, err := f.svc.Challenges.Peek(f.pendingID); err != nil {
		t.Fatalf("challenge was burned by an invalid signature: %v", err)
	}
	good := f.buildAssertion(t, f.defaultOpt(false, 6))
	res, err := f.svc.FinishAssertion(good)
	if err != nil {
		t.Fatalf("retry after bad signature failed: %v", err)
	}
	if f.svc.Sessions.Len() != 1 || res.SessionToken == "" {
		t.Fatal("exactly one session expected after retry")
	}
}

func TestFinishAssertion_ConcurrentDuplicateAssertionsIssueOneSession(t *testing.T) {
	f := newFixture(t, false, 10)

	const n = 32
	inputs := make([]AssertionInput, n)
	for i := range inputs {
		// Independent signing call each time: distinct r/s values.
		inputs[i] = f.buildAssertion(t, f.defaultOpt(false, 11))
	}

	var wg sync.WaitGroup
	results := make(chan error, n)
	for i := range inputs {
		wg.Add(1)
		go func(in AssertionInput) {
			defer wg.Done()
			_, err := f.svc.FinishAssertion(in)
			results <- err
		}(inputs[i])
	}
	wg.Wait()
	close(results)

	var success, used int
	for err := range results {
		switch err {
		case nil:
			success++
		case ErrChallengeAlreadyUsed, ErrCounterReplay:
			// Which rejection a loser sees depends on interleaving: it can lose
			// the single-use challenge Consume or the counter CAS. Either way
			// no session is issued.
			used++
		default:
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("successes = %d, want exactly 1", success)
	}
	if success+used != n {
		t.Fatalf("total handled = %d, want %d", success+used, n)
	}
	if f.svc.Sessions.Len() != 1 {
		t.Fatalf("sessions issued = %d, want 1", f.svc.Sessions.Len())
	}
}

func TestFinishAssertion_CounterReplayRejectedAndChallengeKept(t *testing.T) {
	f := newFixture(t, false, 10)

	if _, err := f.svc.FinishAssertion(f.buildAssertion(t, f.defaultOpt(false, 10))); err != ErrCounterReplay {
		t.Fatalf("err = %v, want ErrCounterReplay", err)
	}
	if _, err := f.svc.Challenges.Peek(f.pendingID); err != nil {
		t.Fatalf("counter replay burned the challenge: %v", err)
	}

	good := f.buildAssertion(t, f.defaultOpt(false, 11))
	if _, err := f.svc.FinishAssertion(good); err != nil {
		t.Fatalf("valid assertion after rejected replay failed: %v", err)
	}
}

func TestFinishAssertion_BackupEligibleZeroCounter(t *testing.T) {
	f := newFixture(t, true, 0)

	res, err := f.svc.FinishAssertion(f.buildAssertion(t, f.defaultOpt(true, 0)))
	if err != nil {
		t.Fatalf("backup zero counter must be accepted, got %v", err)
	}
	if res.Counter != CounterBackupZero {
		t.Fatalf("counter = %v, want backup-zero", res.Counter)
	}

	// A moving (even non-monotonic) counter on a backup credential is advisory.
	f2 := newFixture(t, true, 3)
	res2, err := f2.svc.FinishAssertion(f2.buildAssertion(t, f2.defaultOpt(true, 2)))
	if err != nil {
		t.Fatalf("backup non-monotonic counter must be tolerated, got %v", err)
	}
	if res2.Counter != CounterBackupAdvisory {
		t.Fatalf("counter = %v, want backup-advisory", res2.Counter)
	}
	stored, _ := f2.svc.Credentials.Get(f2.credID)
	if stored.SignCount != 3 {
		t.Fatalf("stored backup count = %d, want unchanged 3", stored.SignCount)
	}
}

func TestFinishAssertion_SingleDeviceZeroCounterFallback(t *testing.T) {
	f := newFixture(t, false, 0)

	res, err := f.svc.FinishAssertion(f.buildAssertion(t, f.defaultOpt(false, 0)))
	if err != nil {
		t.Fatalf("single-device zero fallback must be accepted, got %v", err)
	}
	if res.Counter != CounterSingleDeviceFallback {
		t.Fatalf("counter = %v, want single-device-fallback", res.Counter)
	}

	// Once a real counter exists, dropping back to zero signals a clone.
	f2 := newFixture(t, false, 7)
	if _, err := f2.svc.FinishAssertion(f2.buildAssertion(t, f2.defaultOpt(false, 0))); err != ErrCounterReplay {
		t.Fatalf("err = %v, want ErrCounterReplay", err)
	}
}

func TestFinishAssertion_BackupFlagMismatch(t *testing.T) {
	f := newFixture(t, true, 0)
	// Stored as backup-eligible but assertion claims single-device.
	if _, err := f.svc.FinishAssertion(f.buildAssertion(t, f.defaultOpt(false, 0))); err != ErrBackupFlagMismatch {
		t.Fatalf("err = %v, want ErrBackupFlagMismatch", err)
	}
}

func TestFinishAssertion_BindingMismatches(t *testing.T) {
	cases := []struct {
		name string
		mut  func(o *assertOption)
		want error
	}{
		{"origin", func(o *assertOption) { o.origin = "https://evil.example" }, ErrOriginMismatch},
		{"rpid", func(o *assertOption) { o.rpID = "evil.example" }, ErrRPIDMismatch},
		{"challenge", func(o *assertOption) { o.customChallenge = []byte("totally-different-challenge-value") }, ErrChallengeMismatch},
		{"client type", func(o *assertOption) { o.clientType = "webauthn.create" }, ErrInvalidClientData},
		{"unknown credential", func(o *assertOption) { o.credID = []byte("missing-handle") }, ErrUnknownCredential},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, false, 1)
			opt := f.defaultOpt(false, 2)
			tc.mut(&opt)
			_, err := f.svc.FinishAssertion(f.buildAssertion(t, opt))
			if err != tc.want {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if _, perr := f.svc.Challenges.Peek(f.pendingID); perr != nil {
				t.Fatalf("failed verification must keep challenge: %v", perr)
			}
		})
	}
}

func TestFinishAssertion_ExpiredAndReusedChallenge(t *testing.T) {
	f := newFixture(t, false, 1)
	f.svc.Challenges.Now = func() time.Time { return f.now.Add(10 * time.Minute) }

	if _, err := f.svc.FinishAssertion(f.buildAssertion(t, f.defaultOpt(false, 2))); err != ErrChallengeExpired {
		t.Fatalf("err = %v, want ErrChallengeExpired", err)
	}

	// Back in time: the expired entry was evicted, so re-mint an identical
	// pending challenge. First use succeeds, the second reports already-used.
	f.svc.Challenges.Now = func() time.Time { return f.now }
	f.svc.Challenges.Put(PendingChallenge{
		ID:        f.pendingID,
		Challenge: f.challenge,
		RPID:      testRPID,
		Origin:    testOrigin,
		UserID:    f.userID,
		ExpiresAt: f.now.Add(5 * time.Minute),
	})
	if _, err := f.svc.FinishAssertion(f.buildAssertion(t, f.defaultOpt(false, 2))); err != nil {
		t.Fatalf("first use failed: %v", err)
	}
	if _, err := f.svc.FinishAssertion(f.buildAssertion(t, f.defaultOpt(false, 3))); err != ErrChallengeAlreadyUsed {
		t.Fatalf("err = %v, want ErrChallengeAlreadyUsed", err)
	}
}
