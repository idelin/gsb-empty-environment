package netsec

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by ValidateMarshal48. Callers should match with
// errors.Is and must not rely on the exact message text.
var (
	ErrBadHeader   = errors.New("netsec: invalid marshal 4.8 header")
	ErrTruncated   = errors.New("netsec: truncated marshal stream")
	ErrBadType     = errors.New("netsec: disallowed type code")
	ErrBadLink     = errors.New("netsec: link references an index that does not exist yet")
	ErrClassDenied = errors.New("netsec: object class is not in the caller whitelist")
	ErrDepth       = errors.New("netsec: nesting depth exceeds budget")
	ErrElements    = errors.New("netsec: element count exceeds budget")
	ErrTrailing    = errors.New("netsec: trailing bytes after top-level value")
)

// Budget defaults applied when Options leaves a field at zero.
const (
	DefaultMaxDepth    = 64
	DefaultMaxElements = 100_000
)

// Options configures ValidateMarshal48.
type Options struct {
	// AllowedClasses lists class names permitted for 'o' (user object)
	// records. An empty list rejects every 'o' record.
	AllowedClasses []string
	// MaxDepth bounds recursive nesting. Zero selects DefaultMaxDepth.
	MaxDepth int
	// MaxElements bounds the total number of parsed values. Zero selects
	// DefaultMaxElements.
	MaxElements int
}

// ValidateMarshal48 verifies that stream is a well-formed Ruby Marshal 4.8
// byte stream containing only safe, non-instantiating value kinds. It walks
// the stream without ever constructing Ruby objects: no class is resolved,
// no marshal_load/_load hook can fire, and no gadget can be triggered.
//
// Allowed type codes: 0 (nil), T, F, i (fixnum), '"' (string), ':' (symbol),
// ';' (symbol link), '[' (array), '{' (hash), 'I' (ivar wrapper), '@'
// (object link), and 'o' (object) whose class name appears in
// Options.AllowedClasses. Everything else — u, U, e, C, d, S, M, c, m, f,
// l, '/', '}' and unknown codes — is rejected.
func ValidateMarshal48(stream []byte, opts Options) error {
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	maxElements := opts.MaxElements
	if maxElements <= 0 {
		maxElements = DefaultMaxElements
	}
	allowed := make(map[string]struct{}, len(opts.AllowedClasses))
	for _, name := range opts.AllowedClasses {
		allowed[name] = struct{}{}
	}

	p := &parser{
		buf:         stream,
		allowed:     allowed,
		maxDepth:    maxDepth,
		maxElements: maxElements,
	}
	if err := p.header(); err != nil {
		return err
	}
	if err := p.value(0); err != nil {
		return err
	}
	if p.off != len(p.buf) {
		return fmt.Errorf("%w (%d extra bytes)", ErrTrailing, len(p.buf)-p.off)
	}
	return nil
}

type parser struct {
	buf []byte
	off int

	symbols  []string // marshal symbol table, indexed by ';' links
	objects  int      // count of registered objects, indexed by '@' links
	elements int      // total parsed values, bounded by maxElements

	maxDepth    int
	maxElements int
	allowed     map[string]struct{}
}

func (p *parser) header() error {
	if len(p.buf) < 2 || p.buf[0] != 0x04 || p.buf[1] != 0x08 {
		return ErrBadHeader
	}
	p.off = 2
	return nil
}

func (p *parser) byte() (byte, error) {
	if p.off >= len(p.buf) {
		return 0, ErrTruncated
	}
	b := p.buf[p.off]
	p.off++
	return b, nil
}

// fixnum decodes the marshal variable-length integer encoding.
func (p *parser) fixnum() (int64, error) {
	c, err := p.byte()
	if err != nil {
		return 0, err
	}
	switch {
	case c == 0:
		return 0, nil
	case c < 0x80:
		if c > 4 {
			return int64(c) - 5, nil
		}
		// c in 1..4: little-endian positive integer in c bytes.
		var v int64
		for i := 0; i < int(c); i++ {
			b, err := p.byte()
			if err != nil {
				return 0, err
			}
			v |= int64(b) << (8 * i)
		}
		return v, nil
	default:
		sc := int8(c) // -128..-1
		if sc < -4 {
			return int64(sc) + 5, nil
		}
		// sc in -4..-1: little-endian negative integer in -sc bytes.
		v := int64(-1)
		for i := 0; i < int(-sc); i++ {
			b, err := p.byte()
			if err != nil {
				return 0, err
			}
			v &^= 0xff << (8 * i)
			v |= int64(b) << (8 * i)
		}
		return v, nil
	}
}

