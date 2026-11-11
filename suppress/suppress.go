// Package suppress 为至多 4×4 的非负整数统计表计算最低成本的补充抑制方案。
//
// 背景：仅隐藏敏感（小计数）单元时，攻击者仍可借助每个单元的整数上下界以及
// 行、列总和推断被隐藏值。补充抑制把额外单元一并隐藏，使每个敏感单元都存在
// 至少两个互不相同的可行取值。
//
// 保护判据：记抑制集合 H 为敏感单元与额外抑制单元的并集。敏感单元 k 受保护，
// 当且仅当存在两张均满足上下界与行列和的完整表 T1、T2，二者在 H 之外的全部
// 单元上取值相同，而在 k 上取值不同。两张表的公开单元完全一致，攻击者无法
// 区分真实表。该判据等价于：在仅由 H 内单元构成的二部图中存在一条经过 k 的
// 交错环，沿环把一个单位在行列间转移后仍满足全部边界。本包用整数最大流
// （Dinic）判定可行性，并可用 Enumerate/Verify 在小值域上穷举核对。
package suppress

import (
	"errors"
	"fmt"
)

// 哨兵错误：宿主可用 errors.Is 判别失败类别。
var (
	// ErrInvalidProblem 表示问题定义（维度、边界、边际和、成本、敏感坐标）无效。
	ErrInvalidProblem = errors.New("suppress: 问题定义无效")
	// ErrInfeasible 表示在给定上下界与行列和下不存在任何完整可行表。
	ErrInfeasible = errors.New("suppress: 给定边界与行列和下不存在可行表")
	// ErrUnprotectable 表示不存在能让全部敏感单元都受保护的补充抑制集合。
	ErrUnprotectable = errors.New("suppress: 敏感单元无法被保护")
	// ErrDomainTooLarge 表示穷举核对的可行表数量超过安全上限。
	ErrDomainTooLarge = errors.New("suppress: 值域过大，无法穷举核对")
)

const maxDim = 4

// Cell 按从零开始的行、列坐标标识一个表单元。
type Cell struct {
	Row int
	Col int
}

// Problem 描述一张带先验边界与边际和的非负整数统计表。
//
// Lower、Upper 为每个单元的整数上下界，要求 0 <= Lower[i][j] <= Upper[i][j]。
// RowSums、ColSums 为行、列总和，二者总计必须相等。Sensitive 为已隐藏的原
// 敏感单元，坐标不得重复。Cost 为每个单元被额外隐藏一次的成本，须非负；
// Cost 为 nil 时所有单元成本视为 1，敏感单元的成本不参与计费。
type Problem struct {
	Lower     [][]int
	Upper     [][]int
	RowSums   []int
	ColSums   []int
	Sensitive []Cell
	Cost      [][]int
}

// WitnessSet 保存某个敏感单元的两份完整表见证。
//
// A、B 均满足全部上下界与行列和，在抑制集合之外的单元上取值完全相同，且
// A 在该敏感单元上的取值严格小于 B（即该单元至少存在两个可行值）。
type WitnessSet struct {
	Sensitive Cell
	A         [][]int
	B         [][]int
}

// Plan 是补充抑制求解结果。
//
// Extra 为需要额外隐藏的单元，按行优先坐标排序；TotalCost 为其成本之和；
// Witnesses 与 Problem.Sensitive 同序，逐单元给出两份见证。
type Plan struct {
	Extra     []Cell
	TotalCost int
	Witnesses []WitnessSet
}

type normalized struct {
	rows, cols int
	lower      [][]int
	upper      [][]int
	cost       [][]int
	rowSums    []int
	colSums    []int
	sensitive  []Cell
}

