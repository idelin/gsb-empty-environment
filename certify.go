package netsec

import (
	"fmt"
	"sort"
)

func certKey(b BlockID, idx int) string {
	return fmt.Sprintf("%d:%d", b, idx)
}

// certifyLoad 为一次读取生成范围证书，或登记违例。
func (az *analyzer) certifyLoad(cur *frame, b BlockID, idx int, op *Op) {
	if op.Size <= 0 {
		az.addViolation(op.Site, b, idx, "读取大小必须为正数")
		return
	}
	index := cur.get(op.Src)

	// 外部输入只允许经显式边界检查收窄后参与寻址。
	if index.External && !index.Checked {
		az.addViolation(op.Site, b, idx, "外部输入未经显式边界检查即用于读取寻址")
		return
	}
	if index.Scalar.IsEmpty() {
		az.addViolation(op.Site, b, idx, "索引区间为空（不可达或未定义）")
		return
	}

	type span struct {
		region RegionID
		iv     Interval
	}
	var spans []span
	externalBase := false

	if op.Region != IndirectRegion {
		// 显式区域读取：基址寄存器必须只指向该区域，索引给出偏移区间。
		base := cur.get(op.Base)
		if len(base.Origins) != 1 {
			az.addViolation(op.Site, b, idx, "显式区域读取的基址必须携带唯一区域来源")
			return
		}
		if _, ok := base.Origins[op.Region]; !ok {
			az.addViolation(op.Site, b, idx, "基址来源区域与读取声明区域不一致")
			return
		}
		if base.External && !base.Checked {
			az.addViolation(op.Site, b, idx, "外部基址未经边界检查收窄")
			return
		}
		// 显式区域读取遵循「基址（含偏移区间）+ 索引」的受限寻址。索引允许：
		// 零偏移地址（复制基址的惯用法，偏移恒为 [0,0]）、纯零常量，或已收窄
		// 的外部标量；非零偏移的地址索引会双重计数来源偏移，故拒绝。
		if index.IsAddress() {
			zeroOffset := true
			for _, iv := range index.Origins {
				if !(iv.IsSingle() && iv.Lo == 0) {
					zeroOffset = false
					break
				}
			}
			if !zeroOffset {
				az.addViolation(op.Site, b, idx, "显式区域读取的地址索引必须保持零偏移")
				return
			}
		}
		if index.External && !index.Checked {
			az.addViolation(op.Site, b, idx, "外部索引未经边界检查收窄")
			return
		}
		externalBase = base.External
		offset := base.Origins[op.Region]
		if index.External {
			offset = offset.Add(index.Scalar)
		}
		spans = append(spans, span{region: op.Region, iv: offset})
	} else {
		// 间接读取：来源集合由基址寄存器在合流后保留的来源并集决定。
		base := cur.get(op.Base)
		if len(base.Origins) == 0 {
			az.addViolation(op.Site, b, idx, "间接读取的基址不携带任何区域来源")
			return
		}
		if base.External && !base.Checked {
			az.addViolation(op.Site, b, idx, "外部基址未经边界检查收窄")
			return
		}
		externalBase = base.External
		// 基址自身的偏移区间与索引相加，得到各来源的最终偏移。
		sum := index.Scalar
		for r, iv := range base.Origins {
			spans = append(spans, span{region: r, iv: iv.Add(sum)})
		}
	}

	sort.Slice(spans, func(i, j int) bool { return spans[i].region < spans[j].region })

	originRanges := make(map[RegionID]Interval, len(spans))
	origins := make([]RegionID, 0, len(spans))
	var lo, hi int64
	first := true
	var badReason string

	for _, sp := range spans {
		if int(sp.region) >= len(az.prog.Regions) {
			badReason = fmt.Sprintf("来源区域 %d 未声明", sp.region)
			break
		}
		region := az.prog.Regions[sp.region]
		accessLo := sp.iv.Lo
		accessHi, of := addChecked(sp.iv.Hi, op.Size-1)
		if of {
			badReason = "访问区间端点溢出 int64"
			break
		}
		// 整个偏移闭区间 [accessLo, accessHi] 都必须落在非负的 [0, Size) 内。
		if accessLo < 0 || accessHi < 0 || !region.ContainsOffset(accessLo, op.Size) {
			badReason = fmt.Sprintf(
				"访问偏移 [%d, %d] 越出区域 %q[基址 %d, 上界 %d)",
				accessLo, accessHi, region.Name, region.Base, region.End())
			break
		}
		absLo, ofLo := addChecked(region.Base, accessLo)
		absHi, ofHi := addChecked(region.Base, accessHi)
		if ofLo || ofHi {
			badReason = "绝对访问地址溢出 int64"
			break
		}
		abs := Interval{Lo: absLo, Hi: absHi}
		originRanges[sp.region] = abs
		origins = append(origins, sp.region)
		if first || abs.Lo < lo {
			lo = abs.Lo
		}
		if first || abs.Hi > hi {
			hi = abs.Hi
		}
		first = false
	}

	if badReason != "" {
		az.addViolation(op.Site, b, idx, badReason)
		return
	}

	cert := Certificate{
		Site:         op.Site,
		Block:        b,
		Op:           idx,
		Size:         op.Size,
		IndexRange:   index.Scalar,
		Origins:      origins,
		OriginRanges: originRanges,
		AccessLo:     lo,
		AccessHi:     hi,
		ExternalBase: externalBase,
		CheckSites:   mergeSites(index.CheckSites, cur.get(op.Base).CheckSites),
	}
	az.certs[certKey(b, idx)] = cert
}