// length reads a fixnum that must be a non-negative count fitting in the
// remaining stream.
func (p *parser) length() (int, error) {
	n, err := p.fixnum()
	if err != nil {
		return 0, err
	}
	if n < 0 || n > int64(len(p.buf)-p.off) {
		return 0, fmt.Errorf("%w: invalid length %d", ErrTruncated, n)
	}
	return int(n), nil
}

// value parses one value at the given nesting depth.
func (p *parser) value(depth int) error {
	if depth > p.maxDepth {
		return fmt.Errorf("%w (limit %d)", ErrDepth, p.maxDepth)
	}
	p.elements++
	if p.elements > p.maxElements {
		return fmt.Errorf("%w (limit %d)", ErrElements, p.maxElements)
	}
	t, err := p.byte()
	if err != nil {
		return err
	}
	switch t {
	case '0', 'T', 'F': // nil, true, false
		return nil
	case 'i': // fixnum
		_, err := p.fixnum()
		return err
	case '"': // string: registered before its bytes are consumed
		p.objects++
		n, err := p.length()
		if err != nil {
			return err
		}
		p.off += n
		return nil
	case ':': // symbol: appended to the symbol table
		n, err := p.length()
		if err != nil {
			return err
		}
		p.symbols = append(p.symbols, string(p.buf[p.off:p.off+n]))
		p.off += n
		return nil
	case ';': // symbol link: must reference an already-seen symbol
		idx, err := p.fixnum()
		if err != nil {
			return err
		}
		if idx < 0 || idx >= int64(len(p.symbols)) {
			return fmt.Errorf("%w: symbol link %d", ErrBadLink, idx)
		}
		return nil
	case '[': // array: registered before its elements
		p.objects++
		n, err := p.length()
		if err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			if err := p.value(depth + 1); err != nil {
				return err
			}
		}
		return nil
	case '{': // hash: registered before its key/value pairs
		p.objects++
		n, err := p.length()
		if err != nil {
			return err
		}
		for i := 0; i < 2*n; i++ {
			if err := p.value(depth + 1); err != nil {
				return err
			}
		}
		return nil
	case 'I': // ivar wrapper: the inner value owns the object-table slot
		if err := p.value(depth + 1); err != nil {
			return err
		}
		n, err := p.length()
		if err != nil {
			return err
		}
		for i := 0; i < 2*n; i++ {
			if err := p.value(depth + 1); err != nil {
				return err
			}
		}
		return nil
	case '@': // object link: must reference an already-registered object
		idx, err := p.fixnum()
		if err != nil {
			return err
		}
		if idx < 0 || idx >= int64(p.objects) {
			return fmt.Errorf("%w: object link %d", ErrBadLink, idx)
		}
		return nil
	case 'o': // user object: class name must be whitelisted
		name, err := p.className()
		if err != nil {
			return err
		}
		if _, ok := p.allowed[name]; !ok {
			return fmt.Errorf("%w: %q", ErrClassDenied, name)
		}
		p.objects++
		n, err := p.length()
		if err != nil {
			return err
		}
		for i := 0; i < 2*n; i++ {
			if err := p.value(depth + 1); err != nil {
				return err
			}
		}
		return nil
	default:
		// u, U, e, C, d, S, M, c, m, f, l, '/', '}' and anything unknown.
		return fmt.Errorf("%w: 0x%02x (%q)", ErrBadType, t, string(rune(t)))
	}
}

// className reads the class-name symbol that follows an 'o' type code. It
// accepts a literal symbol or a link to a previously seen symbol.
func (p *parser) className() (string, error) {
	t, err := p.byte()
	if err != nil {
		return "", err
	}
	switch t {
	case ':':
		n, err := p.length()
		if err != nil {
			return "", err
		}
		name := string(p.buf[p.off : p.off+n])
		p.off += n
		p.symbols = append(p.symbols, name)
		return name, nil
	case ';':
		idx, err := p.fixnum()
		if err != nil {
			return "", err
		}
		if idx < 0 || idx >= int64(len(p.symbols)) {
			return "", fmt.Errorf("%w: symbol link %d", ErrBadLink, idx)
		}
		return p.symbols[idx], nil
	default:
		return "", fmt.Errorf("%w: class name starts with 0x%02x", ErrBadType, t)
	}
}
