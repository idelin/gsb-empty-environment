package netsec

import (
	"errors"
	"fmt"
)

// Marshal 4.8 type codes accepted by the gate.
const (
	typeNil     = '0'
	typeTrue    = 'T'
	typeFalse   = 'F'
	typeFixnum  = 'i'
	typeString  = '"'
	typeSymbol  = ':'
	typeSymlink = ';'
	typeArray   = '['
	typeHash    = '{'
	typeIvar    = 'I'
	typeLink    = '@'
	typeObject  = 'o'
)

var (
	ErrBadHeader       = errors.New("netsec: bad marshal header, want 0x04 0x08")
	ErrTruncated       = errors.New("netsec: truncated marshal stream")
	ErrDisallowedType  = errors.New("netsec: disallowed type code")
	ErrClassNotAllowed = errors.New("netsec: object class not in whitelist")
	ErrBadLink         = errors.New("netsec: link references an index not yet seen")
	ErrDepthExceeded   = errors.New("netsec: nesting depth exceeds budget")
	ErrTooManyElements = errors.New("netsec: element count exceeds budget")
	ErrTrailingData    = errors.New("netsec: trailing bytes after top-level value")
)

// Policy configures a Validate call. Zero MaxDepth / MaxElements fall back
// to the defaults from DefaultPolicy.
type Policy struct {
	// AllowedClasses lists class names permitted for 'o' (user object)
	// type codes. Any other class is rejected.
	AllowedClasses []string
	// MaxDepth bounds recursive nesting of arrays, hashes, ivars and objects.
	MaxDepth int
	// MaxElements bounds the total number of values parsed in the stream.
	MaxElements int
}

// DefaultPolicy returns a Policy with conservative budgets and no allowed
// object classes.
func DefaultPolicy() Policy {
	return Policy{MaxDepth: 64, MaxElements: 4096}
}

// Validate walks a Ruby Marshal 4.8 stream and enforces the gate policy.
// It never instantiates any object; it only consumes bytes. stream must
// contain exactly one top-level value.
func Validate(stream []byte, p Policy) error {
	if p.MaxDepth <= 0 {
		p.MaxDepth = DefaultPolicy().MaxDepth
	}
	if p.MaxElements <= 0 {
		p.MaxElements = DefaultPolicy().MaxElements
	}
	if len(stream) < 2 || stream[0] != 0x04 || stream[1] != 0x08 {
		return ErrBadHeader
	}
	g := &gate{
		data:        stream,
		pos:         2,
		allowed:     make(map[string]struct{}, len(p.AllowedClasses)),
		maxDepth:    p.MaxDepth,
		maxElements: p.MaxElements,
	}
	for _, name := range p.AllowedClasses {
		g.allowed[name] = struct{}{}
	}
	if err := g.value(1); err != nil {
		return err
	}
	if g.pos != len(g.data) {
		return ErrTrailingData
	}
	return nil
}

type gate struct {
	data        []byte
	pos         int
	allowed     map[string]struct{}
	maxDepth    int
	maxElements int
	elements    int
	symbols     []string
	objects     int
}

func (g *gate) byte() (byte, error) {
	if g.pos >= len(g.data) {
		return 0, ErrTruncated
	}
	b := g.data[g.pos]
	g.pos++
	return b, nil
}

// fixnum consumes the variable-length integer encoding used after 'i' and
// for all length prefixes.
func (g *gate) fixnum() (int64, error) {
	c, err := g.byte()
	if err != nil {
		return 0, err
	}
	sc := int8(c)
	switch {
	case sc == 0:
		return 0, nil
	case sc > 4:
		return int64(sc) - 5, nil
	case sc < -4:
		return int64(sc) + 5, nil
	case sc > 0:
		var v int64
		for i := int8(0); i < sc; i++ {
			b, err := g.byte()
			if err != nil {
				return 0, err
			}
			v |= int64(b) << (8 * uint(i))
		}
		return v, nil
	default:
		n := -sc
		var v int64
		for i := int8(0); i < n; i++ {
			b, err := g.byte()
			if err != nil {
				return 0, err
			}
			v |= int64(b) << (8 * uint(i))
		}
		if n < 8 {
			v -= 1 << (8 * uint(n))
		}
		return v, nil
	}
}

