package skewguard_test

import (
	"strings"
	"sync"
	"testing"

	skew "skewguard"
)

const (
	onDuty  = "on"
	offDuty = "off"
)

func doctorKey(name string) string { return "doctor:" + name }

// atLeastOneOnDuty 是验收用不变量：值班表至少 1 人在岗。
func atLeastOneOnDuty(v skew.View) bool {
	for _, kv := range v.Scan("doctor:") {
		if kv.Value == onDuty {
			return true
		}
	}
	return false
}

func newRoster(names ...string) *skew.DB {
	db := skew.New()
	db.RegisterConstraint("at-least-one-on-duty", atLeastOneOnDuty)
	for _, n := range names {
		db.Set(doctorKey(n), onDuty)
	}
	return db
}

func onDutyCount(db *skew.DB) int {
	n := 0
	for _, kv := range db.ScanCommitted("doctor:") {
		if kv.Value == onDuty {
			n++
		}
	}
	return n
}

// requestLeave 请假事务：断言「我离开后仍有人值班」，再写入请假并提交。
func requestLeave(tx *skew.Tx, name string) error {
	if err := tx.Assert("at-least-one-on-duty", atLeastOneOnDuty); err != nil {
		return err
	}
	tx.Set(doctorKey(name), offDuty)
	return tx.Commit()
}

// rendezvous 是一个 N 方会合点：所有参与者都到达后才一起放行，
// 用于在测试中确定性地构造「都读完快照、再并发提交」的写倾斜交错。
type rendezvous struct {
	ready   chan struct{}
	release chan struct{}
}

func newRendezvous() *rendezvous {
	return &rendezvous{ready: make(chan struct{}, 8), release: make(chan struct{})}
}

func (r *rendezvous) arrive() { r.ready <- struct{}{} }

func (r *rendezvous) waitRelease() { <-r.release }

func (r *rendezvous) releaseAll(n int) {
	for i := 0; i < n; i++ {
		<-r.ready
	}
	close(r.release)
}

// TestWriteSkewDetected 验收场景：
// A、B 同时值班；A 看到 B 在而请假，B 同时看到 A 在而请假；
// 提交阶段必须检测到值班人数 2 -> 0 的不变量破坏，并精准中止其中一人。
func TestWriteSkewDetected(t *testing.T) {
	db := newRoster("A", "B")
	rv := newRendezvous()

	var wg sync.WaitGroup
	errs := make(map[string]error, 2)
	var mu sync.Mutex

	for _, name := range []string{"A", "B"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			tx := db.Begin()
			// 双方都在各自快照中确认对方在值班。
			other := "B"
			if name == "B" {
				other = "A"
			}
			if v, _ := tx.Get(doctorKey(other)); v != onDuty {
				t.Errorf("%s 应在快照中看到 %s 在值班", name, other)
			}
			rv.arrive()      // 确认双方都已读取快照
			rv.waitRelease() // 再同时进入提交阶段
			err := requestLeave(tx, name)
			mu.Lock()
			errs[name] = err
			mu.Unlock()
		}(name)
	}
	rv.releaseAll(2)
	wg.Wait()

	committed, aborted := 0, 0
	for name, err := range errs {
		if err == nil {
			committed++
			t.Logf("%s 的事务提交成功", name)
		} else {
			aborted++
			t.Logf("%s 的事务被中止: %v", name, err)
			if !skew.IsConstraintError(err) {
				t.Errorf("%s 的中止应为约束错误，实际: %v", name, err)
			}
		}
	}
	if committed != 1 || aborted != 1 {
		t.Fatalf("应恰好一人提交、一人被中止，实际 提交=%d 中止=%d", committed, aborted)
	}
	if got := onDutyCount(db); got != 1 {
		t.Fatalf("不变量被破坏：最终值班人数 = %d，应为 1", got)
	}
}

// TestWriteSkewStress 反复压测并发请假，每轮都必须恰好一人成功。
func TestWriteSkewStress(t *testing.T) {
	for round := 0; round < 200; round++ {
		db := newRoster("A", "B")
		rv := newRendezvous()
		var wg sync.WaitGroup
		var committed int64
		var mu sync.Mutex
		for _, name := range []string{"A", "B"} {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				tx := db.Begin()
				rv.arrive()
				rv.waitRelease()
				if err := requestLeave(tx, name); err == nil {
					mu.Lock()
					committed++
					mu.Unlock()
				}
			}(name)
		}
		rv.releaseAll(2)
		wg.Wait()
		if committed != 1 {
			t.Fatalf("第 %d 轮：应恰好 1 人提交成功，实际 %d", round, committed)
		}
		if got := onDutyCount(db); got != 1 {
			t.Fatalf("第 %d 轮：值班人数 = %d，不变量被破坏", round, got)
		}
	}
}

