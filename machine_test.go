package netsec

import (
	"errors"
	"sync"
	"testing"
)

type redemption struct {
	token Token
	err   error
}

func concurrentRedemptions(m *Machine, code string, n int) []redemption {
	start := make(chan struct{})
	results := make(chan redemption, n)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			token, err := m.Redeem(code)
			results <- redemption{token: token, err: err}
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	out := make([]redemption, 0, n)
	for result := range results {
		out = append(out, result)
	}
	return out
}

func TestSingleRedemptionSucceeds(t *testing.T) {
	m := NewMachine()
	if err := m.Issue("single-use-code"); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	token, err := m.Redeem("single-use-code")
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}
	if token == "" {
		t.Fatal("Redeem() returned an empty token")
	}
	if !m.Validate(token) {
		t.Fatal("token from a single redemption is not valid")
	}

	state, ok := m.StateOf("single-use-code")
	if !ok || state != StateRedeemed {
		t.Fatalf("state = %v, %v; want %v, true", state, ok, StateRedeemed)
	}
}

func TestZeroValueMachineIsUsable(t *testing.T) {
	var m Machine
	if err := m.Issue("zero-value-code"); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	token, err := m.Redeem("zero-value-code")
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}
	if !m.Validate(token) {
		t.Fatal("token from zero-value machine is invalid")
	}
}

func TestConcurrentRedemptionsIssueOnlyOneToken(t *testing.T) {
	for round := 0; round < 100; round++ {
		m := NewMachine("concurrent-code")
		results := concurrentRedemptions(m, "concurrent-code", 2)

		var successes int
		var winner Token
		for _, result := range results {
			if result.err == nil {
				successes++
				winner = result.token
				continue
			}
			if result.token != "" {
				t.Fatalf("failed redemption returned token %q", result.token)
			}
		}

		if successes != 1 {
			t.Fatalf("round %d: successful redemptions = %d; want 1 (results: %+v)", round, successes, results)
		}
		if winner == "" {
			t.Fatalf("round %d: winning redemption returned an empty token", round)
		}
		if m.Validate(winner) {
			t.Fatalf("round %d: token from the first success survived a concurrent replay", round)
		}

		state, ok := m.StateOf("concurrent-code")
		if !ok || state != StateRevoked {
			t.Fatalf("round %d: state = %v, %v; want %v, true", round, state, ok, StateRevoked)
		}
	}
}

func TestConcurrentRedemptionsWithManyCallers(t *testing.T) {
	const callers = 64

	m := NewMachine("hot-code")
	results := concurrentRedemptions(m, "hot-code", callers)

	var successes int
	var winner Token
	for _, result := range results {
		if result.err == nil {
			successes++
			winner = result.token
		}
	}
	if successes != 1 {
		t.Fatalf("successful redemptions = %d; want 1", successes)
	}
	if m.Validate(winner) {
		t.Fatal("winning token is still valid after replay was detected")
	}
}

func TestSequentialReuseRevokesFirstToken(t *testing.T) {
	m := NewMachine("reused-code")

	token, err := m.Redeem("reused-code")
	if err != nil {
		t.Fatalf("first Redeem() error = %v", err)
	}
	if !m.Validate(token) {
		t.Fatal("first token is invalid before replay")
	}

	replayedToken, err := m.Redeem("reused-code")
	if !errors.Is(err, ErrCodeAlreadyUsed) {
		t.Fatalf("second Redeem() error = %v; want %v", err, ErrCodeAlreadyUsed)
	}
	if replayedToken != "" {
		t.Fatalf("second Redeem() token = %q; want empty", replayedToken)
	}
	if m.Validate(token) {
		t.Fatal("first token remains valid after replay")
	}

	if _, err := m.Redeem("reused-code"); !errors.Is(err, ErrCodeRevoked) {
		t.Fatalf("third Redeem() error = %v; want %v", err, ErrCodeRevoked)
	}

	state, ok := m.StateOf("reused-code")
	if !ok || state != StateRevoked {
		t.Fatalf("state = %v, %v; want %v, true", state, ok, StateRevoked)
	}
}

