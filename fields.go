package netsec

import (
	"errors"
	"strings"
)

// securityPaths is the immutable registry of every field that carries an
// approved security, identity or image-digest binding.
var securityPaths = map[string]struct{}{
	"/spec/image":                             {},
	"/spec/identity":                          {},
	"/spec/identity/serviceAccountName":       {},
	"/spec/identity/groupNames":               {},
	"/spec/security":                          {},
	"/spec/security/runAsNonRoot":             {},
	"/spec/security/readOnlyRootFS":           {},
	"/spec/security/allowPrivilegeEscalation": {},
	"/spec/security/privileged":               {},
	"/spec/capabilities":                      {},
	"/spec/serviceAccount":                    {},
}

// knownNonSecurityPaths are registered non-security fields. Anything that is
// neither a security path nor a known path is unregistered and denied by
// default (no implicit trust).
var knownNonSecurityPaths = map[string]struct{}{
	"/spec/replicas":   {},
	"/spec/env":        {},
	"/spec/labels":     {},
	"/metadata/labels": {},
	"/metadata/name":   {},
}

// mutablePrefixes cover map/set-style fields whose individual entries are
// addressable through JSON Pointer child paths; child paths inherit the
// mutability of the parent.
var mutablePrefixes = map[string]struct{}{
	"/spec/labels/":     {},
	"/metadata/labels/": {},
}

// fieldRegistry classifies a JSON Pointer path for one approval.
type fieldRegistry struct {
	mutable map[string]struct{}
}

func newFieldRegistry(mutable []string) (*fieldRegistry, error) {
	r := &fieldRegistry{mutable: map[string]struct{}{}}
	for _, path := range mutable {
		if err := validatePointer(path); err != nil {
			return nil, err
		}
		if isSecurityPath(path) {
			return nil, errors.Join(errMutableDenied, errors.New(path))
		}
		if !isKnownNonSecurity(path) && !isMutablePrefixPath(path) {
			return nil, errors.Join(errMutableUnknown, errors.New(path))
		}
		r.mutable[path] = struct{}{}
	}
	return r, nil
}

// classify returns "security", "mutable", "immutable" or "unregistered".
func (r *fieldRegistry) classify(path string) string {
	if isSecurityPath(path) {
		return classSecurity
	}
	if r.isMutable(path) {
		return classMutable
	}
	if isKnownNonSecurity(path) || isMutablePrefixPath(path) {
		return "immutable"
	}
	if parent := registeredParent(path); parent != "" {
		switch r.classify(parent) {
		case classSecurity:
			return classSecurity
		case classMutable:
			return classMutable
		default:
			return "immutable"
		}
	}
	return classUnregistered
}

// registeredParent walks up a JSON Pointer path until it reaches a registered
// field, so array entries and object children inherit the parent's class.
func registeredParent(path string) string {
	for {
		last := strings.LastIndex(path, "/")
		if last <= 0 {
			return ""
		}
		path = path[:last]
		if isSecurityPath(path) || isKnownNonSecurity(path) || isMutablePrefixPath(path) {
			return path
		}
	}
}

func (r *fieldRegistry) isMutable(path string) bool {
	if _, ok := r.mutable[path]; ok {
		return true
	}
	for prefix := range mutablePrefixes {
		if strings.HasPrefix(path, prefix) {
			if _, ok := r.mutable[strings.TrimSuffix(prefix, "/")]; ok {
				return true
			}
		}
	}
	return false
}

func isSecurityPath(path string) bool {
	if _, ok := securityPaths[path]; ok {
		return true
	}
	// Any child of a frozen object (identity/security) is frozen.
	for _, prefix := range []string{"/spec/identity/", "/spec/security/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func isKnownNonSecurity(path string) bool {
	_, ok := knownNonSecurityPaths[path]
	return ok
}

func isMutablePrefixPath(path string) bool {
	for prefix := range mutablePrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
