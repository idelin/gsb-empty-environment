package netsec

// ResourceKind identifies the kind of a broker resource. A pattern only
// applies to resources of the exact same kind: a topic pattern can never
// authorize a consumer group or a transaction id and vice versa.
type ResourceKind uint8

const (
	// KindTopic is a publish/subscribe topic.
	KindTopic ResourceKind = iota + 1
	// KindGroup is a consumer group.
	KindGroup
	// KindTransaction is a transactional id used by the producer.
	KindTransaction
)

// String returns a stable, human readable name for the resource kind.
func (k ResourceKind) String() string {
	switch k {
	case KindTopic:
		return "topic"
	case KindGroup:
		return "group"
	case KindTransaction:
		return "transaction"
	default:
		return "unknown"
	}
}

// Valid reports whether k is one of the known resource kinds.
func (k ResourceKind) Valid() bool {
	return k == KindTopic || k == KindGroup || k == KindTransaction
}

// PatternType selects how a Pattern value is interpreted.
type PatternType uint8

const (
	// TypeLiteral matches exactly one fully qualified resource name.
	TypeLiteral PatternType = iota + 1
	// TypePrefix matches a resource name on a segment boundary: either the
	// name equals the prefix, or the next character after the prefix is the
	// configured delimiter. A bare string "contains" match is intentionally
	// not supported, because it lets "tenant-a" match "tenant-a-secret".
	TypePrefix
	// TypeWildcard matches segment by segment. Each segment supports '*'
	// globbing within the segment; '*' never crosses the delimiter. The
	// wildcard pattern must have the same number of segments as the name.
	TypeWildcard
)

// String returns a stable, human readable name for the pattern type.
func (t PatternType) String() string {
	switch t {
	case TypeLiteral:
		return "literal"
	case TypePrefix:
		return "prefix"
	case TypeWildcard:
		return "wildcard"
	default:
		return "unknown"
	}
}

// Valid reports whether t is one of the known pattern types.
func (t PatternType) Valid() bool {
	return t == TypeLiteral || t == TypePrefix || t == TypeWildcard
}

// Resource is a concrete broker resource being authorized.
type Resource struct {
	// Kind is the resource type; it must match the pattern kind.
	Kind ResourceKind
	// Name is the fully qualified resource name. It must be non-empty and
	// must not contain leading, trailing or repeated delimiters.
	Name string
}

// Valid checks that the resource has a known kind and a well formed name.
func (r Resource) Valid(delim byte) error {
	if !r.Kind.Valid() {
		return ErrUnknownKind
	}
	return validateName(r.Name, "resource name", delim)
}

// Pattern is an authorization pattern bound to a single resource kind.
type Pattern struct {
	// Kind is the only resource kind this pattern can match.
	Kind ResourceKind
	// Type selects literal, prefix or wildcard interpretation.
	Type PatternType
	// Value is the literal name, the segment-safe prefix or the glob
	// expression depending on Type.
	Value string
}

// Valid checks the kind/type are known and that Value is well formed for
// the selected type.
func (p Pattern) Valid(delim byte) error {
	if !p.Kind.Valid() {
		return ErrUnknownKind
	}
	if !p.Type.Valid() {
		return ErrUnknownPatternType
	}
	switch p.Type {
	case TypeLiteral:
		return validateName(p.Value, "literal pattern", delim)
	case TypePrefix:
		if p.Value == "" {
			return ErrEmptyName
		}
		// A single trailing delimiter is accepted because policies are often
		// written as "tenant-a-" to mark the namespace boundary explicitly.
		// It is normalized away at compile time; doubled delimiters are still
		// rejected because they never describe a valid segment boundary.
		value := p.Value
		if value[len(value)-1] == delim {
			value = value[:len(value)-1]
			if value == "" {
				return ErrEmptyName
			}
		}
		return validateSegments(value, "prefix pattern", delim)
	case TypeWildcard:
		if p.Value == "" {
			return ErrEmptyName
		}
		return validateSegments(p.Value, "wildcard pattern", delim)
	default:
		return ErrUnknownPatternType
	}
}

// Matcher reports whether a concrete resource name matches the pattern.
// Matchers are compiled once per ACL generation and are safe for concurrent
// use because they close over immutable data.
type Matcher func(name string) bool