func TestUnknownAndEmptyCodes(t *testing.T) {
	m := NewMachine()

	if _, err := m.Redeem("missing"); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("Redeem(missing) error = %v; want %v", err, ErrCodeNotFound)
	}
	if _, err := m.Redeem(""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("Redeem(empty) error = %v; want %v", err, ErrInvalidCode)
	}
	if err := m.Issue(""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("Issue(empty) error = %v; want %v", err, ErrInvalidCode)
	}
}

func TestReissuingCodeDoesNotResetState(t *testing.T) {
	m := NewMachine("code")
	token, err := m.Redeem("code")
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}

	if err := m.Issue("code"); !errors.Is(err, ErrCodeAlreadyIssued) {
		t.Fatalf("Issue() error = %v; want %v", err, ErrCodeAlreadyIssued)
	}
	if !m.Validate(token) {
		t.Fatal("re-issuing a used code reset its state")
	}
	if state, _ := m.StateOf("code"); state != StateRedeemed {
		t.Fatalf("state = %v; want %v", state, StateRedeemed)
	}
}

func TestRevokeTokenAndCode(t *testing.T) {
	m := NewMachine("code-a", "code-b")

	tokenA, err := m.Redeem("code-a")
	if err != nil {
		t.Fatalf("Redeem(code-a) error = %v", err)
	}
	if err := m.Revoke(tokenA); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if m.Validate(tokenA) {
		t.Fatal("revoked token is valid")
	}
	if err := m.Revoke(tokenA); err != nil {
		t.Fatalf("second Revoke() error = %v", err)
	}

	tokenB, err := m.Redeem("code-b")
	if err != nil {
		t.Fatalf("Redeem(code-b) error = %v", err)
	}
	if err := m.RevokeCode("code-b"); err != nil {
		t.Fatalf("RevokeCode() error = %v", err)
	}
	if m.Validate(tokenB) {
		t.Fatal("token from revoked code is valid")
	}
	if state, _ := m.StateOf("code-b"); state != StateRevoked {
		t.Fatalf("state = %v; want %v", state, StateRevoked)
	}
}

func TestApplyEvents(t *testing.T) {
	m := NewMachine()

	issued := m.Apply(Event{Action: ActionIssue, Code: "event-code"})
	if !issued.OK || issued.State != StateIssued {
		t.Fatalf("issue result = %+v", issued)
	}

	redeemed := m.Apply(Event{Action: ActionRedeem, Code: "event-code"})
	if !redeemed.OK || redeemed.Token == "" || redeemed.State != StateRedeemed {
		t.Fatalf("redeem result = %+v", redeemed)
	}

	replayed := m.Apply(Event{Action: ActionRedeem, Code: "event-code"})
	if replayed.OK || !errors.Is(replayed.Err, ErrCodeAlreadyUsed) || replayed.State != StateRevoked {
		t.Fatalf("replay result = %+v", replayed)
	}
	if m.Validate(redeemed.Token) {
		t.Fatal("token survived replay applied through Apply")
	}
}

func TestNextState(t *testing.T) {
	tests := []struct {
		current State
		action  Action
		want    State
		wantErr error
	}{
		{StateUnknown, ActionIssue, StateIssued, nil},
		{StateIssued, ActionRedeem, StateRedeemed, nil},
		{StateRedeemed, ActionRedeem, StateRevoked, nil},
		{StateRevoked, ActionRedeem, StateRevoked, ErrCodeRevoked},
		{StateIssued, ActionRevokeCode, StateRevoked, nil},
	}

	for _, test := range tests {
		got, err := NextState(test.current, test.action)
		if !errors.Is(err, test.wantErr) {
			t.Fatalf("NextState(%v, %v) error = %v; want %v", test.current, test.action, err, test.wantErr)
		}
		if got != test.want {
			t.Fatalf("NextState(%v, %v) = %v; want %v", test.current, test.action, got, test.want)
		}
	}
}
