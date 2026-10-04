// Package incjoin 实现多重集（允许重复行）语义下两表等值连接的增量维护。
//
// 插入与删除均按重数计数；一个批次可同时修改左右两表，
// 结果差量严格满足 Δ(L⋈R) = ΔL⋈R + L⋈ΔR + ΔL⋈ΔR（含交叉增量）。
// 任何会使行重数变为负数的批次被整体拒绝，状态保持不变。
package incjoin

import "fmt"

// Row 是表中的一行：Key 为连接键，Val 为负载。
type Row struct {
	Key string
	Val string
}

// OutRow 是连接结果中的一行。
type OutRow struct {
	Key  string
	LVal string
	RVal string
}

// Side 标识批次操作作用于哪张表。
type Side int

const (
	Left Side = iota
	Right
)

// Kind 标识操作类型。
type Kind int

const (
	Insert Kind = iota
	Delete
)

// Op 是批次中的一条操作：对 Side 表插入/删除 Count 个 Row。
type Op struct {
	Side  Side
	Kind  Kind
	Row   Row
	Count int64
}

// Engine 增量维护 L⋈R 的结果。
type Engine struct {
	left   map[Row]int64
	right  map[Row]int64
	result map[OutRow]int64
}

// New 创建一个空引擎。
func New() *Engine {
	return &Engine{
		left:   make(map[Row]int64),
		right:  make(map[Row]int64),
		result: make(map[OutRow]int64),
	}
}

// Apply 原子地应用一个批次，返回结果差量（仅含非零项）。
// 若任一操作非法（Count<=0）或会导致某行重数为负，整个批次被拒绝并返回错误。
func (e *Engine) Apply(ops []Op) (map[OutRow]int64, error) {
	dL := make(map[Row]int64)
	dR := make(map[Row]int64)
	for _, op := range ops {
		if op.Count <= 0 {
			return nil, fmt.Errorf("incjoin: 非法计数 %d（必须为正整数）", op.Count)
		}
		delta := dL
		if op.Side == Right {
			delta = dR
		}
		n := op.Count
		if op.Kind == Delete {
			n = -n
		}
		delta[op.Row] += n
	}
	for row, d := range dL {
		if cur := e.left[row]; cur+d < 0 {
			return nil, fmt.Errorf("incjoin: 删除左表行 %+v 重数 %d 超过现有 %d", row, -d, cur)
		}
	}
	for row, d := range dR {
		if cur := e.right[row]; cur+d < 0 {
			return nil, fmt.Errorf("incjoin: 删除右表行 %+v 重数 %d 超过现有 %d", row, -d, cur)
		}
	}

	out := make(map[OutRow]int64)
	// ΔL⋈R 与交叉项 ΔL⋈ΔR
	for l, dl := range dL {
		for r, cr := range e.right {
			if l.Key == r.Key {
				out[OutRow{l.Key, l.Val, r.Val}] += dl * cr
			}
		}
		for r, dr := range dR {
			if l.Key == r.Key {
				out[OutRow{l.Key, l.Val, r.Val}] += dl * dr
			}
		}
	}
	// L⋈ΔR
	for r, dr := range dR {
		for l, cl := range e.left {
			if l.Key == r.Key {
				out[OutRow{l.Key, l.Val, r.Val}] += cl * dr
			}
		}
	}

	applyDelta(e.left, dL)
	applyDelta(e.right, dR)
	applyDelta(e.result, out)

	delta := make(map[OutRow]int64, len(out))
	for row, n := range out {
		if n != 0 {
			delta[row] = n
		}
	}
	return delta, nil
}

func applyDelta[K comparable](table, delta map[K]int64) {
	for row, d := range delta {
		n := table[row] + d
		if n == 0 {
			delete(table, row)
		} else {
			table[row] = n
		}
	}
}

// Result 返回当前增量维护的连接结果（重数>0 的行）。
func (e *Engine) Result() map[OutRow]int64 {
	out := make(map[OutRow]int64, len(e.result))
	for row, n := range e.result {
		out[row] = n
	}
	return out
}

// FullJoin 从当前两表状态全量重算连接结果，用于对照验证。
func (e *Engine) FullJoin() map[OutRow]int64 {
	out := make(map[OutRow]int64)
	for l, cl := range e.left {
		for r, cr := range e.right {
			if l.Key == r.Key {
				out[OutRow{l.Key, l.Val, r.Val}] += cl * cr
			}
		}
	}
	return out
}
