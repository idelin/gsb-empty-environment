package netsec

import (
	"errors"
	"math"
	"sort"
	"sync"
)

// Perm 是 DMA 页权限位。
type Perm uint8

const (
	PermRead  Perm = 1 << iota // 允许设备读该页
	PermWrite                  // 允许设备写该页
)

var (
	ErrBadRange   = errors.New("netsec: invalid or overflowing range")
	ErrUnmapped   = errors.New("netsec: range not covered by registered DMA pages")
	ErrPermission = errors.New("netsec: descriptor permission exceeds underlying page")
	ErrReserved   = errors.New("netsec: range touches a reserved region")
	ErrOverlapReg = errors.New("netsec: DMA page registration overlaps existing one")
)

// region 是半开区间 [base, limit)。
type region struct {
	base  uint64
	limit uint64
	perm  Perm
}

func (r region) contains(addr uint64) bool { return addr >= r.base && addr < r.limit }

// Registry 记录单个虚拟机已登记的 DMA 页与保留区。
// 保留区永远优先：落在保留区内的任何访问一律拒绝。
type Registry struct {
	mu       sync.RWMutex
	pages    []region // 按 base 升序，互不重叠
	reserved []region // 按 base 升序，互不重叠
}

func NewRegistry() *Registry { return &Registry{} }

func normRange(base, size uint64) (region, error) {
	if size == 0 || base > math.MaxUint64-size {
		return region{}, ErrBadRange
	}
	return region{base: base, limit: base + size}, nil
}

func overlaps(rs []region, n region) bool {
	for _, r := range rs {
		if n.base < r.limit && r.base < n.limit {
			return true
		}
	}
	return false
}

// RegisterDMA 登记一段允许 DMA 的客户机物理区间及其权限。
func (r *Registry) RegisterDMA(base, size uint64, perm Perm) error {
	n, err := normRange(base, size)
	if err != nil {
		return err
	}
	n.perm = perm
	r.mu.Lock()
	defer r.mu.Unlock()
	if overlaps(r.pages, n) {
		return ErrOverlapReg
	}
	r.pages = append(r.pages, n)
	sort.Slice(r.pages, func(i, j int) bool { return r.pages[i].base < r.pages[j].base })
	return nil
}

// Reserve 登记保留区；保留区内的地址对 DMA 永远不可达。
func (r *Registry) Reserve(base, size uint64) error {
	n, err := normRange(base, size)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if overlaps(r.reserved, n) {
		return ErrOverlapReg
	}
	r.reserved = append(r.reserved, n)
	sort.Slice(r.reserved, func(i, j int) bool { return r.reserved[i].base < r.reserved[j].base })
	return nil
}

// check 校验半开区间 [addr, addr+length)：
//   - 不得触碰任何保留区；
//   - 必须被已登记 DMA 页完整覆盖（允许跨页）；
//   - 覆盖路径上每一页的权限都必须包含 need（跨页权限不得放大）。
//
// 调用方保证 addr+length 不溢出且 length >= 1。
func (r *Registry) check(addr uint64, length uint64, need Perm) error {
	end := addr + length // 半开区间终点
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, res := range r.reserved {
		if addr < res.limit && res.base < end {
			return ErrReserved
		}
	}
	cur := addr
	for cur < end {
		// 找到覆盖 cur 的页（pages 已排序且互不重叠）。
		i := sort.Search(len(r.pages), func(i int) bool { return r.pages[i].limit > cur })
		if i >= len(r.pages) || !r.pages[i].contains(cur) {
			return ErrUnmapped
		}
		pg := r.pages[i]
		if pg.perm&need != need {
			return ErrPermission
		}
		cur = pg.limit
	}
	return nil
}
