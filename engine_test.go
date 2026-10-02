package lsm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testValue(i int) []byte {
	return []byte(fmt.Sprintf("value-%08d-%s", i, strings.Repeat("x", 90)))
}

func countFiles(t *testing.T, dir, suffix string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), suffix) {
			out = append(out, e.Name())
		}
	}
	return out
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}

// 验收主场景: 高频写入两次触发 4MB 阈值, 自动轮转生成 2 个 Immutable
// MemTable, 后台单线程顺序刷入 L0, WAL 安全轮转, 数据完整可读。
func TestRotationAndSequentialFlush(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(dir, nil) // 默认阈值 4MB
	if err != nil {
		t.Fatal(err)
	}

	const total = 70000 // 约 9.2MB, 恰好触发 2 次轮转
	for i := 0; i < total; i++ {
		key := []byte(fmt.Sprintf("key%08d", i))
		if err := eng.Put(key, testValue(i)); err != nil {
			t.Fatal(err)
		}
	}

	s := eng.Stats()
	if s.Rotations != 2 {
		t.Fatalf("rotations = %d, want 2", s.Rotations)
	}

	// 等待后台单线程顺序刷盘完成。
	waitFor(t, 30*time.Second, func() bool {
		s := eng.Stats()
		return s.Flushes == 2 && s.QueueLength == 0
	}, "2 immutable memtables flushed to L0")

	s = eng.Stats()
	if s.L0Files != 2 {
		t.Fatalf("L0 files = %d, want 2", s.L0Files)
	}

	// 磁盘上应有 2 个 SSTable + 1 个活跃 WAL 段(旧段已安全删除)。
	ssts := countFiles(t, dir, ".sst")
	if len(ssts) != 2 || ssts[0] != "L0-000000.sst" || ssts[1] != "L0-000001.sst" {
		t.Fatalf("unexpected sst files: %v", ssts)
	}
	logs := countFiles(t, dir, ".log")
	if len(logs) != 1 || logs[0] != "wal-000002.log" {
		t.Fatalf("unexpected wal files: %v", logs)
	}

	// 全量校验: 所有 key 可读且值正确(横跨 L0 两个文件与活跃表)。
	for i := 0; i < total; i++ {
		got, ok, err := eng.Get(fmt.Sprintf("key%08d", i))
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("key %d missing", i)
		}
		if string(got) != string(testValue(i)) {
			t.Fatalf("key %d value mismatch", i)
		}
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
}

