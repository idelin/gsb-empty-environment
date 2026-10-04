package main

import (
	"fmt"
	"math/big"
	"sort"
)

// Piece 是一段有理函数 Num/Den，生效区间为 [Start, effEnd)。
// effEnd 为显式 End（若给出），否则为下一段的 Start；最后一段缺省为 +inf。
type Piece struct {
	Start   *big.Rat
	End     *big.Rat // 可为 nil
	Num     Poly
	Den     Poly
	effEnd  *big.Rat // 载入时计算；nil 表示 +inf
	reduced bool
}

// RatString 返回该段有理函数的字符串形式（约化后）。
func (pc *Piece) RatString() string {
	return fmt.Sprintf("(%s)/(%s)", pc.Num.String(), pc.Den.String())
}

// Piecewise 是分段有理函数，断点取值右连续，定义域末端单独包含。
type Piecewise struct {
	Name   string
	Pieces []*Piece
}

// NewPiecewise 排序、约化各段并计算有效端点。
func NewPiecewise(name string, pieces []*Piece) (*Piecewise, error) {
	if len(pieces) == 0 {
		return nil, fmt.Errorf("%s: 至少需要一个分段", name)
	}
	sort.Slice(pieces, func(i, j int) bool { return pieces[i].Start.Cmp(pieces[j].Start) < 0 })
	for i, pc := range pieces {
		if pc.Den.IsZero() {
			return nil, fmt.Errorf("%s 第 %d 段: 分母为零多项式", name, i+1)
		}
		// 约化：消去分子分母公因式（可去奇点），分母化为首一。
		g := pc.Num.GCD(pc.Den)
		if g.Deg() > 0 {
			pc.Num = pc.Num.Div(g)
			pc.Den = pc.Den.Div(g)
		}
		lead := pc.Den.Lead()
		if lead.Cmp(big.NewRat(1, 1)) != 0 {
			inv := new(big.Rat).Inv(lead)
			pc.Num = pc.Num.Scale(inv)
			pc.Den = pc.Den.Scale(inv)
		}
		pc.reduced = true
	}
	for i, pc := range pieces {
		switch {
		case pc.End != nil:
			if pc.End.Cmp(pc.Start) <= 0 {
				return nil, fmt.Errorf("%s 第 %d 段: end 必须大于 start", name, i+1)
			}
			pc.effEnd = pc.End
		case i+1 < len(pieces):
			pc.effEnd = pieces[i+1].Start
		default:
			pc.effEnd = nil // +inf
		}
	}
	return &Piecewise{Name: name, Pieces: pieces}, nil
}

// DomainEnd 返回定义域末端（最后一段 effEnd），可能为 nil（+inf）。
func (pw *Piecewise) DomainEnd() *big.Rat {
	return pw.Pieces[len(pw.Pieces)-1].effEnd
}

// pieceAt 按右连续约定返回 x 所属分段下标；不在定义域内返回 -1。
// 定义域末端（最后一段 effEnd）单独包含，归于最后一段。
func (pw *Piecewise) pieceAt(x *big.Rat) int {
	for i, pc := range pw.Pieces {
		if x.Cmp(pc.Start) < 0 {
			return -1
		}
		if pc.effEnd == nil || x.Cmp(pc.effEnd) < 0 {
			return i
		}
		if i == len(pw.Pieces)-1 && x.Cmp(pc.effEnd) == 0 {
			return i // 末端单独包含
		}
	}
	return -1
}

// Eval 精确求值；x 不在定义域或分母为零（极点）时返回 false。
func (pw *Piecewise) Eval(x *big.Rat) (*big.Rat, bool) {
	i := pw.pieceAt(x)
	if i < 0 {
		return nil, false
	}
	pc := pw.Pieces[i]
	d := pc.Den.Eval(x)
	if d.Sign() == 0 {
		return nil, false
	}
	return new(big.Rat).Quo(pc.Num.Eval(x), d), true
}

// EvalFloat 浮点求值。
func (pw *Piecewise) EvalFloat(x float64) (float64, bool) {
	xr := new(big.Rat).SetFloat64(x)
	i := pw.pieceAt(xr)
	if i < 0 {
		return 0, false
	}
	pc := pw.Pieces[i]
	d := pc.Den.EvalFloat(x)
	if d == 0 {
		return 0, false
	}
	return pc.Num.EvalFloat(x) / d, true
}

// Breakpoints 返回落在开区间 (lo, hi) 内的全部断点（各段起止点）。
func (pw *Piecewise) Breakpoints(lo, hi *big.Rat) []*big.Rat {
	var out []*big.Rat
	for _, pc := range pw.Pieces {
		for _, t := range []*big.Rat{pc.Start, pc.effEnd} {
			if t != nil && t.Cmp(lo) > 0 && t.Cmp(hi) < 0 {
				out = append(out, t)
			}
		}
	}
	return out
}

// pieceRightOf 返回覆盖 x 右侧开区间的分段下标。
// 与 pieceAt 的区别：定义域末端的“单点包含”不意味着覆盖右侧区间。
func (pw *Piecewise) pieceRightOf(x *big.Rat) int {
	i := pw.pieceAt(x)
	if i < 0 {
		return -1
	}
	pc := pw.Pieces[i]
	if pc.effEnd != nil && pc.effEnd.Cmp(x) <= 0 {
		return -1
	}
	return i
}
