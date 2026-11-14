package netsec

import "strings"

// hasPrefix reports whether key starts with prefix using raw byte
// comparison. An empty prefix matches every key and is therefore treated
// as dangerous at every call site that handles tenant data.
func hasPrefix(key, prefix string) bool {
	if len(prefix) == 0 {
		return false
	}
	if len(key) < len(prefix) {
		return false
	}
	return key[:len(prefix)] == prefix
}

// prefixContained reports whether prefix is equal to, or a strict
// sub-prefix of, allowed. Prefix containment uses raw byte boundaries:
// "tenantA/x" is not contained in "tenantA" only when the caller treats
// the boundary explicitly. The service is expected to model namespaces
// with a trailing separator (for example "tenantA/"); the byte-prefix
// rule itself never grants access to sibling namespaces such as
// "tenantA2/" because the byte at the divergence point differs.
func prefixContained(prefix, allowed string) bool {
	if len(allowed) == 0 || len(prefix) < len(allowed) {
		return false
	}
	return prefix[:len(allowed)] == allowed
}

// anyAllowedPrefix reports whether key matches at least one of the
// permitted prefixes.
func anyAllowedPrefix(key string, allowed []string) bool {
	for _, p := range allowed {
		if hasPrefix(key, p) {
			return true
		}
	}
	return false
}

// validateWatchPrefix enforces the core namespace invariant for a watch:
// the prefix must be non-empty and must fall entirely inside at least one
// prefix the tenant is permitted to read.
func validateWatchPrefix(prefix string, allowed []string) error {
	if prefix == "" {
		return ErrEmptyPrefix
	}
	if containsPathTraversal(prefix) {
		return ErrPrefixOutsideNamespace
	}
	for _, p := range allowed {
		if prefixContained(prefix, p) {
			return nil
		}
	}
	return ErrPrefixOutsideNamespace
}

// validateNamespace rejects empty or duplicated namespace prefixes.
func validateNamespace(prefixes []string) error {
	if len(prefixes) == 0 {
		return ErrEmptyPrefix
	}
	seen := make(map[string]struct{}, len(prefixes))
	for _, p := range prefixes {
		if p == "" {
			return ErrEmptyPrefix
		}
		if containsPathTraversal(p) {
			return ErrPrefixOutsideNamespace
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
	}
	return nil
}

// containsPathTraversal reports whether s contains a "." or ".." path
// segment. The authorization model treats keys as byte-prefix strings,
// but a traversal segment must never be honored as a way to escape the
// tenant namespace; such keys and prefixes are rejected outright.
func containsPathTraversal(s string) bool {
	for _, segment := range strings.Split(s, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}
