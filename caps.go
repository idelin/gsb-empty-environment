package netsec

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// ErrInvalidCaps is returned when a capability string, or a programmatically
// constructed capability, does not satisfy the grammar.
var ErrInvalidCaps = errors.New("netsec: invalid caps")

// ErrSyntax is an alias for ErrInvalidCaps kept for callers that distinguish
// parsing errors from policy errors.
var ErrSyntax = ErrInvalidCaps

// ParseError describes which comma-separated rule was rejected. Parsing is
// all-or-nothing: no partially parsed Caps value is returned.
type ParseError struct {
	// Rule is the one-based position of the offending rule, or zero when the
	// error applies to the input as a whole.
	Rule int
	// Reason is a human-readable description of the error.
	Reason string
}

func (e *ParseError) Error() string {
	if e.Rule > 0 {
		return fmt.Sprintf("%s: rule %d: %s", ErrInvalidCaps, e.Rule, e.Reason)
	}
	return fmt.Sprintf("%s: %s", ErrInvalidCaps, e.Reason)
}

func (e *ParseError) Unwrap() error { return ErrInvalidCaps }

// Permission is a bit set of capability rights.
type Permission uint8

const (
	// PermNone grants nothing and is not a valid parsed permission.
	PermNone Permission = 0

	// PermRead is the r right.
	PermRead Permission = 1 << 0
	// PermWrite is the w right.
	PermWrite Permission = 1 << 1
	// PermExecute is the x right.
	PermExecute Permission = 1 << 2

	// PermAll is equivalent to the * permission.
	PermAll = PermRead | PermWrite | PermExecute
)

// Has reports whether p contains every bit in required. A zero requirement is
// never granted.
func (p Permission) Has(required Permission) bool {
	return required != 0 && p&required == required
}

// Valid reports whether p is a non-zero combination of known permission bits.
func (p Permission) Valid() bool {
	return p != 0 && p&^PermAll == 0
}

func (p Permission) String() string {
	if p&^PermAll != 0 || p == 0 {
		return ""
	}
	if p == PermAll {
		return "*"
	}
	var b strings.Builder
	if p&PermRead != 0 {
		b.WriteByte('r')
	}
	if p&PermWrite != 0 {
		b.WriteByte('w')
	}
	if p&PermExecute != 0 {
		b.WriteByte('x')
	}
	return b.String()
}

// ParsePermission parses exactly one permission token. The token must be "*"
// or a non-empty combination of the distinct lowercase letters r, w, and x.
func ParsePermission(s string) (Permission, error) {
	if s == "" {
		return PermNone, &ParseError{Reason: "empty permission"}
	}
	if s == "*" {
		return PermAll, nil
	}

	var p Permission
	for i := 0; i < len(s); i++ {
		var bit Permission
		switch s[i] {
		case 'r':
			bit = PermRead
		case 'w':
			bit = PermWrite
		case 'x':
			bit = PermExecute
		default:
			return PermNone, &ParseError{Reason: fmt.Sprintf("invalid permission %q", s)}
		}
		if p&bit != 0 {
			return PermNone, &ParseError{Reason: fmt.Sprintf("duplicate permission %q", s)}
		}
		p |= bit
	}
	return p, nil
}

// Capability is one allow rule. An unset pool or namespace means that part of
// the scope is unrestricted; it is not a textual wildcard. When HasPool or
// HasNamespace is false, the corresponding name must be empty.
type Capability struct {
	Permissions Permission

	Pool    string
	HasPool bool

	Namespace    string
	HasNamespace bool
}

// Caps is the ordered union of capabilities. Matching succeeds when any one
// capability grants the requested permission in the requested scope.
type Caps []Capability

