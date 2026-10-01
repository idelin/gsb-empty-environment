package llk

import (
	"fmt"
	"strings"
)

// ParseError 表示输入串不符合文法（与文法本身是否 LL(k) 无关）。
type ParseError struct {
	Pos      int
	Expected string
	Got      string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("parse error at token %d: expected %s, got %q", e.Pos, e.Expected, e.Got)
}

// Parser 是表驱动的 LL(k) 递归下降解析器：每个非终结符恰好展开一次，
// 每个输入 Token 恰好消费一次，无回溯，时间复杂度 O(n)。
type Parser struct {
	table *Table
	toks  []string
	pos   int
	// Steps 记录预测表查询次数（每次展开一个非终结符计一次）。
	Steps int
}

// NewParser 基于预测表创建解析器。
func NewParser(t *Table) *Parser { return &Parser{table: t} }

// Parse 解析整个 Token 序列，要求恰好消费完全部输入。
func (p *Parser) Parse(tokens []string) error {
	p.toks = tokens
	p.pos = 0
	p.Steps = 0
	if err := p.parseNonTerm(p.table.Start); err != nil {
		return err
	}
	if p.pos != len(p.toks) {
		return &ParseError{Pos: p.pos, Expected: "end of input", Got: p.toks[p.pos]}
	}
	return nil
}

// lookahead 取接下来至多 K 个 Token（不足 K 个时取剩余全部）。
func (p *Parser) lookahead() TokenSeq {
	n := p.table.K
	if rest := len(p.toks) - p.pos; rest < n {
		n = rest
	}
	la := make(TokenSeq, n)
	copy(la, p.toks[p.pos:p.pos+n])
	return la
}

func (p *Parser) parseNonTerm(name string) error {
	p.Steps++
	la := p.lookahead()
	prod := p.table.lookup(name, la)
	if prod == nil {
		return &ParseError{
			Pos:      p.pos,
			Expected: fmt.Sprintf("one of FIRST_%d(%s)", p.table.K, name),
			Got:      strings.Join(la, " "),
		}
	}
	return p.parseRHS(prod.RHS)
}

func (p *Parser) parseRHS(rhs []Symbol) error {
	if len(rhs) == 0 { // ε 产生式
		return nil
	}
	for _, sym := range rhs {
		switch {
		case sym.Epsilon:
			// 不消费任何 Token
		case sym.Term:
			if p.pos >= len(p.toks) {
				return &ParseError{Pos: p.pos, Expected: sym.String(), Got: "EOF"}
			}
			if p.toks[p.pos] != sym.Name {
				return &ParseError{Pos: p.pos, Expected: sym.String(), Got: p.toks[p.pos]}
			}
			p.pos++
		default:
			if err := p.parseNonTerm(sym.Name); err != nil {
				return err
			}
		}
	}
	return nil
}
