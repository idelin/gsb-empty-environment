package grantsweep

import (
	"fmt"
	"sort"
	"strings"
)

// CacheEntry is a pending-index entry used by a host cache.
type CacheEntry struct {
	GrantID    string
	Generation uint64
	TokenID    string
	RequestID  string
	Creator    Principal
	Grantee    Principal
	Operation  Operation
	KeyID      string
	KeyVersion uint64
}

// CacheKey returns the strong pending-index key. It binds every security
// dimension, so entries that differ in principal, operation, resource or
// generation can never alias.
func CacheKey(e CacheEntry) string {
	return strings.Join([]string{
		"v1",
		e.GrantID,
		fmt.Sprintf("gen=%d", e.Generation),
		"tok=" + e.TokenID,
		"req=" + e.RequestID,
		"creator=" + string(e.Creator),
		"grantee=" + string(e.Grantee),
		"op=" + string(e.Operation),
		fmt.Sprintf("key=%s/v%d", e.KeyID, e.KeyVersion),
	}, "|")
}

// WeakGrantIDKey is the vulnerable lookup that keys only on GrantID. Two
// entries for the same GrantID collide regardless of whose token it is or what
// operation/resource it was scoped to.
func WeakGrantIDKey(e CacheEntry) string { return "gid=" + e.GrantID }

// CacheCollision is an alias pair found by differential key search.
type CacheCollision struct {
	A, B CacheEntry
	Key  string
}

// SearchCacheKeyCollisions builds entries that differ on exactly one security
// dimension at a time and reports every pair the weak key collapses while the
// strong key keeps distinct.
func SearchCacheKeyCollisions() []CacheCollision {
	base := CacheEntry{
		GrantID: "g1", Generation: 0, TokenID: "tok-1", RequestID: "r1",
		Creator: "alice", Grantee: "bob", Operation: OpDecrypt,
		KeyID: "k1", KeyVersion: 1,
	}
	mutants := []struct {
		name string
		fn   func(*CacheEntry)
	}{
		{"generation", func(e *CacheEntry) { e.Generation = 1 }},
		{"tokenID", func(e *CacheEntry) { e.TokenID = "tok-2" }},
		{"requestID", func(e *CacheEntry) { e.RequestID = "r2" }},
		{"creator", func(e *CacheEntry) { e.Creator = "mallory" }},
		{"grantee", func(e *CacheEntry) { e.Grantee = "mallory" }},
		{"operation", func(e *CacheEntry) { e.Operation = OpSign }},
		{"keyID", func(e *CacheEntry) { e.KeyID = "k2" }},
		{"keyVersion", func(e *CacheEntry) { e.KeyVersion = 2 }},
	}

	var collisions []CacheCollision
	for _, m := range mutants {
		mutated := base
		m.fn(&mutated)
		if CacheKey(base) == CacheKey(mutated) {
			continue
		}
		if WeakGrantIDKey(base) == WeakGrantIDKey(mutated) {
			collisions = append(collisions, CacheCollision{
				A: base, B: mutated, Key: WeakGrantIDKey(base),
			})
		}
	}
	sort.Slice(collisions, func(i, j int) bool { return collisions[i].Key < collisions[j].Key })
	return collisions
}
