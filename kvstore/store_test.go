package kvstore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var errSimCrash = errors.New("simulated crash")

// simulateCrash 模拟进程被杀：直接关掉所有文件句柄，丢弃内存状态，不做任何清理。
func (s *Store) simulateCrash() {
	for _, seg := range s.segments {
		seg.file.Close()
	}
	s.closed = true
}

func countSegFiles(t *testing.T, dir string) int {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range ents {
		if filepath.Ext(e.Name()) == ".seg" {
			n++
		}
	}
	return n
}

func mustGet(t *testing.T, s *Store, key, want string) {
	t.Helper()
	got, err := s.Get([]byte(key))
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if string(got) != want {
		t.Fatalf("Get(%q) = %q, want %q", key, got, want)
	}
}

func mustNotFound(t *testing.T, s *Store, key string) {
	t.Helper()
	if _, err := s.Get([]byte(key)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(%q) err = %v, want ErrNotFound", key, err)
	}
}

// 同键 写->删->写，只能看到末值；压缩与重启后依然成立。
func TestWriteDeleteWriteLastWins(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, &Options{MaxSegmentBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put([]byte("k"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete([]byte("k")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put([]byte("k"), []byte("v2")); err != nil {
		t.Fatal(err)
	}
	mustGet(t, s, "k", "v2")

	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	mustGet(t, s, "k", "v2")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustGet(t, s, "k", "v2")
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}
}

// 写入多个键并触发段轮转，重启后扫描各段重建索引。
func TestReopenRebuildsIndex(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, &Options{MaxSegmentBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if err := s.Put([]byte(fmt.Sprintf("key%02d", i)), []byte(fmt.Sprintf("val%02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if n := s.SegmentCount(); n < 2 {
		t.Fatalf("SegmentCount = %d, want rotation to create multiple segments", n)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 30; i++ {
		mustGet(t, s, fmt.Sprintf("key%02d", i), fmt.Sprintf("val%02d", i))
	}
}

// 压缩只保留可见记录：被覆盖的旧值与墓碑全部丢弃，段文件数下降。
func TestCompactionDropsShadowedAndTombstones(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, &Options{MaxSegmentBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := s.Put([]byte(fmt.Sprintf("k%d", i)), []byte("old")); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ { // 覆盖，产生影子记录
		if err := s.Put([]byte(fmt.Sprintf("k%d", i)), []byte(fmt.Sprintf("new%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ { // 删除一半，产生墓碑
		if err := s.Delete([]byte(fmt.Sprintf("k%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	before := countSegFiles(t, dir)
	if before < 3 {
		t.Fatalf("seg files before compact = %d, want >= 3", before)
	}
	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	if got := countSegFiles(t, dir); got != 2 {
		t.Fatalf("seg files after compact = %d, want 2 (compacted + active)", got)
	}
	for i := 0; i < 5; i++ {
		mustNotFound(t, s, fmt.Sprintf("k%d", i))
	}
	for i := 5; i < 10; i++ {
		mustGet(t, s, fmt.Sprintf("k%d", i), fmt.Sprintf("new%d", i))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// 重启后同样成立（压缩结果已持久化）。
	s, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Len() != 5 {
		t.Fatalf("Len after reopen = %d, want 5", s.Len())
	}
	for i := 5; i < 10; i++ {
		mustGet(t, s, fmt.Sprintf("k%d", i), fmt.Sprintf("new%d", i))
	}
}

// 断电在活跃段尾部留下半条记录：重启时截断，已有数据完好，可继续写。
func TestTornTailTruncatedOnRecovery(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, &Options{SyncWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put([]byte("b"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	// 模拟断电：一条记录只写了一半。
	torn := encodeRecord([]byte("c"), []byte("3"), false)
	actPath := s.segments[s.order[len(s.order)-1]].path
	f, err := os.OpenFile(actPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(torn[:len(torn)-5]); err != nil {
		t.Fatal(err)
	}
	f.Close()
	s.simulateCrash()

	s, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustGet(t, s, "a", "1")
	mustGet(t, s, "b", "2")
	mustNotFound(t, s, "c")                                 // 撕裂记录被截断，不可见
	if err := s.Put([]byte("d"), []byte("4")); err != nil { // 截断后仍可追加
		t.Fatal(err)
	}
	mustGet(t, s, "d", "4")
}

// 读者 pin 住旧段期间，压缩不能回收底层文件；读者结束后文件才被删除。
func TestOldSegmentReclaimedOnlyAfterReadersFinish(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, &Options{MaxSegmentBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Put([]byte("pin"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ { // 写满并轮转，让 pin 所在段变成不可变段
		if err := s.Put([]byte(fmt.Sprintf("filler%d", i)), []byte("xxxxxxxx")); err != nil {
			t.Fatal(err)
		}
	}

	// 模拟一个慢读者：固定段句柄。
	s.mu.RLock()
	loc := s.index["pin"]
	seg := s.segments[loc.segID]
	seg.pin()
	s.mu.RUnlock()

	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(seg.path); err != nil {
		t.Fatalf("segment reclaimed while reader still holds it: %v", err)
	}
	// 慢读者仍能从旧段读到数据。
	buf, err := seg.readRecordAt(loc.offset, loc.length)
	if err != nil {
		t.Fatalf("read from pinned segment: %v", err)
	}
	_, val, _, err := decodeRecord(buf)
	if err != nil || string(val) != "value" {
		t.Fatalf("pinned read = %q, %v", val, err)
	}

	seg.unpin() // 最后一个读者离开，文件应被回收
	if _, err := os.Stat(seg.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("segment not reclaimed after last reader, stat err = %v", err)
	}
	mustGet(t, s, "pin", "value") // 新读者走压缩后的新段
}

// 并发读写与压缩交叉：任何读者都不能读到已回收的文件。
func TestConcurrentReadsDuringCompaction(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, &Options{MaxSegmentBytes: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 20; i++ {
		if err := s.Put([]byte(fmt.Sprintf("k%02d", i)), []byte(fmt.Sprintf("v%02d", i))); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	errCh := make(chan error, 64)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("k%02d", (g+i)%20)
				got, err := s.Get([]byte(key))
				if err != nil {
					errCh <- fmt.Errorf("Get(%q): %w", key, err)
					return
				}
				if string(got) != fmt.Sprintf("v%02d", (g+i)%20) {
					errCh <- fmt.Errorf("Get(%q) = %q", key, got)
					return
				}
			}
		}(g)
	}
	for c := 0; c < 5; c++ {
		for i := 0; i < 20; i++ { // 制造轮转与垃圾
			if err := s.Put([]byte(fmt.Sprintf("tmp%d", i)), []byte("junk")); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Compact(); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

// 压缩在 rename 之前崩溃：旧清单完好，重启后数据完整，临时文件被清理。
func TestCrashBeforeManifestRename(t *testing.T) {
	dir := t.TempDir()
	faults := &Faults{}
	s, err := Open(dir, &Options{MaxSegmentBytes: 64, Faults: faults})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := s.Put([]byte(fmt.Sprintf("k%d", i)), []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete([]byte("k0")); err != nil {
		t.Fatal(err)
	}
	segsBefore := countSegFiles(t, dir)

	faults.BeforeManifestRename = func() { panic(errSimCrash) }
	func() {
		defer func() {
			if r := recover(); r != errSimCrash {
				t.Fatalf("recover = %v, want simulated crash", r)
			}
		}()
		s.Compact()
	}()
	s.simulateCrash()

	s, err = Open(dir, nil) // 崩溃后重启恢复
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustNotFound(t, s, "k0")
	for i := 1; i < 10; i++ {
		mustGet(t, s, fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
	}
	if got := countSegFiles(t, dir); got != segsBefore {
		t.Fatalf("seg files = %d, want %d (orphan compacted segment cleaned)", got, segsBefore)
	}
	if _, err := os.Stat(filepath.Join(dir, "MANIFEST.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("MANIFEST.tmp should be cleaned, stat err = %v", err)
	}
}

// 压缩在 rename 之后崩溃：新清单已生效，重启后走压缩后的段，旧段被清理。
func TestCrashAfterManifestRename(t *testing.T) {
	dir := t.TempDir()
	faults := &Faults{}
	s, err := Open(dir, &Options{MaxSegmentBytes: 64, Faults: faults})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := s.Put([]byte(fmt.Sprintf("k%d", i)), []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete([]byte("k0")); err != nil {
		t.Fatal(err)
	}

	faults.AfterManifestRename = func() { panic(errSimCrash) }
	func() {
		defer func() {
			if r := recover(); r != errSimCrash {
				t.Fatalf("recover = %v, want simulated crash", r)
			}
		}()
		s.Compact()
	}()
	s.simulateCrash()

	s, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustNotFound(t, s, "k0")
	for i := 1; i < 10; i++ {
		mustGet(t, s, fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
	}
	if got := countSegFiles(t, dir); got != 2 {
		t.Fatalf("seg files = %d, want 2 (compacted + active); old segments must be reclaimed", got)
	}
}

// 崩溃发生在记录落盘后、应答前：重启扫描会重放这条完整记录。
func TestCrashAfterRecordAppend(t *testing.T) {
	dir := t.TempDir()
	faults := &Faults{}
	s, err := Open(dir, &Options{Faults: faults})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put([]byte("stable"), []byte("ok")); err != nil {
		t.Fatal(err)
	}
	faults.AfterRecordAppend = func() { panic(errSimCrash) }
	func() {
		defer func() {
			if r := recover(); r != errSimCrash {
				t.Fatalf("recover = %v, want simulated crash", r)
			}
		}()
		s.Put([]byte("unacked"), []byte("on-disk"))
	}()
	s.simulateCrash()

	s, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustGet(t, s, "stable", "ok")
	// 完整落盘的记录恢复后可见（调用方虽未收到成功应答）。
	mustGet(t, s, "unacked", "on-disk")
}

// 记录编解码往返。
func TestRecordRoundTrip(t *testing.T) {
	for _, tomb := range []bool{false, true} {
		rec := encodeRecord([]byte("hello"), []byte("world"), tomb)
		k, v, tb, err := decodeRecord(rec)
		if err != nil {
			t.Fatal(err)
		}
		if string(k) != "hello" || tb != tomb {
			t.Fatalf("round trip = %q/%v, tomb=%v", k, v, tb)
		}
		if !tomb && string(v) != "world" {
			t.Fatalf("value = %q", v)
		}
	}
	// 篡改一个字节后 CRC 必须失败。
	rec := encodeRecord([]byte("hello"), []byte("world"), false)
	rec[len(rec)-1] ^= 0xff
	if _, _, _, err := decodeRecord(rec); !errors.Is(err, errCorrupt) {
		t.Fatalf("corrupt record decoded without error")
	}
	if _, _, _, err := decodeRecord(rec[:len(rec)-3]); !errors.Is(err, errCorrupt) {
		t.Fatalf("truncated record decoded without error")
	}
	_ = bytes.MinRead // keep bytes import if unused elsewhere
}
