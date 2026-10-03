package grid

// 本文件实现六边形格子的纯整数拓扑运算。
//
// 六边形中心格点用 Eisenstein 整数表示：轴向坐标 (q, r)，
// 隐式满足立方坐标约束 q + r + s = 0（s = -q-r）。
//
// 层级缩放基为 A = 2 + ω（ω 为六次单位根），范数 |A|^2 = 7，
// 即每个父单元恰好细分为 7 个子单元（aperture 7，与 H3 同构）。
// A 的整数矩阵为 [[2,-1],[1,3]]，det = 7。
// 数字集合 {0, e1..e6}（中心 + 六个单位方向）构成模 A 的完全剩余系，
// 因此任意格点存在唯一的有限 "A 进制" 展开 —— 这就是分层编码的拓扑骨架。
// 全部运算为整数加减乘除，不含任何浮点三角计算。

// axial 轴向整数坐标。
type axial struct{ q, r int64 }

// unitDirs 六个邻居方向（逆时针，digit 1..6 依次对应）。
var unitDirs = [6]axial{{1, 0}, {0, 1}, {-1, 1}, {-1, 0}, {0, -1}, {1, -1}}

// digitDir 返回 digit(1..6) 对应的单位方向；digit 0 表示中心。
func digitDir(d int) axial {
	if d <= 0 || d > 6 {
		return axial{}
	}
	return unitDirs[d-1]
}

// mulA 乘以缩放基 A：(q,r) -> (2q-r, q+3r)。
func mulA(a axial) axial { return axial{2*a.q - a.r, a.q + 3*a.r} }

// divA 除以 A（仅对 A 子格中的点调用，整除精确）：
// A^{-1} = (1/7) * [[3,1],[-1,2]]。
func divA(a axial) axial { return axial{(3*a.q + a.r) / 7, (-a.q + 2*a.r) / 7} }

// residue 计算模 A 剩余类：f(q,r) = (q - 2r) mod 7。
// 可验证 f(A*x) == 0，且 {0,e1..e6} 的剩余恰好遍历 0..6。
func residue(a axial) int {
	v := (a.q - 2*a.r) % 7
	if v < 0 {
		v += 7
	}
	return int(v)
}

// residueToDigit 剩余类 -> digit。剩余 0 为中心 digit 0。
var residueToDigit = [7]int{0, 1, 5, 6, 3, 2, 4}

// expand 将格点展开为 digit 序列（粗 -> 细），即 A 进制展开。
// 每步 |T| 缩小 sqrt(7) 倍，保证有限步终止。
func expand(t axial) []int {
	var rev []int
	for t.q != 0 || t.r != 0 {
		d := residueToDigit[residue(t)]
		rev = append(rev, d)
		if d != 0 {
			dir := digitDir(d)
			t.q -= dir.q
			t.r -= dir.r
		}
		t = divA(t)
	}
	digits := make([]int, len(rev))
	for i, d := range rev {
		digits[len(rev)-1-i] = d
	}
	return digits
}

// collapse 将 digit 序列（粗 -> 细）折叠回格点：T = sum A^(n-i) * v_di。
func collapse(digits []int) axial {
	var t axial
	for _, d := range digits {
		t = mulA(t)
		if d != 0 {
			dir := digitDir(d)
			t.q += dir.q
			t.r += dir.r
		}
	}
	return t
}

// hexDistance 两格点间的六边形拓扑距离（环数）。
func hexDistance(a, b axial) int {
	dq := a.q - b.q
	dr := a.r - b.r
	ds := -dq - dr
	return int((abs64(dq) + abs64(dr) + abs64(ds)) / 2)
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
