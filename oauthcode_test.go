package oauthcode

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	testClientID = "client-123"
	testRedirect = "https://app.example/callback"
	testVerifier = "correct-horse-battery-staple-verifier"
)

func s256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func newTestServer(ttl time.Duration) (*Server, *ManualClock) {
	clock := NewManualClock(time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC))
	return NewServer(clock, ttl), clock
}

func issueTestCode(t *testing.T, s *Server) string {
	t.Helper()
	code, err := s.IssueCode(testClientID, testRedirect, s256Challenge(testVerifier), "S256")
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	return code
}

func TestIssueRequiresS256(t *testing.T) {
	s, _ := newTestServer(time.Minute)
	if _, err := s.IssueCode(testClientID, testRedirect, "abc", "plain"); !errors.Is(err, ErrUnsupportedMethod) {
		t.Fatalf("plain method: got %v, want ErrUnsupportedMethod", err)
	}
	if _, err := s.IssueCode(testClientID, testRedirect, "", "S256"); !errors.Is(err, ErrMissingChallenge) {
		t.Fatalf("empty challenge: got %v, want ErrMissingChallenge", err)
	}
}

func TestRedeemSuccess(t *testing.T) {
	s, _ := newTestServer(time.Minute)
	code := issueTestCode(t, s)
	tok, err := s.Redeem(code, testClientID, testRedirect, testVerifier)
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if !s.IsTokenActive(tok) {
		t.Fatal("freshly issued token should be active")
	}
}

