package limiter

import (
	"math/big"
	"math/rand"
	"testing"
)

// refModel is an independent event-by-event rational model of the bucket.
// It deliberately avoids big.Rat and tracks the balance as a raw
// numerator/denominator pair of big.Int, so the cross-check exercises a
// separate arithmetic path from the limiter under test.
type refModel struct {
	num     *big.Int // balance numerator
	den     *big.Int // balance denominator (always positive)
	rateNum int64
	rateDen int64
	cap_    int64
	last    int64
}

func newRefModel(cfg Config, now int64) *refModel {
	return &refModel{
		num:     big.NewInt(cfg.Capacity),
		den:     big.NewInt(1),
		rateNum: cfg.Rate.Num,
		rateDen: cfg.Rate.Den,
		cap_:    cfg.Capacity,
		last:    now,
	}
}

// settle adds rate*elapsed to the balance and truncates at capacity,
// discarding any excess. It must be called with now >= m.last.
func (m *refModel) settle(now int64) {
	elapsed := now - m.last
	if elapsed == 0 {
		return
	}
	// balance += rateNum*elapsed / rateDen, computed as
	// (num*rateDen + rateNum*elapsed*den) / (den*rateDen).
	lhs := new(big.Int).Mul(m.num, big.NewInt(m.rateDen))
	rhs := new(big.Int).Mul(big.NewInt(m.rateNum*elapsed), m.den)
	m.num.Add(lhs, rhs)
	m.den.Mul(m.den, big.NewInt(m.rateDen))
	m.last = now
	m.truncate()
}

func (m *refModel) truncate() {
	// cap as num/den > cap_  <=>  num > cap_*den
	capNum := new(big.Int).Mul(big.NewInt(m.cap_), m.den)
	if m.num.Cmp(capNum) > 0 {
		m.num.Set(capNum)
	}
}

func (m *refModel) allow(now int64, n int64) bool {
	m.settle(now)
	// balance >= n  <=>  num >= n*den
	want := new(big.Int).Mul(big.NewInt(n), m.den)
	if m.num.Cmp(want) < 0 {
		return false
	}
	m.num.Sub(m.num, want)
	return true
}

func (m *refModel) setConfig(now int64, cfg Config) {
	m.settle(now)
	m.rateNum, m.rateDen = cfg.Rate.Num, cfg.Rate.Den
	m.cap_ = cfg.Capacity
	m.truncate()
}

// balance returns the exact rational balance.
func (m *refModel) balance() *big.Rat {
	return new(big.Rat).SetFrac(m.num, m.den)
}

