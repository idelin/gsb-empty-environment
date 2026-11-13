package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

const testServiceKey = "test-service-key"

type fixture struct {
	srv     *Server
	clock   *ManualClock
	account AccountID
	device  DeviceID
	pub     ed25519.PublicKey
	priv    ed25519.PrivateKey
}

func newFixture(t *testing.T, mutate func(*Config)) *fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clock := NewManualClock(0)
	cfg := Config{
		ServiceKey:          []byte(testServiceKey),
		Clock:               clock,
		Rand:                NewDeterministicReader(0x0123456789abcdef),
		HourlyCreationLimit: 100,
		DenialLockThreshold: 3,
		DenialLockDuration:  5 * time.Minute,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		srv:     srv,
		clock:   clock,
		account: "acct-1",
		device:  "device-1",
		pub:     pub,
		priv:    priv,
	}
	if err := srv.RegisterDevice(f.device, f.account, pub); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) request(browser BrowserSessionID, digest RequestDigest) ChallengeRequest {
	return ChallengeRequest{
		AccountID:        f.account,
		BrowserSessionID: browser,
		DeviceID:         f.device,
		RequestDigest:    digest,
		Risk:             RiskMedium,
	}
}

func (f *fixture) create(t *testing.T, browser BrowserSessionID, digest RequestDigest, senders ...PushSender) *Challenge {
	t.Helper()
	ch, err := f.srv.CreateChallenge(f.request(browser, digest), senders...)
	if err != nil {
		t.Fatalf("CreateChallenge: %v", err)
	}
	return ch
}

func mustErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want %v", err, target)
	}
}

func TestDisplayCodeFormulaIsServerDerived(t *testing.T) {
	f := newFixture(t, nil)
	digest := DigestRequest([]byte("login-payload"))
	browser := BrowserSessionID("browser-a")
	ch := f.create(t, browser, digest)

	mac := hmac.New(sha256.New, []byte(testServiceKey))
	_, _ = mac.Write(ch.ID[:])
	_, _ = mac.Write(digest[:])
	sum := mac.Sum(nil)
	var n uint32
	for _, b := range sum {
		n = (n*256 + uint32(b)) % 1000
	}
	want := fmt.Sprintf("%03d", n)
	if ch.DisplayCode != want {
		t.Fatalf("display code = %q, want %q", ch.DisplayCode, want)
	}
	if len(ch.DisplayCode) != 3 {
		t.Fatalf("display code %q is not three characters", ch.DisplayCode)
	}
	if ch.ID == (ChallengeID{}) {
		t.Fatal("challenge id must be server-generated and non-zero")
	}
	if len(ch.ID) != ChallengeIDBytes {
		t.Fatalf("challenge id length = %d", len(ch.ID))
	}

	// The request type has no code/id fields, so a client cannot supply either.
	// A collision in the three-digit code must not become an identifier.
	if DisplayCode([]byte(testServiceKey), ch.ID, digest) != ch.DisplayCode {
		t.Fatal("display code is not deterministic")
	}
}

func TestChallengeBindingAndPendingUniqueness(t *testing.T) {
	f := newFixture(t, nil)
	digest := DigestRequest([]byte("request"))
	browser := BrowserSessionID("browser-a")
	ch := f.create(t, browser, digest)

	if ch.AccountID != f.account || ch.BrowserSessionID != browser || ch.DeviceID != f.device {
		t.Fatalf("challenge binding mismatch: %+v", ch)
	}
	if ch.RequestDigest != digest || ch.Risk != RiskMedium {
		t.Fatalf("challenge request binding mismatch: %+v", ch)
	}
	if ch.CreatedAt != 0 || ch.ExpiresAt != MonoTime(DefaultChallengeTTL) {
		t.Fatalf("timestamps = %s..%s", ch.CreatedAt, ch.ExpiresAt)
	}
	if ch.State != ChallengePending {
		t.Fatalf("state = %s, want pending", ch.State)
	}

	_, err := f.srv.CreateChallenge(f.request("browser-b", DigestRequest([]byte("other"))))
	if !errors.Is(err, ErrPendingExists) {
		t.Fatalf("second pending challenge error = %v, want ErrPendingExists", err)
	}

	// A different account is independent.
	pub2, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.srv.RegisterDevice("device-2", "acct-2", pub2); err != nil {
		t.Fatal(err)
	}
	req2 := ChallengeRequest{
		AccountID:        "acct-2",
		BrowserSessionID: browser,
		DeviceID:         "device-2",
		RequestDigest:    digest,
		Risk:             RiskLow,
	}
	if _, err := f.srv.CreateChallenge(req2); err != nil {
		t.Fatalf("different account should be independent: %v", err)
	}

	if err := f.srv.CancelChallenge(f.account, ch.ID); err != nil {
		t.Fatal(err)
	}
	if pending, ok := f.srv.PendingChallenge(f.account); ok {
		t.Fatalf("pending challenge survived cancellation: %s", pending.ID)
	}
	if _, err := f.srv.CreateChallenge(f.request(browser, digest)); !errors.Is(err, ErrCooldown) {
		t.Fatalf("replacement during cooldown error = %v, want ErrCooldown", err)
	}
	f.clock.Advance(DefaultCancelCooldown)
	replacement := f.create(t, browser, digest)
	if replacement.ID == ch.ID {
		t.Fatal("challenge id was reused")
	}
}

