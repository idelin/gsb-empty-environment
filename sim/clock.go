package sim

import "time"

type Clock interface {
	Now() time.Time
}

type ReferenceClock struct {
	now time.Time
}

func NewReferenceClock(start time.Time) *ReferenceClock {
	return &ReferenceClock{now: start}
}

func (c *ReferenceClock) Now() time.Time {
	return c.now
}

func (c *ReferenceClock) Set(now time.Time) {
	if now.After(c.now) {
		c.now = now
	}
}

type SkewedClock struct {
	ref       *ReferenceClock
	offset    time.Duration
	num       int64
	den       int64
	originRef time.Time
}

func NewSkewedClock(ref *ReferenceClock, offset time.Duration, driftNumerator, driftDenominator int64) *SkewedClock {
	if driftDenominator == 0 {
		driftDenominator = 1
	}
	if driftNumerator == 0 {
		driftNumerator = driftDenominator
	}
	return &SkewedClock{
		ref:       ref,
		offset:    offset,
		num:       driftNumerator,
		den:       driftDenominator,
		originRef: ref.Now(),
	}
}

func (c *SkewedClock) Now() time.Time {
	elapsed := c.ref.Now().Sub(c.originRef)
	scaled := time.Duration(int64(elapsed) * c.num / c.den)
	return c.originRef.Add(c.offset).Add(scaled)
}

func (c *SkewedClock) Offset() time.Duration {
	return c.offset
}
