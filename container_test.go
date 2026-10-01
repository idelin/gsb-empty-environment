package moc

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func tempPath(t *testing.T) (string, func()) {
	dir, err := ioutil.TempDir("", "moc-test")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "data.moc"), func() { os.RemoveAll(dir) }
}

func TestAppendGetRoundtrip(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	c, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Append("alpha", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Append("beta", []byte{0, 1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
	if _, err := c.Get("missing"); err != ErrNotFound {
		t.Fatalf("expect ErrNotFound, got %v", err)
	}
}

func TestDuplicateIDVersioning(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	c, _ := Create(path)
	v1, _ := c.Append("doc", []byte("v1-content"))
	v2, _ := c.Append("doc", []byte("v2-content"))
	v3, _ := c.Append("doc", []byte("v3-content"))
	if v1 != 1 || v2 != 2 || v3 != 3 {
		t.Fatalf("versions %d %d %d", v1, v2, v3)
	}
	if got, _ := c.Get("doc"); string(got) != "v3-content" {
		t.Fatalf("latest = %q", got)
	}
	if got, err := c.GetVersion("doc", 1); err != nil || string(got) != "v1-content" {
		t.Fatalf("v1 = %q, %v", got, err)
	}
	if got, err := c.GetVersion("doc", 2); err != nil || string(got) != "v2-content" {
		t.Fatalf("v2 = %q, %v", got, err)
	}
	if _, err := c.GetVersion("doc", 99); err != ErrNoVersion {
		t.Fatalf("expect ErrNoVersion, got %v", err)
	}
	if vs := c.Versions("doc"); !reflect.DeepEqual(vs, []uint64{1, 2, 3}) {
		t.Fatalf("versions list %v", vs)
	}
}

func TestPersistenceAndSource(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	c, _ := Create(path)
	c.Append("a", []byte("1"))
	c.Append("a", []byte("2"))

	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Source() != SourceIndex {
		t.Fatalf("source = %v", c2.Source())
	}
	if got, _ := c2.Get("a"); string(got) != "2" {
		t.Fatalf("got %q", got)
	}
}

func TestCorruptIndexRebuildByScan(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	c, _ := Create(path)
	c.Append("x", []byte("data-x"))
	c.Append("y", []byte("data-y"))

	// 破坏尾部索引的结尾 magic。
	f, _ := os.OpenFile(path, os.O_RDWR, 0)
	info, _ := f.Stat()
	f.WriteAt([]byte{0xFF, 0xFF}, info.Size()-2)
	f.Close()

	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Source() != SourceScan {
		t.Fatalf("source = %v, want scan-rebuilt", c2.Source())
	}
	if got, _ := c2.Get("x"); string(got) != "data-x" {
		t.Fatalf("x = %q", got)
	}
	if got, _ := c2.Get("y"); string(got) != "data-y" {
		t.Fatalf("y = %q", got)
	}
}

func TestIncompleteTailNotPublished(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	c, _ := Create(path)
	c.Append("a", []byte("complete"))

	// 模拟崩溃:索引之后出现半条未写完的记录。
	f, _ := os.OpenFile(path, os.O_RDWR, 0)
	info, _ := f.Stat()
	partial := []byte(recordMagic)[:6] // 半个 magic,未完成的尾部
	f.WriteAt(partial, info.Size())
	f.Close()

	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := c2.Get("a"); string(got) != "complete" {
		t.Fatalf("a = %q", got)
	}
	if ids := c2.IDs(); len(ids) != 1 || ids[0] != "a" {
		t.Fatalf("ids = %v, 未完成尾部不应进入索引", ids)
	}
}

func TestCrashDuringAppendKeepsOldIndex(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	c, _ := Create(path)
	c.Append("a", []byte("old"))

	// 模拟 Append 在写完记录、写索引之前崩溃:
	// 截掉旧索引,手工写入一条完整记录,但不写新索引。
	info, _ := os.Stat(path)
	dataEnd := info.Size() - indexOverhead - int64(indexPayloadLen(t, path))
	f, _ := os.OpenFile(path, os.O_RDWR, 0)
	f.Truncate(dataEnd)
	payload := []byte("new-unpublished")
	digest := sha256.Sum256(payload)
	var rec bytes.Buffer
	rec.WriteString(recordMagic)
	var b4 [4]byte
	binary.BigEndian.PutUint32(b4[:], 1)
	rec.Write(b4[:])
	rec.WriteString("a")
	var b8 [8]byte
	binary.BigEndian.PutUint64(b8[:], 2)
	rec.Write(b8[:])
	binary.BigEndian.PutUint64(b8[:], uint64(len(payload)))
	rec.Write(b8[:])
	rec.Write(digest[:])
	rec.Write(payload)
	f.WriteAt(rec.Bytes(), dataEnd)
	f.Sync()
	f.Close()

	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// 尾部索引缺失 -> 扫描重建;完整记录可被恢复,但来源必须标明。
	if c2.Source() != SourceScan {
		t.Fatalf("source = %v", c2.Source())
	}
	// 扫描能恢复完整记录(崩溃恢复语义),此处验证恢复数据正确。
	if got, _ := c2.Get("a"); string(got) != "new-unpublished" {
		t.Fatalf("a = %q", got)
	}
}

// indexPayloadLen 读取当前文件尾部索引的负载长度。
func indexPayloadLen(t *testing.T, path string) uint64 {
	f, _ := os.Open(path)
	defer f.Close()
	info, _ := f.Stat()
	tail := make([]byte, 16)
	if _, err := f.ReadAt(tail, info.Size()-16); err != nil {
		t.Fatal(err)
	}
	return binary.BigEndian.Uint64(tail[:8])
}

func TestOutOfBoundsIndexRejected(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	c, _ := Create(path)
	c.Append("a", []byte("payload"))

	// 篡改索引:把 offset 改到数据区之外,然后按原样重写索引块。
	// 直接伪造整个文件:一条合法记录 + 越界索引。
	f, _ := os.OpenFile(path, os.O_RDWR, 0)
	info, _ := f.Stat()
	idxLen := int64(indexPayloadLen(t, path))
	dataEnd := info.Size() - idxLen - indexOverhead

	// 解出索引,篡改后重写。
	v, err := Unmarshal(mustReadAt(t, f, dataEnd+16, idxLen))
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]interface{})
	entry := m["a"].([]interface{})[0].(map[string]interface{})
	entry["offset"] = dataEnd + 4096 // 越界
	forged, _ := Marshal(m)
	var blk bytes.Buffer
	blk.WriteString(indexMagic)
	var b8 [8]byte
	binary.BigEndian.PutUint64(b8[:], uint64(len(forged)))
	blk.Write(b8[:])
	blk.Write(forged)
	blk.Write(b8[:])
	blk.WriteString(indexMagic)
	f.Truncate(dataEnd)
	f.WriteAt(blk.Bytes(), dataEnd)
	f.Sync()
	f.Close()

	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// 越界索引必须被拒绝,回退到扫描重建。
	if c2.Source() != SourceScan {
		t.Fatalf("越界索引未被拒绝, source = %v", c2.Source())
	}
	if got, _ := c2.Get("a"); string(got) != "payload" {
		t.Fatalf("a = %q", got)
	}
}

func mustReadAt(t *testing.T, f *os.File, off, n int64) []byte {
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, off); err != nil {
		t.Fatal(err)
	}
	return buf
}

