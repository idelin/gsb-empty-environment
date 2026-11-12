package linearleak

import (
	"math/big"
)

// triState 是可行性判定的三值结果。
type triState int

const (
	triUnknown triState = iota
	triFalse
	triTrue
)

// solver 在固定节点预算内判定整数线性方程组是否存在 0/1 解。
type solver struct {
	n        int
	coeff    [][]int64
	rhs      []int64
	assigned []int8 // 0/1 已赋值，-1 未赋值
	limit    int64
	nodes    int64
	rowBig   []bool // 该行求和会溢出 int64，改用 big.Int
}

func newSolver(n int, coeff [][]int64, rhs []int64, limit int64) *solver {
	return &solver{
		n:        n,
		coeff:    coeff,
		rhs:      rhs,
		assigned: make([]int8, n),
		limit:    limit,
		rowBig:   make([]bool, len(coeff)),
	}
}

// feasible 是入口。seed 中的变量被预先固定。
func (s *solver) feasible(seed map[int]int) triState {
	for j := range s.assigned {
		s.assigned[j] = -1
	}
	for j, v := range seed {
		if j < 0 || j >= s.n || (v != 0 && v != 1) {
			return triFalse
		}
		s.assigned[j] = int8(v)
	}

	// 根节点做一次有理消元：直接发现矛盾或实数固定变量。
	if !s.preprocessRoot() {
		return triFalse
	}
	return s.dfs()
}

// preprocessRoot 用 RREF 做根节点剪枝，返回 false 表示已确认不可行。
func (s *solver) preprocessRoot() bool {
	mat := make([]ratRow, len(s.coeff))
	for i, row := range s.coeff {
		rr := make(ratRow, s.n+1)
		for j := 0; j < s.n; j++ {
			rr[j] = big.NewRat(row[j], 1)
		}
		rr[s.n] = big.NewRat(s.rhs[i], 1)
		mat[i] = rr
	}
	_, frees, inconsistent := rref(mat)
	if inconsistent {
		return false
	}
	if len(mat) == 0 {
		return true
	}

	// RREF 主元行不含任何自由变量时，该主元变量被实数唯一固定。
	one := big.NewRat(1, 1)
	for r := 0; r < len(mat); r++ {
		allFreeZero := true
		for _, f := range frees {
			if mat[r][f].Sign() != 0 {
				allFreeZero = false
				break
			}
		}
		if !allFreeZero {
			continue
		}
		pivot := -1
		for j := 0; j < s.n; j++ {
			if mat[r][j].Cmp(one) == 0 {
				pivot = j
				break
			}
		}
		if pivot < 0 {
			continue
		}
		v, ok := ratBit(mat[r][s.n])
		if !ok {
			// 实数唯一但取值非 0/1，布尔域无解。
			return false
		}
		if s.assigned[pivot] != -1 && int(s.assigned[pivot]) != v {
			return false
		}
		s.assigned[pivot] = int8(v)
	}
	return true
}

func add64(a, b int64) (int64, bool) {
	r := a + b
	if (b > 0 && r < a) || (b < 0 && r > a) {
		return 0, false
	}
	return r, true
}

func mul64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	r := a * b
	if r/a != b {
		return 0, false
	}
	return r, true
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// evalRow 计算行 i 的残差（右端减已赋值贡献）以及剩余自由变量
// 贡献的可达区间 [lo, hi]。overflow 为 true 表示触发 int64 溢出，
// 调用方应改用 evalRowBig。
func (s *solver) evalRow(i int) (res, lo, hi int64, overflow bool) {
	res = s.rhs[i]
	for j, c := range s.coeff[i] {
		if s.assigned[j] == -1 {
			if c > 0 {
				h, ok := add64(hi, c)
				if !ok {
					return 0, 0, 0, true
				}
				hi = h
			} else if c < 0 {
				l, ok := add64(lo, c)
				if !ok {
					return 0, 0, 0, true
				}
				lo = l
			}
			continue
		}
		term, ok := mul64(c, int64(s.assigned[j]))
		if !ok {
			return 0, 0, 0, true
		}
		r, ok := add64(res, -term)
		if !ok {
			return 0, 0, 0, true
		}
		res = r
	}
	return res, lo, hi, false
}

