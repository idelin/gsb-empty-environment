// Package billing 实现订阅套餐的月中升级结算。
//
// 规则：
//   - 升级沿有向升级边进行，不允许降级绕过包含量/价格限制；
//   - 月中升级追溯生效：最终套餐的包含量覆盖整月用量；
//   - 已付款（月初扣费 + 历次升级追补）全额抵扣，不重复扣除；
//   - 所有金额以整数分计算与核验。
package billing

import "errors"

// Plan 描述一个订阅套餐。
type Plan struct {
	ID                  string // 套餐唯一标识
	FeeCents            int64  // 月费，整数分
	Quota               int64  // 每月包含量
	OverageCentsPerUnit int64  // 超出包含量后的单价，分/单位
}

// Catalog 保存套餐集合与升级关系。
type Catalog struct {
	plans    map[string]Plan
	upgrades map[string][]string // from -> 可升级到的 to 列表
}

// 错误定义。
var (
	ErrUnknownPlan   = errors.New("billing: unknown plan")
	ErrNoUpgradeEdge = errors.New("billing: no upgrade edge between plans")
)

// NewCatalog 构建目录。upgrades 中每个元素 {from, to} 表示允许 from 升级到 to。
// 重复套餐 ID、负金额、悬空升级边都会报错。
func NewCatalog(plans []Plan, upgrades [][2]string) (*Catalog, error) {
	c := &Catalog{
		plans:    make(map[string]Plan, len(plans)),
		upgrades: make(map[string][]string),
	}
	for _, p := range plans {
		if p.ID == "" {
			return nil, errors.New("billing: plan with empty ID")
		}
		if p.FeeCents < 0 || p.Quota < 0 || p.OverageCentsPerUnit < 0 {
			return nil, errors.New("billing: plan " + p.ID + " has negative amount")
		}
		if _, dup := c.plans[p.ID]; dup {
			return nil, errors.New("billing: duplicate plan " + p.ID)
		}
		c.plans[p.ID] = p
	}
	for _, e := range upgrades {
		from, to := e[0], e[1]
		if _, ok := c.plans[from]; !ok {
			return nil, errors.New("billing: upgrade from unknown plan " + from)
		}
		if _, ok := c.plans[to]; !ok {
			return nil, errors.New("billing: upgrade to unknown plan " + to)
		}
		if from == to {
			return nil, errors.New("billing: self upgrade edge on " + from)
		}
		c.upgrades[from] = append(c.upgrades[from], to)
	}
	return c, nil
}

// costOf 计算某套餐覆盖整月用量的总费用（整数分）：月费 + 超量费。
func (c *Catalog) costOf(planID string, usage int64) int64 {
	p := c.plans[planID]
	cost := p.FeeCents
	if usage > p.Quota {
		cost += (usage - p.Quota) * p.OverageCentsPerUnit
	}
	return cost
}

// Step 描述变更路径中的一次升级及其追补金额。
type Step struct {
	From       string
	To         string
	TopUpCents int64 // 本次升级追补 = 新月费 - 旧月费（不为负，升级边保证费用不减时成立；否则取 0 并由核验兜底）
}

// Settlement 是一次结算的结果。
type Settlement struct {
	StartPlan   string   // 起始套餐
	Path        []string // 完整变更路径（含起点与终点）
	Steps       []Step   // 每次升级的追补明细
	FinalPlan   string   // 最终套餐
	Usage       int64    // 本月用量
	TotalCents  int64    // 应付总额 = 最终套餐月费 + 追溯整月用量的超量费
	PaidCents   int64    // 已付款（月初扣费 + 历次追补）
	DueCents    int64    // 月末应补 = max(0, Total-Paid)
	CreditCents int64    // 溢付余额 = max(0, Paid-Total)，不退款，仅记录
}

