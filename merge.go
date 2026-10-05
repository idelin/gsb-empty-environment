package main

import "sort"

// hunk 表示一处修改：base[baseLo:baseHi] 被替换为 mod[modLo:modHi]。
// 区间均为半开区间；baseLo == baseHi 表示纯插入，modLo == modHi 表示纯删除。
type hunk struct {
	baseLo, baseHi int
	modLo, modHi   int
}

// diff 用 LCS 动态规划求 base -> mod 的最小修改序列。
// 匹配按序列位置进行：内容相同的重复行各自占据不同下标，
// 不会因为是“同样的行”而被错位匹配。回溯时确定性 tie-break，
// 保证同样的输入永远得到同样的结果。
func diff(base, mod []string) []hunk {
	n, m := len(base), len(mod)
	if n == 0 || m == 0 {
		if n == 0 && m == 0 {
			return nil
		}
		return []hunk{{0, n, 0, m}}
	}
	// dp[i][j] = LCS(base[i:], mod[j:]) 的长度
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case base[i] == mod[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	// 回溯出匹配对序列（含首尾哨兵），匹配对之间的空隙即 hunk。
	type match struct{ b, m int }
	matches := make([]match, 0, dp[0][0]+2)
	matches = append(matches, match{-1, -1})
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case base[i] == mod[j]:
			matches = append(matches, match{i, j})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	matches = append(matches, match{n, m})
	var hunks []hunk
	for k := 1; k < len(matches); k++ {
		prev, cur := matches[k-1], matches[k]
		if cur.b > prev.b+1 || cur.m > prev.m+1 {
			hunks = append(hunks, hunk{prev.b + 1, cur.b, prev.m + 1, cur.m})
		}
	}
	return hunks
}

// Conflict 描述一个最小且互不重叠的冲突块，并给出三方来源区间（半开、0 基）。
type Conflict struct {
	BaseLo, BaseHi   int
	LeftLo, LeftHi   int
	RightLo, RightHi int
	BaseLines        []string
	LeftLines        []string
	RightLines       []string
}

// Segment 是合并输出的一段：要么是干净行，要么是一个冲突块。
type Segment struct {
	Lines    []string
	Conflict *Conflict
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// overlaps 判断一个 hunk 是否与当前聚簇 [lo,hi) 重叠。
// 规则：两个非空区间按区间相交判断（仅贴边不算重叠，因此两侧对
// 相邻行的独立修改可以自动合并）；插入点（空区间）落在区间边界或
// 内部都算重叠，避免一侧的插入与另一侧的修改被静默叠加出重复内容。
func overlaps(lo, hi int, h hunk) bool {
	if h.baseLo == h.baseHi {
		return lo <= h.baseLo && h.baseLo <= hi
	}
	if lo == hi {
		return h.baseLo <= lo && lo <= h.baseHi
	}
	return h.baseLo < hi && lo < h.baseHi
}

// mapPos 把 base 中的位置 p 映射到 mod 中的对应位置。
// p 必须不严格落在任何 hunk 内部（聚簇边界满足这一性质）。
// 恰好位于 p 的插入点不计入（插入内容属于 p 之后的区域）。
func mapPos(p int, hunks []hunk) int {
	off := 0
	for _, h := range hunks {
		if h.baseHi <= p && (h.baseLo < h.baseHi || h.baseLo < p) {
			off += (h.modHi - h.modLo) - (h.baseHi - h.baseLo)
		}
	}
	return p + off
}

// Merge 执行三方合并：base 为共同基线，left/right 为两份修改稿。
// 返回按 base 顺序排列的输出段。算法对 left/right 完全对称，
// 交换两侧只会交换冲突块中的左右内容，不会改变合并结果本身。
func Merge(base, left, right []string) []Segment {
	leftHunks := diff(base, left)
	rightHunks := diff(base, right)

	type tagged struct {
		h      hunk
		isLeft bool
	}
	all := make([]tagged, 0, len(leftHunks)+len(rightHunks))
	for _, h := range leftHunks {
		all = append(all, tagged{h, true})
	}
	for _, h := range rightHunks {
		all = append(all, tagged{h, false})
	}
	sort.Slice(all, func(a, b int) bool {
		if all[a].h.baseLo != all[b].h.baseLo {
			return all[a].h.baseLo < all[b].h.baseLo
		}
		if all[a].h.baseHi != all[b].h.baseHi {
			return all[a].h.baseHi < all[b].h.baseHi
		}
		return all[a].isLeft && !all[b].isLeft
	})

	var segs []Segment
	pos := 0 // 已消费到的 base 位置
	for i := 0; i < len(all); {
		lo, hi := all[i].h.baseLo, all[i].h.baseHi
		var ls, rs []hunk
		add := func(t tagged) {
			if t.isLeft {
				ls = append(ls, t.h)
			} else {
				rs = append(rs, t.h)
			}
		}
		add(all[i])
		i++
		for i < len(all) && overlaps(lo, hi, all[i].h) {
			if all[i].h.baseHi > hi {
				hi = all[i].h.baseHi
			}
			add(all[i])
			i++
		}
		if lo > pos {
			segs = append(segs, Segment{Lines: base[pos:lo]})
		}
		pos = hi
		switch {
		case len(rs) == 0: // 仅左侧修改
			segs = append(segs, Segment{Lines: left[ls[0].modLo:ls[len(ls)-1].modHi]})
		case len(ls) == 0: // 仅右侧修改
			segs = append(segs, Segment{Lines: right[rs[0].modLo:rs[len(rs)-1].modHi]})
		default: // 两侧都触及该区域
			segs = append(segs, mergeCluster(base, left, right, lo, hi,
				leftHunks, rightHunks, ls, rs)...)
		}
	}
	if pos < len(base) {
		segs = append(segs, Segment{Lines: base[pos:]})
	}
	return segs
}

// mergeCluster 处理两侧都修改了的同一 base 区域 [lo,hi)。
func mergeCluster(base, left, right []string, lo, hi int,
	leftHunks, rightHunks, ls, rs []hunk) []Segment {

	var leftLo, leftHi, rightLo, rightHi int
	if lo == hi {
		// 纯插入点聚簇：两侧在同一位置插入
		leftLo, leftHi = ls[0].modLo, ls[len(ls)-1].modHi
		rightLo, rightHi = rs[0].modLo, rs[len(rs)-1].modHi
	} else {
		leftLo, leftHi = mapPos(lo, leftHunks), mapPos(hi, leftHunks)
		rightLo, rightHi = mapPos(lo, rightHunks), mapPos(hi, rightHunks)
	}
	leftText := left[leftLo:leftHi]
	rightText := right[rightLo:rightHi]
	baseText := base[lo:hi]

	switch {
	case equalLines(leftText, rightText):
		// 两侧对同一区域做了相同修改：只保留一次
		return []Segment{{Lines: leftText}}
	case equalLines(leftText, baseText):
		// 左侧未改，取右侧
		return []Segment{{Lines: rightText}}
	case equalLines(rightText, baseText):
		// 右侧未改，取左侧
		return []Segment{{Lines: leftText}}
	}

	// 削去两侧输出相同的前后缀，使冲突块最小。
	pre := 0
	for pre < len(leftText) && pre < len(rightText) && leftText[pre] == rightText[pre] {
		pre++
	}
	suf := 0
	for suf < len(leftText)-pre && suf < len(rightText)-pre &&
		leftText[len(leftText)-1-suf] == rightText[len(rightText)-1-suf] {
		suf++
	}
	leftCore := leftText[pre : len(leftText)-suf]
	rightCore := rightText[pre : len(rightText)-suf]
	prefix := leftText[:pre]
	suffix := leftText[len(leftText)-suf:]

	// 削去公共前后缀后，若某一侧的剩余内容与基线一致，说明该侧的
	// 修改已全部体现在公共部分中，直接取另一侧，不产生冲突。
	if equalLines(leftCore, baseText) {
		return cleanSegs(prefix, rightCore, suffix)
	}
	if equalLines(rightCore, baseText) {
		return cleanSegs(prefix, leftCore, suffix)
	}

	var segs []Segment
	if pre > 0 {
		segs = append(segs, Segment{Lines: prefix})
	}
	segs = append(segs, Segment{Conflict: &Conflict{
		BaseLo: lo, BaseHi: hi,
		LeftLo: leftLo + pre, LeftHi: leftHi - suf,
		RightLo: rightLo + pre, RightHi: rightHi - suf,
		BaseLines:  baseText,
		LeftLines:  leftCore,
		RightLines: rightCore,
	}})
	if suf > 0 {
		segs = append(segs, Segment{Lines: suffix})
	}
	return segs
}

func cleanSegs(parts ...[]string) []Segment {
	var segs []Segment
	for _, p := range parts {
		if len(p) > 0 {
			segs = append(segs, Segment{Lines: p})
		}
	}
	return segs
}
