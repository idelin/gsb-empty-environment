package taskpkg

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const (
	// MaxLabelLength is the largest DNS label payload permitted here.
	MaxLabelLength = 63
	// MaxNameWireLength is the maximum expanded wire length, including the
	// length octets and the final root label.
	MaxNameWireLength = 255
)

var (
	// ErrInvalidOffset means the requested name does not start at a valid
	// position in the message.
	ErrInvalidOffset = errors.New("taskpkg: invalid name offset")
	// ErrTruncated means the message ends before the encoded name does.
	ErrTruncated = errors.New("taskpkg: truncated name")
	// ErrBadLabelType means a label used one of the reserved 0x40/0x80 forms.
	ErrBadLabelType = errors.New("taskpkg: reserved label type")
	// ErrPointerOutOfRange means a pointer target lies outside the message.
	ErrPointerOutOfRange = errors.New("taskpkg: pointer target out of range")
	// ErrPointerForward means a pointer does not point strictly backwards.
	ErrPointerForward = errors.New("taskpkg: pointer does not point backwards")
	// ErrPointerNotLabelStart means a pointer target is not a previously
	// validated start of a complete label.
	ErrPointerNotLabelStart = errors.New("taskpkg: pointer target is not a complete label start")
	// ErrCompressionLoop means following a pointer revisited the same pointer.
	ErrCompressionLoop = errors.New("taskpkg: compression pointer loop")
	// ErrNameTooLong means the expanded name exceeds MaxNameWireLength.
	ErrNameTooLong = errors.New("taskpkg: expanded name exceeds 255 bytes")
	// ErrMissingCheck means no security-check callback was supplied.
	ErrMissingCheck = errors.New("taskpkg: missing security check callback")
	// ErrMissingBusiness means no business callback was supplied.
	ErrMissingBusiness = errors.New("taskpkg: missing business callback")
)

// DecodeError describes a rejected name. Use errors.Is with one of the
// exported Err* values to identify the rejection reason.
type DecodeError struct {
	Cause  error
	Offset int
	Target int
	Detail string
}

func (e *DecodeError) Error() string {
	var b strings.Builder
	if e.Cause != nil {
		b.WriteString(e.Cause.Error())
	} else {
		b.WriteString("taskpkg: decode error")
	}
	if e.Offset >= 0 {
		fmt.Fprintf(&b, " at offset %d", e.Offset)
	}
	if e.Target >= 0 {
		fmt.Fprintf(&b, " (target %d)", e.Target)
	}
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	return b.String()
}

// Unwrap allows errors.Is(err, ErrPointerForward), for example.
func (e *DecodeError) Unwrap() error { return e.Cause }

func decodeError(cause error, offset, target int, detail string) error {
	return &DecodeError{
		Cause:  cause,
		Offset: offset,
		Target: target,
		Detail: detail,
	}
}

type labelSpan struct {
	wireStart    int
	payloadStart int
	payloadLen   int
}

// Name is an immutable, fully expanded DNS name. Its byte slices are private
// and are never exposed by reference, so a callback cannot change the name
// observed by a later callback.
type Name struct {
	wire      []byte
	origins   []int
	labels    []labelSpan
	canonical []byte
	identity  string
}

// Message owns a private copy of a DNS message and remembers label starts
// validated by earlier successful decodes. Hosts should decode names in wire
// order through one Message when a name may point at an earlier name in the
// same datagram.
type Message struct {
	mu     sync.Mutex
	data   []byte
	starts map[int]struct{}
	inside map[int]struct{}
}

// NewMessage copies message so later mutation by the caller cannot change
// decoded names or the set of trusted label starts.
func NewMessage(message []byte) *Message {
	return &Message{
		data:   append([]byte(nil), message...),
		starts: make(map[int]struct{}),
		inside: make(map[int]struct{}),
	}
}

// Len reports the copied message length.
func (m *Message) Len() int {
	if m == nil {
		return 0
	}
	return len(m.data)
}

// DecodeName decodes one name from a fresh message. Compression targets are
// trusted only when they are complete label starts traversed while decoding
// this name. Use a Message to retain starts from names decoded earlier in the
// same datagram.
func DecodeName(message []byte, offset int) (*Name, error) {
	return NewMessage(message).DecodeName(offset)
}

// Decode is an alias for DecodeName.
func Decode(message []byte, offset int) (*Name, error) {
	return DecodeName(message, offset)
}

