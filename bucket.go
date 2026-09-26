package tbsim

import "math/big"

// Rat builds an exact rational n/d.
func Rat(n, d int64) *big.Rat { return big.NewRat(n, d) }

// Bucket is a token bucket with an exact rational fill rate (tokens/tick).
// Refill is lazy and monotone: Advance(now) with now <= last is a no-op, so a
// clock that jumps backwards and later recovers can never refill twice.
type Bucket struct {
	Name string
	Cap  int64 // capacity in tokens (== max burst, tokens never exceed this)

	rate      *big.Rat // tokens per tick
	avail     *big.Rat // tokens available at time `last`
	last      int64    // tick of last state update (monotone, never rewinds)
	born      int64    // creation tick
	consumed  int64    // total tokens consumed by passed packets
	generated *big.Rat // total tokens generated since birth
	overflow  *big.Rat // tokens discarded because of the cap (conservation evidence)
}

func NewBucket(name string, rate *big.Rat, cap, born int64) *Bucket {
	return &Bucket{
		Name:      name,
		Cap:       cap,
		rate:      new(big.Rat).Set(rate),
		avail:     new(big.Rat).SetInt64(cap), // start full: initial burst allowed
		last:      born,
		born:      born,
		generated: new(big.Rat),
		overflow:  new(big.Rat),
	}
}

// Advance moves the bucket clock to `now`. Backwards or repeated stamps are
// ignored: tokens are refilled exactly once per elapsed tick, even across
// clock rewinds and recovery.
func (b *Bucket) Advance(now int64) {
	if now <= b.last {
		return
	}
	dt := new(big.Rat).SetInt64(now - b.last)
	add := new(big.Rat).Mul(b.rate, dt)
	b.generated.Add(b.generated, add)
	b.avail.Add(b.avail, add)
	if capRat := new(big.Rat).SetInt64(b.Cap); b.avail.Cmp(capRat) > 0 {
		b.overflow.Add(b.overflow, new(big.Rat).Sub(b.avail, capRat))
		b.avail.Set(capRat) // idle tokens are capped, never above Cap
	}
	b.last = now
}

// SetRate hot-swaps the fill rate. The bucket is first settled at `now`, so
// the old rate is accounted exactly once and the new rate applies from `now`.
func (b *Bucket) SetRate(now int64, rate *big.Rat) {
	b.Advance(now)
	b.rate = new(big.Rat).Set(rate)
}

// Has reports whether size tokens are available at `now`.
func (b *Bucket) Has(now, size int64) bool {
	b.Advance(now)
	return b.avail.Cmp(new(big.Rat).SetInt64(size)) >= 0
}

// WaitTicks returns the minimal number of ticks after `now` until size tokens
// are available, or -1 if the bucket never refills (zero rate).
func (b *Bucket) WaitTicks(now, size int64) int64 {
	b.Advance(now)
	if b.avail.Cmp(new(big.Rat).SetInt64(size)) >= 0 {
		return 0
	}
	if b.rate.Sign() == 0 {
		return -1
	}
	need := new(big.Rat).Sub(new(big.Rat).SetInt64(size), b.avail)
	q := new(big.Rat).Quo(need, b.rate) // > 0
	num, den := q.Num(), q.Denom()
	ceil := new(big.Int).Add(num, den)
	ceil.Sub(ceil, big.NewInt(1))
	return ceil.Quo(ceil, den).Int64()
}

// Consume removes size tokens at `now`; caller must ensure Has(now, size).
func (b *Bucket) Consume(now, size int64) {
	b.Advance(now)
	if b.avail.Cmp(new(big.Rat).SetInt64(size)) < 0 {
		panic("tbsim: consume without tokens")
	}
	b.avail.Sub(b.avail, new(big.Rat).SetInt64(size))
	b.consumed += size
}

// Avail returns the exact token level at `now`.
func (b *Bucket) Avail(now int64) *big.Rat {
	b.Advance(now)
	return new(big.Rat).Set(b.avail)
}

// Last returns the tick of the last state update.
func (b *Bucket) Last() int64 { return b.last }

// Audit is conservation evidence for a bucket:
// consumed + remaining + overflow == initialCap + generated.
type Audit struct {
	Name      string
	Generated *big.Rat
	Consumed  *big.Rat
	Remaining *big.Rat
	Overflow  *big.Rat
	Conserved bool
}

func (b *Bucket) Audit(now int64) Audit {
	b.Advance(now)
	lhs := new(big.Rat).SetInt64(b.consumed)
	lhs.Add(lhs, b.avail)
	lhs.Add(lhs, b.overflow)
	rhs := new(big.Rat).Add(b.generated, new(big.Rat).SetInt64(b.Cap))
	return Audit{
		Name:      b.Name,
		Generated: new(big.Rat).Set(b.generated),
		Consumed:  new(big.Rat).SetInt64(b.consumed),
		Remaining: new(big.Rat).Set(b.avail),
		Overflow:  new(big.Rat).Set(b.overflow),
		Conserved: lhs.Cmp(rhs) == 0,
	}
}
