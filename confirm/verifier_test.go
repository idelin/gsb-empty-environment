package confirm

import (
	"errors"
	"testing"
	"time"

	"example.org/netsec/q0063/ocra"
)

var testSecret = []byte("super-secret-shared-key-32-bytes")

func testHarness(t *testing.T) (*Verifier, *MemoryStore, Transaction, time.Time) {
	t.Helper()
	store := NewMemoryStore()
	store.Provision("user-1", testSecret)
	v := NewVerifier(store)

	tx := Transaction{
		Amount:   "1250.00",
		Currency: "CNY",
		Payee:    "6222021234567890123",
		Nonce:    "0123456789abcdef0123456789abcdef",
	}
	// Minute boundary so +/-N minute offsets stay on deterministic steps.
	at := time.Unix(60*60*24*365*10, 0)
	return v, store, tx, at
}

func sign(t *testing.T, v *Verifier, counter uint64, tx Transaction, at time.Time) string {
	t.Helper()
	challenge, err := Challenge(tx)
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	code, err := v.GenerateResponse(testSecret, counter, challenge, at)
	if err != nil {
		t.Fatalf("GenerateResponse: %v", err)
	}
	return code
}

func TestVerify_LegitimateResponse(t *testing.T) {
	v, _, tx, at := testHarness(t)

	err := v.Verify(Request{
		UserID: "user-1", Tx: tx, Counter: 0,
		Response: sign(t, v, 0, tx, at), Now: at,
	})
	if err != nil {
		t.Fatalf("legitimate confirmation rejected: %v", err)
	}

	// A second, distinct transfer with a fresh counter must also succeed.
	tx2 := tx
	tx2.Amount = "9.90"
	err = v.Verify(Request{
		UserID: "user-1", Tx: tx2, Counter: 1,
		Response: sign(t, v, 1, tx2, at), Now: at,
	})
	if err != nil {
		t.Fatalf("second legitimate confirmation rejected: %v", err)
	}
}

func TestVerify_RejectsTamperedAmount(t *testing.T) {
	v, _, tx, at := testHarness(t)

	// The answer signs 1250.00; the submitted transfer claims a different
	// amount, which is exactly the phishing substitution the service blocks.
	tampered := tx
	tampered.Amount = "1.00"

	err := v.Verify(Request{
		UserID: "user-1", Tx: tampered, Counter: 0,
		Response: sign(t, v, 0, tx, at), Now: at,
	})
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("tampered amount: got %v, want ErrInvalidResponse", err)
	}

	// The failed attempt must not have consumed any state.
	state, _ := v.store.Get("user-1")
	if state.CounterUsed || len(state.UsedChallenges) != 0 {
		t.Fatalf("failed verification mutated state: %+v", state)
	}
}

func TestVerify_RejectsChallengeReplay(t *testing.T) {
	v, _, tx, at := testHarness(t)
	code := sign(t, v, 0, tx, at)

	first := Request{UserID: "user-1", Tx: tx, Counter: 0, Response: code, Now: at}
	if err := v.Verify(first); err != nil {
		t.Fatalf("first confirmation: %v", err)
	}

	// Reusing the exact same answer on a fresh, advanced counter is still a
	// replay: the challenge may be consumed only once.
	replay := Request{UserID: "user-1", Tx: tx, Counter: 1, Response: code, Now: at}
	err := v.Verify(replay)
	if !errors.Is(err, ErrChallengeReplay) {
		t.Fatalf("replay: got %v, want ErrChallengeReplay", err)
	}
}

func TestVerify_RejectsCounterRollback(t *testing.T) {
	v, _, tx, at := testHarness(t)

	if err := v.Verify(Request{
		UserID: "user-1", Tx: tx, Counter: 10,
		Response: sign(t, v, 10, tx, at), Now: at,
	}); err != nil {
		t.Fatalf("counter 10 confirmation: %v", err)
	}

	// Same counter reuse.
	err := v.Verify(Request{
		UserID: "user-1", Tx: tx, Counter: 10,
		Response: sign(t, v, 10, tx, at), Now: at,
	})
	if !errors.Is(err, ErrCounterReplay) {
		t.Fatalf("counter reuse: got %v, want ErrCounterReplay", err)
	}

	// Rolled-back counter with a correctly computed answer.
	rollback := Transaction{
		Amount: "50.00", Currency: "CNY",
		Payee: "6222029999999999999", Nonce: "fedcba9876543210fedcba9876543210",
	}
	err = v.Verify(Request{
		UserID: "user-1", Tx: rollback, Counter: 9,
		Response: sign(t, v, 9, rollback, at), Now: at,
	})
	if !errors.Is(err, ErrCounterReplay) {
		t.Fatalf("counter rollback: got %v, want ErrCounterReplay", err)
	}
}

