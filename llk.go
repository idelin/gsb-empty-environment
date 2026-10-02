// Package llk 实现一个基于固定 K 步前瞻的 LL(k) 预测分析器。
//
// 工作流程：
//  1. 为文法每个产生式候选式计算 FIRST_k 集合；
//  2. 若同一规则的两个候选式的 FIRST_k 集合相交（存在长度 <= K 的公共前缀），
//     说明该文法超出 LL(K) 能力边界，构建时显式抛出 GrammarOutOfBoundsError；
//  3. 否则构建“前瞻串 -> 候选式”的分支预测表；
//  4. 解析时采用递归下降，每次只看 K 个 Token 查表决定分支，
//     输入指针单调前进，无回溯，线性时间完成解析。
package llk

import (
	"fmt"
	"strings"
)

// EOF 是输入末尾的填充 Token。
const EOF = "$"

// Symbol 表示产生式中的一个符号（终结符或非终结符）。
type Symbol struct {
	Terminal bool
	Value    string
}

// T 构造终结符。
func T(v string) Symbol { return Symbol{Terminal: true, Value: v} }

// N 构造非终结符。
func N(name string) Symbol { return Symbol{Value: name} }

// Alternative 是一条候选式（符号序列，空序列表示 ε 产生式）。
type Alternative []Symbol

// Rule 表示一条文法规则：Name -> Alts[0] | Alts[1] | ...
type Rule struct {
	Name string
	Alts []Alternative
}

// Grammar 是一组文法规则，Start 为开始符号。
type Grammar struct {
	Start string
	Rules []*Rule
}

func (g *Grammar) rule(name string) *Rule {
	for _, r := range g.Rules {
		if r.Name == name {
			return r
		}
	}
	return nil
}

// GrammarOutOfBoundsError 是“文法超界异常”：
// 当某个规则存在两个候选式，其 FIRST_k 集合相交（即 K 步前瞻无法区分分支）时抛出。
type GrammarOutOfBoundsError struct {
	K         int
	Rule      string
	Lookahead []string // 冲突的公共前瞻串
	AltA      int
	AltB      int
}

func (e *GrammarOutOfBoundsError) Error() string {
	return fmt.Sprintf("文法超界：规则 %s 的候选式 #%d 与 #%d 在前瞻 K=%d 下无法区分（公共前瞻 %q），需要更深的 K",
		e.Rule, e.AltA, e.AltB, e.K, strings.Join(e.Lookahead, " "))
}

// ParseError 表示输入串不符合文法。
type ParseError struct {
	Pos      int
	Rule     string
	Got      []string
	Expected []string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("解析错误：位置 %d，规则 %s 处遇到前瞻 %q，期望之一为 %q",
		e.Pos, e.Rule, strings.Join(e.Got, " "), strings.Join(e.Expected, " | "))
}

// tupleSet 是 Token 元组集合，key 为 join 后的字符串。
type tupleSet map[string][]string

func join(tokens []string) string { return strings.Join(tokens, "\x00") }

func (s tupleSet) add(tuple []string) bool {
	key := join(tuple)
	if _, ok := s[key]; ok {
		return false
	}
	cp := make([]string, len(tuple))
	copy(cp, tuple)
	s[key] = cp
	return true
}

// takeK 截断/保留元组前 k 个元素。
func takeK(tokens []string, k int) []string {
	if len(tokens) > k {
		return tokens[:k]
	}
	return tokens
}

// concatK 计算两个集合的 k 连接：{ take_k(xy(xy) | x∈A, y∈B }。
func concatK(a, b tupleSet, k int) tupleSet {
	out := tupleSet{}
	for _, x := range a {
		for _, y := range b {
			merged := make([]string, 0, len(x)+len(y))
			merged = append(merged, x...)
			merged = append(merged, y...)
			out.add(takeK(merged, k))
		}
	}
	return out
}

// firstKOfSeq 计算符号序列的 FIRST_k 集合。
func firstKOfSeq(seq []Symbol, first map[string]tupleSet, k int) tupleSet {
	result := tupleSet{}
	result.add(nil) // {ε}
	for _, sym := range seq {
		var symFirst tupleSet
		if sym.Terminal {
			symFirst = tupleSet{}
			symFirst.add([]string{sym.Value})
		} else {
			symFirst = first[sym.Value]
			if symFirst == nil {
				symFirst = tupleSet{}
			}
		}
		result = concatK(result, symFirst, k)
	}
	return result
}

// computeFirstK 通过不动点迭代计算每个非终结符的 FIRST_k 集合。
func computeFirstK(g *Grammar, k int) map[string]tupleSet {
	first := map[string]tupleSet{}
	for _, r := range g.Rules {
		first[r.Name] = tupleSet{}
	}
	for changed := true; changed; {
		changed = false
		for _, r := range g.Rules {
			for _, alt := range r.Alts {
				for _, tuple := range firstKOfSeq(alt, first, k) {
					if first[r.Name].add(tuple) {
						changed = true
					}
				}
			}
		}
	}
	return first
}

