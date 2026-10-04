// Package billing 实现订阅套餐的月中升级结算。
//
// 模型约定：
//   - 所有金额一律使用整数分（cents），杜绝浮点误差。
//   - 月中升级后，新套餐的包含量追溯覆盖整个账期（本月）用量。
//   - 升级只补差价：新套餐月费减去已付月费，已付款项绝不重复扣除。
//   - 升级关系构成有向图，只允许沿升级边变更；月费降低的边在
//     NewCatalog 阶段即被拒绝，杜绝“降级绕过限制”。
//   - 月末按最终套餐一次性核算：应付 = 最终套餐月费 + 超量费用；
//     追补 = 应付 - 已付（已付超出部分记为应退额度）。
package billing

import (
	"errors"
	"fmt"
	"sort"
)

// Plan 描述一个订阅套餐。
type Plan struct {
	ID                  string // 套餐标识，目录内唯一
	MonthlyFeeCents     int64  // 月费（分）
	IncludedUnits       int64  // 套餐包含量
	OverageCentsPerUnit int64  // 超量单价（分/单位）
}

// OverageUnits 返回 usage 超出套餐包含量的部分。
func (p Plan) OverageUnits(usageUnits int64) int64 {
	if usageUnits <= p.IncludedUnits {
		return 0
	}
	return usageUnits - p.IncludedUnits
}

// Due 计算该套餐覆盖整个账期 usage 时的应付总额（分）。
func (p Plan) Due(usageUnits int64) int64 {
	return p.MonthlyFeeCents + p.OverageUnits(usageUnits)*p.OverageCentsPerUnit
}

// Upgrade 表示一条合法的升级关系：From 可变更为 To。
type Upgrade struct {
	From string
	To   string
}

// Catalog 保存套餐目录与升级关系图。
type Catalog struct {
	plans    map[string]Plan
	upgrades map[string][]string // 邻接表，按字典序排序保证确定性
}

// NewCatalog 构建目录并校验：
//   - 套餐 ID 非空且不重复，费用/包含量/单价非负；
//   - 升级边两端必须存在，不允许自环；
//   - 目标套餐月费不得低于源套餐，否则视为降级，直接拒绝。
func NewCatalog(plans []Plan, upgrades []Upgrade) (*Catalog, error) {
	c := &Catalog{
		plans:    make(map[string]Plan, len(plans)),
		upgrades: make(map[string][]string),
	}
	for _, p := range plans {
		if p.ID == "" {
			return nil, errors.New("billing: plan ID must not be empty")
		}
		if p.MonthlyFeeCents < 0 || p.IncludedUnits < 0 || p.OverageCentsPerUnit < 0 {
			return nil, fmt.Errorf("billing: plan %q has negative fee, quota or overage price", p.ID)
		}
		if _, dup := c.plans[p.ID]; dup {
			return nil, fmt.Errorf("billing: duplicate plan ID %q", p.ID)
		}
		c.plans[p.ID] = p
	}
	for _, u := range upgrades {
		from, ok := c.plans[u.From]
		if !ok {
			return nil, fmt.Errorf("billing: upgrade source %q is not a known plan", u.From)
		}
		to, ok := c.plans[u.To]
		if !ok {
			return nil, fmt.Errorf("billing: upgrade target %q is not a known plan", u.To)
		}
		if u.From == u.To {
			return nil, fmt.Errorf("billing: self-upgrade on plan %q is not allowed", u.From)
		}
		if to.MonthlyFeeCents < from.MonthlyFeeCents {
			return nil, fmt.Errorf("billing: upgrade %q -> %q decreases the monthly fee (%d -> %d cents); downgrades are not allowed",
				u.From, u.To, from.MonthlyFeeCents, to.MonthlyFeeCents)
		}
		c.upgrades[u.From] = append(c.upgrades[u.From], u.To)
	}
	for from := range c.upgrades {
		sort.Strings(c.upgrades[from])
	}
	return c, nil
}

