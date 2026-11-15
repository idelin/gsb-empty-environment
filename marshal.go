package netsec

import (
	"errors"
	"fmt"
)

const (
	// MarshalMajor and MarshalMinor are the version bytes of the only
	// supported Ruby Marshal stream format.
	MarshalMajor byte = 4
	MarshalMinor byte = 8

	// DefaultMaxDepth is the default maximum nesting depth.
	DefaultMaxDepth = 100
	// DefaultMaxElements is the default maximum number of values (including
	// symbols used as ivar/class names) that may appear in a stream.
	DefaultMaxElements = 100_000
)

var (
	// ErrInvalidHeader reports a stream that does not start with 04 08.
	ErrInvalidHeader = errors.New("netsec: invalid Ruby Marshal 4.8 header")
	// ErrTruncated reports a stream that ends in the middle of a value.
	ErrTruncated = errors.New("netsec: truncated Ruby Marshal stream")
	// ErrTrailingData reports bytes after the single top-level value.
	ErrTrailingData = errors.New("netsec: trailing data after Ruby Marshal value")
	// ErrForbiddenType reports a type code that the gate does not permit.
	ErrForbiddenType = errors.New("netsec: forbidden Ruby Marshal type")
	// ErrClassNotAllowed reports an object whose class is not whitelisted.
	ErrClassNotAllowed = errors.New("netsec: Ruby Marshal class is not allowed")
	// ErrInvalidReference reports a forward or out-of-range symbol/object link.
	ErrInvalidReference = errors.New("netsec: invalid Ruby Marshal link")
	// ErrDepthLimit reports that the nesting budget was exceeded.
	ErrDepthLimit = errors.New("netsec: Ruby Marshal nesting depth exceeded")
	// ErrElementLimit reports that the value budget was exceeded.
	ErrElementLimit = errors.New("netsec: Ruby Marshal element budget exceeded")
	// ErrInvalidFormat reports a structurally invalid Marshal value.
	ErrInvalidFormat = errors.New("netsec: invalid Ruby Marshal data")
)

// Limits bounds the resources a stream may consume while it is inspected.
//
// MaxDepth limits nesting. A single top-level value has depth zero; each
// nested value adds one. MaxElements limits the total number of values and
// symbol references visited. Non-positive values select the defaults.
type Limits struct {
	MaxDepth    int
	MaxElements int
}

func (l Limits) normalized() Limits {
	if l.MaxDepth <= 0 {
		l.MaxDepth = DefaultMaxDepth
	}
	if l.MaxElements <= 0 {
		l.MaxElements = DefaultMaxElements
	}
	return l
}

// Gate validates Ruby Marshal 4.8 streams without loading or instantiating any
// Ruby objects. It is safe for concurrent use after construction.
type Gate struct {
	allowed map[string]struct{}
	limits  Limits
}

// NewGate returns a gate that accepts o objects only for the supplied class
// names. If limits is omitted, DefaultMaxDepth and DefaultMaxElements are used.
func NewGate(allowedClasses []string, limits ...Limits) *Gate {
	allowed := make(map[string]struct{}, len(allowedClasses))
	for _, name := range allowedClasses {
		allowed[name] = struct{}{}
	}

	cfg := Limits{}
	if len(limits) > 0 {
		cfg = limits[0]
	}

	return &Gate{
		allowed: allowed,
		limits:  cfg.normalized(),
	}
}

// NewGateWithLimits is a convenience constructor with explicit budgets.
func NewGateWithLimits(allowedClasses []string, maxDepth, maxElements int) *Gate {
	return NewGate(allowedClasses, Limits{
		MaxDepth:    maxDepth,
		MaxElements: maxElements,
	})
}

// Validate checks that data is a single complete Marshal 4.8 value made only
// from the gate's permitted type codes and classes.
func (g *Gate) Validate(data []byte) error {
	var allowed map[string]struct{}
	cfg := Limits{}
	if g != nil {
		allowed = g.allowed
		cfg = g.limits
	}
	cfg = cfg.normalized()

	if len(data) < 2 || data[0] != MarshalMajor || data[1] != MarshalMinor {
		return ErrInvalidHeader
	}

	p := parser{
		data:        data,
		pos:         2,
		allowed:     allowed,
		maxDepth:    cfg.MaxDepth,
		maxElements: cfg.MaxElements,
	}

	if err := p.value(0); err != nil {
		return err
	}
	if p.pos != len(p.data) {
		return fmt.Errorf("%w at offset %d", ErrTrailingData, p.pos)
	}
	return nil
}

