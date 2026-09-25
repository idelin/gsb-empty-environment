package diskbtree

import (
	"bytes"
	"encoding/binary"
	"sort"
)

const (
	pageTypeLeaf     byte = 1
	pageTypeInternal byte = 2

	// 页头: [0]类型 [1:3]条目数 [3:8]保留
	// 叶子: [8]hasNext, 若 hasNext 则随后 u16 长度 + nextKey 字节
	headerSize = 16
)

// page 是 B 树节点在内存中的表示。磁盘上的页一旦提交就不可变(COW)。
//
// 叶链采用逻辑指针: 叶子记录后继叶子的首键(nextKey)而非页号。
// 因为 COW 会改变页号, 页号链无法在不级联修改前驱的情况下维护;
// 而分裂时左半页保留原首键, 前驱的 nextKey 永远有效, 且旧快照
// 的链完全不受新写入影响。
type page struct {
	typ      byte
	keys     [][]byte
	vals     [][]byte // 仅叶子
	children []uint64 // 仅内部节点, len = len(keys)+1
	hasNext  bool     // 仅叶子: 是否存在右兄弟
	nextKey  []byte   // 仅叶子: 右兄弟的首键
}

func (p *page) size() int {
	if p.typ == pageTypeLeaf {
		n := headerSize
		if p.hasNext {
			n += 2 + len(p.nextKey)
		}
		for i := range p.keys {
			n += 4 + len(p.keys[i]) + len(p.vals[i])
		}
		return n
	}
	n := headerSize + 8*len(p.children)
	for _, k := range p.keys {
		n += 2 + len(k)
	}
	return n
}

func (p *page) encode(buf []byte) error {
	if p.size() > len(buf) {
		return errPageOverflow
	}
	for i := range buf {
		buf[i] = 0
	}
	buf[0] = p.typ
	binary.LittleEndian.PutUint16(buf[1:3], uint16(len(p.keys)))
	if p.typ == pageTypeLeaf {
		off := headerSize
		if p.hasNext {
			buf[8] = 1
			binary.LittleEndian.PutUint16(buf[off:off+2], uint16(len(p.nextKey)))
			off += 2
			copy(buf[off:], p.nextKey)
			off += len(p.nextKey)
		}
		for i := range p.keys {
			binary.LittleEndian.PutUint16(buf[off:off+2], uint16(len(p.keys[i])))
			binary.LittleEndian.PutUint16(buf[off+2:off+4], uint16(len(p.vals[i])))
			off += 4
			copy(buf[off:], p.keys[i])
			off += len(p.keys[i])
			copy(buf[off:], p.vals[i])
			off += len(p.vals[i])
		}
		return nil
	}
	off := headerSize
	for _, c := range p.children {
		binary.LittleEndian.PutUint64(buf[off:off+8], c)
		off += 8
	}
	for _, k := range p.keys {
		binary.LittleEndian.PutUint16(buf[off:off+2], uint16(len(k)))
		off += 2
		copy(buf[off:], k)
		off += len(k)
	}
	return nil
}

func decodePage(buf []byte) (*page, error) {
	if len(buf) < headerSize {
		return nil, errCorrupt
	}
	p := &page{typ: buf[0]}
	n := int(binary.LittleEndian.Uint16(buf[1:3]))
	switch p.typ {
	case pageTypeLeaf:
		off := headerSize
		if buf[8] == 1 {
			if off+2 > len(buf) {
				return nil, errCorrupt
			}
			kl := int(binary.LittleEndian.Uint16(buf[off : off+2]))
			off += 2
			if off+kl > len(buf) {
				return nil, errCorrupt
			}
			p.hasNext = true
			p.nextKey = append([]byte(nil), buf[off:off+kl]...)
			off += kl
		}
		for i := 0; i < n; i++ {
			if off+4 > len(buf) {
				return nil, errCorrupt
			}
			kl := int(binary.LittleEndian.Uint16(buf[off : off+2]))
			vl := int(binary.LittleEndian.Uint16(buf[off+2 : off+4]))
			off += 4
			if off+kl+vl > len(buf) {
				return nil, errCorrupt
			}
			p.keys = append(p.keys, append([]byte(nil), buf[off:off+kl]...))
			off += kl
			p.vals = append(p.vals, append([]byte(nil), buf[off:off+vl]...))
			off += vl
		}
	case pageTypeInternal:
		off := headerSize
		if off+8*(n+1) > len(buf) {
			return nil, errCorrupt
		}
		p.children = make([]uint64, n+1)
		for i := range p.children {
			p.children[i] = binary.LittleEndian.Uint64(buf[off : off+8])
			off += 8
		}
		for i := 0; i < n; i++ {
			if off+2 > len(buf) {
				return nil, errCorrupt
			}
			kl := int(binary.LittleEndian.Uint16(buf[off : off+2]))
			off += 2
			if off+kl > len(buf) {
				return nil, errCorrupt
			}
			p.keys = append(p.keys, append([]byte(nil), buf[off:off+kl]...))
			off += kl
		}
	default:
		return nil, errCorrupt
	}
	return p, nil
}

// childIndex 返回 key 应该落入的子节点下标。
func (p *page) childIndex(key []byte) int {
	return sort.Search(len(p.keys), func(i int) bool {
		return bytes.Compare(key, p.keys[i]) < 0
	})
}

// lowerBound 返回叶子中第一个 >= key 的下标。
func (p *page) lowerBound(key []byte) int {
	return sort.Search(len(p.keys), func(i int) bool {
		return bytes.Compare(p.keys[i], key) >= 0
	})
}
