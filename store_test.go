package walstore

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func pageData(s string) []byte {
	d := make([]byte, PageDataSize)
	copy(d, s)
	return d
}

// runScenario 串行执行三笔事务（页有重叠，顺序敏感），
// 返回持久化事件轨迹和四个串行状态 S0..S3（页 0..2 的数据）。
//
//	T1: p0=A1 p1=B1
//	T2: p1=B2 p2=C2
//	T3: p0=A3 p2=C3
func runScenario(t *testing.T) ([]Op, [][][]byte) {
	tr := &Tracer{}
	walF := NewSimFile("wal", tr)
	dbF := NewSimFile("db", tr)
	st, err := Open(walF, dbF)
	if err != nil {
		t.Fatal(err)
	}
	st.OnCommit = func(id uint64) {
		tr.Ops = append(tr.Ops, Op{Kind: OpAck, Note: fmt.Sprintf("ack:%d", id)})
	}
	commit := func(pages map[uint32][]byte) {
		tx := st.Begin()
		for pid := uint32(0); pid < 3; pid++ {
			if d, ok := pages[pid]; ok {
				tx.Set(pid, d)
			}
		}
		if err := st.Commit(tx); err != nil {
			t.Fatal(err)
		}
		if err := st.Checkpoint(); err != nil {
			t.Fatal(err)
		}
	}
	commit(map[uint32][]byte{0: pageData("A1"), 1: pageData("B1")})
	commit(map[uint32][]byte{1: pageData("B2"), 2: pageData("C2")})
	commit(map[uint32][]byte{0: pageData("A3"), 2: pageData("C3")})

	zero := make([]byte, PageDataSize)
	states := [][][]byte{
		{zero, zero, zero},                               // S0
		{pageData("A1"), pageData("B1"), zero},           // S1
		{pageData("A1"), pageData("B2"), pageData("C2")}, // S2
		{pageData("A3"), pageData("B2"), pageData("C3")}, // S3
	}
	return tr.Ops, states
}

// commitSyncIndex 返回每笔事务 commit 刷盘（WAL Sync）在轨迹中的下标。
func commitSyncIndex(t *testing.T, ops []Op) map[int]int {
	idx := map[int]int{}
	for i, op := range ops {
		if op.Kind == OpSync && strings.HasPrefix(op.Note, "commit:") {
			id, err := strconv.Atoi(strings.TrimPrefix(op.Note, "commit:"))
			if err != nil {
				t.Fatal(err)
			}
			idx[id] = i
		}
	}
	if len(idx) != 3 {
		t.Fatalf("expect 3 commit syncs, got %d", len(idx))
	}
	return idx
}

// recoverPages 在崩溃后的文件对上做恢复，读出页 0..2。
// 随后再做一次恢复，核对幂等（状态不变且第二次恢复零页写）。
func recoverPages(t *testing.T, files map[string]*SimFile) [][]byte {
	st, err := Open(files["wal"], files["db"])
	if err != nil {
		t.Fatal(err)
	}
	pages := make([][]byte, 3)
	for pid := uint32(0); pid < 3; pid++ {
		p, err := st.Get(pid)
		if err != nil {
			t.Fatal(err)
		}
		pages[pid] = p
	}
	// 重复恢复：必须不变，且不得产生任何新的页写。
	tr := &Tracer{}
	files["db"].tracer = tr
	st2, err := Open(files["wal"], files["db"])
	if err != nil {
		t.Fatal(err)
	}
	for pid := uint32(0); pid < 3; pid++ {
		p, err := st2.Get(pid)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(p, pages[pid]) {
			t.Fatalf("repeated recovery changed page %d", pid)
		}
	}
	for _, op := range tr.Ops {
		if op.Kind == OpWrite {
			t.Fatalf("repeated recovery is not idempotent: rewrote db at %d", op.Off)
		}
	}
	return pages
}

func stateLabel(states [][][]byte, pages [][]byte) string {
	for i, s := range states {
		if bytes.Equal(s[0], pages[0]) && bytes.Equal(s[1], pages[1]) && bytes.Equal(s[2], pages[2]) {
			return fmt.Sprintf("S%d", i)
		}
	}
	return ""
}

// TestPersistenceCutPoints 枚举三笔事务执行轨迹上的每一个持久化切点
// （含崩溃瞬间正在进行的写只落盘前缀的变体），崩溃恢复后核对：
//  1. 状态恰好等于串行前缀 S_i，i = 切点前已完成 commit 刷盘的事务数；
//  2. 每笔事务全有或全无；
//  3. 重复恢复不变。
func TestPersistenceCutPoints(t *testing.T) {
	ops, states := runScenario(t)
	csync := commitSyncIndex(t, ops)

	// 不变式：每笔事务的 ack 一定出现在它的 commit 刷盘之后。
	for i, op := range ops {
		if op.Kind == OpAck {
			id, _ := strconv.Atoi(strings.TrimPrefix(op.Note, "ack:"))
			if csync[id] >= i {
				t.Fatalf("ack of T%d (op %d) is not after its commit sync (op %d)", id, i, csync[id])
			}
		}
	}

	for cut := 0; cut <= len(ops); cut++ {
		tears := []int{-1} // 完整重放
		if cut > 0 && ops[cut-1].Kind == OpWrite {
			tears = append(tears, 0, len(ops[cut-1].Data)/2) // 崩溃瞬间的撕裂写
		}
		for _, tear := range tears {
			files := ReplayPrefix(ops, cut, tear)
			pages := recoverPages(t, files)

			want := 0
			for id := 1; id <= 3; id++ {
				if csync[id] < cut {
					want = id
				}
			}
			label := stateLabel(states, pages)
			if label == "" {
				t.Fatalf("cut=%d tear=%d: recovered state is not a serial prefix: %q %q %q",
					cut, tear, pages[0][:4], pages[1][:4], pages[2][:4])
			}
			if label != fmt.Sprintf("S%d", want) {
				t.Fatalf("cut=%d tear=%d: recovered %s, want S%d", cut, tear, label, want)
			}
		}
	}
}

