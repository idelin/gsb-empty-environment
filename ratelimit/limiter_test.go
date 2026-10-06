package ratelimit

import (
	"math/big"
	"math/rand"
	"testing"
)

// model is an independent rational per-event model of the limiter. Every
// event is settled step by step with exact rational arithmetic, and the
// limiter under test must agree with it after each event.
type model struct {
	capacity *big.Rat
	rate     *big.Rat
	tokens   *big.Rat
	last     int64
}

func newModel(capacity, rate *big.Rat, now int64) *model {
	return &model{
		capacity: new(big.Rat).Set(capacity),
		rate:     new(big.Rat).Set(rate),
		tokens:   new(big.Rat).Set(capacity),
		last:     now,
	}
}

func (m *model) settle(now int64) bool {
	if now < m.last {
		return false
	}
	if dt := now - m.last; dt > 0 {
		gain := new(big.Rat).Mul(m.rate, new(big.Rat).SetInt64(dt))
		m.tokens.Add(m.tokens, gain)
		m.last = now
	}
	if m.tokens.Cmp(m.capacity) > 0 {
		// Truncate to capacity; the remainder is discarded, not carried.
		m.tokens.Set(m.capacity)
	}
	return true
}

func (m *model) allow(now int64, cost *big.Rat) (bool, bool) {
	if !m.settle(now) {
		return false, false
	}
	if m.tokens.Cmp(cost) < 0 {
		return false, true
	}
	m.tokens.Sub(m.tokens, cost)
	return true, true
}

func (m *model) setConfig(now int64, capacity, rate *big.Rat) bool {
	if !m.settle(now) {
		return false
	}
	m.capacity = new(big.Rat).Set(capacity)
	m.rate = new(big.Rat).Set(rate)
	return true
}

func rat(num, den int64) *big.Rat {
	return big.NewRat(num, den)
}

func checkState(t *testing.T, l *Limiter, m *model, ctx string) {
	t.Helper()
	if l.Tokens().Cmp(m.tokens) != 0 {
		t.Fatalf("%s: tokens = %s, model says %s", ctx, l.Tokens(), m.tokens)
	}
	if l.Last() != m.last {
		t.Fatalf("%s: last = %d, model says %d", ctx, l.Last(), m.last)
	}
	cfg := l.Config()
	if cfg.Capacity.Cmp(m.capacity) != 0 || cfg.Rate.Cmp(m.rate) != 0 {
		t.Fatalf("%s: config = (%s, %s), model says (%s, %s)",
			ctx, cfg.Capacity, cfg.Rate, m.capacity, m.rate)
	}
}

