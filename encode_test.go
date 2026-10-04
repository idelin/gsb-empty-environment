package set32

import (
	"encoding/binary"
	"math/rand"
	"reflect"
	"testing"
)

// ---------- 手工构造编码的工具 ----------

type rawBlock struct {
	key     uint16
	kind    byte
	resv    byte
	count   uint32
	payload []byte
}

func rawEncode(magic uint32, blocks []rawBlock, trailing []byte) []byte {
	var out []byte
	var tmp [8]byte
	binary.LittleEndian.PutUint32(tmp[:4], magic)
	out = append(out, tmp[:4]...)
	binary.LittleEndian.PutUint32(tmp[:4], uint32(len(blocks)))
	out = append(out, tmp[:4]...)
	for _, b := range blocks {
		binary.LittleEndian.PutUint16(tmp[:2], b.key)
		out = append(out, tmp[:2]...)
		out = append(out, b.kind, b.resv)
		binary.LittleEndian.PutUint32(tmp[:4], b.count)
		out = append(out, tmp[:4]...)
		out = append(out, b.payload...)
	}
	return append(out, trailing...)
}

func rawArray(vals ...uint16) []byte {
	var out []byte
	var tmp [2]byte
	for _, v := range vals {
		binary.LittleEndian.PutUint16(tmp[:2], v)
		out = append(out, tmp[:]...)
	}
	return out
}

func rawRuns(rs ...run) []byte {
	var out []byte
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:4], uint32(len(rs)))
	out = append(out, tmp[:]...)
	for _, r := range rs {
		binary.LittleEndian.PutUint16(tmp[:2], r.lo)
		out = append(out, tmp[:2]...)
		binary.LittleEndian.PutUint16(tmp[:2], r.hi)
		out = append(out, tmp[:2]...)
	}
	return out
}

func rawBitmap(vals ...uint16) []byte {
	var w [bitmapBytes / 8]uint64
	for _, v := range vals {
		w[v>>6] |= uint64(1) << (v & 63)
	}
	out := make([]byte, bitmapBytes)
	for i, x := range w {
		binary.LittleEndian.PutUint64(out[i*8:], x)
	}
	return out
}

// ---------- 往返 ----------

func TestRoundTrip(t *testing.T) {
	s := New()
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		s.Add(rng.Uint32())
	}
	for v := uint32(1000); v < 2000; v++ {
		s.Add(v)
	}
	for v := uint32(9 << 16); v < 9<<16+40000; v++ {
		s.Add(v)
	}
	d, err := Decode(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.Collect(), s.Collect()) {
		t.Fatal("round trip mismatch")
	}
	if !reflect.DeepEqual(d.Encode(), s.Encode()) {
		t.Fatal("re-encode differs")
	}
}

// 规范编码唯一：同一逻辑集合无论以何种路径构造，编码字节必须相同。
func TestCanonicalUniqueness(t *testing.T) {
	vals := []uint32{0, 1, 2, 100, 101, 102, 103, 65535, 65536, 1 << 20, 4294967295}
	for v := uint32(50000); v < 60000; v++ {
		vals = append(vals, v)
	}
	fwd := New()
	for _, v := range vals {
		fwd.Add(v)
	}
	rev := New()
	for i := len(vals) - 1; i >= 0; i-- {
		rev.Add(vals[i])
	}
	// 先加多余元素再删掉。
	extra := New()
	for _, v := range vals {
		extra.Add(v)
	}
	for v := uint32(30000); v < 40000; v++ {
		extra.Add(v)
	}
	for v := uint32(30000); v < 40000; v++ {
		extra.Remove(v)
	}
	// 经 OR 合并两半。
	half1, half2 := New(), New()
	for i, v := range vals {
		if i%2 == 0 {
			half1.Add(v)
		} else {
			half2.Add(v)
		}
	}
	merged := half1.Or(half2)
	// 经解码往返。
	rt, err := Decode(fwd.Encode())
	if err != nil {
		t.Fatal(err)
	}
	want := fwd.Encode()
	for name, s := range map[string]*Set{"rev": rev, "extra": extra, "merged": merged, "roundtrip": rt} {
		if !reflect.DeepEqual(s.Encode(), want) {
			t.Fatalf("%s: encoding differs", name)
		}
	}
}

