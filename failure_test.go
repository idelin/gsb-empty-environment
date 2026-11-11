package netsec

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestExchangeTimeoutLeavesTransactionIntact makes the token endpoint stall
// past the client deadline. The exchange fails but the transaction must not
// be consumed: a later retry against the recovered provider succeeds.
func TestExchangeTimeoutLeavesTransactionIntact(t *testing.T) {
	mgr := NewManager(time.Minute)
	idp := NewFakeProvider("https://slow-idp.example.com", []byte("slow-secret"))

	// Short HTTP timeout so the test stays fast.
	exchanger := idp.NewExchanger()
	exchanger.HTTPClient.Timeout = 120 * time.Millisecond

	sessionID := "browser-slow"
	verifier, err := NewCodeVerifier()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := mgr.Begin(BeginParams{
		BrowserSessionID:    sessionID,
		Issuer:              idp.Issuer(),
		CodeChallenge:       S256Challenge(verifier),
		CodeChallengeMethod: "S256",
		RedirectURI:         testRedirectURI,
		Exchanger:           exchanger,
		Validator:           idp.NewValidator(),
	})
	if err != nil {
		t.Fatal(err)
	}

	idp.TokenDelay = 2 * time.Second
	code, state, iss := followAuthorize(t, idp, tx)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err = mgr.Complete(ctx, Callback{
		State:            state,
		Code:             code,
		Issuer:           iss,
		BrowserSessionID: sessionID,
		CodeVerifier:     verifier,
		RedirectURI:      testRedirectURI,
	}); err == nil {
		t.Fatal("expected timeout error, got success")
	}

	if _, ok := mgr.Lookup(state); !ok {
		t.Fatal("transaction was consumed despite exchange timeout")
	}
	if idp.IsCodeConsumed(code) {
		t.Fatal("authorization code marked consumed even though the client gave up")
	}

	// Provider recovers: the same pinned transaction completes on retry.
	idp.TokenDelay = 0
	result, err := mgr.Complete(context.Background(), Callback{
		State:            state,
		Code:             code,
		Issuer:           iss,
		BrowserSessionID: sessionID,
		CodeVerifier:     verifier,
		RedirectURI:      testRedirectURI,
	})
	if err != nil {
		t.Fatalf("retry after timeout: %v", err)
	}
	if result.Claims.Subject() == "" {
		t.Fatal("retry succeeded without identity claims")
	}
	if mgr.Len() != 0 {
		t.Fatalf("pending = %d, want 0", mgr.Len())
	}
}

// TestInvalidResponsesDoNotBurnTransaction exercises every failure mode that
// happens after the pinned checks pass. None of them may consume the
// transaction; a subsequent well-formed response completes the same
// transaction.
func TestInvalidResponsesDoNotBurnTransaction(t *testing.T) {
	cases := []struct {
		name    string
		perturb func(*FakeProvider)
		want    string
	}{
		{
			name:    "token endpoint 500",
			perturb: func(fp *FakeProvider) { fp.TokenStatusCode = http.StatusInternalServerError },
			want:    "status 500",
		},
		{
			name:    "malformed json",
			perturb: func(fp *FakeProvider) { fp.MalformedJSON = true },
			want:    "malformed token response",
		},
		{
			name:    "missing id token",
			perturb: func(fp *FakeProvider) { fp.NoIDToken = true },
			want:    "no ID token",
		},
		{
			name:    "bad token signature",
			perturb: func(fp *FakeProvider) { fp.WrongSecret = []byte("attacker-secret") },
			want:    "signature mismatch",
		},
		{
			name:    "wrong issuer in token",
			perturb: func(fp *FakeProvider) { fp.WrongIssuer = "https://evil.example.com" },
			want:    "issuer mismatch",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewManager(time.Minute)
			idp := NewFakeProvider(
				"https://idp-"+strings.ReplaceAll(tc.name, " ", "-")+".example.com",
				[]byte("case-secret"))

			setup := beginLogin(t, mgr, idp, "browser-case")
			tc.perturb(idp)
			code, state, iss := followAuthorize(t, idp, setup.tx)

			_, err := mgr.Complete(context.Background(),
				callbackFor(setup, code, state, iss, "browser-case"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Complete error = %v, want substring %q", err, tc.want)
			}

			if _, ok := mgr.Lookup(state); !ok {
				t.Fatal("transaction burned by invalid response")
			}

			// Recovery with a fresh, well-formed provider response: the
			// original transaction is still completable. A new code is
			// needed because provider-side state from failed attempts is
			// not guaranteed reusable.
			idp.TokenStatusCode = 0
			idp.MalformedJSON = false
			idp.NoIDToken = false
			idp.WrongSecret = nil
			idp.WrongIssuer = ""

			code2, state2, iss2 := followAuthorize(t, idp, setup.tx)
			if state2 != state {
				t.Fatalf("provider changed state: %q vs %q", state2, state)
			}
			result, err := mgr.Complete(context.Background(),
				callbackFor(setup, code2, state, iss2, "browser-case"))
			if err != nil {
				t.Fatalf("completion after recovery: %v", err)
			}
			if result.Claims.Issuer() != idp.Issuer() {
				t.Fatalf("issuer = %q, want %q", result.Claims.Issuer(), idp.Issuer())
			}
			if mgr.Len() != 0 {
				t.Fatalf("pending = %d, want 0", mgr.Len())
			}
		})
	}
}