func TestRedeemValidationFailures(t *testing.T) {
	cases := []struct {
		name                                  string
		code, clientID, redirectURI, verifier string
		want                                  error
	}{
		{"unknown code", "no-such-code", testClientID, testRedirect, testVerifier, ErrInvalidGrant},
		{"wrong client", "", "other-client", testRedirect, testVerifier, ErrClientMismatch},
		{"redirect trailing slash", "", testClientID, testRedirect + "/", testVerifier, ErrRedirectMismatch},
		{"redirect case differs", "", testClientID, "HTTPS://app.example/callback", testVerifier, ErrRedirectMismatch},
		{"redirect empty", "", testClientID, "", testVerifier, ErrRedirectMismatch},
		{"bad verifier", "", testClientID, testRedirect, "wrong-verifier", ErrPKCEMismatch},
		{"empty verifier", "", testClientID, testRedirect, "", ErrPKCEMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestServer(time.Minute)
			code := issueTestCode(t, s)
			if tc.code != "" {
				code = tc.code
			}
			if _, err := s.Redeem(code, tc.clientID, tc.redirectURI, tc.verifier); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestFailedValidationDoesNotConsumeCode(t *testing.T) {
	s, _ := newTestServer(time.Minute)
	code := issueTestCode(t, s)
	if _, err := s.Redeem(code, testClientID, testRedirect+"/", testVerifier); !errors.Is(err, ErrRedirectMismatch) {
		t.Fatalf("got %v, want ErrRedirectMismatch", err)
	}
	if _, err := s.Redeem(code, testClientID, testRedirect, "bad-verifier"); !errors.Is(err, ErrPKCEMismatch) {
		t.Fatalf("got %v, want ErrPKCEMismatch", err)
	}
	tok, err := s.Redeem(code, testClientID, testRedirect, testVerifier)
	if err != nil {
		t.Fatalf("valid redeem after failed attempts should succeed: %v", err)
	}
	if !s.IsTokenActive(tok) {
		t.Fatal("token should be active")
	}
}

func TestExpiredCodeRejected(t *testing.T) {
	s, clock := newTestServer(time.Minute)
	code := issueTestCode(t, s)

	clock.Advance(2 * time.Minute)
	if _, err := s.Redeem(code, testClientID, testRedirect, testVerifier); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("past expiry: got %v, want ErrCodeExpired", err)
	}
}

func TestCodeExpiresExactlyAtDeadline(t *testing.T) {
	s, clock := newTestServer(time.Minute)
	code := issueTestCode(t, s)

	clock.Advance(time.Minute - time.Nanosecond)
	if _, err := s.Redeem(code, testClientID, testRedirect, testVerifier); err != nil {
		t.Fatalf("just before expiry should succeed: %v", err)
	}

	code2 := issueTestCode(t, s)
	clock.Advance(time.Minute + time.Nanosecond)
	if _, err := s.Redeem(code2, testClientID, testRedirect, testVerifier); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("at/after deadline: got %v, want ErrCodeExpired", err)
	}
}

func TestReplayRevokesIssuedTokens(t *testing.T) {
	s, _ := newTestServer(time.Minute)
	code := issueTestCode(t, s)

	tok, err := s.Redeem(code, testClientID, testRedirect, testVerifier)
	if err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if !s.IsTokenActive(tok) {
		t.Fatal("token should be active before replay")
	}

	if _, err := s.Redeem(code, testClientID, testRedirect, testVerifier); !errors.Is(err, ErrCodeReuse) {
		t.Fatalf("replay: got %v, want ErrCodeReuse", err)
	}
	if s.IsTokenActive(tok) {
		t.Fatal("replay must revoke the token issued for the code")
	}
}

// redeemResult captures the outcome of one Redeem call.
type redeemResult struct {
	token string
	err   error
}

// TestConcurrentRedeemOrderings uses a barrier to enumerate both possible
// orderings of two racing redemptions of the same code. Exactly one may
// succeed, the loser must observe ErrCodeReuse, and the winner's token must
// end up revoked.
func TestConcurrentRedeemOrderings(t *testing.T) {
	for _, first := range []int{0, 1} {
		name := "goroutine0-first"
		if first == 1 {
			name = "goroutine1-first"
		}
		t.Run(name, func(t *testing.T) {
			s, _ := newTestServer(time.Minute)
			code := issueTestCode(t, s)

			arrived := make(chan int, 2)
			release := []chan struct{}{make(chan struct{}), make(chan struct{})}
			var idxMu sync.Mutex
			arrivals := 0
			s.preRedeem = func() {
				idxMu.Lock()
				idx := arrivals
				arrivals++
				idxMu.Unlock()
				arrived <- idx
				<-release[idx]
			}

			results := make([]redeemResult, 2)
			done := make([]chan struct{}, 2)
			for i := 0; i < 2; i++ {
				done[i] = make(chan struct{})
				go func(i int) {
					defer close(done[i])
					tok, err := s.Redeem(code, testClientID, testRedirect, testVerifier)
					results[i] = redeemResult{token: tok, err: err}
				}(i)
				// Wait for this goroutine to reach the barrier before
				// starting the next, so arrival index == goroutine id.
				if got := <-arrived; got != i {
					t.Fatalf("arrival order: got %d, want %d", got, i)
				}
			}

			// Both redemptions are now in flight. Release them in the order
			// under test, waiting for each to finish before releasing the next.
			second := 1 - first
			close(release[first])
			<-done[first]
			close(release[second])
			<-done[second]

			winner, loser := results[first], results[second]
			if winner.err != nil {
				t.Fatalf("first redemption should succeed, got %v", winner.err)
			}
			if winner.token == "" {
				t.Fatal("winner should hold a token")
			}
			if !errors.Is(loser.err, ErrCodeReuse) {
				t.Fatalf("loser: got %v, want ErrCodeReuse", loser.err)
			}
			if loser.token != "" {
				t.Fatal("loser must not hold a token")
			}
			if s.IsTokenActive(winner.token) {
				t.Fatal("replay must revoke the winner's token")
			}
		})
	}
}

// TestConcurrentRedeemHammer fires uncoordinated concurrent redemptions to
// confirm the atomic consume holds without any test orchestration.
func TestConcurrentRedeemHammer(t *testing.T) {
	for i := 0; i < 100; i++ {
		s, _ := newTestServer(time.Minute)
		code := issueTestCode(t, s)

		var wg sync.WaitGroup
		res := make([]redeemResult, 2)
		for g := 0; g < 2; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				tok, err := s.Redeem(code, testClientID, testRedirect, testVerifier)
				res[g] = redeemResult{token: tok, err: err}
			}(g)
		}
		wg.Wait()

		successes := 0
		var winToken string
		for _, r := range res {
			if r.err == nil {
				successes++
				winToken = r.token
			} else if !errors.Is(r.err, ErrCodeReuse) {
				t.Fatalf("unexpected error: %v", r.err)
			}
		}
		if successes != 1 {
			t.Fatalf("iteration %d: %d successes, want exactly 1", i, successes)
		}
		if s.IsTokenActive(winToken) {
			t.Fatalf("iteration %d: winner token not revoked after replay", i)
		}
	}
}
