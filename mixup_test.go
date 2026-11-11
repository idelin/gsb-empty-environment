package netsec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestCrossCallbackMixUp is the core IdP mix-up scenario: a response produced
// by provider B is presented to the transaction pinned to provider A. The
// manager must reject it before any token exchange whenever possible, and
// leave both transactions intact so the legitimate callbacks still complete.
func TestCrossCallbackMixUp(t *testing.T) {
	mgr := NewManager(time.Minute)

	providerA := NewFakeProvider("https://idp-a.example.com", []byte("secret-a"))
	providerB := NewFakeProvider("https://idp-b.example.com", []byte("secret-b"))

	sessionA := "browser-session-A"
	sessionB := "browser-session-B"

	loginA := beginLogin(t, mgr, providerA, sessionA)
	loginB := beginLogin(t, mgr, providerB, sessionB)

	codeA, stateA, issA := followAuthorize(t, providerA, loginA.tx)
	codeB, stateB, issB := followAuthorize(t, providerB, loginB.tx)

	// Attacker replays B's response against A's browser session / callback
	// route. Three increasingly aggressive mix-up variants are tried.
	attackerCallbacks := []Callback{
		{
			// B's state presented on A's session: unknown state outright.
			State:            stateB,
			Code:             codeB,
			Issuer:           issB,
			BrowserSessionID: sessionA,
			CodeVerifier:     loginA.verifier,
			RedirectURI:      testRedirectURI,
		},
		{
			// B's code carried inside A's state, claiming B's issuer:
			// the transaction-pinned issuer rejects it pre-exchange.
			State:            stateA,
			Code:             codeB,
			Issuer:           issB,
			BrowserSessionID: sessionA,
			CodeVerifier:     loginA.verifier,
			RedirectURI:      testRedirectURI,
		},
		{
			// B's code inside A's state, forged to claim A's issuer:
			// the pinned exchanger hits A, which does not know B's
			// code; A's transaction must survive the failure.
			State:            stateA,
			Code:             codeB,
			Issuer:           issA,
			BrowserSessionID: sessionA,
			CodeVerifier:     loginA.verifier,
			RedirectURI:      testRedirectURI,
		},
	}

	for i, cb := range attackerCallbacks {
		_, err := mgr.Complete(context.Background(), cb)
		if err == nil {
			t.Fatalf("attack callback %d: expected rejection, got success", i)
		}
		if errors.Is(err, ErrTransactionBusy) {
			t.Fatalf("attack callback %d: transaction unexpectedly busy", i)
		}

		// Neither transaction may be consumed by a failed attack.
		if _, ok := mgr.Lookup(stateA); !ok {
			t.Fatalf("attack callback %d: A's transaction was burned", i)
		}
		if _, ok := mgr.Lookup(stateB); !ok {
			t.Fatalf("attack callback %d: B's transaction was burned", i)
		}
	}

	// The first two variants never reach a token endpoint. The third reaches
	// A's endpoint once; B's endpoint is never touched by A's transaction.
	if got := providerA.ExchangeAttempts(); got != 1 {
		t.Fatalf("provider A exchanges = %d, want 1", got)
	}
	if got := providerB.ExchangeAttempts(); got != 0 {
		t.Fatalf("provider B exchanges = %d, want 0", got)
	}

	// Legitimate callbacks still complete; each transaction is consumed once.
	resultA, err := mgr.Complete(context.Background(),
		callbackFor(loginA, codeA, stateA, issA, sessionA))
	if err != nil {
		t.Fatalf("legit A completion: %v", err)
	}
	if resultA.Claims.Issuer() != providerA.Issuer() {
		t.Fatalf("A claims issuer = %q", resultA.Claims.Issuer())
	}

	resultB, err := mgr.Complete(context.Background(),
		callbackFor(loginB, codeB, stateB, issB, sessionB))
	if err != nil {
		t.Fatalf("legit B completion: %v", err)
	}
	if resultB.Claims.Issuer() != providerB.Issuer() {
		t.Fatalf("B claims issuer = %q", resultB.Claims.Issuer())
	}

	if mgr.Len() != 0 {
		t.Fatalf("pending = %d, want 0 after both completions", mgr.Len())
	}
	if _, err := mgr.Complete(context.Background(),
		callbackFor(loginA, codeA, stateA, issA, sessionA)); !errors.Is(err, ErrUnknownTransaction) {
		t.Fatalf("replay after consumption: got %v, want ErrUnknownTransaction", err)
	}
	if !providerA.IsCodeConsumed(codeA) || !providerB.IsCodeConsumed(codeB) {
		t.Fatal("provider-side codes not marked consumed")
	}
}

// TestTwoAccountsConcurrentLogin runs two full logins for different browser
// sessions in parallel, proving per-transaction isolation under concurrency.
func TestTwoAccountsConcurrentLogin(t *testing.T) {
	mgr := NewManager(time.Minute)

	idp := NewFakeProvider("https://idp.example.com", []byte("shared-secret"))

	type session struct {
		name      string
		sessionID string
		setup     loginSetup
		code      string
		state     string
		iss       string
		result    *Result
		err       error
	}

	sessions := make([]*session, 2)
	for i := range sessions {
		sid := "browser-" + string(rune('A'+i))
		setup := beginLogin(t, mgr, idp, sid)
		code, state, iss := followAuthorize(t, idp, setup.tx)
		sessions[i] = &session{
			name:      sid,
			sessionID: sid,
			setup:     setup,
			code:      code,
			state:     state,
			iss:       iss,
		}
	}

	if mgr.Len() != 2 {
		t.Fatalf("pending = %d, want 2", mgr.Len())
	}

	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func(s *session) {
			defer wg.Done()
			s.result, s.err = mgr.Complete(context.Background(),
				callbackFor(s.setup, s.code, s.state, s.iss, s.sessionID))
		}(s)
	}
	wg.Wait()

	subjects := make(map[string]string)
	for _, s := range sessions {
		if s.err != nil {
			t.Fatalf("%s completion: %v", s.name, s.err)
		}
		if s.result.Transaction.BrowserSession() != s.sessionID {
			t.Fatalf("%s got transaction of another session", s.name)
		}
		subjects[s.result.Claims.Subject()] = s.name
	}
	if len(subjects) != 2 {
		t.Fatalf("expected two distinct subjects, got %v", subjects)
	}
	if mgr.Len() != 0 {
		t.Fatalf("pending = %d, want 0", mgr.Len())
	}
}
