// Package permcompile 是可嵌入 Go 导入宿主的许可编译与执行库。
//
// 所有输入都驻留内存：编译后的导入计划、字节区间请求与回调。库本身不做
// I/O，也不自带加密原语；选择事件的签名校验由宿主通过 Authenticator 回调
// 注入，库负责强制事件与 SessionID、PlanDigest 的绑定。
package permcompile

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// maxPlanDepth 限制嵌套深度，防止恶意深栈。
const maxPlanDepth = 1 << 16

// ObjectID 是不可变对象身份。不同字符串视为不同对象，跨对象读取永不合并。
type ObjectID string

// Generation 是不可变对象的代次；只有代次完全一致的叶子才能服务一次读取。
type Generation uint64

// ByteRange 是左闭右开的 64 位字节区间 [Lo, Hi)。
type ByteRange struct {
	Lo uint64
	Hi uint64
}

// NodeKind 标识计划节点类型。
type NodeKind int

const (
	KindLeaf NodeKind = iota
	KindParallel
	KindSequence
	KindChoice
	KindFallback
)

// Node 是内存中的计划节点。叶子在 Grant 中携带一次性许可。
type Node struct {
	Kind  NodeKind
	Grant *Grant  // 仅 KindLeaf 使用
	Kids  []*Node // 复合节点的子节点
	Gate  string  // 可选门标识；选择/取消事件按 Gate 寻址
	On    string  // 二选一门名，KindChoice 使用
	Opt   string  // 本节点作为选择项时的选项标识
}

// Grant 是叶子上的一次性许可。
type Grant struct {
	Object   ObjectID
	Gen      Generation
	Scope    ByteRange // 授权范围，左闭右开
	MaxReads uint64    // 最大读取次数，必须大于 0
}

// Plan 是一份已签名授权的完整导入计划。
type Plan struct {
	Root        *Node
	SignedHash  []byte // 客户端已签授权中声明的计划摘要（原样保存）
	Description string
}

// PlanDigest 是编译期由计划结构确定的摘要，事件必须绑定该摘要。
type PlanDigest [sha256.Size]byte

// CompiledPlan 是校验并展平后的计划，运行时之间不可变、可共享。
type CompiledPlan struct {
	plan     *Plan
	digest   PlanDigest
	nodes    []flatNode
	leafOf   map[int]int // 节点序号 -> 叶子序号（仅叶子）
	leaves   []grantSpec
	gateOf   map[string]int
	choice   map[int]choiceSpec
	parent   []int // 节点序号 -> 父节点序号，根为 -1
	leafNode []int // 叶子序号 -> 节点序号
}

type flatNode struct {
	kind  NodeKind
	grant int // 叶子许可序号，非叶子为 -1
	kids  []int
	gate  string
	on    string
	opt   string
}

type grantSpec struct {
	object   ObjectID
	gen      Generation
	scope    ByteRange
	maxReads uint64
}

type choiceSpec struct {
	options map[string]int // 选项标识 -> 子节点序号
}

// Digest 返回编译期计划摘要。
func (c *CompiledPlan) Digest() PlanDigest { return c.digest }

// LeafCount 返回展平后的叶子数。
func (c *CompiledPlan) LeafCount() int { return len(c.leaves) }

// Compile 校验计划并展平。任何结构错误都会在编译期被拒绝。
func Compile(p *Plan) (*CompiledPlan, error) {
	if p == nil || p.Root == nil {
		return nil, errors.New("permcompile: plan root is nil")
	}
	cp := &CompiledPlan{
		plan:   p,
		leafOf: map[int]int{},
		gateOf: map[string]int{},
		choice: map[int]choiceSpec{},
	}
	seenGates := map[string]bool{}
	if err := validate(p.Root, 0, seenGates); err != nil {
		return nil, err
	}
	cp.flatten(p.Root)
	cp.digest = digestOf(p)
	return cp, nil
}

