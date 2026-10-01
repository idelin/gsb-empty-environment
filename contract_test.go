package main

import "testing"

// 构造验收场景：业务期 [1,100)，知悉10 录业务50交付30，
// 知悉20 作废之并录业务40交付45。
func newAcceptanceContract(t *testing.T) *Contract {
	t.Helper()
	c := NewContract(0, Terms{EffectiveFrom: 1, PerformStart: 10, End: 100, TotalAmount: 100})
	id, err := c.RecordDelivery(10, 50, 30)
	if err != nil {
		t.Fatalf("录入交付失败: %v", err)
	}
	if err := c.VoidDelivery(20, id); err != nil {
		t.Fatalf("作废交付失败: %v", err)
	}
	if _, err := c.RecordDelivery(20, 40, 45); err != nil {
		t.Fatalf("录入交付失败: %v", err)
	}
	return c
}

// 验收主用例：对比 (业务60, 知悉15) 与 (业务60, 知悉25)。
func TestAcceptanceBitemporalSlices(t *testing.T) {
	c := newAcceptanceContract(t)

	if got := c.StateAt(60, 15); got != StateInProgress {
		t.Errorf("StateAt(60,15) = %q, 期望 %q", got, StateInProgress)
	}
	if got := c.CompletionAt(60, 15); got != 30 {
		t.Errorf("CompletionAt(60,15) = %v, 期望 30（知悉15 只见业务50的交付30）", got)
	}

	if got := c.StateAt(60, 25); got != StateInProgress {
		t.Errorf("StateAt(60,25) = %q, 期望 %q", got, StateInProgress)
	}
	if got := c.CompletionAt(60, 25); got != 45 {
		t.Errorf("CompletionAt(60,25) = %v, 期望 45（知悉25 业务50已作废，只见业务40的交付45）", got)
	}
}

// 作废后旧凭证在任何知悉>=20 的切片下都不可见。
func TestVoidHidesVoucher(t *testing.T) {
	c := newAcceptanceContract(t)
	for _, v := range c.VouchersAsOf(25) {
		if v.BusinessTime == 50 {
			t.Errorf("知悉25 不应再看到业务50的已作废凭证: %+v", v)
		}
	}
	if n := len(c.VouchersAsOf(15)); n != 1 {
		t.Errorf("知悉15 应看到 1 张凭证，实际 %d", n)
	}
}

// 补交历史阶段：知悉25 下，业务45 切片能看到业务40 的补交交付，业务35 不能。
func TestRetroactiveDelivery(t *testing.T) {
	c := newAcceptanceContract(t)
	if got := c.CompletionAt(45, 25); got != 45 {
		t.Errorf("CompletionAt(45,25) = %v, 期望 45", got)
	}
	if got := c.CompletionAt(35, 25); got != 0 {
		t.Errorf("CompletionAt(35,25) = %v, 期望 0", got)
	}
	if got := c.CompletionAt(55, 15); got != 30 {
		t.Errorf("CompletionAt(55,15) = %v, 期望 30", got)
	}
	if got := c.CompletionAt(45, 15); got != 0 {
		t.Errorf("CompletionAt(45,15) = %v, 期望 0（知悉15 的交付在业务50）", got)
	}
}

// 状态沿业务时间轴演进：草案 -> 生效 -> 履行中 -> 终止。
func TestStateLifecycle(t *testing.T) {
	c := newAcceptanceContract(t)
	cases := []struct {
		business int64
		want     State
	}{
		{0, StateDraft},
		{1, StateEffective},
		{9, StateEffective},
		{10, StateInProgress},
		{99, StateInProgress},
		{100, StateTerminated},
		{150, StateTerminated},
	}
	for _, tc := range cases {
		if got := c.StateAt(tc.business, 25); got != tc.want {
			t.Errorf("StateAt(%d,25) = %q, 期望 %q", tc.business, got, tc.want)
		}
	}
}

// 条款时态变更：知悉30 改合同总量 100 -> 150，历史知悉切片不受影响。
func TestTermsAmendmentIsTemporal(t *testing.T) {
	c := newAcceptanceContract(t)
	c.AmendTerms(30, Terms{EffectiveFrom: 1, PerformStart: 10, End: 100, TotalAmount: 150})

	if got := c.CompletionAt(60, 25); got != 45 {
		t.Errorf("CompletionAt(60,25) = %v, 期望 45（改约前切片不变）", got)
	}
	if got := c.CompletionAt(60, 35); got != 30 {
		t.Errorf("CompletionAt(60,35) = %v, 期望 30（45/150）", got)
	}
	if got := c.StateAt(60, 35); got != StateInProgress {
		t.Errorf("StateAt(60,35) = %q, 期望 %q", got, StateInProgress)
	}
}

// 作废边界：未知凭证报错、重复作废报错。
func TestVoidErrors(t *testing.T) {
	c := newAcceptanceContract(t)
	if err := c.VoidDelivery(30, 999); err == nil {
		t.Error("作废不存在的凭证应报错")
	}
	if err := c.VoidDelivery(30, 1); err == nil {
		t.Error("重复作废应报错")
	}
	if _, err := c.RecordDelivery(30, 60, -5); err == nil {
		t.Error("非正交付量应报错")
	}
}

// 完成度上限截断到 100%。
func TestCompletionCapped(t *testing.T) {
	c := NewContract(0, Terms{EffectiveFrom: 1, PerformStart: 10, End: 100, TotalAmount: 50})
	if _, err := c.RecordDelivery(5, 60, 80); err != nil {
		t.Fatal(err)
	}
	if got := c.CompletionAt(60, 10); got != 100 {
		t.Errorf("CompletionAt = %v, 期望截断为 100", got)
	}
}