// TestTornSyncedWALWrite 枚举“WAL 写被撕裂、随后又被 Sync 持久化”的
// 切点：CRC 必须在恢复时截断日志，保证已落盘部分仍是合法串行前缀
// （事务全有或全无，绝不出现半笔事务）。
func TestTornSyncedWALWrite(t *testing.T) {
	ops, states := runScenario(t)
	csync := commitSyncIndex(t, ops)

	for j, op := range ops {
		if op.Kind != OpWrite || op.File != "wal" {
			continue
		}
		// 该写属于哪笔事务：位于哪条 commit 刷盘之前。
		owner := 0
		for id := 1; id <= 3; id++ {
			if j < csync[id] {
				owner = id
				break
			}
		}
		if owner == 0 {
			t.Fatalf("wal write op %d has no owning txn", j)
		}
		cut := csync[owner] + 1 // 包含该事务的 commit 刷盘
		for _, m := range []int{0, walHeaderSize / 2, len(op.Data) / 2, len(op.Data) - 1} {
			// 重放 ops[0:cut]，但第 j 个写只落盘前 m 字节。
			files := replayWithTornWrite(ops, cut, j, m)
			pages := recoverPages(t, files)
			// 若被撕掉的后缀本身全是零字节（页数据尾部填充），
			// 撕裂写与完整写字节相同，WAL 不受影响。
			noop := true
			for _, b := range op.Data[m:] {
				if b != 0 {
					noop = false
					break
				}
			}
			wantID := owner - 1
			if noop {
				wantID = owner
			}
			want := fmt.Sprintf("S%d", wantID)
			if label := stateLabel(states, pages); label != want {
				t.Fatalf("torn wal write op=%d bytes=%d: recovered %s, want %s",
					j, m, label, want)
			}
		}
	}
}

// replayWithTornWrite 重放 ops[0:cut]，其中第 tearIdx 个写只落盘前 m 字节，
// 然后崩溃。与 ReplayPrefix 的区别是撕裂点可以在轨迹中间（后续还有 Sync
// 把撕裂的字节持久化）。
func replayWithTornWrite(ops []Op, cut, tearIdx, m int) map[string]*SimFile {
	files := map[string]*SimFile{
		"wal": NewSimFile("wal", nil),
		"db":  NewSimFile("db", nil),
	}
	for i := 0; i < cut; i++ {
		op := ops[i]
		f := files[op.File]
		if f == nil {
			continue
		}
		switch op.Kind {
		case OpWrite:
			data := op.Data
			if i == tearIdx && m < len(data) {
				data = data[:m]
			}
			end := int(op.Off) + len(data)
			for len(f.volatile) < end {
				f.volatile = append(f.volatile, 0)
			}
			copy(f.volatile[int(op.Off):end], data)
		case OpSync:
			f.durable = append([]byte(nil), f.volatile...)
		}
	}
	for _, f := range files {
		f.Crash()
	}
	return files
}

// TestFaultInjection 直接验证文件接口的短写注入与持久化丢失。
func TestFaultInjection(t *testing.T) {
	// 短写：WriteAt 只落盘前缀并报错，WAL 扫描在撕裂处截断。
	f := NewSimFile("wal", nil)
	rec1 := encodeCommitRecord(1, 1)
	if _, err := f.WriteAt(rec1, 0); err != nil {
		t.Fatal(err)
	}
	f.TearNextWrite(3)
	rec2 := encodeCommitRecord(2, 2)
	n, err := f.WriteAt(rec2, int64(len(rec1)))
	if err == nil || n != 3 {
		t.Fatalf("expect short write of 3 bytes with error, got n=%d err=%v", n, err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	recs, validEnd, err := scanWAL(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || validEnd != int64(len(rec1)) {
		t.Fatalf("expect 1 valid record ending at %d, got %d records ending at %d",
			len(rec1), len(recs), validEnd)
	}

	// 持久化丢失：未 Sync 的写在 Crash 后消失。
	f2 := NewSimFile("db", nil)
	if _, err := f2.WriteAt([]byte("hello"), 0); err != nil {
		t.Fatal(err)
	}
	if err := f2.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := f2.WriteAt([]byte("world"), 5); err != nil {
		t.Fatal(err)
	}
	f2.Crash()
	size, _ := f2.Size()
	if size != 5 {
		t.Fatalf("unsynced write survived crash, size=%d", size)
	}
	buf := make([]byte, 5)
	if _, err := f2.ReadAt(buf, 0); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("durable content corrupted: %q", buf)
	}
}

// TestUncommittedTxnNeverReachesDisk 未提交事务的页不得出现在页文件。
func TestUncommittedTxnNeverReachesDisk(t *testing.T) {
	walF := NewSimFile("wal", nil)
	dbF := NewSimFile("db", nil)
	st, err := Open(walF, dbF)
	if err != nil {
		t.Fatal(err)
	}
	tx := st.Begin()
	tx.Set(0, pageData("XX"))
	// 不提交，直接崩溃。
	walF.Crash()
	dbF.Crash()
	st2, err := Open(walF, dbF)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st2.Get(0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p, make([]byte, PageDataSize)) {
		t.Fatalf("uncommitted write leaked: %q", p[:4])
	}
}
