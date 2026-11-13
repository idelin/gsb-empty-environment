package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// oracleFor builds the host callback for a fixed hidden secret.
func oracleFor(secret []byte) Oracle {
	return func(query []byte) (int, error) {
		return CompressLen(Payload(secret, query)), nil
	}
}

func toBytes(ss ...string) [][]byte {
	out := make([][]byte, len(ss))
	for i, s := range ss {
		out[i] = []byte(s)
	}
	return out
}

func TestFullRecovery(t *testing.T) {
	candidates := toBytes("alpha", "bravo", "charlie", "delta")
	queries := toBytes("alpha", "bravo", "charlie", "delta", "zzz")
	for _, secret := range candidates {
		s, err := NewSession(candidates, queries)
		if err != nil {
			t.Fatal(err)
		}
		status, err := s.Run(oracleFor(secret), 16)
		if err != nil {
			t.Fatalf("secret %q: %v", secret, err)
		}
		if status != StatusRecovered {
			t.Fatalf("secret %q: status=%v, report:\n%s", secret, status, s.Report())
		}
		got, ok := s.Recovered()
		if !ok || !bytes.Equal(got, secret) {
			t.Fatalf("secret %q: recovered %q, ok=%v", secret, got, ok)
		}
		if len(s.Rounds()) == 0 {
			t.Fatalf("secret %q: no elimination rounds recorded", secret)
		}
		for _, r := range s.Rounds() {
			if r.After != r.Before-len(r.Eliminated) {
				t.Fatalf("secret %q: inconsistent round record: %v", secret, r)
			}
		}
	}
}

func TestQueryTieBreakByByteOrder(t *testing.T) {
	// "car" and "cat" split the candidate set equally well; the
	// lexicographically smaller query must be chosen even though the
	// host listed "cat" first.
	candidates := toBytes("cat", "car", "dog")
	queries := toBytes("cat", "car")
	s, err := NewSession(candidates, queries)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(oracleFor([]byte("dog")), 1); err != nil {
		t.Fatal(err)
	}
	rounds := s.Rounds()
	if len(rounds) != 1 {
		t.Fatalf("rounds=%d, want 1", len(rounds))
	}
	if !bytes.Equal(rounds[0].Query, []byte("car")) {
		t.Fatalf("chosen query=%q, want %q (byte-order tie break)", rounds[0].Query, "car")
	}
}

func TestIndistinguishableGroupNotRecovered(t *testing.T) {
	// Disjoint byte sets and no internal repeats: every query compresses
	// to the same length for both secrets, so no query can split them.
	candidates := toBytes("ABCDEFG", "HIJKLMN")
	queries := toBytes("XYZ", "QQQ", "123", "")
	s, err := NewSession(candidates, queries)
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.Run(oracleFor([]byte("HIJKLMN")), 8)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusIndistinguishable {
		t.Fatalf("status=%v, want indistinguishable", status)
	}
	if _, ok := s.Recovered(); ok {
		t.Fatal("must not claim recovery for an indistinguishable group")
	}
	remaining := s.Remaining()
	if len(remaining) != 2 {
		t.Fatalf("remaining=%q, want both candidates", remaining)
	}
	if len(s.Rounds()) != 0 {
		t.Fatalf("no oracle round should be consumed, got %v", s.Rounds())
	}
}

func TestModelInconsistent(t *testing.T) {
	s, err := NewSession(toBytes("alpha", "bravo"), toBytes("alpha", "bravo"))
	if err != nil {
		t.Fatal(err)
	}
	bogus := Oracle(func([]byte) (int, error) { return 9999, nil })
	if err := s.Step(bogus); !errors.Is(err, ErrModelInconsistent) {
		t.Fatalf("err=%v, want ErrModelInconsistent", err)
	}
	// State must be untouched so the session can resume with a fixed oracle.
	if got := len(s.Remaining()); got != 2 {
		t.Fatalf("remaining=%d, want 2 after inconsistent feedback", got)
	}
	if s.Status() != StatusInProgress {
		t.Fatalf("status=%v, want in-progress", s.Status())
	}
	oracleErr := Oracle(func([]byte) (int, error) { return 0, fmt.Errorf("host down") })
	if err := s.Step(oracleErr); err == nil || errors.Is(err, ErrModelInconsistent) {
		t.Fatalf("err=%v, want wrapped oracle error", err)
	}
}

