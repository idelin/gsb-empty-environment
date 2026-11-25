package dma

// region 是一段按页对齐、包含同一权限的连续区间。
type region struct {
	base uint64
	end  uint64
	perm Perm
}

// overlaps 报告两个半开区间 [base,end) 是否相交。
func (r region) overlaps(base, end uint64) bool {
	return r.base < end && base < r.end
}

// PageTable 登记某台虚拟机可用于 DMA 的页区间与保留区。
// 零值不可直接使用，请用 NewPageTable 构造；它可作为字段嵌入设备宿主。
type PageTable struct {
	mapped   []region
	reserved []region
}

// NewPageTable 创建空页表。
func NewPageTable() *PageTable {
	return &PageTable{}
}

func aligned(base, length uint64) bool {
	return base%PageSize == 0 && length%PageSize == 0
}

// Register 登记一段按页对齐、具备 perm 权限的 DMA 页区间。
// 与既有登记或保留区重叠、零长度或未对齐都会报错。
func (t *PageTable) Register(base, length uint64, perm Perm) error {
	if length == 0 || perm == 0 {
		return ErrAlignment
	}
	if !aligned(base, length) {
		return ErrAlignment
	}
	end := base + length
	for _, r := range t.mapped {
		if r.overlaps(base, end) {
			return ErrRegionOverlap
		}
	}
	for _, r := range t.reserved {
		if r.overlaps(base, end) {
			return ErrRegionOverlap
		}
	}
	t.mapped = append(t.mapped, region{base: base, end: end, perm: perm})
	return nil
}

// Reserve 标记一段按页对齐的保留区；任何描述符触碰该区即拒绝。
func (t *PageTable) Reserve(base, length uint64) error {
	if length == 0 {
		return ErrAlignment
	}
	if !aligned(base, length) {
		return ErrAlignment
	}
	end := base + length
	for _, r := range t.mapped {
		if r.overlaps(base, end) {
			return ErrRegionOverlap
		}
	}
	for _, r := range t.reserved {
		if r.overlaps(base, end) {
			return ErrRegionOverlap
		}
	}
	t.reserved = append(t.reserved, region{base: base, end: end})
	return nil
}

// validate 逐项校验描述符传输区间：区间内每一页都必须已登记、
// 权限不低于描述符要求，且任何字节都不得落在保留区。
// 描述符自身已保证 addr+length 不溢出，因此跨页切分不会回绕。
func (t *PageTable) validate(d Descriptor) error {
	start := d.Addr
	end := start + uint64(d.Length)
	need := d.Perm()

	for _, r := range t.reserved {
		if r.overlaps(start, end) {
			return ErrReservedRegion
		}
	}

	// 按 4 KiB 页切分；每个页块落在且仅落在一个登记区内，因此相邻
	// 登记区拼成的长区间也能逐页通过，空洞则报未映射。
	cursor := start
	for cursor < end {
		chunkEnd := (cursor/PageSize + 1) * PageSize
		if chunkEnd > end {
			chunkEnd = end
		}
		r, ok := t.lookup(cursor, chunkEnd)
		if !ok {
			return ErrPageNotMapped
		}
		if !r.perm.Contains(need) {
			return ErrPermissionDenied
		}
		cursor = chunkEnd
	}
	return nil
}

// lookup 返回完整覆盖 [start,end) 的登记区间；区间跨越登记边界或
// 落在空洞上都视为未映射。
func (t *PageTable) lookup(start, end uint64) (region, bool) {
	for _, r := range t.mapped {
		if r.base <= start && end <= r.end {
			return r, true
		}
	}
	return region{}, false
}
