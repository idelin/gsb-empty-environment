package graphdec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var magic = [4]byte{'O', 'G', 'D', '1'}

// node is the staged, pre-publication representation of a graph node.
// Staged nodes are never exposed to callers.
type node struct {
	kind Kind
	b    bool
	i    int64
	str  string
	byts []byte
	refs []uint64 // child ids; for KindMap, alternating key,value
}

type reader struct {
	buf []byte
	off int
}

var errTruncated = errors.New("graphdec: truncated input")

func (r *reader) remaining() int { return len(r.buf) - r.off }

func (r *reader) uvarint() (uint64, error) {
	v, n := binary.Uvarint(r.buf[r.off:])
	if n <= 0 {
		return 0, errTruncated
	}
	r.off += n
	return v, nil
}

func (r *reader) varint() (int64, error) {
	v, n := binary.Varint(r.buf[r.off:])
	if n <= 0 {
		return 0, errTruncated
	}
	r.off += n
	return v, nil
}

func (r *reader) byte() (byte, error) {
	if r.remaining() < 1 {
		return 0, errTruncated
	}
	b := r.buf[r.off]
	r.off++
	return b, nil
}

// take validates n against the remaining input before allocating.
func (r *reader) take(n uint64) ([]byte, error) {
	if n > uint64(r.remaining()) {
		return nil, fmt.Errorf("graphdec: declared length %d exceeds %d remaining bytes", n, r.remaining())
	}
	out := make([]byte, n)
	copy(out, r.buf[r.off:r.off+int(n)])
	r.off += int(n)
	return out, nil
}

