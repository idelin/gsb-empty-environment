package bptree

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

const (
	// PageSize is the fixed on-disk page size in bytes.
	PageSize = 4096

	// MaxValueSize is the maximum length of a value in bytes.
	MaxValueSize = 128

	metaMagic uint64 = 0x42504c5554524545 // "BPLUTREE"
	pageMagic uint16 = 0x4250

	pageTypeFree     = 0
	pageTypeLeaf     = 1
	pageTypeInternal = 2

	headerSize = 32
)

var (
	// ErrCorrupt is returned when a page fails magic or checksum validation.
	ErrCorrupt = errors.New("bptree: corrupt page")
	// ErrNotFound is returned by Get when the key does not exist.
	ErrNotFound = errors.New("bptree: key not found")
	// ErrValueTooLarge is returned when a value exceeds MaxValueSize.
	ErrValueTooLarge = errors.New("bptree: value exceeds MaxValueSize")
	// ErrSnapshotClosed is returned when an iterator or snapshot is used
	// after its snapshot has been closed.
	ErrSnapshotClosed = errors.New("bptree: snapshot closed")
	// ErrClosed is returned when the DB is used after Close.
	ErrClosed = errors.New("bptree: db closed")
	// ErrInvalidCapacity is returned for an out-of-range capacity parameter.
	ErrInvalidCapacity = errors.New("bptree: invalid capacity")
)

// MaxCapacity returns the largest page capacity (max keys per node) that
// fits a page given MaxValueSize.
func MaxCapacity() int {
	return (PageSize - headerSize - 8) / (8 + 2 + MaxValueSize)
}

func checkCapacity(capacity int) error {
	if capacity < 2 || capacity > MaxCapacity() {
		return fmt.Errorf("%w: %d (valid range 2..%d)", ErrInvalidCapacity, capacity, MaxCapacity())
	}
	return nil
}

// node is the in-memory form of a B+ tree node. Nodes loaded from the pager
// are immutable; write transactions clone them before modifying (COW).
type node struct {
	id      uint64
	version uint64 // commit sequence that created this page
	leaf    bool
	keys    []int64
	values  [][]byte // leaf only, len == len(keys)
	child   []uint64 // internal only, len == len(keys)+1
	next    uint64   // leaf only: id of the next leaf, 0 if none
}

func (n *node) clone() *node {
	c := &node{
		id:      n.id,
		version: n.version,
		leaf:    n.leaf,
		next:    n.next,
	}
	c.keys = append([]int64(nil), n.keys...)
	if n.leaf {
		c.values = make([][]byte, len(n.values))
		for i, v := range n.values {
			c.values[i] = append([]byte(nil), v...)
		}
	} else {
		c.child = append([]uint64(nil), n.child...)
	}
	return c
}

// encode serializes the node into a full page with header and checksum.
func (n *node) encode() []byte {
	buf := make([]byte, PageSize)
	binary.LittleEndian.PutUint16(buf[0:2], pageMagic)
	if n.leaf {
		buf[2] = pageTypeLeaf
	} else {
		buf[2] = pageTypeInternal
	}
	binary.LittleEndian.PutUint64(buf[4:12], n.id)
	binary.LittleEndian.PutUint64(buf[12:20], n.version)
	binary.LittleEndian.PutUint16(buf[20:22], uint16(len(n.keys)))
	off := headerSize
	if n.leaf {
		binary.LittleEndian.PutUint64(buf[off:off+8], n.next)
		off += 8
		for i, k := range n.keys {
			binary.LittleEndian.PutUint64(buf[off:off+8], uint64(k))
			off += 8
			binary.LittleEndian.PutUint16(buf[off:off+2], uint16(len(n.values[i])))
			off += 2
			copy(buf[off:], n.values[i])
			off += len(n.values[i])
		}
	} else {
		for _, k := range n.keys {
			binary.LittleEndian.PutUint64(buf[off:off+8], uint64(k))
			off += 8
		}
		for _, c := range n.child {
			binary.LittleEndian.PutUint64(buf[off:off+8], c)
			off += 8
		}
	}
	crc := crc32.NewIEEE()
	crc.Write(buf[0:26])
	crc.Write(buf[30:off])
	binary.LittleEndian.PutUint32(buf[26:30], crc.Sum32())
	return buf
}

