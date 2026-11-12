// Package trustpolicy implements Notation-style trust policy scope
// selection and verification-level enforcement for an image admission
// component.
package trustpolicy

import (
	"errors"
	"fmt"
	"strings"
)

// GlobalScope matches every repository.
const GlobalScope = "*"

// wildcardSuffix is the only permitted wildcard form: a trailing "/*".
const wildcardSuffix = "/*"

// ErrNoMatchingPolicy is returned when no policy scope applies to an image.
var ErrNoMatchingPolicy = errors.New("trustpolicy: no policy matches image")

// OverlapError reports that more than one specific scope matched an image,
// which is a configuration error: the admission component cannot tell which
// policy should govern the image.
type OverlapError struct {
	Repository string
	Policies   []string
}

func (e *OverlapError) Error() string {
	return fmt.Sprintf("trustpolicy: repository %q matched by multiple specific scopes from policies %v", e.Repository, e.Policies)
}

// ValidateScope reports whether scope is a legal registry scope.
//
// A scope is either the global "*", or has the form "registry/repo" with an
// optional trailing "/*" wildcard. Wildcards anywhere else (e.g.
// "registry/*\/repo" or "reg*istry/repo") are rejected so a scope can never
// silently match more than its literal path prefix.
func ValidateScope(scope string) error {
	if scope == GlobalScope {
		return nil
	}
	if scope == "" {
		return errors.New("trustpolicy: empty registry scope")
	}
	wildcard := strings.Contains(scope, "*")
	if wildcard {
		if strings.Count(scope, "*") != 1 || !strings.HasSuffix(scope, wildcardSuffix) {
			return fmt.Errorf("trustpolicy: scope %q: wildcard is only allowed as a trailing %q", scope, wildcardSuffix)
		}
	}
	base := strings.TrimSuffix(scope, wildcardSuffix)
	segments := strings.Split(base, "/")
	// An exact scope needs registry/repo; a wildcard scope may stop at the
	// registry ("example.com/*") to cover every repository it hosts.
	if !wildcard && len(segments) < 2 {
		return fmt.Errorf("trustpolicy: scope %q: must have the form registry/repo", scope)
	}
	for _, seg := range segments {
		if seg == "" {
			return fmt.Errorf("trustpolicy: scope %q: empty path segment", scope)
		}
	}
	return nil
}

// RepositoryOf extracts the "registry/repo" repository path from an image
// reference, stripping any ":tag" or "@digest" suffix. Matching is always
// performed against the repository, never against a tag.
func RepositoryOf(imageRef string) (string, error) {
	ref := imageRef
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	// A tag separator only counts after the last path separator, so a port
	// in the registry host (registry:5000/repo) is not mistaken for a tag.
	if i := strings.LastIndex(ref, ":"); i >= 0 && i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	if ref == "" || !strings.Contains(ref, "/") {
		return "", fmt.Errorf("trustpolicy: cannot derive registry/repo from image reference %q", imageRef)
	}
	return ref, nil
}

// scopeMatches reports whether repo (a "registry/repo" path) is covered by
// scope. A trailing "/*" wildcard matches on a path-segment boundary only:
// "example.com/prod/*" matches "example.com/prod/app" but never
// "example.com/prod-evil/app".
func scopeMatches(scope, repo string) bool {
	if scope == GlobalScope {
		return true
	}
	if strings.HasSuffix(scope, wildcardSuffix) {
		prefix := strings.TrimSuffix(scope, "*") // keeps the trailing "/"
		return strings.HasPrefix(repo, prefix)
	}
	return scope == repo
}
