// Package identity exposes the purl-based package identity component for
// consumers that prefer an explicit identity-oriented import path.
package identity

import (
	netsec "example.org/netsec/q0371"
)

// PackageURL is a parsed and normalized package identity.
type PackageURL = netsec.PackageURL

// PURL is an alias for PackageURL.
type PURL = netsec.PackageURL

// Version is a parsed, comparable version.
type Version = netsec.Version

// Interval is a contiguous version interval.
type Interval = netsec.Interval

// VersionRange is a union of version intervals.
type VersionRange = netsec.VersionRange

// Parse parses a package URL and returns its normalized identity.
func Parse(raw string) (*PackageURL, error) { return netsec.Parse(raw) }

// ParsePURL parses a package URL and returns its normalized identity.
func ParsePURL(raw string) (*PackageURL, error) { return netsec.Parse(raw) }

// ParsePackageURL parses a package URL and returns its normalized identity.
func ParsePackageURL(raw string) (*PackageURL, error) { return netsec.Parse(raw) }

// FromString parses a package URL and returns its normalized identity.
func FromString(raw string) (*PackageURL, error) { return netsec.Parse(raw) }

// New parses a package URL and returns its normalized identity.
func New(raw string) (*PackageURL, error) { return netsec.Parse(raw) }

// MustParse parses a package URL and panics on error.
func MustParse(raw string) *PackageURL { return netsec.MustParse(raw) }

// IdentityOf returns the canonical identity string for raw.
func IdentityOf(raw string) (string, error) {
	p, err := netsec.Parse(raw)
	if err != nil {
		return "", err
	}
	return p.Identity(), nil
}

// EqualPURL reports whether two purls have the same identity.
func EqualPURL(a, b string) (bool, error) { return netsec.EqualPURL(a, b) }

// ParseVersion parses a semantic version.
func ParseVersion(raw string) (Version, error) { return netsec.ParseVersion(raw) }

// CompareVersions compares two versions.
func CompareVersions(a, b string) int { return netsec.CompareVersions(a, b) }

// ParseVersionRange parses a native or VERS version range.
func ParseVersionRange(expr string) (*VersionRange, error) { return netsec.ParseVersionRange(expr) }

// VersionInRange reports whether version is in rangeExpr.
func VersionInRange(version, rangeExpr string) (bool, error) {
	return netsec.VersionInRange(version, rangeExpr)
}

// Satisfies reports whether version satisfies rangeExpr.
func Satisfies(version, rangeExpr string) (bool, error) {
	return netsec.Satisfies(version, rangeExpr)
}

// RegisterTypeAlias registers an ecosystem type alias.
func RegisterTypeAlias(alias, canonical string) error {
	return netsec.RegisterTypeAlias(alias, canonical)
}

// RegisterPackageAlias registers a package-coordinate alias.
func RegisterPackageAlias(aliasPURL, canonicalPURL string) error {
	return netsec.RegisterPackageAlias(aliasPURL, canonicalPURL)
}

// RegisterAlias registers a package-coordinate alias.
func RegisterAlias(aliasPURL, canonicalPURL string) error {
	return netsec.RegisterAlias(aliasPURL, canonicalPURL)
}

// ResetAliases removes registered aliases.
func ResetAliases() { netsec.ResetAliases() }
