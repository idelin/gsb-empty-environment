package netsec

import (
	"encoding/hex"
	"errors"
	"sort"
	"strings"
)

// Sentinel errors returned by Resolve. Callers should use errors.Is rather than
// matching message text. Every one of them fails closed: a Go install host
// must treat them as "do not fetch, and do not fall back to a public proxy".
var (
	// ErrNoRule means no approved rule covers the requested old source,
	// package and version.
	ErrNoRule = errors.New("netsec: no migration rule matches the request")

	// ErrRevoked means the requested version is explicitly revoked by the
	// snapshot and must never be installed.
	ErrRevoked = errors.New("netsec: requested version is revoked")

	// ErrUnknownVersion means the requested version is not enumerated in the
	// finite approved version domain. It is never mapped implicitly.
	ErrUnknownVersion = errors.New("netsec: version is not part of the approved version domain")

	// ErrUnknownModule means the requested module is not enumerated in the
	// finite approved version domain.
	ErrUnknownModule = errors.New("netsec: module is not part of the approved version domain")
)

// SourceIdentity identifies the legacy private source a request originates
// from. A rule only applies when both the hostname and, when required by the
// deployment, the instance identifier match. This binding is what prevents an
// approval for a private host from being reused for a same-named public
// package: a public request never carries the private source identity.
type SourceIdentity struct {
	// Host is the legacy module source hostname, compared case-insensitively
	// with surrounding ASCII whitespace and a trailing dot removed.
	Host string

	// InstanceID is an optional deployment-specific identifier (for example a
	// VCS endpoint ID). An empty string matches only rules whose InstanceID is
	// also empty.
	InstanceID string
}

func (s SourceIdentity) canonical() SourceIdentity {
	h := strings.ToLower(strings.TrimSpace(s.Host))
	h = strings.TrimSuffix(h, ".")
	return SourceIdentity{Host: h, InstanceID: strings.TrimSpace(s.InstanceID)}
}

func (s SourceIdentity) equal(other SourceIdentity) bool {
	a, b := s.canonical(), other.canonical()
	return a.Host == b.Host && a.InstanceID == b.InstanceID
}

// PackageScope selects module paths using the Go path-prefix rule: Prefix is
// either the exact module path or a path segment ending in "/". For example
// "git.example.net/team" matches only that exact path, while
// "git.example.net/team/" matches "git.example.net/team/lib" but never
// "git.example.net/team-evil".
type PackageScope struct {
	Prefix string
}

func (p PackageScope) canonical() PackageScope {
	return PackageScope{Prefix: strings.TrimSpace(p.Prefix)}
}

// contains reports whether the exact module path path is inside the scope.
func (p PackageScope) contains(path string) bool {
	base := p.canonical().Prefix
	if base == "" || path == "" {
		return false
	}
	if strings.HasSuffix(base, "/") {
		return strings.HasPrefix(path, base)
	}
	return path == base
}

// Fingerprint is the pinned certificate fingerprint of the new private source,
// stored as raw bytes. The zero value is rejected for active rules, so an
// approval can never silently skip certificate pinning.
type Fingerprint []byte

// ParseFingerprint decodes a SHA-256 fingerprint written as hexadecimal,
// optionally with colon separators (OpenSSL style). Both uppercase and
// lowercase hex are accepted; exactly 32 bytes must result.
func ParseFingerprint(s string) (Fingerprint, error) {
	clean := strings.ReplaceAll(strings.TrimSpace(s), ":", "")
	raw, err := hex.DecodeString(clean)
	if err != nil {
		return nil, errors.New("netsec: certificate fingerprint must be hexadecimal")
	}
	if len(raw) != 32 {
		return nil, errors.New("netsec: certificate fingerprint must be a 32-byte SHA-256 value")
	}
	return Fingerprint(raw), nil
}

func (f Fingerprint) equal(other Fingerprint) bool {
	if len(f) != len(other) {
		return false
	}
	for i := range f {
		if f[i] != other[i] {
			return false
		}
	}
	return true
}

// Target is the only place an approved request may be redirected to: the new
// private host together with its pinned certificate fingerprint. There is no
// rewrite to a public path and no way to express "any certificate".
type Target struct {
	NewHost     string
	Fingerprint Fingerprint
}

func (t Target) canonical() Target {
	h := strings.ToLower(strings.TrimSpace(t.NewHost))
	h = strings.TrimSuffix(h, ".")
	return Target{NewHost: h, Fingerprint: Fingerprint(append([]byte(nil), t.Fingerprint...))}
}

func (t Target) equal(other Target) bool {
	a, b := t.canonical(), other.canonical()
	return a.NewHost == b.NewHost && a.Fingerprint.equal(b.Fingerprint)
}

// Rule is one entry of the migration policy. It binds four things together:
// the legacy source identity, the package-name scope, a version interval, and
// either the new source target or an explicit revocation.
type Rule struct {
	// ID is a human-readable identifier used in conflict reports. When empty,
	// Compile assigns "rule#<index>".
	ID string

	// OldSource must match the request's source identity exactly.
	OldSource SourceIdentity

	// Scope limits which module paths this rule covers.
	Scope PackageScope

	// Versions is the approved interval within the scope.
	Versions VersionRange

	// Target is the destination for approved versions. It must be set when
	// Revoked is false and must be empty when Revoked is true.
	Target Target

	// Revoked marks an interval as explicitly denied, even if another rule
	// would approve it. A revocation overlapping an approval is reported as a
	// conflict at compile time rather than resolved by rule ordering.
	Revoked bool
}

// VersionDomain enumerates the finite set of module paths and concrete
// versions against which rule boundaries, revocations and overlap consistency
// are checked at compile time. Nothing outside this domain is ever approved.
type VersionDomain struct {
	modules map[string][]SemVer
}

// NewVersionDomain creates an empty domain.
func NewVersionDomain() *VersionDomain {
	return &VersionDomain{modules: map[string][]SemVer{}}
}

// Add registers concrete versions for a module path. Duplicate versions are
// ignored. Add never sorts the caller's slice.
func (d *VersionDomain) Add(modulePath string, versions ...string) error {
	if d == nil || d.modules == nil {
		return errors.New("netsec: use NewVersionDomain to construct a version domain")
	}
	modulePath = strings.TrimSpace(modulePath)
	if modulePath == "" {
		return errors.New("netsec: module path must not be empty")
	}
	existing := map[string]bool{}
	for _, v := range d.modules[modulePath] {
		existing[v.String()] = true
	}
	for _, raw := range versions {
		v, err := ParseSemVer(raw)
		if err != nil {
			return err
		}
		key := v.String()
		if existing[key] {
			continue
		}
		existing[key] = true
		d.modules[modulePath] = append(d.modules[modulePath], v)
	}
	return nil
}

func (d *VersionDomain) sortedCopy() map[string][]SemVer {
	out := make(map[string][]SemVer, len(d.modules))
	for path, vs := range d.modules {
		cp := append([]SemVer(nil), vs...)
		sort.Slice(cp, func(i, j int) bool { return cp[i].Compare(cp[j]) < 0 })
		out[path] = cp
	}
	return out
}