func mustNew(t *testing.T, cfg Config, now int64) *Limiter {
	l, err := New(cfg, now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

func mustAllow(t *testing.T, l *Limiter, now, n int64) bool {
	ok, err := l.Allow(now, n)
	if err != nil {
		t.Fatalf("Allow(%d, %d): %v", now, n, err)
	}
	return ok
}

func expectBalance(t *testing.T, l *Limiter, want *big.Rat) {
	if got := l.Tokens(); got.Cmp(want) != 0 {
		t.Fatalf("balance = %s, want %s", got.RatString(), want.RatString())
	}
}

// TestLongIdle: after a huge idle gap the bucket must be exactly at
// capacity — the truncated overflow is discarded, not retained.
func TestLongIdle(t *testing.T) {
	cfg := Config{Rate: Rate{Num: 3, Den: 7}, Capacity: 10}
	l := mustNew(t, cfg, 0)

	if !mustAllow(t, l, 0, 10) {
		t.Fatal("initial drain of full bucket rejected")
	}
	expectBalance(t, l, big.NewRat(0, 1))

	// Idle for 2^40 ns: refill would be 3/7 * 2^40 >> capacity.
	if !mustAllow(t, l, 1<<40, 10) {
		t.Fatal("bucket not exactly full after long idle")
	}
	expectBalance(t, l, big.NewRat(0, 1))

	// One ns later only 3/7 of a token has refilled: nothing truncated
	// from before may reappear, so an 11th token is still unavailable.
	if mustAllow(t, l, (1<<40)+1, 1) {
		t.Fatal("truncated remainder leaked back into the bucket")
	}
	expectBalance(t, l, big.NewRat(3, 7))
}

// TestBoundaryConsumption: exact rational bookkeeping at the edges —
// a request succeeds precisely when the balance reaches the cost.
func TestBoundaryConsumption(t *testing.T) {
	cfg := Config{Rate: Rate{Num: 1, Den: 3}, Capacity: 2}
	l := mustNew(t, cfg, 0)

	if !mustAllow(t, l, 0, 2) {
		t.Fatal("initial drain rejected")
	}
	// 3 ns -> exactly 1 token; consumed down to exactly 0.
	if !mustAllow(t, l, 3, 1) {
		t.Fatal("exactly-one-token boundary request rejected")
	}
	expectBalance(t, l, big.NewRat(0, 1))

	// 1 ns -> 1/3 token: request for 1 must fail and consume nothing.
	if mustAllow(t, l, 4, 1) {
		t.Fatal("request granted with only 1/3 token")
	}
	expectBalance(t, l, big.NewRat(1, 3))

	// 2 more ns -> exactly 1 token again.
	if !mustAllow(t, l, 6, 1) {
		t.Fatal("boundary request after fractional accumulation rejected")
	}
	expectBalance(t, l, big.NewRat(0, 1))

	// Zero-cost requests are always granted and change nothing.
	if !mustAllow(t, l, 6, 0) {
		t.Fatal("zero-cost request rejected")
	}
	expectBalance(t, l, big.NewRat(0, 1))
}

// TestRateSwitch: the switch settles the bucket under the old config up
// to the switch moment, then the new rate and capacity apply.
func TestRateSwitch(t *testing.T) {
	old := Config{Rate: Rate{Num: 1, Den: 2}, Capacity: 100}
	l := mustNew(t, old, 0)
	if !mustAllow(t, l, 0, 100) {
		t.Fatal("initial drain rejected")
	}

	// Switch at t=10: old config settles 10 * 1/2 = 5 tokens, then the
	// new capacity 3 truncates the balance, discarding 2 tokens.
	fast := Config{Rate: Rate{Num: 5, Den: 1}, Capacity: 3}
	if err := l.SetConfig(10, fast); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	expectBalance(t, l, big.NewRat(3, 1))

	// New rate applies from the switch moment: 1 ns -> +5, capped at 3.
	if !mustAllow(t, l, 11, 3) {
		t.Fatal("refill under new rate missing")
	}
	expectBalance(t, l, big.NewRat(0, 1))
	if !mustAllow(t, l, 12, 3) {
		t.Fatal("new-rate refill over one ns should reach capacity 3")
	}

	// Switching to a larger capacity keeps the existing balance: the
	// old fast config settles 12->13 (+5, capped at 3), and raising the
	// capacity to 50 does not mint new tokens.
	slow := Config{Rate: Rate{Num: 1, Den: 4}, Capacity: 50}
	if err := l.SetConfig(13, slow); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	expectBalance(t, l, big.NewRat(3, 1))
	// 8 ns under 1/4 token/ns -> 3 + 2 = exactly 5 tokens.
	if !mustAllow(t, l, 21, 5) {
		t.Fatal("settle under switched config incorrect")
	}
	expectBalance(t, l, big.NewRat(0, 1))
}

// TestTimeBackward: a backwards timestamp is rejected and leaves both
// the bucket state and the configuration untouched.
func TestTimeBackward(t *testing.T) {
	cfg := Config{Rate: Rate{Num: 2, Den: 5}, Capacity: 10}
	l := mustNew(t, cfg, 100)
	if !mustAllow(t, l, 110, 3) {
		t.Fatal("drain rejected")
	}
	before := l.Tokens()
	if before.Cmp(big.NewRat(7, 1)) != 0 { // refill 4 truncates at 10, then 3 consumed
		t.Fatalf("setup balance = %s, want 7", before.RatString())
	}

	if _, err := l.Allow(109, 1); err != ErrTimeBackward {
		t.Fatalf("Allow backwards: err = %v, want ErrTimeBackward", err)
	}
	if l.Tokens().Cmp(before) != 0 || l.Last() != 110 {
		t.Fatal("backwards Allow mutated the state")
	}

	newCfg := Config{Rate: Rate{Num: 9, Den: 1}, Capacity: 99}
	if err := l.SetConfig(109, newCfg); err != ErrTimeBackward {
		t.Fatalf("SetConfig backwards: err = %v, want ErrTimeBackward", err)
	}
	if l.Tokens().Cmp(before) != 0 || l.Last() != 110 {
		t.Fatal("backwards SetConfig mutated the state")
	}
	if l.Config() != cfg {
		t.Fatal("backwards SetConfig mutated the configuration")
	}

	// Equal timestamps are not backwards and must still work.
	if !mustAllow(t, l, 110, 1) {
		t.Fatal("same-timestamp request rejected")
	}
	expectBalance(t, l, big.NewRat(6, 1))
}

// TestAgainstReferenceModel drives the limiter and the independent
// rational model through a deterministic pseudo-random event stream —
// long idle gaps, boundary-sized consumptions and rate switches — and
// requires identical decisions and exact balances after every event.
func TestAgainstReferenceModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	cfgs := []Config{
		{Rate: Rate{Num: 3, Den: 7}, Capacity: 10},
		{Rate: Rate{Num: 1, Den: 1}, Capacity: 4},
		{Rate: Rate{Num: 0, Den: 1}, Capacity: 6},
		{Rate: Rate{Num: 40, Den: 3}, Capacity: 25},
		{Rate: Rate{Num: 1, Den: 1000}, Capacity: 2},
	}

	cfg := cfgs[0]
	l := mustNew(t, cfg, 0)
	m := newRefModel(cfg, 0)
	now := int64(0)

	for i := 0; i < 2000; i++ {
		// Occasionally jump far into the future (long idle), otherwise
		// advance a small, sometimes zero, amount.
		if rng.Intn(20) == 0 {
			now += 1 << uint(20+rng.Intn(20))
		} else {
			now += int64(rng.Intn(12))
		}

		switch rng.Intn(10) {
		case 0: // configuration switch
			cfg = cfgs[rng.Intn(len(cfgs))]
			if err := l.SetConfig(now, cfg); err != nil {
				t.Fatalf("event %d: SetConfig: %v", i, err)
			}
			m.setConfig(now, cfg)
		default: // consumption, often exactly at the balance boundary
			var n int64
			switch rng.Intn(4) {
			case 0:
				n = cfg.Capacity
			case 1:
				n = cfg.Capacity + 1
			default:
				n = int64(rng.Intn(int(cfg.Capacity) + 2))
			}
			got, err := l.Allow(now, n)
			if err != nil {
				t.Fatalf("event %d: Allow: %v", i, err)
			}
			if want := m.allow(now, n); got != want {
				t.Fatalf("event %d (t=%d, n=%d): allow = %v, model = %v",
					i, now, n, got, want)
			}
		}

		if got, want := l.Tokens(), m.balance(); got.Cmp(want) != 0 {
			t.Fatalf("event %d (t=%d): balance = %s, model = %s",
				i, now, got.RatString(), want.RatString())
		}
		if l.Last() != m.last {
			t.Fatalf("event %d: last = %d, model = %d", i, l.Last(), m.last)
		}
	}
}

// TestValidation covers invalid configurations and arguments.
func TestValidation(t *testing.T) {
	if _, err := New(Config{Rate: Rate{Num: 1, Den: 0}, Capacity: 1}, 0); err == nil {
		t.Fatal("zero denominator accepted")
	}
	if _, err := New(Config{Rate: Rate{Num: -1, Den: 1}, Capacity: 1}, 0); err == nil {
		t.Fatal("negative rate accepted")
	}
	if _, err := New(Config{Rate: Rate{Num: 1, Den: 1}, Capacity: -1}, 0); err == nil {
		t.Fatal("negative capacity accepted")
	}
	l := mustNew(t, Config{Rate: Rate{Num: 1, Den: 1}, Capacity: 1}, 0)
	if _, err := l.Allow(0, -1); err == nil {
		t.Fatal("negative consumption accepted")
	}
	if err := l.SetConfig(0, Config{Rate: Rate{Num: 1, Den: -3}, Capacity: 1}); err == nil {
		t.Fatal("SetConfig with invalid rate accepted")
	}
}
