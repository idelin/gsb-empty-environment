package netsec

// MergePolicy decides whether an incoming page may be mapped onto a resident
// physical page.  The host only offers candidates with equal digests; the
// policy is the authorization layer for domain, tags and shareability.
type MergePolicy interface {
	CanMerge(incoming, resident Page) bool
	Name() string
}

// PolicyFunc adapts a function to MergePolicy.
type PolicyFunc func(incoming, resident Page) bool

// CanMerge implements MergePolicy.
func (f PolicyFunc) CanMerge(incoming, resident Page) bool { return f(incoming, resident) }

// Name implements MergePolicy.
func (f PolicyFunc) Name() string { return "callback" }

// ConservativePolicy is the default policy: pages must be in the same domain,
// both explicitly shareable, and neither page may carry a sensitive tag.
type ConservativePolicy struct{}

// CanMerge implements MergePolicy.
func (ConservativePolicy) CanMerge(incoming, resident Page) bool {
	return incoming.Digest == resident.Digest &&
		incoming.DomainID == resident.DomainID &&
		incoming.Shareable && resident.Shareable &&
		!incoming.IsSensitive() && !resident.IsSensitive()
}

// Name implements MergePolicy.
func (ConservativePolicy) Name() string { return "conservative" }

// HardenedPolicy is the policy used to check that cross-domain observations
// are independent of a victim secret.  It permits same-domain deduplication
// only when both pages are explicitly shareable and tag-free.
type HardenedPolicy struct{}

// CanMerge implements MergePolicy.
func (HardenedPolicy) CanMerge(incoming, resident Page) bool {
	return ConservativePolicy{}.CanMerge(incoming, resident)
}

// Name implements MergePolicy.
func (HardenedPolicy) Name() string { return "hardened" }

// GlobalDedupPolicy is deliberately insecure.  It ignores domains, sensitive
// tags and shareability, allowing the cross-domain COW probe to be reproduced.
// The host still only offers pages with equal digests.
type GlobalDedupPolicy struct{}

// CanMerge implements MergePolicy.
func (GlobalDedupPolicy) CanMerge(incoming, resident Page) bool { return true }

// Name implements MergePolicy.
func (GlobalDedupPolicy) Name() string { return "global-insecure" }

// DefaultPolicy is the safe policy used by NewMemory.
var DefaultPolicy MergePolicy = ConservativePolicy{}

// Hardened is a convenient zero-value hardened policy.
var Hardened MergePolicy = HardenedPolicy{}

// GlobalDedup is a convenient zero-value insecure policy.
var GlobalDedup MergePolicy = GlobalDedupPolicy{}

// Aliases used by callers that prefer different terminology.
type SafePolicy = ConservativePolicy
type StrictPolicy = ConservativePolicy
type SecurePolicy = HardenedPolicy
type InsecurePolicy = GlobalDedupPolicy

// coercePolicy accepts the policy forms used by the public configuration
// structs: a MergePolicy implementation, a callback over Page snapshots, or a
// callback over page pointers.
func coercePolicy(value any) MergePolicy {
	switch policy := value.(type) {
	case nil:
		return nil
	case MergePolicy:
		return policy
	case func(Page, Page) bool:
		return PolicyFunc(policy)
	case func(*Page, *Page) bool:
		return PolicyFunc(func(incoming, resident Page) bool {
			return policy(&incoming, &resident)
		})
	default:
		return nil
	}
}