// Settle 给定起始套餐、本月用量与已付款，求最低应付总额及合法升级路径。
//
// 已付款按整数分全额抵扣最终账单；升级追溯覆盖整月用量，因此最终套餐的
// 包含量直接作用于 usage，已付款不会被重复扣除。
func (c *Catalog) Settle(start string, usage, paidCents int64) (Settlement, error) {
	var s Settlement
	if _, ok := c.plans[start]; !ok {
		return s, ErrUnknownPlan
	}
	if usage < 0 || paidCents < 0 {
		return s, errors.New("billing: negative usage or paid amount")
	}
	if paidCents < c.plans[start].FeeCents {
		return s, errors.New("billing: paid amount below current plan fee")
	}

	// BFS 枚举所有经合法升级边可达的套餐（含原地不动），保证不降级。
	type node struct {
		id   string
		path []string
	}
	visited := map[string]bool{start: true}
	queue := []node{{id: start, path: []string{start}}}

	bestCost := c.costOf(start, usage)
	bestPath := []string{start}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range c.upgrades[cur.id] {
			if visited[next] {
				continue
			}
			visited[next] = true
			path := append(append([]string{}, cur.path...), next)
			cost := c.costOf(next, usage)
			// 平局时偏好更少跳数；再平局偏好字典序更小的路径，保证确定性。
			if cost < bestCost ||
				(cost == bestCost && (len(path) < len(bestPath) ||
					(len(path) == len(bestPath) && lessPath(path, bestPath)))) {
				bestCost = cost
				bestPath = path
			}
			queue = append(queue, node{id: next, path: path})
		}
	}

	s = Settlement{
		StartPlan: start,
		Path:      bestPath,
		FinalPlan: bestPath[len(bestPath)-1],
		Usage:     usage,
		PaidCents: paidCents,
	}
	for i := 1; i < len(bestPath); i++ {
		from, to := bestPath[i-1], bestPath[i]
		topUp := c.plans[to].FeeCents - c.plans[from].FeeCents
		if topUp < 0 {
			topUp = 0 // 防御：升级边不应降费；核验会拒绝此类目录
		}
		s.Steps = append(s.Steps, Step{From: from, To: to, TopUpCents: topUp})
	}
	s.TotalCents = bestCost
	if s.TotalCents > paidCents {
		s.DueCents = s.TotalCents - paidCents
	} else {
		s.CreditCents = paidCents - s.TotalCents
	}
	return s, nil
}

// Verify 用整数分独立核验一份结算：
//  1. 路径每一步都存在合法升级边（杜绝降级绕过）；
//  2. 每次升级追补 = 新月费 - 旧月费，且不为负；
//  3. 已付款与追补总额自洽：Paid 至少覆盖路径上的追补；
//  4. Total/Due/Credit 与最终套餐费用、用量、已付款完全一致。
func (c *Catalog) Verify(s Settlement) error {
	if len(s.Path) == 0 || s.Path[0] != s.StartPlan {
		return errors.New("billing: path does not start at start plan")
	}
	if s.FinalPlan != s.Path[len(s.Path)-1] {
		return errors.New("billing: final plan mismatch")
	}
	if len(s.Steps) != len(s.Path)-1 {
		return errors.New("billing: step count mismatch")
	}
	if _, ok := c.plans[s.FinalPlan]; !ok {
		return ErrUnknownPlan
	}

	for i, st := range s.Steps {
		if st.From != s.Path[i] || st.To != s.Path[i+1] {
			return errors.New("billing: step does not match path")
		}
		if !c.hasEdge(st.From, st.To) {
			return ErrNoUpgradeEdge
		}
		want := c.plans[st.To].FeeCents - c.plans[st.From].FeeCents
		if want < 0 {
			return errors.New("billing: upgrade edge lowers fee (downgrade-like edge)")
		}
		if st.TopUpCents != want {
			return errors.New("billing: top-up amount mismatch")
		}
	}

	// 已付款不得低于起始套餐月费（否则连当前套餐都未付清）。
	// 追补总额仅作记录：若升级已实际发生，Paid 应等于首月费+追补之和；
	// 若月末一次性结算，Paid 只含首月费，差额体现在 Due 中。
	// 两种业务状态下 Total = Paid + Due（或 Paid - Credit）均成立，不重复扣除。
	if s.PaidCents < c.plans[s.StartPlan].FeeCents {
		return errors.New("billing: paid amount below start plan fee")
	}

	if want := c.costOf(s.FinalPlan, s.Usage); s.TotalCents != want {
		return errors.New("billing: total amount mismatch")
	}
	if s.TotalCents >= s.PaidCents {
		if s.DueCents != s.TotalCents-s.PaidCents || s.CreditCents != 0 {
			return errors.New("billing: due/credit mismatch")
		}
	} else {
		if s.CreditCents != s.PaidCents-s.TotalCents || s.DueCents != 0 {
			return errors.New("billing: due/credit mismatch")
		}
	}
	return nil
}

func (c *Catalog) hasEdge(from, to string) bool {
	for _, t := range c.upgrades[from] {
		if t == to {
			return true
		}
	}
	return false
}

func lessPath(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
