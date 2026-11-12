package taskpkg

import "math/big"

type relKind int

const (
	kindEquation relKind = iota
	kindKeyEq
)

// preparedRel 是预处理后的关系；活跃行/分组只取决于原始缺失模式，与候选选择无关。
type preparedRel struct {
	id     string
	kind   relKind
	keyCol int

	coeffCols []int
	coeffs    []*big.Rat
	rhs       *big.Rat
	rhsCol    int

	// 方程形态：所有相关列齐备的行。
	activeRows []int
	// 业务键等值形态：同一业务键且行数 >= 2 的分组。
	keyGroups [][]int
}

func prepareRelations(in *Input, present [][]bool) ([]preparedRel, error) {
	rows, cols := len(in.Rows), len(in.Candidates)
	out := make([]preparedRel, 0, len(in.Relations))
	for _, rel := range in.Relations {
		p := preparedRel{
			id:     rel.ID,
			rhsCol: rel.RHSCol,
			keyCol: rel.KeyCol,
		}
		if rel.RHS != nil {
			p.rhs = new(big.Rat).Set(rel.RHS)
		}
		// RHSCol 零值 0 与“未设置”在 Go 结构体里无法区分，因此约定：
		// 不需要右端列时宿主应显式传 NoRHSColumn（-1）；这里对负值统一归一。
		if p.rhsCol < 0 {
			p.rhsCol = -1
		}
		for c, q := range rel.Coeffs {
			if c < 0 || c >= cols {
				return nil, errf("关系 %q 引用了越界列 %d", rel.ID, c)
			}
			if q == nil {
				return nil, errf("关系 %q 的列 %d 系数为 nil", rel.ID, c)
			}
			p.coeffCols = append(p.coeffCols, c)
			p.coeffs = append(p.coeffs, new(big.Rat).Set(q))
		}
		if len(p.coeffCols) == 0 {
			return nil, errf("关系 %q 没有任何系数列", rel.ID)
		}
		if rel.RHSCol >= cols || rel.RHSCol < -1 {
			return nil, errf("关系 %q 的 RHSCol 越界", rel.ID)
		}

		needed := append([]int(nil), p.coeffCols...)
		if p.rhsCol >= 0 {
			needed = append(needed, p.rhsCol)
		}

		if rel.KeyCol < 0 {
			p.kind = kindEquation
			for r := 0; r < rows; r++ {
				if allPresent(present[r], needed) {
					p.activeRows = append(p.activeRows, r)
				}
			}
		} else {
			if rel.KeyCol >= cols {
				return nil, errf("关系 %q 的 KeyCol 越界", rel.ID)
			}
			p.kind = kindKeyEq
			groups := map[string][]int{}
			var order []string
			for r := 0; r < rows; r++ {
				k := in.Keys[r]
				if _, ok := groups[k]; !ok {
					order = append(order, k)
				}
				groups[k] = append(groups[k], r)
			}
			for _, k := range order {
				if len(groups[k]) >= 2 {
					p.keyGroups = append(p.keyGroups, groups[k])
				}
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func allPresent(row []bool, cols []int) bool {
	for _, c := range cols {
		if !row[c] {
			return false
		}
	}
	return true
}

// satisfied 判断给定规范值矩阵是否满足该关系；缺失跳过逻辑已在预处理阶段固化。
func (p preparedRel) satisfied(values [][]*big.Rat) bool {
	switch p.kind {
	case kindEquation:
		for _, r := range p.activeRows {
			// 方程：sum(coeff * value) == RHS + value[RHSCol]。
			// RHSCol 属于右端，绝不能混入左端系数。
			lhs := new(big.Rat)
			for i, c := range p.coeffCols {
				term := new(big.Rat).Mul(p.coeffs[i], values[r][c])
				lhs.Add(lhs, term)
			}
			rhs := new(big.Rat)
			if p.rhs != nil {
				rhs.Set(p.rhs)
			}
			if p.rhsCol >= 0 {
				rhs.Add(rhs, values[r][p.rhsCol])
			}
			if lhs.Cmp(rhs) != 0 {
				return false
			}
		}
		return true
	case kindKeyEq:
		cols := append([]int(nil), p.coeffCols...)
		if p.rhsCol >= 0 {
			cols = append(cols, p.rhsCol)
		}
		for _, group := range p.keyGroups {
			for _, c := range cols {
				var ref *big.Rat
				for _, r := range group {
					v := values[r][c]
					if v == nil {
						continue
					}
					if ref == nil {
						ref = v
					} else if ref.Cmp(v) != 0 {
						return false
					}
				}
			}
		}
		return true
	default:
		return false
	}
}
