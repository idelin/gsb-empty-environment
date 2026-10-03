package lsm

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// smallOpts 使用很小的容量水位,让 Compaction 在少量数据下即可逐层触发。
func smallOpts() *Options {
	return &Options{
		MemtableFlushThreshold: 1000, // 测试中手动 Flush
		MaxLevels:              4,    // L0..L3,L3 为 Bottom-most
		Level0Capacity:         2,    // L0 容量 2 条
		LevelSizeRatio:         2,    // L1=4, L2=8, L3 不限
		AutoCompact:            false,
	}
}

func openTemp(t *testing.T, opts *Options) (*DB, string, func()) {
	dir, err := ioutil.TempDir("", "lsm-test-")
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir, opts)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	return db, dir, func() {
		db.Close()
		os.RemoveAll(dir)
	}
}

func mustGet(t *testing.T, db *DB, key, want string) {
	t.Helper()
	got, err := db.Get(key)
	if err != nil {
		t.Fatalf("Get(%q) unexpected err: %v", key, err)
	}
	if string(got) != want {
		t.Fatalf("Get(%q) = %q, want %q", key, got, want)
	}
}

func mustNotFound(t *testing.T, db *DB, key string) {
	t.Helper()
	if _, err := db.Get(key); err != ErrNotFound {
		t.Fatalf("Get(%q) err = %v, want ErrNotFound", key, err)
	}
}