// 平局转换：字节数相同时按固定优先级 array > runs > bitmap。
func TestTieConversions(t *testing.T) {
	// array vs runs 平局：{0,1,3,4} → n=4(8B) 平 rc=2(8B) → array。
	s := New()
	for _, v := range []uint32{0, 1, 3, 4} {
		s.Add(v)
	}
	checkKind(t, s, 0, kindArray)

	// 三方平局：2048 个长度 2 的 run → n=4096, rc=2048，三者皆 8192B → array。
	s2 := New()
	for k := uint32(0); k < 2048; k++ {
		s2.Add(4*k + 0)
		s2.Add(4*k + 1)
	}
	if s2.blocks[0].n != 4096 || s2.blocks[0].rc != 2048 {
		t.Fatalf("n=%d rc=%d", s2.blocks[0].n, s2.blocks[0].rc)
	}
	checkKind(t, s2, 0, kindArray)

	// runs vs bitmap 平局：2048 个长度 3 的 run → n=6144, rc=2048，
	// array 12288B 出局，runs 8192B 平 bitmap 8192B → runs。
	s3 := New()
	for k := uint32(0); k < 2048; k++ {
		s3.Add(4*k + 0)
		s3.Add(4*k + 1)
		s3.Add(4*k + 2)
	}
	checkKind(t, s3, 0, kindRuns)

	// bitmap 严格最小：全部偶数 → n=32768, rc=32768。
	s4 := New()
	for v := uint32(0); v < 65536; v += 2 {
		s4.Add(v)
	}
	checkKind(t, s4, 0, kindBitmap)

	// 解码器拒绝非规范容器：同一内容用手工 runs / bitmap 编码必须报错。
	blk := rawBlock{key: 0, kind: byte(kindRuns), count: 4, payload: rawRuns(run{0, 1}, run{3, 4})}
	if _, err := Decode(rawEncode(magic, []rawBlock{blk}, nil)); err == nil {
		t.Fatal("non-canonical runs encoding accepted")
	}
	blk = rawBlock{key: 0, kind: byte(kindBitmap), count: 4, payload: rawBitmap(0, 1, 3, 4)}
	if _, err := Decode(rawEncode(magic, []rawBlock{blk}, nil)); err == nil {
		t.Fatal("non-canonical bitmap encoding accepted")
	}
	// 而规范的 array 编码必须接受。
	blk = rawBlock{key: 0, kind: byte(kindArray), count: 4, payload: rawArray(0, 1, 3, 4)}
	if _, err := Decode(rawEncode(magic, []rawBlock{blk}, nil)); err != nil {
		t.Fatalf("canonical encoding rejected: %v", err)
	}
}

