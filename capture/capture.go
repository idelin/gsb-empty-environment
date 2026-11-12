package capture

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Reader abstracts the target address space. Implementations must be
// safe for concurrent use with the agent's own threads, and Version
// must change whenever any byte reachable through it may have changed
// (a seqlock-style counter bumped by the mutator, a ptrace stop
// generation, etc.).
type Reader interface {
	// ReadAt copies len(buf) bytes starting at addr.
	ReadAt(addr uint64, buf []byte) error
	// Version returns a counter that changes whenever the address
	// space may have changed.
	Version() uint64
}

// Budget bounds one capture. A zero field means "no limit", except
// MaxRetries where zero means a version change fails immediately.
// Byte, node and depth limits are charged per attempt; retries start
// with a fresh allowance.
type Budget struct {
	MaxBytes   int // total bytes read from the target
	MaxNodes   int // total nodes captured
	MaxDepth   int // pointer-follow nesting depth
	MaxRetries int // whole-capture retries after a version change
}

var (
	ErrByteBudget  = errors.New("capture: byte budget exceeded")
	ErrNodeBudget  = errors.New("capture: node budget exceeded")
	ErrDepthBudget = errors.New("capture: depth budget exceeded")
	ErrRetries     = errors.New("capture: retries exhausted, target keeps changing")
	ErrBadSchema   = errors.New("capture: invalid schema")
	ErrOverflow    = errors.New("capture: address computation overflow")
)

// Capture copies the value at addr, laid out as s, out of r and returns
// the root of an immutable snapshot graph.
//
// The whole graph is captured against one version of the target: if
// r.Version() moves during the walk, the partial graph is discarded and
// the capture restarts from scratch, up to b.MaxRetries times. Budget
// and read errors are not retried.
func Capture(r Reader, addr uint64, s Schema, b Budget) (*Node, error) {
	for attempt := 0; ; attempt++ {
		v0 := r.Version()
		c := &capturer{r: r, b: b, shared: make(map[uint64]*Node)}
		root, err := c.pointee(addr, s, 0)
		if err != nil {
			return nil, err
		}
		if r.Version() == v0 {
			return root, nil
		}
		if attempt >= b.MaxRetries {
			return nil, ErrRetries
		}
	}
}

type capturer struct {
	r      Reader
	b      Budget
	shared map[uint64]*Node // address -> node captured (or being captured) there
	bytes  int
	nodes  int
}

// pointee captures a pointer target (or the root) at addr, or returns
// the node already captured there. The shell is registered before it
// is filled so that cyclic pointers link back instead of recursing
// forever, and repeated pointers to one address share a single capture.
func (c *capturer) pointee(addr uint64, s Schema, depth int) (*Node, error) {
	if n, ok := c.shared[addr]; ok {
		return n, nil
	}
	if err := c.guard(depth); err != nil {
		return nil, err
	}
	n := &Node{addr: addr}
	c.shared[addr] = n
	c.nodes++
	if err := c.fill(n, addr, s, depth); err != nil {
		return nil, err
	}
	return n, nil
}

// at captures the value at addr into a fresh node. Inline values
// (struct fields, pointer slots) are captured this way; only pointer
// targets deduplicate, through pointee.
func (c *capturer) at(addr uint64, s Schema, depth int) (*Node, error) {
	if err := c.guard(depth); err != nil {
		return nil, err
	}
	n := &Node{addr: addr}
	c.nodes++
	if err := c.fill(n, addr, s, depth); err != nil {
		return nil, err
	}
	return n, nil
}

func (c *capturer) guard(depth int) error {
	if c.b.MaxDepth > 0 && depth > c.b.MaxDepth {
		return ErrDepthBudget
	}
	if c.b.MaxNodes > 0 && c.nodes >= c.b.MaxNodes {
		return ErrNodeBudget
	}
	return nil
}

func (c *capturer) fill(n *Node, addr uint64, s Schema, depth int) error {
	switch t := s.(type) {
	case Ref:
		if t.S == nil {
			return fmt.Errorf("%w: Ref with nil target", ErrBadSchema)
		}
		return c.fill(n, addr, *t.S, depth)

	case Uint64:
		var buf [8]byte
		if err := c.read(addr, buf[:]); err != nil {
			return err
		}
		n.kind = KindUint
		n.u64 = binary.LittleEndian.Uint64(buf[:])

	case CString:
		if t.Max <= 0 {
			return fmt.Errorf("%w: CString.Max must be positive", ErrBadSchema)
		}
		data, err := c.readCString(addr, t.Max)
		if err != nil {
			return err
		}
		n.kind = KindBytes
		n.bytes = data

	case Blob:
		if t.Len < 0 {
			return fmt.Errorf("%w: Blob.Len must be non-negative", ErrBadSchema)
		}
		data := make([]byte, t.Len)
		if err := c.read(addr, data); err != nil {
			return err
		}
		n.kind = KindBytes
		n.bytes = data

	case Ptr:
		var buf [8]byte
		if err := c.read(addr, buf[:]); err != nil {
			return err
		}
		n.kind = KindPtr
		if target := binary.LittleEndian.Uint64(buf[:]); target != 0 {
			elem, err := c.pointee(target, t.Elem, depth+1)
			if err != nil {
				return err
			}
			n.elem = elem
		}

	case Struct:
		n.kind = KindStruct
		for _, f := range t.Fields {
			child, err := c.at(addr+f.Off, f.Type, depth+1)
			if err != nil {
				return err
			}
			n.fields = append(n.fields, fieldNode{f.Name, child})
		}

	case Counted:
		var hdr [16]byte
		if err := c.read(addr, hdr[:]); err != nil {
			return err
		}
		count := binary.LittleEndian.Uint64(hdr[0:8])
		base := binary.LittleEndian.Uint64(hdr[8:16])
		size, ok := StaticSize(t.Elem)
		if !ok {
			return fmt.Errorf("%w: Counted element has no static size", ErrBadSchema)
		}
		if count > 0 && base == 0 {
			return fmt.Errorf("%w: Counted with %d elements at null base", ErrBadSchema, count)
		}
		if size > 0 && count > (math.MaxUint64-base)/size {
			return ErrOverflow
		}
		n.kind = KindSlice
		for i := uint64(0); i < count; i++ {
			elem, err := c.pointee(base+i*size, t.Elem, depth+1)
			if err != nil {
				return err
			}
			n.elems = append(n.elems, elem)
		}

	default:
		return fmt.Errorf("%w: unknown schema type %T", ErrBadSchema, s)
	}
	return nil
}

func (c *capturer) read(addr uint64, buf []byte) error {
	if c.b.MaxBytes > 0 && c.bytes+len(buf) > c.b.MaxBytes {
		return ErrByteBudget
	}
	if err := c.r.ReadAt(addr, buf); err != nil {
		return err
	}
	c.bytes += len(buf)
	return nil
}

// readCString reads in 64-byte chunks so a short string does not force
// reads far past its terminator into possibly unmapped memory.
func (c *capturer) readCString(addr uint64, max int) ([]byte, error) {
	var out []byte
	for len(out) < max {
		n := min(64, max-len(out))
		buf := make([]byte, n)
		if err := c.read(addr+uint64(len(out)), buf); err != nil {
			return nil, err
		}
		if i := bytes.IndexByte(buf, 0); i >= 0 {
			return append(out, buf[:i]...), nil
		}
		out = append(out, buf...)
	}
	return out, nil
}
