package quota

import (
	"fmt"
	"sync"
	"testing"
)

func newTestManager(t *testing.T, globalQuota int64) *Manager {
	t.Helper()
	return NewManager(globalQuota)
}

func mustReserve(t *testing.T, m *Manager, id, tenant string, bytes int64) {
	t.Helper()
	if _, err := m.Reserve(id, tenant, bytes); err != nil {
		t.Fatalf("Reserve(%s) failed: %v", id, err)
	}
}

func mustSettle(t *testing.T, m *Manager, id string, size int64, block string) {
	t.Helper()
	if _, err := m.Settle(id, size, block); err != nil {
		t.Fatalf("Settle(%s) failed: %v", id, err)
	}
}

// 基本流程：预留占额，结算按实际大小计费并释放预留。
func TestReserveSettleBasic(t *testing.T) {
	m := newTestManager(t, 1000)
	if err := m.SetTenantQuota("a", 500); err != nil {
		t.Fatal(err)
	}
	mustReserve(t, m, "r1", "a", 200)

	used, reserved := m.Usage("a")
	if used != 0 || reserved != 200 {
		t.Fatalf("after reserve: used=%d reserved=%d, want 0/200", used, reserved)
	}

	mustSettle(t, m, "r1", 120, "blk-1") // 压缩后比预估小
	used, reserved = m.Usage("a")
	if used != 120 || reserved != 0 {
		t.Fatalf("after settle: used=%d reserved=%d, want 120/0", used, reserved)
	}
	gUsed, gReserved := m.GlobalUsage()
	if gUsed != 120 || gReserved != 0 {
		t.Fatalf("global: used=%d reserved=%d, want 120/0", gUsed, gReserved)
	}
}

// 两级配额：租户超限与全局超限都要拒绝预留。
func TestTwoLevelQuotaOnReserve(t *testing.T) {
	m := newTestManager(t, 300)
	if err := m.SetTenantQuota("a", 100); err != nil {
		t.Fatal(err)
	}
	if err := m.SetTenantQuota("b", 250); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Reserve("r1", "a", 101); err != ErrQuotaExceeded {
		t.Fatalf("tenant quota: got %v, want ErrQuotaExceeded", err)
	}
	mustReserve(t, m, "r2", "a", 100)
	mustReserve(t, m, "r3", "b", 150)
	// 全局已预留 250，再预留 60 超全局 300。
	if _, err := m.Reserve("r4", "b", 60); err != ErrQuotaExceeded {
		t.Fatalf("global quota: got %v, want ErrQuotaExceeded", err)
	}
}

// 预留幂等：同 ID 同参数返回原单；参数不同报冲突。
func TestReserveIdempotent(t *testing.T) {
	m := newTestManager(t, Unlimited)
	r1, err := m.Reserve("r1", "a", 100)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := m.Reserve("r1", "a", 100)
	if err != nil {
		t.Fatalf("idempotent reserve: %v", err)
	}
	if r1.State != r2.State || r1.ReservedBytes != r2.ReservedBytes {
		t.Fatalf("idempotent reserve returned different reservation")
	}
	if _, err := m.Reserve("r1", "a", 200); err != ErrReservationExists {
		t.Fatalf("conflicting reserve: got %v, want ErrReservationExists", err)
	}
	// 幂等重放不得重复占额。
	_, reserved := m.Usage("a")
	if reserved != 100 {
		t.Fatalf("reserved=%d, want 100", reserved)
	}
}

// 场景一：压缩失败。Abort 释放预留且幂等；已中止的预留不可再结算。
func TestCompressionFailureAbort(t *testing.T) {
	m := newTestManager(t, Unlimited)
	mustReserve(t, m, "r1", "a", 500)

	if err := m.Abort("r1"); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if err := m.Abort("r1"); err != nil { // 幂等
		t.Fatalf("abort again: %v", err)
	}
	if err := m.Cancel("r1"); err != nil { // 已释放，取消也幂等成功
		t.Fatalf("cancel after abort: %v", err)
	}
	used, reserved := m.Usage("a")
	if used != 0 || reserved != 0 {
		t.Fatalf("after abort: used=%d reserved=%d, want 0/0", used, reserved)
	}
	if _, err := m.Settle("r1", 100, "blk-x"); err != ErrReservationClosed {
		t.Fatalf("settle aborted: got %v, want ErrReservationClosed", err)
	}
}