func scanKeys(t *testing.T, db *DB, start, end string) []string {
	t.Helper()
	kvs, err := db.Scan(start, end)
	if err != nil {
		t.Fatalf("Scan err: %v", err)
	}
	keys := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		keys = append(keys, kv.Key)
	}
	return keys
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// keyOnDisk 直接扫描所有 SSTable 文件的原始字节,验证 key 的物理痕迹。
func keyOnDisk(t *testing.T, dir, key string) bool {
	t.Helper()
	fis, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, fi := range fis {
		if !strings.HasSuffix(fi.Name(), ".sst") {
			continue
		}
		data, err := ioutil.ReadFile(filepath.Join(dir, fi.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(key)) {
			return true
		}
	}
	return false
}

// 1. 插入 K1=V1 后删除 K1(写入 Tombstone),查询立即返回 NotFound。
func TestDeleteWritesTombstoneAndGetReturnsNotFound(t *testing.T) {
	db, _, cleanup := openTemp(t, nil)
	defer cleanup()

	if err := db.Put("K1", []byte("V1")); err != nil {
		t.Fatal(err)
	}
	mustGet(t, db, "K1", "V1")

	if err := db.Delete("K1"); err != nil {
		t.Fatal(err)
	}
	// Tombstone 还在 Memtable 中,Get 必须立即被屏蔽。
	mustNotFound(t, db, "K1")

	// Memtable 中应存在 Tombstone 物理记录。
	probe, err := db.Probe("K1")
	if err != nil {
		t.Fatal(err)
	}
	if len(probe) != 1 || !probe[0].Tombstone || probe[0].Level != -1 {
		t.Fatalf("expected one tombstone in memtable, got %+v", probe)
	}
}

// 2. 旧值已落盘时,Tombstone(无论在 Memtable 还是 SSTable)依然屏蔽 Point Get。
func TestTombstoneMasksFlushedValue(t *testing.T) {
	db, _, cleanup := openTemp(t, smallOpts())
	defer cleanup()

	db.Put("K1", []byte("V1"))
	db.Put("A1", []byte("a"))
	if err := db.Flush(); err != nil {
		t.Fatal(err)
	}
	// 此时 K1=V1 已在 L0 SSTable 中,删除只写 Tombstone 到 Memtable。
	if err := db.Delete("K1"); err != nil {
		t.Fatal(err)
	}
	mustNotFound(t, db, "K1")

	// Tombstone 也落盘后,Get 仍须返回 NotFound。
	db.Put("B2", []byte("b"))
	if err := db.Flush(); err != nil {
		t.Fatal(err)
	}
	mustNotFound(t, db, "K1")
}

// 3. Range Scan 中被删除的 Key 不出现,其余键有序返回。
func TestRangeScanSkipsTombstonedKeys(t *testing.T) {
	db, _, cleanup := openTemp(t, nil)
	defer cleanup()

	for _, k := range []string{"k1", "k2", "k3", "k4", "k5"} {
		db.Put(k, []byte("v-"+k))
	}
	db.Delete("k2")
	db.Delete("k4")
	if err := db.Flush(); err != nil {
		t.Fatal(err)
	}

	want := []string{"k1", "k3", "k5"}
	if got := scanKeys(t, db, "", ""); !equalStrings(got, want) {
		t.Fatalf("full scan = %v, want %v", got, want)
	}
	// 范围边界:[k2, k5) 应得到 k3、k4 被删 -> k3。
	if got := scanKeys(t, db, "k2", "k5"); !equalStrings(got, []string{"k3"}) {
		t.Fatalf("range scan = %v, want [k3]", got)
	}
}

// 4. 非 Bottom-most 层合并时,Tombstone 必须保留(更深层可能还有旧版本)。
func TestTombstoneSurvivesNonBottomCompaction(t *testing.T) {
	db, _, cleanup := openTemp(t, smallOpts())
	defer cleanup()

	db.Put("alpha", []byte("1"))
	db.Put("beta", []byte("2"))
	db.Flush() // L0: 2 条
	db.Delete("alpha")
	db.Put("gamma", []byte("3"))
	db.Flush() // L0: 4 条 > 容量 2

	merged, err := db.CompactOnce()
	if err != nil {
		t.Fatal(err)
	}
	if !merged {
		t.Fatal("expected a compaction to happen")
	}

	// 合并只到 L1(非底层),Tombstone 必须仍然存在。
	probe, err := db.Probe("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(probe) != 1 || !probe[0].Tombstone || probe[0].Level != 1 {
		t.Fatalf("tombstone should survive at L1, got %+v", probe)
	}
	mustNotFound(t, db, "alpha")
	mustGet(t, db, "beta", "2")
	mustGet(t, db, "gamma", "3")
}

// 5. 验收主用例:多轮 Compaction 使 Tombstone 逐层下沉至 Bottom-most,
//    确认无更老版本后被彻底物理擦除。
func TestTombstonePurgedAtBottomMostLevel(t *testing.T) {
	db, dir, cleanup := openTemp(t, smallOpts())
	defer cleanup()

	db.Put("alpha", []byte("1"))
	db.Put("beta", []byte("2"))
	db.Flush()
	db.Delete("alpha")
	db.Put("gamma", []byte("3"))
	db.Flush()

	bottom := db.MaxLevels() - 1
	sawIntermediate := false

	// 持续写入无关数据制造合并压力,驱动 Tombstone 逐层下沉。
	for round := 0; round < 500; round++ {
		if err := db.Compact(); err != nil {
			t.Fatal(err)
		}
		probe, err := db.Probe("alpha")
		if err != nil {
			t.Fatal(err)
		}
		if len(probe) == 0 {
			break // Tombstone 已被物理擦除
		}
		for _, p := range probe {
			if !p.Tombstone {
				t.Fatalf("unexpected live value for alpha at L%d", p.Level)
			}
			if p.Level == bottom {
				t.Fatalf("tombstone reached bottom-most L%d but was not purged", bottom)
			}
			sawIntermediate = true
		}
		db.Put(fmt.Sprintf("f%03da", round), []byte("x"))
		db.Put(fmt.Sprintf("f%03db", round), []byte("y"))
		if err := db.Flush(); err != nil {
			t.Fatal(err)
		}
	}

	if !sawIntermediate {
		t.Fatal("tombstone never observed at an intermediate level")
	}

	// 语义层面:查询仍然 NotFound。
	mustNotFound(t, db, "alpha")
	// 物理层面:所有 SSTable 文件中不再存在 "alpha" 的任何字节痕迹。
	if keyOnDisk(t, dir, "alpha") {
		t.Fatal("alpha still physically present in sstable files")
	}
	// 存活数据不受影响。
	mustGet(t, db, "beta", "2")
	mustGet(t, db, "gamma", "3")
}

// 6. 同一 Key 多版本:新版本胜出,删除后所有旧版本被屏蔽。
func TestNewestVersionWins(t *testing.T) {
	db, _, cleanup := openTemp(t, smallOpts())
	defer cleanup()

	db.Put("k", []byte("v1"))
	db.Flush()
	db.Put("k", []byte("v2"))
	db.Flush()
	mustGet(t, db, "k", "v2")

	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}
	mustGet(t, db, "k", "v2")

	db.Delete("k")
	mustNotFound(t, db, "k")
	db.Flush()
	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}
	mustNotFound(t, db, "k")
}