func TestBudgetInterruptAndResume(t *testing.T) {
	candidates := toBytes("alpha", "bravo", "charlie", "delta")
	queries := toBytes("alpha", "bravo", "charlie", "delta")
	secret := []byte("delta")

	s, err := NewSession(candidates, queries)
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.Run(oracleFor(secret), 1)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusInProgress {
		t.Fatalf("status=%v after 1-round budget, want in-progress", status)
	}
	if len(s.Rounds()) != 1 {
		t.Fatalf("rounds=%d, want exactly 1", len(s.Rounds()))
	}

	// Persist the interrupted session and resume from the snapshot.
	blob, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Import(blob)
	if err != nil {
		t.Fatal(err)
	}
	status, err = resumed.Run(oracleFor(secret), 16)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusRecovered {
		t.Fatalf("status=%v after resume, want recovered; report:\n%s", status, resumed.Report())
	}
	got, ok := resumed.Recovered()
	if !ok || !bytes.Equal(got, secret) {
		t.Fatalf("recovered %q ok=%v, want %q", got, ok, secret)
	}
	if len(resumed.Rounds()) <= 1 {
		t.Fatalf("resumed session should record further rounds, got %d", len(resumed.Rounds()))
	}
}

func TestValidation(t *testing.T) {
	good := toBytes("a", "b")
	if _, err := NewSession(nil, nil); !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("err=%v, want ErrNoCandidates", err)
	}
	if _, err := NewSession(toBytes(""), nil); !errors.Is(err, ErrCandidateEmpty) {
		t.Fatalf("err=%v, want ErrCandidateEmpty", err)
	}
	if _, err := NewSession(toBytes("0123456789abcdefg"), nil); !errors.Is(err, ErrCandidateTooLong) {
		t.Fatalf("err=%v, want ErrCandidateTooLong", err)
	}
	if _, err := NewSession([][]byte{{'a', Separator}}, nil); !errors.Is(err, ErrCandidateHasSeparator) {
		t.Fatalf("err=%v, want ErrCandidateHasSeparator", err)
	}
	tooMany := make([][]byte, MaxCandidates+1)
	for i := range tooMany {
		tooMany[i] = []byte{byte(i), byte(i >> 8)}
	}
	if _, err := NewSession(tooMany, nil); !errors.Is(err, ErrTooManyCandidates) {
		t.Fatalf("err=%v, want ErrTooManyCandidates", err)
	}
	tooManyQ := make([][]byte, MaxQueries+1)
	for i := range tooManyQ {
		tooManyQ[i] = []byte{byte(i)}
	}
	if _, err := NewSession(good, tooManyQ); !errors.Is(err, ErrTooManyQueries) {
		t.Fatalf("err=%v, want ErrTooManyQueries", err)
	}
	// 256 candidates / 128 queries are accepted (bytes kept below 0xFF).
	okC := make([][]byte, MaxCandidates)
	for i := range okC {
		okC[i] = []byte{byte(i % 128), byte(i / 128), 7}
	}
	okQ := make([][]byte, MaxQueries)
	for i := range okQ {
		okQ[i] = []byte{byte(i)}
	}
	if _, err := NewSession(okC, okQ); err != nil {
		t.Fatalf("boundary config rejected: %v", err)
	}
}

func TestSingleCandidateRecoveredImmediately(t *testing.T) {
	s, err := NewSession(toBytes("only"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status() != StatusRecovered {
		t.Fatalf("status=%v, want recovered", s.Status())
	}
	got, ok := s.Recovered()
	if !ok || !bytes.Equal(got, []byte("only")) {
		t.Fatalf("recovered %q ok=%v", got, ok)
	}
}
