package netsec

import (
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"strings"
	"sync"
)

// MergePolicy decides how repeated instances of the same header field are
// emitted when a snapshot is sealed.
type MergePolicy int

const (
	// PreserveInstances keeps every instance of the field as its own line.
	PreserveInstances MergePolicy = iota
	// CommaMerge folds repeated instances into one line joined by ", ".
	// Set-Cookie is exempt: it is always preserved, never comma-merged.
	CommaMerge
)

var (
	// ErrEmptyName is returned when a header name is empty.
	ErrEmptyName = errors.New("netsec: empty header name")
	// ErrNilBuilder is returned when sealing a nil builder.
	ErrNilBuilder = errors.New("netsec: nil builder")
)

// isTokenChar reports whether b is an RFC 7230 tchar, the only bytes
// permitted in a header field name.
func isTokenChar(b byte) bool {
	switch {
	case '0' <= b && b <= '9', 'a' <= b && b <= 'z', 'A' <= b && b <= 'Z':
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

// isValueChar reports whether b may appear in a header field value:
// HTAB, SP, VCHAR (excluding DEL) and obs-text. CR and LF are excluded,
// so response-splitting bytes can never pass validation.
func isValueChar(b byte) bool {
	return b == '\t' || (b >= 0x20 && b != 0x7f)
}

// ValidateName checks a header field name byte by byte.
func ValidateName(name string) error {
	if name == "" {
		return ErrEmptyName
	}
	for i := 0; i < len(name); i++ {
		if !isTokenChar(name[i]) {
			return fmt.Errorf("netsec: invalid byte 0x%02x in header name %q at offset %d", name[i], name, i)
		}
	}
	return nil
}

// ValidateValue checks a header field value byte by byte.
func ValidateValue(value string) error {
	for i := 0; i < len(value); i++ {
		if !isValueChar(value[i]) {
			return fmt.Errorf("netsec: invalid byte 0x%02x in header value at offset %d", value[i], i)
		}
	}
	return nil
}

// canonicalName normalizes a name for policy lookup and merging. The name
// must already have been validated.
func canonicalName(name string) string {
	return textproto.CanonicalMIMEHeaderKey(name)
}

// isSetCookie reports whether a canonical name is Set-Cookie, which RFC 6265
// forbids from being combined with commas.
func isSetCookie(canonical string) bool {
	return canonical == "Set-Cookie"
}

// Entry is a single validated header line in a sealed snapshot.
type Entry struct {
	Name  string
	Value string
}

// Snapshot is an immutable, fully validated set of header lines. It is safe
// for concurrent use after publication.
type Snapshot struct {
	entries []Entry
}

// Entries returns a copy of the sealed header lines in emission order.
func (s *Snapshot) Entries() []Entry {
	if s == nil {
		return nil
	}
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

// Values returns every value sealed under name, in order.
func (s *Snapshot) Values(name string) []string {
	if s == nil {
		return nil
	}
	canonical := canonicalName(name)
	var out []string
	for _, e := range s.entries {
		if e.Name == canonical {
			out = append(out, e.Value)
		}
	}
	return out
}

// Builder accumulates header lines for one response. Nothing is published
// until Seal succeeds; a failed Add leaves the builder's accepted input
// untouched and never affects any previously sealed snapshot.
type Builder struct {
	policy func(canonical string) MergePolicy
	order  []string
	values map[string][]string
}

// NewBuilder creates a Builder. policy maps a canonical header name to its
// merge policy; a nil policy func means PreserveInstances for every field.
func NewBuilder(policy func(canonical string) MergePolicy) *Builder {
	return &Builder{
		policy: policy,
		values: make(map[string][]string),
	}
}

// Add validates name and value byte by byte, then stages the line. On error
// nothing is staged.
func (b *Builder) Add(name, value string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := ValidateValue(value); err != nil {
		return fmt.Errorf("netsec: header %q: %w", name, err)
	}
	canonical := canonicalName(name)
	if _, seen := b.values[canonical]; !seen {
		b.order = append(b.order, canonical)
	}
	b.values[canonical] = append(b.values[canonical], value)
	return nil
}

// AddReader stages a header value read from r, validating byte by byte as
// the stream is consumed. Injection bytes are caught even when they straddle
// read-chunk boundaries, and a read or validation failure stages nothing.
func (b *Builder) AddReader(name string, r io.Reader) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	var sb strings.Builder
	var buf [4096]byte
	offset := 0
	for {
		n, err := r.Read(buf[:])
		for i := 0; i < n; i++ {
			if !isValueChar(buf[i]) {
				return fmt.Errorf("netsec: header %q: invalid byte 0x%02x in value at stream offset %d", name, buf[i], offset+i)
			}
		}
		sb.Write(buf[:n])
		offset += n
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("netsec: header %q: read value: %w", name, err)
		}
	}
	return b.Add(name, sb.String())
}

// Seal re-validates every staged line and freezes the result into an
// immutable Snapshot, applying each field's merge policy. Set-Cookie
// instances are always preserved, never comma-merged. Only after every line
// passes validation is a snapshot produced; on error it returns nil and no
// published state changes.
func (b *Builder) Seal() (*Snapshot, error) {
	if b == nil {
		return nil, ErrNilBuilder
	}
	for _, name := range b.order {
		for _, value := range b.values[name] {
			if err := ValidateName(name); err != nil {
				return nil, err
			}
			if err := ValidateValue(value); err != nil {
				return nil, fmt.Errorf("netsec: header %q: %w", name, err)
			}
		}
	}
	var entries []Entry
	for _, name := range b.order {
		values := b.values[name]
		if b.merge(name) == CommaMerge && len(values) > 1 {
			entries = append(entries, Entry{Name: name, Value: strings.Join(values, ", ")})
			continue
		}
		for _, value := range values {
			entries = append(entries, Entry{Name: name, Value: value})
		}
	}
	return &Snapshot{entries: entries}, nil
}

func (b *Builder) merge(canonical string) MergePolicy {
	if isSetCookie(canonical) {
		return PreserveInstances
	}
	if b.policy == nil {
		return PreserveInstances
	}
	return b.policy(canonical)
}

// Sealer holds the currently published snapshot for a response host. The
// snapshot is swapped atomically and only after full validation, so readers
// never observe a partially written or failed response.
type Sealer struct {
	mu      sync.RWMutex
	current *Snapshot
}

// NewSealer creates an empty Sealer.
func NewSealer() *Sealer {
	return &Sealer{}
}

// Publish seals b and, only on success, atomically replaces the published
// snapshot. A failed seal leaves the previously published snapshot intact.
func (s *Sealer) Publish(b *Builder) (*Snapshot, error) {
	snap, err := b.Seal()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.current = snap
	s.mu.Unlock()
	return snap, nil
}

// Current returns the most recently published snapshot, or nil if none.
func (s *Sealer) Current() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}
