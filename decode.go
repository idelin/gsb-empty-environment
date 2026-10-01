package canon

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

// maxDecodeDepth bounds nesting of arrays/objects (and graph labels) so
// hostile inputs cannot exhaust the stack.
const maxDecodeDepth = 512

// Decode parses a single value from data, which must be consumed in full.
// It only constructs data; it never executes host code. Every length
// prefix is checked against the remaining input before any allocation
// proportional to it is made. Decoding accepts any well-formed encoding;
// re-encoding a decoded value yields its canonical form.
func Decode(data []byte) (Value, error) {
	p := &parser{b: data}
	v, err := p.value(0)
	if err != nil {
		return Value{}, err
	}
	if p.off != len(p.b) {
		return Value{}, fmt.Errorf("canon: %d trailing bytes after value", len(p.b)-p.off)
	}
	return v, nil
}

type parser struct {
	b   []byte
	off int
}

var errTruncated = errors.New("canon: unexpected end of input")

func (p *parser) remaining() int { return len(p.b) - p.off }

func (p *parser) tag() (byte, error) {
	if p.remaining() < 1 {
		return 0, errTruncated
	}
	c := p.b[p.off]
	p.off++
	return c, nil
}

func (p *parser) uvarint() (uint64, error) {
	v, n := binary.Uvarint(p.b[p.off:])
	if n <= 0 {
		return 0, errTruncated
	}
	p.off += n
	return v, nil
}

// take returns the next n bytes, validating n against the remaining
// input before any allocation proportional to n happens.
func (p *parser) take(n uint64) ([]byte, error) {
	if n > uint64(p.remaining()) {
		return nil, fmt.Errorf("canon: declared length %d exceeds remaining input %d", n, p.remaining())
	}
	out := p.b[p.off : p.off+int(n)]
	p.off += int(n)
	return out, nil
}

// count validates a declared element count. Every element needs at least
// one byte, so count can never exceed the remaining input.
func (p *parser) count() (uint64, error) {
	n, err := p.uvarint()
	if err != nil {
		return 0, err
	}
	if n > uint64(p.remaining()) {
		return 0, fmt.Errorf("canon: declared count %d exceeds remaining input %d", n, p.remaining())
	}
	return n, nil
}

func (p *parser) value(depth int) (Value, error) {
	if depth > maxDecodeDepth {
		return Value{}, errors.New("canon: nesting depth limit exceeded")
	}
	t, err := p.tag()
	if err != nil {
		return Value{}, err
	}
	switch t {
	case tagNull:
		return Null(), nil
	case tagBool:
		b, err := p.tag()
		if err != nil {
			return Value{}, err
		}
		if b > 1 {
			return Value{}, fmt.Errorf("canon: invalid bool byte %d", b)
		}
		return Bool(b == 1), nil
	case tagInt:
		raw, err := p.take(8)
		if err != nil {
			return Value{}, err
		}
		return Int(int64(binary.BigEndian.Uint64(raw))), nil
	case tagFloat:
		raw, err := p.take(8)
		if err != nil {
			return Value{}, err
		}
		f := math.Float64frombits(binary.BigEndian.Uint64(raw))
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return Value{}, errors.New("canon: non-finite float in input")
		}
		if f == 0 {
			f = 0 // normalize -0.0 to +0.0
		}
		return Float(f), nil
	case tagString:
		n, err := p.uvarint()
		if err != nil {
			return Value{}, err
		}
		raw, err := p.take(n)
		if err != nil {
			return Value{}, err
		}
		if !utf8.Valid(raw) {
			return Value{}, errors.New("canon: string is not valid UTF-8")
		}
		return String(string(raw)), nil
	case tagArray:
		n, err := p.count()
		if err != nil {
			return Value{}, err
		}
		elems := make([]Value, 0, n)
		for i := uint64(0); i < n; i++ {
			e, err := p.value(depth + 1)
			if err != nil {
				return Value{}, err
			}
			elems = append(elems, e)
		}
		return Array(elems...), nil
	case tagObject:
		n, err := p.count()
		if err != nil {
			return Value{}, err
		}
		pairs := make([]Pair, 0, n)
		seen := make(map[string]struct{}, n)
		for i := uint64(0); i < n; i++ {
			klen, err := p.uvarint()
			if err != nil {
				return Value{}, err
			}
			raw, err := p.take(klen)
			if err != nil {
				return Value{}, err
			}
			if !utf8.Valid(raw) {
				return Value{}, errors.New("canon: object key is not valid UTF-8")
			}
			key := string(raw)
			if _, dup := seen[key]; dup {
				return Value{}, fmt.Errorf("canon: duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			val, err := p.value(depth + 1)
			if err != nil {
				return Value{}, err
			}
			pairs = append(pairs, Pair{Key: key, Value: val})
		}
		return Object(pairs...), nil
	case tagGraph:
		return p.graph(depth)
	default:
		return Value{}, fmt.Errorf("canon: unknown tag 0x%02x", t)
	}
}

func (p *parser) graph(depth int) (Value, error) {
	n, err := p.count()
	if err != nil {
		return Value{}, err
	}
	if n == 0 {
		return Value{}, errors.New("canon: graph encoding has zero nodes")
	}
	root, err := p.uvarint()
	if err != nil {
		return Value{}, err
	}
	if root >= n {
		return Value{}, fmt.Errorf("canon: graph root index %d out of range %d", root, n)
	}
	nodes := make([]*Node, n)
	adjs := make([][]int, n)
	for i := uint64(0); i < n; i++ {
		label, err := p.value(depth + 1)
		if err != nil {
			return Value{}, err
		}
		if containsGraph(label) {
			return Value{}, errNestedGraph
		}
		deg, err := p.count()
		if err != nil {
			return Value{}, err
		}
		adj := make([]int, 0, deg)
		for j := uint64(0); j < deg; j++ {
			t, err := p.uvarint()
			if err != nil {
				return Value{}, err
			}
			if t >= n {
				return Value{}, fmt.Errorf("canon: edge target %d out of range %d", t, n)
			}
			adj = append(adj, int(t))
		}
		nodes[i] = &Node{Label: label}
		adjs[i] = adj
	}
	for i := range nodes {
		edges := make([]*Node, len(adjs[i]))
		for j, t := range adjs[i] {
			edges[j] = nodes[t]
		}
		nodes[i].Edges = edges
	}
	return GraphValue(nodes[root]), nil
}
