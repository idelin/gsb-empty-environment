package depseal

import "fmt"

// CrossSourceError reports that a fetch attempted to leave the locked
// source's trusted host set, for example a private registry redirecting to a
// public registry hosting a same-named package.
type CrossSourceError struct {
	SourceID     string
	AllowedHosts []string
	FromHost     string
	ToHost       string
}

func (e *CrossSourceError) Error() string {
	return fmt.Sprintf("depseal: cross-source redirect blocked for source %s: %q -> %q is outside allowed hosts %v",
		e.SourceID, e.FromHost, e.ToHost, e.AllowedHosts)
}

// DigestMismatchError reports that downloaded content did not match the
// locked digest.
type DigestMismatchError struct {
	Wanted Digest
	Got    Digest
}

func (e *DigestMismatchError) Error() string {
	return fmt.Sprintf("depseal: digest mismatch: wanted %s, got %s", e.Wanted, e.Got)
}

// TenantDeniedError reports that a tenant is not authorized to use a source.
type TenantDeniedError struct {
	Tenant   string
	SourceID string
}

func (e *TenantDeniedError) Error() string {
	return fmt.Sprintf("depseal: tenant %q is not authorized to use source %q", e.Tenant, e.SourceID)
}

// FetchError wraps a fetch failure with the package coordinate and its
// parent provenance.
type FetchError struct {
	Package Package
	Parent  string
	Err     error
}

func (e *FetchError) Error() string {
	if e.Parent == "" {
		return fmt.Sprintf("depseal: fetch %s: %v", e.Package.Identity(), e.Err)
	}
	return fmt.Sprintf("depseal: fetch %s (parent %s): %v", e.Package.Identity(), e.Parent, e.Err)
}

func (e *FetchError) Unwrap() error { return e.Err }

// HTTPStatusError is a non-2xx response from a source.
type HTTPStatusError struct {
	SourceID string
	Status   int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("depseal: unexpected HTTP status %d from source %s", e.Status, e.SourceID)
}
