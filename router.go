package memberlist

import (
	"errors"
	"sort"
)

// ErrNoHealthyNode 表示没有满足条件的健康节点。
var ErrNoHealthyNode = errors.New("memberlist: no healthy node matches selector")

// Router 基于成员列表提供按属性的动态拓扑子集路由查询。
type Router struct {
	cluster *Cluster
}

// NewRouter 创建路由器。
func NewRouter(c *Cluster) *Router { return &Router{cluster: c} }

// QueryAll 返回满足选择器的健康节点子集，按权重降序、ID 升序排列。
func (r *Router) QueryAll(selector string) ([]Node, error) {
	conds, err := ParseSelector(selector)
	if err != nil {
		return nil, err
	}
	var out []Node
	for _, n := range r.cluster.Snapshot() {
		n := n
		if n.Status != StatusAlive {
			continue
		}
		if Match(&n, conds) {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Meta.Weight != out[j].Meta.Weight {
			return out[i].Meta.Weight > out[j].Meta.Weight
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Query 返回满足选择器的健康且权重最高的节点。
func (r *Router) Query(selector string) (Node, error) {
	nodes, err := r.QueryAll(selector)
	if err != nil {
		return Node{}, err
	}
	if len(nodes) == 0 {
		return Node{}, ErrNoHealthyNode
	}
	return nodes[0], nil
}
