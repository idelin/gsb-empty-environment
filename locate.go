package sparse

// Locate 返回最小下标 p 使得 [0,p] 的累计和 >= cum（即第 cum 个单位落的位置）。
// 仅对非负快照开放：调用前须保证 NonNeg() 为 true，否则返回 ok=false。
// cum 的有效范围是 [1, 总和]；越界返回 ok=false。
//
// 复杂度 O(段数 + 例外数)：均匀缺省段用除法算术跳跃，绝不逐元素遍历。
func (a *Array) Locate(cum int64) (uint64, bool) {
	if !a.NonNeg() {
		return 0, false
	}
	return a.v.locate(a.n, cum)
}

// Locate 是快照上的累计定位（快照创建时已判定非负性）。
func (s *Snapshot) Locate(cum int64) (uint64, bool) {
	if !s.nonNeg {
		return 0, false
	}
	return s.v.locate(s.n, cum)
}

func (v *view) locate(n uint64, cum int64) (uint64, bool) {
	if cum < 1 {
		return 0, false
	}
	epos, eval := v.mergedExc()
	ei, si := 0, 0
	var pos uint64
	prefix := int64(0) // [0,pos) 的累计和
	for pos < n {
		for si < len(v.segs) && v.segs[si].hi <= pos {
			si++
		}
		var w int64
		segEnd := n
		if si < len(v.segs) && v.segs[si].lo <= pos {
			w = v.def + v.segs[si].d
			segEnd = v.segs[si].hi
		} else {
			w = v.def
			if si < len(v.segs) && v.segs[si].lo < segEnd {
				segEnd = v.segs[si].lo
			}
		}
		for ei < len(epos) && epos[ei] < pos {
			ei++
		}
		boundary := segEnd
		if ei < len(epos) && epos[ei] < boundary {
			boundary = epos[ei]
		}
		if boundary > pos { // 均匀段 [pos,boundary)，每元素值 w >= 0
			L := boundary - pos
			if w > 0 {
				need := cum - prefix // >= 1
				j := (need + w - 1) / w
				if uint64(j) <= L {
					return pos + uint64(j) - 1, true
				}
				prefix += w * int64(L)
			}
			pos = boundary
		}
		if ei < len(epos) && epos[ei] == pos { // 例外单点
			if prefix+eval[ei] >= cum {
				return pos, true
			}
			prefix += eval[ei]
			pos++
			ei++
		}
	}
	return 0, false
}