// Plan 返回目录中的套餐。
func (c *Catalog) Plan(id string) (Plan, bool) {
	p, ok := c.plans[id]
	return p, ok
}

// PaymentKind 标识流水类型。
type PaymentKind string

const (
	PaymentMonthStartFee   PaymentKind = "month_start_fee"   // 月初订阅扣费
	PaymentUpgradeDelta    PaymentKind = "upgrade_delta"     // 升级补差价
	PaymentMidMonthOverage PaymentKind = "mid_month_overage" // 月中已结算的超量扣费
	PaymentBackPay         PaymentKind = "back_pay"          // 月末追补
	PaymentRefund          PaymentKind = "refund"            // 月末应退（已付超出应付）
)

// Payment 是一条整数分流水，Cents 恒为非负。
type Payment struct {
	Kind   PaymentKind
	PlanID string
	Cents  int64
	Note   string
}

// Ledger 记录账期内实际已发生的扣款。
type Ledger struct {
	Payments []Payment
}

// Charge 追加一笔扣款，金额必须非负。
func (l *Ledger) Charge(kind PaymentKind, planID string, cents int64, note string) error {
	if cents < 0 {
		return fmt.Errorf("billing: negative charge %d cents for %s", cents, kind)
	}
	l.Payments = append(l.Payments, Payment{Kind: kind, PlanID: planID, Cents: cents, Note: note})
	return nil
}

// TotalCents 返回流水中已付总额（分）。
func (l *Ledger) TotalCents() int64 {
	var total int64
	for _, p := range l.Payments {
		total += p.Cents
	}
	return total
}

// Settlement 是一次月末结算结果。
type Settlement struct {
	UsageUnits    int64     // 账期总用量
	Path          []string  // 合法变更路径，Path[0] 为起始套餐，末位为最终套餐
	FinalPlanID   string    // 最终套餐
	OverageUnits  int64     // 按最终套餐追溯全月用量得到的超量
	TotalDueCents int64     // 最终应付 = 最终套餐月费 + 超量费用
	PaidCents     int64     // 已付款合计（含月费、升级差价、月中已结超量）
	BackPayCents  int64     // 月末追补 = max(0, 应付-已付)
	CreditCents   int64     // 应退额度 = max(0, 已付-应付)
	Ledger        []Payment // 完整流水（含月末结清条目）
}

// Verify 用整数分校验恒等式：已付 + 追补 - 应退 == 应付。
func (s Settlement) Verify() error {
	if s.BackPayCents < 0 || s.CreditCents < 0 {
		return errors.New("billing: negative back-pay or credit")
	}
	if s.BackPayCents > 0 && s.CreditCents > 0 {
		return errors.New("billing: back-pay and credit cannot both be positive")
	}
	if got := s.PaidCents + s.BackPayCents - s.CreditCents; got != s.TotalDueCents {
		return fmt.Errorf("billing: ledger mismatch: paid %d + back-pay %d - credit %d = %d, want due %d",
			s.PaidCents, s.BackPayCents, s.CreditCents, got, s.TotalDueCents)
	}
	return nil
}

// reachablePaths 从 start 出发沿升级边 BFS，返回每个可达套餐的最短路径
// （含 start 自身，路径为 [start]）。邻接表已排序，结果确定。
func (c *Catalog) reachablePaths(start string) (map[string][]string, error) {
	if _, ok := c.plans[start]; !ok {
		return nil, fmt.Errorf("billing: unknown start plan %q", start)
	}
	paths := map[string][]string{start: {start}}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range c.upgrades[cur] {
			if _, seen := paths[next]; seen {
				continue
			}
			path := make([]string, 0, len(paths[cur])+1)
			path = append(path, paths[cur]...)
			path = append(path, next)
			paths[next] = path
			queue = append(queue, next)
		}
	}
	return paths, nil
}

