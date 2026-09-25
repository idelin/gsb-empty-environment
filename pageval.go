package rootslot

import (
	"encoding/binary"
	"fmt"
)

// PageReader 由调用方（页管理器）实现，按页 id 读页。
type PageReader interface {
	ReadPage(id uint64) ([]byte, error)
}

// PageValidator 是一个示例 Validator：根描述符为 8 字节大端根页 id；
// 页格式为 [0:8) 子页数量，随后是子页 id 列表。它从根页出发做 BFS，
// 要求可达闭包内每一页都完整可读，否则拒绝发布。
type PageValidator struct {
	Pages PageReader
}

func (v *PageValidator) ValidateReachable(root []byte) error {
	if len(root) != 8 {
		return fmt.Errorf("pageval: root descriptor must be 8 bytes, got %d", len(root))
	}
	rootID := binary.BigEndian.Uint64(root)
	seen := map[uint64]bool{}
	queue := []uint64{rootID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		page, err := v.Pages.ReadPage(id)
		if err != nil {
			return fmt.Errorf("pageval: page %d not readable: %w", id, err)
		}
		if len(page) < 8 {
			return fmt.Errorf("pageval: page %d truncated header", id)
		}
		n := binary.BigEndian.Uint64(page[0:8])
		if uint64(len(page)) < 8+n*8 {
			return fmt.Errorf("pageval: page %d truncated child list", id)
		}
		for i := uint64(0); i < n; i++ {
			queue = append(queue, binary.BigEndian.Uint64(page[8+i*8:16+i*8]))
		}
	}
	return nil
}

// MemPages 是一个简单的内存 PageReader，便于测试与演示。
type MemPages map[uint64][]byte

func (m MemPages) ReadPage(id uint64) ([]byte, error) {
	p, ok := m[id]
	if !ok {
		return nil, fmt.Errorf("page %d missing", id)
	}
	return p, nil
}

// LeafPage 构造一个无子页的页。
func LeafPage() []byte { return make([]byte, 8) }

// InternalPage 构造一个带子页指针的页。
func InternalPage(children ...uint64) []byte {
	p := make([]byte, 8+8*len(children))
	binary.BigEndian.PutUint64(p[0:8], uint64(len(children)))
	for i, c := range children {
		binary.BigEndian.PutUint64(p[8+8*i:16+8*i], c)
	}
	return p
}

// RootDescriptor 把根页 id 编码为根描述符。
func RootDescriptor(id uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, id)
	return b
}