func TestVerify_TimeWindow(t *testing.T) {
	v, _, tx, at := testHarness(t)

	// Two minutes of skew is inside the default +/-2 step window.
	if err := v.Verify(Request{
		UserID: "user-1", Tx: tx, Counter: 0,
		Response: sign(t, v, 0, tx, at.Add(2*time.Minute)), Now: at,
	}); err != nil {
		t.Fatalf("answer within skew window rejected: %v", err)
	}

	// A fresh challenge/counter signed three minutes away must fail.
	tx2 := Transaction{
		Amount: "7.00", Currency: "CNY", Payee: tx.Payee,
		Nonce: "11111111111111111111111111111111",
	}
	err := v.Verify(Request{
		UserID: "user-1", Tx: tx2, Counter: 1,
		Response: sign(t, v, 1, tx2, at.Add(3*time.Minute)), Now: at,
	})
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("answer outside window: got %v, want ErrInvalidResponse", err)
	}
}

func TestVerify_UnknownUser(t *testing.T) {
	v, _, tx, at := testHarness(t)
	err := v.Verify(Request{
		UserID: "ghost", Tx: tx, Counter: 0,
		Response: sign(t, v, 0, tx, at), Now: at,
	})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unknown user: got %v, want ErrUserNotFound", err)
	}
}

func TestChallenge_BindsEveryFieldAndIsStable(t *testing.T) {
	base := Transaction{
		Amount: "1250.00", Currency: "CNY",
		Payee: "6222021234567890123", Nonce: "0123456789abcdef0123456789abcdef",
	}
	first, err := Challenge(base)
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if len(first) != 64 {
		t.Fatalf("challenge length = %d, want 64 hex chars", len(first))
	}

	// Deterministic and independent of nonce letter case.
	upper := base
	upper.Nonce = "0123456789ABCDEF0123456789ABCDEF"
	again, err := Challenge(upper)
	if err != nil || again != first {
		t.Fatalf("case-insensitive nonce mismatch: %q vs %q (%v)", again, first, err)
	}

	// Changing any signed field changes the digest.
	mutants := []Transaction{
		{Amount: "1250.01", Currency: base.Currency, Payee: base.Payee, Nonce: base.Nonce},
		{Amount: base.Amount, Currency: "USD", Payee: base.Payee, Nonce: base.Nonce},
		{Amount: base.Amount, Currency: base.Currency, Payee: "6222021234567890124", Nonce: base.Nonce},
		{Amount: base.Amount, Currency: base.Currency, Payee: base.Payee, Nonce: "0123456789abcdef0123456789abcdee"},
	}
	for i, mutant := range mutants {
		got, err := Challenge(mutant)
		if err != nil {
			t.Fatalf("mutant %d: %v", i, err)
		}
		if got == first {
			t.Fatalf("mutant %d produced the same challenge", i)
		}
	}
}

func TestChallenge_RejectsMalformedTransaction(t *testing.T) {
	good := Transaction{
		Amount: "1.00", Currency: "CNY",
		Payee: "acct-1", Nonce: "0123456789abcdef",
	}
	bad := []Transaction{
		{Amount: "", Currency: good.Currency, Payee: good.Payee, Nonce: good.Nonce},
		{Amount: "1,000.00", Currency: good.Currency, Payee: good.Payee, Nonce: good.Nonce},
		{Amount: good.Amount, Currency: "cny", Payee: good.Payee, Nonce: good.Nonce},
		{Amount: good.Amount, Currency: good.Currency, Payee: "", Nonce: good.Nonce},
		{Amount: good.Amount, Currency: good.Currency, Payee: good.Payee, Nonce: "abc"},
		{Amount: good.Amount, Currency: good.Currency, Payee: good.Payee, Nonce: "0123456789abcde"},
	}
	for i, tx := range bad {
		if _, err := Challenge(tx); err == nil {
			t.Errorf("malformed transaction %d accepted", i)
		}
	}
}

func TestNewNonce(t *testing.T) {
	n1, err := NewNonce()
	if err != nil {
		t.Fatalf("NewNonce: %v", err)
	}
	n2, err := NewNonce()
	if err != nil {
		t.Fatalf("NewNonce: %v", err)
	}
	if n1 == n2 || len(n1) != 32 {
		t.Fatalf("nonce not unique or wrong length: %q %q", n1, n2)
	}
}

func TestSuiteConstant(t *testing.T) {
	if ocra.SuiteC_QH64_T1M != "OCRA-1:HOTP-SHA256-8:C-QH64-T1M" {
		t.Fatalf("unexpected suite constant %q", ocra.SuiteC_QH64_T1M)
	}
}
