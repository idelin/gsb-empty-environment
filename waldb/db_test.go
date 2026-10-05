package waldb

import (
	"errors"
	"testing"
	"time"
)

const testPages = 6

var errSimulatedCrash = errors.New("simulated crash")

// harness 驱动可崩溃的数据库：hook 在指定切点丢弃未持久化数据并 panic，
// 模拟掉电；之后可重新打开（触发恢复）并用独立检查器验证。
type harness struct {
	logFile  *InjectedFile
	pageFile *InjectedFile
	original []uint64

	scripts   map[int64][]PageWrite // txnID -> 计划的页写
	committed []CommittedTxn        // commit 记录已持久化的事务（提交顺序）

	crashAt          CrashPoint
	flushBeforeCrash bool // 崩溃前先把脏页刷盘（构造“页已改但 END 未写”）
	db               *DB
}

func newHarness() *harness {
	h := &harness{
		logFile:  NewInjectedFile(),
		pageFile: NewInjectedFile(),
		original: []uint64{100, 200, 300, 400, 500, 600},
		scripts:  make(map[int64][]PageWrite),
	}
	h.db = NewDatabase(h.logFile, h.pageFile, testPages, h.original, h.hook)
	h.armCommitTracker()
	return h
}

func (h *harness) armCommitTracker() {
	h.db.SetOnCommitDurable(func(id int64) {
		h.committed = append(h.committed, CommittedTxn{TxnID: id, Updates: h.scripts[id]})
	})
}

func (h *harness) hook(p CrashPoint) {
	if p != h.crashAt {
		return
	}
	h.crashAt = CutNever // 只崩一次
	if h.flushBeforeCrash {
		h.db.bp.FlushAll()
	}
	h.logFile.Crash()
	h.pageFile.Crash()
	panic(errSimulatedCrash)
}

// reopen 重新打开数据库（执行恢复）。
func (h *harness) reopen() {
	h.db = OpenDatabase(h.logFile, h.pageFile, testPages, h.hook)
	h.armCommitTracker()
}

// attempt 运行 fn，报告是否发生模拟崩溃。
func attempt(fn func()) (crashed bool) {
	defer func() {
		if r := recover(); r != nil {
			if r == errSimulatedCrash {
				crashed = true
				return
			}
			panic(r)
		}
	}()
	fn()
	return false
}

// runWorkload 正常执行一段混合负载：
// t1,t2 提交；检查点；t3 主动回滚；t4 更新后滞留未提交（且脏页被 steal）；
// t5 提交。任一时刻崩溃后，t1/t2/t5 的效果必须保留，t3/t4 的必须消失。
func runWorkload(h *harness) {
	t1 := h.db.Begin()
	h.scripts[t1.id] = []PageWrite{{0, 111}, {1, 211}}
	t1.Update(0, 111)
	t1.Update(1, 211)
	t1.Commit()

	t2 := h.db.Begin()
	h.scripts[t2.id] = []PageWrite{{1, 212}, {2, 312}}
	t2.Update(1, 212)
	t2.Update(2, 312)
	t2.Commit()

	h.db.Checkpoint()

	t3 := h.db.Begin()
	h.scripts[t3.id] = []PageWrite{{0, 113}}
	t3.Update(0, 113)
	t3.Abort()

	t4 := h.db.Begin()
	h.scripts[t4.id] = []PageWrite{{2, 314}, {3, 414}}
	t4.Update(2, 314)
	t4.Update(3, 414)
	h.db.FlushPages() // steal：未提交事务的脏页落盘

	t5 := h.db.Begin()
	h.scripts[t5.id] = []PageWrite{{4, 515}}
	t5.Update(4, 515)
	t5.Commit()
	// t4 故意不提交：崩溃或关闭时它是输家事务
}

// check 用独立检查器验证最终页状态。
func (h *harness) check(t *testing.T) {
	t.Helper()
	if err := CheckPages(h.pageFile, h.logFile, testPages, h.original, h.committed); err != nil {
		t.Fatal(err)
	}
}

func TestNormalExecution(t *testing.T) {
	h := newHarness()
	runWorkload(h)
	h.reopen() // 关闭重开：恢复撤销滞留的 t4
	h.check(t)
}

// TestCrashAtEveryNormalCutPoint 在正常执行的每个切点崩溃后恢复。
func TestCrashAtEveryNormalCutPoint(t *testing.T) {
	points := []CrashPoint{
		CutLogBuffered, CutCommitDurable, CutBeforeEnd, CutPageFlush, CutCheckpointPartial,
	}
	for _, p := range points {
		t.Run(p.String(), func(t *testing.T) {
			h := newHarness()
			h.crashAt = p
			if !attempt(func() { runWorkload(h) }) {
				t.Fatalf("cut point %s never fired", p)
			}
			h.reopen()
			h.check(t)
		})
	}
}

// TestCrashAtEveryRecoveryCutPoint 在恢复过程的每个切点崩溃，反复恢复直到成功。
func TestCrashAtEveryRecoveryCutPoint(t *testing.T) {
	points := []CrashPoint{
		CutRecoveryAnalysis, CutRecoveryRedo, CutRecoveryCLR, CutRecoveryUndoPage, CutRecoveryEnd,
	}
	for _, p := range points {
		t.Run(p.String(), func(t *testing.T) {
			h := newHarness()
			runWorkload(h)
			h.crashAt = p
			if !attempt(func() { h.reopen() }) {
				t.Fatalf("cut point %s never fired", p)
			}
			h.reopen() // 再次恢复
			h.check(t)
		})
	}
}