// Compile turns a validated pattern into a Matcher.
func (p Pattern) Compile(delim byte) (Matcher, error) {
	if err := p.Valid(delim); err != nil {
		return nil, err
	}
	switch p.Type {
	case TypeLiteral:
		value := p.Value
		return func(name string) bool {
			return name == value
		}, nil
	case TypePrefix:
		prefix := p.Value
		// A trailing delimiter marks a namespace-only prefix: it must not
		// match the bare name without that trailing segment. Preserve the
		// marker on the compiled closure rather than stripping it.
		namespaceOnly := prefix[len(prefix)-1] == delim
		if namespaceOnly {
			prefix = prefix[:len(prefix)-1]
		}
		return func(name string) bool {
			if namespaceOnly && name == prefix {
				return false
			}
			return segmentPrefixMatch(name, prefix, delim)
		}, nil
	case TypeWildcard:
		segments := splitSegments(p.Value, delim)
		return func(name string) bool {
			nameSegments := splitSegments(name, delim)
			if len(nameSegments) != len(segments) {
				return false
			}
			for i, segment := range segments {
				if !globMatch(segment, nameSegments[i]) {
					return false
				}
			}
			return true
		}, nil
	default:
		return nil, ErrUnknownPatternType
	}
}

// segmentPrefixMatch implements the boundary-safe prefix rule. It returns
// true only when name equals prefix or when the character immediately
// following the prefix is the delimiter. "tenant-a" therefore does not
// match "tenant-a-secret" as a bare string, while "tenant-a" does match
// "tenant-a" itself and a prefix declared as "tenant-a-" (or a prefix whose
// own segments are "tenant","a") matches on the delimiter boundary.
func segmentPrefixMatch(name, prefix string, delim byte) bool {
	if len(name) < len(prefix) || name[:len(prefix)] != prefix {
		return false
	}
	if len(name) == len(prefix) {
		return true
	}
	return name[len(prefix)] == delim
}

// splitSegments breaks a validated name or pattern into its segments.
func splitSegments(value string, delim byte) []string {
	var segments []string
	start := 0
	for i := 0; i < len(value); i++ {
		if value[i] == delim {
			segments = append(segments, value[start:i])
			start = i + 1
		}
	}
	return append(segments, value[start:])
}

// globMatch reports whether segment matches the glob pattern, where '*'
// stands for any run of runes inside a single segment (it never matches the
// resource delimiter because segments are compared independently).
func globMatch(pattern, segment string) bool {
	return globMatchRunes([]rune(pattern), []rune(segment))
}

func globMatchRunes(pattern, value []rune) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			// Collapse consecutive stars and try every possible suffix.
			for len(pattern) > 1 && pattern[1] == '*' {
				pattern = pattern[1:]
			}
			if len(pattern) == 1 {
				return true
			}
			for i := 0; i <= len(value); i++ {
				if globMatchRunes(pattern[1:], value[i:]) {
					return true
				}
			}
			return false
		default:
			if len(value) == 0 || value[0] != pattern[0] {
				return false
			}
			pattern = pattern[1:]
			value = value[1:]
		}
	}
	return len(value) == 0
}

// validateName rejects empty names and names with empty segments.
func validateName(value, field string, delim byte) error {
	if value == "" {
		return ErrEmptyName
	}
	return validateSegments(value, field, delim)
}

// validateSegments rejects leading, trailing and repeated delimiters so that
// segment boundaries stay unambiguous.
func validateSegments(value, field string, delim byte) error {
	if value[0] == delim {
		return &MalformedNameError{Value: value, Reason: field + " must not start with the delimiter"}
	}
	if value[len(value)-1] == delim {
		return &MalformedNameError{Value: value, Reason: field + " must not end with the delimiter"}
	}
	for i := 0; i+1 < len(value); i++ {
		if value[i] == delim && value[i+1] == delim {
			return &MalformedNameError{Value: value, Reason: field + " must not contain repeated delimiters"}
		}
	}
	return nil
}

// MalformedNameError explains why a name or pattern value is invalid.
type MalformedNameError struct {
	Value  string
	Reason string
}

func (e *MalformedNameError) Error() string {
	return "netsec: malformed resource name/pattern " + quote(e.Value) + ": " + e.Reason
}

func quote(value string) string {
	return "\"" + value + "\""
}
