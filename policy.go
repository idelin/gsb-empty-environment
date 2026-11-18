package netsec

import (
	"path/filepath"
	"strings"
)

// Policy describes when a signature is mandatory and which signing keys are
// trusted.
type Policy struct {
	// TrustedKeyIDs is the set of key identifiers accepted on a signature
	// attribute. A nil or empty map trusts no keys.
	TrustedKeyIDs map[uint32]bool

	// RequireSignature reports whether path must carry a signature
	// attribute. Digest-only attributes are rejected for such paths.
	//
	// A nil function means that no path requires a signature. Callers that
	// protect all paths can use AllPaths.
	RequireSignature func(path string) bool
}

// RequiresSignature applies the policy to path.
func (p Policy) RequiresSignature(path string) bool {
	return p.RequireSignature != nil && p.RequireSignature(path)
}

// TrustsKeyID reports whether keyID is in the policy's trusted set.
func (p Policy) TrustsKeyID(keyID uint32) bool {
	return p.TrustedKeyIDs != nil && p.TrustedKeyIDs[keyID]
}

// NewPolicy builds a policy from a list of trusted key identifiers and a
// signature-requirement predicate.
func NewPolicy(trustedKeyIDs []uint32, requireSignature func(path string) bool) Policy {
	trusted := make(map[uint32]bool, len(trustedKeyIDs))
	for _, keyID := range trustedKeyIDs {
		trusted[keyID] = true
	}
	return Policy{
		TrustedKeyIDs:    trusted,
		RequireSignature: requireSignature,
	}
}

// TrustedKeys returns a trusted-key set containing ids.
func TrustedKeys(ids ...uint32) map[uint32]bool {
	trusted := make(map[uint32]bool, len(ids))
	for _, id := range ids {
		trusted[id] = true
	}
	return trusted
}

// AllPaths always requires a signature.
func AllPaths(string) bool { return true }

// NoPaths never requires a signature.
func NoPaths(string) bool { return false }

// PathMatcher returns a signature-requirement predicate for patterns.
//
// A pattern may be a literal path, a filepath.Match-style glob, or a directory
// prefix. Directory prefixes should end in a path separator (for example
// "/usr/bin/"). Globs are matched against both the supplied path and its
// absolute form; globs without a separator are also matched against the base
// name.
func PathMatcher(patterns ...string) func(path string) bool {
	cleaned := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		directoryPrefix := strings.HasSuffix(pattern, string(filepath.Separator))
		pattern = filepath.Clean(pattern)
		if directoryPrefix && !strings.HasSuffix(pattern, string(filepath.Separator)) {
			pattern += string(filepath.Separator)
		}
		cleaned = append(cleaned, pattern)
	}

	return func(path string) bool {
		if len(cleaned) == 0 {
			return false
		}

		absolute, err := filepath.Abs(path)
		if err != nil {
			absolute = filepath.Clean(path)
		}
		base := filepath.Base(path)

		for _, pattern := range cleaned {
			if pattern == string(filepath.Separator) {
				return true
			}
			if pattern == path || pattern == absolute {
				return true
			}
			if strings.HasSuffix(pattern, string(filepath.Separator)) &&
				(strings.HasPrefix(path, pattern) || strings.HasPrefix(absolute, pattern)) {
				return true
			}
			if matchPath(pattern, path) || matchPath(pattern, absolute) {
				return true
			}
			if !strings.ContainsRune(pattern, filepath.Separator) &&
				matchPath(pattern, base) {
				return true
			}
		}
		return false
	}
}

func matchPath(pattern, name string) bool {
	matched, err := filepath.Match(pattern, name)
	return err == nil && matched
}