func normalize(p *Problem) (*normalized, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: Problem 为 nil", ErrInvalidProblem)
	}
	r := len(p.Lower)
	if r == 0 || r > maxDim {
		return nil, fmt.Errorf("%w: 行数须在 1..%d 之间", ErrInvalidProblem, maxDim)
	}
	c := len(p.Lower[0])
	if c == 0 || c > maxDim {
		return nil, fmt.Errorf("%w: 列数须在 1..%d 之间", ErrInvalidProblem, maxDim)
	}
	if len(p.Upper) != r {
		return nil, fmt.Errorf("%w: Upper 行数与 Lower 不一致", ErrInvalidProblem)
	}
	if p.Cost != nil && len(p.Cost) != r {
		return nil, fmt.Errorf("%w: Cost 行数与 Lower 不一致", ErrInvalidProblem)
	}
	lower := make([][]int, r)
	upper := make([][]int, r)
	cost := make([][]int, r)
	for i := 0; i < r; i++ {
		if len(p.Lower[i]) != c || len(p.Upper[i]) != c {
			return nil, fmt.Errorf("%w: 第 %d 行宽度不是 %d", ErrInvalidProblem, i, c)
		}
		if p.Cost != nil && len(p.Cost[i]) != c {
			return nil, fmt.Errorf("%w: Cost 第 %d 行宽度不是 %d", ErrInvalidProblem, i, c)
		}
		lower[i] = append([]int(nil), p.Lower[i]...)
		upper[i] = append([]int(nil), p.Upper[i]...)
		cost[i] = make([]int, c)
		for j := 0; j < c; j++ {
			if lower[i][j] < 0 {
				return nil, fmt.Errorf("%w: Lower[%d][%d] 为负", ErrInvalidProblem, i, j)
			}
			if upper[i][j] < lower[i][j] {
				return nil, fmt.Errorf("%w: Upper[%d][%d] 小于 Lower", ErrInvalidProblem, i, j)
			}
			if p.Cost != nil {
				if p.Cost[i][j] < 0 {
					return nil, fmt.Errorf("%w: Cost[%d][%d] 为负", ErrInvalidProblem, i, j)
				}
				cost[i][j] = p.Cost[i][j]
			} else {
				cost[i][j] = 1
			}
		}
	}
	if len(p.RowSums) != r || len(p.ColSums) != c {
		return nil, fmt.Errorf("%w: 行列和长度与表维度不匹配", ErrInvalidProblem)
	}
	rowSums := append([]int(nil), p.RowSums...)
	colSums := append([]int(nil), p.ColSums...)
	totalRow, totalCol := 0, 0
	for i, v := range rowSums {
		if v < 0 {
			return nil, fmt.Errorf("%w: RowSums[%d] 为负", ErrInvalidProblem, i)
		}
		totalRow += v
	}
	for j, v := range colSums {
		if v < 0 {
			return nil, fmt.Errorf("%w: ColSums[%d] 为负", ErrInvalidProblem, j)
		}
		totalCol += v
	}
	if totalRow != totalCol {
		return nil, fmt.Errorf("%w: 行和总计 %d != 列和总计 %d", ErrInvalidProblem, totalRow, totalCol)
	}

	seen := make(map[Cell]bool, len(p.Sensitive))
	sensitive := make([]Cell, 0, len(p.Sensitive))
	for _, cell := range p.Sensitive {
		if cell.Row < 0 || cell.Row >= r || cell.Col < 0 || cell.Col >= c {
			return nil, fmt.Errorf("%w: 敏感单元 (%d,%d) 越界", ErrInvalidProblem, cell.Row, cell.Col)
		}
		if seen[cell] {
			return nil, fmt.Errorf("%w: 敏感单元 (%d,%d) 重复", ErrInvalidProblem, cell.Row, cell.Col)
		}
		seen[cell] = true
		sensitive = append(sensitive, cell)
	}

	return &normalized{
		rows:      r,
		cols:      c,
		lower:     lower,
		upper:     upper,
		cost:      cost,
		rowSums:   rowSums,
		colSums:   colSums,
		sensitive: sensitive,
	}, nil
}
