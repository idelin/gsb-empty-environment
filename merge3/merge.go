package main

// 三方合并核心逻辑。
//
// 算法概述（diff3 风格）：
//  1. 分别对 base->left、base->right 做 LCS 对齐（严格按序列位置匹配，
//     重复行不会错位；tie-break 规则固定，结果确定）。
//  2. 用“运行位置”在 base 上切块：一行为稳定行当且仅当两侧都保留它，
//     且两侧已消费位置恰好等于它的对齐位置（即其前间隙无未消费插入）。
//     极大连续稳定行构成稳定块；其余构成变更块，变更块结束于下一个
//     双侧稳定行，因此块最小、互不重叠，且任意两个变更块之间至少隔着
//     一个稳定行。
//  3. 逐块合并：仅一侧变更 -> 取该侧；两侧变更相同 -> 保留一次；
//     两侧变更不同 -> 冲突，输出三方来源区间。
//  合并规则对 left/right 完全对称，不存在修改先后带来的偏向。

// lcsMatches 返回 a、b 之间 LCS 的匹配下标对，严格递增。
// 匹配按序列位置进行：第 i 个重复行只与对应位置的第 j 个重复行配对。
func lcsMatches(a, b []string) [][2]int {
	n, m := len(a), len(b)
	// dp[i][j] = LCS(a[i:], b[j:]) 的长度
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var matches [][2]int
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			matches = append(matches, [2]int{i, j})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return matches
}

// sideDiff 描述 base -> mod 一侧的对齐结果。
type sideDiff struct {
	keep  []bool // keep[i]: base 第 i 行在 mod 中保留
	match []int  // match[i]: base 第 i 行对应 mod 的下标，未保留为 -1
}

func diffSide(base, mod []string) sideDiff {
	n := len(base)
	d := sideDiff{keep: make([]bool, n), match: make([]int, n)}
	for i := range d.match {
		d.match[i] = -1
	}
	for _, p := range lcsMatches(base, mod) {
		d.keep[p[0]] = true
		d.match[p[0]] = p[1]
	}
	return d
}

// Chunk 是叠加两条 diff 后切出的对齐块，三方区间均为左闭右开。
type Chunk struct {
	Stable           bool
	BaseLo, BaseHi   int
	LeftLo, LeftHi   int
	RightLo, RightHi int
}

// buildChunks 将 base 切成稳定块与变更块。
// 三方的块区间分别恰好划分 [0,len(base))、[0,len(left))、[0,len(right))。
func buildChunks(base, left, right []string) []Chunk {
	n := len(base)
	ld := diffSide(base, left)
	rd := diffSide(base, right)
	stableLine := func(i int) bool { return ld.keep[i] && rd.keep[i] }
	var chunks []Chunk
	i, lpos, rpos := 0, 0, 0
	for i < n || lpos < len(left) || rpos < len(right) {
		if i < n && stableLine(i) && lpos == ld.match[i] && rpos == rd.match[i] {
			// 稳定块：极大连续稳定行，且相邻行在两侧的对齐位置也相邻
			// （行间无插入）。
			j := i + 1
			for j < n && stableLine(j) &&
				ld.match[j] == ld.match[j-1]+1 &&
				rd.match[j] == rd.match[j-1]+1 {
				j++
			}
			chunks = append(chunks, Chunk{
				Stable: true,
				BaseLo: i, BaseHi: j,
				LeftLo: lpos, LeftHi: lpos + (j - i),
				RightLo: rpos, RightHi: rpos + (j - i),
			})
			lpos += j - i
			rpos += j - i
			i = j
			continue
		}
		// 变更块：结束于首个双侧稳定行（或文件末尾）。
		// 该行之前间隙中的插入由两侧区间右端点自然吸收。
		j := i
		for j < n && !stableLine(j) {
			j++
		}
		lhi, rhi := len(left), len(right)
		if j < n {
			lhi = ld.match[j]
			rhi = rd.match[j]
		}
		chunks = append(chunks, Chunk{
			Stable: false,
			BaseLo: i, BaseHi: j,
			LeftLo: lpos, LeftHi: lhi,
			RightLo: rpos, RightHi: rhi,
		})
		lpos, rpos = lhi, rhi
		i = j
	}
	return chunks
}

// Segment 是合并输出的一段：要么是已解析的文本，要么是冲突。
type Segment struct {
	Conflict bool
	Lines    []string // 非冲突时的输出行

	// 冲突时的三方内容（均为对应区间原文）
	Base  []string
	Left  []string
	Right []string
	Chunk Chunk
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

// Merge 对 base/left/right 做三方合并，返回分段结果。
func Merge(base, left, right []string) []Segment {
	var segs []Segment
	for _, c := range buildChunks(base, left, right) {
		if c.Stable {
			segs = append(segs, Segment{Lines: base[c.BaseLo:c.BaseHi]})
			continue
		}
		b := base[c.BaseLo:c.BaseHi]
		l := left[c.LeftLo:c.LeftHi]
		r := right[c.RightLo:c.RightHi]
		switch {
		case equalLines(l, r):
			// 两侧对同一区域做了相同修改，只保留一次
			segs = append(segs, Segment{Lines: l})
		case equalLines(l, b):
			// 仅右侧修改
			segs = append(segs, Segment{Lines: r})
		case equalLines(r, b):
			// 仅左侧修改
			segs = append(segs, Segment{Lines: l})
		default:
			segs = append(segs, Segment{
				Conflict: true,
				Base:     b, Left: l, Right: r,
				Chunk: c,
			})
		}
	}
	return segs
}
