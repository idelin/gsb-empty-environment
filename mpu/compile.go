package mpu

import "sort"

// Compile 把受限内存区间编译成有限数量的 MPU 区域。
//
// 规则：
//   - 每个区间 [Start,End) 内的地址必须获得 Perm 描述的权限；
//   - 未被任何区间覆盖的地址回落到 defaultPerm（通常为 PermNone）；
//   - 重叠处按 Priority 解释，值越大优先级越高；同优先级重叠且权限
//     不一致即歧义，返回 ErrInvalidRegion（MapAmbiguous）；
//   - 非默认权限必须能由 4 字节子区粒度表达，否则返回 ErrNotRepresentable；
//   - 所需区域数超过 dregion（硬件区域寄存器数量）时返回 ErrNotRepresentable。
//
// 返回的区域按槽位号升序，槽位号即硬件上的区域优先级。
func Compile(ivs []Interval, spaceSize uint64, defaultPerm Perm, dregion int) ([]Region, error) {
	if dregion <= 0 {
		return nil, &MapError{Kind: MapCapacity, Detail: "no MPU regions available"}
	}
	if spaceSize < (1<<MinRegionSizeLog2) || spaceSize&(spaceSize-1) != 0 {
		return nil, &MapError{Kind: MapOutOfRange, Addr: spaceSize,
			Detail: "address space size must be a power of two >= 32"}
	}

	want, err := resolveIntervals(ivs, int(spaceSize), defaultPerm)
	if err != nil {
		return nil, err
	}

	n := int(spaceSize)
	needed := neededSet(want, defaultPerm, n)
	if needed.count() == 0 {
		return nil, nil
	}
	if err := checkSubregionAlignment(want, defaultPerm, n); err != nil {
		return nil, err
	}

	chosen, err := searchRegions(want, defaultPerm, n, dregion)
	if err != nil {
		return nil, err
	}

	assignSlots(chosen, ivs)
	sort.SliceStable(chosen, func(i, j int) bool { return chosen[i].Slot < chosen[j].Slot })
	return chosen, nil
}

// resolveIntervals 逐地址解析最终权限，并检测同优先级重叠歧义。
func resolveIntervals(ivs []Interval, n int, defaultPerm Perm) ([]Perm, error) {
	want := make([]Perm, n)
	ownerPri := make([]int, n)
	for i := range want {
		want[i] = defaultPerm
		ownerPri[i] = int(^uint(0) >> 1) // 最大 int：默认权限哨兵
	}
	sentinel := int(^uint(0) >> 1)
	for _, iv := range ivs {
		if iv.Start >= iv.End {
			return nil, &MapError{Kind: MapOutOfRange, Addr: iv.Start,
				Detail: "interval " + iv.Name + " is empty or reversed"}
		}
		if iv.End > uint64(n) {
			return nil, &MapError{Kind: MapOutOfRange, Addr: iv.End,
				Detail: "interval " + iv.Name + " extends beyond address space"}
		}
		for a := int(iv.Start); a < int(iv.End); a++ {
			if ownerPri[a] == iv.Priority && want[a] != iv.Perm {
				return nil, &MapError{Kind: MapAmbiguous, Addr: uint64(a),
					Detail: "overlapping intervals with equal priority disagree on permissions"}
			}
			if ownerPri[a] == sentinel || iv.Priority > ownerPri[a] {
				want[a] = iv.Perm
				ownerPri[a] = iv.Priority
			}
		}
	}
	return want, nil
}

// checkSubregionAlignment 保证非默认权限游程按 4 字节子区边界起止。
func checkSubregionAlignment(want []Perm, defaultPerm Perm, n int) error {
	for a := 0; a < n; {
		if want[a] == defaultPerm {
			a++
			continue
		}
		start := a
		p := want[a]
		for a < n && want[a] == p {
			a++
		}
		if start%MinSubregionBytes != 0 || a%MinSubregionBytes != 0 {
			return &MapError{Kind: MapAlignment, Addr: uint64(start),
				Detail: "permission run does not meet 4-byte subregion granularity"}
		}
	}
	return nil
}

// searchRegions 以迭代加深方式穷举最少区域组合。
func searchRegions(want []Perm, defaultPerm Perm, n, dregion int) ([]Region, error) {
	maxLog2 := maxSizeLog2(n)
	for limit := 1; limit <= dregion; limit++ {
		covered := newBitSet(n)
		var chosen []Region
		if dfsRegions(want, defaultPerm, n, maxLog2, limit, covered, &chosen) {
			return chosen, nil
		}
	}
	return nil, &MapError{Kind: MapCapacity, Addr: uint64(n),
		Detail: "representation requires more than the available MPU regions"}
}

