package main

import (
	"fmt"
	"strings"
)

// Op 是布尔表达式节点类型。
type Op int

const (
	OpLeaf Op = iota
	OpAnd
	OpOr
)

// Node 是验收布尔表达式的抽象语法树节点。
type Node struct {
	Op   Op
	Ref  string // OpLeaf 时引用的证据 ID
	Kids []*Node
}

// ---------- 解析 ----------

type token struct {
	kind string // "ident" "(" ")" "and" "or"
	text string
}

func lex(s string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			toks = append(toks, token{kind: "("})
			i++
		case c == ')':
			toks = append(toks, token{kind: ")"})
			i++
		case c == '&':
			if i+1 < len(s) && s[i+1] == '&' {
				i++
			}
			toks = append(toks, token{kind: "and"})
			i++
		case c == '|':
			if i+1 < len(s) && s[i+1] == '|' {
				i++
			}
			toks = append(toks, token{kind: "or"})
			i++
		default:
			j := i
			for j < len(s) && (isIdentChar(s[j])) {
				j++
			}
			if j == i {
				return nil, fmt.Errorf("无法识别的字符 %q", string(c))
			}
			word := s[i:j]
			switch strings.ToUpper(word) {
			case "AND":
				toks = append(toks, token{kind: "and"})
			case "OR":
				toks = append(toks, token{kind: "or"})
			default:
				toks = append(toks, token{kind: "ident", text: word})
			}
			i = j
		}
	}
	return toks, nil
}

func isIdentChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' || c == ':'
}

type parser struct {
	toks []token
	pos  int
}

// ParseExpr 解析布尔表达式，支持 AND/OR（及 &&/||、&/|）与括号。
func ParseExpr(s string) (*Node, error) {
	toks, err := lex(s)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, fmt.Errorf("空表达式")
	}
	p := &parser{toks: toks}
	n, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("表达式在 %q 处存在多余内容", p.toks[p.pos].text)
	}
	return n, nil
}

func (p *parser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos].kind
	}
	return ""
}

func (p *parser) parseOr() (*Node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	kids := []*Node{left}
	for p.peek() == "or" {
		p.pos++
		n, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		kids = append(kids, n)
	}
	if len(kids) == 1 {
		return left, nil
	}
	return &Node{Op: OpOr, Kids: kids}, nil
}

func (p *parser) parseAnd() (*Node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	kids := []*Node{left}
	for p.peek() == "and" {
		p.pos++
		n, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		kids = append(kids, n)
	}
	if len(kids) == 1 {
		return left, nil
	}
	return &Node{Op: OpAnd, Kids: kids}, nil
}

func (p *parser) parseUnary() (*Node, error) {
	switch p.peek() {
	case "(":
		p.pos++
		n, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek() != ")" {
			return nil, fmt.Errorf("缺少右括号")
		}
		p.pos++
		return n, nil
	case "ident":
		t := p.toks[p.pos]
		p.pos++
		return &Node{Op: OpLeaf, Ref: t.text}, nil
	}
	return nil, fmt.Errorf("表达式缺少操作数")
}

// ---------- 求值 ----------

// EvalMask 按证据位掩码求值。bitOf 给出证据 ID 到位的映射；
// 未登记（如已过期被剔除）的证据恒为 false。
func EvalMask(n *Node, mask uint64, bitOf map[string]int) bool {
	switch n.Op {
	case OpLeaf:
		i, ok := bitOf[n.Ref]
		return ok && mask&(uint64(1)<<uint(i)) != 0
	case OpAnd: // 全部满足才算满足
		for _, k := range n.Kids {
			if !EvalMask(k, mask, bitOf) {
				return false
			}
		}
		return true
	case OpOr: // 任一满足即算满足
		for _, k := range n.Kids {
			if EvalMask(k, mask, bitOf) {
				return true
			}
		}
		return false
	}
	return false
}

// ---------- 解释 ----------

// explNode 记录一次求值的完整轨迹，用于生成满足路径或未满足原因。
type explNode struct {
	node   *Node
	sat    bool
	kids   []*explNode
	detail string // 叶子节点的状态说明
}

// explain 用 leafStatus 获取每个证据叶子的状态（是否满足、状态说明）。
func explain(n *Node, leafStatus func(id string) (bool, string)) *explNode {
	e := &explNode{node: n}
	switch n.Op {
	case OpLeaf:
		e.sat, e.detail = leafStatus(n.Ref)
	case OpAnd:
		e.sat = true
		for _, k := range n.Kids {
			ke := explain(k, leafStatus)
			e.kids = append(e.kids, ke)
			if !ke.sat {
				e.sat = false
			}
		}
	case OpOr:
		for _, k := range n.Kids {
			ke := explain(k, leafStatus)
			e.kids = append(e.kids, ke)
			if ke.sat {
				e.sat = true
			}
		}
	}
	return e
}

// satisfactionPath 渲染已满足表达式的证据路径。
// AND 必须列出全部分支；OR 只取一个已满足分支（任一满足即可）。
func satisfactionPath(e *explNode) string {
	switch e.node.Op {
	case OpLeaf:
		return e.node.Ref
	case OpAnd:
		parts := make([]string, 0, len(e.kids))
		for _, k := range e.kids {
			parts = append(parts, satisfactionPath(k))
		}
		return "(" + strings.Join(parts, " AND ") + ")"
	case OpOr:
		satisfied := 0
		path := ""
		for _, k := range e.kids {
			if k.sat {
				satisfied++
				if path == "" {
					path = satisfactionPath(k)
				}
			}
		}
		if satisfied > 1 {
			return path + fmt.Sprintf("（任一分支满足即可，另有 %d 个分支亦满足）", satisfied-1)
		}
		return path + "（任一分支满足即可）"
	}
	return ""
}

// unsatReason 渲染未满足原因。
// AND 需全部满足，列出失败分支；OR 任一即可，说明所有分支均失败。
func unsatReason(e *explNode) string {
	switch e.node.Op {
	case OpLeaf:
		return e.detail
	case OpAnd:
		parts := make([]string, 0, len(e.kids))
		for _, k := range e.kids {
			if !k.sat {
				parts = append(parts, unsatReason(k))
			}
		}
		return "需全部满足，但 " + strings.Join(parts, "；")
	case OpOr:
		parts := make([]string, 0, len(e.kids))
		for _, k := range e.kids {
			parts = append(parts, unsatReason(k))
		}
		return "任一分支满足即可，但全部不满足： " + strings.Join(parts, "；")
	}
	return ""
}
