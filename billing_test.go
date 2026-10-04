package billing

import (
	"strings"
	"testing"
)

func standardCatalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := NewCatalog(
		[]Plan{
			{ID: "basic", MonthlyFeeCents: 1000, IncludedUnits: 100, OverageCentsPerUnit: 20},
			{ID: "pro", MonthlyFeeCents: 3000, IncludedUnits: 500, OverageCentsPerUnit: 10},
			{ID: "max", MonthlyFeeCents: 8000, IncludedUnits: 2000, OverageCentsPerUnit: 5},
		},
		[]Upgrade{
			{From: "basic", To: "pro"},
			{From: "pro", To: "max"},
		},
	)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	return c
}

func TestPlanDue(t *testing.T) {
	p := Plan{ID: "basic", MonthlyFeeCents: 1000, IncludedUnits: 100, OverageCentsPerUnit: 20}
	if got := p.Due(100); got != 1000 {
		t.Fatalf("within quota: got %d, want 1000", got)
	}
	if got := p.Due(250); got != 1000+150*20 {
		t.Fatalf("with overage: got %d, want %d", got, 1000+150*20)
	}
}

func TestNewCatalogRejectsInvalid(t *testing.T) {
	plans := []Plan{
		{ID: "a", MonthlyFeeCents: 2000, IncludedUnits: 10, OverageCentsPerUnit: 1},
		{ID: "b", MonthlyFeeCents: 1000, IncludedUnits: 10, OverageCentsPerUnit: 1},
	}
	cases := []struct {
		name     string
		plans    []Plan
		upgrades []Upgrade
		wantErr  string
	}{
		{"duplicate plan", []Plan{plans[0], plans[0]}, nil, "duplicate"},
		{"empty id", []Plan{{ID: "", MonthlyFeeCents: 1}}, nil, "must not be empty"},
		{"negative fee", []Plan{{ID: "x", MonthlyFeeCents: -1}}, nil, "negative"},
		{"unknown source", plans, []Upgrade{{From: "ghost", To: "a"}}, "not a known plan"},
		{"unknown target", plans, []Upgrade{{From: "a", To: "ghost"}}, "not a known plan"},
		{"self upgrade", plans, []Upgrade{{From: "a", To: "a"}}, "self-upgrade"},
		{"downgrade rejected", plans, []Upgrade{{From: "a", To: "b"}}, "downgrades are not allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewCatalog(tc.plans, tc.upgrades)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got err %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

// 月中升级追溯覆盖全月用量：800 单位用量下 pro 最优，
// 已付 = 月初 basic 月费 + 升级差价，追补只扣差额，不重复扣已付。
func TestMinSettlementUpgradeRetroactive(t *testing.T) {
	c := standardCatalog(t)
	s, err := c.MinSettlement("basic", 800, nil)
	if err != nil {
		t.Fatalf("MinSettlement: %v", err)
	}
	if s.FinalPlanID != "pro" {
		t.Fatalf("final plan = %q, want pro", s.FinalPlanID)
	}
	if want := []string{"basic", "pro"}; len(s.Path) != 2 || s.Path[0] != want[0] || s.Path[1] != want[1] {
		t.Fatalf("path = %v, want %v", s.Path, want)
	}
	if s.OverageUnits != 300 {
		t.Fatalf("overage = %d, want 300", s.OverageUnits)
	}
	if s.TotalDueCents != 3000+300*10 {
		t.Fatalf("due = %d, want 6000", s.TotalDueCents)
	}
	if s.PaidCents != 3000 { // 1000 月初 + 2000 升级差价
		t.Fatalf("paid = %d, want 3000", s.PaidCents)
	}
	if s.BackPayCents != 3000 || s.CreditCents != 0 {
		t.Fatalf("back-pay %d / credit %d, want 3000 / 0", s.BackPayCents, s.CreditCents)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 月中已按旧套餐结过超量费：升级追溯后旧超量被新套餐包含量覆盖，
// 已付超出应付的部分记为应退，绝不重复扣除。
func TestMidMonthOverageCreditedNotDoubleCharged(t *testing.T) {
	c := standardCatalog(t)
	prior := &Ledger{}
	if err := prior.Charge(PaymentMidMonthOverage, "basic", 8000, "400 units over basic quota, billed mid-month"); err != nil {
		t.Fatal(err)
	}
	s, err := c.MinSettlement("basic", 800, prior)
	if err != nil {
		t.Fatalf("MinSettlement: %v", err)
	}
	if s.TotalDueCents != 6000 {
		t.Fatalf("due = %d, want 6000", s.TotalDueCents)
	}
	if s.PaidCents != 3000+8000 {
		t.Fatalf("paid = %d, want 11000", s.PaidCents)
	}
	if s.BackPayCents != 0 || s.CreditCents != 5000 {
		t.Fatalf("back-pay %d / credit %d, want 0 / 5000", s.BackPayCents, s.CreditCents)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 用量低时不变更即最优。
func TestStayIsOptimal(t *testing.T) {
	c := standardCatalog(t)
	s, err := c.MinSettlement("basic", 50, nil)
	if err != nil {
		t.Fatalf("MinSettlement: %v", err)
	}
	if s.FinalPlanID != "basic" || len(s.Path) != 1 {
		t.Fatalf("final %q path %v, want basic only", s.FinalPlanID, s.Path)
	}
	if s.TotalDueCents != 1000 || s.PaidCents != 1000 || s.BackPayCents != 0 {
		t.Fatalf("due %d paid %d back-pay %d, want 1000/1000/0", s.TotalDueCents, s.PaidCents, s.BackPayCents)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 大用量时沿链升级到 max；应付取全月追溯口径。
func TestChainUpgradeToMax(t *testing.T) {
	c := standardCatalog(t)
	s, err := c.MinSettlement("basic", 5000, nil)
	if err != nil {
		t.Fatalf("MinSettlement: %v", err)
	}
	// basic: 1000+4900*20=99000; pro: 3000+4500*10=48000; max: 8000+3000*5=23000
	if s.FinalPlanID != "max" || s.TotalDueCents != 23000 {
		t.Fatalf("final %q due %d, want max/23000", s.FinalPlanID, s.TotalDueCents)
	}
	if want := []string{"basic", "pro", "max"}; len(s.Path) != 3 || s.Path[2] != want[2] {
		t.Fatalf("path = %v, want %v", s.Path, want)
	}
	if s.PaidCents != 8000 || s.BackPayCents != 15000 {
		t.Fatalf("paid %d back-pay %d, want 8000/15000", s.PaidCents, s.BackPayCents)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 不允许降级绕过限制：低价高量套餐无法通过降级到达；
// 手工构造的降级路径也会被 SettlePath 拒绝。
func TestDowngradeBypassRejected(t *testing.T) {
	c, err := NewCatalog(
		[]Plan{
			{ID: "basic", MonthlyFeeCents: 1000, IncludedUnits: 100, OverageCentsPerUnit: 50},
			{ID: "pro", MonthlyFeeCents: 3000, IncludedUnits: 500, OverageCentsPerUnit: 10},
			{ID: "loophole", MonthlyFeeCents: 500, IncludedUnits: 100000, OverageCentsPerUnit: 1},
		},
		[]Upgrade{{From: "basic", To: "pro"}},
	)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	// loophole 不可达，最优只能停在 basic/pro。
	s, err := c.MinSettlement("basic", 1000, nil)
	if err != nil {
		t.Fatalf("MinSettlement: %v", err)
	}
	if s.FinalPlanID == "loophole" {
		t.Fatalf("unreachable plan chosen: %v", s.Path)
	}
	if s.FinalPlanID != "pro" || s.TotalDueCents != 3000+500*10 {
		t.Fatalf("final %q due %d, want pro/8000", s.FinalPlanID, s.TotalDueCents)
	}
	// 直接构造降级路径必须报错。
	if _, err := c.SettlePath([]string{"pro", "basic"}, 1000, nil); err == nil ||
		!strings.Contains(err.Error(), "not a registered upgrade") {
		t.Fatalf("downgrade path: got %v, want not-a-registered-upgrade error", err)
	}
	// 目录层面登记降级边也必须报错。
	if _, err := NewCatalog(
		[]Plan{
			{ID: "hi", MonthlyFeeCents: 3000, IncludedUnits: 1, OverageCentsPerUnit: 1},
			{ID: "lo", MonthlyFeeCents: 100, IncludedUnits: 999999, OverageCentsPerUnit: 1},
		},
		[]Upgrade{{From: "hi", To: "lo"}},
	); err == nil || !strings.Contains(err.Error(), "downgrades are not allowed") {
		t.Fatalf("downgrade edge: got %v, want downgrade rejection", err)
	}
}

// 分叉升级图：两条路都能到大套餐，选应付最低且路径合法。
func TestDiamondUpgradeGraph(t *testing.T) {
	c, err := NewCatalog(
		[]Plan{
			{ID: "start", MonthlyFeeCents: 100, IncludedUnits: 10, OverageCentsPerUnit: 100},
			{ID: "a", MonthlyFeeCents: 500, IncludedUnits: 100, OverageCentsPerUnit: 50},
			{ID: "b", MonthlyFeeCents: 600, IncludedUnits: 100, OverageCentsPerUnit: 1},
			{ID: "top", MonthlyFeeCents: 2000, IncludedUnits: 10000, OverageCentsPerUnit: 1},
		},
		[]Upgrade{
			{From: "start", To: "a"},
			{From: "start", To: "b"},
			{From: "a", To: "top"},
			{From: "b", To: "top"},
		},
	)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	s, err := c.MinSettlement("start", 500, nil)
	if err != nil {
		t.Fatalf("MinSettlement: %v", err)
	}
	// start: 100+490*100=49100; a: 500+400*50=20500; b: 600+400=1000; top: 2000
	if s.FinalPlanID != "b" || s.TotalDueCents != 1000 {
		t.Fatalf("final %q due %d, want b/1000", s.FinalPlanID, s.TotalDueCents)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// 大用量时 top 最优，路径必须沿合法升级边（经 a 或 b）。
	s, err = c.MinSettlement("start", 50000, nil)
	if err != nil {
		t.Fatalf("MinSettlement: %v", err)
	}
	if s.FinalPlanID != "top" || len(s.Path) != 3 {
		t.Fatalf("final %q path %v, want top via one hop", s.FinalPlanID, s.Path)
	}
	if !c.hasUpgrade(s.Path[0], s.Path[1]) || !c.hasUpgrade(s.Path[1], s.Path[2]) {
		t.Fatalf("illegal path %v", s.Path)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 全量扫描：对每个起始套餐与多档用量，Verify 恒等式必须成立。
func TestVerifyInvariantAcrossUsage(t *testing.T) {
	c := standardCatalog(t)
	for _, start := range []string{"basic", "pro", "max"} {
		for _, usage := range []int64{0, 1, 100, 101, 500, 501, 2000, 2001, 100000} {
			s, err := c.MinSettlement(start, usage, nil)
			if err != nil {
				t.Fatalf("start %s usage %d: %v", start, usage, err)
			}
			if err := s.Verify(); err != nil {
				t.Fatalf("start %s usage %d: %v", start, usage, err)
			}
			// 最终套餐必须可达且路径合法。
			for i := 0; i+1 < len(s.Path); i++ {
				if !c.hasUpgrade(s.Path[i], s.Path[i+1]) {
					t.Fatalf("start %s usage %d: illegal hop in %v", start, usage, s.Path)
				}
			}
			// 最优性：应付不超过任何可达套餐。
			paths, _ := c.reachablePaths(start)
			for id := range paths {
				if due := c.plans[id].Due(usage); s.TotalDueCents > due {
					t.Fatalf("start %s usage %d: due %d beats chosen %d", start, usage, due, s.TotalDueCents)
				}
			}
		}
	}
}

func TestSettlePathRejectsBadInput(t *testing.T) {
	c := standardCatalog(t)
	if _, err := c.SettlePath(nil, 10, nil); err == nil {
		t.Fatal("empty path: want error")
	}
	if _, err := c.SettlePath([]string{"ghost"}, 10, nil); err == nil {
		t.Fatal("unknown plan: want error")
	}
	if _, err := c.MinSettlement("ghost", 10, nil); err == nil {
		t.Fatal("unknown start: want error")
	}
	if _, err := c.MinSettlement("basic", -1, nil); err == nil {
		t.Fatal("negative usage: want error")
	}
	l := &Ledger{}
	if err := l.Charge(PaymentBackPay, "basic", -5, ""); err == nil {
		t.Fatal("negative charge: want error")
	}
}