// evalRowBig 是溢出路径，全程使用 big.Int。
func (s *solver) evalRowBig(i int) (res, lo, hi *big.Int) {
	res = new(big.Int).SetInt64(s.rhs[i])
	lo = new(big.Int)
	hi = new(big.Int)
	for j, c := range s.coeff[i] {
		cb := big.NewInt(c)
		if s.assigned[j] == -1 {
			if c > 0 {
				hi.Add(hi, cb)
			} else if c < 0 {
				lo.Add(lo, cb)
			}
			continue
		}
		res.Sub(res, new(big.Int).Mul(cb, big.NewInt(int64(s.assigned[j]))))
	}
	return res, lo, hi
}

// dfs 执行带上下界传播的回溯，返回是否存在可行赋值。
func (s *solver) dfs() triState {
	if s.nodes >= s.limit {
		return triUnknown
	}
	s.nodes++

	// 不动点传播：每行检查可达区间并钉死强制变量。
	var undo []int
	changed := true
	for changed {
		changed = false
		for i := range s.coeff {
			var res, lo, hi *big.Int
			if s.rowBig[i] {
				res, lo, hi = s.evalRowBig(i)
			} else {
				r, l, h, overflow := s.evalRow(i)
				if overflow {
					s.rowBig[i] = true
					res, lo, hi = s.evalRowBig(i)
				} else {
					res, lo, hi = big.NewInt(r), big.NewInt(l), big.NewInt(h)
				}
			}
			if res.Cmp(lo) < 0 || res.Cmp(hi) > 0 {
				s.rollback(undo)
				return triFalse
			}
			conflict, didChange := s.propagate(i, res, lo, hi, &undo)
			if conflict {
				s.rollback(undo)
				return triFalse
			}
			if didChange {
				changed = true
			}
		}
	}

	// 选择系数绝对值之和最大的自由变量作为分支变量。
	branch := -1
	bestScore := int64(-1)
	score := make([]int64, s.n)
	for i := range s.coeff {
		if s.rowBig[i] {
			continue
		}
		for j, c := range s.coeff[i] {
			if s.assigned[j] == -1 {
				if sc, ok := add64(score[j], abs64(c)); ok {
					score[j] = sc
				}
			}
		}
	}
	for j := 0; j < s.n; j++ {
		if s.assigned[j] == -1 && score[j] > bestScore {
			bestScore, branch = score[j], j
		}
	}
	if branch < 0 {
		s.rollback(undo)
		return triTrue
	}

	for _, v := range []int8{0, 1} {
		s.assigned[branch] = v
		outcome := s.dfs()
		s.assigned[branch] = -1
		if outcome == triTrue {
			s.rollback(undo)
			return triTrue
		}
		if outcome == triUnknown {
			s.rollback(undo)
			return triUnknown
		}
	}
	s.rollback(undo)
	return triFalse
}

// propagate 依据一行的残差与可达区间强制钉死变量。
//
// 对自由变量 j（系数 c），其余自由变量可达区间为 [lo-c, hi-c]：
//   - 置 0 可行当且仅当 res ∈ [lo-c, hi-c]；
//   - 置 1 可行当且仅当 res-c ∈ [lo-c, hi-c]。
//
// 两者都不可行即行冲突；仅一个可行则变量被强制。
func (s *solver) propagate(i int, res, lo, hi *big.Int, undo *[]int) (conflict, changed bool) {
	for j, c := range s.coeff[i] {
		if s.assigned[j] != -1 || c == 0 {
			continue
		}
		cb := big.NewInt(c)
		lo0 := new(big.Int).Sub(lo, cb)
		hi0 := new(big.Int).Sub(hi, cb)

		zeroFeasible := inRange(res, lo0, hi0)
		resMinusC := new(big.Int).Sub(res, cb)
		oneFeasible := inRange(resMinusC, lo0, hi0)

		if !zeroFeasible && !oneFeasible {
			return true, changed
		}
		forced := -1
		if !zeroFeasible {
			forced = 1
		} else if !oneFeasible {
			forced = 0
		}
		if forced < 0 {
			continue
		}
		s.assigned[j] = int8(forced)
		*undo = append(*undo, j)
		changed = true
	}
	return false, changed
}

func inRange(v, lo, hi *big.Int) bool {
	return v.Cmp(lo) >= 0 && v.Cmp(hi) <= 0
}

func (s *solver) rollback(undo []int) {
	for _, j := range undo {
		s.assigned[j] = -1
	}
}
