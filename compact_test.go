package leveled

import (
	"io/ioutil"
	"os"
	"testing"
)

func tempDB(t *testing.T, opts Options) (*DB, func()) {
	dir, err := ioutil.TempDir("", "leveled-test")
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	return db, func() { os.RemoveAll(dir) }
}

func mustGet(t *testing.T, db *DB, key string) (string, bool) {
	v, found, err := db.Get(key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	return string(v), found
}

func tableIDs(tables []TableMeta) []uint64 {
	var ids []uint64
	for _, tm := range tables {
		ids = append(ids, tm.ID)
	}
	return ids
}

// 验收场景：L0 堆积 4 个 SSTable 触发 Compaction；
// L0 全部文件与 L1 中有交集的 2 个文件合并；
// Tombstone 清理废弃 Key，生成紧凑的 L1 新文件。
func TestL0CompactionMergesAndDropsTombstones(t *testing.T) {
	db, cleanup := tempDB(t, Options{
		L0CompactionThreshold: 4,
		LevelCapacities:       []int64{1 << 20, 1 << 20},
	})
	defer cleanup()

	// L1 预置 3 个互不重叠的文件：f1[a,c]、f2[e,h]、f3[x,z]。
	if err := db.Ingest(1, []Entry{{Key: "a", Value: []byte("a1")}, {Key: "b", Value: []byte("b1")}, {Key: "c", Value: []byte("c1")}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Ingest(1, []Entry{{Key: "e", Value: []byte("e1")}, {Key: "f", Value: []byte("f1")}, {Key: "g", Value: []byte("g1")}, {Key: "h", Value: []byte("h1")}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Ingest(1, []Entry{{Key: "x", Value: []byte("x1")}, {Key: "y", Value: []byte("y1")}, {Key: "z", Value: []byte("z1")}}); err != nil {
		t.Fatal(err)
	}
	l1 := db.Levels()[1]
	if len(l1) != 3 {
		t.Fatalf("expect 3 L1 tables, got %d", len(l1))
	}
	f3ID := l1[2].ID // [x,z] 不与 L0 重叠，压缩后应原样保留

	// 4 次 Flush 堆积 L0；第 4 次触发压缩。
	// 覆盖 b/f（新版本）、删除 c/h（Tombstone）、新增 m/q。
	flushes := [][]Entry{
		{{Key: "b", Value: []byte("b2")}, {Key: "f", Value: []byte("f2")}},
		{{Key: "c", Tombstone: true}, {Key: "g", Value: []byte("g2")}},
		{{Key: "h", Tombstone: true}, {Key: "m", Value: []byte("m1")}},
		{{Key: "q", Value: []byte("q1")}},
	}
	for i, batch := range flushes {
		if err := db.Flush(batch); err != nil {
			t.Fatal(err)
		}
		if i < 3 && len(db.Levels()[0]) != i+1 {
			t.Fatalf("flush %d: expect %d L0 tables, got %d", i, i+1, len(db.Levels()[0]))
		}
	}

	levels := db.Levels()
	if got := len(levels[0]); got != 0 {
		t.Fatalf("expect L0 drained, got %d tables", got)
	}
	// L1 = 合并产物 1 个新文件 + 未参与合并的 f3。
	if got := len(levels[1]); got != 2 {
		t.Fatalf("expect 2 L1 tables after compaction, got %d", got)
	}
	var merged, kept *TableMeta
	for i := range levels[1] {
		tm := levels[1][i]
		if tm.ID == f3ID {
			kept = &tm
		} else {
			merged = &tm
		}
	}
	if kept == nil || merged == nil {
		t.Fatalf("expect untouched f3 plus one merged table, got ids %v", tableIDs(levels[1]))
	}
	if merged.MinKey != "a" || merged.MaxKey != "q" {
		t.Fatalf("merged range = [%s,%s], want [a,q]", merged.MinKey, merged.MaxKey)
	}

	// 合并产物中不应残留任何 Tombstone 与被覆盖的旧版本。
	entries, err := readAll(merged.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Tombstone {
			t.Fatalf("tombstone for %q leaked into compacted output", e.Key)
		}
	}
	want := map[string]string{
		"a": "a1", "b": "b2", "e": "e1", "f": "f2",
		"g": "g2", "m": "m1", "q": "q1",
	}
	if len(entries) != len(want) {
		t.Fatalf("merged table has %d entries, want %d", len(entries), len(want))
	}
	for _, e := range entries {
		if want[e.Key] != string(e.Value) {
			t.Fatalf("merged[%q] = %q, want %q", e.Key, e.Value, want[e.Key])
		}
	}

	// 对外可见性：新版本生效，被删除的 Key 不可见，未重叠的 f3 不受影响。
	for k, v := range want {
		if got, found := mustGet(t, db, k); !found || got != v {
			t.Fatalf("Get(%q) = %q,%v; want %q,true", k, got, found, v)
		}
	}
	for _, k := range []string{"c", "h"} {
		if _, found := mustGet(t, db, k); found {
			t.Fatalf("Get(%q) should be deleted", k)
		}
	}
	if got, found := mustGet(t, db, "z"); !found || got != "z1" {
		t.Fatalf("Get(z) = %q,%v; want z1,true", got, found)
	}
}

// 容量触发：L1 总大小超限后，选择与 L2 重叠范围最小的文件下沉。
func TestSizeTriggeredPicksSmallestOverlap(t *testing.T) {
	db, cleanup := tempDB(t, Options{
		L0CompactionThreshold: 100,
		LevelCapacities:       []int64{1, 1 << 20}, // L1 容量仅 1 字节，必然触发
	})
	defer cleanup()

	put := func(level int, keys ...string) {
		var es []Entry
		for _, k := range keys {
			es = append(es, Entry{Key: k, Value: []byte("v-" + k)})
		}
		if err := db.ingest(level, es); err != nil { // 不触发压缩，先堆出超限的 L1
			t.Fatal(err)
		}
	}
	// L2 三个文件，L1 的 wide 文件与其中两个重叠，narrow 文件不重叠。
	put(2, "a", "b")
	put(2, "m", "n")
	put(2, "y", "z")
	put(1, "a", "m") // wide: 与 L2 两个文件重叠
	put(1, "g")      // narrow: 与 L2 零重叠 -> 应被选中

	c := db.pickCompaction()
	if c == nil {
		t.Fatal("expect a compaction to be picked")
	}
	if c.level != 1 {
		t.Fatalf("compaction level = %d, want 1", c.level)
	}
	if len(c.inputs0) != 1 || c.inputs0[0].MinKey != "g" {
		t.Fatalf("picked inputs0 = %+v, want the narrow [g] table", c.inputs0)
	}
	if len(c.inputs1) != 0 {
		t.Fatalf("inputs1 should be empty, got %+v", c.inputs1)
	}

	if err := db.MaybeCompact(); err != nil {
		t.Fatal(err)
	}
	levels := db.Levels()
	// 容量仍超限，wide 文件随后也被级联下沉，L1 最终清空。
	if len(levels[1]) != 0 {
		t.Fatalf("L1 should be drained after cascading compaction, got %+v", levels[1])
	}
	// wide 下沉时与 L2 的 [a,b]、[g]、[m,n] 重叠，归并为一个文件；
	// 加上未重叠的 [y,z]，L2 共 2 个文件。
	if len(levels[2]) != 2 {
		t.Fatalf("L2 should have 2 tables, got %+v", levels[2])
	}
	if got, found := mustGet(t, db, "g"); !found || got != "v-g" {
		t.Fatalf("Get(g) = %q,%v; want v-g,true", got, found)
	}
	if got, found := mustGet(t, db, "m"); !found || got != "v-m" {
		t.Fatalf("Get(m) = %q,%v; want v-m,true", got, found)
	}
}

// 深层存在更旧版本时，Tombstone 必须保留，不能提前清理。
func TestTombstoneKeptWhenDeeperLevelHasKey(t *testing.T) {
	db, cleanup := tempDB(t, Options{
		L0CompactionThreshold: 2,
		LevelCapacities:       []int64{1 << 20, 1 << 20},
	})
	defer cleanup()

	if err := db.Ingest(2, []Entry{{Key: "k", Value: []byte("old")}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Flush([]Entry{{Key: "k", Tombstone: true}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Flush([]Entry{{Key: "j", Value: []byte("j1")}}); err != nil { // 触发 L0->L1
		t.Fatal(err)
	}

	levels := db.Levels()
	if len(levels[1]) != 1 {
		t.Fatalf("expect 1 L1 table, got %+v", levels[1])
	}
	entries, err := readAll(levels[1][0].Path)
	if err != nil {
		t.Fatal(err)
	}
	var sawTombstone bool
	for _, e := range entries {
		if e.Key == "k" && e.Tombstone {
			sawTombstone = true
		}
	}
	if !sawTombstone {
		t.Fatal("tombstone for k must survive: L2 still holds an older version")
	}
	if _, found := mustGet(t, db, "k"); found {
		t.Fatal("Get(k) should observe the tombstone and report deleted")
	}
}
