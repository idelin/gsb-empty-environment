// Package llk 实现一个基于固定 K 步前瞻的 LL(k) 预测分析器。
//
// 工作流程：
//  1. 用 Grammar 描述上下文无关文法（终结符 / 非终结符 / 产生式）。
//  2. BuildTable 计算每个候选产生式的 FIRST_k 集合，并构建分支预测表。
//     若同一非终结符的两个候选在 K 步前瞻下不可区分（FIRST_k 交集非空），
//     说明该文法不是 LL(k)，显式返回 *LLKError（文法超界异常）。
//  3. Parser 依据预测表做无回溯的线性递归下降解析。
package llk

import "fmt"

// Symbol 表示文法符号：终结符或非终结符。
type Symbol struct {
	Name    string
	Term    bool // true 表示终结符
	Epsilon bool // true 表示空串 ε
}

// T 构造终结符。
func T(name string) Symbol { return Symbol{Name: name, Term: true} }

// N 构造非终结符。
func N(name string) Symbol { return Symbol{Name: name} }

// Eps 是空串符号 ε。
var Eps = Symbol{Name: "ε", Term: true, Epsilon: true}

func (s Symbol) String() string {
	if s.Epsilon {
		return "ε"
	}
	if s.Term {
		return fmt.Sprintf("%q", s.Name)
	}
	return s.Name
}

// Production 是一条产生式候选：LHS -> RHS。
type Production struct {
	LHS string
	RHS []Symbol // 空切片或 [Eps] 表示 ε 产生式
}

func (p Production) String() string {
	if len(p.RHS) == 0 || (len(p.RHS) == 1 && p.RHS[0].Epsilon) {
		return p.LHS + " -> ε"
	}
	out := p.LHS + " ->"
	for _, s := range p.RHS {
		out += " " + s.String()
	}
	return out
}

// Grammar 是一组产生式，按左部非终结符分组。
type Grammar struct {
	Start string
	Prods map[string][]Production
}

// NewGrammar 以 start 为开始符号构建文法。
func NewGrammar(start string, prods ...Production) *Grammar {
	g := &Grammar{Start: start, Prods: map[string][]Production{}}
	for _, p := range prods {
		g.Prods[p.LHS] = append(g.Prods[p.LHS], p)
	}
	return g
}
