package walstore

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

const (
	testPageSize = 64
	testNumPages = 6
)

func pagePayload(c byte) []byte {
	p := make([]byte, payloadSize(testPageSize))
	for i := range p {
		p[i] = c
	}
	return p
}

// 三笔事务,每笔改多个页,且页之间有交叠,串行结果唯一确定。
func runThreeTxns(t *testing.T, s *Store) {
	t.Helper()
	tx1 := s.Begin()
	mustWrite(t, tx1, 0, pagePayload('A'))
	mustWrite(t, tx1, 1, pagePayload('B'))
	mustCommit(t, tx1)

	tx2 := s.Begin()
	mustWrite(t, tx2, 1, pagePayload('C'))
	mustWrite(t, tx2, 2, pagePayload('D'))
	mustCommit(t, tx2)

	tx3 := s.Begin()
	mustWrite(t, tx3, 0, pagePayload('E'))
	mustWrite(t, tx3, 3, pagePayload('F'))
	mustCommit(t, tx3)
}

func mustWrite(t *testing.T, tx *Txn, page int, p []byte) {
	t.Helper()
	if err := tx.Write(page, p); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func mustCommit(t *testing.T, tx *Txn) {
	t.Helper()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// 串行状态:k 笔事务按序提交后的全部页载荷。
func serialState(k int) [][]byte {
	state := make([][]byte, testNumPages)
	for i := range state {
		state[i] = pagePayload(0)
	}
	apply := func(page int, c byte) { state[page] = pagePayload(c) }
	if k >= 1 {
		apply(0, 'A')
		apply(1, 'B')
	}
	if k >= 2 {
		apply(1, 'C')
		apply(2, 'D')
	}
	if k >= 3 {
		apply(0, 'E')
		apply(3, 'F')
	}
	return state
}

func checkState(t *testing.T, s *Store, want [][]byte, ctx string) {
	t.Helper()
	for p := 0; p < testNumPages; p++ {
		if got := s.Read(p); !bytes.Equal(got, want[p]) {
			t.Fatalf("%s: page %d = %q, want %q", ctx, p, got[:4], want[p][:4])
		}
	}
}

// commitRecordEnds 返回日志中各 commit 记录的结束偏移(按文件顺序)。
func commitRecordEnds(t *testing.T, logBytes []byte) []int {
	t.Helper()
	var ends []int
	off := 0
	for off < len(logBytes) {
		typ, _, total, err := decodeRecord(logBytes[off:])
		if err != nil {
			t.Fatalf("log corrupt at %d: %v", off, err)
		}
		if typ == recCommit {
			ends = append(ends, off+total)
		}
		off += total
	}
	if len(ends) != 3 {
		t.Fatalf("expected 3 commit records, got %d", len(ends))
	}
	return ends
}

// TestCutPoints 枚举三笔事务日志的每一个持久化切点(含记录中间),
// 验证恢复结果恰好等于对应的串行前缀状态,且重复恢复不变。
func TestCutPoints(t *testing.T) {
	data := NewMemFile()
	logf := NewMemFile()
	s, err := Open(data, logf, testPageSize, testNumPages)
	if err != nil {
		t.Fatal(err)
	}
	runThreeTxns(t, s)

	logBytes := logf.DurableBytes()
	commitEnds := commitRecordEnds(t, logBytes)
	initialData := make([]byte, testPageSize*testNumPages) // Open 初始化后的全零页

	for cut := 0; cut <= len(logBytes); cut++ {
		// 该切点之前完整落盘的 commit 记录数 = 应当恢复出的事务数
		k := 0
		for _, end := range commitEnds {
			if end <= cut {
				k++
			}
		}
		want := serialState(k)

		d := NewMemFileDurable(initialData)
		l := NewMemFileDurable(logBytes[:cut])
		rs, err := Open(d, l, testPageSize, testNumPages)
		if err != nil {
			t.Fatalf("cut=%d: open: %v", cut, err)
		}
		checkState(t, rs, want, "cut="+itoa(cut)+" first-recover")

		// 重复恢复(第一次恢复已把重做结果写回数据文件):结果必须不变
		rs2, err := Open(d, l, testPageSize, testNumPages)
		if err != nil {
			t.Fatalf("cut=%d: reopen: %v", cut, err)
		}
		checkState(t, rs2, want, "cut="+itoa(cut)+" second-recover")

		// 再崩溃一次(模拟恢复过程中断电):结果仍必须不变
		d.Crash()
		l.Crash()
		rs3, err := Open(d, l, testPageSize, testNumPages)
		if err != nil {
			t.Fatalf("cut=%d: reopen after crash: %v", cut, err)
		}
		checkState(t, rs3, want, "cut="+itoa(cut)+" third-recover")
	}
	t.Logf("verified %d cut points over %d log bytes", len(logBytes)+1, len(logBytes))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// TestShortWrite 注入短写:日志与数据文件每次写只完成前缀,
// 写入方重试后提交仍成功,崩溃恢复结果正确。
func TestShortWrite(t *testing.T) {
	data := NewMemFile()
	logf := NewMemFile()
	data.ShortWriteMax = 7
	logf.ShortWriteMax = 5
	s, err := Open(data, logf, testPageSize, testNumPages)
	if err != nil {
		t.Fatal(err)
	}
	runThreeTxns(t, s)
	if err := s.FlushData(); err != nil {
		t.Fatal(err)
	}

	data.Crash()
	logf.Crash()
	rs, err := Open(data, logf, testPageSize, testNumPages)
	if err != nil {
		t.Fatal(err)
	}
	checkState(t, rs, serialState(3), "short-write recover")
}

// TestSyncLoss 注入持久化丢失:Sync 假成功,提交虽被确认,
// 崩溃后该事务不出现——说明确认路径完全依赖真实的 Sync 语义。
func TestSyncLoss(t *testing.T) {
	data := NewMemFile()
	logf := NewMemFile()
	s, err := Open(data, logf, testPageSize, testNumPages)
	if err != nil {
		t.Fatal(err)
	}
	logf.SyncLoss = true
	tx := s.Begin()
	mustWrite(t, tx, 0, pagePayload('A'))
	mustCommit(t, tx) // 确认已发出,但日志从未真正持久化

	logf.Crash()
	rs, err := Open(data, logf, testPageSize, testNumPages)
	if err != nil {
		t.Fatal(err)
	}
	checkState(t, rs, serialState(0), "sync-loss recover")
}

// TestUncommittedInvisible 未提交事务的日志即使部分落盘也不可见。
func TestUncommittedInvisible(t *testing.T) {
	data := NewMemFile()
	logf := NewMemFile()
	s, err := Open(data, logf, testPageSize, testNumPages)
	if err != nil {
		t.Fatal(err)
	}
	tx1 := s.Begin()
	mustWrite(t, tx1, 0, pagePayload('A'))
	mustWrite(t, tx1, 1, pagePayload('B'))
	mustCommit(t, tx1)

	tx2 := s.Begin()
	mustWrite(t, tx2, 0, pagePayload('Z'))
	mustWrite(t, tx2, 1, pagePayload('Z'))
	// 不提交,但把未提交的日志记录也"持久化"(直接 Sync 文件)
	if err := logf.Sync(); err != nil {
		t.Fatal(err)
	}
	logf.Crash()
	data.Crash()

	rs, err := Open(data, logf, testPageSize, testNumPages)
	if err != nil {
		t.Fatal(err)
	}
	checkState(t, rs, serialState(1), "uncommitted recover")
}

// TestOSFile 真实文件冒烟测试。
func TestOSFile(t *testing.T) {
	dir, err := ioutil.TempDir("", "walstore")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	data, err := OpenOSFile(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	logf, err := OpenOSFile(filepath.Join(dir, "wal.log"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(data, logf, testPageSize, testNumPages)
	if err != nil {
		t.Fatal(err)
	}
	runThreeTxns(t, s)
	if err := s.FlushData(); err != nil {
		t.Fatal(err)
	}

	data2, err := OpenOSFile(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	log2, err := OpenOSFile(filepath.Join(dir, "wal.log"))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := Open(data2, log2, testPageSize, testNumPages)
	if err != nil {
		t.Fatal(err)
	}
	checkState(t, rs, serialState(3), "osfile recover")
}
