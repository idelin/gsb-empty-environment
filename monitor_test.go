package exposure

import (
	"math"
	"strings"
	"testing"
)

const (
	wan  = 10000.0
	wan6 = 600 * wan
	wan7 = 700 * wan
)

// 验收边界：集团总授信 1000 万；子公司 A 借款 600 万；时间 30 追溯上调 A 的
// 历史借款为 700 万；监控器重现时间 30 时总敞口触碰警戒线并输出告警。
func TestAcceptance_RetroactiveCorrectionTriggersAlert(t *testing.T) {
	m := NewMonitor("CNY", 0.65) // 预警线：额度的 65%（650 万）
	m.SetFacility(1000*wan, 0, 100, 0)

	// 时间 10：A 借款 600 万 CNY。
	m.RecordBorrowing("A", wan6, "CNY", 10, 10)

	// 重现时间 20 的知悉状态：总敞口 600 万，占比 60%，未触线。
	if a := m.Evaluate(10, 20); a != nil {
		t.Fatalf("tx=20 时不应告警，得到 %+v", a)
	}
	if hits := m.Replay(20, 0, 20); len(hits) != 0 {
		t.Fatalf("以 tx=20 重放不应有告警，得到 %d 条", len(hits))
	}

	// 时间 30：追溯上调 A 的历史借款为 700 万（业务生效时间仍为 10）。
	m.RecordBorrowing("A", wan7, "CNY", 10, 30)

	// 双时态：历史不被改写——以 tx=20 看 valid=10 仍是 600 万。
	if total, _ := m.ExposureAt(10, 20); total != wan6 {
		t.Fatalf("tx=20 知悉状态下 valid=10 敞口应为 600 万，得到 %.0f", total)
	}
	// 以 tx=30 看同一业务时点已是 700 万。
	if total, _ := m.ExposureAt(10, 30); total != wan7 {
		t.Fatalf("tx=30 知悉状态下 valid=10 敞口应为 700 万，得到 %.0f", total)
	}

	// 监控器重现时间 30：总敞口 700 万 >= 650 万预警线，输出告警。
	hits := m.Replay(30, 0, 30)
	if len(hits) == 0 {
		t.Fatal("重现时间 30 时应触发告警")
	}
	first := hits[0]
	if first.ValidTime != 10 {
		t.Fatalf("首个告警应出现在 valid=10（借款生效时点），得到 %d", first.ValidTime)
	}
	if first.Total != wan7 || first.Limit != 1000*wan {
		t.Fatalf("告警金额不符：total=%.0f limit=%.0f", first.Total, first.Limit)
	}
	if math.Abs(first.Ratio-0.70) > 1e-9 {
		t.Fatalf("告警占比应为 70%%，得到 %.4f", first.Ratio)
	}
	if !strings.Contains(first.Reason, "追溯调整") {
		t.Fatalf("告警应注明由追溯调整触发：%s", first.Reason)
	}

	// 告警日志已记录。
	if len(m.Alerts()) == 0 {
		t.Fatal("告警日志为空")
	}
	t.Logf("告警样例：%s", m.Alerts()[0].Reason)
}

// 双时态版本链：追溯调整关闭旧版本记录区间，历史任意时点可按知悉时间重现。
func TestBitemporalVersionChain(t *testing.T) {
	m := NewMonitor("CNY", 0.65)
	m.SetFacility(1000*wan, 0, 100, 0)
	m.RecordBorrowing("A", wan6, "CNY", 10, 10)
	m.RecordBorrowing("A", wan7, "CNY", 10, 30)

	versions := m.ExposureVersions("A")
	if len(versions) != 2 {
		t.Fatalf("应有 2 个版本，得到 %d", len(versions))
	}
	old, cur := versions[0], versions[1]
	if old.TxFrom != 10 || old.TxTo != 30 || old.Amount != wan6 {
		t.Fatalf("旧版本应为 [10,30) 的 600 万，得到 %+v", old)
	}
	if cur.TxFrom != 30 || cur.TxTo != Infinity || cur.Amount != wan7 {
		t.Fatalf("新版本应为 [30,∞) 的 700 万，得到 %+v", cur)
	}
}

