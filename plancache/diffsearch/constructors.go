package diffsearch

import "formal.local/w3-bc80ec5616/plancache"

// FullKeyConstructor is the reference constructor: it uses the complete
// CacheKey hash and therefore cannot collide across any of the six
// dimensions. It is the expected "passes review" result.
type FullKeyConstructor struct{}

// Name implements KeyConstructor.
func (FullKeyConstructor) Name() string { return "full-key" }

// BuildKey implements KeyConstructor.
func (FullKeyConstructor) BuildKey(in plancache.CompileInput) string {
	return plancache.KeyOf(in).Hash()
}

// TextOnlyConstructor models the banned anti-pattern of keying on a query
// digest plus parameter types while dropping every authorization and
// dependency dimension. It collides on the very first distance-1 pair
// (principal switch).
type TextOnlyConstructor struct{}

// Name implements KeyConstructor.
func (TextOnlyConstructor) Name() string { return "text-only-weak" }

// BuildKey implements KeyConstructor.
func (TextOnlyConstructor) BuildKey(in plancache.CompileInput) string {
	key := plancache.KeyOf(in)
	key.Caller = ""
	key.RoleClosure = ""
	key.DepGraph = nil
	key.RLSPolicies = nil
	key.PolicyEpoch = 0
	return key.Hash()
}

// PrincipalOnlyConstructor keeps the principal but drops role closure,
// dependency graph and policy state. Its shortest collision is a role
// closure flip.
type PrincipalOnlyConstructor struct{}

// Name implements KeyConstructor.
func (PrincipalOnlyConstructor) Name() string { return "principal-only-weak" }

// BuildKey implements KeyConstructor.
func (PrincipalOnlyConstructor) BuildKey(in plancache.CompileInput) string {
	key := plancache.KeyOf(in)
	key.RoleClosure = ""
	key.DepGraph = nil
	key.RLSPolicies = nil
	key.PolicyEpoch = 0
	return key.Hash()
}

// EpochBlindConstructor keeps identity and dependency versions but omits the
// per-table RLS digest and global policy epoch, modeling a cache that
// reuses plans after an RLS tightening. Its shortest collision is an RLS
// digest flip at distance one.
type EpochBlindConstructor struct{}

// Name implements KeyConstructor.
func (EpochBlindConstructor) Name() string { return "epoch-blind-weak" }

// BuildKey implements KeyConstructor.
func (EpochBlindConstructor) BuildKey(in plancache.CompileInput) string {
	key := plancache.KeyOf(in)
	graph := make([]plancache.DependencyNode, len(key.DepGraph))
	copy(graph, key.DepGraph)
	for i := range graph {
		if graph[i].RefType == "table" {
			graph[i].Table.RLSPolicy = ""
		}
	}
	key.DepGraph = graph
	key.RLSPolicies = nil
	key.PolicyEpoch = 0
	return key.Hash()
}

// ReviewSet is the standard constructor lineup for cache-key review.
func ReviewSet() []KeyConstructor {
	return []KeyConstructor{
		FullKeyConstructor{},
		TextOnlyConstructor{},
		PrincipalOnlyConstructor{},
		EpochBlindConstructor{},
	}
}