// DecodeName decodes one name at offset, then commits the label starts it
// validated. A failed decode commits nothing.
func (m *Message) DecodeName(offset int) (*Name, error) {
	if m == nil {
		return nil, decodeError(ErrInvalidOffset, offset, -1, "nil message")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.inside[offset]; ok {
		return nil, decodeError(ErrInvalidOffset, offset, -1, "offset is inside a validated label")
	}

	trusted := make(map[int]struct{}, len(m.starts)+8)
	for start := range m.starts {
		trusted[start] = struct{}{}
	}

	name, added, inside, err := decode(m.data, offset, trusted)
	if err != nil {
		return nil, err
	}
	for start := range added {
		m.starts[start] = struct{}{}
	}
	for pos := range inside {
		m.inside[pos] = struct{}{}
	}
	return name, nil
}

func decode(data []byte, start int, trusted map[int]struct{}) (*Name, map[int]struct{}, map[int]struct{}, error) {
	if start < 0 || start > len(data) {
		return nil, nil, nil, decodeError(ErrInvalidOffset, start, -1, "")
	}
	if start == len(data) {
		return nil, nil, nil, decodeError(ErrTruncated, start, -1, "missing name")
	}

	added := make(map[int]struct{})
	inside := make(map[int]struct{})
	wire := make([]byte, 0, 32)
	origins := make([]int, 0, 32)
	labels := make([]labelSpan, 0, 4)
	pointers := make(map[int]struct{})

	pos := start
	for {
		if pos >= len(data) {
			return nil, nil, nil, decodeError(ErrTruncated, pos, -1, "")
		}

		size := data[pos]
		switch size & 0xc0 {
		case 0xc0:
			if pos+1 >= len(data) {
				return nil, nil, nil, decodeError(ErrTruncated, pos, -1, "pointer needs two bytes")
			}

			target := int(size&0x3f)<<8 | int(data[pos+1])
			if target >= len(data) {
				return nil, nil, nil, decodeError(ErrPointerOutOfRange, pos, target, "")
			}
			if target >= pos {
				return nil, nil, nil, decodeError(ErrPointerForward, pos, target, "")
			}
			if _, ok := trusted[target]; !ok {
				return nil, nil, nil, decodeError(ErrPointerNotLabelStart, pos, target, "")
			}
			if _, seen := pointers[pos]; seen {
				return nil, nil, nil, decodeError(ErrCompressionLoop, pos, target, "")
			}

			pointers[pos] = struct{}{}
			inside[pos+1] = struct{}{}
			pos = target

		case 0x00:
			if size == 0 {
				trusted[pos] = struct{}{}
				added[pos] = struct{}{}
				wire = append(wire, 0)
				origins = append(origins, pos)
				if len(wire) > MaxNameWireLength {
					return nil, nil, nil, decodeError(ErrNameTooLong, pos, -1, "")
				}
				return newName(wire, origins, labels), added, inside, nil
			}

			n := int(size)
			if n > MaxLabelLength {
				return nil, nil, nil, decodeError(ErrBadLabelType, pos, -1, "")
			}
			end := pos + 1 + n
			if end > len(data) {
				return nil, nil, nil, decodeError(ErrTruncated, pos, -1, "label payload")
			}

			// Reserve one byte for the root label that must eventually
			// follow. This rejects oversized expansions before allocation.
			if len(wire)+1+n+1 > MaxNameWireLength {
				return nil, nil, nil, decodeError(ErrNameTooLong, pos, -1, "")
			}

			trusted[pos] = struct{}{}
			added[pos] = struct{}{}

			wireStart := len(wire)
			wire = append(wire, size)
			origins = append(origins, pos)
			payloadStart := len(wire)
			for i := 0; i < n; i++ {
				wire = append(wire, data[pos+1+i])
				origins = append(origins, pos+1+i)
				inside[pos+1+i] = struct{}{}
			}
			labels = append(labels, labelSpan{
				wireStart:    wireStart,
				payloadStart: payloadStart,
				payloadLen:   n,
			})
			pos = end

		default:
			return nil, nil, nil, decodeError(ErrBadLabelType, pos, -1, "")
		}
	}
}

func newName(wire []byte, origins []int, labels []labelSpan) *Name {
	canonical := append([]byte(nil), wire...)
	for i, b := range canonical {
		if b >= 'A' && b <= 'Z' {
			canonical[i] = b + ('a' - 'A')
		}
	}

	var identity strings.Builder
	if len(labels) == 0 {
		identity.WriteByte('.')
	} else {
		for i, span := range labels {
			if i != 0 {
				identity.WriteByte('.')
			}
			writeLabel(&identity, wire[span.payloadStart:span.payloadStart+span.payloadLen], true)
		}
	}

	return &Name{
		wire:      wire,
		origins:   origins,
		labels:    labels,
		canonical: canonical,
		identity:  identity.String(),
	}
}

func writeLabel(b *strings.Builder, label []byte, lower bool) {
	for _, c := range label {
		if lower && c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		switch {
		case c == '.' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c >= 0x20 && c <= 0x7e:
			b.WriteByte(c)
		default:
			b.WriteByte('\\')
			b.WriteByte('0' + c/100)
			b.WriteByte('0' + (c/10)%10)
			b.WriteByte('0' + c%10)
		}
	}
}

// String returns the name in escaped presentation form, preserving case.
func (n *Name) String() string {
	if n == nil {
		return ""
	}
	if len(n.labels) == 0 {
		return "."
	}

	var b strings.Builder
	for i, span := range n.labels {
		if i != 0 {
			b.WriteByte('.')
		}
		writeLabel(&b, n.wire[span.payloadStart:span.payloadStart+span.payloadLen], false)
	}
	return b.String()
}

// Identity is the case-insensitive authorization identity. It depends only on
// the expanded label bytes, never on compression layout or source positions.
// Equal names therefore have equal identities even when encoded as aliases.
func (n *Name) Identity() string {
	if n == nil {
		return ""
	}
	return n.identity
}

// CanonicalWire returns a copy of the expanded wire bytes with ASCII label
// letters lowercased. Compression pointers are not present.
func (n *Name) CanonicalWire() []byte {
	if n == nil {
		return nil
	}
	return append([]byte(nil), n.canonical...)
}

// Equal compares expanded, case-insensitive names.
func (n *Name) Equal(other *Name) bool {
	if n == nil || other == nil {
		return n == other
	}
	return bytes.Equal(n.canonical, other.canonical)
}

// Root reports whether the decoded name is the DNS root.
func (n *Name) Root() bool {
	return n != nil && len(n.labels) == 0
}

// ExpandedLength is the expanded wire length, including label length octets
// and the root byte. It never exceeds MaxNameWireLength.
func (n *Name) ExpandedLength() int {
	if n == nil {
		return 0
	}
	return len(n.wire)
}

// ExpandedWire returns a copy of the fully expanded wire bytes.
func (n *Name) ExpandedWire() []byte {
	if n == nil {
		return nil
	}
	return append([]byte(nil), n.wire...)
}

// LabelCount reports the number of non-root labels.
func (n *Name) LabelCount() int {
	if n == nil {
		return 0
	}
	return len(n.labels)
}

// Label returns the raw bytes of one non-root label.
func (n *Name) Label(index int) (string, bool) {
	if n == nil || index < 0 || index >= len(n.labels) {
		return "", false
	}
	span := n.labels[index]
	return string(n.wire[span.payloadStart : span.payloadStart+span.payloadLen]), true
}

// Labels returns copies of the raw non-root label bytes.
func (n *Name) Labels() []string {
	if n == nil {
		return nil
	}
	out := make([]string, len(n.labels))
	for i, span := range n.labels {
		out[i] = string(n.wire[span.payloadStart : span.payloadStart+span.payloadLen])
	}
	return out
}

// SourcePosition maps an index in ExpandedWire to the byte offset in the
// original message that supplied that byte. Length octets and the root byte
// are included.
func (n *Name) SourcePosition(expandedIndex int) (int, bool) {
	if n == nil || expandedIndex < 0 || expandedIndex >= len(n.origins) {
		return 0, false
	}
	return n.origins[expandedIndex], true
}

// SourcePositions returns a copy of the complete expanded-byte origin map.
func (n *Name) SourcePositions() []int {
	if n == nil {
		return nil
	}
	return append([]int(nil), n.origins...)
}

// LabelSourcePosition returns the original offset of a label's length octet.
func (n *Name) LabelSourcePosition(labelIndex int) (int, bool) {
	if n == nil || labelIndex < 0 || labelIndex >= len(n.labels) {
		return 0, false
	}
	return n.origins[n.labels[labelIndex].wireStart], true
}

// LabelByteSourcePosition returns the original offset of one payload byte in
// one label.
func (n *Name) LabelByteSourcePosition(labelIndex, byteIndex int) (int, bool) {
	if n == nil || labelIndex < 0 || labelIndex >= len(n.labels) {
		return 0, false
	}
	span := n.labels[labelIndex]
	if byteIndex < 0 || byteIndex >= span.payloadLen {
		return 0, false
	}
	return n.origins[span.payloadStart+byteIndex], true
}

// Callback consumes the one immutable Name produced for this request.
type Callback func(*Name) error

// Callbacks are invoked with the exact same *Name. Business runs only after a
// successful decode and a successful security check.
type Callbacks struct {
	Check    Callback
	Business Callback
}

// Process decodes once from a fresh message and runs the callbacks.
func Process(message []byte, offset int, callbacks Callbacks) error {
	return NewMessage(message).Process(offset, callbacks)
}

// Process decodes once, runs Check, and then runs Business only if decoding
// and the check succeeded. A rejected input therefore makes zero business
// calls.
func (m *Message) Process(offset int, callbacks Callbacks) error {
	if callbacks.Check == nil {
		return ErrMissingCheck
	}
	if callbacks.Business == nil {
		return ErrMissingBusiness
	}

	name, err := m.DecodeName(offset)
	if err != nil {
		return err
	}
	if err := callbacks.Check(name); err != nil {
		return err
	}
	return callbacks.Business(name)
}
