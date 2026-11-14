// Package purl exposes the package-identity implementation under a
// conventional import name.
package purl

import (
	netsec "example.org/netsec/q0371"
)

// PackageURL is a parsed and normalized Package URL.
type PackageURL = netsec.PackageURL

// PURL is an alias for PackageURL.
type PURL = netsec.PackageURL

// Version is a parsed, comparable version.
type Version = netsec.Version

// Bound is one end of a version interval.
type Bound = netsec.Bound

// Interval is a contiguous version interval.
type Interval = netsec.Interval

// VersionRange is a union of version intervals.
type VersionRange = netsec.VersionRange

// Parse parses and normalizes a purl.
func Parse(raw string) (*PackageURL, error) { return netsec.Parse(raw) }

// ParsePURL parses and normalizes a purl.
func ParsePURL(raw string) (*PackageURL, error) { return netsec.Parse(raw) }

// ParsePackageURL parses and normalizes a purl.
func ParsePackageURL(raw string) (*PackageURL, error) { return netsec.Parse(raw) }

// FromString parses and normalizes a purl.
func FromString(raw string) (*PackageURL, error) { return netsec.Parse(raw) }

// New parses and normalizes a purl.
func New(raw string) (*PackageURL, error) { return netsec.Parse(raw) }

// MustParse parses a purl and panics on error.
func MustParse(raw string) *PackageURL { return netsec.MustParse(raw) }

// Normalize returns the canonical spelling of a purl.
func Normalize(raw string) (string, error) { return netsec.Normalize(raw) }

// Canonicalize returns the canonical spelling of a purl.
func Canonicalize(raw string) (string, error) { return netsec.Canonicalize(raw) }

// IdentityOf returns the canonical identity string for raw.
func IdentityOf(raw string) (string, error) { return netsec.IdentityOf(raw) }

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

// EqualPURL reports whether two purls have the same identity.
func EqualPURL(a, b string) (bool, error) { return netsec.EqualPURL(a, b) }

// MatchesPURL reports whether two purls refer to the same package coordinate
// with overlapping versions.
func MatchesPURL(a, b string) (bool, error) { return netsec.MatchesPURL(a, b) }

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
