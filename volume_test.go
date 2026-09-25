package volmap

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"testing"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "volmap-test")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func fill(b byte, n int) []byte { return bytes.Repeat([]byte{b}, n) }

func readAll(t *testing.T, v *Volume, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	if _, err := v.Read(buf, 0); err != nil {
		t.Fatal(err)
	}
	return buf
}

// checkNonOverlap 校验区间不变式：升序、不重叠、非空、无可合并的相邻对。
func checkNonOverlap(t *testing.T, ivs []Interval) {
	t.Helper()
	for i, iv := range ivs {
		if iv.End <= iv.Start {
			t.Fatalf("空区间: %+v", iv)
		}
		if i == 0 {
			continue
		}
		prev := ivs[i-1]
		if prev.End > iv.Start {
			t.Fatalf("区间重叠: %+v 与 %+v", prev, iv)
		}
		if prev.End == iv.Start && prev.Block == iv.Block && prev.BOff+prev.Len() == iv.BOff {
			t.Fatalf("相邻区间未合并: %+v 与 %+v", prev, iv)
		}
	}
}

func TestWriteReadAndReopen(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	v, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Write(10, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := readAll(t, v, 20)
	want := make([]byte, 20)
	copy(want[10:], "hello")
	if !bytes.Equal(buf, want) {
		t.Fatalf("got %q want %q", buf, want)
	}

	// 重新打开（模拟进程重启），数据仍在。
	v2, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, v2, 20); !bytes.Equal(got, want) {
		t.Fatalf("重开后 got %q want %q", got, want)
	}
}

// 跨两个块写入只改覆盖区，两侧原数据保留。
func TestCrossBlockWriteOnlyTouchesCovered(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	v, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 两个相邻块：[0,64)='A'，[64,128)='B'。
	if err := v.Write(0, fill('A', 64)); err != nil {
		t.Fatal(err)
	}
	if err := v.Write(64, fill('B', 64)); err != nil {
		t.Fatal(err)
	}
	// 跨界写 [48,80)，横跨两个块。
	if err := v.Write(48, fill('X', 32)); err != nil {
		t.Fatal(err)
	}

	want := append(append(fill('A', 48), fill('X', 32)...), fill('B', 48)...)
	if got := readAll(t, v, 128); !bytes.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	checkNonOverlap(t, v.m.Live)
	if len(v.m.Live) != 3 {
		t.Fatalf("期望 3 个区间（左A/中X/右B），实际 %d: %+v", len(v.m.Live), v.m.Live)
	}
	// 左右两侧仍指向原来的两个块，偏移正确。
	left, right := v.m.Live[0], v.m.Live[2]
	if left.End != 48 || left.BOff != 0 {
		t.Fatalf("左侧区间错误: %+v", left)
	}
	if right.Start != 80 || right.BOff != 16 {
		t.Fatalf("右侧区间错误: %+v", right)
	}
}

// 打洞中段后两侧保留，且仍引用原物理块。
func TestPunchHoleMiddleKeepsSides(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	v, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Write(0, fill('A', 96)); err != nil {
		t.Fatal(err)
	}
	if err := v.PunchHole(32, 32); err != nil {
		t.Fatal(err)
	}

	want := append(append(fill('A', 32), make([]byte, 32)...), fill('A', 32)...)
	if got := readAll(t, v, 96); !bytes.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	checkNonOverlap(t, v.m.Live)
	if len(v.m.Live) != 2 {
		t.Fatalf("期望 2 个区间，实际 %d: %+v", len(v.m.Live), v.m.Live)
	}
	left, right := v.m.Live[0], v.m.Live[1]
	if left.Block != right.Block {
		t.Fatalf("两侧应共享同一物理块: %+v %+v", left, right)
	}
	if left.Start != 0 || left.End != 32 || left.BOff != 0 {
		t.Fatalf("左侧区间错误: %+v", left)
	}
	if right.Start != 64 || right.End != 96 || right.BOff != 64 {
		t.Fatalf("右侧区间错误: %+v", right)
	}
}

