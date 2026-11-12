package netsec

import (
	"fmt"
	"strings"
	"unicode"
)

// PackageKey identifies a package version under a canonical, case-folded key.
// Scope and Name are always stored in ASCII lower case.
type PackageKey struct {
	Scope   string
	Name    string
	Version string
}

// NewPackageKey validates scope and name and returns a canonical package key.
// Scope and name are ASCII case-insensitive and may contain only ASCII
// letters, digits, and hyphens.
func NewPackageKey(scope, name, version string) (PackageKey, error) {
	if err := validateSegment("scope", scope); err != nil {
		return PackageKey{}, err
	}
	if err := validateSegment("name", name); err != nil {
		return PackageKey{}, err
	}
	if err := validateVersion(version); err != nil {
		return PackageKey{}, err
	}
	return PackageKey{
		Scope:   asciiLower(scope),
		Name:    asciiLower(name),
		Version: version,
	}, nil
}

// ParseKey parses "scope.name@version" and returns its canonical form.
// Scope and name are case-folded; the version is preserved verbatim.
func ParseKey(key string) (PackageKey, error) {
	if key == "" {
		return PackageKey{}, fmt.Errorf("%w: empty key", ErrInvalidKey)
	}

	at := strings.IndexByte(key, '@')
	if at < 0 {
		return PackageKey{}, fmt.Errorf("%w: %q is missing '@version'", ErrInvalidKey, key)
	}

	packagePart, version := key[:at], key[at+1:]
	dot := strings.IndexByte(packagePart, '.')
	if dot < 0 {
		return PackageKey{}, fmt.Errorf("%w: %q is missing 'scope.name'", ErrInvalidKey, key)
	}

	return NewPackageKey(packagePart[:dot], packagePart[dot+1:], version)
}

// String returns the canonical "scope.name@version" key.
func (k PackageKey) String() string {
	return k.Scope + "." + k.Name + "@" + k.Version
}

// Artifact is an immutable package release presented for verification.
// An empty Signer means the artifact is unsigned.
type Artifact struct {
	Scope    string
	Name     string
	Version  string
	Checksum string
	Signer   string
}

// Key returns the canonical key for the artifact.
func (a Artifact) Key() (PackageKey, error) {
	return NewPackageKey(a.Scope, a.Name, a.Version)
}

func validateSegment(kind, value string) error {
	if value == "" {
		return fmt.Errorf("%w: empty %s", ErrInvalidKey, kind)
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' {
			continue
		}
		return fmt.Errorf(
			"%w: %s %q must contain only ASCII letters, digits, or '-'",
			ErrInvalidKey, kind, value,
		)
	}
	return nil
}

func validateVersion(version string) error {
	if version == "" {
		return fmt.Errorf("%w: empty version", ErrInvalidKey)
	}
	for _, r := range version {
		if r == '@' || unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("%w: invalid version %q", ErrInvalidKey, version)
		}
	}
	return nil
}

func asciiLower(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}
