package parity_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	parity "prescreen.local/ps-2b994de5fb"
)

type scripted struct {
	inner            parity.Callback
	unavailableCalls int
	versionAlways    bool
	seen             map[string]int
	blockDuplicates  bool
	calls            int
}

func (s *scripted) query(x *big.Int) (parity.Parity, error) {
	s.calls++
	if s.blockDuplicates {
		key := x.Text(16)
		if s.seen == nil {
			s.seen = map[string]int{}
		}
		s.seen[key]++
		if s.seen[key] > 1 {
			return parity.ParityRejected, parity.ErrUnavailable
		}
	}
	if s.unavailableCalls > 0 {
		s.unavailableCalls--
		return parity.ParityRejected, parity.ErrUnavailable
	}
	if s.versionAlways {
		return parity.ParityRejected, parity.ErrVersionMismatch
	}
	return s.inner(x)
}

func paramsFor(k tinyKey, m int64) parity.Params {
	return parity.Params{N: k.N, E: k.E, C: encrypt(k, big.NewInt(m)), KeyVersion: "kv-7"}
}

func TestConsecutiveUnavailableThenRecover(t *testing.T) {
	k := makeTinyKey(13, 17) // N=221
	for _, streak := range []int{1, 5, 40} {
		s := &scripted{inner: k.oracle(), unavailableCalls: streak}
		res, err := parity.Recover(paramsFor(k, 123), s.query, parity.Options{
			QueryBudget: 500,
		})
		if err != nil {
			t.Fatalf("streak=%d: %v", streak, err)
		}
		if res.Plaintext.Int64() != 123 {
			t.Fatalf("streak=%d: got %s", streak, res.Plaintext)
		}
		if res.Unavailable != streak {
			t.Fatalf("streak=%d: recorded unavailable=%d", streak, res.Unavailable)
		}
	}
}

func TestAllUnavailableBudgetCheckpointResume(t *testing.T) {
	k := makeTinyKey(11, 19) // N=209
	p := paramsFor(k, 77)

	dead := &scripted{inner: k.oracle(), unavailableCalls: 1 << 30}
	res, err := parity.Recover(p, dead.query, parity.Options{QueryBudget: 12})
	if !errors.Is(err, parity.ErrBudgetExhausted) {
		t.Fatalf("want budget error, got %v", err)
	}
	if res.Status != parity.StatusBudgetExhausted || res.Checkpoint == nil {
		t.Fatalf("status=%s cp=%v", res.Status, res.Checkpoint != nil)
	}
	if res.Queries != 12 || res.Step != 0 {
		t.Fatalf("queries=%d step=%d want 12/0 (no interval advance)", res.Queries, res.Step)
	}
	if res.Lower.RatString() != "0" || res.Upper.Cmp(new(big.Rat).SetInt(k.N)) != 0 {
		t.Fatalf("interval advanced during unavailability: [%s,%s]", res.Lower, res.Upper)
	}

	// JSON round trip the checkpoint.
	buf, err := res.Checkpoint.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	cp, err := parity.ParseCheckpoint(buf)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(buf, &generic); err != nil {
		t.Fatalf("checkpoint is not generic JSON: %v", err)
	}

	healthy := &scripted{inner: k.oracle()}
	res2, err := parity.Resume(context.Background(), p, cp, healthy.query, parity.Options{QueryBudget: 500})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res2.Status != parity.StatusRecovered || res2.Plaintext.Int64() != 77 {
		t.Fatalf("resume status=%s m=%v", res2.Status, res2.Plaintext)
	}
	if res2.Queries <= 12 {
		t.Fatalf("cumulative queries should retain history, got %d", res2.Queries)
	}
}

