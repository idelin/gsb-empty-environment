package billing

import (
	"reflect"
	"testing"
)

// 套餐：
//   basic:  月费 1000 分，含 100 单位，超量 20 分/单位
//   pro:    月费 3000 分，含 500 单位，超量 10 分/单位
//   max:    月费 8000 分，含 2000 单位，超量 5 分/单位
//   cheap:  月费 100 分，含 10000 单位，超量 1 分/单位（仅可从 max 降级到达，不允许）
//
// 升级边：basic->pro, pro->max, basic->max, max->cheap（cheap 仅用于构造非法目录对照，见下）
func catalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := NewCatalog(
		[]Plan{
			{ID: "basic", FeeCents: 1000, Quota: 100, OverageCentsPerUnit: 20},
			{ID: "pro", FeeCents: 3000, Quota: 500, OverageCentsPerUnit: 10},
			{ID: "max", FeeCents: 8000, Quota: 2000, OverageCentsPerUnit: 5},
		},
		[][2]string{{"basic", "pro"}, {"pro", "max"}, {"basic", "max"}},
	)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	return c
}

func settle(t *testing.T, c *Catalog, start string, usage, paid int64) Settlement {
	t.Helper()
	s, err := c.Settle(start, usage, paid)
	if err != nil {
		t.Fatalf("Settle(%s,%d,%d): %v", start, usage, paid, err)
	}
	if err := c.Verify(s); err != nil {
		t.Fatalf("Verify(Settle(%s,%d,%d)): %v", start, usage, paid, err)
	}
	return s
}

// 月中升级追溯覆盖整月用量：pro 的 500 包含量覆盖全部 400 用量，无超量费。
func TestUpgradeRetroactiveCoverage(t *testing.T) {
	c := catalog(t)
	s := settle(t, c, "basic", 400, 1000)

	if s.FinalPlan != "pro" {
		t.Fatalf("FinalPlan = %s, want pro", s.FinalPlan)
	}
	if !reflect.DeepEqual(s.Path, []string{"basic", "pro"}) {
		t.Fatalf("Path = %v", s.Path)
	}
	if s.TotalCents != 3000 {
		t.Fatalf("TotalCents = %d, want 3000（pro 月费，无超量）", s.TotalCents)
	}
	// 已付 1000，不重复扣除：应补 2000，恰为升级追补。
	if s.DueCents != 2000 || s.CreditCents != 0 {
		t.Fatalf("Due=%d Credit=%d, want 2000/0", s.DueCents, s.CreditCents)
	}
	if len(s.Steps) != 1 || s.Steps[0].TopUpCents != 2000 {
		t.Fatalf("Steps = %+v, want single top-up of 2000", s.Steps)
	}
}

// 不升级时超量费可能更贵，库应选出最低应付总额。
func TestPicksMinimumCost(t *testing.T) {
	c := catalog(t)
	// 用量 800：basic=1000+700*20=15000；pro=3000+300*10=6000；max=8000。最优 pro。
	s := settle(t, c, "basic", 800, 1000)
	if s.FinalPlan != "pro" || s.TotalCents != 6000 {
		t.Fatalf("FinalPlan=%s Total=%d, want pro/6000", s.FinalPlan, s.TotalCents)
	}
	// 用量 3000：pro=3000+2500*10=28000；max=8000+1000*5=13000。最优 max。
	s = settle(t, c, "basic", 3000, 1000)
	if s.FinalPlan != "max" || s.TotalCents != 13000 {
		t.Fatalf("FinalPlan=%s Total=%d, want max/13000", s.FinalPlan, s.TotalCents)
	}
	// 直达边 basic->max 一跳即可，路径应为 [basic max]。
	if !reflect.DeepEqual(s.Path, []string{"basic", "max"}) {
		t.Fatalf("Path = %v, want [basic max]", s.Path)
	}
}

// 用量低时原地不动就是最优。
func TestStayWhenCheapest(t *testing.T) {
	c := catalog(t)
	s := settle(t, c, "basic", 50, 1000)
	if s.FinalPlan != "basic" || len(s.Path) != 1 || s.TotalCents != 1000 || s.DueCents != 0 {
		t.Fatalf("unexpected settlement: %+v", s)
	}
}