// lessPath 比较两条路径：先比跳数，再按字典序，保证结果可复现。
func lessPath(a, b []string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// MinSettlement 在所有合法变更路径（含不变更）中，求最终应付总额最低的
// 结算方案。prior 为账期内已发生的额外扣款（如月中已结超量），可为 nil；
// 已付款项只抵扣、不重复扣除。
func (c *Catalog) MinSettlement(startPlanID string, usageUnits int64, prior *Ledger) (Settlement, error) {
	paths, err := c.reachablePaths(startPlanID)
	if err != nil {
		return Settlement{}, err
	}
	if usageUnits < 0 {
		return Settlement{}, fmt.Errorf("billing: negative usage %d", usageUnits)
	}
	var best []string
	var bestDue int64
	for id, path := range paths {
		due := c.plans[id].Due(usageUnits)
		if best == nil || due < bestDue || (due == bestDue && lessPath(path, best)) {
			best, bestDue = path, due
		}
	}
	return c.SettlePath(best, usageUnits, prior)
}

// SettlePath 校验给定路径的合法性（每跳必须是已登记的升级关系），
// 并按最终套餐追溯全月用量进行月末结算。
func (c *Catalog) SettlePath(path []string, usageUnits int64, prior *Ledger) (Settlement, error) {
	if len(path) == 0 {
		return Settlement{}, errors.New("billing: empty plan path")
	}
	if usageUnits < 0 {
		return Settlement{}, fmt.Errorf("billing: negative usage %d", usageUnits)
	}
	for _, id := range path {
		if _, ok := c.plans[id]; !ok {
			return Settlement{}, fmt.Errorf("billing: unknown plan %q in path", id)
		}
	}
	for i := 0; i+1 < len(path); i++ {
		if !c.hasUpgrade(path[i], path[i+1]) {
			return Settlement{}, fmt.Errorf("billing: %q -> %q is not a registered upgrade; downgrades are not allowed",
				path[i], path[i+1])
		}
	}

	ledger := Ledger{}
	cur := c.plans[path[0]]
	if err := ledger.Charge(PaymentMonthStartFee, cur.ID, cur.MonthlyFeeCents, "month-start subscription fee"); err != nil {
		return Settlement{}, err
	}
	for i := 1; i < len(path); i++ {
		next := c.plans[path[i]]
		delta := next.MonthlyFeeCents - cur.MonthlyFeeCents // 目录校验保证 >= 0
		note := fmt.Sprintf("upgrade %s -> %s: fee difference, already-paid credited", cur.ID, next.ID)
		if err := ledger.Charge(PaymentUpgradeDelta, next.ID, delta, note); err != nil {
			return Settlement{}, err
		}
		cur = next
	}
	if prior != nil {
		ledger.Payments = append(ledger.Payments, prior.Payments...)
	}

	final := c.plans[path[len(path)-1]]
	due := final.Due(usageUnits)
	paid := ledger.TotalCents()

	s := Settlement{
		UsageUnits:    usageUnits,
		Path:          append([]string(nil), path...),
		FinalPlanID:   final.ID,
		OverageUnits:  final.OverageUnits(usageUnits),
		TotalDueCents: due,
		PaidCents:     paid,
	}
	switch {
	case due > paid:
		s.BackPayCents = due - paid
		ledger.Payments = append(ledger.Payments, Payment{
			Kind: PaymentBackPay, PlanID: final.ID, Cents: s.BackPayCents,
			Note: "month-end back-pay: due minus already-paid",
		})
	case paid > due:
		s.CreditCents = paid - due
		ledger.Payments = append(ledger.Payments, Payment{
			Kind: PaymentRefund, PlanID: final.ID, Cents: s.CreditCents,
			Note: "month-end refund: already-paid exceeds retroactive due",
		})
	}
	s.Ledger = ledger.Payments
	return s, nil
}

func (c *Catalog) hasUpgrade(from, to string) bool {
	for _, next := range c.upgrades[from] {
		if next == to {
			return true
		}
	}
	return false
}