func validate(n *Node, depth int, gates map[string]bool) error {
	if n == nil {
		return errors.New("permcompile: nil node")
	}
	if depth > maxPlanDepth {
		return errors.New("permcompile: plan nested too deep")
	}
	switch n.Kind {
	case KindLeaf:
		if n.Grant == nil {
			return errors.New("permcompile: leaf without grant")
		}
		if len(n.Kids) != 0 {
			return errors.New("permcompile: leaf must have no children")
		}
		g := n.Grant
		if g.Scope.Hi <= g.Scope.Lo {
			return errors.New("permcompile: grant scope must be non-empty")
		}
		if g.MaxReads == 0 {
			return errors.New("permcompile: grant MaxReads must be positive")
		}
	case KindParallel, KindSequence, KindChoice, KindFallback:
		if n.Grant != nil {
			return errors.New("permcompile: composite node cannot carry a grant")
		}
		if len(n.Kids) == 0 {
			return errors.New("permcompile: composite node needs children")
		}
		if n.Kind == KindChoice {
			if n.On == "" {
				return errors.New("permcompile: choice node requires On gate name")
			}
			if gates[n.On] {
				return fmt.Errorf("permcompile: duplicate gate id %q", n.On)
			}
			gates[n.On] = true
			if len(n.Kids) != 2 {
				return errors.New("permcompile: choice node must have exactly two options")
			}
			opts := map[string]bool{}
			for _, k := range n.Kids {
				if k.Opt == "" {
					return errors.New("permcompile: choice option requires Opt id")
				}
				if opts[k.Opt] {
					return fmt.Errorf("permcompile: duplicate choice option %q", k.Opt)
				}
				opts[k.Opt] = true
			}
		}
		if n.Kind == KindFallback && len(n.Kids) != 2 {
			return errors.New("permcompile: fallback node needs primary and fallback child")
		}
		if n.Kind == KindFallback && n.Gate == "" {
			return errors.New("permcompile: fallback node requires Gate id for its trigger event")
		}
		for _, k := range n.Kids {
			if err := validate(k, depth+1, gates); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("permcompile: unknown node kind %d", n.Kind)
	}
	if n.Gate != "" {
		if gates[n.Gate] {
			return fmt.Errorf("permcompile: duplicate gate id %q", n.Gate)
		}
		gates[n.Gate] = true
	}
	return nil
}

func (c *CompiledPlan) flatten(n *Node) int {
	idx := len(c.nodes)
	fn := flatNode{kind: n.Kind, grant: -1, gate: n.Gate, on: n.On, opt: n.Opt}
	c.nodes = append(c.nodes, fn)
	c.parent = append(c.parent, -1)
	if n.Kind == KindLeaf {
		leaf := len(c.leaves)
		c.leaves = append(c.leaves, grantSpec{
			object:   n.Grant.Object,
			gen:      n.Grant.Gen,
			scope:    n.Grant.Scope,
			maxReads: n.Grant.MaxReads,
		})
		c.leafOf[idx] = leaf
		c.leafNode = append(c.leafNode, idx)
	}
	for _, k := range n.Kids {
		child := c.flatten(k)
		c.parent[child] = idx
		c.nodes[idx].kids = append(c.nodes[idx].kids, child)
	}
	if n.Gate != "" {
		c.gateOf[n.Gate] = idx
	}
	if n.Kind == KindChoice {
		c.gateOf[n.On] = idx
		spec := choiceSpec{options: map[string]int{}}
		for _, child := range c.nodes[idx].kids {
			spec.options[c.nodes[child].opt] = child
		}
		c.choice[idx] = spec
	}
	return idx
}

// digestOf 对计划结构做确定性摘要；签名原文本身不参与，避免循环依赖。
func digestOf(p *Plan) PlanDigest {
	h := sha256.New()
	var buf [8]byte
	var walk func(n *Node)
	walk = func(n *Node) {
		buf[0] = byte(n.Kind)
		h.Write(buf[:1])
		writeStr := func(s string) {
			binary.BigEndian.PutUint64(buf[:], uint64(len(s)))
			h.Write(buf[:])
			h.Write([]byte(s))
		}
		if n.Kind == KindLeaf {
			g := n.Grant
			writeStr(string(g.Object))
			binary.BigEndian.PutUint64(buf[:], uint64(g.Gen))
			h.Write(buf[:])
			binary.BigEndian.PutUint64(buf[:], g.Scope.Lo)
			h.Write(buf[:])
			binary.BigEndian.PutUint64(buf[:], g.Scope.Hi)
			h.Write(buf[:])
			binary.BigEndian.PutUint64(buf[:], g.MaxReads)
			h.Write(buf[:])
		}
		writeStr(n.Gate)
		writeStr(n.On)
		writeStr(n.Opt)
		for _, k := range n.Kids {
			walk(k)
		}
	}
	walk(p.Root)
	var out PlanDigest
	copy(out[:], h.Sum(nil))
	return out
}