// 洞读为零且不占实体块：整个区域打洞后块文件被回收。
func TestHoleReadsZeroAndOccupiesNoBlock(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	v, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Write(0, fill('A', 128)); err != nil {
		t.Fatal(err)
	}
	if n := countBlocks(t, dir); n != 1 {
		t.Fatalf("写入后应有 1 个块，实际 %d", n)
	}
	if err := v.PunchHole(0, 128); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, v, 128); !bytes.Equal(got, make([]byte, 128)) {
		t.Fatalf("洞应读为零，got %q", got)
	}
	if n := countBlocks(t, dir); n != 0 {
		t.Fatalf("打洞后不应残留物理块，实际 %d", n)
	}
}

func countBlocks(t *testing.T, dir string) int {
	t.Helper()
	names, err := blockFileNames(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(names)
}

// 旧快照不受后续打洞/写入影响。
func TestSnapshotImmuneToLaterPunchAndWrite(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	v, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Write(0, fill('A', 96)); err != nil {
		t.Fatal(err)
	}
	snap, err := v.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	// 打洞中段 + 覆盖写头部。
	if err := v.PunchHole(32, 32); err != nil {
		t.Fatal(err)
	}
	if err := v.Write(0, fill('B', 16)); err != nil {
		t.Fatal(err)
	}

	// 活跃视图是新状态。
	liveWant := append(append(fill('B', 16), fill('A', 16)...), append(make([]byte, 32), fill('A', 32)...)...)
	if got := readAll(t, v, 96); !bytes.Equal(got, liveWant) {
		t.Fatalf("活跃视图 got %q want %q", got, liveWant)
	}
	// 快照仍是打洞前的样子。
	snapBuf := make([]byte, 96)
	if _, err := v.ReadSnapshot(snap, snapBuf, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snapBuf, fill('A', 96)) {
		t.Fatalf("快照被后续操作污染: %q", snapBuf)
	}
	// 快照钉住了原物理块，不能被回收。
	if n := countBlocks(t, dir); n < 1 {
		t.Fatalf("快照引用的块不应被回收")
	}

	// 重开后快照仍可读。
	v2, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapBuf2 := make([]byte, 96)
	if _, err := v2.ReadSnapshot(snap, snapBuf2, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snapBuf2, fill('A', 96)) {
		t.Fatalf("重开后快照数据错误: %q", snapBuf2)
	}
}

// 随机写入/打洞与字节级模型对比，并全程校验区间不变式。
func TestRandomOpsAgainstModel(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	v, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	model := map[uint64]byte{}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 300; i++ {
		off := uint64(rng.Intn(256))
		if rng.Intn(3) == 0 {
			ln := uint64(rng.Intn(64) + 1)
			if err := v.PunchHole(off, ln); err != nil {
				t.Fatal(err)
			}
			for j := off; j < off+ln; j++ {
				delete(model, j)
			}
		} else {
			data := make([]byte, rng.Intn(64)+1)
			rng.Read(data)
			if err := v.Write(off, data); err != nil {
				t.Fatal(err)
			}
			for j, b := range data {
				model[off+uint64(j)] = b
			}
		}
		checkNonOverlap(t, v.m.Live)
	}

	// 重开后逐字节对比模型（洞/未写区域为零）。
	v2, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	buf := readAll(t, v2, 512)
	for i := range buf {
		if want := model[uint64(i)]; buf[i] != want {
			t.Fatalf("偏移 %d: got %d want %d", i, buf[i], want)
		}
	}
}

// crash 模拟进程崩溃：到达指定注入点时 panic，不做任何清理。
type crash struct{ p FaultPoint }

func crashAt(p FaultPoint) FaultHook {
	return func(fp FaultPoint) error {
		if fp == p {
			panic(crash{p})
		}
		return nil
	}
}

func ignoreCrash(f func()) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(crash); !ok {
				panic(r)
			}
		}
	}()
	f()
}