// Long idle periods must truncate to exactly the capacity: the truncated
// remainder is discarded and must never resurface in later events.
func TestLongIdleTruncatesAndDiscardsRemainder(t *testing.T) {
	l, err := NewLimiter(Config{Capacity: rat(10, 1), Rate: rat(1, 3)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(rat(10, 1), rat(1, 3), 0)

	// Drain part of the bucket, then idle far past the point of overflow.
	if ok, err := l.Allow(0, rat(5, 1)); !ok || err != nil {
		t.Fatalf("drain: ok=%v err=%v", ok, err)
	}
	m.allow(0, rat(5, 1))
	checkState(t, l, m, "after drain")

	if ok, err := l.Allow(1000000000, rat(0, 1)); !ok || err != nil {
		t.Fatalf("idle probe: ok=%v err=%v", ok, err)
	}
	m.allow(1000000000, rat(0, 1))
	checkState(t, l, m, "after long idle")
	if got := l.Tokens(); got.Cmp(rat(10, 1)) != 0 {
		t.Fatalf("after long idle tokens = %s, want exactly 10", got)
	}

	// Empty the bucket completely at the truncation point, then let a
	// single nanosecond pass. The balance must be exactly one rate unit;
	// any remainder kept from the truncation would show up here.
	if ok, err := l.Allow(1000000000, rat(10, 1)); !ok || err != nil {
		t.Fatalf("empty: ok=%v err=%v", ok, err)
	}
	m.allow(1000000000, rat(10, 1))
	if ok, err := l.Allow(1000000001, rat(0, 1)); !ok || err != nil {
		t.Fatalf("probe: ok=%v err=%v", ok, err)
	}
	m.allow(1000000001, rat(0, 1))
	checkState(t, l, m, "after empty + 1ns")
	if got := l.Tokens(); got.Cmp(rat(1, 3)) != 0 {
		t.Fatalf("tokens = %s, want exactly 1/3 (no carried remainder)", got)
	}

	// Idling again must saturate at exactly the capacity, not above it.
	if ok, err := l.Allow(2000000000, rat(0, 1)); !ok || err != nil {
		t.Fatalf("second idle: ok=%v err=%v", ok, err)
	}
	m.allow(2000000000, rat(0, 1))
	checkState(t, l, m, "after second idle")
	if got := l.Tokens(); got.Cmp(rat(10, 1)) != 0 {
		t.Fatalf("after second idle tokens = %s, want exactly 10", got)
	}
}

// Consuming exactly the available balance must succeed and leave zero;
// consuming a hair more must fail and leave the balance untouched.
func TestBoundaryConsumption(t *testing.T) {
	l, err := NewLimiter(Config{Capacity: rat(10, 1), Rate: rat(1, 2)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(rat(10, 1), rat(1, 2), 0)

	// Empty the bucket, then accumulate exactly 7/2 tokens over 7 ns.
	l.Allow(0, rat(10, 1))
	m.allow(0, rat(10, 1))
	if ok, err := l.Allow(7, rat(7, 2)); !ok || err != nil {
		t.Fatalf("exact consume: ok=%v err=%v", ok, err)
	}
	m.allow(7, rat(7, 2))
	checkState(t, l, m, "after exact consume")
	if got := l.Tokens(); got.Sign() != 0 {
		t.Fatalf("tokens = %s, want exactly 0", got)
	}

	// Accumulate 2 tokens, then ask for 2 + 1/3: must be refused and the
	// settled balance must remain exactly 2.
	if ok, err := l.Allow(11, rat(7, 3)); ok || err != nil {
		t.Fatalf("over-consume: ok=%v err=%v", ok, err)
	}
	m.allow(11, rat(7, 3))
	checkState(t, l, m, "after refused consume")
	if got := l.Tokens(); got.Cmp(rat(2, 1)) != 0 {
		t.Fatalf("tokens = %s, want exactly 2", got)
	}

	// The refused request still settled the clock: asking for exactly 2 at
	// the same instant must now succeed.
	if ok, err := l.Allow(11, rat(2, 1)); !ok || err != nil {
		t.Fatalf("consume after refusal: ok=%v err=%v", ok, err)
	}
	m.allow(11, rat(2, 1))
	checkState(t, l, m, "after consume at same instant")
}

// A config change settles the bucket under the old config up to the switch
// instant, and only the new rate applies afterwards.
func TestRateSwitchSettlesUnderOldConfig(t *testing.T) {
	l, err := NewLimiter(Config{Capacity: rat(100, 1), Rate: rat(3, 2)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(rat(100, 1), rat(3, 2), 0)

	// Burn the bucket down to 1 token.
	if ok, err := l.Allow(0, rat(99, 1)); !ok || err != nil {
		t.Fatalf("burn: ok=%v err=%v", ok, err)
	}
	m.allow(0, rat(99, 1))

	// Switch at t=10: old rate 3/2 accrues up to t=10 (1 + 15 = 16),
	// then the new rate 1/7 applies.
	if err := l.SetConfig(10, Config{Capacity: rat(100, 1), Rate: rat(1, 7)}); err != nil {
		t.Fatal(err)
	}
	m.setConfig(10, rat(100, 1), rat(1, 7))
	checkState(t, l, m, "at switch")
	if got := l.Tokens(); got.Cmp(rat(16, 1)) != 0 {
		t.Fatalf("tokens at switch = %s, want 16", got)
	}

	// 14 ns later: 16 + 14/7 = 18 exactly.
	if ok, err := l.Allow(24, rat(0, 1)); !ok || err != nil {
		t.Fatalf("probe: ok=%v err=%v", ok, err)
	}
	m.allow(24, rat(0, 1))
	checkState(t, l, m, "after new-rate accrual")
	if got := l.Tokens(); got.Cmp(rat(18, 1)) != 0 {
		t.Fatalf("tokens = %s, want 18", got)
	}

	// Switch to a smaller capacity: settlement still uses the old config;
	// the balance above the new capacity is truncated lazily at the next
	// event, and the excess is discarded rather than carried.
	if err := l.SetConfig(24, Config{Capacity: rat(5, 1), Rate: rat(1, 1)}); err != nil {
		t.Fatal(err)
	}
	m.setConfig(24, rat(5, 1), rat(1, 1))
	checkState(t, l, m, "at capacity shrink")
	if ok, err := l.Allow(24, rat(0, 1)); !ok || err != nil {
		t.Fatalf("probe after shrink: ok=%v err=%v", ok, err)
	}
	m.allow(24, rat(0, 1))
	checkState(t, l, m, "after shrink truncation")
	if got := l.Tokens(); got.Cmp(rat(5, 1)) != 0 {
		t.Fatalf("tokens = %s, want exactly 5", got)
	}
}

// Backwards timestamps must be rejected and must not change any state.
func TestBackwardsTimeRejectedStateUnchanged(t *testing.T) {
	l, err := NewLimiter(Config{Capacity: rat(8, 1), Rate: rat(2, 5)}, 100)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(rat(8, 1), rat(2, 5), 100)

	if ok, err := l.Allow(130, rat(3, 1)); !ok || err != nil {
		t.Fatalf("setup consume: ok=%v err=%v", ok, err)
	}
	m.allow(130, rat(3, 1))
	checkState(t, l, m, "setup")

	snapshot := func() (string, int64, string, string) {
		cfg := l.Config()
		return l.Tokens().String(), l.Last(), cfg.Capacity.String(), cfg.Rate.String()
	}
	beforeTok, beforeLast, beforeCap, beforeRate := snapshot()

	if _, err := l.Allow(129, rat(1, 1)); err != ErrTimeBackwards {
		t.Fatalf("Allow backwards: err = %v, want ErrTimeBackwards", err)
	}
	if err := l.SetConfig(129, Config{Capacity: rat(4, 1), Rate: rat(1, 1)}); err != ErrTimeBackwards {
		t.Fatalf("SetConfig backwards: err = %v, want ErrTimeBackwards", err)
	}

	afterTok, afterLast, afterCap, afterRate := snapshot()
	if beforeTok != afterTok || beforeLast != afterLast ||
		beforeCap != afterCap || beforeRate != afterRate {
		t.Fatalf("state changed after backwards calls: (%s,%d,%s,%s) -> (%s,%d,%s,%s)",
			beforeTok, beforeLast, beforeCap, beforeRate,
			afterTok, afterLast, afterCap, afterRate)
	}
	checkState(t, l, m, "after backwards calls")

	// A call at the exact same timestamp is not backwards and must work.
	if ok, err := l.Allow(130, rat(1, 1)); !ok || err != nil {
		t.Fatalf("same-instant allow: ok=%v err=%v", ok, err)
	}
	m.allow(130, rat(1, 1))
	checkState(t, l, m, "same instant")
}

// Randomized cross-check: replay a long random event stream (consumes,
// config switches, deliberate backwards jumps) through both the limiter and
// the model and require agreement after every single event.
func TestRandomEventStreamMatchesModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	randRat := func(maxNum, maxDen int64) *big.Rat {
		return big.NewRat(rng.Int63n(maxNum), 1+rng.Int63n(maxDen))
	}

	cfg := Config{Capacity: randRat(50, 3), Rate: randRat(5, 7)}
	l, err := NewLimiter(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(cfg.Capacity, cfg.Rate, 0)

	now := int64(0)
	for i := 0; i < 2000; i++ {
		now += rng.Int63n(50)
		switch rng.Intn(10) {
		case 0: // config switch
			newCfg := Config{Capacity: randRat(50, 3), Rate: randRat(5, 7)}
			if err := l.SetConfig(now, newCfg); err != nil {
				t.Fatalf("event %d: SetConfig err = %v", i, err)
			}
			m.setConfig(now, newCfg.Capacity, newCfg.Rate)
		case 1: // backwards jump: must be rejected, state unchanged
			past := m.last - 1 - rng.Int63n(m.last+1)
			if _, err := l.Allow(past, rat(1, 1)); err != ErrTimeBackwards {
				t.Fatalf("event %d: backwards Allow err = %v", i, err)
			}
			if err := l.SetConfig(past, cfg); err != ErrTimeBackwards {
				t.Fatalf("event %d: backwards SetConfig err = %v", i, err)
			}
		default: // consume
			cost := randRat(20, 5)
			ok, err := l.Allow(now, cost)
			if err != nil {
				t.Fatalf("event %d: Allow err = %v", i, err)
			}
			wantOK, settled := m.allow(now, cost)
			if !settled {
				t.Fatalf("event %d: model rejected a valid timestamp", i)
			}
			if ok != wantOK {
				t.Fatalf("event %d: Allow = %v, model says %v (cost %s)",
					i, ok, wantOK, cost)
			}
		}
		checkState(t, l, m, "random stream")
	}
}
