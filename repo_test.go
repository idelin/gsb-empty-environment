package gsb

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// detSeed 生成确定性伪随机字节串。
func detSeed(n int, seed uint32) []byte {
	out := make([]byte, n)
	x := seed
	for i := range out {
		x = x*1664525 + 1013904223
		out[i] = byte(x >> 24)
	}
	return out
}

// det 生成默认种子的确定性伪随机字节串。
func det(n int) []byte { return detSeed(n, 0x9e3779b9) }

// detAlt 生成与 det 内容不同的确定性字节串（避免测试被物理去重命中）。
func detAlt(n int) []byte { return detSeed(n, 0xDEADBEEF) }

func buildRepo(t *testing.T, data []byte) *Repo {
	t.Helper()
	r := NewRepo(0)
	if err := r.Insert("main", 0, data); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	return r
}

func mustRead(t *testing.T, r *Repo, branch string, off, n int) []byte {
	t.Helper()
	got, err := r.Read(branch, off, n)
	if err != nil {
		t.Fatalf("Read(%q,%d,%d): %v", branch, off, n, err)
	}
	return got
}

func TestCrossBlockRead(t *testing.T) {
	data := det(200) // 4 个物理块
	r := buildRepo(t, data)
	for off := 0; off <= len(data); off++ {
		for n := 0; n <= len(data)-off; n += 7 {
			if got := mustRead(t, r, "main", off, n); !bytes.Equal(got, data[off:off+len(got)]) {
				t.Fatalf("Read(%d,%d) 与参考不符", off, n)
			}
		}
	}
	if err := r.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
}

func TestEndianCrossBlock(t *testing.T) {
	data := det(128)
	r := buildRepo(t, data)
	// uint64 写在偏移 60，恰好跨越 64 字节块边界。
	v := uint64(0x0102030405060708)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	if err := r.Overwrite("main", 60, buf[:]); err != nil {
		t.Fatal(err)
	}
	gotLE, err := r.ReadUint64LE("main", 60)
	if err != nil {
		t.Fatal(err)
	}
	if gotLE != v {
		t.Fatalf("跨块小端读取 = %#x，期望 %#x", gotLE, v)
	}
	gotBE, err := r.ReadUint64BE("main", 60)
	if err != nil {
		t.Fatal(err)
	}
	if want := binary.BigEndian.Uint64(buf[:]); gotBE != want {
		t.Fatalf("跨块大端读取 = %#x，期望 %#x", gotBE, want)
	}
	// uint32 写在偏移 62，同样跨块。
	w32 := uint32(0xA1B2C3D4)
	var b4 [4]byte
	binary.BigEndian.PutUint32(b4[:], w32)
	if err := r.Overwrite("main", 62, b4[:]); err != nil {
		t.Fatal(err)
	}
	got32, err := r.ReadUint32BE("main", 62)
	if err != nil {
		t.Fatal(err)
	}
	if got32 != w32 {
		t.Fatalf("跨块大端 uint32 = %#x，期望 %#x", got32, w32)
	}
	if err := r.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
}

func TestEmptySlice(t *testing.T) {
	r := NewRepo(0)
	if n, err := r.Len("main"); err != nil || n != 0 {
		t.Fatalf("空分支长度 = %d, %v", n, err)
	}
	got, err := r.Read("main", 0, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("空读 = %v, %v", got, err)
	}
	if _, err := r.Read("main", 0, 1); err != ErrRange {
		t.Fatalf("空切片越界读应报 ErrRange，得到 %v", err)
	}
	if _, err := r.ReadUint64LE("main", 0); err != ErrRange {
		t.Fatalf("空切片整数读应报 ErrRange，得到 %v", err)
	}
	if err := r.Delete("main", 0, 1); err != ErrRange {
		t.Fatalf("空切片删除应报 ErrRange，得到 %v", err)
	}
	if err := r.Overwrite("main", 0, []byte("x")); err != ErrRange {
		t.Fatalf("空切片覆盖应报 ErrRange，得到 %v", err)
	}
	if err := r.Move("main", 0, 0, 1); err != ErrRange {
		t.Fatalf("空切片移动应报 ErrRange，得到 %v", err)
	}
	if err := r.Insert("main", 0, []byte("ab")); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.Len("main"); n != 2 {
		t.Fatalf("插入后长度 = %d", n)
	}
	if err := r.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
}

