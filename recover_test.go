package main

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func tempPaths(t *testing.T) (string, string, func()) {
	dir, err := ioutil.TempDir("", "logrec")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "log.data"), filepath.Join(dir, "log.index"), func() {
		os.RemoveAll(dir)
	}
}

// writeRecords 直接按格式写 n 条完整数据记录，返回总字节数。
func writeRecords(t *testing.T, dataPath string, payloads [][]byte) int64 {
	f, err := os.OpenFile(dataPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var n int64
	for _, p := range payloads {
		b := encodeRecord(p)
		if _, err := f.Write(b); err != nil {
			t.Fatal(err)
		}
		n += int64(len(b))
	}
	return n
}

// writeIndex 为前 k 条记录生成索引项（顺序衔接）。
func writeIndex(t *testing.T, indexPath string, payloads [][]byte, k int) {
	f, err := os.OpenFile(indexPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var off uint64
	for i := 0; i < k; i++ {
		e := indexEntry{Offset: off, Length: uint32(len(payloads[i])), Digest: checksum(payloads[i])}
		if _, err := f.Write(encodeIndexEntry(e)); err != nil {
			t.Fatal(err)
		}
		off += uint64(recordHeaderSize) + uint64(len(payloads[i]))
	}
}

func payloads(n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = bytes.Repeat([]byte{byte('a' + i)}, 10+i)
	}
	return out
}

// verifyStore 重开并校验记录内容恰好等于 want，不漏不重。
func verifyStore(t *testing.T, dataPath, indexPath string, want [][]byte) {
	s, rep, err := Open(dataPath, indexPath, false)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	defer s.Close()
	if s.Len() != len(want) {
		t.Fatalf("record count = %d, want %d (report: %s)", s.Len(), len(want), rep)
	}
	for i, w := range want {
		got, err := s.Get(i)
		if err != nil {
			t.Fatalf("get %d: %v", i, err)
		}
		if !bytes.Equal(got, w) {
			t.Fatalf("record %d mismatch", i)
		}
	}
}

// verifyIdempotent 再次恢复应为空操作（不漏不重的幂等性）。
func verifyIdempotent(t *testing.T, dataPath, indexPath string, entries int) {
	rep, err := Recover(dataPath, indexPath)
	if err != nil {
		t.Fatalf("second recover: %v", err)
	}
	if rep.ValidIndexEntries != entries || rep.DroppedIndexEntries != 0 ||
		rep.RebuiltEntries != 0 || rep.TruncatedDataAt >= 0 || rep.PartialIndexTail != 0 {
		t.Fatalf("recover not idempotent: %s", rep)
	}
}

// F2：索引落后于数据 —— 数据 5 条，索引只有 3 条，应补建 2 条。
func TestRecoverIndexLags(t *testing.T) {
	dp, ip, cleanup := tempPaths(t)
	defer cleanup()
	p := payloads(5)
	writeRecords(t, dp, p)
	writeIndex(t, ip, p, 3)

	rep, err := Recover(dp, ip)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ValidIndexEntries != 3 || rep.RebuiltEntries != 2 {
		t.Fatalf("unexpected report: %s", rep)
	}
	if rep.RebuildFromOffset < 0 || rep.RebuildToOffset <= rep.RebuildFromOffset {
		t.Fatalf("bad rebuild range: %s", rep)
	}
	verifyStore(t, dp, ip, p)
	verifyIdempotent(t, dp, ip, 5)
}

// F3：索引含未持久化尾 —— 索引 5 项，数据只有 3 条，应丢弃 2 项。
func TestRecoverIndexAhead(t *testing.T) {
	dp, ip, cleanup := tempPaths(t)
	defer cleanup()
	p := payloads(5)
	writeRecords(t, dp, p[:3])
	writeIndex(t, ip, p, 5)

	rep, err := Recover(dp, ip)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ValidIndexEntries != 3 || rep.DroppedIndexEntries != 2 {
		t.Fatalf("unexpected report: %s", rep)
	}
	verifyStore(t, dp, ip, p[:3])
	verifyIdempotent(t, dp, ip, 3)
}

// F4：数据尾部撕裂写 —— 3 条完整记录 + 半条记录，应截断数据尾。
func TestRecoverTornDataTail(t *testing.T) {
	dp, ip, cleanup := tempPaths(t)
	defer cleanup()
	p := payloads(4)
	full := writeRecords(t, dp, p[:3])
	// 追加半条第 4 记录（只有头的一部分）。
	f, _ := os.OpenFile(dp, os.O_WRONLY|os.O_APPEND, 0644)
	f.Write(encodeRecord(p[3])[:7])
	f.Close()
	writeIndex(t, ip, p, 3)

	rep, err := Recover(dp, ip)
	if err != nil {
		t.Fatal(err)
	}
	if rep.TruncatedDataAt != full || rep.TruncatedBytes != 7 {
		t.Fatalf("unexpected truncation: %s", rep)
	}
	fi, _ := os.Stat(dp)
	if fi.Size() != full {
		t.Fatalf("data size = %d, want %d", fi.Size(), full)
	}
	verifyStore(t, dp, ip, p[:3])
	verifyIdempotent(t, dp, ip, 3)
}

// F5：索引尾部撕裂写 —— 索引大小非 16 的倍数，应截掉残缺字节。
func TestRecoverTornIndexTail(t *testing.T) {
	dp, ip, cleanup := tempPaths(t)
	defer cleanup()
	p := payloads(3)
	writeRecords(t, dp, p)
	writeIndex(t, ip, p, 3)
	f, _ := os.OpenFile(ip, os.O_WRONLY|os.O_APPEND, 0644)
	f.Write([]byte{0xDE, 0xAD, 0xBE}) // 3 字节残缺
	f.Close()

	rep, err := Recover(dp, ip)
	if err != nil {
		t.Fatal(err)
	}
	if rep.PartialIndexTail != 3 {
		t.Fatalf("unexpected report: %s", rep)
	}
	verifyStore(t, dp, ip, p)
	verifyIdempotent(t, dp, ip, 3)
}

// F6：完整坏记录 —— framing 完整但 CRC 不符，必须报告且不得当尾截断。
func TestRecoverCorruptRecord(t *testing.T) {
	dp, ip, cleanup := tempPaths(t)
	defer cleanup()
	p := payloads(5)
	writeRecords(t, dp, p)
	writeIndex(t, ip, p, 2)

	// 翻转第 3 条记录 payload 的一个字节（记录保持完整）。
	var off int64
	for i := 0; i < 2; i++ {
		off += int64(recordHeaderSize + len(p[i]))
	}
	f, _ := os.OpenFile(dp, os.O_RDWR, 0644)
	one := make([]byte, 1)
	f.ReadAt(one, off+recordHeaderSize)
	one[0] ^= 0xFF
	f.WriteAt(one, off+recordHeaderSize)
	sizeBefore, _ := f.Stat()
	f.Close()

	rep, err := Recover(dp, ip)
	if err != ErrCorruptRecord {
		t.Fatalf("err = %v, want ErrCorruptRecord", err)
	}
	if len(rep.Corrupt) != 1 || rep.Corrupt[0].Offset != off {
		t.Fatalf("corrupt not reported correctly: %s", rep)
	}
	if rep.TruncatedDataAt >= 0 {
		t.Fatalf("corrupt record must not be treated as tail truncation: %s", rep)
	}
	// 坏记录不得保留原样：文件大小不变。
	fi, _ := os.Stat(dp)
	if fi.Size() != sizeBefore.Size() {
		t.Fatalf("corrupt record was silently truncated")
	}
	// 腐败点之前的部分可用。
	s, _, oerr := Open(dp, ip, false)
	if oerr != ErrCorruptRecord {
		t.Fatalf("open err = %v, want ErrCorruptRecord", oerr)
	}
	if s.Len() != 2 {
		t.Fatalf("recovered prefix = %d records, want 2", s.Len())
	}
	for i := 0; i < 2; i++ {
		got, err := s.Get(i)
		if err != nil || !bytes.Equal(got, p[i]) {
			t.Fatalf("record %d: got %v err %v", i, got, err)
		}
	}
	s.Close()
	// 重开仍报告同一坏记录，不会静默跳过。
	_, err = Recover(dp, ip)
	if err != ErrCorruptRecord {
		t.Fatalf("reopen should keep reporting corruption, got %v", err)
	}
}

// 索引项内容损坏（摘要被改）：其后索引全部作废，从数据重建。
func TestRecoverGarbageIndexEntry(t *testing.T) {
	dp, ip, cleanup := tempPaths(t)
	defer cleanup()
	p := payloads(4)
	writeRecords(t, dp, p)
	writeIndex(t, ip, p, 4)
	// 破坏第 2 项的摘要。
	f, _ := os.OpenFile(ip, os.O_RDWR, 0644)
	f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF}, 1*indexEntrySize+12)
	f.Close()

	rep, err := Recover(dp, ip)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ValidIndexEntries != 1 || rep.DroppedIndexEntries != 3 || rep.RebuiltEntries != 3 {
		t.Fatalf("unexpected report: %s", rep)
	}
	verifyStore(t, dp, ip, p)
	verifyIdempotent(t, dp, ip, 4)
}