// padEOF 将长度不足 k 的前瞻元组用 EOF 填充到长度 k。
func padEOF(tuple []string, k int) []string {
	out := make([]string, 0, k)
	out = append(out, tuple...)
	for len(out) < k {
		out = append(out, EOF)
	}
	return out
}

// Parser 是构建好分支预测表的 LL(k) 递归下降分析器。
type Parser struct {
	grammar *Grammar
	k       int
	// table[规则名][前瞻串key] = 候选式下标
	table map[string]map[string]int
	// expect首轮每个规则可选的前瞻串（用于错误提示）
	expected map[string][]string
}

// NewParser 为文法 g 构建 K 步前瞻的预测分析器。
// 若文法不是 LL(K) 的，返回 *GrammarOutOfBoundsError。
func NewParser(g *Grammar, k int) (*Parser, error) {
	if k < 1 {
		return nil, fmt.Errorf("K 必须 >= 1，当前为 %d", k)
	}
	if g.rule(g.Start) == nil {
		return nil, fmt.Errorf("开始符号 %q 没有对应规则", g.Start)
	}
	first := computeFirstK(g, k)
	p := &Parser{
		grammar:  g,
		k:        k,
		table:    map[string]map[string]int{},
		expected: map[string][]string{},
	}
	for _, r := range g.Rules {
		entries := map[string]int{}
		for i, alt := range r.Alts {
			for _, tuple := range firstKOfSeq(alt, first, k) {
				la := padEOF(tuple, k)
				key := join(la)
				if prev, ok := entries[key]; ok {
					return nil, &GrammarOutOfBoundsError{
						K: k, Rule: r.Name,
						Lookahead: la, AltA: prev, AltB: i,
					}
				}
				entries[key] = i
				p.expected[r.Name] = append(p.expected[r.Name], strings.Join(la, " "))
			}
		}
		p.table[r.Name] = entries
	}
	return p, nil
}

// Table 返回规则 name 的分支预测表（前瞻串 -> 候选式下标），用于调试与展示。
func (p *Parser) Table(name string) map[string]int {
	out := map[string]int{}
	for key, alt := range p.table[name] {
		out[strings.Join(strings.Split(key, "\x00"), " ")] = alt
	}
	return out
}

// Node 是解析树节点。
type Node struct {
	Symbol   string
	Terminal bool
	Children []*Node
}

func (n *Node) String() string {
	if n.Terminal {
		return n.Symbol
	}
	parts := make([]string, len(n.Children))
	for i, c := range n.Children {
		parts[i] = c.String()
	}
	return "(" + n.Symbol + " " + strings.Join(parts, " ") + ")"
}

type input struct {
	tokens []string
	pos    int
}

// peek 返回从当前位置起的 k 个 Token，不足时用 EOF 填充。不移动指针。
func (in *input) peek(k int) []string {
	out := make([]string, 0, k)
	for i := 0; i < k; i++ {
		if in.pos+i < len(in.tokens) {
			out = append(out, in.tokens[in.pos+i])
		} else {
			out = append(out, EOF)
		}
	}
	return out
}

// Parse 以开始符号解析 tokens，返回解析树。
// 解析过程只前进不回溯：每个分支决策仅查一次预测表。
func (p *Parser) Parse(tokens []string) (*Node, error) {
	in := &input{tokens: tokens}
	root, err := p.parseNonTerminal(p.grammar.Start, in)
	if err != nil {
		return nil, err
	}
	if in.pos != len(in.tokens) {
		return nil, &ParseError{
			Pos:  in.pos,
			Rule: p.grammar.Start,
			Got:  in.peek(p.k),
			Expected: []string{
				strings.Join(padEOF(nil, p.k), " "),
			},
		}
	}
	return root, nil
}

func (p *Parser) parseNonTerminal(name string, in *input) (*Node, error) {
	la := in.peek(p.k)
	altIdx, ok := p.table[name][join(la)]
	if !ok {
		return nil, &ParseError{
			Pos:      in.pos,
			Rule:     name,
			Got:      la,
			Expected: p.expected[name],
		}
	}
	rule := p.grammar.rule(name)
	node := &Node{Symbol: name}
	for _, sym := range rule.Alts[altIdx] {
		if sym.Terminal {
			if in.pos >= len(in.tokens) || in.tokens[in.pos] != sym.Value {
				return nil, &ParseError{
					Pos:      in.pos,
					Rule:     name,
					Got:      in.peek(p.k),
					Expected: []string{sym.Value},
				}
			}
			in.pos++
			node.Children = append(node.Children, &Node{Symbol: sym.Value, Terminal: true})
		} else {
			child, err := p.parseNonTerminal(sym.Value, in)
			if err != nil {
				return nil, err
			}
			node.Children = append(node.Children, child)
		}
	}
	return node, nil
}