func TestBranchIsolation(t *testing.T) {
	data := det(200)
	r := buildRepo(t, data)
	if err := r.Snapshot("b", "main"); err != nil {
		t.Fatal(err)
	}
	patch := bytes.Repeat([]byte("X"), 50)
	if err := r.Overwrite("main", 50, patch); err != nil {
		t.Fatal(err)
	}
	// 相交区域修改只影响目标版本：b 必须保持操作前内容。
	if got := mustRead(t, r, "b", 0, 200); !bytes.Equal(got, data) {
		t.Fatal("快照分支被相交修改污染")
	}
	want := append([]byte(nil), data[:50]...)
	want = append(want, patch...)
	want = append(want, data[100:]...)
	if got := mustRead(t, r, "main", 0, 200); !bytes.Equal(got, want) {
		t.Fatal("目标分支修改结果不符")
	}
	// 反向：修改 b 的相交区间，main 不受影响。
	if err := r.Overwrite("b", 75, bytes.Repeat([]byte("Y"), 50)); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, r, "main", 0, 200); !bytes.Equal(got, want) {
		t.Fatal("main 被 b 的相交修改污染")
	}
	if err := r.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
}

func modelMove(m []byte, src, dst, n int) []byte {
	seg := append([]byte(nil), m[src:src+n]...)
	rest := append([]byte(nil), m[:src]...)
	rest = append(rest, m[src+n:]...)
	ins := dst
	if dst > src {
		ins = dst - n
	}
	if ins < 0 {
		ins = 0
	}
	if ins > len(m)-n {
		ins = len(m) - n
	}
	out := append([]byte(nil), rest[:ins]...)
	out = append(out, seg...)
	return append(out, rest[ins:]...)
}

func TestOverlapMove(t *testing.T) {
	base := []byte("0123456789abcdef")
	cases := [][3]int{
		{2, 6, 4},  // 目标在源之后且区间重叠
		{6, 2, 4},  // 目标在源之前且区间重叠
		{3, 4, 5},  // 目标落在源区间内部
		{0, 16, 4}, // 整体后移到末尾
		{12, 0, 4}, // 尾部移到开头
		{5, 5, 3},  // 原地移动（无操作效果）
		{0, 0, 16}, // 全长原地
	}
	for _, c := range cases {
		r := buildRepo(t, base)
		if err := r.Move("main", c[0], c[1], c[2]); err != nil {
			t.Fatalf("Move%v: %v", c, err)
		}
		want := modelMove(base, c[0], c[1], c[2])
		if got := mustRead(t, r, "main", 0, len(want)); !bytes.Equal(got, want) {
			t.Fatalf("Move%v = %s，期望 %s", c, got, want)
		}
		if err := r.VerifyLedger(); err != nil {
			t.Fatalf("Move%v 后账本: %v", c, err)
		}
	}
}

func TestRollbackCancel(t *testing.T) {
	data := det(300)
	r := buildRepo(t, data)
	live0 := r.Store().LiveBytes()
	tx := r.Begin()
	if err := r.Splice(tx, "main", 10, 50, det(80)); err != nil {
		t.Fatal(err)
	}
	if err := r.Splice(tx, "main", 0, 0, det(40)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, r, "main", 0, len(data)); !bytes.Equal(got, data) {
		t.Fatal("回滚后内容被改变")
	}
	if got := r.Store().LiveBytes(); got != live0 {
		t.Fatalf("回滚后存活字节 = %d，期望 %d", got, live0)
	}
	if err := r.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != ErrTxClosed {
		t.Fatalf("回滚后再提交应报 ErrTxClosed，得到 %v", err)
	}
}

func TestQuotaRollback(t *testing.T) {
	data := det(200)
	r := buildRepo(t, data)
	live0 := r.Store().LiveBytes()
	r.Store().quota = live0 + 40 // 只够再放一个小块
	if err := r.Insert("main", 0, detAlt(200)); err != ErrQuota {
		t.Fatalf("超额插入应报 ErrQuota，得到 %v", err)
	}
	if got := mustRead(t, r, "main", 0, len(data)); !bytes.Equal(got, data) {
		t.Fatal("配额回滚后内容被改变")
	}
	if got := r.Store().LiveBytes(); got != live0 {
		t.Fatalf("配额回滚后存活字节 = %d，期望 %d", got, live0)
	}
	// 配额内的插入可以成功。
	if err := r.Insert("main", 0, []byte("z")); err != nil {
		t.Fatalf("配额内插入失败: %v", err)
	}
	if err := r.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
}