// 场景二：重复回执。相同参数的重复结算返回首次结果且不重复计费；
// 参数不同的回执报冲突；取消幂等。
func TestDuplicateReceipts(t *testing.T) {
	m := newTestManager(t, Unlimited)
	mustReserve(t, m, "r1", "a", 300)

	first, err := m.Settle("r1", 180, "blk-1")
	if err != nil {
		t.Fatal(err)
	}
	dup, err := m.Settle("r1", 180, "blk-1")
	if err != nil {
		t.Fatalf("duplicate settle: %v", err)
	}
	if dup.BilledBytes != first.BilledBytes || dup.State != StateSettled {
		t.Fatalf("duplicate settle returned inconsistent result")
	}
	used, _ := m.Usage("a")
	if used != 180 {
		t.Fatalf("used=%d, want 180 (no double billing)", used)
	}

	if _, err := m.Settle("r1", 181, "blk-1"); err != ErrSettleConflict {
		t.Fatalf("conflicting receipt: got %v, want ErrSettleConflict", err)
	}
	if _, err := m.Settle("r1", 180, "blk-2"); err != ErrSettleConflict {
		t.Fatalf("conflicting block: got %v, want ErrSettleConflict", err)
	}
	if err := m.Cancel("r1"); err != ErrReservationClosed {
		t.Fatalf("cancel settled: got %v, want ErrReservationClosed", err)
	}

	// 取消幂等。
	mustReserve(t, m, "r2", "a", 50)
	if err := m.Cancel("r2"); err != nil {
		t.Fatal(err)
	}
	if err := m.Cancel("r2"); err != nil {
		t.Fatalf("cancel again: %v", err)
	}
	_, reserved := m.Usage("a")
	if reserved != 0 {
		t.Fatalf("reserved=%d, want 0", reserved)
	}
}

// 场景三：租户限额调低。预留时合法，结算时限额已调低导致超额：
// 预留单转为 rejected，预留释放，文件不得发布（无块、不计费），
// 重复回执得到同样的拒绝结果。
func TestSettleRejectedAfterQuotaLowered(t *testing.T) {
	m := newTestManager(t, Unlimited)
	if err := m.SetTenantQuota("a", 1000); err != nil {
		t.Fatal(err)
	}
	mustReserve(t, m, "r1", "a", 800)

	if err := m.SetTenantQuota("a", 500); err != nil { // 调低限额
		t.Fatal(err)
	}
	if _, err := m.Settle("r1", 700, "blk-1"); err != ErrQuotaExceeded {
		t.Fatalf("settle over lowered quota: got %v, want ErrQuotaExceeded", err)
	}

	// 文件未发布：块不存在、不计费、预留已释放。
	if _, err := m.BlockOwner("blk-1"); err != ErrBlockNotFound {
		t.Fatalf("rejected file must not be published: got %v", err)
	}
	used, reserved := m.Usage("a")
	if used != 0 || reserved != 0 {
		t.Fatalf("after reject: used=%d reserved=%d, want 0/0", used, reserved)
	}
	r, err := m.Settle("r1", 700, "blk-1") // 重复回执 → 同样的拒绝
	if err != ErrQuotaExceeded || r != nil {
		t.Fatalf("replayed rejected settle: got %v", err)
	}

	// 全局限额调低同样生效。
	if err := m.SetGlobalQuota(100); err != nil {
		t.Fatal(err)
	}
	mustReserve(t, m, "r2", "b", 90)
	if err := m.SetGlobalQuota(50); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Settle("r2", 60, "blk-2"); err != ErrQuotaExceeded {
		t.Fatalf("settle over lowered global quota: got %v, want ErrQuotaExceeded", err)
	}
}

// 共享物理块只向归属方计费：第二个租户引用已有块时计费为 0。
func TestSharedBlockBilledToOwnerOnly(t *testing.T) {
	m := newTestManager(t, Unlimited)
	mustReserve(t, m, "r1", "a", 100)
	mustSettle(t, m, "r1", 100, "blk-shared")

	mustReserve(t, m, "r2", "b", 100)
	r, err := m.Settle("r2", 100, "blk-shared") // 去重命中
	if err != nil {
		t.Fatal(err)
	}
	if r.BilledBytes != 0 {
		t.Fatalf("dedup hit billed=%d, want 0", r.BilledBytes)
	}
	usedA, _ := m.Usage("a")
	usedB, _ := m.Usage("b")
	if usedA != 100 || usedB != 0 {
		t.Fatalf("owner billing: a=%d b=%d, want 100/0", usedA, usedB)
	}
}