func decode(data []byte, lim Limits) (*Value, error) {
	if len(data) > lim.MaxBytes {
		return nil, fmt.Errorf("graphdec: input of %d bytes exceeds byte budget %d", len(data), lim.MaxBytes)
	}
	r := &reader{buf: data}
	for i, want := range magic {
		b, err := r.byte()
		if err != nil {
			return nil, err
		}
		if b != want {
			return nil, fmt.Errorf("graphdec: bad magic at byte %d", i)
		}
	}
	rootID, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	nodeCount, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	// Validate the declared count against the byte budget before
	// allocating: every node costs at least 2 bytes (id + kind).
	if nodeCount > uint64(lim.MaxNodes) {
		return nil, fmt.Errorf("graphdec: declared %d nodes exceeds node budget %d", nodeCount, lim.MaxNodes)
	}
	if nodeCount > uint64(r.remaining()/2)+1 && nodeCount > 0 {
		return nil, fmt.Errorf("graphdec: declared %d nodes cannot fit in %d remaining bytes", nodeCount, r.remaining())
	}

	nodes := make([]*node, 0, nodeCount)
	ids := make([]uint64, 0, nodeCount)
	index := make(map[uint64]int, nodeCount)
	pending := make(map[uint64]struct{}) // staged forward references
	var refCount uint64

	addRef := func(id uint64) error {
		refCount++
		if refCount > uint64(lim.MaxRefs) {
			return fmt.Errorf("graphdec: reference budget %d exceeded", lim.MaxRefs)
		}
		if _, declared := index[id]; declared {
			return nil
		}
		if _, ok := pending[id]; !ok {
			if len(pending) >= lim.MaxPendingRefs {
				return fmt.Errorf("graphdec: pending forward-reference budget %d exceeded", lim.MaxPendingRefs)
			}
			pending[id] = struct{}{}
		}
		return nil
	}

	for i := uint64(0); i < nodeCount; i++ {
		id, err := r.uvarint()
		if err != nil {
			return nil, err
		}
		if _, dup := index[id]; dup {
			return nil, fmt.Errorf("graphdec: duplicate node id %d", id)
		}
		kindByte, err := r.byte()
		if err != nil {
			return nil, err
		}
		nd := &node{kind: Kind(kindByte)}
		if !nd.kind.valid() {
			return nil, fmt.Errorf("graphdec: node %d has unknown kind %d", id, kindByte)
		}
		switch nd.kind {
		case KindNull:
		case KindBool:
			b, err := r.byte()
			if err != nil {
				return nil, err
			}
			if b > 1 {
				return nil, fmt.Errorf("graphdec: node %d has invalid bool value %d", id, b)
			}
			nd.b = b == 1
		case KindInt:
			v, err := r.varint()
			if err != nil {
				return nil, err
			}
			nd.i = v
		case KindString, KindBytes:
			n, err := r.uvarint()
			if err != nil {
				return nil, err
			}
			raw, err := r.take(n)
			if err != nil {
				return nil, fmt.Errorf("graphdec: node %d: %w", id, err)
			}
			if nd.kind == KindString {
				nd.str = string(raw)
			} else {
				nd.byts = raw
			}
		case KindList, KindMap:
			n, err := r.uvarint()
			if err != nil {
				return nil, err
			}
			// Validate before allocating: each reference id costs at
			// least one byte, and every reference draws on the budget.
			if n > uint64(r.remaining()) {
				return nil, fmt.Errorf("graphdec: node %d declares %d children with only %d bytes left", id, n, r.remaining())
			}
			if n > uint64(lim.MaxRefs)-refCount {
				return nil, fmt.Errorf("graphdec: node %d declares %d children, reference budget %d would be exceeded", id, n, lim.MaxRefs)
			}
			total := n
			if nd.kind == KindMap {
				total = 2 * n
				if total > uint64(r.remaining()) {
					return nil, fmt.Errorf("graphdec: node %d declares %d map entries with only %d bytes left", id, n, r.remaining())
				}
				if total > uint64(lim.MaxRefs)-refCount {
					return nil, fmt.Errorf("graphdec: node %d declares %d map entries, reference budget %d would be exceeded", id, n, lim.MaxRefs)
				}
			}
			nd.refs = make([]uint64, 0, total)
			for j := uint64(0); j < total; j++ {
				child, err := r.uvarint()
				if err != nil {
					return nil, err
				}
				if err := addRef(child); err != nil {
					return nil, err
				}
				nd.refs = append(nd.refs, child)
			}
		}
		index[id] = len(nodes)
		delete(pending, id)
		ids = append(ids, id)
		nodes = append(nodes, nd)
	}

	if len(pending) > 0 {
		return nil, fmt.Errorf("graphdec: %d unresolved forward references", len(pending))
	}
	if r.remaining() != 0 {
		return nil, fmt.Errorf("graphdec: %d trailing bytes after last node", r.remaining())
	}
	rootIdx, ok := index[rootID]
	if !ok {
		return nil, fmt.Errorf("graphdec: root id %d is not declared", rootID)
	}

	// Independent structural analysis: resolve references, check
	// reachability, depth (SCC condensation) and cycle quota.
	edges, err := analyze(nodes, ids, rootIdx, lim)
	if err != nil {
		return nil, err
	}

	// All validation passed: publish the graph atomically.
	values := make([]*Value, len(nodes))
	for i := range values {
		values[i] = &Value{}
	}
	for i, nd := range nodes {
		v := values[i]
		v.Kind = nd.kind
		switch nd.kind {
		case KindBool:
			v.Bool = nd.b
		case KindInt:
			v.Int = nd.i
		case KindString:
			v.Str = nd.str
		case KindBytes:
			v.Bytes = nd.byts
		case KindList:
			v.List = make([]*Value, len(edges[i]))
			for j, t := range edges[i] {
				v.List[j] = values[t]
			}
		case KindMap:
			v.Map = make([]MapEntry, len(edges[i])/2)
			for j := range v.Map {
				v.Map[j] = MapEntry{Key: values[edges[i][2*j]], Value: values[edges[i][2*j+1]]}
			}
		}
	}
	return values[rootIdx], nil
}
