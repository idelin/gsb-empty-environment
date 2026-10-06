// Package gateway simulates a gateway that admits requests through a
// ratelimit.Limiter. Requests arrive at integer nanosecond timestamps and
// each request costs a rational number of tokens.
package gateway

import (
	"math/big"

	"gwsim/ratelimit"
)

// Request is a single admission attempt at the gateway.
type Request struct {
	Time int64    // arrival time in integer nanoseconds
	Cost *big.Rat // tokens charged to the bucket
}

// Decision records the outcome of one Request.
type Decision struct {
	Request Request
	Allowed bool
}

// Simulator drives a limiter with an ordered stream of requests.
type Simulator struct {
	limiter *ratelimit.Limiter
}

// NewSimulator wraps an existing limiter.
func NewSimulator(l *ratelimit.Limiter) *Simulator {
	return &Simulator{limiter: l}
}

// Handle processes one request and reports whether it was admitted. A
// backwards timestamp is rejected with ratelimit.ErrTimeBackwards and the
// limiter state is left unchanged.
func (s *Simulator) Handle(req Request) (bool, error) {
	return s.limiter.Allow(req.Time, req.Cost)
}

// Run processes requests in order and returns one Decision per request. It
// stops at the first error (for example a backwards timestamp) and returns
// the decisions made so far together with the error.
func (s *Simulator) Run(reqs []Request) ([]Decision, error) {
	decisions := make([]Decision, 0, len(reqs))
	for _, req := range reqs {
		allowed, err := s.Handle(req)
		if err != nil {
			return decisions, err
		}
		decisions = append(decisions, Decision{Request: req, Allowed: allowed})
	}
	return decisions, nil
}
