// Package ratelimit implements an exactly-computed token bucket limiter for
// a gateway simulation. Time is expressed in integer nanoseconds and token
// rates are rational numbers, so all accounting is exact: refills are
// computed lazily at each event with rational arithmetic, and whenever the
// bucket would exceed its capacity it is truncated to the capacity and the
// truncated remainder is discarded (never carried into later events).
package ratelimit

import (
	"errors"
	"math/big"
)

// ErrTimeBackwards is returned when an event timestamp is earlier than the
// timestamp of the last settled event. The limiter state is left unchanged.
var ErrTimeBackwards = errors.New("ratelimit: time went backwards")

// Config describes a token bucket: Capacity is the maximum number of tokens
// the bucket can hold and Rate is the number of tokens refilled per
// nanosecond. Both are exact rational values.
type Config struct {
	Capacity *big.Rat
	Rate     *big.Rat
}

func validConfig(cfg Config) bool {
	return cfg.Capacity != nil && cfg.Capacity.Sign() >= 0 &&
		cfg.Rate != nil && cfg.Rate.Sign() >= 0
}

// Limiter is a lazily-refilled token bucket. It is not safe for concurrent
// use; the gateway simulation drives it from a single event loop.
type Limiter struct {
	cfg    Config
	tokens *big.Rat
	last   int64 // nanosecond timestamp of the last settled event
}

// NewLimiter returns a limiter that starts full at the given nanosecond
// timestamp. The config is copied.
func NewLimiter(cfg Config, now int64) (*Limiter, error) {
	if !validConfig(cfg) {
		return nil, errors.New("ratelimit: invalid config")
	}
	return &Limiter{
		cfg: Config{
			Capacity: new(big.Rat).Set(cfg.Capacity),
			Rate:     new(big.Rat).Set(cfg.Rate),
		},
		tokens: new(big.Rat).Set(cfg.Capacity),
		last:   now,
	}, nil
}

// settle advances the bucket to now using the current config: it adds
// rate*elapsed tokens and truncates to the capacity, discarding any
// remainder beyond the capacity. A backwards timestamp is rejected and
// leaves the state untouched.
func (l *Limiter) settle(now int64) error {
	if now < l.last {
		return ErrTimeBackwards
	}
	elapsed := now - l.last
	if elapsed > 0 {
		gain := new(big.Rat).SetInt64(elapsed)
		gain.Mul(gain, l.cfg.Rate)
		l.tokens.Add(l.tokens, gain)
		l.last = now
	}
	if l.tokens.Cmp(l.cfg.Capacity) > 0 {
		l.tokens.Set(l.cfg.Capacity)
	}
	return nil
}

// Allow settles the bucket to now and then tries to withdraw cost tokens.
// It reports whether the withdrawal succeeded. Failed withdrawals leave the
// (settled) tokens untouched. A backwards timestamp is rejected with
// ErrTimeBackwards and the state is left unchanged.
func (l *Limiter) Allow(now int64, cost *big.Rat) (bool, error) {
	if cost == nil || cost.Sign() < 0 {
		return false, errors.New("ratelimit: invalid cost")
	}
	if err := l.settle(now); err != nil {
		return false, err
	}
	if l.tokens.Cmp(cost) < 0 {
		return false, nil
	}
	l.tokens.Sub(l.tokens, cost)
	return true, nil
}

// SetConfig settles the bucket to now under the OLD config and only then
// switches to the new config. Tokens above the new capacity are kept until
// the next event, where lazy truncation applies. A backwards timestamp is
// rejected with ErrTimeBackwards and the state (including the old config)
// is left unchanged.
func (l *Limiter) SetConfig(now int64, cfg Config) error {
	if !validConfig(cfg) {
		return errors.New("ratelimit: invalid config")
	}
	if err := l.settle(now); err != nil {
		return err
	}
	l.cfg = Config{
		Capacity: new(big.Rat).Set(cfg.Capacity),
		Rate:     new(big.Rat).Set(cfg.Rate),
	}
	return nil
}

// Tokens returns a copy of the current token balance.
func (l *Limiter) Tokens() *big.Rat {
	return new(big.Rat).Set(l.tokens)
}

// Last returns the nanosecond timestamp of the last settled event.
func (l *Limiter) Last() int64 {
	return l.last
}

// Config returns a copy of the active config.
func (l *Limiter) Config() Config {
	return Config{
		Capacity: new(big.Rat).Set(l.cfg.Capacity),
		Rate:     new(big.Rat).Set(l.cfg.Rate),
	}
}
