package btree

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"sort"
)

// Node page layout (pageSize bytes):
//
//	[0:4]   magic "BTND"
//	[4]     type: nodeLeaf | nodeInternal
//	[5:7]   reserved
//	[7:9]   numKeys uint16
//	[9:13]  next leaf node ID (leaf only, nilID if none)
//	[13:17] node ID (self, for validation)
//	[17:]   cells
//	[P-4:]  crc32 IEEE of [0:P-4]
//
// Leaf cell:     klen uint16 | vlen uint32 | key | value
// Internal page: child0 uint32, then per key: klen uint16 | key | child uint32
//
// Internal node invariant: keys[i] == minimum key of subtree children[i+1].
const (
	nodeLeaf     byte = 1
	nodeInternal byte = 2

	nilID uint32 = 0

	nodeMagic   uint32 = 0x444E5442 // "BTND" little-endian
	nodeHdrSize        = 17
	nodeCRCSize        = 4
)

type node struct {
	id       uint32
	typ      byte
	keys     [][]byte
	vals     [][]byte // leaf only
	children []uint32 // internal only, len == len(keys)+1
	next     uint32   // leaf only
}

func (n *node) isLeaf() bool { return n.typ == nodeLeaf }

func (n *node) size() int {
	sz := nodeHdrSize + nodeCRCSize
	if n.isLeaf() {
		for i, k := range n.keys {
			sz += 6 + len(k) + len(n.vals[i])
		}
	} else {
		sz += 4
		for _, k := range n.keys {
			sz += 6 + len(k)
		}
	}
	return sz
}

func (n *node) encode(pageSize int) ([]byte, error) {
	if !n.isLeaf() && len(n.children) != len(n.keys)+1 {
		return nil, ErrCorrupt
	}
	if n.size() > pageSize {
		return nil, ErrValueTooLarge
	}
	buf := make([]byte, pageSize)
	binary.LittleEndian.PutUint32(buf[0:4], nodeMagic)
	buf[4] = n.typ
	binary.LittleEndian.PutUint16(buf[7:9], uint16(len(n.keys)))
	binary.LittleEndian.PutUint32(buf[9:13], n.next)
	binary.LittleEndian.PutUint32(buf[13:17], n.id)
	off := nodeHdrSize
	if n.isLeaf() {
		for i, k := range n.keys {
			v := n.vals[i]
			binary.LittleEndian.PutUint16(buf[off:], uint16(len(k)))
			off += 2
			binary.LittleEndian.PutUint32(buf[off:], uint32(len(v)))
			off += 4
			copy(buf[off:], k)
			off += len(k)
			copy(buf[off:], v)
			off += len(v)
		}
	} else {
		binary.LittleEndian.PutUint32(buf[off:], n.children[0])
		off += 4
		for i, k := range n.keys {
			binary.LittleEndian.PutUint16(buf[off:], uint16(len(k)))
			off += 2
			copy(buf[off:], k)
			off += len(k)
			binary.LittleEndian.PutUint32(buf[off:], n.children[i+1])
			off += 4
		}
	}
	crc := crc32.ChecksumIEEE(buf[:pageSize-nodeCRCSize])
	binary.LittleEndian.PutUint32(buf[pageSize-nodeCRCSize:], crc)
	return buf, nil
}

// cursor is a bounds-checked decoder over a page body (CRC trailer excluded).
type cursor struct {
	buf []byte
	off int
	ok  bool
}

func (c *cursor) take(n int) []byte {
	limit := len(c.buf) - nodeCRCSize
	if !c.ok || n < 0 || c.off+n > limit {
		c.ok = false
		return nil
	}
	b := c.buf[c.off : c.off+n]
	c.off += n
	return b
}

func (c *cursor) u16() int {
	b := c.take(2)
	if !c.ok {
		return 0
	}
	return int(binary.LittleEndian.Uint16(b))
}

func (c *cursor) u32() uint32 {
	b := c.take(4)
	if !c.ok {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

func decodeNode(buf []byte) (*node, error) {
	if len(buf) < nodeHdrSize+nodeCRCSize {
		return nil, ErrCorrupt
	}
	ps := len(buf)
	if binary.LittleEndian.Uint32(buf[0:4]) != nodeMagic {
		return nil, ErrCorrupt
	}
	want := binary.LittleEndian.Uint32(buf[ps-nodeCRCSize:])
	if crc32.ChecksumIEEE(buf[:ps-nodeCRCSize]) != want {
		return nil, ErrCorrupt
	}
	n := &node{typ: buf[4]}
	numKeys := int(binary.LittleEndian.Uint16(buf[7:9]))
	n.next = binary.LittleEndian.Uint32(buf[9:13])
	n.id = binary.LittleEndian.Uint32(buf[13:17])
	c := &cursor{buf: buf, off: nodeHdrSize, ok: true}
	switch n.typ {
	case nodeLeaf:
		for i := 0; i < numKeys; i++ {
			klen := c.u16()
			vlen := c.u32()
			k := append([]byte(nil), c.take(klen)...)
			v := append([]byte(nil), c.take(int(vlen))...)
			if !c.ok {
				return nil, ErrCorrupt
			}
			n.keys = append(n.keys, k)
			n.vals = append(n.vals, v)
		}
	case nodeInternal:
		c0 := c.u32()
		if !c.ok {
			return nil, ErrCorrupt
		}
		n.children = append(n.children, c0)
		for i := 0; i < numKeys; i++ {
			klen := c.u16()
			k := append([]byte(nil), c.take(klen)...)
			child := c.u32()
			if !c.ok {
				return nil, ErrCorrupt
			}
			n.keys = append(n.keys, k)
			n.children = append(n.children, child)
		}
	default:
		return nil, ErrCorrupt
	}
	return n, nil
}

// childIndex returns the index of the child subtree that may contain key.
// Invariant: keys[i] == min(children[i+1]).
func (n *node) childIndex(key []byte) int {
	return sort.Search(len(n.keys), func(i int) bool {
		return bytes.Compare(key, n.keys[i]) < 0
	})
}

// lowerBound returns the first index i with keys[i] >= key.
func lowerBound(keys [][]byte, key []byte) int {
	return sort.Search(len(keys), func(i int) bool {
		return bytes.Compare(keys[i], key) >= 0
	})
}

func insertBytes(s [][]byte, i int, v []byte) [][]byte {
	s = append(s, nil)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func insertUint32(s []uint32, i int, v uint32) []uint32 {
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}
