package netsec

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// ObjectType is the kind of kernel object a name is allowed to denote.
type ObjectType uint8

const (
	ObjectTypeUnknown ObjectType = iota
	ObjectTypeMutex
	ObjectTypeSection
)

func (t ObjectType) String() string {
	switch t {
	case ObjectTypeMutex:
		return "mutex"
	case ObjectTypeSection:
		return "section"
	default:
		return "unknown"
	}
}

// Valid reports whether t is a type understood by the admission package.
func (t ObjectType) Valid() bool {
	return t == ObjectTypeMutex || t == ObjectTypeSection
}

// Scope selects the object-manager namespace used to resolve a name.
//
// A scope is either global and shared by every session, or bound to exactly
// one non-empty session identifier.
type Scope struct {
	Global    bool
	SessionID string
}

// GlobalScope returns the cross-session object-manager scope.
func GlobalScope() Scope {
	return Scope{Global: true}
}

// SessionScope returns the namespace belonging to sessionID.
func SessionScope(sessionID string) Scope {
	return Scope{SessionID: sessionID}
}

// Validate checks that the scope is unambiguous.
func (s Scope) Validate() error {
	if s.Global {
		if s.SessionID != "" {
			return fmt.Errorf("%w: global scope must not have a session ID", ErrInvalidScope)
		}
		return nil
	}
	if s.SessionID == "" {
		return fmt.Errorf("%w: session scope requires a session ID", ErrInvalidScope)
	}
	if strings.ContainsRune(s.SessionID, 0) {
		return fmt.Errorf("%w: session ID contains NUL", ErrInvalidScope)
	}
	return nil
}

func (s Scope) String() string {
	if s.Global {
		return "Global"
	}
	return "Session[" + s.SessionID + "]"
}

// Name is a fully specified object-manager name. Text is relative to Scope and
// uses backslash as the separator, as in the Windows object namespace.
type Name struct {
	Scope Scope
	Text  string
	Type  ObjectType
}

// NewName validates and constructs a name.
func NewName(scope Scope, text string, typ ObjectType) (Name, error) {
	n := Name{Scope: scope, Text: text, Type: typ}
	if err := n.Validate(); err != nil {
		return Name{}, err
	}
	return n, nil
}

// MustName is NewName for package-level or test configuration that cannot
// reasonably recover from an invalid fixed name.
func MustName(scope Scope, text string, typ ObjectType) Name {
	n, err := NewName(scope, text, typ)
	if err != nil {
		panic(err)
	}
	return n
}

// Validate applies the admission rules for a name.
func (n Name) Validate() error {
	if err := n.Scope.Validate(); err != nil {
		return err
	}
	if !n.Type.Valid() {
		return fmt.Errorf("%w: %d", ErrInvalidObjectType, n.Type)
	}
	return validateUTF16Name(n.Text)
}

func (n Name) String() string {
	return n.Scope.String() + `\` + n.Text + " (" + n.Type.String() + ")"
}

func validateUTF16Name(text string) error {
	if text == "" {
		return fmt.Errorf("%w: name is empty", ErrInvalidName)
	}
	if !utf8.ValidString(text) {
		return fmt.Errorf("%w: name is not valid UTF-8", ErrInvalidUTF16)
	}
	for _, r := range text {
		switch {
		case r == 0:
			return fmt.Errorf("%w: name contains NUL", ErrNULInName)
		case utf16.IsSurrogate(r):
			return fmt.Errorf("%w: name contains a surrogate", ErrInvalidUTF16)
		}
	}

	// The namespace key is derived from UTF-16 code units. Re-encoding here
	// also ensures the input has a single well-formed UTF-16 representation.
	units := utf16.Encode([]rune(text))
	if len(units) == 0 || string(utf16.Decode(units)) != text {
		return fmt.Errorf("%w: name is not canonical UTF-16", ErrInvalidUTF16)
	}

	if strings.ContainsRune(text, '/') {
		return fmt.Errorf("%w: '/' is not a namespace separator", ErrInvalidName)
	}

	parts := strings.Split(text, `\`)
	for _, part := range parts {
		switch {
		case part == "":
			return fmt.Errorf("%w: empty path component", ErrInvalidName)
		case part == "." || part == "..":
			return fmt.Errorf("%w: dot path component %q", ErrDotSegment, part)
		case strings.HasSuffix(part, " "), strings.HasSuffix(part, "."):
			return fmt.Errorf("%w: component %q ends in a space or dot", ErrTrailingSpaceOrDot, part)
		}
	}
	return nil
}

func (n Name) canonicalKey() (string, error) {
	if err := n.Validate(); err != nil {
		return "", err
	}
	return makeKey(n.Scope, foldName(n.Text)), nil
}

func makeKey(scope Scope, foldedName string) string {
	if scope.Global {
		return "G\x00" + foldedName
	}
	return fmt.Sprintf("S%d:%s\x00%s", len(scope.SessionID), scope.SessionID, foldedName)
}

func sameFoldedName(a, b string) bool {
	return foldName(a) == foldName(b)
}

var foldExpansions = map[rune]string{
	'\u00b5': "\u03bc", // micro sign -> Greek small mu
	'\u00df': "ss",     // ß
	'\u0130': "i\u0307",
	'\u017f': "s",      // long s
	'\u03c2': "\u03c3", // final sigma
	'\u1e9e': "ss",     // ẞ
	'\ufb00': "ff",
	'\ufb01': "fi",
	'\ufb02': "fl",
	'\ufb03': "ffi",
	'\ufb04': "ffl",
	'\ufb05': "st",
	'\ufb06': "st",
}

// foldName produces the case-folded UTF-16 ordering key. It intentionally
// covers the common full-fold expansions rather than only ASCII lowercasing,
// so names such as "Straße" and "STRASSE" collide.
func foldName(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if r < utf8.RuneSelf {
			if 'A' <= r && r <= 'Z' {
				r += 'a' - 'A'
			}
			b.WriteByte(byte(r))
			continue
		}
		if expansion, ok := foldExpansions[r]; ok {
			b.WriteString(expansion)
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
