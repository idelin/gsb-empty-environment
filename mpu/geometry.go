package mpu

// validateRegion 检查区域是否满足 MPU 几何约束：
// 尺寸为 [32B, 4GiB] 的 2 的幂、基址按尺寸对齐、区域不越出地址空间。
func validateRegion(r Region, spaceSize uint64) error {
	if r.SizeLog2 < MinRegionSizeLog2 || r.SizeLog2 > MaxRegionSizeLog2 {
		return &MapError{Kind: MapOutOfRange, Addr: r.Base,
			Detail: "region size exponent out of supported range"}
	}
	size := uint64(1) << r.SizeLog2
	if r.Base%size != 0 {
		return &MapError{Kind: MapAlignment, Addr: r.Base,
			Detail: "base address is not aligned to the region size"}
	}
	if r.Base+size > spaceSize {
		return &MapError{Kind: MapOutOfRange, Addr: r.Base,
			Detail: "region extends beyond the address space"}
	}
	return nil
}

// subregionSize 返回区域内单个子区的字节数（区域尺寸 / 8）。
func subregionSize(sizeLog2 uint8) uint64 {
	return uint64(1) << (sizeLog2 - 3)
}

// covers 报告地址 a 是否落在区域的使能子区内（SRD 对应位为 0）。
func (r Region) covers(a uint64) bool {
	size := uint64(1) << r.SizeLog2
	if a < r.Base || a >= r.Base+size {
		return false
	}
	subSize := subregionSize(r.SizeLog2)
	idx := int((a-r.Base)/subSize) % SubregionCount
	return r.SRD&(1<<uint(idx)) == 0
}

// footprint 返回区域实际使能（未被 SRD 屏蔽）的地址集合。
func (r Region) footprint(spaceSize int) bitSet {
	set := newBitSet(spaceSize)
	subSize := int(subregionSize(r.SizeLog2))
	for sr := 0; sr < SubregionCount; sr++ {
		if r.SRD&(1<<uint(sr)) != 0 {
			continue
		}
		from := int(r.Base) + sr*subSize
		to := from + subSize
		if from >= spaceSize {
			break
		}
		if to > spaceSize {
			to = spaceSize
		}
		set.setRange(from, to)
	}
	return set
}

// alignedBaseFor 返回覆盖 a、尺寸为 1<<sizeLog2 的对齐区域基址。
func alignedBaseFor(a uint64, sizeLog2 uint8) uint64 {
	mask := (uint64(1) << sizeLog2) - 1
	return a &^ mask
}
