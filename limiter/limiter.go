// Package limiter implements a token-bucket rate limiter for a gateway
// simulation. Time is an integer nanosecond timestamp and the refill rate
// is a rational number of tokens per nanosecond, so all arithmetic is
// exact: no floating point rounding ever enters the bucket state.
package limiter

import (
	"errors"
	"fmt"
	"math/big"
)

// ErrTimeBackward is returned when an event carries a timestamp earlier
// than the last timestamp the limiter has seen. The limiter state is left
// completely unchanged in that case.
var ErrTimeBackward = errors.New("limiter: time moved backwards")

// Rate is a rational refill speed: Num/Den tokens per nanosecond.
type Rate struct {
	Num int64
	Den int64
}

func (r Rate) rat() *big.Rat {
	return big.NewRat(r.Num, r.Den)
}

// Config describes a token bucket: a rational refill rate and an integer
// capacity in tokens.
type Config struct {
	Rate     Rate
	Capacity int64
}

func (c Config) validate() error {
	if c.Rate.Den <= 0 {
		return fmt.Errorf("limiter: rate denominator must be positive, got %d", c.Rate.Den)
	}
	if c.Rate.Num < 0 {
		return fmt.Errorf("limiter: rate numerator must be non-negative, got %d", c.Rate.Num)
	}
	if c.Capacity < 0 {
		return fmt.Errorf("limiter: capacity must be non-negative, got %d", c.Capacity)
	}
	return nil
}

// Limiter is a lazily-refilled token bucket. Tokens are only (logically)
// added when an event arrives; the amount added is computed exactly from
// the elapsed nanoseconds. When the lazy refill would exceed the capacity,
// the bucket is truncated to the capacity and the excess is discarded —
// no truncated remainder is retained anywhere.
type Limiter struct {
	cfg    Config
	tokens *big.Rat
	last   int64
}

// New creates a limiter that starts full at timestamp now.
func New(cfg Config, now int64) (*Limiter, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Limiter{
		cfg:    cfg,
		tokens: big.NewRat(cfg.Capacity, 1),
		last:   now,
	}, nil
}

// advance settles the bucket under the current configuration up to now.
// It returns ErrTimeBackward (without touching any state) when now is
// earlier than the last settled timestamp.
func (l *Limiter) advance(now int64) error {
	if now < l.last {
		return ErrTimeBackward
	}
	elapsed := now - l.last
	if elapsed > 0 {
		gain := new(big.Rat).Mul(l.cfg.Rate.rat(), big.NewRat(elapsed, 1))
		l.tokens.Add(l.tokens, gain)
		l.truncate()
		l.last = now
	}
	return nil
}

// truncate caps the bucket at its capacity, discarding any excess.
func (l *Limiter) truncate() {
	capRat := big.NewRat(l.cfg.Capacity, 1)
	if l.tokens.Cmp(capRat) > 0 {
		l.tokens.Set(capRat)
	}
}

// Allow reports whether n tokens are available at timestamp now and, if
// so, consumes them. The bucket is first refilled lazily and exactly for
// the elapsed time. A rejected (insufficient tokens) request consumes
// nothing, but the refill up to now still stands. A backwards timestamp
// is rejected with ErrTimeBackward and leaves the state unchanged.
func (l *Limiter) Allow(now int64, n int64) (bool, error) {
	if n < 0 {
		return false, fmt.Errorf("limiter: cannot consume a negative amount %d", n)
	}
	if err := l.advance(now); err != nil {
		return false, err
	}
	want := big.NewRat(n, 1)
	if l.tokens.Cmp(want) < 0 {
		return false, nil
	}
	l.tokens.Sub(l.tokens, want)
	return true, nil
}

// SetConfig switches the limiter to cfg at timestamp now. The bucket is
// first settled under the old configuration up to the switch moment, then
// the new configuration applies from now on. If the current balance
// exceeds the new capacity it is truncated and the excess discarded.
// A backwards timestamp is rejected with ErrTimeBackward and leaves both
// the state and the configuration unchanged.
func (l *Limiter) SetConfig(now int64, cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if err := l.advance(now); err != nil {
		return err
	}
	l.cfg = cfg
	l.truncate()
	return nil
}

// Tokens returns the current exact token balance.
func (l *Limiter) Tokens() *big.Rat {
	return new(big.Rat).Set(l.tokens)
}

// Last returns the last settled timestamp.
func (l *Limiter) Last() int64 {
	return l.last
}

// Config returns the active configuration.
func (l *Limiter) Config() Config {
	return l.cfg
}