// 归属迁移原子性：成功时计费整体转移；迁入方配额不足时整体不生效；
// 非归属方不能迁出；重复迁移幂等。
func TestOwnershipTransferAtomic(t *testing.T) {
	m := newTestManager(t, Unlimited)
	if err := m.SetTenantQuota("b", 150); err != nil {
		t.Fatal(err)
	}
	mustReserve(t, m, "r1", "a", 100)
	mustSettle(t, m, "r1", 100, "blk-1")

	if err := m.TransferOwnership("blk-1", "c", "b"); err != ErrNotOwner {
		t.Fatalf("transfer by non-owner: got %v, want ErrNotOwner", err)
	}
	if err := m.TransferOwnership("blk-1", "a", "b"); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	usedA, _ := m.Usage("a")
	usedB, _ := m.Usage("b")
	if usedA != 0 || usedB != 100 {
		t.Fatalf("after transfer: a=%d b=%d, want 0/100", usedA, usedB)
	}
	if err := m.TransferOwnership("blk-1", "a", "b"); err != nil { // 幂等
		t.Fatalf("idempotent transfer: %v", err)
	}

	// 迁入方配额不足：原子失败，归属与计费不变。
	mustReserve(t, m, "r2", "a", 200)
	mustSettle(t, m, "r2", 200, "blk-2")
	if err := m.TransferOwnership("blk-2", "a", "b"); err != ErrQuotaExceeded {
		t.Fatalf("transfer exceeding quota: got %v, want ErrQuotaExceeded", err)
	}
	owner, _ := m.BlockOwner("blk-2")
	usedA, _ = m.Usage("a")
	if owner != "a" || usedA != 200 {
		t.Fatalf("failed transfer must be atomic: owner=%s usedA=%d", owner, usedA)
	}
}

// 事件账本复算峰值：峰值 = 历史上 (已结算 + 在途预留) 的最大值，
// 且账本回放终态与实时状态一致。
func TestLedgerRecomputesPeaks(t *testing.T) {
	m := newTestManager(t, Unlimited)
	mustReserve(t, m, "r1", "a", 100) // a 在途 100
	mustReserve(t, m, "r2", "a", 50)  // a 在途 150 ← 峰值
	mustSettle(t, m, "r1", 80, "b1")  // a: used 80
	if err := m.Abort("r2"); err != nil {
		t.Fatal(err)
	}
	mustReserve(t, m, "r3", "c", 200) // 全局在途 200 + a used 80 = 280 ← 全局峰值
	mustSettle(t, m, "r3", 150, "b2")

	peaks, err := RecomputePeaks(m.Ledger())
	if err != nil {
		t.Fatal(err)
	}
	if peaks.Tenant["a"] != 150 {
		t.Fatalf("tenant a peak=%d, want 150", peaks.Tenant["a"])
	}
	if peaks.Tenant["c"] != 200 {
		t.Fatalf("tenant c peak=%d, want 200", peaks.Tenant["c"])
	}
	if peaks.Global != 280 {
		t.Fatalf("global peak=%d, want 280", peaks.Global)
	}
	if err := m.VerifyAgainstLedger(); err != nil {
		t.Fatalf("ledger/live divergence: %v", err)
	}
}

// 并发冒烟：多租户并发预留/结算/取消，终态必须与账本一致。
func TestConcurrentSmoke(t *testing.T) {
	m := newTestManager(t, Unlimited)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tenant := fmt.Sprintf("t%d", i%4)
			id := fmt.Sprintf("r%d", i)
			if _, err := m.Reserve(id, tenant, 64); err != nil {
				t.Error(err)
				return
			}
			if i%3 == 0 {
				_ = m.Cancel(id)
				return
			}
			if _, err := m.Settle(id, 48, fmt.Sprintf("blk-%d", i)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if err := m.VerifyAgainstLedger(); err != nil {
		t.Fatal(err)
	}
}
