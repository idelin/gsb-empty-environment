package graphdec

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// Marshal encodes the graph rooted at root into the wire format. Nodes
// are numbered in breadth-first order; shared and cyclic references are
// preserved through pointer identity. It is provided for producers,
// tests, and examples; decoding never depends on it.
func Marshal(root *Value) ([]byte, error) {
	if root == nil {
		return nil, errors.New("graphdec: cannot marshal nil root")
	}
	var order []*Value
	seen := map[*Value]uint64{root: 0}
	queue := []*Value{root}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		order = append(order, v)
		for _, c := range v.children() {
			if c == nil {
				return nil, errors.New("graphdec: cannot marshal nil child")
			}
			if _, ok := seen[c]; !ok {
				seen[c] = uint64(len(seen))
				queue = append(queue, c)
			}
		}
	}

	var buf bytes.Buffer
	buf.Write(magic[:])
	buf.Write(binary.AppendUvarint(nil, seen[root]))
	buf.Write(binary.AppendUvarint(nil, uint64(len(order))))
	for _, v := range order {
		buf.Write(binary.AppendUvarint(nil, seen[v]))
		buf.WriteByte(byte(v.Kind))
		switch v.Kind {
		case KindNull:
		case KindBool:
			if v.Bool {
				buf.WriteByte(1)
			} else {
				buf.WriteByte(0)
			}
		case KindInt:
			buf.Write(binary.AppendVarint(nil, v.Int))
		case KindString:
			buf.Write(binary.AppendUvarint(nil, uint64(len(v.Str))))
			buf.WriteString(v.Str)
		case KindBytes:
			buf.Write(binary.AppendUvarint(nil, uint64(len(v.Bytes))))
			buf.Write(v.Bytes)
		case KindList:
			buf.Write(binary.AppendUvarint(nil, uint64(len(v.List))))
			for _, c := range v.List {
				buf.Write(binary.AppendUvarint(nil, seen[c]))
			}
		case KindMap:
			buf.Write(binary.AppendUvarint(nil, uint64(len(v.Map))))
			for _, e := range v.Map {
				buf.Write(binary.AppendUvarint(nil, seen[e.Key]))
				buf.Write(binary.AppendUvarint(nil, seen[e.Value]))
			}
		default:
			return nil, errors.New("graphdec: cannot marshal unknown kind")
		}
	}
	return buf.Bytes(), nil
}
