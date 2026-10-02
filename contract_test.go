package contract

import (
	"math"
	"testing"
)

// 验收边界主场景：
// 合同业务期 [1, 100)，总量 100。
// 知悉 10：录入业务 50 交付 30；
// 知悉 20：作废该交付，并录入业务 40 交付 60；
// 对比知悉 15 与知悉 25 查询业务 60 时的状态与完成度。
func buildAcceptanceContract(t *testing.T) *Contract {
	t.Helper()
	c, err := NewContract(1, 100, 100)
	if err != nil {
		t.Fatalf("NewContract: %v", err)
	}
	if err := c.AdvanceKnowledge(10); err != nil {
		t.Fatalf("AdvanceKnowledge(10): %v", err)
	}
	id, err := c.RecordDelivery(50, 30)
	if err != nil {
		t.Fatalf("RecordDelivery(50,30): %v", err)
	}
	if err := c.AdvanceKnowledge(20); err != nil {
		t.Fatalf("AdvanceKnowledge(20): %v", err)
	}
	if err := c.VoidDelivery(id); err != nil {
		t.Fatalf("VoidDelivery(%d): %v", id, err)
	}
	if _, err := c.RecordDelivery(40, 60); err != nil {
		t.Fatalf("RecordDelivery(40,60): %v", err)
	}
	return c
}

func TestAcceptance_Knowledge15_Business60(t *testing.T) {
	c := buildAcceptanceContract(t)
	snap := c.SnapshotAt(15, 60)
	if snap.State != InProgress {
		t.Errorf("状态 = %v, 期望 履行中", snap.State)
	}
	if snap.Delivered != 30 {
		t.Errorf("累计交付 = %d, 期望 30", snap.Delivered)
	}
	if snap.Completion != 30 {
		t.Errorf("完成度 = %v%%, 期望 30%%", snap.Completion)
	}
}

func TestAcceptance_Knowledge25_Business60(t *testing.T) {
	c := buildAcceptanceContract(t)
	snap := c.SnapshotAt(25, 60)
	if snap.State != InProgress {
		t.Errorf("状态 = %v, 期望 履行中", snap.State)
	}
	if snap.Delivered != 60 {
		t.Errorf("累计交付 = %d, 期望 60（旧凭证已作废，新凭证生效）", snap.Delivered)
	}
	if snap.Completion != 60 {
		t.Errorf("完成度 = %v%%, 期望 60%%", snap.Completion)
	}
}

// 补交历史阶段交付会改变过去业务切片的视图：
// 业务 45 在知悉 15 时尚无交付（原交付在业务 50），
// 知悉 25 时因补交的业务 40 交付而进入履行中。
func TestRetroactiveDeliveryChangesHistoricalSlice(t *testing.T) {
	c := buildAcceptanceContract(t)

	before := c.SnapshotAt(15, 45)
	if before.State != Effective || before.Completion != 0 {
		t.Errorf("知悉15/业务45 = (%v, %v%%), 期望 (生效, 0%%)", before.State, before.Completion)
	}

	after := c.SnapshotAt(25, 45)
	if after.State != InProgress || after.Completion != 60 {
		t.Errorf("知悉25/业务45 = (%v, %v%%), 期望 (履行中, 60%%)", after.State, after.Completion)
	}
}

func TestStateLifecycleAlongBusinessTime(t *testing.T) {
	c := buildAcceptanceContract(t)
	cases := []struct {
		business int64
		want     State
	}{
		{0, Draft},
		{1, Effective}, // 已进入有效期，但业务40交付尚未发生
		{39, Effective},
		{40, InProgress}, // 知悉25视角下业务40交付已知
		{99, InProgress},
		{100, Terminated},
		{150, Terminated},
	}
	for _, tc := range cases {
		knowledge := int64(25)
		if got := c.StateAt(knowledge, tc.business); got != tc.want {
			t.Errorf("StateAt(%d, %d) = %v, 期望 %v", knowledge, tc.business, got, tc.want)
		}
	}
}

func TestVoidTwiceAndUnknownID(t *testing.T) {
	c := buildAcceptanceContract(t)
	if err := c.VoidDelivery(1); err != ErrAlreadyVoided {
		t.Errorf("重复作废 err = %v, 期望 ErrAlreadyVoided", err)
	}
	if err := c.VoidDelivery(999); err != ErrDeliveryNotFound {
		t.Errorf("作废未知凭证 err = %v, 期望 ErrDeliveryNotFound", err)
	}
}

func TestRecordDeliveryOutOfBusinessPeriod(t *testing.T) {
	c, _ := NewContract(1, 100, 100)
	for _, bt := range []int64{0, 100, 101, -5} {
		if _, err := c.RecordDelivery(bt, 10); err == nil {
			t.Errorf("业务时间 %d 应被拒绝", bt)
		}
	}
	if _, err := c.RecordDelivery(50, 0); err == nil {
		t.Error("交付量 0 应被拒绝")
	}
}

func TestCompletionCappedAt100(t *testing.T) {
	c, _ := NewContract(1, 100, 100)
	_ = c.AdvanceKnowledge(5)
	if _, err := c.RecordDelivery(10, 250); err != nil {
		t.Fatal(err)
	}
	if got := c.CompletionAt(5, 99); got != 100 {
		t.Errorf("完成度 = %v%%, 期望封顶 100%%", got)
	}
}

func TestKnowledgeClockMonotonic(t *testing.T) {
	c, _ := NewContract(1, 100, 100)
	_ = c.AdvanceKnowledge(10)
	if err := c.AdvanceKnowledge(5); err == nil {
		t.Error("知悉时钟回退应被拒绝")
	}
}

func TestVoidedDeliveryInvisibleAtExactVoidInstant(t *testing.T) {
	c, _ := NewContract(1, 100, 100)
	_ = c.AdvanceKnowledge(10)
	id, _ := c.RecordDelivery(50, 30)
	_ = c.AdvanceKnowledge(20)
	if err := c.VoidDelivery(id); err != nil {
		t.Fatal(err)
	}
	// 知悉区间 [10, 20)：k=19 可见，k=20 起不可见
	if got := c.DeliveredAt(19, 60); got != 30 {
		t.Errorf("知悉19 累计 = %d, 期望 30", got)
	}
	if got := c.DeliveredAt(20, 60); got != 0 {
		t.Errorf("知悉20 累计 = %d, 期望 0", got)
	}
	if c.deliveries[0].KnownTo != 20 || c.deliveries[0].KnownFrom != 10 {
		t.Errorf("知悉区间 = [%d, %d), 期望 [10, 20)",
			c.deliveries[0].KnownFrom, c.deliveries[0].KnownTo)
	}
	if c.deliveries[0].KnownTo == math.MaxInt64 {
		t.Error("作废后 KnownTo 不应为 MaxInt64")
	}
}
