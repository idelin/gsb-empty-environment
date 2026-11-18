package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxFragmentBytes is the largest fragment accepted by Admit, including a
// leading UTF-8 BOM and CRLF line endings.
const MaxFragmentBytes = 64 << 10

// MaxFragmentSize is an alias for MaxFragmentBytes.
const MaxFragmentSize = MaxFragmentBytes

var (
	// ErrFragmentTooLarge indicates that the uploaded fragment exceeds 64 KiB.
	ErrFragmentTooLarge = errors.New("TOML fragment exceeds 64 KiB")
	// ErrInvalidUTF8 indicates that the fragment is not valid UTF-8.
	ErrInvalidUTF8 = errors.New("TOML fragment is not valid UTF-8")
	// ErrInvalidTOML indicates malformed TOML syntax.
	ErrInvalidTOML = errors.New("invalid TOML")
	// ErrForbiddenRoot indicates that a complete key path does not start with
	// the case-sensitive segment "user".
	ErrForbiddenRoot = errors.New("TOML key path must start with \"user\"")
	// ErrDuplicateKey indicates that a key or value path is defined twice.
	ErrDuplicateKey = errors.New("TOML key is already defined")
	// ErrTableRedefined indicates that a table header and a dotted key both
	// define the same table, or that a table header is repeated.
	ErrTableRedefined = errors.New("TOML table is already defined")
	// ErrInlineTableAppend indicates an attempt to add keys or subtables to a
	// closed inline table.
	ErrInlineTableAppend = errors.New("cannot append to an inline table")
	// ErrArrayTableMismatch indicates that a path is used both as a normal
	// table and as an array of tables.
	ErrArrayTableMismatch = errors.New("TOML table and array table cannot share a path")
)

// Compatibility aliases for callers that prefer shorter names.
var (
	ErrTooLarge          = ErrFragmentTooLarge
	ErrRootNotUser       = ErrForbiddenRoot
	ErrTableKindConflict = ErrArrayTableMismatch
)

// Error describes why a fragment was rejected.
type Error struct {
	// Err is one of the package error sentinels.
	Err error
	// Detail adds parser or merge context. It may be empty.
	Detail string
	// Path is the decoded, complete key path involved in the failure.
	Path []string
	// Offset is the zero-based byte offset in the normalized fragment.
	Offset int
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("netsec: ")
	b.WriteString(e.Err.Error())
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	if len(e.Path) != 0 {
		fmt.Fprintf(&b, " (path %s)", formatPath(e.Path))
	}
	if e.Offset >= 0 {
		fmt.Fprintf(&b, " (byte %d)", e.Offset)
	}
	return b.String()
}

// Unwrap supports errors.Is and errors.As with the exported sentinels.
func (e *Error) Unwrap() error { return e.Err }

func newError(kind error, detail string, path []string, offset int) *Error {
	return &Error{
		Err:    kind,
		Detail: detail,
		Path:   append([]string(nil), path...),
		Offset: offset,
	}
}

func formatPath(path []string) string {
	parts := make([]string, len(path))
	for i, part := range path {
		parts[i] = fmt.Sprintf("%q", part)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// Admitter validates and merges successive user fragments into one namespace.
// It is not safe for concurrent use.
type Admitter struct {
	root *tableNode
}

// Validator is an alias for Admitter.
type Validator = Admitter

// State is an alias for Admitter.
type State = Admitter

// NewAdmitter returns an empty admission state.
func NewAdmitter() *Admitter {
	return &Admitter{root: newTable()}
}

// New is an alias for NewAdmitter.
func New() *Admitter { return NewAdmitter() }

// NewValidator is an alias for NewAdmitter.
func NewValidator() *Validator { return NewAdmitter() }

// NewState is an alias for NewAdmitter.
func NewState() *State { return NewAdmitter() }

// Reset discards all previously admitted fragments.
func (a *Admitter) Reset() {
	a.root = newTable()
}

// Validate checks one standalone fragment against an empty admission state.
// Use Admitter.Admit when validating fragments that will be merged.
func Validate(fragment []byte) error {
	return NewAdmitter().Admit(fragment)
}

// ValidateFragment is an alias for Validate.
func ValidateFragment(fragment []byte) error { return Validate(fragment) }

// Admit parses one fragment and atomically merges it into the admission state.
// On error the state is left unchanged.
func (a *Admitter) Admit(fragment []byte) error {
	if a == nil {
		return errors.New("netsec: nil Admitter")
	}
	if a.root == nil {
		a.root = newTable()
	}
	normalized, err := normalizeFragment(fragment)
	if err != nil {
		return err
	}

	working := a.root.clone()
	p := newParser(normalized, working)
	if err := p.parse(); err != nil {
		return err
	}
	a.root = working
	return nil
}

// Validate is a stateful alias for Admit.
func (a *Admitter) Validate(fragment []byte) error { return a.Admit(fragment) }

func normalizeFragment(fragment []byte) ([]byte, error) {
	if len(fragment) > MaxFragmentBytes {
		return nil, newError(ErrFragmentTooLarge, "", nil, -1)
	}
	if !utf8.Valid(fragment) {
		return nil, newError(ErrInvalidUTF8, "", nil, utf8Offset(fragment))
	}

	if bytes.HasPrefix(fragment, []byte{0xEF, 0xBB, 0xBF}) {
		fragment = fragment[3:]
	}
	if !bytes.Contains(fragment, []byte{'\r'}) {
		return fragment, nil
	}

	normalized := make([]byte, 0, len(fragment))
	for i := 0; i < len(fragment); i++ {
		if fragment[i] == '\r' && i+1 < len(fragment) && fragment[i+1] == '\n' {
			normalized = append(normalized, '\n')
			i++
			continue
		}
		normalized = append(normalized, fragment[i])
	}
	return normalized, nil
}

func utf8Offset(b []byte) int {
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size == 1 {
			return i
		}
		i += size
	}
	return -1
}