// ParseCaps parses the complete capability string. A single malformed rule
// invalidates the entire string.
func ParseCaps(s string) (Caps, error) {
	if strings.TrimSpace(s) == "" {
		return nil, &ParseError{Reason: "empty caps string"}
	}

	rawRules := strings.Split(s, ",")
	caps := make(Caps, 0, len(rawRules))
	for i, raw := range rawRules {
		ruleNumber := i + 1
		rule := strings.TrimSpace(raw)
		if rule == "" {
			return nil, &ParseError{Rule: ruleNumber, Reason: "empty rule"}
		}

		fields := strings.Fields(rule)
		if len(fields) == 0 || fields[0] != "allow" {
			return nil, &ParseError{Rule: ruleNumber, Reason: "rule must start with allow"}
		}
		if len(fields) < 2 {
			return nil, &ParseError{Rule: ruleNumber, Reason: "missing permission"}
		}

		perms, err := ParsePermission(fields[1])
		if err != nil {
			return nil, withRule(err, ruleNumber)
		}
		capability := Capability{Permissions: perms}

		// The grammar is allow <permission> [pool=name] [namespace=name].
		// Keeping the order strict makes alternate spellings unambiguous.
		seenPool := false
		seenNamespace := false
		for _, token := range fields[2:] {
			switch {
			case strings.HasPrefix(token, "pool="):
				if seenPool {
					return nil, &ParseError{Rule: ruleNumber, Reason: "duplicate pool"}
				}
				if seenNamespace {
					return nil, &ParseError{Rule: ruleNumber, Reason: "pool must precede namespace"}
				}
				name := strings.TrimPrefix(token, "pool=")
				if reason := scopeNameError(name); reason != "" {
					return nil, &ParseError{Rule: ruleNumber, Reason: "invalid pool: " + reason}
				}
				capability.Pool = name
				capability.HasPool = true
				seenPool = true

			case strings.HasPrefix(token, "namespace="):
				if seenNamespace {
					return nil, &ParseError{Rule: ruleNumber, Reason: "duplicate namespace"}
				}
				name := strings.TrimPrefix(token, "namespace=")
				if reason := scopeNameError(name); reason != "" {
					return nil, &ParseError{Rule: ruleNumber, Reason: "invalid namespace: " + reason}
				}
				capability.Namespace = name
				capability.HasNamespace = true
				seenNamespace = true

			default:
				return nil, &ParseError{
					Rule:   ruleNumber,
					Reason: fmt.Sprintf("unknown keyword %q", token),
				}
			}
		}

		caps = append(caps, capability)
	}
	return caps, nil
}

// Parse is an alias for ParseCaps.
func Parse(s string) (Caps, error) {
	return ParseCaps(s)
}

func withRule(err error, rule int) error {
	var parseErr *ParseError
	if errors.As(err, &parseErr) {
		parseErr.Rule = rule
		return parseErr
	}
	return err
}

func scopeNameError(name string) string {
	if name == "" {
		return "empty name"
	}
	for _, r := range name {
		switch {
		case r == ',':
			return "name contains a comma"
		case r == '=':
			return "name contains '='"
		case r == '*' || r == '?':
			return "wildcards are not supported"
		case unicode.IsSpace(r) || unicode.IsControl(r):
			return "name contains whitespace or a control character"
		}
	}
	return ""
}

// Validate checks that every capability is well formed. An empty set is
// valid: it grants no capabilities.
func (c Caps) Validate() error {
	for i, capability := range c {
		if !capability.Permissions.Valid() {
			return fmt.Errorf("%w: capability %d: invalid permissions", ErrInvalidCaps, i+1)
		}
		if capability.HasPool {
			if reason := scopeNameError(capability.Pool); reason != "" {
				return fmt.Errorf("%w: capability %d: invalid pool: %s", ErrInvalidCaps, i+1, reason)
			}
		} else if capability.Pool != "" {
			return fmt.Errorf("%w: capability %d: pool name set without pool", ErrInvalidCaps, i+1)
		}
		if capability.HasNamespace {
			if reason := scopeNameError(capability.Namespace); reason != "" {
				return fmt.Errorf("%w: capability %d: invalid namespace: %s", ErrInvalidCaps, i+1, reason)
			}
		} else if capability.Namespace != "" {
			return fmt.Errorf("%w: capability %d: namespace set without namespace", ErrInvalidCaps, i+1)
		}
	}
	return nil
}