// decodeNode parses and validates a raw page into a node.
func decodeNode(buf []byte) (*node, error) {
	if len(buf) != PageSize {
		return nil, fmt.Errorf("%w: bad page size %d", ErrCorrupt, len(buf))
	}
	if binary.LittleEndian.Uint16(buf[0:2]) != pageMagic {
		return nil, fmt.Errorf("%w: bad page magic", ErrCorrupt)
	}
	typ := buf[2]
	if typ != pageTypeLeaf && typ != pageTypeInternal {
		return nil, fmt.Errorf("%w: bad page type %d", ErrCorrupt, typ)
	}
	n := &node{
		id:      binary.LittleEndian.Uint64(buf[4:12]),
		version: binary.LittleEndian.Uint64(buf[12:20]),
		leaf:    typ == pageTypeLeaf,
	}
	nkeys := int(binary.LittleEndian.Uint16(buf[20:22]))
	// Walk the body to determine its length so the checksum can be verified.
	off := headerSize
	bodyEnd := off
	if n.leaf {
		bodyEnd += 8
		for i := 0; i < nkeys; i++ {
			bodyEnd += 8 + 2
			if bodyEnd > PageSize {
				return nil, fmt.Errorf("%w: leaf entries overflow page", ErrCorrupt)
			}
			vlen := int(binary.LittleEndian.Uint16(buf[bodyEnd-2 : bodyEnd]))
			if vlen > MaxValueSize {
				return nil, fmt.Errorf("%w: value length %d too large", ErrCorrupt, vlen)
			}
			bodyEnd += vlen
			if bodyEnd > PageSize {
				return nil, fmt.Errorf("%w: leaf entries overflow page", ErrCorrupt)
			}
		}
	} else {
		bodyEnd += 8*nkeys + 8*(nkeys+1)
		if bodyEnd > PageSize {
			return nil, fmt.Errorf("%w: internal entries overflow page", ErrCorrupt)
		}
	}
	crc := crc32.NewIEEE()
	crc.Write(buf[0:26])
	crc.Write(buf[30:bodyEnd])
	if binary.LittleEndian.Uint32(buf[26:30]) != crc.Sum32() {
		return nil, fmt.Errorf("%w: checksum mismatch", ErrCorrupt)
	}
	if n.leaf {
		n.next = binary.LittleEndian.Uint64(buf[off : off+8])
		off += 8
		n.keys = make([]int64, 0, nkeys)
		n.values = make([][]byte, 0, nkeys)
		for i := 0; i < nkeys; i++ {
			k := int64(binary.LittleEndian.Uint64(buf[off : off+8]))
			off += 8
			vlen := int(binary.LittleEndian.Uint16(buf[off : off+2]))
			off += 2
			v := append([]byte(nil), buf[off:off+vlen]...)
			off += vlen
			n.keys = append(n.keys, k)
			n.values = append(n.values, v)
		}
	} else {
		n.keys = make([]int64, 0, nkeys)
		for i := 0; i < nkeys; i++ {
			n.keys = append(n.keys, int64(binary.LittleEndian.Uint64(buf[off:off+8])))
			off += 8
		}
		n.child = make([]uint64, 0, nkeys+1)
		for i := 0; i < nkeys+1; i++ {
			n.child = append(n.child, binary.LittleEndian.Uint64(buf[off:off+8]))
			off += 8
		}
	}
	return n, nil
}

// encodeFreePage builds a page marked as free.
func encodeFreePage(id uint64) []byte {
	buf := make([]byte, PageSize)
	binary.LittleEndian.PutUint16(buf[0:2], pageMagic)
	buf[2] = pageTypeFree
	binary.LittleEndian.PutUint64(buf[4:12], id)
	crc := crc32.NewIEEE()
	crc.Write(buf[0:26])
	crc.Write(buf[30:headerSize])
	binary.LittleEndian.PutUint32(buf[26:30], crc.Sum32())
	return buf
}

// pageTypeOf reads the type byte of a raw page (no validation).
func pageTypeOf(buf []byte) byte {
	if len(buf) < 3 || binary.LittleEndian.Uint16(buf[0:2]) != pageMagic {
		return 0xFF
	}
	return buf[2]
}