// length reads a non-negative length prefix bounded by the element budget.
func (g *gate) length() (int, error) {
	n, err := g.fixnum()
	if err != nil {
		return 0, err
	}
	if n < 0 || n > int64(g.maxElements) {
		return 0, ErrTooManyElements
	}
	return int(n), nil
}

func (g *gate) rawBytes(n int) ([]byte, error) {
	if n < 0 || len(g.data)-g.pos < n {
		return nil, ErrTruncated
	}
	b := g.data[g.pos : g.pos+n]
	g.pos += n
	return b, nil
}

func (g *gate) value(depth int) error {
	if depth > g.maxDepth {
		return ErrDepthExceeded
	}
	g.elements++
	if g.elements > g.maxElements {
		return ErrTooManyElements
	}
	t, err := g.byte()
	if err != nil {
		return err
	}
	switch t {
	case typeNil, typeTrue, typeFalse:
		return nil
	case typeFixnum:
		_, err := g.fixnum()
		return err
	case typeString:
		n, err := g.length()
		if err != nil {
			return err
		}
		if _, err := g.rawBytes(n); err != nil {
			return err
		}
		g.objects++
		return nil
	case typeSymbol:
		_, err := g.symbol()
		return err
	case typeSymlink:
		_, err := g.symlink()
		return err
	case typeLink:
		n, err := g.fixnum()
		if err != nil {
			return err
		}
		if n < 0 || n >= int64(g.objects) {
			return ErrBadLink
		}
		return nil
	case typeArray:
		n, err := g.length()
		if err != nil {
			return err
		}
		g.objects++
		for i := 0; i < n; i++ {
			if err := g.value(depth + 1); err != nil {
				return err
			}
		}
		return nil
	case typeHash:
		n, err := g.length()
		if err != nil {
			return err
		}
		g.objects++
		for i := 0; i < 2*n; i++ {
			if err := g.value(depth + 1); err != nil {
				return err
			}
		}
		return nil
	case typeIvar:
		if err := g.value(depth + 1); err != nil {
			return err
		}
		n, err := g.length()
		if err != nil {
			return err
		}
		for i := 0; i < 2*n; i++ {
			if err := g.value(depth + 1); err != nil {
				return err
			}
		}
		return nil
	case typeObject:
		name, err := g.className()
		if err != nil {
			return err
		}
		if _, ok := g.allowed[name]; !ok {
			return fmt.Errorf("%w: %q", ErrClassNotAllowed, name)
		}
		g.objects++
		n, err := g.length()
		if err != nil {
			return err
		}
		for i := 0; i < 2*n; i++ {
			if err := g.value(depth + 1); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: 0x%02x", ErrDisallowedType, t)
	}
}

// symbol consumes a ':' symbol literal and registers it in the symbol table.
func (g *gate) symbol() (string, error) {
	n, err := g.length()
	if err != nil {
		return "", err
	}
	b, err := g.rawBytes(n)
	if err != nil {
		return "", err
	}
	name := string(b)
	g.symbols = append(g.symbols, name)
	return name, nil
}

// symlink consumes a ';' reference into the symbol table.
func (g *gate) symlink() (string, error) {
	n, err := g.fixnum()
	if err != nil {
		return "", err
	}
	if n < 0 || n >= int64(len(g.symbols)) {
		return "", ErrBadLink
	}
	return g.symbols[n], nil
}

// className reads the class name of an 'o' object: either an inline symbol
// or a symlink to a previously seen symbol.
func (g *gate) className() (string, error) {
	t, err := g.byte()
	if err != nil {
		return "", err
	}
	switch t {
	case typeSymbol:
		return g.symbol()
	case typeSymlink:
		return g.symlink()
	default:
		return "", fmt.Errorf("%w: expected class name symbol, got 0x%02x", ErrDisallowedType, t)
	}
}
