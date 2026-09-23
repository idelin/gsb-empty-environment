package objectgraph

import (
	"encoding/binary"
	"math"
)

// Additional structural errors.
var (
	errOverflow  = constErr("objectgraph: varint overflows 64 bits")
	errTrailing  = constErr("objectgraph: trailing bytes after final record")
	errMalformed = constErr("objectgraph: malformed graph")
)

type constErr string

func (e constErr) Error() string { return string(e) }

// Decoder is safe for concurrent use and keeps no per-call state. A call that
// fails because a limit was reached does not consume or "poison" any quota:
// subsequent Decode calls get the full configured Limits again.
type Decoder struct {
	limits Limits
}

// NewDecoder validates limits and returns a reusable decoder.
func NewDecoder(limits Limits) (*Decoder, error) {
	if !limits.valid() {
		return nil, ErrBadLimits
	}
	return &Decoder{limits: limits}, nil
}

// Decode decodes with DefaultLimits.
func Decode(p []byte) (*Value, error) {
	d, err := NewDecoder(DefaultLimits())
	if err != nil {
		return nil, err
	}
	return d.Decode(p)
}

// Limits returns the decoder's configured limits.
func (d *Decoder) Limits() Limits { return d.limits }

// Decode fully validates p before publishing a graph. The published *Value is
// only returned after every reference and structural check has succeeded; on
// any error the result is nil, so partial objects never escape.
func (d *Decoder) Decode(p []byte) (*Value, error) {
	lim := d.limits

	// Bound total bytes first.
	if len(p) > lim.MaxBytes {
		return nil, wrapf(ErrBadLength, "input %d bytes exceeds MaxBytes %d", len(p), lim.MaxBytes)
	}
	c := &cur{buf: p}

	// Header.
	if len(p) < 4 || p[0] != magic0 || p[1] != magic1 || p[2] != magic2 {
		return nil, ErrTruncated
	}
	if p[3] != version1 {
		return nil, wrapf(ErrBadVersion, "got version %d", p[3])
	}
	c.pos = 4

	nodeCountU, err := c.uvarint()
	if err != nil {
		return nil, err
	}
	rootU, err := c.uvarint()
	if err != nil {
		return nil, err
	}
	if nodeCountU < 1 || nodeCountU > uint64(lim.MaxNodes) || nodeCountU > math.MaxInt32 {
		return nil, wrapf(ErrNodeLimit, "node count %d (limit %d)", nodeCountU, lim.MaxNodes)
	}
	nodeCount := int(nodeCountU)
	if rootU >= nodeCountU {
		return nil, wrapf(ErrRootIndex, "root %d with %d nodes", rootU, nodeCount)
	}
	root := int(rootU)

	// Allocate the node table only after nodeCount has been validated.
	raw := make([]rawNode, nodeCount)
	// pending[t] = number of already-seen edges pointing forward to node t.
	pending := make([]int, nodeCount)
	openPending := 0
	refCount := 0

	for i := 0; i < nodeCount; i++ {
		// Any forward edges staged for this record are now resolvable.
		openPending -= pending[i]
		if openPending < 0 {
			return nil, errMalformed
		}

		tag, err := c.byte()
		if err != nil {
			return nil, err
		}
		rn := &raw[i]
		rn.tag = tag
		switch tag {
		case tagNull:
		case tagBool:
			v, err := c.byte()
			if err != nil {
				return nil, err
			}
			if v > 1 {
				return nil, wrapf(errMalformed, "node %d: bool byte %d", i, v)
			}
			rn.boolV = v == 1
		case tagInt:
			v, err := c.varint()
			if err != nil {
				return nil, err
			}
			rn.intV = v
		case tagFloat:
			b, err := c.bytes(8)
			if err != nil {
				return nil, err
			}
			rn.floatV = math.Float64frombits(binary.BigEndian.Uint64(b))
		case tagString:
			s, err := c.text()
			if err != nil {
				return nil, err
			}
			rn.strV = s
		case tagArray, tagObject:
			lengthU, err := c.uvarint()
			if err != nil {
				return nil, err
			}
			if lengthU > uint64(lim.MaxContainerLen) {
				return nil, wrapf(ErrBadLength, "node %d container length %d exceeds %d", i, lengthU, lim.MaxContainerLen)
			}
			if lengthU > uint64(lim.MaxRefs-refCount) {
				return nil, wrapf(ErrRefLimit, "node %d length %d would exceed reference budget", i, lengthU)
			}
			length := int(lengthU)
			// Backing slice is allocated only after the length passed both
			// the container cap and the remaining reference budget.
			rn.edges = make([]int, 0, length)
			if tag == tagObject {
				rn.keys = make([]string, 0, length)
			}
			for e := 0; e < length; e++ {
				if tag == tagObject {
					key, err := c.text()
					if err != nil {
						return nil, err
					}
					rn.keys = append(rn.keys, key)
				}
				targetU, err := c.uvarint()
				if err != nil {
					return nil, err
				}
				if targetU >= uint64(nodeCount) {
					return nil, wrapf(ErrBadRef, "node %d edge %d -> %d (nodes=%d)", i, e, targetU, nodeCount)
				}
				target := int(targetU)
				rn.edges = append(rn.edges, target)
				refCount++
				if target > i {
					// Target record has not been parsed yet: stage it.
					if openPending >= lim.MaxPendingRefs {
						return nil, wrapf(ErrPendingRefs, "node %d staged edge -> %d (cap %d)", i, target, lim.MaxPendingRefs)
					}
					pending[target]++
					openPending++
				}
			}
		default:
			return nil, wrapf(ErrBadTag, "node %d tag %d", i, tag)
		}
	}

	if c.pos != len(p) {
		return nil, wrapf(errTrailing, "%d unconsumed bytes", len(p)-c.pos)
	}
	if openPending != 0 {
		// Defensive: with valid indices every staged ref is resolved above.
		return nil, ErrDanglingRef
	}

	// Independent analysis: reachability, SCC condensation depth and the
	// separate cyclic-node quota. This runs before any Value is built.
	adj := make([][]int, nodeCount)
	for i := range raw {
		adj[i] = raw[i].edges
	}
	stats, err := AnalyzeGraph(adj, root)
	if err != nil {
		return nil, err
	}
	if len(stats.Unreachable) != 0 {
		return nil, wrapf(ErrUnreachable, "%d of %d nodes unreachable from root", len(stats.Unreachable), nodeCount)
	}
	if stats.Depth > lim.MaxDepth {
		return nil, wrapf(ErrDepth, "depth %d exceeds MaxDepth %d", stats.Depth, lim.MaxDepth)
	}
	if stats.CyclicNodes > lim.MaxCyclicNodes {
		return nil, wrapf(ErrCyclicNodes, "%d cyclic nodes exceeds quota %d", stats.CyclicNodes, lim.MaxCyclicNodes)
	}

	// All validation passed: materialize atomically. Shared nodes get exactly
	// one *Value entity; edges only hold pointers.
	return materialize(raw, root), nil
}

