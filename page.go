package bptree

import (
	"encoding/binary"
	"hash/crc32"
)

// PageID identifies a page in the Store. Page 0 is reserved as the null page.
type PageID uint64

const (
	pageMagic    uint32 = 0x42505431 // "BPT1"
	pageLeaf     uint8  = 1
	pageInternal uint8  = 2
	headerSize          = 24
	crcSize             = 4
)

var crc32c = crc32.MakeTable(crc32.Castagnoli)

// node is the in-memory form of a B+tree node. Pages in the store are
// immutable: a write transaction always encodes modified nodes into freshly
// allocated pages (copy-on-write), so a decoded node never changes underneath
// a reader.
//
// Leaves deliberately have no sibling links. In a copy-on-write tree a leaf
// link cannot be maintained without either mutating pinned old pages or
// cascading copies to every leaf on the left; worse, a stale link can point
// at a page since recycled by a newer version. Iterators therefore navigate
// between leaves through the pinned version's internal pages (path stack),
// which can never wander into a different version of the tree.
type node struct {
	id       PageID
	leaf     bool
	keys     []int64
	vals     [][]byte // leaf only, parallel to keys
	children []PageID // internal only, len == len(keys)+1
}

// Page layout (little endian):
//   0  magic      uint32
//   4  page id    uint64
//   12 type       uint8  (1=leaf, 2=internal)
//   13 reserved   uint8
//   14 key count  uint16
//   16 reserved   uint64
//   24 payload
//      leaf:     [key int64][vlen uint16][value bytes] * count
//      internal: [child0 uint64] then [key int64][child uint64] * count
//   pageSize-4 crc32 (Castagnoli) over all preceding bytes

// pageSizeFor derives the store page size from the tree options so that a
// node of either kind holding `capacity` keys always fits in one page.
func pageSizeFor(capacity, maxValueSize int) int {
	leaf := headerSize + capacity*(8+2+maxValueSize) + crcSize
	internal := headerSize + 8 + capacity*16 + crcSize
	sz := leaf
	if internal > sz {
		sz = internal
	}
	const align = 64
	if sz%align != 0 {
		sz += align - sz%align
	}
	return sz
}

func (n *node) encode(pageSize int) []byte {
	buf := make([]byte, pageSize)
	binary.LittleEndian.PutUint32(buf[0:], pageMagic)
	binary.LittleEndian.PutUint64(buf[4:], uint64(n.id))
	if n.leaf {
		buf[12] = pageLeaf
	} else {
		buf[12] = pageInternal
	}
	binary.LittleEndian.PutUint16(buf[14:], uint16(len(n.keys)))
	off := headerSize
	if n.leaf {
		for i, k := range n.keys {
			binary.LittleEndian.PutUint64(buf[off:], uint64(k))
			off += 8
			binary.LittleEndian.PutUint16(buf[off:], uint16(len(n.vals[i])))
			off += 2
			copy(buf[off:], n.vals[i])
			off += len(n.vals[i])
		}
	} else {
		binary.LittleEndian.PutUint64(buf[off:], uint64(n.children[0]))
		off += 8
		for i, k := range n.keys {
			binary.LittleEndian.PutUint64(buf[off:], uint64(k))
			off += 8
			binary.LittleEndian.PutUint64(buf[off:], uint64(n.children[i+1]))
			off += 8
		}
	}
	crc := crc32.Checksum(buf[:pageSize-crcSize], crc32c)
	binary.LittleEndian.PutUint32(buf[pageSize-crcSize:], crc)
	return buf
}

func decodeNode(buf []byte) (*node, error) {
	pageSize := len(buf)
	if pageSize < headerSize+crcSize {
		return nil, ErrCorrupt
	}
	if binary.LittleEndian.Uint32(buf[0:]) != pageMagic {
		return nil, ErrCorrupt
	}
	want := binary.LittleEndian.Uint32(buf[pageSize-crcSize:])
	got := crc32.Checksum(buf[:pageSize-crcSize], crc32c)
	if want != got {
		return nil, ErrChecksum
	}
	n := &node{id: PageID(binary.LittleEndian.Uint64(buf[4:]))}
	count := int(binary.LittleEndian.Uint16(buf[14:]))
	off := headerSize
	switch buf[12] {
	case pageLeaf:
		n.leaf = true
		n.keys = make([]int64, 0, count)
		n.vals = make([][]byte, 0, count)
		for i := 0; i < count; i++ {
			if off+10 > pageSize-crcSize {
				return nil, ErrCorrupt
			}
			k := int64(binary.LittleEndian.Uint64(buf[off:]))
			off += 8
			vl := int(binary.LittleEndian.Uint16(buf[off:]))
			off += 2
			if off+vl > pageSize-crcSize {
				return nil, ErrCorrupt
			}
			v := make([]byte, vl)
			copy(v, buf[off:off+vl])
			off += vl
			n.keys = append(n.keys, k)
			n.vals = append(n.vals, v)
		}
	case pageInternal:
		n.keys = make([]int64, 0, count)
		n.children = make([]PageID, 0, count+1)
		if off+8 > pageSize-crcSize {
			return nil, ErrCorrupt
		}
		n.children = append(n.children, PageID(binary.LittleEndian.Uint64(buf[off:])))
		off += 8
		for i := 0; i < count; i++ {
			if off+16 > pageSize-crcSize {
				return nil, ErrCorrupt
			}
			k := int64(binary.LittleEndian.Uint64(buf[off:]))
			off += 8
			c := PageID(binary.LittleEndian.Uint64(buf[off:]))
			off += 8
			n.keys = append(n.keys, k)
			n.children = append(n.children, c)
		}
	default:
		return nil, ErrCorrupt
	}
	return n, nil
}