// 不允许降级绕过限制：cheap 更便宜但无升级边可达，结算不得选它。
func TestNoDowngradeBypass(t *testing.T) {
	c, err := NewCatalog(
		[]Plan{
			{ID: "pro", FeeCents: 3000, Quota: 500, OverageCentsPerUnit: 10},
			{ID: "cheap", FeeCents: 100, Quota: 100000, OverageCentsPerUnit: 1},
		},
		[][2]string{{"cheap", "pro"}}, // 只有 cheap->pro，没有 pro->cheap
	)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	s := settle(t, c, "pro", 400, 3000)
	if s.FinalPlan != "pro" {
		t.Fatalf("downgrade bypassed restriction: FinalPlan=%s", s.FinalPlan)
	}
	if s.TotalCents != 3000 {
		t.Fatalf("TotalCents=%d, want 3000", s.TotalCents)
	}
}

// 溢付不退款，记为 Credit。
func TestOverpaymentBecomesCredit(t *testing.T) {
	c := catalog(t)
	s := settle(t, c, "basic", 10, 5000)
	if s.TotalCents != 1000 || s.DueCents != 0 || s.CreditCents != 4000 {
		t.Fatalf("Total=%d Due=%d Credit=%d, want 1000/0/4000", s.TotalCents, s.DueCents, s.CreditCents)
	}
}

// 升级链：basic -> pro -> max 的逐步追补。
func TestChainedUpgradePath(t *testing.T) {
	c, err := NewCatalog(
		[]Plan{
			{ID: "a", FeeCents: 100, Quota: 0, OverageCentsPerUnit: 100},
			{ID: "b", FeeCents: 200, Quota: 0, OverageCentsPerUnit: 50},
			{ID: "z", FeeCents: 500, Quota: 1000, OverageCentsPerUnit: 1},
		},
		[][2]string{{"a", "b"}, {"b", "z"}}, // 无 a->z 直达
	)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	s := settle(t, c, "a", 10, 100)
	if !reflect.DeepEqual(s.Path, []string{"a", "b", "z"}) {
		t.Fatalf("Path = %v, want [a b z]", s.Path)
	}
	if len(s.Steps) != 2 || s.Steps[0].TopUpCents != 100 || s.Steps[1].TopUpCents != 300 {
		t.Fatalf("Steps = %+v, want top-ups 100 then 300", s.Steps)
	}
	if s.TotalCents != 500 || s.DueCents != 400 {
		t.Fatalf("Total=%d Due=%d, want 500/400", s.TotalCents, s.DueCents)
	}
}

// Verify 拒绝各类篡改。
func TestVerifyRejectsTampering(t *testing.T) {
	c := catalog(t)
	good := settle(t, c, "basic", 400, 1000)

	cases := map[string]func(Settlement) Settlement{
		"伪造降级路径": func(s Settlement) Settlement {
			s.Path = []string{"basic", "ghost"}
			s.FinalPlan = "ghost"
			s.Steps = []Step{{From: "basic", To: "ghost", TopUpCents: 0}}
			return s
		},
		"追补金额不符": func(s Settlement) Settlement {
			s.Steps[0].TopUpCents = 1
			return s
		},
		"少报已付款": func(s Settlement) Settlement {
			s.PaidCents = 500 // 低于 basic 月费
			return s
		},
		"总额不符": func(s Settlement) Settlement {
			s.TotalCents = 1
			return s
		},
		"应补不符": func(s Settlement) Settlement {
			s.DueCents = 0
			return s
		},
		"步骤与路径不符": func(s Settlement) Settlement {
			s.Steps = nil
			return s
		},
	}
	for name, mutate := range cases {
		bad := mutate(good)
		if err := c.Verify(bad); err == nil {
			t.Errorf("%s: Verify 未报错", name)
		}
	}
}

// 非法输入与非法目录。
func TestInvalidInputs(t *testing.T) {
	c := catalog(t)
	if _, err := c.Settle("ghost", 0, 0); err != ErrUnknownPlan {
		t.Errorf("Settle unknown plan: err=%v", err)
	}
	if _, err := c.Settle("basic", -1, 0); err == nil {
		t.Error("negative usage accepted")
	}
	if _, err := c.Settle("basic", 0, 999); err == nil {
		t.Error("paid below plan fee accepted")
	}
	if _, err := NewCatalog([]Plan{{ID: "x", FeeCents: -1}}, nil); err == nil {
		t.Error("negative fee plan accepted")
	}
	if _, err := NewCatalog([]Plan{{ID: "x"}, {ID: "x"}}, nil); err == nil {
		t.Error("duplicate plan accepted")
	}
	if _, err := NewCatalog([]Plan{{ID: "x"}}, [][2]string{{"x", "y"}}); err == nil {
		t.Error("dangling upgrade edge accepted")
	}
}
