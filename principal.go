package krb5policy

import (
	"fmt"
	"strings"
	"unicode"
)

// Principal is a Kerberos principal.  Realm is stored separately so that
// callers do not have to round-trip through string escaping when they
// already have parsed ASN.1 name components.
type Principal struct {
	Name  []string
	Realm string
}

// NewPrincipal builds a principal from its realm and name components.
func NewPrincipal(realm string, name ...string) Principal {
	return Principal{Name: append([]string(nil), name...), Realm: realm}
}

// ParsePrincipal parses the conventional "component/...@REALM" form.
// Backslash escapes are recognized for '/', '@', and '\'.
func ParsePrincipal(s string) (Principal, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Principal{}, fmt.Errorf("%w: empty principal", ErrInvalidRealm)
	}
	at := -1
	escaped := false
	for i := 0; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		if s[i] == '\\' {
			escaped = true
			continue
		}
		if s[i] == '@' {
			at = i
		}
	}
	var namePart, realm string
	if at >= 0 {
		namePart = s[:at]
		realm = s[at+1:]
		if realm == "" {
			return Principal{}, fmt.Errorf("%w: empty realm in %q", ErrInvalidRealm, s)
		}
	} else {
		namePart = s
	}
	if namePart == "" {
		return Principal{}, fmt.Errorf("%w: empty name in %q", ErrInvalidRealm, s)
	}
	parts, err := splitEscaped(namePart, '/')
	if err != nil {
		return Principal{}, err
	}
	if len(parts) == 0 {
		return Principal{}, fmt.Errorf("%w: empty name in %q", ErrInvalidRealm, s)
	}
	for _, p := range parts {
		if p == "" {
			return Principal{}, fmt.Errorf("%w: empty principal component in %q", ErrInvalidRealm, s)
		}
	}
	if realm != "" {
		if err := validateRealm(realm); err != nil {
			return Principal{}, err
		}
	}
	return Principal{Name: parts, Realm: realm}, nil
}

func splitEscaped(s string, sep byte) ([]string, error) {
	var out []string
	var b strings.Builder
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			if c != '\\' && c != '/' && c != '@' {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == sep {
			out = append(out, b.String())
			b.Reset()
			continue
		}
		b.WriteByte(c)
	}
	if escaped {
		return nil, fmt.Errorf("%w: dangling escape in %q", ErrInvalidRealm, s)
	}
	out = append(out, b.String())
	return out, nil
}

// String renders the conventional principal form.
func (p Principal) String() string {
	parts := make([]string, len(p.Name))
	for i, part := range p.Name {
		parts[i] = escapePrincipalComponent(part)
	}
	name := strings.Join(parts, "/")
	if p.Realm == "" {
		return name
	}
	return name + "@" + p.Realm
}

func escapePrincipalComponent(part string) string {
	var b strings.Builder
	for _, r := range part {
		switch r {
		case '\\', '/', '@':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Canonical returns a comparison-only form: realm is upper-cased and name
// components are lower-cased.  Host policy in this package treats realms
// and directory account names as case-insensitive, which matches DNS-style
// realms and common directory deployments.  Original values are retained in
// Principal.Name and Principal.Realm for signing and diagnostics.
func (p Principal) Canonical() string {
	name := make([]string, len(p.Name))
	for i, part := range p.Name {
		name[i] = strings.ToLower(part)
	}
	return strings.Join(name, "/") + "@" + canonicalRealm(p.Realm)
}

// Equal compares principals using the package's directory comparison rules.
func (p Principal) Equal(other Principal) bool {
	return p.Canonical() == other.Canonical()
}

// IsZero reports whether both the realm and all name components are empty.
func (p Principal) IsZero() bool {
	if p.Realm != "" {
		return false
	}
	for _, part := range p.Name {
		if part != "" {
			return false
		}
	}
	return true
}

// IsTGT reports whether the principal uses the reserved krbtgt service.
func (p Principal) IsTGT() bool {
	return len(p.Name) >= 2 && strings.EqualFold(p.Name[0], "krbtgt")
}

// TargetRealm returns the realm named by a krbtgt/DEST@ISSUER principal.
// For non-TGT principals it returns the principal's own realm.
func (p Principal) TargetRealm() string {
	if p.IsTGT() {
		return p.Name[1]
	}
	return p.Realm
}

func canonicalRealm(realm string) string {
	return strings.ToUpper(strings.TrimSpace(realm))
}

func sameRealm(a, b string) bool {
	return canonicalRealm(a) == canonicalRealm(b)
}

func validateRealm(realm string) error {
	if realm == "" {
		return fmt.Errorf("%w: empty realm", ErrInvalidRealm)
	}
	if len(realm) > 4096 {
		return fmt.Errorf("%w: realm too long", ErrInvalidRealm)
	}
	for _, r := range realm {
		if r == 0 || unicode.IsControl(r) {
			return fmt.Errorf("%w: control character in realm", ErrInvalidRealm)
		}
	}
	return nil
}

func canonicalPrincipalString(s string) string {
	p, err := ParsePrincipal(s)
	if err != nil {
		return strings.ToUpper(strings.TrimSpace(s))
	}
	return p.Canonical()
}