// 解码器拒绝各类非法输入。
func TestDecoderRejections(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"bad magic", rawEncode(0xDEADBEEF, nil, nil)},
		{"truncated header", []byte{1, 2, 3}},
		{"empty block", rawEncode(magic, []rawBlock{{key: 0, kind: byte(kindArray), count: 0}}, nil)},
		{"count too large", rawEncode(magic, []rawBlock{{key: 0, kind: byte(kindArray), count: 65537}}, nil)},
		{"array unsorted", rawEncode(magic, []rawBlock{{key: 0, kind: byte(kindArray), count: 2, payload: rawArray(5, 3)}}, nil)},
		{"array duplicate", rawEncode(magic, []rawBlock{{key: 0, kind: byte(kindArray), count: 2, payload: rawArray(5, 5)}}, nil)},
		{"array count mismatch", rawEncode(magic, []rawBlock{{key: 0, kind: byte(kindArray), count: 3, payload: rawArray(1, 2, 3, 4)}}, nil)},
		{"runs overlap", rawEncode(magic, []rawBlock{{key: 0, kind: byte(kindRuns), count: 18, payload: rawRuns(run{0, 10}, run{5, 20})}}, nil)},
		{"runs adjacent", rawEncode(magic, []rawBlock{{key: 0, kind: byte(kindRuns), count: 21, payload: rawRuns(run{0, 10}, run{11, 20})}}, nil)},
		{"run lo>hi", rawEncode(magic, []rawBlock{{key: 0, kind: byte(kindRuns), count: 1, payload: rawRuns(run{10, 5})}}, nil)},
		{"runs count mismatch", rawEncode(magic, []rawBlock{{key: 0, kind: byte(kindRuns), count: 5, payload: rawRuns(run{0, 3})}}, nil)},
		{"zero runs", rawEncode(magic, []rawBlock{{key: 0, kind: byte(kindRuns), count: 1, payload: rawRuns()}}, nil)},
		{"bitmap popcount mismatch", rawEncode(magic, []rawBlock{{key: 0, kind: byte(kindBitmap), count: 3, payload: rawBitmap(1, 2)}}, nil)},
		{"blocks out of order", rawEncode(magic, []rawBlock{
			{key: 5, kind: byte(kindArray), count: 1, payload: rawArray(1)},
			{key: 2, kind: byte(kindArray), count: 1, payload: rawArray(1)},
		}, nil)},
		{"reserved byte", rawEncode(magic, []rawBlock{{key: 0, kind: byte(kindArray), resv: 1, count: 1, payload: rawArray(1)}}, nil)},
		{"unknown kind", rawEncode(magic, []rawBlock{{key: 0, kind: 9, count: 1, payload: rawArray(1)}}, nil)},
		{"trailing bytes", rawEncode(magic, nil, []byte{0})},
		{"truncated payload", rawEncode(magic, []rawBlock{{key: 0, kind: byte(kindArray), count: 2, payload: rawArray(1)}}, nil)},
	}
	for _, c := range cases {
		if _, err := Decode(c.data); err == nil {
			t.Fatalf("%s: expected error, got nil", c.name)
		}
	}
}

// 真实编码尺寸报告（不做性能承诺，只记录实测字节数）。
func TestEncodingSizes(t *testing.T) {
	rng := rand.New(rand.NewSource(9))

	sparse := New()
	for i := 0; i < 1000; i++ {
		sparse.Add(rng.Uint32())
	}
	t.Logf("稀疏 1000 个随机值（全 32 位域）: %d 字节 (%.1f B/元素)",
		sparse.EncodedSize(), float64(sparse.EncodedSize())/1000)

	full := New()
	for v := uint32(0); v < 65536; v++ {
		full.Add(v)
	}
	t.Logf("全块 65536 个连续值: %d 字节 (%.4f B/元素)",
		full.EncodedSize(), float64(full.EncodedSize())/65536)

	million := New()
	for i := 0; i < 1000000; i++ {
		million.Add(rng.Uint32())
	}
	t.Logf("100 万个随机值（全 32 位域）: %d 字节 (%.2f B/元素)",
		million.EncodedSize(), float64(million.EncodedSize())/1000000)

	runs := New()
	for i := 0; i < 1000; i++ {
		base := rng.Uint32() & 0xfffff000
		for d := uint32(0); d < 100; d++ {
			runs.Add(base + d)
		}
	}
	t.Logf("1000 段长度 100 的连续区间: %d 字节，共 %d 元素 (%.2f B/元素)",
		runs.EncodedSize(), runs.Len(), float64(runs.EncodedSize())/float64(runs.Len()))

	dense := New()
	seen := make(map[uint32]bool)
	for len(seen) < 50000 {
		v := uint32(rng.Intn(65536))
		if !seen[v] {
			seen[v] = true
			dense.Add(v)
		}
	}
	t.Logf("单块内 50000 个随机值（76%% 密度）: %d 字节 (%.2f B/元素)",
		dense.EncodedSize(), float64(dense.EncodedSize())/50000)
}
