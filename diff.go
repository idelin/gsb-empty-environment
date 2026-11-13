package netsec

import "fmt"

// KeyInput is the five-dimensional input considered by the differential key
// audit. ObjectID is intentionally represented by the version dimension; use
// one domain per object when auditing a callback that ignores object identity.
type KeyInput struct {
	PrincipalID       PrincipalID
	RoleClosureDigest string
	ObjectVersion     uint64
	RLSDigest         string
	PolicyEpoch       uint64
}

// CacheKeyInput is an alias for KeyInput.
type CacheKeyInput = KeyInput

// KeyDomain supplies exactly two candidate values for every audited dimension.
type KeyDomain struct {
	Principals     [2]PrincipalID
	RoleClosures   [2]string
	ObjectVersions [2]uint64
	RLSDigests     [2]string
	PolicyEpochs   [2]uint64
}

// CacheKeyFunc is the candidate key callback under review.
type CacheKeyFunc func(KeyInput) string

// AuthorizationFunc returns the correct authorization decision for an input.
// A weak cache key is one that maps two inputs to the same key while these
// decisions differ.
type AuthorizationFunc func(KeyInput) bool

// Counterexample is the shortest input pair that distinguishes a weak key.
type Counterexample struct {
	First            KeyInput
	Second           KeyInput
	Distance         int
	Key              string
	FirstAuthorized  bool
	SecondAuthorized bool
}

// String renders a compact audit result.
func (c Counterexample) String() string {
	return fmt.Sprintf("distance=%d key=%q first=%+v(%t) second=%+v(%t)",
		c.Distance, c.Key, c.First, c.FirstAuthorized, c.Second, c.SecondAuthorized)
}

// FindWeakKeyCounterexample exhaustively searches the 2^5 domain in order of
// increasing Hamming distance. It returns the shortest pair whose key is equal
// while the correct authorization decision differs.
func FindWeakKeyCounterexample(keyFn CacheKeyFunc, authorize AuthorizationFunc, domain KeyDomain) (Counterexample, bool) {
	if keyFn == nil || authorize == nil {
		return Counterexample{}, false
	}
	inputs := expandDomain(domain)
	for distance := 1; distance <= 5; distance++ {
		for i := 0; i < len(inputs); i++ {
			for j := i + 1; j < len(inputs); j++ {
				if hammingDistance(i, j) != distance {
					continue
				}
				a, b := inputs[i], inputs[j]
				keyA, keyB := keyFn(a), keyFn(b)
				if keyA != keyB {
					continue
				}
				authorizedA, authorizedB := authorize(a), authorize(b)
				if authorizedA == authorizedB {
					continue
				}
				if authorizedA {
					return Counterexample{
						First: a, Second: b, Distance: distance, Key: keyA,
						FirstAuthorized: true, SecondAuthorized: false,
					}, true
				}
				return Counterexample{
					First: a, Second: b, Distance: distance, Key: keyA,
					FirstAuthorized: false, SecondAuthorized: true,
				}, true
			}
		}
	}
	return Counterexample{}, false
}

// SearchWeakCacheKey is an alias for FindWeakKeyCounterexample.
func SearchWeakCacheKey(keyFn CacheKeyFunc, authorize AuthorizationFunc, domain KeyDomain) (Counterexample, bool) {
	return FindWeakKeyCounterexample(keyFn, authorize, domain)
}

// SearchWeakKey is an alias for FindWeakKeyCounterexample.
func SearchWeakKey(keyFn CacheKeyFunc, authorize AuthorizationFunc, domain KeyDomain) (Counterexample, bool) {
	return FindWeakKeyCounterexample(keyFn, authorize, domain)
}

// FindWeakKey is an alias for FindWeakKeyCounterexample.
func FindWeakKey(keyFn CacheKeyFunc, authorize AuthorizationFunc, domain KeyDomain) (Counterexample, bool) {
	return FindWeakKeyCounterexample(keyFn, authorize, domain)
}

// FindShortestCounterexample is an alias for FindWeakKeyCounterexample.
func FindShortestCounterexample(keyFn CacheKeyFunc, authorize AuthorizationFunc, domain KeyDomain) (Counterexample, bool) {
	return FindWeakKeyCounterexample(keyFn, authorize, domain)
}

// AuditKeyCoverage returns an error when a callback has any shortest
// counterexample in the supplied two-value-per-dimension domain.
func AuditKeyCoverage(keyFn CacheKeyFunc, authorize AuthorizationFunc, domain KeyDomain) error {
	example, found := FindWeakKeyCounterexample(keyFn, authorize, domain)
	if !found {
		return nil
	}
	return fmt.Errorf("weak cache key: %s", example)
}

func expandDomain(domain KeyDomain) [32]KeyInput {
	var out [32]KeyInput
	for mask := 0; mask < 32; mask++ {
		out[mask] = KeyInput{
			PrincipalID:       domain.Principals[(mask>>0)&1],
			RoleClosureDigest: domain.RoleClosures[(mask>>1)&1],
			ObjectVersion:     domain.ObjectVersions[(mask>>2)&1],
			RLSDigest:         domain.RLSDigests[(mask>>3)&1],
			PolicyEpoch:       domain.PolicyEpochs[(mask>>4)&1],
		}
	}
	return out
}

func hammingDistance(a, b int) int {
	x := a ^ b
	distance := 0
	for x != 0 {
		distance += x & 1
		x >>= 1
	}
	return distance
}
