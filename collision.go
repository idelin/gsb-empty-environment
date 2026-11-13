package netsec

import (
	"bytes"
	"sort"
	"strconv"
)

// FieldCandidates supplies two candidate values for each canonical identity
// field. Differential collision search enumerates every combination of the two
// candidates.
type FieldCandidates struct {
	TenantID           [2]string
	RoleID             [2]string
	PolicyDigest       [2]Digest
	SessionTagsDigest  [2]Digest
	ExternalIdentityID [2]string
	Audience           [2]string
	ProviderGeneration [2]uint64
}

// WeakCacheKeyFunc is the cache-key callback under review.
type WeakCacheKeyFunc func(Identity) string

// Collision is a differential witness. WeakKey is equal for A and B while the
// canonical identities and therefore CacheKey values differ.
type Collision struct {
	A               Identity
	B               Identity
	WeakKey         string
	WeakKeyA        string
	WeakKeyB        string
	CanonicalKeyA   Digest
	CanonicalKeyB   Digest
	DifferingFields []string
}

var identityFieldOrder = map[string]int{
	"tenantID":           0,
	"roleID":             1,
	"policyDigest":       2,
	"sessionTagsDigest":  3,
	"externalIdentityID": 4,
	"audience":           5,
	"providerGeneration": 6,
}

// FindDifferentialCollision searches all pairs formed from FieldCandidates for
// an example where weak returns the same cache key but the canonical identities
// differ and either the tenants or the policy digests differ.
//
// The shortest witness is selected by fewest differing identity fields, then
// by smallest combined canonical encoding, then by deterministic enumeration
// order.
func FindDifferentialCollision(weak WeakCacheKeyFunc, candidates FieldCandidates) (Collision, bool) {
	if weak == nil {
		return Collision{}, false
	}

	identities := candidates.identities()
	var best *Collision
	var bestScore collisionScore
	for i := 0; i < len(identities); i++ {
		a := identities[i]
		for j := i + 1; j < len(identities); j++ {
			b := identities[j]
			if a == b {
				continue
			}
			if a.TenantID == b.TenantID && a.PolicyDigest == b.PolicyDigest {
				continue
			}
			weakA := weak(a)
			weakB := weak(b)
			if weakA != weakB {
				continue
			}
			canonicalA := a.CacheKey()
			canonicalB := b.CacheKey()
			if canonicalA == canonicalB {
				continue
			}
			fields := differingIdentityFields(a, b)
			score := collisionScore{
				differingFields: len(fields),
				encodedLength:   len(a.CanonicalBytes()) + len(b.CanonicalBytes()),
				maskSum:         i + j,
			}
			witness := Collision{
				A:               a,
				B:               b,
				WeakKey:         weakA,
				WeakKeyA:        weakA,
				WeakKeyB:        weakB,
				CanonicalKeyA:   canonicalA,
				CanonicalKeyB:   canonicalB,
				DifferingFields: fields,
			}
			if best == nil || score.less(bestScore, a, b, best.A, best.B) {
				copy := witness
				best = &copy
				bestScore = score
			}
		}
	}
	if best == nil {
		return Collision{}, false
	}
	return *best, true
}

// FindCacheKeyCollision is an alias for FindDifferentialCollision.
func FindCacheKeyCollision(weak WeakCacheKeyFunc, candidates FieldCandidates) (Collision, bool) {
	return FindDifferentialCollision(weak, candidates)
}

type collisionScore struct {
	differingFields int
	encodedLength   int
	maskSum         int
}

func (s collisionScore) less(other collisionScore, a, b, otherA, otherB Identity) bool {
	if s.differingFields != other.differingFields {
		return s.differingFields < other.differingFields
	}
	if s.encodedLength != other.encodedLength {
		return s.encodedLength < other.encodedLength
	}
	if s.maskSum != other.maskSum {
		return s.maskSum < other.maskSum
	}
	if cmp := bytes.Compare(a.CanonicalBytes(), otherA.CanonicalBytes()); cmp != 0 {
		return cmp < 0
	}
	return bytes.Compare(b.CanonicalBytes(), otherB.CanonicalBytes()) < 0
}

func (c FieldCandidates) identities() []Identity {
	const combinations = 1 << 7
	out := make([]Identity, 0, combinations)
	for mask := 0; mask < combinations; mask++ {
		out = append(out, c.identity(mask))
	}
	return out
}

func (c FieldCandidates) identity(mask int) Identity {
	pick := func(bit int) int {
		if mask&(1<<bit) != 0 {
			return 1
		}
		return 0
	}
	return Identity{
		TenantID:           c.TenantID[pick(0)],
		RoleID:             c.RoleID[pick(1)],
		PolicyDigest:       c.PolicyDigest[pick(2)],
		SessionTagsDigest:  c.SessionTagsDigest[pick(3)],
		ExternalIdentityID: c.ExternalIdentityID[pick(4)],
		Audience:           c.Audience[pick(5)],
		ProviderGeneration: c.ProviderGeneration[pick(6)],
	}
}

func differingIdentityFields(a, b Identity) []string {
	var fields []string
	if a.TenantID != b.TenantID {
		fields = append(fields, "tenantID")
	}
	if a.RoleID != b.RoleID {
		fields = append(fields, "roleID")
	}
	if a.PolicyDigest != b.PolicyDigest {
		fields = append(fields, "policyDigest")
	}
	if a.SessionTagsDigest != b.SessionTagsDigest {
		fields = append(fields, "sessionTagsDigest")
	}
	if a.ExternalIdentityID != b.ExternalIdentityID {
		fields = append(fields, "externalIdentityID")
	}
	if a.Audience != b.Audience {
		fields = append(fields, "audience")
	}
	if a.ProviderGeneration != b.ProviderGeneration {
		fields = append(fields, "providerGeneration")
	}
	sort.Slice(fields, func(i, j int) bool {
		return identityFieldOrder[fields[i]] < identityFieldOrder[fields[j]]
	})
	return fields
}

// String returns a compact human-readable collision description.
func (c Collision) String() string {
	return "weak key " + strconv.Quote(c.WeakKey) +
		" collides across fields " + joinFields(c.DifferingFields)
}

func joinFields(fields []string) string {
	out := ""
	for i, field := range fields {
		if i != 0 {
			out += ","
		}
		out += field
	}
	return out
}
