package quota

import (
	"errors"
	"testing"
)

func mustReserve(t *testing.T, s *Store, key, tenant string, bytes int64) Reservation {
	t.Helper()
	res, err := s.Reserve(key, tenant, bytes)
	if err != nil {
		t.Fatalf("Reserve(%s, %d) failed: %v", tenant, bytes, err)
	}
	return res
}

func TestReserveIdempotent(t *testing.T) {
	s := NewStore(1000)
	s.SetTenantLimit("t1", 500)

	r1, err1 := s.Reserve("k1", "t1", 200)
	r2, err2 := s.Reserve("k1", "t1", 200)
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v %v", err1, err2)
	}
	if r1.ID != r2.ID {
		t.Fatalf("duplicate receipt produced different reservations: %s vs %s", r1.ID, r2.ID)
	}
	if u := s.Usage("t1"); u.Reserved != 200 {
		t.Fatalf("reserved counted twice: got %d want 200", u.Reserved)
	}
}

func TestTwoLevelQuota(t *testing.T) {
	s := NewStore(300) // 全局 300
	s.SetTenantLimit("t1", 200)
	s.SetTenantLimit("t2", 200)

	mustReserve(t, s, "k1", "t1", 200)
	if _, err := s.Reserve("k2", "t1", 1); err != ErrTenantLimitExceeded {
		t.Fatalf("tenant limit: got %v", err)
	}
	mustReserve(t, s, "k3", "t2", 100) // 全局恰好打满
	if _, err := s.Reserve("k4", "t2", 1); err != ErrGlobalLimitExceeded {
		t.Fatalf("global limit: got %v", err)
	}
}