// Allows reports whether the union grants perm for the exact pool and
// namespace bytes. Permissions from rules with overlapping scopes are
// combined. An omitted pool or namespace in a rule covers every value.
func (c Caps) Allows(pool, namespace string, perm Permission) bool {
	if !perm.Valid() || c.Validate() != nil {
		return false
	}
	return c.permissionsFor(pool, namespace).Has(perm)
}

// PermissionsFor returns the union of permissions granted for one scope.
// Malformed capability sets grant nothing.
func (c Caps) PermissionsFor(pool, namespace string) Permission {
	if c.Validate() != nil {
		return PermNone
	}
	return c.permissionsFor(pool, namespace)
}

func (c Caps) permissionsFor(pool, namespace string) Permission {
	var granted Permission
	for _, capability := range c {
		if capability.matchesScope(pool, namespace) {
			granted |= capability.Permissions
		}
	}
	return granted
}

// Match is an alias for Allows.
func (c Caps) Match(pool, namespace string, perm Permission) bool {
	return c.Allows(pool, namespace, perm)
}

// Matches reports whether this single capability grants perm in the given
// scope.
func (c Capability) Matches(pool, namespace string, perm Permission) bool {
	if !perm.Valid() || !c.Permissions.Has(perm) {
		return false
	}
	if (Caps{c}).Validate() != nil {
		return false
	}
	return c.matchesScope(pool, namespace)
}

func (c Capability) matchesScope(pool, namespace string) bool {
	if c.HasPool && c.Pool != pool {
		return false
	}
	if c.HasNamespace && c.Namespace != namespace {
		return false
	}
	return true
}

// Covers reports whether this capability grants every right and scope granted
// by other.
func (c Capability) Covers(other Capability) bool {
	if err := (Caps{c, other}).Validate(); err != nil {
		return false
	}
	if !c.Permissions.Has(other.Permissions) {
		return false
	}
	if c.HasPool && (!other.HasPool || c.Pool != other.Pool) {
		return false
	}
	if c.HasNamespace && (!other.HasNamespace || c.Namespace != other.Namespace) {
		return false
	}
	return true
}

// IsSubsetOf applies the renewal policy: every new capability must be covered
// by one old capability, with old permissions a superset and old scope equal
// or broader. Requiring coverage by a single old rule keeps the check
// entry-by-entry and avoids combining unrelated grants.
func (c Caps) IsSubsetOf(old Caps) bool {
	if err := c.Validate(); err != nil {
		return false
	}
	if err := old.Validate(); err != nil {
		return false
	}
	for _, newCapability := range c {
		covered := false
		for _, oldCapability := range old {
			if oldCapability.Covers(newCapability) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

// SubsetOf is an alias for IsSubsetOf.
func (c Caps) SubsetOf(old Caps) bool {
	return c.IsSubsetOf(old)
}

// Covers reports whether the union c grants every capability in other.
func (c Caps) Covers(other Caps) bool {
	return other.IsSubsetOf(c)
}

func (c Capability) String() string {
	var b strings.Builder
	b.WriteString("allow ")
	b.WriteString(c.Permissions.String())
	if c.HasPool {
		b.WriteString(" pool=")
		b.WriteString(c.Pool)
	}
	if c.HasNamespace {
		b.WriteString(" namespace=")
		b.WriteString(c.Namespace)
	}
	return b.String()
}

func (c Caps) String() string {
	parts := make([]string, len(c))
	for i, capability := range c {
		parts[i] = capability.String()
	}
	return strings.Join(parts, ", ")
}
