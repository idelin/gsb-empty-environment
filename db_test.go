package ssi

import "testing"

// 验收场景：A=100, B=100，约束 A+B>=0。
// T1 读 A、B 并扣 A 80；T2 并发读 A、B 并扣 B 80。
// SSI 必须精准识别危险结构并中止 T2。
func TestSSIAbortsWriteSkew(t *testing.T) {
	db := New()
	db.Put("A", 100)
	db.Put("B", 100)

	t1 := db.Begin()
	t2 := db.Begin()

	// T1：读 A、B，检查约束后扣 A 80
	a1, err := t1.Get("A")
	if err != nil {
		t.Fatal(err)
	}
	b1, err := t1.Get("B")
	if err != nil {
		t.Fatal(err)
	}
	if a1+b1-80 < 0 {
		t.Fatal("T1 约束检查不应失败")
	}
	if err := t1.Set("A", a1-80); err != nil {
		t.Fatal(err)
	}

	// T2：基于同一快照读 A、B，看到 sum=200
	a2, err := t2.Get("A")
	if err != nil {
		t.Fatal(err)
	}
	b2, err := t2.Get("B")
	if err != nil {
		t.Fatal(err)
	}
	if a2 != 100 || b2 != 100 {
		t.Fatalf("T2 应读到快照 A=100 B=100，实际 A=%d B=%d", a2, b2)
	}

	// T1 先提交
	if err := t1.Commit(); err != nil {
		t.Fatalf("T1 应成功提交: %v", err)
	}

	// T2 扣 B 80：其写入与 T1 的 SIREAD 锁构成危险结构
	// T1 -rw-> T2 -rw-> T1，必须在此刻被精准中止。
	err = t2.Set("B", b2-80)
	if err != ErrSerialization {
		t.Fatalf("T2.Set 应返回 ErrSerialization，实际 %v", err)
	}
	if err := t2.Commit(); err != ErrAborted {
		t.Fatalf("T2.Commit 应返回 ErrAborted，实际 %v", err)
	}

	// 最终状态：只有 T1 生效，约束保持。
	if got := db.Get("A"); got != 20 {
		t.Fatalf("A 应为 20，实际 %d", got)
	}
	if got := db.Get("B"); got != 100 {
		t.Fatalf("B 应为 100（T2 被中止），实际 %d", got)
	}
	if sum := db.Get("A") + db.Get("B"); sum < 0 {
		t.Fatalf("约束被破坏: A+B=%d", sum)
	}
}

// 对照：纯 SI（关闭 SSI 检测）下两事务都提交，写倾斜真实发生。
// 扣款 150 时各自看到 sum=200 均通过约束检查，最终 A+B=-100 违约。
func TestPureSIAllowsWriteSkew(t *testing.T) {
	db := New()
	db.DisableSSI = true
	db.Put("A", 100)
	db.Put("B", 100)

	t1 := db.Begin()
	t2 := db.Begin()

	a1, _ := t1.Get("A")
	t1.Get("B")
	if err := t1.Set("A", a1-150); err != nil {
		t.Fatal(err)
	}
	t2.Get("A")
	b2, _ := t2.Get("B")
	if err := t2.Set("B", b2-150); err != nil {
		t.Fatal(err)
	}
	if err := t1.Commit(); err != nil {
		t.Fatalf("纯 SI 下 T1 应提交成功: %v", err)
	}
	if err := t2.Commit(); err != nil {
		t.Fatalf("纯 SI 下 T2 应提交成功（写倾斜未被检测）: %v", err)
	}

	// 写倾斜异常：两个事务都基于 sum=200 的快照扣款，最终违约。
	if sum := db.Get("A") + db.Get("B"); sum >= 0 {
		t.Fatalf("纯 SI 下应发生违约，实际 A+B=%d", sum)
	}
}

// SSI 不应误伤无冲突的并发事务。
func TestNoFalseAbortOnDisjointWrites(t *testing.T) {
	db := New()
	db.Put("A", 100)
	db.Put("B", 100)

	t1 := db.Begin()
	t2 := db.Begin()

	a1, _ := t1.Get("A")
	if err := t1.Set("A", a1-10); err != nil {
		t.Fatal(err)
	}
	b2, _ := t2.Get("B")
	if err := t2.Set("B", b2-10); err != nil {
		t.Fatal(err)
	}
	if err := t1.Commit(); err != nil {
		t.Fatalf("T1 应提交成功: %v", err)
	}
	if err := t2.Commit(); err != nil {
		t.Fatalf("T2 应提交成功（无冲突）: %v", err)
	}
	if db.Get("A") != 90 || db.Get("B") != 90 {
		t.Fatalf("余额错误: A=%d B=%d", db.Get("A"), db.Get("B"))
	}
}

// 通过“读到被覆写版本”路径建立 rw-反依赖边，同样要检测出危险结构。
func TestDangerousStructureViaOverwrittenRead(t *testing.T) {
	db := New()
	db.Put("A", 100)
	db.Put("B", 100)

	t1 := db.Begin()
	// T2 在 T1 快照之后覆写 A 并提交。
	t2 := db.Begin()
	if err := t2.Set("A", 50); err != nil {
		t.Fatal(err)
	}
	if err := t2.Commit(); err != nil {
		t.Fatal(err)
	}
	// T1 读到旧版本 A=100，建立边 T1 -rw-> T2。
	a1, err := t1.Get("A")
	if err != nil {
		t.Fatal(err)
	}
	if a1 != 100 {
		t.Fatalf("T1 应读到快照值 100，实际 %d", a1)
	}
	// T3 活跃，读 B（被 T1 随后写入），形成 T3 -rw-> T1 -rw-> T2。
	t3 := db.Begin()
	if _, err := t3.Get("B"); err != nil {
		t.Fatal(err)
	}
	if err := t1.Set("B", 0); err != ErrSerialization {
		t.Fatalf("应检测到危险结构并中止，实际 %v", err)
	}
}

// 写写冲突按 first-committer-wins 中止后提交者。
func TestWriteWriteConflict(t *testing.T) {
	db := New()
	db.Put("A", 100)

	t1 := db.Begin()
	t2 := db.Begin()
	if err := t1.Set("A", 1); err != nil {
		t.Fatal(err)
	}
	if err := t2.Set("A", 2); err != nil {
		t.Fatal(err)
	}
	if err := t1.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := t2.Commit(); err != ErrWriteConflict {
		t.Fatalf("T2 应报写写冲突，实际 %v", err)
	}
	if got := db.Get("A"); got != 1 {
		t.Fatalf("A 应为 1，实际 %d", got)
	}
}
