package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
)

// Sampler is a local sampling policy. Implementations must not consume remote
// trace flags unless they are explicitly willing to let a trusted upstream
// choose the local decision.
type Sampler interface {
	Sample(traceID, tenantID, origin string) bool
}

// SamplerFunc adapts a function to Sampler.
type SamplerFunc func(traceID, tenantID, origin string) bool

// Sample implements Sampler.
func (f SamplerFunc) Sample(traceID, tenantID, origin string) bool {
	return f(traceID, tenantID, origin)
}

// AlwaysSample is the secure default: no request can evade collection by
// forging an upstream unsampled flag.
type AlwaysSample struct{}

// Sample implements Sampler.
func (AlwaysSample) Sample(string, string, string) bool {
	return true
}

// NeverSample disables sampling for a configured scope.
type NeverSample struct{}

// Sample implements Sampler.
func (NeverSample) Sample(string, string, string) bool {
	return false
}

// RateSampler makes a deterministic local decision from trace ID plus the
// authenticated tenant and origin. It is safe for concurrent use.
type RateSampler struct {
	threshold uint64
}

const rateSamplerBuckets = uint64(1) << 53

// NewRateSampler constructs a deterministic sampler for rate in [0,1].
func NewRateSampler(rate float64) (*RateSampler, error) {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 1 {
		return nil, fmt.Errorf("netsec: sampling rate must be between 0 and 1")
	}
	if rate == 0 {
		return &RateSampler{}, nil
	}
	if rate == 1 {
		return &RateSampler{threshold: rateSamplerBuckets}, nil
	}
	return &RateSampler{threshold: uint64(rate * float64(rateSamplerBuckets))}, nil
}

// MustRateSampler is NewRateSampler for static configuration.
func MustRateSampler(rate float64) *RateSampler {
	sampler, err := NewRateSampler(rate)
	if err != nil {
		panic(err)
	}
	return sampler
}

// Sample implements Sampler.
func (s *RateSampler) Sample(traceID, tenantID, origin string) bool {
	if s == nil || s.threshold == 0 {
		return false
	}
	if s.threshold >= rateSamplerBuckets {
		return true
	}
	h := sha256.New()
	for _, part := range []string{traceID, tenantID, origin} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		h.Write(size[:])
		h.Write([]byte(part))
	}
	sum := h.Sum(nil)
	bucket := binary.BigEndian.Uint64(sum[:8]) >> 11
	return bucket < s.threshold
}