// 7. 删除从未存在的 Key:Tombstone 最终也会被底层合并清理,不留痕迹。
func TestDeleteNonExistentKeyEventuallyPurged(t *testing.T) {
	opts := smallOpts()
	opts.MaxLevels = 2 // L0 + Bottom-most,一次合并即到底
	db, dir, cleanup := openTemp(t, opts)
	defer cleanup()

	db.Put("a", []byte("1"))
	db.Put("b", []byte("2"))
	db.Flush()
	db.Delete("ghost")
	db.Put("c", []byte("3"))
	db.Flush() // L0: 4 条 > 容量 2

	if _, err := db.CompactOnce(); err != nil {
		t.Fatal(err)
	}
	mustNotFound(t, db, "ghost")
	probe, err := db.Probe("ghost")
	if err != nil {
		t.Fatal(err)
	}
	if len(probe) != 0 {
		t.Fatalf("ghost tombstone should be purged at bottom-most, got %+v", probe)
	}
	if keyOnDisk(t, dir, "ghost") {
		t.Fatal("ghost still physically present")
	}
	if got := scanKeys(t, db, "", ""); !equalStrings(got, []string{"a", "b", "c"}) {
		t.Fatalf("scan = %v, want [a b c]", got)
	}
}

// 8. 关闭重开后,已落盘数据与序号正确恢复,删除语义继续生效。
func TestReopenRecoversState(t *testing.T) {
	dir, err := ioutil.TempDir("", "lsm-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	db, err := Open(dir, smallOpts())
	if err != nil {
		t.Fatal(err)
	}
	db.Put("K1", []byte("V1"))
	db.Put("K2", []byte("V2"))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db2, err := Open(dir, smallOpts())
	if err != nil {
		t.Fatal(err)
	}
	mustGet(t, db2, "K1", "V1")
	db2.Delete("K1")
	mustNotFound(t, db2, "K1")
	if err := db2.Close(); err != nil {
		t.Fatal(err)
	}

	db3, err := Open(dir, smallOpts())
	if err != nil {
		t.Fatal(err)
	}
	defer db3.Close()
	mustNotFound(t, db3, "K1")
	mustGet(t, db3, "K2", "V2")
}

// 9. AutoCompact 模式下,Flush 自动触发级联合并,语义保持一致。
func TestAutoCompactKeepsSemantics(t *testing.T) {
	opts := smallOpts()
	opts.AutoCompact = true
	db, _, cleanup := openTemp(t, opts)
	defer cleanup()

	for i := 0; i < 30; i++ {
		key := fmt.Sprintf("key%02d", i)
		db.Put(key, []byte(fmt.Sprintf("val%02d", i)))
		if i%3 == 0 {
			db.Delete(key)
		}
		if err := db.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 30; i++ {
		key := fmt.Sprintf("key%02d", i)
		if i%3 == 0 {
			mustNotFound(t, db, key)
		} else {
			mustGet(t, db, key, fmt.Sprintf("val%02d", i))
		}
	}
}