func TestSettleBillsActualAndSharedBlocksOnce(t *testing.T) {
	s := NewStore(1000)
	s.SetTenantLimit("t1", 500)
	s.SetTenantLimit("t2", 500)

	r1 := mustReserve(t, s, "k1", "t1", 300)
	published := 0
	err := s.Settle("s1", r1.ID, []Block{{"b1", 100}, {"b2", 80}}, func() error {
		published++
		return nil
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if published != 1 {
		t.Fatalf("publish called %d times", published)
	}
	if u := s.Usage("t1"); u.Used != 180 || u.Reserved != 0 {
		t.Fatalf("t1 usage = %+v, want used=180 reserved=0", u)
	}

	// t2 的文件共享 b1：只向归属方 t1 计费，t2 只计新块 b3。
	r2 := mustReserve(t, s, "k2", "t2", 300)
	if err := s.Settle("s2", r2.ID, []Block{{"b1", 100}, {"b3", 50}}, nil); err != nil {
		t.Fatalf("settle t2: %v", err)
	}
	if u := s.Usage("t2"); u.Used != 50 {
		t.Fatalf("shared block billed to t2: used=%d want 50", u.Used)
	}
	if owner, _ := s.BlockOwner("b1"); owner != "t1" {
		t.Fatalf("b1 owner = %s want t1", owner)
	}
}

func TestSettleOverQuotaNotPublished(t *testing.T) {
	s := NewStore(1000)
	s.SetTenantLimit("t1", 100)

	res := mustReserve(t, s, "k1", "t1", 90)
	published := false
	err := s.Settle("s1", res.ID, []Block{{"b1", 150}}, func() error {
		published = true
		return nil
	})
	if err != ErrTenantLimitExceeded {
		t.Fatalf("got %v want ErrTenantLimitExceeded", err)
	}
	if published {
		t.Fatal("file published despite quota rejection")
	}
	if u := s.Usage("t1"); u.Used != 0 || u.Reserved != 0 {
		t.Fatalf("rejected settle left usage: %+v", u)
	}
	if _, err := s.BlockOwner("b1"); err != ErrBlockNotFound {
		t.Fatal("rejected settle registered block")
	}
	snap, _ := s.Reservation(res.ID)
	if snap.State != StateRejected {
		t.Fatalf("state = %v want rejected", snap.State)
	}
	// 预留已释放，可再次预约。
	mustReserve(t, s, "k2", "t1", 90)
}

func TestSettleDuplicateReceipt(t *testing.T) {
	s := NewStore(1000)
	s.SetTenantLimit("t1", 500)
	res := mustReserve(t, s, "k1", "t1", 200)

	published := 0
	pub := func() error { published++; return nil }
	if err := s.Settle("dup", res.ID, []Block{{"b1", 120}}, pub); err != nil {
		t.Fatalf("first settle: %v", err)
	}
	if err := s.Settle("dup", res.ID, []Block{{"b1", 120}}, pub); err != nil {
		t.Fatalf("duplicate settle returned error: %v", err)
	}
	if published != 1 {
		t.Fatalf("publish ran %d times on duplicate receipt", published)
	}
	if u := s.Usage("t1"); u.Used != 120 {
		t.Fatalf("billed twice: used=%d want 120", u.Used)
	}
	// 不同幂等键对同一预留单结算：拒绝，不重复计费。
	if err := s.Settle("other", res.ID, []Block{{"b9", 10}}, nil); err != ErrReservationNotPending {
		t.Fatalf("got %v want ErrReservationNotPending", err)
	}
	if u := s.Usage("t1"); u.Used != 120 {
		t.Fatalf("double settle changed usage: %d", u.Used)
	}
}

func TestCompressionFailureReleasesReservation(t *testing.T) {
	s := NewStore(100)
	s.SetTenantLimit("t1", 100)

	res := mustReserve(t, s, "k1", "t1", 100) // 打满
	if err := s.Fail("f1", res.ID, "zstd: corrupt input"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	// 幂等：重复失败回执与取消均为空操作。
	if err := s.Fail("f1", res.ID, "zstd: corrupt input"); err != nil {
		t.Fatalf("duplicate fail receipt: %v", err)
	}
	if err := s.Cancel("c1", res.ID); err != nil {
		t.Fatalf("cancel after fail should be no-op: %v", err)
	}
	snap, _ := s.Reservation(res.ID)
	if snap.State != StateFailed {
		t.Fatalf("state = %v want failed", snap.State)
	}
	if u := s.Usage("t1"); u.Reserved != 0 {
		t.Fatalf("reservation not released: %+v", u)
	}
	mustReserve(t, s, "k2", "t1", 100) // 容量已回收
}

func TestCancelIdempotent(t *testing.T) {
	s := NewStore(100)
	res := mustReserve(t, s, "k1", "t1", 50)
	if err := s.Cancel("c1", res.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := s.Cancel("c1", res.ID); err != nil {
		t.Fatalf("duplicate cancel receipt: %v", err)
	}
	if err := s.Cancel("c2", res.ID); err != nil {
		t.Fatalf("cancel of cancelled should be no-op: %v", err)
	}
	if u := s.Usage("t1"); u.Reserved != 0 {
		t.Fatalf("reserved = %d want 0", u.Reserved)
	}
}

func TestTenantLimitReduction(t *testing.T) {
	s := NewStore(1000)
	s.SetTenantLimit("t1", 200)

	r1 := mustReserve(t, s, "k1", "t1", 150)
	if err := s.Settle("s1", r1.ID, []Block{{"b1", 120}}, nil); err != nil {
		t.Fatalf("settle: %v", err)
	}

	// 限额调低到低于当前占用：存量保留，新增一律拒绝。
	s.SetTenantLimit("t1", 100)
	if _, err := s.Reserve("k2", "t1", 1); err != ErrTenantLimitExceeded {
		t.Fatalf("reserve after reduction: got %v", err)
	}
	if u := s.Usage("t1"); u.Used != 120 {
		t.Fatalf("existing usage clawed back: %+v", u)
	}
	// 调低前已存在的预留单，结算也受新限额约束。
	s.SetTenantLimit("t1", 300)
	r2 := mustReserve(t, s, "k3", "t1", 100)
	s.SetTenantLimit("t1", 150) // used=120，结算后 120+90=210 > 150
	if err := s.Settle("s2", r2.ID, []Block{{"b2", 90}}, nil); err != ErrTenantLimitExceeded {
		t.Fatalf("settle under reduced limit: got %v", err)
	}
	if u := s.Usage("t1"); u.Used != 120 || u.Reserved != 0 {
		t.Fatalf("rejected settle changed usage: %+v", u)
	}
}

func TestTransferOwnershipAtomic(t *testing.T) {
	s := NewStore(1000)
	s.SetTenantLimit("t1", 500)
	s.SetTenantLimit("t2", 100)

	r1 := mustReserve(t, s, "k1", "t1", 200)
	if err := s.Settle("s1", r1.ID, []Block{{"b1", 150}}, nil); err != nil {
		t.Fatalf("settle: %v", err)
	}

	// 目标租户装不下：迁移整体失败，源计费不变。
	if err := s.TransferOwnership("x1", "b1", "t1", "t2"); err != ErrTenantLimitExceeded {
		t.Fatalf("got %v want ErrTenantLimitExceeded", err)
	}
	if u := s.Usage("t1"); u.Used != 150 {
		t.Fatalf("failed transfer moved billing: %+v", u)
	}
	if owner, _ := s.BlockOwner("b1"); owner != "t1" {
		t.Fatalf("owner changed on failed transfer: %s", owner)
	}

	s.SetTenantLimit("t2", 200)
	if err := s.TransferOwnership("x2", "b1", "t1", "t2"); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if u1, u2 := s.Usage("t1"), s.Usage("t2"); u1.Used != 0 || u2.Used != 150 {
		t.Fatalf("after transfer t1=%+v t2=%+v", u1, u2)
	}
	if owner, _ := s.BlockOwner("b1"); owner != "t2" {
		t.Fatalf("owner = %s want t2", owner)
	}

	// 幂等重放与「已在目标」空操作。
	if err := s.TransferOwnership("x2", "b1", "t1", "t2"); err != nil {
		t.Fatalf("duplicate transfer receipt: %v", err)
	}
	if err := s.TransferOwnership("x3", "b1", "t1", "t2"); err != nil {
		t.Fatalf("already-owned should be no-op: %v", err)
	}
	// 源不符：拒绝。
	if err := s.TransferOwnership("x4", "b1", "t1", "t1"); err != ErrBlockOwnerMismatch {
		t.Fatalf("got %v want ErrBlockOwnerMismatch", err)
	}
	if u := s.Usage("t2"); u.Used != 150 {
		t.Fatalf("transfer replay double-billed: %+v", u)
	}
}

func TestReplayPeak(t *testing.T) {
	s := NewStore(10000)
	s.SetTenantLimit("t1", 1000)
	s.SetTenantLimit("t2", 1000)

	// t1 预留 800、t2 预留 400 同时存在 -> 全局峰值 1200。
	r1 := mustReserve(t, s, "k1", "t1", 800)
	r2 := mustReserve(t, s, "k2", "t2", 400)
	// t1: 结算 300，峰值 800。
	if err := s.Settle("s1", r1.ID, []Block{{"b1", 300}}, nil); err != nil {
		t.Fatalf("settle: %v", err)
	}
	// t2: 压缩失败释放，峰值 400。
	if err := s.Fail("f1", r2.ID, "boom"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	// t1 再预留 500（used=300，合计 800），取消。
	r3 := mustReserve(t, s, "k3", "t1", 500)
	if err := s.Cancel("c1", r3.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// 迁移 b1(300) 到 t2。
	if err := s.TransferOwnership("x1", "b1", "t1", "t2"); err != nil {
		t.Fatalf("transfer: %v", err)
	}

	peak := s.ReplayPeak()
	if peak.Tenant["t1"] != 800 {
		t.Fatalf("t1 peak = %d want 800", peak.Tenant["t1"])
	}
	if peak.Tenant["t2"] != 400 {
		t.Fatalf("t2 peak = %d want 400", peak.Tenant["t2"])
	}
	// 全局峰值：r1(800) 与 r2(400) 曾同时存在 -> 1200。
	if peak.Global != 1200 {
		t.Fatalf("global peak = %d want 1200", peak.Global)
	}

	// 复算与实时视图一致：当前占用。
	if u := s.Usage("t2"); u.Used != 300 {
		t.Fatalf("t2 used = %d want 300 after transfer", u.Used)
	}
}

func TestSettlePublishFailureDoesNotRollback(t *testing.T) {
	s := NewStore(1000)
	s.SetTenantLimit("t1", 500)
	res := mustReserve(t, s, "k1", "t1", 100)

	boom := errors.New("cdn publish failed")
	err := s.Settle("s1", res.ID, []Block{{"b1", 80}}, func() error { return boom })
	if err != boom {
		t.Fatalf("got %v want publish error", err)
	}
	// 物理字节已落盘：计费保留，预留已结算。
	if u := s.Usage("t1"); u.Used != 80 {
		t.Fatalf("usage rolled back on publish failure: %+v", u)
	}
	// 重复回执返回相同错误，不重复发布。
	if err := s.Settle("s1", res.ID, []Block{{"b1", 80}}, func() error { return nil }); err != boom {
		t.Fatalf("replay got %v want cached publish error", err)
	}
	if u := s.Usage("t1"); u.Used != 80 {
		t.Fatalf("replay double-billed: %+v", u)
	}
}