// TestConcurrentCompletionIsSingleUse races two completions of one
// transaction. Exactly one may succeed; the loser is rejected with
// ErrTransactionBusy (overlap) or ErrUnknownTransaction (already consumed),
// and the authorization code is exchanged at most once.
func TestConcurrentCompletionIsSingleUse(t *testing.T) {
	mgr := NewManager(time.Minute)
	idp := NewFakeProvider("https://idp.example.com", []byte("race-secret"))

	setup := beginLogin(t, mgr, idp, "browser-race")
	code, state, iss := followAuthorize(t, idp, setup.tx)

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = mgr.Complete(context.Background(),
				callbackFor(setup, code, state, iss, "browser-race"))
		}(i)
	}
	wg.Wait()

	var successes, rejected int
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrTransactionBusy), errors.Is(err, ErrUnknownTransaction):
			rejected++
		default:
			t.Fatalf("unexpected completion error: %v", err)
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatalf("successes = %d, rejected = %d, want 1/1", successes, rejected)
	}
	if got := idp.ExchangeAttempts(); got != 1 {
		t.Fatalf("token endpoint calls = %d, want 1", got)
	}
	if mgr.Len() != 0 {
		t.Fatalf("pending = %d, want 0", mgr.Len())
	}
}

// TestPinnedBindingRejections checks every individual binding established in
// Begin. All rejections happen without network calls and preserve the
// transaction.
func TestPinnedBindingRejections(t *testing.T) {
	mgr := NewManager(time.Minute)
	idp := NewFakeProvider("https://idp.example.com", []byte("pin-secret"))

	setup := beginLogin(t, mgr, idp, "browser-1")
	code, state, iss := followAuthorize(t, idp, setup.tx)

	base := func() Callback {
		return callbackFor(setup, code, state, iss, "browser-1")
	}

	t.Run("unknown state", func(t *testing.T) {
		cb := base()
		cb.State = "not-a-real-state"
		if _, err := mgr.Complete(context.Background(), cb); !errors.Is(err, ErrUnknownTransaction) {
			t.Fatalf("got %v, want ErrUnknownTransaction", err)
		}
	})

	t.Run("wrong browser session", func(t *testing.T) {
		cb := base()
		cb.BrowserSessionID = "browser-2"
		if _, err := mgr.Complete(context.Background(), cb); !errors.Is(err, ErrSessionMismatch) {
			t.Fatalf("got %v, want ErrSessionMismatch", err)
		}
		if idp.ExchangeAttempts() != 0 {
			t.Fatal("network exchange happened before binding checks")
		}
	})

	t.Run("wrong issuer", func(t *testing.T) {
		cb := base()
		cb.Issuer = "https://other-idp.example.com"
		if _, err := mgr.Complete(context.Background(), cb); !errors.Is(err, ErrIssuerMismatch) {
			t.Fatalf("got %v, want ErrIssuerMismatch", err)
		}
		if idp.ExchangeAttempts() != 0 {
			t.Fatal("network exchange happened before issuer check")
		}
	})

	t.Run("wrong pkce verifier", func(t *testing.T) {
		cb := base()
		cb.CodeVerifier = "attacker-verifier"
		if _, err := mgr.Complete(context.Background(), cb); !errors.Is(err, ErrPKCEMismatch) {
			t.Fatalf("got %v, want ErrPKCEMismatch", err)
		}
	})

	t.Run("wrong redirect uri", func(t *testing.T) {
		cb := base()
		cb.RedirectURI = "https://evil.example.com/cb"
		if _, err := mgr.Complete(context.Background(), cb); !errors.Is(err, ErrRedirectURIMismatch) {
			t.Fatalf("got %v, want ErrRedirectURIMismatch", err)
		}
	})

	if _, ok := mgr.Lookup(state); !ok {
		t.Fatal("transaction burned by rejected callbacks")
	}

	// The untouched transaction still completes with the genuine callback.
	result, err := mgr.Complete(context.Background(), base())
	if err != nil {
		t.Fatalf("genuine completion: %v", err)
	}
	if result.Claims.Subject() == "" {
		t.Fatal("missing subject in claims")
	}
}