// 在每个注入点模拟写操作中途崩溃，重开后状态必须一致：
// 要么完全是旧状态，要么完全是新状态，且能继续工作。
func TestCrashRecoveryDuringWrite(t *testing.T) {
	points := []FaultPoint{FaultAfterBlockSync, FaultBeforeManifestRename, FaultAfterManifestRename}
	for _, p := range points {
		t.Run(fmt.Sprint(int(p)), func(t *testing.T) {
			dir := tempDir(t)
			defer os.RemoveAll(dir)

			v, err := Open(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := v.Write(0, fill('A', 64)); err != nil {
				t.Fatal(err)
			}

			v, err = Open(dir, crashAt(p))
			if err != nil {
				t.Fatal(err)
			}
			ignoreCrash(func() { v.Write(32, fill('B', 32)) })

			// 崩溃后重开：状态必须是“旧”或“新”之一。
			v2, err := Open(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := readAll(t, v2, 64)
			oldState := fill('A', 64)
			newState := append(fill('A', 32), fill('B', 32)...)
			if !bytes.Equal(got, oldState) && !bytes.Equal(got, newState) {
				t.Fatalf("注入点 %d 崩溃后状态撕裂: %q", p, got)
			}
			checkNonOverlap(t, v2.m.Live)
			// 无孤儿块、无临时文件残留。
			if n := countBlocks(t, dir); n != len(v2.referencedBlocks()) {
				t.Fatalf("块文件数 %d 与引用数 %d 不一致", n, len(v2.referencedBlocks()))
			}
			// 卷仍可继续写。
			if err := v2.Write(0, fill('C', 8)); err != nil {
				t.Fatal(err)
			}
			if got := readAll(t, v2, 8); !bytes.Equal(got, fill('C', 8)) {
				t.Fatalf("崩溃恢复后写入失败: %q", got)
			}
		})
	}
}

// 打洞中途崩溃：洞要么完整存在，要么完全不存在。
func TestCrashRecoveryDuringPunch(t *testing.T) {
	points := []FaultPoint{FaultBeforeManifestRename, FaultAfterManifestRename}
	for _, p := range points {
		t.Run(fmt.Sprint(int(p)), func(t *testing.T) {
			dir := tempDir(t)
			defer os.RemoveAll(dir)

			v, err := Open(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := v.Write(0, fill('A', 96)); err != nil {
				t.Fatal(err)
			}
			v, err = Open(dir, crashAt(p))
			if err != nil {
				t.Fatal(err)
			}
			ignoreCrash(func() { v.PunchHole(32, 32) })

			v2, err := Open(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := readAll(t, v2, 96)
			withHole := append(append(fill('A', 32), make([]byte, 32)...), fill('A', 32)...)
			if !bytes.Equal(got, fill('A', 96)) && !bytes.Equal(got, withHole) {
				t.Fatalf("注入点 %d 崩溃后打洞状态撕裂: %q", p, got)
			}
			checkNonOverlap(t, v2.m.Live)
		})
	}
}

// 块已落盘、清单未提交时崩溃，孤儿块在 Open 时被回收。
func TestOrphanBlockReclaimedOnOpen(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	v, err := Open(dir, crashAt(FaultAfterBlockSync))
	if err != nil {
		t.Fatal(err)
	}
	ignoreCrash(func() { v.Write(0, fill('A', 64)) })
	if n := countBlocks(t, dir); n != 1 {
		t.Fatalf("崩溃后应残留 1 个孤儿块，实际 %d", n)
	}

	if _, err := Open(dir, nil); err != nil {
		t.Fatal(err)
	}
	if n := countBlocks(t, dir); n != 0 {
		t.Fatalf("孤儿块应被回收，实际剩 %d", n)
	}
}

// 注入点返回普通错误（而非崩溃）时，操作以错误中止且状态保持上一提交。
func TestFaultErrorAbortsCleanly(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	boom := fmt.Errorf("注入故障")
	commits := 0
	v, err := Open(dir, func(p FaultPoint) error {
		if p == FaultBeforeManifestRename && commits > 0 {
			return boom
		}
		if p == FaultBeforeManifestRename {
			commits++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Write(0, fill('A', 32)); err != nil {
		t.Fatal(err)
	}
	err = v.Write(0, fill('B', 32))
	if err != boom {
		t.Fatalf("期望注入错误，got %v", err)
	}
	// 状态仍是上一次提交的内容。
	if got := readAll(t, v, 32); !bytes.Equal(got, fill('A', 32)) {
		t.Fatalf("中止后状态错误: %q", got)
	}
}