type rawNode struct {
	tag    byte
	boolV  bool
	intV   int64
	floatV float64
	strV   string
	edges  []int
	keys   []string
}

func materialize(raw []rawNode, root int) *Value {
	vals := make([]*Value, len(raw))
	for i := range raw {
		rn := &raw[i]
		v := &Value{}
		switch rn.tag {
		case tagNull:
			v.kind = KindNull
		case tagBool:
			v.kind, v.boolV = KindBool, rn.boolV
		case tagInt:
			v.kind, v.intV = KindInt, rn.intV
		case tagFloat:
			v.kind, v.floatV = KindFloat, rn.floatV
		case tagString:
			v.kind, v.strV = KindString, rn.strV
		case tagArray:
			v.kind = KindArray
			v.elems = make([]*Value, len(rn.edges))
		case tagObject:
			v.kind = KindObject
			v.fields = make([]Field, len(rn.edges))
		}
		vals[i] = v
	}
	for i := range raw {
		rn := &raw[i]
		switch rn.tag {
		case tagArray:
			for e, t := range rn.edges {
				vals[i].elems[e] = vals[t]
			}
		case tagObject:
			for e, t := range rn.edges {
				vals[i].fields[e] = Field{Key: rn.keys[e], Value: vals[t]}
			}
		}
	}
	return vals[root]
}

// cur is a bounds-checked cursor over the input. Every read validates length
// against the remaining bytes before consuming them.
type cur struct {
	buf []byte
	pos int
}

func (c *cur) byte() (byte, error) {
	if c.pos >= len(c.buf) {
		return 0, ErrTruncated
	}
	v := c.buf[c.pos]
	c.pos++
	return v, nil
}

func (c *cur) bytes(n int) ([]byte, error) {
	if n < 0 || c.pos > len(c.buf)-n {
		return nil, ErrTruncated
	}
	b := c.buf[c.pos : c.pos+n]
	c.pos += n
	return b, nil
}

func (c *cur) uvarint() (uint64, error) {
	v, k := binary.Uvarint(c.buf[c.pos:])
	if k == 0 {
		return 0, ErrTruncated
	}
	if k < 0 {
		return 0, errOverflow
	}
	c.pos += k
	return v, nil
}

func (c *cur) varint() (int64, error) {
	v, k := binary.Varint(c.buf[c.pos:])
	if k == 0 {
		return 0, ErrTruncated
	}
	if k < 0 {
		return 0, errOverflow
	}
	c.pos += k
	return v, nil
}

func (c *cur) text() (string, error) {
	nU, err := c.uvarint()
	if err != nil {
		return "", err
	}
	if nU > uint64(math.MaxInt32) {
		return "", wrapf(ErrBadLength, "string length %d", nU)
	}
	// Length is checked against remaining input bytes before allocation.
	b, err := c.bytes(int(nU))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
