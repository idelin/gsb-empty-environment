package netsec

import (
	"errors"
	"fmt"
)

// DefaultMaxNodes 是输入图允许的最大节点数。
const DefaultMaxNodes = 1024

var (
	ErrEmptyGraph        = errors.New("netsec: empty graph")
	ErrTooManyNodes      = errors.New("netsec: graph exceeds node limit")
	ErrBadReference      = errors.New("netsec: node reference out of range")
	ErrRefCycle          = errors.New("netsec: reference cycle with no underlying object")
	ErrUnknownKind       = errors.New("netsec: unknown node kind")
	ErrUnknownCapability = errors.New("netsec: unknown capability token")
	ErrCrossTenant       = errors.New("netsec: capability belongs to a different tenant")
	ErrStaleGeneration   = errors.New("netsec: capability token is from a superseded generation")
	ErrRevoked           = errors.New("netsec: capability has been revoked")
)

// Decoder 把攻击者可控的输入图恢复为对象图。
//
// 解码分两个阶段：
//  1. 整图验证：结构（节点数、下标范围、引用环）与全部能力 token
//     （存在、租户匹配、当前代次、未撤销）；
//  2. 原子租约：在注册器同一把锁内复检并取得全部能力租约。
//
// 任一阶段失败都不返回部分对象、不持有任何租约，解码过程本身
// 不调用任何能力。同一 token 在图中出现多次只计一次租约。
type Decoder struct {
	Reg *Registry
	// MaxNodes 覆盖节点上限，零值使用 DefaultMaxNodes。
	MaxNodes int

	// beforeAcquire 是测试用的可控调度点：在整图验证通过、
	// 原子租约获取之前调用，用于精确编排撤销与解码的竞态。
	beforeAcquire func()
}

// Result 是解码成功的产物。Release 释放本次解码取得的全部租约，
// 幂等且并发安全；释放后对象图仍可读，但宿主不应再依据其中的
// 能力句柄发起新调用。
type Result struct {
	Root   *Object
	leases *leaseSet
}

// Release 释放全部能力租约，重复调用无副作用。
func (r *Result) Release() {
	if r == nil || r.leases == nil {
		return
	}
	r.leases.release()
}

// Decode 验证并物化输入图，返回根对象与租约集合。
// 失败时返回的 *Result 恒为 nil。
func (d *Decoder) Decode(tenant string, g *Graph) (*Result, error) {
	if d.Reg == nil {
		return nil, errors.New("netsec: nil registry")
	}
	tokens, err := d.validate(tenant, g)
	if err != nil {
		return nil, err
	}
	if d.beforeAcquire != nil {
		d.beforeAcquire()
	}
	if err := d.Reg.acquireAll(tenant, tokens); err != nil {
		return nil, err
	}
	caps := d.Reg.snapshotCapabilities(tokens)
	return &Result{
		Root:   materialize(g, caps),
		leases: &leaseSet{reg: d.Reg, tokens: tokens},
	}, nil
}

func (d *Decoder) maxNodes() int {
	if d.MaxNodes > 0 {
		return d.MaxNodes
	}
	return DefaultMaxNodes
}

// validate 执行整图验证，返回去重后的能力 token 列表（首次出现序）。
func (d *Decoder) validate(tenant string, g *Graph) ([]string, error) {
	if g == nil || len(g.Nodes) == 0 {
		return nil, ErrEmptyGraph
	}
	if len(g.Nodes) > d.maxNodes() {
		return nil, fmt.Errorf("%w: %d nodes", ErrTooManyNodes, len(g.Nodes))
	}
	n := len(g.Nodes)
	inRange := func(i, idx int) error {
		if idx < 0 || idx >= n {
			return fmt.Errorf("%w: node %d -> %d", ErrBadReference, i, idx)
		}
		return nil
	}

	seen := make(map[string]struct{})
	var tokens []string
	for i := range g.Nodes {
		node := &g.Nodes[i]
		switch node.Kind {
		case KindString:
		case KindArray:
			for _, c := range node.Items {
				if err := inRange(i, c); err != nil {
					return nil, err
				}
			}
		case KindDict:
			for _, c := range node.Fields {
				if err := inRange(i, c); err != nil {
					return nil, err
				}
			}
		case KindRef:
			if err := inRange(i, node.Ref); err != nil {
				return nil, err
			}
		case KindCap:
			if node.Token == "" {
				return nil, fmt.Errorf("%w: empty token at node %d", ErrUnknownCapability, i)
			}
			if _, dup := seen[node.Token]; dup {
				continue
			}
			seen[node.Token] = struct{}{}
			if err := d.Reg.checkToken(tenant, node.Token); err != nil {
				return nil, fmt.Errorf("node %d: %w", i, err)
			}
			tokens = append(tokens, node.Token)
		default:
			return nil, fmt.Errorf("%w: node %d kind %d", ErrUnknownKind, i, int(node.Kind))
		}
	}

	// 纯引用环没有可物化的对象身份，必须拒绝，否则物化阶段无法终止。
	for i := range g.Nodes {
		if g.Nodes[i].Kind != KindRef {
			continue
		}
		j := i
		visited := make(map[int]struct{})
		for g.Nodes[j].Kind == KindRef {
			if _, cyc := visited[j]; cyc {
				return nil, fmt.Errorf("%w: node %d", ErrRefCycle, i)
			}
			visited[j] = struct{}{}
			j = g.Nodes[j].Ref
		}
	}
	return tokens, nil
}

// materialize 物化对象图。同一输入下标恒映射为同一 *Object，
// 从而保持共享与环身份；KindRef 是透明别名，直接返回目标对象。
// 调用前图已通过 validate，下标与引用链均合法。
func materialize(g *Graph, caps map[string]*Capability) *Object {
	memo := make(map[int]*Object, len(g.Nodes))
	var build func(i int) *Object
	build = func(i int) *Object {
		if o, ok := memo[i]; ok {
			return o
		}
		node := &g.Nodes[i]
		if node.Kind == KindRef {
			o := build(node.Ref)
			memo[i] = o
			return o
		}
		o := &Object{Kind: node.Kind}
		memo[i] = o
		switch node.Kind {
		case KindString:
			o.Str = node.Str
		case KindArray:
			o.Items = make([]*Object, len(node.Items))
			for k, c := range node.Items {
				o.Items[k] = build(c)
			}
		case KindDict:
			o.Fields = make(map[string]*Object, len(node.Fields))
			for k, c := range node.Fields {
				o.Fields[k] = build(c)
			}
		case KindCap:
			o.Cap = caps[node.Token]
		}
		return o
	}
	return build(0)
}