func TestBudgetReachedMidRunAndResume(t *testing.T) {
	k := makeTinyKey(17, 19) // N=323
	p := paramsFor(k, 300)

	res, err := parity.Recover(p, k.oracle(), parity.Options{QueryBudget: 4})
	if !errors.Is(err, parity.ErrBudgetExhausted) {
		t.Fatalf("want budget, got %v", err)
	}
	if res.Checkpoint == nil || res.Queries != 4 {
		t.Fatalf("queries=%d cp=%v", res.Queries, res.Checkpoint != nil)
	}

	cp := res.Checkpoint
	// Pin the externally shared digest in a second resume.
	digest := cp.HistoryDigest
	res2, err := parity.Resume(context.Background(), p, cp, k.oracle(), parity.Options{
		TotalQueryBudget: 5000, ExpectedHistoryDigest: digest,
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res2.Plaintext.Int64() != 300 {
		t.Fatalf("got %s", res2.Plaintext)
	}
}

func TestResumeRejectsBindingAndDigestTampering(t *testing.T) {
	k := makeTinyKey(7, 23) // N=161
	p := paramsFor(k, 88)
	res, err := parity.Recover(p, k.oracle(), parity.Options{QueryBudget: 5})
	if err == nil || !errors.Is(err, parity.ErrBudgetExhausted) {
		t.Fatalf("expected budget cp, got %v", err)
	}
	cp := res.Checkpoint

	wrong := p
	wrong.KeyVersion = "different-version"
	if _, err := parity.Resume(context.Background(), wrong, cp, k.oracle(), parity.Options{}); !errors.Is(err, parity.ErrBindingMismatch) {
		t.Fatalf("version binding: want ErrBindingMismatch, got %v", err)
	}

	wrongN := p
	wrongN.N = new(big.Int).Add(k.N, big.NewInt(2))
	if _, err := parity.Resume(context.Background(), wrongN, cp, k.oracle(), parity.Options{}); !errors.Is(err, parity.ErrBindingMismatch) {
		t.Fatalf("N binding: %v", err)
	}

	tampered := *cp
	tampered.History = append([]parity.QueryRecord(nil), cp.History...)
	tampered.History[0].X = "deadbeef"
	if _, err := parity.Resume(context.Background(), p, &tampered, k.oracle(), parity.Options{}); !errors.Is(err, parity.ErrDigestMismatch) {
		t.Fatalf("tamper digest: want ErrDigestMismatch, got %v", err)
	}

	if _, err := parity.Resume(context.Background(), p, cp, k.oracle(), parity.Options{ExpectedHistoryDigest: "00"}); !errors.Is(err, parity.ErrDigestMismatch) {
		t.Fatalf("pinned digest: %v", err)
	}
}

func TestVersionMismatchAbortsImmediately(t *testing.T) {
	k := makeTinyKey(11, 17)
	p := paramsFor(k, 9)
	s := &scripted{inner: k.oracle(), versionAlways: true}
	res, err := parity.Recover(p, s.query, parity.Options{})
	if !errors.Is(err, parity.ErrVersionMismatch) {
		t.Fatalf("want version mismatch, got %v", err)
	}
	if res.Status != parity.StatusVersionInterrupted {
		t.Fatalf("status=%s", res.Status)
	}
	if s.calls != 1 || res.Queries != 1 {
		t.Fatalf("must abort immediately; calls=%d queries=%d", s.calls, res.Queries)
	}
}

func TestInvalidInputs(t *testing.T) {
	k := makeTinyKey(5, 7) // N=35
	goodC := encrypt(k, big.NewInt(3))
	cases := []parity.Params{
		{N: big.NewInt(36), E: k.E, C: big.NewInt(1), KeyVersion: "v"}, // even N
		{N: k.N, E: k.E, C: big.NewInt(-1)},
		{N: k.N, E: k.E, C: k.N}, // c == N
		{N: k.N, E: big.NewInt(0), C: goodC},
		{N: nil, E: k.E, C: goodC},
	}
	for i, pp := range cases {
		_, err := parity.Recover(pp, k.oracle(), parity.Options{})
		if !errors.Is(err, parity.ErrInvalidInput) {
			t.Fatalf("case %d: want ErrInvalidInput, got %v", i, err)
		}
	}
	if _, err := parity.Recover(parity.Params{N: k.N, E: k.E, C: goodC}, nil, parity.Options{}); !errors.Is(err, parity.ErrNoOracle) {
		t.Fatalf("nil callback should be no-oracle, got %v", err)
	}
}