func TestPushFailureCountsButCreatesNoPending(t *testing.T) {
	injected := errors.New("push provider unavailable")
	f := newFixture(t, func(cfg *Config) {
		cfg.HourlyCreationLimit = 2
	})
	digest := DigestRequest([]byte("request"))

	ch, err := f.srv.CreateChallenge(f.request("browser-a", digest), func(Challenge) error {
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("send error = %v, want injected error", err)
	}
	if ch == nil || ch.State != ChallengeSendFailed {
		t.Fatalf("failed send challenge = %+v", ch)
	}
	if _, ok := f.srv.PendingChallenge(f.account); ok {
		t.Fatal("failed send produced a pending challenge")
	}
	status := f.srv.AccountStatus(f.account)
	if status.HourlyCreations != 1 {
		t.Fatalf("hourly creations = %d, want 1", status.HourlyCreations)
	}

	// The failed attempt consumed allowance but not the pending slot.
	okChallenge := f.create(t, "browser-b", digest)
	if okChallenge.State != ChallengePending {
		t.Fatalf("state = %s", okChallenge.State)
	}
	if status := f.srv.AccountStatus(f.account); status.HourlyCreations != 2 {
		t.Fatalf("hourly creations = %d, want 2", status.HourlyCreations)
	}

	// Cancel the pending challenge, wait out cooldown, and confirm the limit.
	if err := f.srv.CancelChallenge(f.account, okChallenge.ID); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(DefaultCancelCooldown)
	_, err = f.srv.CreateChallenge(f.request("browser-c", digest))
	mustErr(t, err, ErrHourlyLimit)
}

func TestPushFailureUsesSlidingHourWindow(t *testing.T) {
	injected := errors.New("push failed")
	f := newFixture(t, func(cfg *Config) {
		cfg.HourlyCreationLimit = 2
	})
	digest := DigestRequest([]byte("request"))

	for i := 0; i < 2; i++ {
		_, err := f.srv.CreateChallenge(f.request(BrowserSessionID(fmt.Sprintf("b%d", i)), digest), func(Challenge) error {
			return injected
		})
		if !errors.Is(err, injected) {
			t.Fatalf("attempt %d error = %v", i, err)
		}
		if i == 0 {
			f.clock.Advance(30 * time.Minute)
		}
	}
	_, err := f.srv.CreateChallenge(f.request("b-limit", digest), func(Challenge) error { return injected })
	mustErr(t, err, ErrHourlyLimit)

	f.clock.Advance(31 * time.Minute) // t0 rolls out of the one-hour window
	_, err = f.srv.CreateChallenge(f.request("b-after-window", digest), func(Challenge) error { return injected })
	if errors.Is(err, ErrHourlyLimit) {
		t.Fatal("old creation should have rolled out of the sliding window")
	}
}

func TestCancelWhileSendingCannotResurrectPending(t *testing.T) {
	f := newFixture(t, nil)
	digest := DigestRequest([]byte("request"))
	started := make(chan Challenge, 1)
	release := make(chan struct{})
	done := make(chan struct {
		ch  *Challenge
		err error
	}, 1)

	go func() {
		ch, err := f.srv.CreateChallenge(f.request("browser-a", digest), func(ch Challenge) error {
			started <- ch
			<-release
			return nil
		})
		done <- struct {
			ch  *Challenge
			err error
		}{ch: ch, err: err}
	}()

	ch := <-started
	if ch.State != ChallengeSending {
		t.Fatalf("sender saw state %s, want sending", ch.State)
	}
	if err := f.srv.CancelChallenge(f.account, ch.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	result := <-done
	if !errors.Is(result.err, ErrChallengeCancelled) {
		t.Fatalf("callback result error = %v, want ErrChallengeCancelled", result.err)
	}
	snapshot, ok := f.srv.GetChallenge(ch.ID)
	if !ok || snapshot.State != ChallengeCancelled {
		t.Fatalf("challenge after callback = %+v", snapshot)
	}
	if _, ok := f.srv.PendingChallenge(f.account); ok {
		t.Fatal("cancelled callback resurrected a pending challenge")
	}
	if _, err := f.srv.CreateChallenge(f.request("browser-b", digest)); !errors.Is(err, ErrCooldown) {
		t.Fatalf("replacement error = %v, want ErrCooldown", err)
	}
	f.clock.Advance(DefaultCancelCooldown)
	if _, err := f.srv.CreateChallenge(f.request("browser-b", digest)); err != nil {
		t.Fatalf("replacement after cooldown: %v", err)
	}
}

func TestResponseSignatureCoversEveryBindingField(t *testing.T) {
	f := newFixture(t, nil)
	ch := f.create(t, "browser-a", DigestRequest([]byte("request")))
	base, err := ch.SigningMessage(DecisionApprove, ch.DisplayCode)
	if err != nil {
		t.Fatal(err)
	}

	mutations := []struct {
		name string
		fn   func(*Challenge)
	}{
		{"account", func(c *Challenge) { c.AccountID = "other" }},
		{"browser", func(c *Challenge) { c.BrowserSessionID = "other" }},
		{"device", func(c *Challenge) { c.DeviceID = "other" }},
		{"challenge-id", func(c *Challenge) { c.ID[0] ^= 1 }},
		{"display-code", func(c *Challenge) { c.DisplayCode = "999" }},
		{"digest", func(c *Challenge) { c.RequestDigest[0] ^= 1 }},
		{"risk", func(c *Challenge) { c.Risk = RiskCritical }},
		{"created", func(c *Challenge) { c.CreatedAt++ }},
		{"expires", func(c *Challenge) { c.ExpiresAt++ }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			mutated := *ch
			test.fn(&mutated)
			msg, err := mutated.SigningMessage(DecisionApprove, mutated.DisplayCode)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(msg, base) {
				t.Fatalf("%s is not covered by the signed message", test.name)
			}
		})
	}

	deny, err := ch.SigningMessage(DecisionDeny, "")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(deny, base) {
		t.Fatal("decision is not covered by the signed message")
	}
}

func TestApprovalRequiresSignatureCodeAndOneTimeTransition(t *testing.T) {
	f := newFixture(t, nil)
	ch := f.create(t, "browser-a", DigestRequest([]byte("request")))

	wrongCode, err := SignResponse(f.priv, *ch, DecisionApprove, "999")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.srv.Respond(wrongCode)
	mustErr(t, err, ErrDisplayCodeMismatch)
	if snapshot, _ := f.srv.GetChallenge(ch.ID); snapshot.State != ChallengePending {
		t.Fatalf("wrong code changed state to %s", snapshot.State)
	}

	// Signature by an unregistered key must fail even with identical fields.
	_, otherKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	forged, err := SignApproval(otherKey, *ch)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.srv.Respond(forged)
	mustErr(t, err, ErrInvalidSignature)

	resp, err := SignApproval(f.priv, *ch)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := f.srv.Respond(resp)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != ChallengeApproved {
		t.Fatalf("state = %s, want approved", updated.State)
	}
	if _, err := f.srv.Respond(resp); !errors.Is(err, ErrChallengeNotPending) {
		t.Fatalf("second transition error = %v, want ErrChallengeNotPending", err)
	}
}

func TestSignedResponseTamperingIsRejected(t *testing.T) {
	f := newFixture(t, nil)
	ch := f.create(t, "browser-a", DigestRequest([]byte("request")))
	resp, err := SignApproval(f.priv, *ch)
	if err != nil {
		t.Fatal(err)
	}

	mutations := []struct {
		name string
		fn   func(*DeviceResponse)
	}{
		{"account", func(r *DeviceResponse) { r.AccountID = "other" }},
		{"browser", func(r *DeviceResponse) { r.BrowserSessionID = "other" }},
		{"device", func(r *DeviceResponse) { r.DeviceID = "other" }},
		{"challenge-id", func(r *DeviceResponse) { r.ChallengeID[0] ^= 1 }},
		{"display-code", func(r *DeviceResponse) { r.DisplayCode = "000" }},
		{"digest", func(r *DeviceResponse) { r.RequestDigest[0] ^= 1 }},
		{"risk", func(r *DeviceResponse) { r.Risk = RiskCritical }},
		{"created", func(r *DeviceResponse) { r.CreatedAt++ }},
		{"expires", func(r *DeviceResponse) { r.ExpiresAt++ }},
		{"decision", func(r *DeviceResponse) { r.Decision = DecisionDeny }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			mutated := resp
			test.fn(&mutated)
			if _, err := f.srv.Respond(mutated); err == nil {
				t.Fatal("tampered response was accepted")
			}
			if snapshot, _ := f.srv.GetChallenge(ch.ID); snapshot.State != ChallengePending {
				t.Fatalf("tampered response changed state to %s", snapshot.State)
			}
		})
	}
}

