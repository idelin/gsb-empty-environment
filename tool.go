package pathcount

import (
	"errors"
	"fmt"
	"math/big"
)

// DefaultBudget 是单次查询默认的状态展开预算。
const DefaultBudget int64 = 4 << 20

// state 是子集状态记忆的键：当前位于顶点 v，已访问集合为 mask（含 v）。
// 必经点覆盖情况可由 mask & req 推出，无需单独入键。
type state struct {
	v    int
	mask uint32
}

// Result 是计数结果。Known 为 false 表示预算中断，值为未知（不是 0）。
type Result struct {
	Value   *big.Int // Known 为 true 时非 nil
	Known   bool
	Version uint64 // 产生该结果的图版本
}

// Page 是一次分页枚举结果。Total 是与本页同一版本、同一前缀条件下的
// 路径总数，Known 为 false 时 Paths 与 Total 均不可信（置空）。
type Page struct {
	Paths   [][]int
	Total   *big.Int
	Known   bool
	Version uint64
}

// Tool 持有图的一份私有快照与查询参数。计数与分页共享同一份
// 子集状态记忆，因此天然对应同一版本；Update 修改图后会递增版本
// 并清空记忆，保证新旧版本的结果不会混用。
type Tool struct {
	g      *Graph
	s, t   int
	req    uint32
	budget int64

	version uint64
	memo    map[state]*big.Int

	spent   int64 // 本次查询已展开的状态数
	aborted bool  // 本次查询是否因预算耗尽而中断
}

// NewTool 以图 g 的快照构造工具。s、t 为起终点，required 为必经点集合，
// budget 为单次查询的状态展开预算（<=0 时使用 DefaultBudget）。
func NewTool(g *Graph, s, t int, required []int, budget int64) *Tool {
	g.checkVertex(s)
	g.checkVertex(t)
	var req uint32
	for _, v := range required {
		g.checkVertex(v)
		req |= 1 << uint(v)
	}
	if budget <= 0 {
		budget = DefaultBudget
	}
	return &Tool{
		g:      g.clone(),
		s:      s,
		t:      t,
		req:    req,
		budget: budget,
		memo:   make(map[state]*big.Int),
	}
}

// Version 返回当前图版本。
func (t *Tool) Version() uint64 { return t.version }

// SetBudget 调整单次查询的状态展开预算（<=0 时恢复 DefaultBudget）。
// 已缓存的子集状态保留，增大预算后重试会在既有记忆上继续。
func (t *Tool) SetBudget(budget int64) {
	if budget <= 0 {
		budget = DefaultBudget
	}
	t.budget = budget
}

// Update 以 mutate 修改内部图快照，递增版本并清空子集状态记忆。
// 之后所有计数与分页结果都对应新版本。
func (t *Tool) Update(mutate func(g *Graph)) {
	mutate(t.g)
	t.version++
	t.memo = make(map[state]*big.Int)
}

// count 返回从状态 (v, mask) 出发、最终到达 t 且覆盖全部必经点的
// 简单路径条数。known 为 false 表示预算中断，结果未知。
func (t *Tool) count(v int, mask uint32) (c *big.Int, known bool) {
	if v == t.t {
		if mask&t.req == t.req {
			return big.NewInt(1), true
		}
		return big.NewInt(0), true
	}
	st := state{v: v, mask: mask}
	if c, ok := t.memo[st]; ok {
		return c, true
	}
	if t.spent >= t.budget {
		t.aborted = true
		return nil, false
	}
	t.spent++
	sum := new(big.Int)
	adj := t.g.adj[v]
	for w := 0; w < t.g.n; w++ {
		bit := uint32(1) << uint(w)
		if adj&bit == 0 || mask&bit != 0 {
			continue
		}
		sub, ok := t.count(w, mask|bit)
		if !ok {
			return nil, false // 未知结果绝不写入记忆，避免被当成 0
		}
		sum.Add(sum, sub)
	}
	t.memo[st] = sum
	return sum, true
}