// 端到端：多轮“写入 -> 模拟崩溃 -> 恢复 -> 再写入”，重开结果不漏不重。
func TestReopenNoMissNoDup(t *testing.T) {
	dp, ip, cleanup := tempPaths(t)
	defer cleanup()
	var want [][]byte

	appendN := func(n int) {
		s, _, err := Open(dp, ip, false)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			p := []byte{byte(len(want))}
			if _, err := s.Append(p); err != nil {
				t.Fatal(err)
			}
			want = append(want, p)
		}
		s.Close()
	}

	appendN(3)
	// 模拟崩溃：索引尾被截掉 2 项（等价于 F2 索引落后）。
	fi, _ := os.Stat(ip)
	f, _ := os.OpenFile(ip, os.O_RDWR, 0644)
	f.Truncate(fi.Size() - 2*indexEntrySize)
	f.Close()
	verifyStore(t, dp, ip, want) // 恢复应补回 2 条

	appendN(2)
	// 模拟崩溃：数据尾部 1 条记录丢失但索引已写（F3）。
	di, _ := os.Stat(dp)
	last := encodeRecord(want[len(want)-1])
	df, _ := os.OpenFile(dp, os.O_RDWR, 0644)
	df.Truncate(di.Size() - int64(len(last)))
	df.Close()
	want = want[:len(want)-1]
	verifyStore(t, dp, ip, want) // 恢复应丢弃悬空索引项

	appendN(4)
	verifyStore(t, dp, ip, want)
	verifyIdempotent(t, dp, ip, len(want))
}

// 空文件恢复。
func TestRecoverEmpty(t *testing.T) {
	dp, ip, cleanup := tempPaths(t)
	defer cleanup()
	rep, err := Recover(dp, ip)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ValidIndexEntries != 0 || rep.RebuiltEntries != 0 {
		t.Fatalf("unexpected report: %s", rep)
	}
	verifyStore(t, dp, ip, nil)
}
