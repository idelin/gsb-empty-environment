package lsm

import (
	"fmt"
	"os"
	"testing"
)

func put(key, val string, seq uint64) Entry {
	return Entry{Seq: seq, Op: OpPut, Key: key, Value: []byte(val)}
}

// 验收场景：L0 堆积 4 个 SSTable 触发压实；L0 全部文件与 L1 中有交集的
// 2 个文件（共 3 个）合并；Tombstone 被清理，生成紧凑的 L1 新文件。
func TestL0TriggerCompactsWithOverlappingL1(t *testing.T) {
	l, err := Open(Options{Dir: t.TempDir(), L0CompactionTrigger: 4})
	if err != nil {
		t.Fatal(err)
	}

	// L1 铺底 3 个互不重叠的 SSTable：t1[a..c] t2[g..m] t3[x..z]
	if _, err := l.AddTable(1, []Entry{put("a", "A1", 1), put("c", "C1", 2)}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.AddTable(1, []Entry{put("g", "G1", 3), put("m", "M1", 4)}); err != nil {
		t.Fatal(err)
	}
	untouched, err := l.AddTable(1, []Entry{put("x", "X1", 5), put("z", "Z1", 6)})
	if err != nil {
		t.Fatal(err)
	}

	// 4 次 Flush 产生 4 个 L0 文件，Key 范围 [b..h]，与 t1、t2 相交，与 t3 不相交。
	l.Put("b", "B1")
	l.Put("d", "D1")
	l.Put("f", "F0") // 旧版本，应被 F1 覆盖
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	l.Put("d", "D2")
	l.Put("f", "F1")
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	l.Del("d") // Tombstone：d 的最终版本
	l.Put("h", "H1")
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := len(l.Levels()[0]); got != 3 {
		t.Fatalf("前 3 次 Flush 不应触发压实，L0 文件数 = %d, 期望 3", got)
	}

	l.Put("e", "E1")
	if err := l.Flush(); err != nil { // 第 4 个 L0 文件 -> 触发压实
		t.Fatal(err)
	}

	levels := l.Levels()
	if got := len(levels[0]); got != 0 {
		t.Fatalf("压实后 L0 应为空，实际 %d 个文件", got)
	}
	if got := len(levels[1]); got != 2 {
		t.Fatalf("压实后 L1 应为 2 个文件（合并产物 + 未涉及的 t3），实际 %d", got)
	}

	// 找到合并产物（不是未涉及的 t3）
	var merged *SSTable
	for _, f := range levels[1] {
		if f.ID != untouched.ID {
			merged = f
		}
	}
	if merged == nil {
		t.Fatal("未找到合并产物")
	}
	entries, err := DumpTable(merged)
	if err != nil {
		t.Fatal(err)
	}

	// 期望：a b c e f g h m（d 被 Tombstone 清理），全部是最新版本
	want := map[string]string{
		"a": "A1", "b": "B1", "c": "C1", "e": "E1",
		"f": "F1", "g": "G1", "h": "H1", "m": "M1",
	}
	if len(entries) != len(want) {
		t.Fatalf("合并产物记录数 = %d, 期望 %d: %v", len(entries), len(want), entries)
	}
	for _, e := range entries {
		if e.Op != OpPut {
			t.Errorf("最底层压实不应残留 Tombstone: %+v", e)
		}
		if w, ok := want[e.Key]; !ok {
			t.Errorf("意外的 Key %q（被清理的 d 不应出现）", e.Key)
		} else if string(e.Value) != w {
			t.Errorf("Key %q = %q, 期望 %q", e.Key, e.Value, w)
		}
	}

	// 通过 Get 验证整体可读性
	if _, ok := l.Get("d"); ok {
		t.Error("d 已删除，Get 应返回 false")
	}
	for key, w := range map[string]string{"a": "A1", "b": "B1", "f": "F1", "x": "X1", "z": "Z1"} {
		if v, ok := l.Get(key); !ok || string(v) != w {
			t.Errorf("Get(%q) = %q,%v, 期望 %q,true", key, v, ok, w)
		}
	}
}

// 非 L0 层超容量时，调度器应挑选与下一层重叠 Key 范围最小的 SSTable。
func TestPickerChoosesSmallestOverlap(t *testing.T) {
	l, err := Open(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	val := make([]byte, 40)

	// L1 三个文件，与 L2 的重叠数分别为 2、0、1。
	f1, _ := l.AddTable(1, []Entry{{Seq: 1, Op: OpPut, Key: "a", Value: val}, {Seq: 2, Op: OpPut, Key: "c", Value: val}})
	f2, _ := l.AddTable(1, []Entry{{Seq: 3, Op: OpPut, Key: "h", Value: val}, {Seq: 4, Op: OpPut, Key: "i", Value: val}})
	f3, _ := l.AddTable(1, []Entry{{Seq: 5, Op: OpPut, Key: "m", Value: val}, {Seq: 6, Op: OpPut, Key: "o", Value: val}})
	// L2：g1[b..d]、g2[c..e] 与 f1 重叠；g3[n..p] 与 f3 重叠；f2 无重叠。
	l.AddTable(2, []Entry{{Seq: 7, Op: OpPut, Key: "b", Value: val}, {Seq: 8, Op: OpPut, Key: "d", Value: val}})
	l.AddTable(2, []Entry{{Seq: 9, Op: OpPut, Key: "c", Value: val}, {Seq: 10, Op: OpPut, Key: "e", Value: val}})
	l.AddTable(2, []Entry{{Seq: 11, Op: OpPut, Key: "n", Value: val}, {Seq: 12, Op: OpPut, Key: "p", Value: val}})

	// 容量设为：去掉 f2 后刚好达标，保证只压实一次。
	var total int64
	for _, f := range l.Levels()[1] {
		total += f.Size
	}
	l.opt.BaseLevelSize = total - f2.Size

	if err := l.MaybeCompact(); err != nil {
		t.Fatal(err)
	}

	levels := l.Levels()
	if got := len(levels[1]); got != 2 {
		t.Fatalf("压实后 L1 应剩 2 个文件，实际 %d", got)
	}
	for _, f := range levels[1] {
		if f.ID == f2.ID {
			t.Error("f2（重叠最小）应被选中压实，不应留在 L1")
		}
		if f.ID != f1.ID && f.ID != f3.ID {
			t.Errorf("L1 中出现意外文件 %v", f)
		}
	}
	if got := len(levels[2]); got != 4 {
		t.Fatalf("L2 应为 3 个原始文件 + 1 个合并产物，实际 %d", got)
	}
	if _, err := os.Stat(f2.Path); !os.IsNotExist(err) {
		t.Error("被压实的 f2 文件应从磁盘删除")
	}
	if v, ok := l.Get("h"); !ok || string(v) != string(val) {
		t.Error("f2 的数据应能在 L2 合并产物中读到")
	}
}

// 目标层之下还有数据时，Tombstone 必须保留（否则下层旧值会复活）。
func TestTombstonePreservedWhenNotBottommost(t *testing.T) {
	l, err := Open(Options{Dir: t.TempDir(), L0CompactionTrigger: 4})
	if err != nil {
		t.Fatal(err)
	}

	// L2 有数据 -> L1 不是最底层，Tombstone 不能清理。
	if _, err := l.AddTable(2, []Entry{put("k", "OLD", 1)}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		l.Put(fmt.Sprintf("k%d", i), "v")
		if err := l.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	l.Del("k")
	if err := l.Flush(); err != nil { // 触发 L0 -> L1 压实
		t.Fatal(err)
	}

	levels := l.Levels()
	if got := len(levels[1]); got != 1 {
		t.Fatalf("L1 应有 1 个合并产物，实际 %d", got)
	}
	entries, err := DumpTable(levels[1][0])
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Key == "k" {
			found = true
			if e.Op != OpDelete {
				t.Error("非最底层压实必须保留 k 的 Tombstone")
			}
		}
	}
	if !found {
		t.Error("合并产物中应保留 k 的 Tombstone")
	}
	if _, ok := l.Get("k"); ok {
		t.Error("k 已删除，Get 应返回 false")
	}
}
