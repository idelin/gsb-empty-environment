package monitor

import (
	"strings"
	"testing"
)

// 验收场景：
//   - 集团总授信 1000万，预警线 95%（950万），质押率告警阈值 110%。
//   - 子公司 A 授信协议有效期 [0, 50)，协议额度 1000万。
//   - 记录时间 0：A 借款 600万 USD（业务时间 0 起）；USD 汇率 1.4（业务时间 0 起）；
//     担保物 C1 估值 1200万（业务时间 0 起）。
//   - 记录时间 5：USD 汇率上调为 1.5（业务时间 15 起）——汇率波动影响折算。
//   - 记录时间 20：担保物 C1 估值下调为 800万（业务时间 20 起）——触发质押率重算。
//   - 记录时间 30：追溯上调 A 的历史借款为 700万（业务时间 0 起）。
func newAcceptanceStore() *Store {
	s := NewStore(1000, 0.95, 1.10)
	s.RecordAgreement(0, 0, 50, Agreement{Subsidiary: "A", Limit: 1000})
	s.RecordLoan(0, 0, Loan{ID: "L1", Subsidiary: "A", Currency: "USD", Amount: 600})
	s.RecordFxRate(0, 0, "USD", 1.4)
	s.RecordFxRate(5, 15, "USD", 1.5)
	s.RecordCollateral(0, 0, Collateral{ID: "C1", Subsidiary: "A", Value: 1200})
	s.RecordCollateral(20, 20, Collateral{ID: "C1", Subsidiary: "A", Value: 800})
	return s
}

func hasAlertContaining(r Report, substr string) bool {
	for _, a := range r.Alerts {
		if strings.Contains(a, substr) {
			return true
		}
	}
	return false
}

// 验收边界：时间 30 追溯上调 A 的历史借款为 700万后，
// 监控器重现时间 30 时总敞口 700×1.5=1050万 触碰预警线 950万 并输出告警。
func TestAcceptanceRetroactiveCorrection(t *testing.T) {
	s := newAcceptanceStore()

	// 修正前：记录时间 29 重现业务时间 30，敞口 600×1.5=900万，未触碰预警线。
	before := s.Replay(30, 29)
	if before.Total != 900 {
		t.Fatalf("修正前总敞口 = %v, 期望 900", before.Total)
	}
	if hasAlertContaining(before, "预警线") {
		t.Fatalf("修正前不应有预警线告警: %v", before.Alerts)
	}

	// 记录时间 30：追溯修正历史借款 600万 -> 700万（业务时间 0 起）。
	s.RecordLoan(30, 0, Loan{ID: "L1", Subsidiary: "A", Currency: "USD", Amount: 700})

	// 重现时间 30：总敞口 700×1.5=1050万，触碰预警线 950万 并击穿授信 1000万。
	after := s.Replay(30, 30)
	if after.Total != 1050 {
		t.Fatalf("修正后总敞口 = %v, 期望 1050", after.Total)
	}
	if !hasAlertContaining(after, "触碰预警线") {
		t.Fatalf("修正后应输出预警线告警: %v", after.Alerts)
	}
	if !hasAlertContaining(after, "击穿集团授信额度") {
		t.Fatalf("修正后应输出击穿授信告警: %v", after.Alerts)
	}
	if !hasAlertContaining(after, "超过其授信协议额度") {
		t.Fatalf("修正后应输出子授信额度告警: %v", after.Alerts)
	}
}

// 双时态：追溯修正不改写历史——修正发生后，用修正前的记录时间重现，仍看到旧值。
func TestCorrectionDoesNotRewriteHistory(t *testing.T) {
	s := newAcceptanceStore()
	s.RecordLoan(30, 0, Loan{ID: "L1", Subsidiary: "A", Currency: "USD", Amount: 700})

	asOf29 := s.Replay(30, 29)
	if asOf29.Total != 900 {
		t.Fatalf("记录时间 29 重现的总敞口 = %v, 期望 900（修正不可见）", asOf29.Total)
	}
	asOf30 := s.Replay(30, 30)
	if asOf30.Total != 1050 {
		t.Fatalf("记录时间 30 重现的总敞口 = %v, 期望 1050", asOf30.Total)
	}
}

// 汇率波动影响折算：业务时间 10 适用旧汇率 1.4，业务时间 30 适用新汇率 1.5。
func TestFxFluctuationAffectsExposure(t *testing.T) {
	s := newAcceptanceStore()

	r10 := s.Replay(10, 29)
	if r10.Total != 840 { // 600 × 1.4
		t.Fatalf("业务时间 10 总敞口 = %v, 期望 840", r10.Total)
	}
	r30 := s.Replay(30, 29)
	if r30.Total != 900 { // 600 × 1.5
		t.Fatalf("业务时间 30 总敞口 = %v, 期望 900", r30.Total)
	}
}

// 授信协议有效期影响敞口计入：业务时间 60 协议已过期，敞口不计入。
func TestAgreementExpiryExcludesExposure(t *testing.T) {
	s := newAcceptanceStore()
	s.RecordLoan(30, 0, Loan{ID: "L1", Subsidiary: "A", Currency: "USD", Amount: 700})

	r := s.Replay(60, 30)
	if r.Total != 0 {
		t.Fatalf("协议过期后总敞口 = %v, 期望 0", r.Total)
	}
	if len(r.Details) != 1 || r.Details[0].Counted || !strings.Contains(r.Details[0].Note, "不在有效期") {
		t.Fatalf("协议过期后借款应标记为不计入: %+v", r.Details)
	}
}

// 担保物价值变动引发质押率重算，且重算结果随记录时间可追溯：
//   - 记录时间 15（估值下调未登记）：质押率 900/1200 = 75%，无告警；
//   - 记录时间 25（估值下调已登记）：质押率 900/800 = 112.5%，触发告警；
//   - 记录时间 30（借款追溯修正后）：质押率 1050/800 = 131.25%，触发告警。
func TestCollateralDrivenPledgeRecalc(t *testing.T) {
	s := newAcceptanceStore()

	r15 := s.Replay(30, 15)
	if len(r15.Pledges) != 1 {
		t.Fatalf("应有 1 条质押率重算记录: %+v", r15.Pledges)
	}
	if got := r15.Pledges[0].PledgeRate; got != 0.75 {
		t.Fatalf("记录时间 15 质押率 = %v, 期望 0.75", got)
	}
	if r15.Pledges[0].Alert {
		t.Fatalf("记录时间 15 不应有质押率告警")
	}

	r25 := s.Replay(30, 25)
	if got := r25.Pledges[0].PledgeRate; got != 1.125 {
		t.Fatalf("记录时间 25 质押率 = %v, 期望 1.125", got)
	}
	if !hasAlertContaining(r25, "质押率") {
		t.Fatalf("担保物估值下调后应触发质押率告警: %v", r25.Alerts)
	}

	s.RecordLoan(30, 0, Loan{ID: "L1", Subsidiary: "A", Currency: "USD", Amount: 700})
	r30 := s.Replay(30, 30)
	if got := r30.Pledges[0].PledgeRate; got != 1.3125 {
		t.Fatalf("记录时间 30 质押率 = %v, 期望 1.3125", got)
	}
	if !hasAlertContaining(r30, "质押率") {
		t.Fatalf("追溯修正后应触发质押率告警: %v", r30.Alerts)
	}
	if len(r30.Trace) == 0 || !strings.Contains(r30.Trace[0], "质押率重算") {
		t.Fatalf("应输出质押率重算追溯记录: %v", r30.Trace)
	}
}