// Check reports whether data passes Validate.
func (g *Gate) Check(data []byte) bool {
	return g.Validate(data) == nil
}

// Validate checks data with a one-off class whitelist. If limits is omitted,
// the default budgets are used.
func Validate(data []byte, allowedClasses []string, limits ...Limits) error {
	return NewGate(allowedClasses, limits...).Validate(data)
}

// ValidateWithLimits checks data with a one-off class whitelist and budgets.
func ValidateWithLimits(data []byte, allowedClasses []string, limits Limits) error {
	return NewGate(allowedClasses, limits).Validate(data)
}

// Check reports whether data passes Validate with a one-off class whitelist.
func Check(data []byte, allowedClasses []string, limits ...Limits) bool {
	return Validate(data, allowedClasses, limits...) == nil
}

// ValidateMarshal is an explicit alias for Validate.
func ValidateMarshal(data []byte, allowedClasses []string, limits ...Limits) error {
	return Validate(data, allowedClasses, limits...)
}

type symbolRef struct {
	offset int
	length int
}

type parser struct {
	data []byte
	pos  int

	allowed     map[string]struct{}
	maxDepth    int
	maxElements int

	elements int
	objects  int
	symbols  []symbolRef
}

func (p *parser) value(depth int) error {
	if depth > p.maxDepth {
		return fmt.Errorf("%w at offset %d (limit %d)", ErrDepthLimit, p.pos, p.maxDepth)
	}
	if err := p.spend(1); err != nil {
		return err
	}

	offset := p.pos
	code, err := p.readByte()
	if err != nil {
		return err
	}

	switch code {
	case '0', 'T', 'F':
		return nil

	case 'i':
		_, err := p.readLong()
		return err

	case '"':
		return p.string()

	case ':':
		_, err := p.symbol()
		return err

	case ';':
		return p.symbolLink()

	case '[':
		return p.array(depth)

	case '{':
		return p.hash(depth)

	case 'I':
		return p.ivar(depth)

	case '@':
		return p.objectLink()

	case 'o':
		return p.object(depth)

	default:
		return fmt.Errorf("%w 0x%02x at offset %d", ErrForbiddenType, code, offset)
	}
}

func (p *parser) string() error {
	length, err := p.length()
	if err != nil {
		return err
	}
	if _, err := p.take(length); err != nil {
		return err
	}

	// Ruby registers strings in the object table after their bytes are read.
	p.objects++
	return nil
}

func (p *parser) symbol() (symbolRef, error) {
	length, err := p.length()
	if err != nil {
		return symbolRef{}, err
	}
	offset, err := p.take(length)
	if err != nil {
		return symbolRef{}, err
	}

	ref := symbolRef{offset: offset, length: int(length)}
	p.symbols = append(p.symbols, ref)
	return ref, nil
}

func (p *parser) symbolLink() error {
	index, err := p.readLong()
	if err != nil {
		return err
	}
	if index < 0 || index >= int64(len(p.symbols)) {
		return fmt.Errorf("%w: symbol index %d at offset %d (have %d)",
			ErrInvalidReference, index, p.pos, len(p.symbols))
	}
	return nil
}

func (p *parser) symbolRef(depth int) (symbolRef, error) {
	if depth > p.maxDepth {
		return symbolRef{}, fmt.Errorf("%w at offset %d (limit %d)",
			ErrDepthLimit, p.pos, p.maxDepth)
	}
	if err := p.spend(1); err != nil {
		return symbolRef{}, err
	}

	offset := p.pos
	code, err := p.readByte()
	if err != nil {
		return symbolRef{}, err
	}

	switch code {
	case ':':
		return p.symbol()
	case ';':
		index, err := p.readLong()
		if err != nil {
			return symbolRef{}, err
		}
		if index < 0 || index >= int64(len(p.symbols)) {
			return symbolRef{}, fmt.Errorf(
				"%w: symbol index %d at offset %d (have %d)",
				ErrInvalidReference, index, p.pos, len(p.symbols))
		}
		return p.symbols[index], nil
	default:
		return symbolRef{}, fmt.Errorf(
			"%w 0x%02x where a symbol was required at offset %d",
			ErrForbiddenType, code, offset)
	}
}