func TestConcurrentApprovalHasOneWinner(t *testing.T) {
	f := newFixture(t, nil)
	ch := f.create(t, "browser-a", DigestRequest([]byte("request")))
	resp, err := SignApproval(f.priv, *ch)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 32
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			_, err := f.srv.Respond(resp)
			results <- err
		}()
	}
	wins := 0
	for i := 0; i < workers; i++ {
		err := <-results
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrChallengeNotPending) {
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("successful approvals = %d, want exactly 1", wins)
	}

	tx, err := f.srv.BeginLogin(f.account, "browser-a")
	if err != nil {
		t.Fatal(err)
	}
	sessionResults := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			_, err := f.srv.ConsumeApproval(tx.ID, ch.ID)
			sessionResults <- err
		}()
	}
	sessionWins := 0
	for i := 0; i < workers; i++ {
		if err := <-sessionResults; err == nil {
			sessionWins++
		}
	}
	if sessionWins != 1 {
		t.Fatalf("successful consumptions = %d, want exactly 1", sessionWins)
	}
}

func TestConsumptionIsBoundToBrowserAndTransaction(t *testing.T) {
	f := newFixture(t, nil)
	digest := DigestRequest([]byte("user-request"))
	ch := f.create(t, "user-browser", digest)
	resp, err := SignApproval(f.priv, *ch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.Respond(resp); err != nil {
		t.Fatal(err)
	}

	userTx, err := f.srv.BeginLogin(f.account, "user-browser")
	if err != nil {
		t.Fatal(err)
	}
	attackerTx, err := f.srv.BeginLogin(f.account, "attacker-browser")
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.srv.ConsumeApproval(attackerTx.ID, ch.ID)
	mustErr(t, err, ErrBrowserMismatch)
	if snapshot, _ := f.srv.GetChallenge(ch.ID); snapshot.State != ChallengeApproved {
		t.Fatalf("browser mismatch changed state to %s", snapshot.State)
	}

	session, err := f.srv.ConsumeApproval(userTx.ID, ch.ID, ch.DisplayCode)
	if err != nil {
		t.Fatal(err)
	}
	if session.AccountID != f.account || session.BrowserSessionID != "user-browser" ||
		session.ChallengeID != ch.ID || session.RequestDigest != digest {
		t.Fatalf("session binding mismatch: %+v", session)
	}
	if snapshot, _ := f.srv.GetChallenge(ch.ID); snapshot.State != ChallengeCompleted {
		t.Fatalf("consumed challenge state = %s, want completed", snapshot.State)
	}
	if tx, _ := f.srv.GetTransaction(userTx.ID); tx.State != TransactionConsumed {
		t.Fatalf("transaction state = %s, want consumed", tx.State)
	}
	_, err = f.srv.ConsumeApproval(userTx.ID, ch.ID)
	mustErr(t, err, ErrTransactionClosed)
}

func TestOldChallengeLateApproveAndCodeCollision(t *testing.T) {
	digest := DigestRequest([]byte("collision-request"))
	id1, id2 := findCollidingIDs(t, []byte(testServiceKey), digest)
	raw := append(append([]byte(nil), id1[:]...), id2[:]...)
	raw = append(raw, make([]byte, 256)...) // room for session/token draws

	f := newFixture(t, func(cfg *Config) {
		cfg.Rand = bytes.NewReader(raw)
	})
	oldChallenge := f.create(t, "browser-a", digest)
	if oldChallenge.ID != id1 {
		t.Fatalf("first challenge id = %s, want %s", oldChallenge.ID, id1)
	}
	if err := f.srv.CancelChallenge(f.account, oldChallenge.ID); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(DefaultCancelCooldown)
	newChallenge := f.create(t, "browser-a", digest)
	if newChallenge.ID != id2 {
		t.Fatalf("second challenge id = %s, want %s", newChallenge.ID, id2)
	}
	if oldChallenge.DisplayCode != newChallenge.DisplayCode {
		t.Fatalf("test setup did not produce a code collision: %q vs %q", oldChallenge.DisplayCode, newChallenge.DisplayCode)
	}

	late, err := SignApproval(f.priv, *oldChallenge)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.srv.Respond(late)
	mustErr(t, err, ErrChallengeCancelled)
	if snapshot, _ := f.srv.GetChallenge(oldChallenge.ID); snapshot.State != ChallengeCancelled {
		t.Fatalf("late approval changed state to %s", snapshot.State)
	}

	// A signed response for the old challenge cannot be relabelled as the new
	// challenge merely because the displayed numbers collide.
	transplanted := late
	transplanted.ChallengeID = newChallenge.ID
	if _, err := f.srv.Respond(transplanted); err == nil {
		t.Fatal("old signed response was transplanted to the colliding challenge")
	}

	// Signing the new challenge with the colliding code is fine because the
	// signature still covers the new challenge id, digest, and browser.
	collisionResp, err := SignResponse(f.priv, *newChallenge, DecisionApprove, oldChallenge.DisplayCode)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := f.srv.Respond(collisionResp)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != ChallengeApproved {
		t.Fatalf("state = %s, want approved", updated.State)
	}
}

func findCollidingIDs(t *testing.T, key []byte, digest RequestDigest) (ChallengeID, ChallengeID) {
	t.Helper()
	seen := make(map[string]ChallengeID)
	for i := uint64(1); i < 100000; i++ {
		var id ChallengeID
		binary.BigEndian.PutUint64(id[:8], i)
		binary.BigEndian.PutUint64(id[8:], i^0x9e3779b97f4a7c15)
		code := DisplayCode(key, id, digest)
		if previous, ok := seen[code]; ok {
			return previous, id
		}
		seen[code] = id
	}
	t.Fatal("could not find a three-digit code collision")
	return ChallengeID{}, ChallengeID{}
}

func TestTimeoutAndPolicyInvalidation(t *testing.T) {
	t.Run("pending timeout", func(t *testing.T) {
		f := newFixture(t, nil)
		ch := f.create(t, "browser-a", DigestRequest([]byte("request")))
		f.clock.Advance(DefaultChallengeTTL + time.Nanosecond)
		if n := f.srv.ExpireDue(); n == 0 {
			t.Fatal("ExpireDue did not expire pending challenge")
		}
		if snapshot, _ := f.srv.GetChallenge(ch.ID); snapshot.State != ChallengeExpired {
			t.Fatalf("state = %s, want expired", snapshot.State)
		}
		resp, err := SignApproval(f.priv, *ch)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.srv.Respond(resp)
		mustErr(t, err, ErrChallengeExpired)
	})

	t.Run("approved timeout", func(t *testing.T) {
		f := newFixture(t, nil)
		ch := f.create(t, "browser-a", DigestRequest([]byte("request")))
		resp, _ := SignApproval(f.priv, *ch)
		if _, err := f.srv.Respond(resp); err != nil {
			t.Fatal(err)
		}
		tx, _ := f.srv.BeginLogin(f.account, "browser-a")
		f.clock.Advance(DefaultChallengeTTL + time.Nanosecond)
		_, err := f.srv.ConsumeApproval(tx.ID, ch.ID)
		mustErr(t, err, ErrChallengeExpired)
		if snapshot, _ := f.srv.GetChallenge(ch.ID); snapshot.State != ChallengeExpired {
			t.Fatalf("state = %s, want expired", snapshot.State)
		}
	})

	t.Run("password change", func(t *testing.T) {
		f := newFixture(t, nil)
		ch := f.create(t, "browser-a", DigestRequest([]byte("request")))
		resp, _ := SignApproval(f.priv, *ch)
		if _, err := f.srv.Respond(resp); err != nil {
			t.Fatal(err)
		}
		if err := f.srv.ChangePassword(f.account); err != nil {
			t.Fatal(err)
		}
		if snapshot, _ := f.srv.GetChallenge(ch.ID); snapshot.State != ChallengeInvalidated {
			t.Fatalf("state = %s, want invalidated", snapshot.State)
		}
		_, err := f.srv.Respond(resp)
		mustErr(t, err, ErrChallengeInvalidated)
		tx, _ := f.srv.BeginLogin(f.account, "browser-a")
		if _, err := f.srv.ConsumeApproval(tx.ID, ch.ID); !errors.Is(err, ErrChallengeInvalidated) {
			t.Fatalf("consume after password change = %v", err)
		}
	})

	t.Run("risk escalation", func(t *testing.T) {
		f := newFixture(t, nil)
		ch := f.create(t, "browser-a", DigestRequest([]byte("request")))
		resp, _ := SignApproval(f.priv, *ch)
		if _, err := f.srv.Respond(resp); err != nil {
			t.Fatal(err)
		}
		if err := f.srv.EscalateRisk(f.account, RiskCritical); err != nil {
			t.Fatal(err)
		}
		if snapshot, _ := f.srv.GetChallenge(ch.ID); snapshot.State != ChallengeInvalidated {
			t.Fatalf("state = %s, want invalidated", snapshot.State)
		}
		tx, _ := f.srv.BeginLogin(f.account, "browser-a")
		if _, err := f.srv.ConsumeApproval(tx.ID, ch.ID); !errors.Is(err, ErrChallengeInvalidated) {
			t.Fatalf("consume after risk escalation = %v", err)
		}
	})

	t.Run("device revocation", func(t *testing.T) {
		f := newFixture(t, nil)
		ch := f.create(t, "browser-a", DigestRequest([]byte("request")))
		resp, _ := SignApproval(f.priv, *ch)
		if _, err := f.srv.Respond(resp); err != nil {
			t.Fatal(err)
		}
		if err := f.srv.RevokeDevice(f.device); err != nil {
			t.Fatal(err)
		}
		if snapshot, _ := f.srv.GetChallenge(ch.ID); snapshot.State != ChallengeInvalidated {
			t.Fatalf("state = %s, want invalidated", snapshot.State)
		}
		tx, _ := f.srv.BeginLogin(f.account, "browser-a")
		_, err := f.srv.ConsumeApproval(tx.ID, ch.ID)
		mustErr(t, err, ErrChallengeInvalidated)
	})
}

func TestConsecutiveDenialLockIsServerSideAndResettable(t *testing.T) {
	f := newFixture(t, func(cfg *Config) {
		cfg.DenialLockThreshold = 2
		cfg.DenialLockDuration = 90 * time.Second
	})
	digest := DigestRequest([]byte("request"))

	first := f.create(t, "browser-a", digest)
	resp, _ := SignDenial(f.priv, *first)
	if _, err := f.srv.Respond(resp); err != nil {
		t.Fatal(err)
	}
	status := f.srv.AccountStatus(f.account)
	if status.ConsecutiveDenials != 1 {
		t.Fatalf("denials = %d, want 1", status.ConsecutiveDenials)
	}

	second := f.create(t, "browser-a", digest)
	resp, _ = SignDenial(f.priv, *second)
	if _, err := f.srv.Respond(resp); err != nil {
		t.Fatal(err)
	}
	status = f.srv.AccountStatus(f.account)
	if status.ConsecutiveDenials != 2 || !status.LockedUntil.After(f.clock.Now()) {
		t.Fatalf("lock status = %+v", status)
	}
	_, err := f.srv.CreateChallenge(f.request("browser-a", digest))
	mustErr(t, err, ErrAccountLocked)

	f.clock.Advance(90*time.Second + time.Nanosecond)
	third := f.create(t, "browser-a", digest)
	approve, _ := SignApproval(f.priv, *third)
	if _, err := f.srv.Respond(approve); err != nil {
		t.Fatal(err)
	}
	if status := f.srv.AccountStatus(f.account); status.ConsecutiveDenials != 0 {
		t.Fatalf("approval did not reset denials: %+v", status)
	}
}

func TestChallengeIDsAndBrowserPairsAreUnique(t *testing.T) {
	f := newFixture(t, func(cfg *Config) {
		cfg.HourlyCreationLimit = 50
	})
	seenIDs := make(map[ChallengeID]struct{})
	digest := DigestRequest([]byte("request"))
	for i := 0; i < 10; i++ {
		browser := BrowserSessionID(fmt.Sprintf("browser-%d", i))
		ch := f.create(t, browser, digest)
		if _, exists := seenIDs[ch.ID]; exists {
			t.Fatalf("challenge id reused: %s", ch.ID)
		}
		seenIDs[ch.ID] = struct{}{}
		if err := f.srv.CancelChallenge(f.account, ch.ID); err != nil {
			t.Fatal(err)
		}
		f.clock.Advance(DefaultCancelCooldown)
	}
}

func TestConfigurationDefaults(t *testing.T) {
	srv, err := NewServer(Config{ServiceKey: []byte("key")})
	if err != nil {
		t.Fatal(err)
	}
	if srv.cfg.ChallengeTTL != DefaultChallengeTTL ||
		srv.cfg.CancelCooldown != 30*time.Second ||
		srv.cfg.HourlyCreationLimit != DefaultHourlyLimit {
		t.Fatalf("defaults = %+v", srv.cfg)
	}
	if _, err := NewServer(Config{}); !errors.Is(err, ErrServiceKeyRequired) {
		t.Fatalf("empty key error = %v", err)
	}
}

func TestDeterministicSearchFindsNoUnauthorizedAttackerSession(t *testing.T) {
	makeFixture := func() *fixture {
		f := newFixture(t, nil)
		return f
	}
	f := makeFixture()

	cfg := AuditConfig{
		AccountID:        f.account,
		DeviceID:         f.device,
		DevicePrivateKey: f.priv,
		UserBrowser:      "user-browser",
		AttackerBrowser:  "attacker-browser",
		UserDigest:       DigestRequest([]byte("user-login")),
		AttackerDigest:   DigestRequest([]byte("attacker-login")),
	}
	auditor, err := NewAuditor(f.srv, cfg)
	if err != nil {
		t.Fatal(err)
	}
	result, err := auditor.Search(SearchOptions{
		MaxDepth:             5,
		MaxNodes:             200000,
		IncludeDenials:       true,
		IncludeAccountEvents: true,
		IncludeSendFailures:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Found {
		t.Fatalf("found unauthorized attacker session after %d steps: %+v", len(result.Trace), result.Trace)
	}
	if result.Explored == 0 {
		t.Fatal("search explored no states")
	}
	if result.ActionCounts["approve"] == 0 {
		t.Fatal("search never exercised an approval")
	}
	if result.ActionCounts["consume-attacker"] == 0 {
		t.Fatal("search never exercised attacker consumption")
	}
	if result.ActionCounts["replay-approval"] == 0 {
		t.Fatal("search never exercised a late or duplicate device response")
	}
	if result.ActionCounts["create-push-failed"] == 0 {
		t.Fatal("search never exercised a failed push send")
	}

	// The scheduler must be deterministic and must not mutate the base server.
	f2 := makeFixture()
	cfg2 := cfg
	cfg2.AccountID = f2.account
	cfg2.DeviceID = f2.device
	cfg2.DevicePrivateKey = f2.priv
	auditor2, err := NewAuditor(f2.srv, cfg2)
	if err != nil {
		t.Fatal(err)
	}
	result2, err := auditor2.Search(SearchOptions{
		MaxDepth:             5,
		MaxNodes:             200000,
		IncludeDenials:       true,
		IncludeAccountEvents: true,
		IncludeSendFailures:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.ActionCounts, result2.ActionCounts) || result.Explored != result2.Explored {
		t.Fatalf("search is not deterministic:\n%+v\n%+v", result, result2)
	}
	if pending, ok := f.srv.PendingChallenge(f.account); ok {
		t.Fatalf("search mutated the base server: %s", pending.ID)
	}
}