// TestExpiryRejectsAndPurges ensures stale transactions cannot be completed
// and that PurgeExpired reclaims them.
func TestExpiryRejectsAndPurges(t *testing.T) {
	mgr := NewManager(50 * time.Millisecond)
	idp := NewFakeProvider("https://idp.example.com", []byte("ttl-secret"))

	setup := beginLogin(t, mgr, idp, "browser-ttl")
	code, state, iss := followAuthorize(t, idp, setup.tx)

	time.Sleep(70 * time.Millisecond)

	if _, err := mgr.Complete(context.Background(),
		callbackFor(setup, code, state, iss, "browser-ttl")); !errors.Is(err, ErrUnknownTransaction) {
		t.Fatalf("expired completion: got %v, want ErrUnknownTransaction", err)
	}

	// Expired entries are still resident until a purge runs.
	if mgr.Len() != 1 {
		t.Fatalf("before purge len = %d, want 1", mgr.Len())
	}
	if removed := mgr.PurgeExpired(); removed != 1 {
		t.Fatalf("purged = %d, want 1", removed)
	}
	if mgr.Len() != 0 {
		t.Fatalf("after purge len = %d, want 0", mgr.Len())
	}
}

// TestBeginValidation covers the mandatory pinning inputs.
func TestBeginValidation(t *testing.T) {
	mgr := NewManager(time.Minute)
	idp := NewFakeProvider("https://idp.example.com", []byte("v-secret"))

	verifier, _ := NewCodeVerifier()
	good := BeginParams{
		BrowserSessionID:    "s",
		Issuer:              idp.Issuer(),
		CodeChallenge:       S256Challenge(verifier),
		CodeChallengeMethod: "S256",
		RedirectURI:         testRedirectURI,
		Exchanger:           idp.NewExchanger(),
		Validator:           idp.NewValidator(),
	}

	modify := func(fn func(*BeginParams)) BeginParams {
		p := good
		fn(&p)
		return p
	}

	bad := []BeginParams{
		modify(func(p *BeginParams) { p.BrowserSessionID = "" }),
		modify(func(p *BeginParams) { p.Issuer = "" }),
		modify(func(p *BeginParams) { p.CodeChallenge = "" }),
		modify(func(p *BeginParams) { p.CodeChallengeMethod = "plain" }),
		modify(func(p *BeginParams) { p.RedirectURI = "" }),
		modify(func(p *BeginParams) { p.Exchanger = nil }),
		modify(func(p *BeginParams) { p.Validator = nil }),
	}
	for i, params := range bad {
		if _, err := mgr.Begin(params); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}

	tx, err := mgr.Begin(good)
	if err != nil {
		t.Fatalf("valid begin: %v", err)
	}
	if tx.State() == "" || tx.Nonce() == "" {
		t.Fatal("state/nonce not generated")
	}
	if tx.Issuer() != idp.Issuer() || tx.BrowserSession() != "s" {
		t.Fatal("pinned fields not recorded")
	}
}
