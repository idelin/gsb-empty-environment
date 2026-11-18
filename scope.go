package netsec

import (
	"fmt"
	"strings"
)

// registryScope is a compiled Notation-style registry scope.
type registryScope struct {
	raw        string
	global     bool
	wildcard   bool
	registry   string
	repository string
}

func parseRegistryScope(raw string) (registryScope, error) {
	if raw == "" {
		return registryScope{}, fmt.Errorf("%w: empty scope", ErrInvalidScope)
	}
	if raw != strings.TrimSpace(raw) {
		return registryScope{}, fmt.Errorf("%w: surrounding whitespace in %q", ErrInvalidScope, raw)
	}
	if strings.Contains(raw, "://") {
		return registryScope{}, fmt.Errorf("%w: schemes are not allowed in %q", ErrInvalidScope, raw)
	}
	if raw == "*" {
		return registryScope{raw: raw, global: true}, nil
	}

	scope := raw
	wildcard := false
	if strings.HasSuffix(scope, "/*") {
		wildcard = true
		scope = scope[:len(scope)-2]
	}
	if scope == "" {
		return registryScope{}, fmt.Errorf("%w: empty scope", ErrInvalidScope)
	}
	if strings.Contains(scope, "*") {
		return registryScope{}, fmt.Errorf("%w: wildcard is only allowed as a trailing /* in %q", ErrInvalidScope, raw)
	}
	if strings.HasPrefix(scope, "/") || strings.HasSuffix(scope, "/") {
		return registryScope{}, fmt.Errorf("%w: empty path component in %q", ErrInvalidScope, raw)
	}

	slash := strings.IndexByte(scope, '/')
	if slash < 0 {
		if !wildcard {
			return registryScope{}, fmt.Errorf("%w: expected registry/repository in %q", ErrInvalidScope, raw)
		}
		registry, err := normalizeRegistry(scope)
		if err != nil {
			return registryScope{}, fmt.Errorf("%w: %v", ErrInvalidScope, err)
		}
		return registryScope{
			raw:      raw,
			wildcard: true,
			registry: registry,
		}, nil
	}
	if slash == 0 || slash == len(scope)-1 {
		return registryScope{}, fmt.Errorf("%w: empty registry or repository in %q", ErrInvalidScope, raw)
	}

	registry, err := normalizeRegistry(scope[:slash])
	if err != nil {
		return registryScope{}, fmt.Errorf("%w: %v", ErrInvalidScope, err)
	}
	repository := scope[slash+1:]
	if err := validateRepository(repository); err != nil {
		return registryScope{}, fmt.Errorf("%w: %v", ErrInvalidScope, err)
	}

	return registryScope{
		raw:        raw,
		wildcard:   wildcard,
		registry:   registry,
		repository: repository,
	}, nil
}

func (s registryScope) match(ref ImageReference) bool {
	if s.global {
		return true
	}
	if !strings.EqualFold(s.registry, ref.Registry) {
		return false
	}
	if !s.wildcard {
		return s.repository == ref.Repository
	}
	if s.repository == "" {
		return ref.Repository != ""
	}
	// Requiring the separator prevents example.com/prod/* from matching
	// example.com/prod-evil/app.
	return strings.HasPrefix(ref.Repository, s.repository+"/")
}

// ValidateScope validates a Notation-style registry scope. The only accepted
// forms are "*" (global), "registry/repository" and "registry/repository/*".
// "registry/*" is the registry-wide form. A trailing "/*" matches only
// descendants of repository, never repository itself and never a repository
// that merely shares the same string prefix.
func ValidateScope(scope string) error {
	_, err := parseRegistryScope(scope)
	return err
}

// MatchScope reports whether a single registry scope matches an image
// reference. Tag and digest components are ignored for scope matching.
func MatchScope(scope, image string) (bool, error) {
	compiled, err := parseRegistryScope(scope)
	if err != nil {
		return false, err
	}
	ref, err := ParseImageReference(image)
	if err != nil {
		return false, err
	}
	return compiled.match(ref), nil
}