// TestCLRFlushedPageNotModified 覆盖“CLR 已落盘但页未改”：
// 恢复撤销时 CLR 持久化后立即崩溃，重恢复不得重复撤销该 CLR。
func TestCLRFlushedPageNotModified(t *testing.T) {
	h := newHarness()
	runWorkload(h)
	h.crashAt = CutRecoveryCLR
	attempt(func() { h.reopen() })

	// 确认 CLR 已持久化而页文件仍是崩溃前的值
	if !logHasRecordOfType(h.logFile, recCLR) {
		t.Fatal("expected a durable CLR after crash")
	}
	h.reopen()
	h.check(t)
	h.reopen() // 幂等：再恢复一次结果不变
	h.check(t)
}

// TestPageModifiedEndNotWritten 覆盖“页已改（且已落盘）但 END 未写”。
func TestPageModifiedEndNotWritten(t *testing.T) {
	h := newHarness()
	runWorkload(h)
	h.crashAt = CutRecoveryUndoPage
	h.flushBeforeCrash = true // 撤销改过的页先落盘，再崩溃
	attempt(func() { h.reopen() })
	h.reopen()
	h.check(t)
}

// TestIncompleteTailRecord 覆盖不完整尾记录：撕裂写/垃圾尾必须被截断。
func TestIncompleteTailRecord(t *testing.T) {
	h := newHarness()
	runWorkload(h)
	h.reopen()

	// 情形一：同步半个记录（撕裂写）
	full := (&LogRecord{Type: recUpdate, TxnID: 99, PageID: 0, Before: 1, After: 2}).encode()
	half := full[:len(full)/2]
	off := h.logFile.Size()
	h.logFile.WriteAt(half, off)
	h.logFile.Sync()
	h.reopen()
	h.check(t)
	if got := h.logFile.Size(); got != off {
		t.Fatalf("incomplete tail not truncated: size %d, want %d", got, off)
	}

	// 情形二：日志文件中部之后被截掉一段（尾部记录残缺）
	h.logFile.Truncate(h.logFile.Size() - 3)
	h.logFile.Sync()
	h.reopen()
	h.check(t)

	// 情形三：尾部追加垃圾字节
	h.logFile.WriteAt([]byte{0xDE, 0xAD, 0xBE, 0xEF}, h.logFile.Size())
	h.logFile.Sync()
	h.reopen()
	h.check(t)
}

// TestPartialCheckpoint 覆盖只完成一部分的检查点：
// begin_checkpoint 持久化后崩溃，恢复必须回退到可用起点。
func TestPartialCheckpoint(t *testing.T) {
	h := newHarness()
	h.crashAt = CutCheckpointPartial
	attempt(func() { runWorkload(h) })
	h.reopen()
	h.check(t)

	// 再构造一个撕裂的 end_checkpoint：只写一半就同步
	h.db.Checkpoint()
	size := h.logFile.Size()
	h.db.Checkpoint()
	tail := h.logFile.Bytes()[size:]
	h.logFile.Truncate(size)
	h.logFile.WriteAt(tail[:len(tail)-2], size) // 残缺的 end_checkpoint
	h.logFile.Sync()
	h.reopen()
	h.check(t)
}

// TestRepeatedRecovery 反复恢复：结果收敛且一致。
func TestRepeatedRecovery(t *testing.T) {
	h := newHarness()
	runWorkload(h)
	for i := 0; i < 3; i++ {
		h.reopen()
		h.check(t)
	}
}

// TestCommitDurabilityBoundary commit 记录持久化即算提交成功：
// 在 commit 持久化与 END 之间崩溃，事务效果必须保留。
func TestCommitDurabilityBoundary(t *testing.T) {
	h := newHarness()
	h.crashAt = CutBeforeEnd
	attempt(func() { runWorkload(h) })
	if len(h.committed) == 0 {
		t.Fatal("expected at least one durable commit before crash")
	}
	h.reopen()
	h.check(t)
}

// TestStrict2PLBlocking 验证严格两阶段独占锁：他事务写同页会阻塞，
// 直到持有者提交；且持有者提交前锁不释放（避免脏写）。
func TestStrict2PLBlocking(t *testing.T) {
	h := newHarness()
	t1 := h.db.Begin()
	t1.Update(0, 111)

	done := make(chan struct{})
	t2 := h.db.Begin()
	go func() {
		t2.Update(0, 999) // 应阻塞到 t1 提交
		t2.Commit()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("t2 acquired page lock while t1 still active")
	case <-time.After(100 * time.Millisecond):
	}
	t1.Commit()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("t2 still blocked after t1 commit")
	}
}

func logHasRecordOfType(f *InjectedFile, typ byte) bool {
	data := f.Bytes()
	off := int64(0)
	for off < int64(len(data)) {
		rec, next, err := decodeAt(data, off)
		if err != nil {
			break
		}
		if rec.Type == typ {
			return true
		}
		off = next
	}
	return false
}
