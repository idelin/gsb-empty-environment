package diskqueue

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func open(t *testing.T, dir string, opts ...Option) *Queue {
	t.Helper()
	q, err := Open(dir, opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return q
}

// reopen 模拟进程重启：关闭句柄后从同一目录恢复。
// Close 不写任何额外数据，因此等价于进程崩溃后的重启。
func reopen(t *testing.T, q *Queue, opts ...Option) *Queue {
	t.Helper()
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return open(t, q.dir, opts...)
}

func popAll(t *testing.T, q *Queue) []Item {
	t.Helper()
	items, err := q.PopBatch(1 << 30)
	if err != nil {
		t.Fatalf("PopBatch: %v", err)
	}
	return items
}

func payloads(items []Item) []string {
	s := make([]string, len(items))
	for i, it := range items {
		s[i] = string(it.Payload)
	}
	return s
}

func TestOrderSamePriorityFIFOAndNegative(t *testing.T) {
	q := open(t, t.TempDir())
	defer q.Close()

	q.Enqueue(5, []byte("mid-1"))
	q.Enqueue(-3, []byte("neg-1")) // 负优先级
	q.Enqueue(5, []byte("mid-2"))
	q.Enqueue(-3, []byte("neg-2"))
	q.Enqueue(0, []byte("zero"))
	q.Enqueue(10, []byte("low"))

	got := payloads(popAll(t, q))
	want := []string{"neg-1", "neg-2", "zero", "mid-1", "mid-2", "low"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if q.Len() != 0 {
		t.Fatalf("Len = %d, want 0", q.Len())
	}
}

func TestEmptyQueue(t *testing.T) {
	q := open(t, t.TempDir())
	defer q.Close()

	items, err := q.PopBatch(10)
	if err != nil || items != nil {
		t.Fatalf("empty pop = %v, %v; want nil, nil", items, err)
	}
	if items, err := q.PopBatch(0); err != nil || items != nil {
		t.Fatalf("pop(0) = %v, %v; want nil, nil", items, err)
	}
	if err := q.Update(999, 1); err != ErrNotFound {
		t.Fatalf("Update missing = %v, want ErrNotFound", err)
	}
	if err := q.Delete(999); err != ErrNotFound {
		t.Fatalf("Delete missing = %v, want ErrNotFound", err)
	}
}

func TestUpdatePriorityReorders(t *testing.T) {
	q := open(t, t.TempDir())
	defer q.Close()

	q.Enqueue(1, []byte("first"))
	seqB, _ := q.Enqueue(2, []byte("second"))
	q.Enqueue(3, []byte("third"))

	if err := q.Update(seqB, -100); err != nil { // 改成负优先级，插到最前
		t.Fatalf("Update: %v", err)
	}
	got := payloads(popAll(t, q))
	want := []string{"second", "first", "third"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestDelete(t *testing.T) {
	q := open(t, t.TempDir())
	defer q.Close()

	seqA, _ := q.Enqueue(1, []byte("A"))
	q.Enqueue(1, []byte("B"))
	if err := q.Delete(seqA); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if q.Len() != 1 {
		t.Fatalf("Len = %d, want 1", q.Len())
	}
	got := payloads(popAll(t, q))
	if !reflect.DeepEqual(got, []string{"B"}) {
		t.Fatalf("got %v, want [B]", got)
	}
}

func TestRestartPreservesOrderAndSeq(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)

	q.Enqueue(7, []byte("a"))
	q.Enqueue(-1, []byte("b"))
	q.Enqueue(7, []byte("c"))
	seqD, _ := q.Enqueue(7, []byte("d"))
	q.Update(seqD, -5)
	q = reopen(t, q)

	// 重启后序号继续递增，不重置。
	seqE, err := q.Enqueue(7, []byte("e"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if seqE != 4 {
		t.Fatalf("seq after restart = %d, want 4", seqE)
	}
	got := payloads(popAll(t, q))
	want := []string{"d", "b", "a", "c", "e"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order after restart = %v, want %v", got, want)
	}
	q.Close()
}

func TestPopBatchAtomicCommitFailureKeepsTasks(t *testing.T) {
	dir := t.TempDir()
	injectErr := errors.New("injected: disk full")
	fail := true
	q := open(t, dir, WithFaultHook(func(p FaultPoint, typ byte) error {
		if fail && p == FaultBeforeAppend && typ == recPopCommit {
			return injectErr
		}
		return nil
	}))
	q.Enqueue(1, []byte("x"))
	q.Enqueue(2, []byte("y"))

	if _, err := q.PopBatch(2); !errors.Is(err, injectErr) {
		t.Fatalf("PopBatch err = %v, want injected", err)
	}
	if q.Len() != 2 {
		t.Fatalf("Len after failed pop = %d, want 2 (no task lost)", q.Len())
	}

	fail = false
	items, err := q.PopBatch(2)
	if err != nil {
		t.Fatalf("PopBatch: %v", err)
	}
	if got := payloads(items); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Fatalf("got %v, want [x y]", got)
	}
	q.Close()
}

func TestCrashAfterCommitSyncNoDuplicate(t *testing.T) {
	dir := t.TempDir()
	crash := false
	crashErr := errors.New("simulated crash after commit synced")
	hook := func(p FaultPoint, typ byte) error {
		if crash && p == FaultAfterSync && typ == recPopCommit {
			return crashErr // commit 已 fsync，进程“崩溃”，内存未应用
		}
		return nil
	}
	q := open(t, dir, WithFaultHook(hook))
	q.Enqueue(1, []byte("x"))
	q.Enqueue(2, []byte("y"))

	crash = true
	if _, err := q.PopBatch(2); !errors.Is(err, crashErr) {
		t.Fatalf("PopBatch err = %v, want simulated crash", err)
	}
	// 重启：commit 记录已落盘，任务被移除，既不丢也不重复。
	crash = false
	q = reopen(t, q, WithFaultHook(hook))
	if q.Len() != 0 {
		t.Fatalf("Len after restart = %d, want 0 (commit was durable)", q.Len())
	}
	if items := popAll(t, q); len(items) != 0 {
		t.Fatalf("duplicate delivery after restart: %v", payloads(items))
	}
	q.Close()
}

func TestTornTailRecovered(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)
	q.Enqueue(1, []byte("a"))
	q.Enqueue(2, []byte("b"))
	q.Close()

	// 模拟断电在 WAL 尾部留下半帧垃圾。
	f, err := os.OpenFile(filepath.Join(dir, "wal.log"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{0x10, 0x00, 0x00, 0x00, 0x01, 0xde, 0xad}) // 长度声称 16，实际只有 3 字节帧体
	f.Close()

	q = open(t, dir)
	defer q.Close()
	if q.Len() != 2 {
		t.Fatalf("Len = %d, want 2 after torn tail truncation", q.Len())
	}
	// 截断后还能继续写入并再次恢复。
	q.Enqueue(3, []byte("c"))
	q = reopen(t, q)
	got := payloads(popAll(t, q))
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("got %v, want [a b c]", got)
	}
	q.Close()
}

func TestStaleUpdateDoesNotResurrectDeleted(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)

	seqA, _ := q.Enqueue(1, []byte("A"))
	q.Update(seqA, 5)
	q.Delete(seqA)
	// API 层面：已删除任务的更新直接拒绝，不产生记录。
	if err := q.Update(seqA, -100); err != ErrNotFound {
		t.Fatalf("Update deleted = %v, want ErrNotFound", err)
	}
	// 模拟一条因崩溃时序而滞留在 WAL 里的旧更新记录（写在 Delete 之后）。
	if err := q.append(recUpdate, encodeUpdate(seqA, -100)); err != nil {
		t.Fatalf("append stale update: %v", err)
	}
	q.Enqueue(1, []byte("B"))

	q = reopen(t, q)
	defer q.Close()
	if q.Len() != 1 {
		t.Fatalf("Len = %d, want 1 (deleted task must not resurrect)", q.Len())
	}
	got := payloads(popAll(t, q))
	if !reflect.DeepEqual(got, []string{"B"}) {
		t.Fatalf("got %v, want [B]", got)
	}
}

func TestCompactAndReplay(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)

	var keep []uint64
	for i := 0; i < 50; i++ {
		seq, _ := q.Enqueue(int64(i%7)-3, []byte(fmt.Sprintf("task-%02d", i))) // 含负优先级
		if i%3 == 0 {
			q.Delete(seq)
		} else {
			keep = append(keep, seq)
		}
	}
	if len(keep) >= 2 {
		q.Update(keep[1], -1000)
	}
	if _, err := q.PopBatch(5); err != nil {
		t.Fatalf("PopBatch: %v", err)
	}
	before := q.Len()

	if err := q.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// 压缩后立即可用。
	if q.Len() != before {
		t.Fatalf("Len after compact = %d, want %d", q.Len(), before)
	}
	// 重启后从快照+空 WAL 恢复，顺序一致。
	q = reopen(t, q)
	if q.Len() != before {
		t.Fatalf("Len after reopen = %d, want %d", q.Len(), before)
	}
	first := popAll(t, q)
	q.Close()

	// 对照组：不压缩，纯 WAL 回放，弹出顺序必须完全相同。
	dir2 := t.TempDir()
	q2 := open(t, dir2)
	var keep2 []uint64
	for i := 0; i < 50; i++ {
		seq, _ := q2.Enqueue(int64(i%7)-3, []byte(fmt.Sprintf("task-%02d", i)))
		if i%3 == 0 {
			q2.Delete(seq)
		} else {
			keep2 = append(keep2, seq)
		}
	}
	q2.Update(keep2[1], -1000)
	q2.PopBatch(5)
	q2 = reopen(t, q2)
	second := popAll(t, q2)
	q2.Close()

	if !reflect.DeepEqual(payloads(first), payloads(second)) {
		t.Fatalf("snapshot replay order differs from WAL replay:\n%v\n%v", payloads(first), payloads(second))
	}
}

func TestSeqMonotonicAcrossPopAndRestart(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)
	q.Enqueue(1, []byte("a"))
	q.Enqueue(1, []byte("b"))
	popAll(t, q) // 清空
	q = reopen(t, q)
	defer q.Close()
	seq, _ := q.Enqueue(1, []byte("c"))
	if seq != 2 {
		t.Fatalf("seq = %d, want 2 (monotonic, never reused)", seq)
	}
}

func TestConcurrentUse(t *testing.T) {
	q := open(t, t.TempDir())
	defer q.Close()
	done := make(chan struct{})
	for g := 0; g < 4; g++ {
		go func(g int) {
			for i := 0; i < 100; i++ {
				q.Enqueue(int64(i-g), []byte("x"))
			}
			done <- struct{}{}
		}(g)
	}
	for g := 0; g < 4; g++ {
		<-done
	}
	if q.Len() != 400 {
		t.Fatalf("Len = %d, want 400", q.Len())
	}
}