func (p *parser) array(depth int) error {
	length, err := p.length()
	if err != nil {
		return err
	}
	if err := p.reserve(length); err != nil {
		return err
	}

	// Arrays are entered into the object table before their elements, so a
	// self-reference is a legal backward link.
	p.objects++
	for i := int64(0); i < length; i++ {
		if err := p.value(depth + 1); err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) hash(depth int) error {
	length, err := p.length()
	if err != nil {
		return err
	}
	if length > int64(p.maxElements-p.elements)/2 {
		return fmt.Errorf("%w: hash of %d pairs at offset %d",
			ErrElementLimit, length, p.pos)
	}

	p.objects++
	for i := int64(0); i < length; i++ {
		if err := p.value(depth + 1); err != nil {
			return err
		}
		if err := p.value(depth + 1); err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) ivar(depth int) error {
	// I wraps an object and then adds ivars to it. The wrapper itself is not a
	// new object-table entry.
	if err := p.value(depth + 1); err != nil {
		return err
	}
	count, err := p.length()
	if err != nil {
		return err
	}
	if count > int64(p.maxElements-p.elements)/2 {
		return fmt.Errorf("%w: %d ivars at offset %d",
			ErrElementLimit, count, p.pos)
	}

	for i := int64(0); i < count; i++ {
		if _, err := p.symbolRef(depth + 1); err != nil {
			return err
		}
		if err := p.value(depth + 1); err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) object(depth int) error {
	classRef, err := p.symbolRef(depth + 1)
	if err != nil {
		return err
	}
	className := string(p.data[classRef.offset : classRef.offset+classRef.length])
	if _, ok := p.allowed[className]; !ok {
		return fmt.Errorf("%w %q at offset %d",
			ErrClassNotAllowed, className, classRef.offset)
	}

	count, err := p.length()
	if err != nil {
		return err
	}
	if count > int64(p.maxElements-p.elements)/2 {
		return fmt.Errorf("%w: %d object ivars at offset %d",
			ErrElementLimit, count, p.pos)
	}

	// As with arrays, the object is visible to its own ivars.
	p.objects++
	for i := int64(0); i < count; i++ {
		if _, err := p.symbolRef(depth + 1); err != nil {
			return err
		}
		if err := p.value(depth + 1); err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) objectLink() error {
	index, err := p.readLong()
	if err != nil {
		return err
	}
	if index < 0 || index >= int64(p.objects) {
		return fmt.Errorf("%w: object index %d at offset %d (have %d)",
			ErrInvalidReference, index, p.pos, p.objects)
	}
	return nil
}

func (p *parser) spend(n int64) error {
	if n < 0 || n > int64(p.maxElements-p.elements) {
		return fmt.Errorf("%w at offset %d (limit %d)",
			ErrElementLimit, p.pos, p.maxElements)
	}
	p.elements += int(n)
	return nil
}

func (p *parser) reserve(n int64) error {
	if n < 0 || n > int64(p.maxElements-p.elements) {
		return fmt.Errorf("%w: %d elements at offset %d",
			ErrElementLimit, n, p.pos)
	}
	return nil
}

func (p *parser) readByte() (byte, error) {
	if p.pos >= len(p.data) {
		return 0, fmt.Errorf("%w at offset %d", ErrTruncated, p.pos)
	}
	b := p.data[p.pos]
	p.pos++
	return b, nil
}

// readLong decodes the variable-length integer encoding used throughout the
// Marshal format.
func (p *parser) readLong() (int64, error) {
	c, err := p.readByte()
	if err != nil {
		return 0, err
	}
	if c == 0 {
		return 0, nil
	}

	if c >= 5 && c <= 127 {
		return int64(c) - 5, nil
	}
	if c >= 128 && c <= 251 {
		return int64(c) - 256 + 5, nil
	}

	var n int
	negative := false
	if c <= 4 {
		n = int(c)
	} else {
		n = 256 - int(c)
		negative = true
	}

	var value int64
	if negative {
		value = -1
	}
	for i := 0; i < n; i++ {
		b, err := p.readByte()
		if err != nil {
			return 0, err
		}
		shift := uint(8 * i)
		value = (value & ^(int64(0xff) << shift)) | (int64(b) << shift)
	}
	return value, nil
}

func (p *parser) length() (int64, error) {
	n, err := p.readLong()
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: negative length %d at offset %d",
			ErrInvalidFormat, n, p.pos)
	}
	return n, nil
}

func (p *parser) take(n int64) (int, error) {
	if n < 0 || n > int64(len(p.data)-p.pos) {
		return 0, fmt.Errorf("%w: need %d bytes at offset %d (have %d)",
			ErrTruncated, n, p.pos, len(p.data)-p.pos)
	}
	offset := p.pos
	p.pos += int(n)
	return offset, nil
}
