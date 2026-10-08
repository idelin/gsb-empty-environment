// Package snapaudit compares two structured snapshots (JSON-like
// trees of maps, slices and scalars) against an audit policy and
// reports policy violations, shallowest path first.
//
// Paths are dot-separated segments, e.g. "spec.containers.0.image".
// In policy patterns the wildcard "*" matches exactly one segment
// (a single level); there is no multi-level wildcard.
package snapaudit

import "strings"

// Policy declares the audit rules applied to a snapshot diff.
//
//   - Immutable: paths whose value must be identical in both
//     snapshots. Any modification, addition or removal at or below
//     an immutable path is a violation, reported at the immutable
//     path itself (the shallowest offending path).
//   - AllowAdd: paths where new keys/indices may appear.
//   - AllowDelete: paths where existing keys/indices may disappear.
//
// Additions and removals of whole subtrees are expanded recursively:
// if no rule matches the subtree root, each descendant is checked in
// turn until a rule matches or a leaf is reached (per-leaf
// expansion).
type Policy struct {
	Immutable   []string
	AllowAdd    []string
	AllowDelete []string
}

// Wildcard is the path segment that matches any single level.
const Wildcard = "*"

// splitPath splits a dot-separated path into segments. The empty
// path (the document root) yields zero segments.
func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	return strings.Split(path, ".")
}

// MatchPath reports whether a policy pattern matches a concrete
// path. The wildcard "*" matches exactly one segment; every other
// segment must match literally, and pattern and path must have the
// same number of segments.
func MatchPath(pattern, path string) bool {
	return matchSegments(splitPath(pattern), splitPath(path))
}

func matchSegments(pattern, path []string) bool {
	if len(pattern) != len(path) {
		return false
	}
	for i, seg := range pattern {
		if seg == Wildcard {
			continue
		}
		if seg != path[i] {
			return false
		}
	}
	return true
}

// matchAny reports whether any pattern matches the given path
// segments.
func matchAny(patterns []string, path []string) bool {
	for _, p := range patterns {
		if matchSegments(splitPath(p), path) {
			return true
		}
	}
	return false
}