// 前台写入不因后台刷盘而停顿: 刷盘被人为阻塞时, 写入与轮转照常进行。
func TestForegroundWritesNotStalled(t *testing.T) {
	dir := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	eng, err := Open(dir, &Options{
		MemtableThreshold: 1 << 20, // 1MB, 加速测试
		FlushHook: func() {
			once.Do(func() { close(entered) })
			<-release // 阻塞刷盘线程, 模拟慢速磁盘
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	put := func(i int) {
		if err := eng.Put([]byte(fmt.Sprintf("key%08d", i)), testValue(i)); err != nil {
			t.Errorf("put %d: %v", i, err)
		}
	}

	const perTable = 8000 // 约 1.05MB, 触发一次轮转
	for i := 0; i < perTable; i++ {
		put(i)
	}
	<-entered // 第一次刷盘已开始并被阻塞

	// 刷盘阻塞期间继续写入, 触发第二次轮转。
	for i := perTable; i < 2*perTable; i++ {
		put(i)
	}
	if s := eng.Stats(); s.Rotations != 2 {
		t.Fatalf("rotations = %d while flush blocked, want 2", s.Rotations)
	}

	// 刷盘阻塞期间, 前台批量写入应迅速返回(不停顿)。
	start := time.Now()
	for i := 2 * perTable; i < 2*perTable+2000; i++ {
		put(i)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("foreground writes stalled for %v while flush blocked", d)
	}

	close(release) // 放行刷盘
	waitFor(t, 30*time.Second, func() bool {
		s := eng.Stats()
		return s.Flushes == 2 && s.QueueLength == 0
	}, "blocked flushes completed")

	// 数据完整性抽查。
	for _, i := range []int{0, perTable - 1, perTable, 2*perTable - 1, 2*perTable + 1999} {
		got, ok, err := eng.Get(fmt.Sprintf("key%08d", i))
		if err != nil || !ok || string(got) != string(testValue(i)) {
			t.Fatalf("key %d: ok=%v err=%v", i, ok, err)
		}
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
}

// 崩溃恢复: 未关闭引擎直接重开, 活跃 WAL 段回放后数据完整。
func TestWALRecoveryActiveSegment(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	const total = 5000
	for i := 0; i < total; i++ {
		if err := eng.Put([]byte(fmt.Sprintf("key%08d", i)), testValue(i)); err != nil {
			t.Fatal(err)
		}
	}
	// 不调用 Close, 模拟进程崩溃(WAL 写入不经过用户态缓冲, 已落盘)。

	eng2, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < total; i++ {
		got, ok, err := eng2.Get(fmt.Sprintf("key%08d", i))
		if err != nil || !ok || string(got) != string(testValue(i)) {
			t.Fatalf("recovered key %d: ok=%v err=%v", i, ok, err)
		}
	}
	if err := eng2.Close(); err != nil {
		t.Fatal(err)
	}
	eng.Close()
}

// 崩溃恢复: 轮转后刷盘尚未完成时崩溃, 两个 WAL 段都被恢复,
// 旧段重建为 Immutable 并重新排队刷盘。
func TestWALRecoveryWithImmutable(t *testing.T) {
	dir := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	eng, err := Open(dir, &Options{
		MemtableThreshold: 1 << 20,
		FlushHook: func() {
			once.Do(func() { close(entered) })
			<-release
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	const total = 10000 // 触发 1 次轮转, 产生 2 个 WAL 段
	for i := 0; i < total; i++ {
		if err := eng.Put([]byte(fmt.Sprintf("key%08d", i)), testValue(i)); err != nil {
			t.Fatal(err)
		}
	}
	<-entered // 刷盘线程被阻塞, 旧 WAL 段仍在磁盘上
	if logs := countFiles(t, dir, ".log"); len(logs) != 2 {
		t.Fatalf("wal segments before crash = %v, want 2", logs)
	}
	// 模拟崩溃: 不 Close, 直接重开。

	eng2, err := Open(dir, &Options{MemtableThreshold: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < total; i++ {
		got, ok, err := eng2.Get(fmt.Sprintf("key%08d", i))
		if err != nil || !ok || string(got) != string(testValue(i)) {
			t.Fatalf("recovered key %d: ok=%v err=%v", i, ok, err)
		}
	}
	// 恢复的 Immutable 被重新刷盘, 旧 WAL 段被清理。
	waitFor(t, 30*time.Second, func() bool {
		return eng2.Stats().Flushes >= 1
	}, "recovered immutable re-flushed")

	close(release)
	if err := eng2.Close(); err != nil {
		t.Fatal(err)
	}
	eng.Close()
}

// 正常关闭: 活跃表被刷入 L0, 全部 WAL 段删除, 重开后数据仍在。
func TestCloseFlushesActiveMemtable(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		if err := eng.Put([]byte(fmt.Sprintf("key%08d", i)), testValue(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	if logs := countFiles(t, dir, ".log"); len(logs) != 0 {
		t.Fatalf("wal segments after close = %v, want none", logs)
	}
	if ssts := countFiles(t, dir, ".sst"); len(ssts) != 1 {
		t.Fatalf("sst files after close = %v, want 1", ssts)
	}

	eng2, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	for i := 0; i < 1000; i++ {
		got, ok, err := eng2.Get(fmt.Sprintf("key%08d", i))
		if err != nil || !ok || string(got) != string(testValue(i)) {
			t.Fatalf("reopened key %d: ok=%v err=%v", i, ok, err)
		}
	}
}

// 删除(墓碑)语义: 删除后不可读, 刷盘与恢复后依然不可读。
func TestDeleteTombstone(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(dir, &Options{MemtableThreshold: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9000; i++ { // 触发一次轮转+刷盘
		if err := eng.Put([]byte(fmt.Sprintf("key%08d", i)), testValue(i)); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 30*time.Second, func() bool { return eng.Stats().Flushes == 1 }, "flush")
	for _, i := range []int{0, 4500, 8999} {
		if err := eng.Delete([]byte(fmt.Sprintf("key%08d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	for _, i := range []int{0, 4500, 8999} {
		if _, ok, _ := eng2.Get(fmt.Sprintf("key%08d", i)); ok {
			t.Fatalf("deleted key %d still visible", i)
		}
	}
	if _, ok, _ := eng2.Get("key00000001"); !ok {
		t.Fatal("live key missing after delete of others")
	}
}

// SSTable 单元测试: 写入-读取往返, 含稀疏索引定位与墓碑。
func TestSSTableRoundTrip(t *testing.T) {
	dir := t.TempDir()
	mt := newMemtable()
	const n = 1000
	for i := 0; i < n; i++ {
		mt.put([]byte(fmt.Sprintf("key%05d", i)), testValue(i))
	}
	mt.delete([]byte("key00007"))
	path := filepath.Join(dir, "L0-000000.sst")
	if err := writeSSTable(path, mt.sorted()); err != nil {
		t.Fatal(err)
	}
	sst, err := openSSTable(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sst.close()

	for _, i := range []int{0, 1, 63, 64, 65, 500, n - 1} {
		if i == 7 {
			continue
		}
		got, ok, err := sst.get(fmt.Sprintf("key%05d", i))
		if err != nil || !ok || string(got.value) != string(testValue(i)) {
			t.Fatalf("sst get %d: ok=%v err=%v", i, ok, err)
		}
	}
	if e, ok, _ := sst.get("key00007"); !ok || !e.deleted {
		t.Fatal("tombstone not preserved")
	}
	if _, ok, _ := sst.get("key01000"); ok {
		t.Fatal("unexpected hit for missing key")
	}
	if _, ok, _ := sst.get("aaa"); ok {
		t.Fatal("unexpected hit below first key")
	}
}
