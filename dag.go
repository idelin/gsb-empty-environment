package main

import (
	"errors"
	"math/big"
)

// ---------- 计算图节点 ----------
//
// 共享子表达式用同一个 *Node 指针表示。求值时按指针记忆化，
// 保证共享节点只展开为一个仿射值，其噪声符号在所有使用处一致。

type NodeKind int

const (
	NInput NodeKind = iota
	NConst
	NAdd
	NSub
	NMul
	NDiv
)

type Node struct {
	kind               NodeKind
	name               string
	nominal, errLo, errHi *big.Rat // NInput
	c                  *big.Rat     // NConst
	args               []*Node
}

func InputNode(name, nominal, errLo, errHi string) *Node {
	return &Node{kind: NInput, name: name, nominal: rat(nominal), errLo: rat(errLo), errHi: rat(errHi)}
}

func ConstNode(s string) *Node { return &Node{kind: NConst, c: rat(s)} }

func Add(a, b *Node) *Node { return &Node{kind: NAdd, args: []*Node{a, b}} }
func Sub(a, b *Node) *Node { return &Node{kind: NSub, args: []*Node{a, b}} }
func Mul(a, b *Node) *Node { return &Node{kind: NMul, args: []*Node{a, b}} }
func Div(a, b *Node) *Node { return &Node{kind: NDiv, args: []*Node{a, b}} }

// Eval 用仿射算术求值，memo 跨整个分析共享（输入节点 -> 固定噪声符号）。
func (n *Node) Eval(e *Engine, memo map[*Node]*Affine) (*Affine, error) {
	if v, ok := memo[n]; ok {
		return v, nil
	}
	args := make([]*Affine, len(n.args))
	for i, ch := range n.args {
		a, err := ch.Eval(e, memo)
		if err != nil {
			return nil, err
		}
		args[i] = a
	}
	var res *Affine
	var err error
	switch n.kind {
	case NInput:
		res = e.NewInput(n.nominal, n.errLo, n.errHi, n.name)
	case NConst:
		res = Const(n.c)
	case NAdd:
		res = args[0].Add(args[1])
	case NSub:
		res = args[0].Sub(args[1])
	case NMul:
		res = e.Mul(args[0], args[1])
	case NDiv:
		res, err = e.Div(args[0], args[1])
	}
	if err != nil {
		return nil, err
	}
	memo[n] = res
	return res, nil
}

var errDivZero = errors.New("精确求值时除数为 0")

// EvalExact 在给定输入赋值下用 big.Rat 精确求值（用于枚举验证）。
func (n *Node) EvalExact(env map[*Node]*big.Rat, memo map[*Node]*big.Rat) (*big.Rat, error) {
	if v, ok := memo[n]; ok {
		return v, nil
	}
	var res *big.Rat
	switch n.kind {
	case NInput:
		res = env[n]
	case NConst:
		res = new(big.Rat).Set(n.c)
	default:
		a, err := n.args[0].EvalExact(env, memo)
		if err != nil {
			return nil, err
		}
		b, err := n.args[1].EvalExact(env, memo)
		if err != nil {
			return nil, err
		}
		switch n.kind {
		case NAdd:
			res = new(big.Rat).Add(a, b)
		case NSub:
			res = new(big.Rat).Sub(a, b)
		case NMul:
			res = new(big.Rat).Mul(a, b)
		case NDiv:
			if b.Sign() == 0 {
				return nil, errDivZero
			}
			res = new(big.Rat).Quo(a, b)
		}
	}
	memo[n] = res
	return res, nil
}

// CollectInputs 按 DFS 顺序收集去重后的输入节点。
func CollectInputs(root *Node) []*Node {
	seen := map[*Node]bool{}
	var out []*Node
	var dfs func(n *Node)
	dfs = func(n *Node) {
		if seen[n] {
			return
		}
		seen[n] = true
		if n.kind == NInput {
			out = append(out, n)
		}
		for _, ch := range n.args {
			dfs(ch)
		}
	}
	dfs(root)
	return out
}