func TestBorrowPreventsGC(t *testing.T) {
	data := det(200)
	r := buildRepo(t, data)
	if err := r.Snapshot("snap", "main"); err != nil {
		t.Fatal(err)
	}
	if err := r.Overwrite("main", 0, detAlt(200)); err != nil {
		t.Fatal(err)
	}
	// 旧底块仍被 snap 借用，Collect 不能回收。
	if blocks, bytesFreed := r.Store().Collect(); blocks != 0 || bytesFreed != 0 {
		t.Fatalf("借用期间回收了 %d 块 %d 字节", blocks, bytesFreed)
	}
	if got := r.Store().GarbageBytes(); got != 0 {
		t.Fatalf("借用期间不应产生可回收垃圾，得到 %d", got)
	}
	if err := r.DropBranch("snap"); err != nil {
		t.Fatal(err)
	}
	garbage := r.Store().GarbageBytes()
	if garbage == 0 {
		t.Fatal("释放快照后应出现可回收垃圾")
	}
	blocks, bytesFreed := r.Store().Collect()
	if blocks == 0 || bytesFreed != garbage {
		t.Fatalf("Collect 回收 %d 块 %d 字节，期望 %d 字节", blocks, bytesFreed, garbage)
	}
	if got := r.Store().GarbageBytes(); got != 0 {
		t.Fatalf("回收后仍有垃圾 %d 字节", got)
	}
	if got := mustRead(t, r, "main", 0, 200); !bytes.Equal(got, detAlt(200)) {
		t.Fatal("回收后 main 内容受损")
	}
	if err := r.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
}

func TestSmallEditNoFullCopy(t *testing.T) {
	data := det(64 * 1024) // 1024 个物理块
	r := buildRepo(t, data)
	before := r.Store().LiveBytes()
	tx := r.Begin()
	if err := r.Splice(tx, "main", 12345, 1, []byte{0xAB}); err != nil {
		t.Fatal(err)
	}
	tempBlocks, tempBytes := tx.TempBlocks(), tx.TempBytes()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after := r.Store().LiveBytes()
	// 小编辑只复制了一个 1 字节新块（头 16 + 负载 1），而非整个 64KiB 逻辑序列。
	if tempBlocks != 1 {
		t.Fatalf("小编辑新建块数 = %d，期望 1", tempBlocks)
	}
	if delta := after - before; delta != BlockHeaderBytes+1 {
		t.Fatalf("小编辑物理增量 = %d 字节，期望 %d", delta, BlockHeaderBytes+1)
	}
	if tempBytes != BlockHeaderBytes+1 {
		t.Fatalf("临时复制记账 = %d 字节，期望 %d", tempBytes, BlockHeaderBytes+1)
	}
	logical, _ := r.Len("main")
	if after-before >= logical/10 {
		t.Fatalf("物理增量 %d 未远小于逻辑长度 %d", after-before, logical)
	}
	if err := r.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
}

func TestTempAccountingDuringTx(t *testing.T) {
	data := det(200)
	r := buildRepo(t, data)
	live0 := r.Store().LiveBytes()
	tx := r.Begin()
	if err := r.Splice(tx, "main", 0, 0, detAlt(200)); err != nil {
		t.Fatal(err)
	}
	// 事务进行中，临时复制必须已计入物理账本。
	if tx.TempBytes() == 0 {
		t.Fatal("事务内临时复制未记账")
	}
	if got := r.Store().LiveBytes(); got != live0+tx.TempBytes() {
		t.Fatalf("事务中存活字节 = %d，期望 %d（含临时复制 %d）", got, live0+tx.TempBytes(), tx.TempBytes())
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := r.Store().LiveBytes(); got != live0 {
		t.Fatalf("回滚后存活字节 = %d，期望 %d", got, live0)
	}
}

func TestPhysicalDedup(t *testing.T) {
	r := NewRepo(0)
	chunk := det(BlockSize)
	if err := r.Insert("main", 0, chunk); err != nil {
		t.Fatal(err)
	}
	live1 := r.Store().LiveBytes()
	if err := r.Insert("main", BlockSize, chunk); err != nil {
		t.Fatal(err)
	}
	// 相同内容物理去重：逻辑翻倍，物理不变。
	if got := r.Store().LiveBytes(); got != live1 {
		t.Fatalf("重复内容未去重: 存活 %d -> %d", live1, got)
	}
	if n := r.Store().LiveBlocks(); n != 1 {
		t.Fatalf("存活物理块 = %d，期望 1", n)
	}
	if err := r.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
}

func TestCollectAfterOverwrite(t *testing.T) {
	data := det(200)
	r := buildRepo(t, data)
	if err := r.Overwrite("main", 0, detAlt(200)); err != nil {
		t.Fatal(err)
	}
	// 无快照借用，旧块立即成为垃圾。
	if got := r.Store().GarbageBytes(); got == 0 {
		t.Fatal("覆盖后应产生可回收垃圾")
	}
	blocks, _ := r.Store().Collect()
	if blocks == 0 {
		t.Fatal("Collect 未回收任何块")
	}
	if got := r.Store().GarbageBytes(); got != 0 {
		t.Fatalf("回收后仍有垃圾 %d 字节", got)
	}
	if err := r.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
}
