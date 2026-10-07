package groupx

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
)

// mergeGrant is a one-time authorization to merge a specific set of source
// tenants into a specific new namespace.
type mergeGrant struct {
	sources map[string]struct{}
	target  string
}

var (
	ErrMergeDisabled     = errors.New("groupx: merges disabled (no admin key)")
	ErrBadAdminKey       = errors.New("groupx: invalid admin key")
	ErrInvalidMerge      = errors.New("groupx: merge needs at least two distinct source tenants and a target")
	ErrTargetExists      = errors.New("groupx: merge target namespace already exists")
	ErrTargetIsSource    = errors.New("groupx: merge target must be a new namespace, not a source")
	ErrUnknownTenant     = errors.New("groupx: merge source tenant does not exist")
	ErrUnauthorizedMerge = errors.New("groupx: missing or mismatched merge authorization token")
)

// IssueMergeToken creates a one-time token authorizing exactly one merge of
// the given source tenants into the given new target namespace. It requires
// the engine's admin key.
func (e *Engine) IssueMergeToken(adminKey string, sources []string, target string) (string, error) {
	if e.adminKey == "" {
		return "", ErrMergeDisabled
	}
	if adminKey != e.adminKey {
		return "", ErrBadAdminKey
	}
	set, err := normalizeSources(sources)
	if err != nil {
		return "", err
	}
	if target == "" || len(set) < 2 {
		return "", ErrInvalidMerge
	}
	if _, ok := set[target]; ok {
		return "", ErrTargetIsSource
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("groupx: token generation: %v", err)
	}
	token := hex.EncodeToString(buf)
	e.mu.Lock()
	e.grants[token] = mergeGrant{sources: set, target: target}
	e.mu.Unlock()
	return token, nil
}

// Merge combines the state of the source tenants into a brand-new target
// namespace. It is the only operation that crosses tenant boundaries, and
// it requires a valid one-time token from IssueMergeToken naming exactly
// these sources and this target. Source tenants are never modified; the
// target must not already exist.
func (e *Engine) Merge(token string, sources []string, target string) error {
	set, err := normalizeSources(sources)
	if err != nil {
		return err
	}
	if target == "" || len(set) < 2 {
		return ErrInvalidMerge
	}
	if _, ok := set[target]; ok {
		return ErrTargetIsSource
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	grant, ok := e.grants[token]
	if !ok || grant.target != target || !sameSet(grant.sources, set) {
		return ErrUnauthorizedMerge
	}
	if _, exists := e.tenants[target]; exists {
		return ErrTargetExists
	}
	for name := range set {
		if _, ok := e.tenants[name]; !ok {
			return ErrUnknownTenant
		}
	}

	merged := newTenantState(e.cacheCap)
	for name := range set {
		src := e.tenants[name]
		for id := range src.seen {
			merged.seen[id] = struct{}{}
		}
		for g, agg := range src.groups {
			m := merged.groups[g]
			m.Count += agg.Count
			m.Sum += agg.Sum
			if agg.LastTs >= m.LastTs {
				m.LastTs = agg.LastTs
				m.LastPayload = agg.LastPayload
			}
			merged.groups[g] = m
		}
		// Populate the new namespace's private cache from source caches.
		// Entries are copied, not shared.
		for id := range src.seen {
			if v, ok := src.cache.get(id); ok {
				merged.cache.put(id, v)
			}
		}
		if src.watermark > merged.watermark {
			merged.watermark = src.watermark
		}
		if src.highSeq > merged.highSeq {
			merged.highSeq = src.highSeq
		}
	}

	e.tenants[target] = merged
	delete(e.grants, token) // one-time use
	return nil
}

func normalizeSources(sources []string) (map[string]struct{}, error) {
	set := make(map[string]struct{}, len(sources))
	for _, s := range sources {
		if s == "" {
			return nil, ErrInvalidMerge
		}
		set[s] = struct{}{}
	}
	return set, nil
}

func sameSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// Tenants returns the sorted list of known tenant namespaces.
func (e *Engine) Tenants() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, 0, len(e.tenants))
	for name := range e.tenants {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