// TestSequentialSecondLeaveBlocked 顺序执行时，第二个请假者被不变量拦截。
func TestSequentialSecondLeaveBlocked(t *testing.T) {
	db := newRoster("A", "B")

	if err := requestLeave(db.Begin(), "A"); err != nil {
		t.Fatalf("A 请假应成功: %v", err)
	}
	err := requestLeave(db.Begin(), "B")
	if !skew.IsConstraintError(err) {
		t.Fatalf("B 请假应被约束中止，实际: %v", err)
	}
	if got := onDutyCount(db); got != 1 {
		t.Fatalf("值班人数 = %d，应为 1", got)
	}
}

// TestNoFalsePositive 并发但不冲突的事务不应被误伤：
// A 请假的同时 C 上岗，两个事务都应提交成功。
func TestNoFalsePositive(t *testing.T) {
	db := newRoster("A", "B")
	rv := newRendezvous()

	var wg sync.WaitGroup
	errs := make([]error, 2)

	wg.Add(2)
	go func() { // A 请假
		defer wg.Done()
		tx := db.Begin()
		rv.arrive()
		rv.waitRelease()
		errs[0] = requestLeave(tx, "A")
	}()
	go func() { // C 上岗
		defer wg.Done()
		tx := db.Begin()
		rv.arrive()
		rv.waitRelease()
		tx.Set(doctorKey("C"), onDuty)
		errs[1] = tx.Commit()
	}()
	rv.releaseAll(2)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("事务 %d 不应被中止: %v", i, err)
		}
	}
	if got := onDutyCount(db); got != 2 { // B、C 在岗
		t.Fatalf("值班人数 = %d，应为 2", got)
	}
}

// TestAssertionImmediateFailure 谓词在事务视图上已不成立时，Assert 立即失败。
func TestAssertionImmediateFailure(t *testing.T) {
	db := newRoster("A") // 仅 1 人值班
	tx := db.Begin()
	tx.Set(doctorKey("A"), offDuty)
	err := tx.Assert("at-least-one-on-duty", atLeastOneOnDuty)
	if !skew.IsConstraintError(err) {
		t.Fatalf("应立刻报约束错误，实际: %v", err)
	}
}

// TestFailedCommitRollsBack 提交失败不得污染已提交状态。
func TestFailedCommitRollsBack(t *testing.T) {
	db := newRoster("A", "B")
	if err := requestLeave(db.Begin(), "A"); err != nil {
		t.Fatalf("A 请假应成功: %v", err)
	}
	tx := db.Begin()
	tx.Set(doctorKey("B"), offDuty)
	tx.Set("unrelated", "dirty")
	if err := tx.Commit(); !skew.IsConstraintError(err) {
		t.Fatalf("B 的提交应被中止，实际: %v", err)
	}
	if v, _ := db.Get(doctorKey("B")); v != onDuty {
		t.Fatalf("回滚后 B 应仍在值班，实际 %q", v)
	}
	if _, ok := db.Get("unrelated"); ok {
		t.Fatal("回滚后不应残留未提交写")
	}
}

// TestDeleteAndReadYourWrites 基本语义：删除、读己之写、快照隔离。
func TestDeleteAndReadYourWrites(t *testing.T) {
	db := skew.New()
	db.Set("k1", "v1")
	db.Set("k2", "v2")

	tx := db.Begin()
	tx.Delete("k1")
	tx.Set("k2", "v2-new")
	if _, ok := tx.Get("k1"); ok {
		t.Fatal("事务内应看到 k1 已删除")
	}
	if v, _ := tx.Get("k2"); v != "v2-new" {
		t.Fatalf("事务内应读己之写，实际 %q", v)
	}
	// 快照隔离：另一个并发事务看不到未提交写。
	other := db.Begin()
	if v, _ := other.Get("k2"); v != "v2" {
		t.Fatalf("其他事务应看到旧快照值，实际 %q", v)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if _, ok := db.Get("k1"); ok {
		t.Fatal("提交后 k1 应被删除")
	}
	if v, _ := db.Get("k2"); v != "v2-new" {
		t.Fatalf("提交后 k2 应为新值，实际 %q", v)
	}
}

// TestConstraintErrorMessage 错误信息应指明被破坏的约束。
func TestConstraintErrorMessage(t *testing.T) {
	db := newRoster("A")
	tx := db.Begin()
	tx.Set(doctorKey("A"), offDuty)
	err := tx.Commit()
	if err == nil || !strings.Contains(err.Error(), "at-least-one-on-duty") {
		t.Fatalf("错误应包含约束名，实际: %v", err)
	}
}
