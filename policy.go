package netsec

import (
	"hash/fnv"
	"sort"
	"strconv"
)

// ValuePolicy decides what happens to a label value that is not present in
// KeyPolicy.Whitelist or exceeds KeyPolicy.MaxLength.
type ValuePolicy int

const (
	// Reject rejects any value that fails whitelist or length validation.
	// The offending admission returns ErrValueNotAllowed / ErrValueTooLong.
	Reject ValuePolicy = iota
	// Bucket collapses non-conforming values into a fixed set of buckets,
	// keeping the cardinality of the dimension bounded.
	Bucket
)

// BucketOther receives every overlong value when the policy is Bucket. Using
// a single fixed bucket (rather than a hash of the value) guarantees that
// attackers cannot grow cardinality with arbitrarily long strings.
const BucketOther = "__other__"

// KeyPolicy constrains a single label key.
type KeyPolicy struct {
	// MaxLength bounds the byte length of accepted values. Values longer
	// than this are rejected or folded into BucketOther according to Policy.
	// A value of zero means "no length limit".
	MaxLength int
	// Whitelist enumerates the only values accepted as-is. It may be nil for
	// free-form dimensions that rely on bucketing for cardinality control.
	Whitelist map[string]struct{}
	// Policy selects the action for values that are not whitelisted or are
	// too long. Reject is the safe default.
	Policy ValuePolicy
	// BucketCount is the number of hash buckets used when Policy is Bucket
	// and the dimension is free-form (empty Whitelist). Bucket ids span
	// [0, BucketCount). BucketCount must be positive for a free-form bucket
	// policy; zero makes every non-whitelisted value fail.
	BucketCount int
}

// Label is a validated, normalized key/value pair produced by Guard.Admit.
type Label struct {
	Key   string
	Value string
}

// normalize validates and canonicalizes a single label against its policy.
// The returned value is either the original whitelisted value or a bucket
// name whose cardinality is bounded by the policy.
func (p KeyPolicy) normalize(key, value string) (string, error) {
	if p.Whitelist != nil {
		if _, ok := p.Whitelist[value]; ok && (p.MaxLength == 0 || len(value) <= p.MaxLength) {
			return value, nil
		}
		return p.handleNonConforming(value)
	}

	// Free-form dimension. Length is enforced first so oversized input can
	// never influence the bucket space.
	if p.MaxLength > 0 && len(value) > p.MaxLength {
		return p.handleNonConforming(value)
	}
	if value == "" {
		// Empty values carry no information and would collide silently; treat
		// them as non-conforming input.
		return p.handleNonConforming(value)
	}

	if p.Policy != Bucket || p.BucketCount <= 0 {
		return "", ErrValueNotAllowed
	}
	return bucketValue(value, p.BucketCount), nil
}

func (p KeyPolicy) handleNonConforming(value string) (string, error) {
	if p.Policy != Bucket {
		if p.MaxLength > 0 && len(value) > p.MaxLength {
			return "", ErrValueTooLong
		}
		return "", ErrValueNotAllowed
	}
	return BucketOther, nil
}

// bucketValue maps an arbitrary value into a stable, fixed-width bucket name.
func bucketValue(value string, count int) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	return "bucket-" + strconv.FormatUint(h.Sum64()%uint64(count), 10)
}

// canonicalize sorts labels by key so that the same logical series always
// produces the same fingerprint regardless of input order.
func canonicalize(labels []Label) {
	sort.Slice(labels, func(i, j int) bool { return labels[i].Key < labels[j].Key })
}

// fingerprint hashes a canonical label set into a series identifier. FNV-64
// collisions are astronomically unlikely at the bounded series counts this
// component enforces; treating collisions as equal series is safe (it can only
// under-count, never over-grow memory).
func fingerprint(labels []Label) uint64 {
	h := fnv.New64a()
	for i := range labels {
		_, _ = h.Write([]byte(labels[i].Key))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(labels[i].Value))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}