func maxSizeLog2(n int) uint8 {
	l := uint8(MinRegionSizeLog2)
	for int(1)<<(l+1) <= n && l < MaxRegionSizeLog2 {
		l++
	}
	return l
}

// dfsRegions 针对第一个仍未覆盖的非默认地址，枚举所有精确表达它的
// 区域候选（尺寸/对齐基址/SRD），候选的每个使能子区权限必须统一。
// 区域间只允许在被屏蔽子区上重叠，使能足迹不得相交。
func dfsRegions(want []Perm, defaultPerm Perm, n int, maxLog2 uint8,
	budget int, covered bitSet, chosen *[]Region) bool {

	need := neededSet(want, defaultPerm, n).sub(covered)
	first := need.first()
	if first < 0 {
		return true
	}
	if budget == 0 {
		return false
	}
	for _, cand := range candidatesFor(uint64(first), want, n, maxLog2) {
		foot := cand.footprint(n)
		if foot.intersects(covered) {
			continue
		}
		*chosen = append(*chosen, cand)
		merged := covered.clone()
		for i := range merged.words {
			merged.words[i] |= foot.words[i]
		}
		if dfsRegions(want, defaultPerm, n, maxLog2, budget-1, merged, chosen) {
			return true
		}
		*chosen = (*chosen)[:len(*chosen)-1]
	}
	return false
}

func neededSet(want []Perm, defaultPerm Perm, n int) bitSet {
	s := newBitSet(n)
	for a := 0; a < n; a++ {
		if want[a] != defaultPerm {
			s.set(a)
		}
	}
	return s
}

// candidatesFor 枚举覆盖地址 a 的全部合法区域候选。命中子区的权限
// 决定区域权限；其余子区只有在“权限一致且等于区域权限”时才能使能，
// 否则必须在 SRD 中屏蔽。
func candidatesFor(a uint64, want []Perm, n int, maxLog2 uint8) []Region {
	var out []Region
	for sizeLog2 := uint8(MinRegionSizeLog2); sizeLog2 <= maxLog2; sizeLog2++ {
		size := uint64(1) << sizeLog2
		base := alignedBaseFor(a, sizeLog2)
		if base+size > uint64(n) {
			continue
		}
		subSize := subregionSize(sizeLog2)
		hitSR := int((a - base) / subSize)
		regionPerm := want[base+uint64(hitSR)*subSize]

		srd := uint8(0)
		ok := true
		for sr := 0; sr < SubregionCount; sr++ {
			from := base + uint64(sr)*subSize
			uniform := true
			for off := uint64(0); off < subSize; off++ {
				if want[from+off] != want[from] {
					uniform = false
					break
				}
			}
			if !uniform || want[from] != regionPerm {
				if sr == hitSR {
					ok = false // 命中子区本身不可屏蔽，此尺寸无解
					break
				}
				srd |= 1 << uint(sr)
			}
		}
		if ok {
			out = append(out, Region{Base: base, SizeLog2: sizeLog2, SRD: srd, Perm: regionPerm})
		}
	}
	return out
}

// regionPriority 返回区域基址所在的最高区间优先级，用于槽位分配。
func regionPriority(r Region, ivs []Interval) int {
	best := int(^uint(0) >> 1)
	found := false
	for _, iv := range ivs {
		if r.Base >= iv.Start && r.Base < iv.End {
			if !found || iv.Priority > best {
				best = iv.Priority
			}
			found = true
		}
	}
	if !found {
		return int(^uint(0) >> 1)
	}
	return best
}

// assignSlots 让高优先级区间贡献的区域获得高槽位号（硬件优先级更高），
// 同优先级内按基址排列，保证结果确定可复现。
func assignSlots(regions []Region, ivs []Interval) {
	sort.SliceStable(regions, func(i, j int) bool {
		pi := regionPriority(regions[i], ivs)
		pj := regionPriority(regions[j], ivs)
		if pi != pj {
			return pi < pj
		}
		if regions[i].Base != regions[j].Base {
			return regions[i].Base < regions[j].Base
		}
		return regions[i].SizeLog2 < regions[j].SizeLog2
	})
	for i := range regions {
		regions[i].Slot = i
	}
}