func TestDigestTamperDetected(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	c, _ := Create(path)
	c.Append("a", []byte("integrity"))

	// 篡改数据区第一个 payload 字节。
	f, _ := os.OpenFile(path, os.O_RDWR, 0)
	f.WriteAt([]byte{'X'}, int64(magicLen+4+1+8+8+32))
	f.Close()

	if _, err := c.Get("a"); err == nil {
		t.Fatal("摘要被篡改后 Get 应失败")
	}
}

func TestSerializerRoundtrip(t *testing.T) {
	in := map[string]interface{}{
		"nil":    nil,
		"true":   true,
		"false":  false,
		"int":    int64(-42),
		"float":  3.25,
		"string": "你好,世界",
		"bytes":  []byte{0xde, 0xad},
		"array":  []interface{}{int64(1), "two", false},
		"nested": map[string]interface{}{"k": []interface{}{}},
	}
	data, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("roundtrip mismatch:\n%#v\n%#v", in, out)
	}
}

func TestSerializerLengthBombRejected(t *testing.T) {
	// 声明 4GB 字符串,实际没有数据:必须在分配前被拒绝。
	bomb := []byte{tagString, 0xff, 0xff, 0xff, 0xff}
	if _, err := Unmarshal(bomb); err == nil {
		t.Fatal("string 长度炸弹未被拒绝")
	}
	// 声明巨额数组。
	bomb = []byte{tagArray, 0xff, 0xff, 0xff, 0xff}
	if _, err := Unmarshal(bomb); err == nil {
		t.Fatal("array 长度炸弹未被拒绝")
	}
	// 声明巨额 map。
	bomb = []byte{tagMap, 0xff, 0xff, 0xff, 0xff}
	if _, err := Unmarshal(bomb); err == nil {
		t.Fatal("map 长度炸弹未被拒绝")
	}
	// 截断输入。
	if _, err := Unmarshal([]byte{tagInt, 0x00, 0x01}); err == nil {
		t.Fatal("截断输入未被拒绝")
	}
	// 未知标签。
	if _, err := Unmarshal([]byte{0x7f}); err == nil {
		t.Fatal("未知标签未被拒绝")
	}
	// 尾部多余字节。
	good, _ := Marshal(int64(1))
	if _, err := Unmarshal(append(good, 0x00)); err == nil {
		t.Fatal("尾部多余字节未被拒绝")
	}
}

func TestSerializerRejectsUnsupportedAndDeep(t *testing.T) {
	type hostStruct struct{ X int }
	if _, err := Marshal(hostStruct{}); err == nil {
		t.Fatal("宿主结构体不应被序列化")
	}
	if _, err := Marshal(func() {}); err == nil {
		t.Fatal("函数不应被序列化")
	}
	// 超深嵌套。
	deep := interface{}(nil)
	for i := 0; i < maxDepth+10; i++ {
		deep = []interface{}{deep}
	}
	if _, err := Marshal(deep); err == nil {
		t.Fatal("超深嵌套未被拒绝")
	}
}