// 多币种：汇率波动直接影响敞口折算。
func TestFXFluctuationChangesExposure(t *testing.T) {
	m := NewMonitor("CNY", 0.65)
	m.SetFacility(1000*wan, 0, 100, 0)
	m.SetFXRate("USD", 6.5, 0)
	m.RecordBorrowing("B", 100*wan, "USD", 5, 5)

	// 汇率 6.5：100 万 USD = 650 万 CNY，恰好触碰 65% 预警线。
	if total, _ := m.ExposureAt(10, 10); total != 650*wan {
		t.Fatalf("汇率 6.5 时敞口应为 650 万，得到 %.0f", total)
	}
	if a := m.Evaluate(10, 10); a == nil {
		t.Fatal("650 万恰好触碰预警线，应告警")
	}

	// 时间 15 汇率升至 7.0：同一笔借款折算为 700 万。
	m.SetFXRate("USD", 7.0, 15)
	if total, _ := m.ExposureAt(20, 20); total != 700*wan {
		t.Fatalf("汇率 7.0 时敞口应为 700 万，得到 %.0f", total)
	}
	// 历史时点仍按当时汇率折算。
	if total, _ := m.ExposureAt(10, 20); total != 650*wan {
		t.Fatalf("valid=10 应按 6.5 折算为 650 万，得到 %.0f", total)
	}
}

// 授信协议有效期：有效期外的敞口为裸露敞口，必须告警。
func TestFacilityExpiry(t *testing.T) {
	m := NewMonitor("CNY", 0.65)
	m.SetFacility(1000*wan, 0, 20, 0)
	m.RecordBorrowing("A", 100*wan, "CNY", 5, 5)

	if a := m.Evaluate(10, 10); a != nil {
		t.Fatalf("有效期内 100 万敞口不应告警，得到 %+v", a)
	}
	a := m.Evaluate(25, 25)
	if a == nil {
		t.Fatal("协议到期后仍有敞口，应告警")
	}
	if !strings.Contains(a.Reason, "不在有效期") {
		t.Fatalf("告警应说明协议失效：%s", a.Reason)
	}
}

// 担保物价值变动与借款追溯调整均触发质押率重算，且全程留痕。
func TestCollateralPledgeRecalcTrace(t *testing.T) {
	m := NewMonitor("CNY", 0.65)
	m.SetFacility(1000*wan, 0, 100, 0)
	m.RecordCollateral("C1", "A", 1000*wan, "CNY", 5, 5)
	m.RecordBorrowing("A", wan6, "CNY", 10, 10)

	// 初始质押率：600 / 1000 = 60%。
	tr := m.RecalcPledgeRatio("A", 10, 10, "初始登记")
	if math.Abs(tr.PledgeRatio-0.60) > 1e-9 {
		t.Fatalf("初始质押率应为 60%%，得到 %.4f", tr.PledgeRatio)
	}

	// 时间 20：担保物贬值至 800 万 -> 质押率重算为 75%。
	m.RecordCollateral("C1", "A", 800*wan, "CNY", 20, 20)
	tr = m.RecalcPledgeRatio("A", 20, 20, "担保物价值变动")
	if math.Abs(tr.PledgeRatio-0.75) > 1e-9 {
		t.Fatalf("贬值后质押率应为 75%%，得到 %.4f", tr.PledgeRatio)
	}

	// 时间 30：借款追溯上调至 700 万 -> 质押率重算为 87.5%。
	m.RecordBorrowing("A", wan7, "CNY", 10, 30)
	tr = m.RecalcPledgeRatio("A", 30, 30, "借款追溯调整")
	if math.Abs(tr.PledgeRatio-0.875) > 1e-9 {
		t.Fatalf("追溯调整后质押率应为 87.5%%，得到 %.4f", tr.PledgeRatio)
	}

	if len(m.PledgeTraces()) != 3 {
		t.Fatalf("应有 3 条质押率追溯记录，得到 %d", len(m.PledgeTraces()))
	}
	// 以 tx=20 的知悉状态回看 valid=10：担保物 1000 万、借款 600 万。
	tr = m.RecalcPledgeRatio("A", 10, 20, "历史重现")
	if math.Abs(tr.PledgeRatio-0.60) > 1e-9 {
		t.Fatalf("以 tx=20 重现 valid=10 质押率应为 60%%，得到 %.4f", tr.PledgeRatio)
	}
}