// Count 统计 s 到 t、不重复顶点且覆盖全部必经点的路径条数。
// 预算耗尽时返回 Known=false（未知），而不是 0。
func (t *Tool) Count() Result {
	t.spent, t.aborted = 0, false
	c, known := t.count(t.s, uint32(1)<<uint(t.s))
	if !known {
		return Result{Known: false, Version: t.version}
	}
	return Result{Value: c, Known: true, Version: t.version}
}

// checkPrefix 校验前缀：必须从 s 出发、沿边前进、顶点不重复。
// 返回前缀末顶点与已访问掩码。
func (t *Tool) checkPrefix(prefix []int) (int, uint32, error) {
	if len(prefix) == 0 {
		return 0, 0, errors.New("pathcount: prefix must not be empty")
	}
	if prefix[0] != t.s {
		return 0, 0, fmt.Errorf("pathcount: prefix must start at s=%d", t.s)
	}
	var mask uint32
	for i, v := range prefix {
		t.g.checkVertex(v)
		bit := uint32(1) << uint(v)
		if mask&bit != 0 {
			return 0, 0, fmt.Errorf("pathcount: prefix repeats vertex %d", v)
		}
		if i > 0 && t.g.adj[prefix[i-1]]&bit == 0 {
			return 0, 0, fmt.Errorf("pathcount: prefix has no edge %d -> %d", prefix[i-1], v)
		}
		mask |= bit
	}
	return prefix[len(prefix)-1], mask, nil
}

// Enumerate 按字典序（邻居编号升序）枚举满足条件且以 prefix 为前缀的
// 完整路径，跳过 offset 条、最多返回 limit 条。limit<=0 表示不限。
// 返回的 Total 是本前缀条件下的路径总数，与 Count 共享同一版本的记忆，
// 二者必然一致；预算中断时 Known=false，不返回部分结果。
func (t *Tool) Enumerate(prefix []int, offset, limit int) Page {
	v, mask, err := t.checkPrefix(prefix)
	if err != nil {
		return Page{Known: false, Version: t.version}
	}
	t.spent, t.aborted = 0, false

	total, known := t.count(v, mask)
	if !known {
		return Page{Known: false, Version: t.version}
	}

	page := Page{Total: total, Known: true, Version: t.version}
	if limit == 0 || total.Sign() == 0 {
		return page
	}
	if limit < 0 {
		if total.IsInt64() {
			limit = int(total.Int64())
		} else {
			limit = 1 << 30 // 实际上无法在合理时间内枚举完毕
		}
	}

	skip := offset
	path := append([]int(nil), prefix...)
	t.enumerate(v, mask, path, &skip, &limit, &page.Paths)
	if t.aborted {
		return Page{Known: false, Version: t.version}
	}
	return page
}

// enumerate 深度优先按字典序枚举，利用子集计数整棵跳过无需展开的子树。
func (t *Tool) enumerate(v int, mask uint32, path []int, skip, limit *int, out *[][]int) {
	if *limit == 0 || t.aborted {
		return
	}
	if v == t.t {
		if mask&t.req == t.req {
			if *skip > 0 {
				*skip--
				return
			}
			cp := append([]int(nil), path...)
			*out = append(*out, cp)
			*limit--
		}
		return
	}
	adj := t.g.adj[v]
	for w := 0; w < t.g.n; w++ {
		bit := uint32(1) << uint(w)
		if adj&bit == 0 || mask&bit != 0 {
			continue
		}
		c, known := t.count(w, mask|bit)
		if !known {
			return // aborted 已置位
		}
		if *skip > 0 && c.IsInt64() && int64(*skip) >= c.Int64() {
			*skip -= int(c.Int64())
			continue
		}
		t.enumerate(w, mask|bit, append(path, w), skip, limit, out)
		if *limit == 0 || t.aborted {
			return
		}
	}
}
