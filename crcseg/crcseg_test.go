package crcseg

import (
	"bytes"
	"errors"
	"hash/crc32"
	"io"
	"math"
	"math/rand"
	"testing"
)

func wantErrIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("err = %v, want errors.Is(., %v)", err, target)
	}
}

// stdChecksum 用标准库交叉验证。
func stdChecksum(p Params, data []byte) uint32 {
	var tab *crc32.Table
	switch p.Polynomial {
	case IEEE.Polynomial:
		tab = crc32.MakeTable(crc32.IEEE)
	case Castagnoli.Polynomial:
		tab = crc32.MakeTable(crc32.Castagnoli)
	default:
		tab = crc32.MakeTable(p.Polynomial)
	}
	// 标准库只支持 init=xorout=0xFFFFFFFF，这里直接比较 IEEE/Castagnoli。
	return crc32.Checksum(data, tab)
}

// 1. 自定义多项式/初末异或：表与一次性校验值与标准库一致。
func TestParamsMatchStdlib(t *testing.T) {
	for name, p := range map[string]Params{"ieee": IEEE, "crc32c": Castagnoli} {
		data := []byte("The quick brown fox jumps over the lazy dog")
		got, err := p.Checksum(data)
		if err != nil {
			t.Fatal(err)
		}
		if want := stdChecksum(p, data); got != want {
			t.Fatalf("%s: got %08x want %08x", name, got, want)
		}
		if _, err := (Params{Polynomial: 0}).Checksum(data); !errors.Is(err, ErrInvalidParams) {
			t.Fatalf("%s: zero polynomial", name)
		}
	}
}

// 2. 任意分块写入 == 一次计算；且与标准库一致（覆盖空块/单字节/大尾块）。
func TestChunkedEqualsWhole(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 200; iter++ {
		n := rng.Intn(4096)
		data := make([]byte, n)
		rng.Read(data)

		whole, err := IEEE.Checksum(data)
		if err != nil {
			t.Fatal(err)
		}
		if std := stdChecksum(IEEE, data); whole != std {
			t.Fatalf("whole vs stdlib: %08x %08x", whole, std)
		}

		h, _ := NewHasher(IEEE)
		for pos := 0; pos < n; {
			step := rng.Intn(70) + 1
			if pos+step > n {
				step = n - pos
			}
			if _, err := h.Write(data[pos : pos+step]); err != nil {
				t.Fatal(err)
			}
			pos += step
		}
		if got := h.Sum32(); got != whole {
			t.Fatalf("iter %d n %d: chunked %08x != whole %08x", iter, n, got, whole)
		}
		if seg := h.Segment(); seg.Length != int64(n) || seg.CRC != whole {
			t.Fatalf("segment mismatch: %+v", seg)
		}
	}
}

// 3. 任意切分合成 == 一次计算（随机切点，多段，Castagnoli）。
func TestCombineArbitrarySplits(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 300; iter++ {
		n := rng.Intn(5000) + 1
		data := make([]byte, n)
		rng.Read(data)
		whole, err := Castagnoli.Checksum(data)
		if err != nil {
			t.Fatal(err)
		}

		parts := rng.Intn(6) + 2
		bounds := []int{0}
		for j := 1; j < parts; j++ {
			bounds = append(bounds, rng.Intn(n+1))
		}
		bounds = append(bounds, n)
		sortInts(bounds)

		acc, _ := EmptySegment(Castagnoli)
		for j := 0; j < parts; j++ {
			lo, hi := bounds[j], bounds[j+1]
			s, err := SegmentFromData(Castagnoli, data[lo:hi])
			if err != nil {
				t.Fatal(err)
			}
			acc, err = Combine(Castagnoli, acc, s)
			if err != nil {
				t.Fatal(err)
			}
		}
		if acc.Length != int64(n) || acc.CRC != whole {
			t.Fatalf("iter %d: combined (len=%d crc=%08x) != whole (len=%d crc=%08x)",
				iter, acc.Length, acc.CRC, n, whole)
		}
	}
}

func sortInts(xs []int) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

// 4. 空段是恒等元。
func TestEmptySegmentIdentity(t *testing.T) {
	data := []byte("identity check payload")
	s, err := SegmentFromData(IEEE, data)
	if err != nil {
		t.Fatal(err)
	}
	e, err := EmptySegment(IEEE)
	if err != nil {
		t.Fatal(err)
	}
	if e.Length != 0 || e.CRC != IEEE.Empty() {
		t.Fatalf("empty = %+v", e)
	}
	if got, err := Combine(IEEE, e, s); err != nil || got != s {
		t.Fatalf("empty||A: got %+v err %v", got, err)
	}
	if got, err := Combine(IEEE, s, e); err != nil || got != s {
		t.Fatalf("A||empty: got %+v err %v", got, err)
	}
	if got, err := Combine(IEEE, e, e); err != nil || got != e {
		t.Fatalf("empty||empty: got %+v err %v", got, err)
	}
	if _, err := Combine(IEEE, Segment{Length: 0, CRC: 123}, e); !errors.Is(err, ErrInvalidSegment) {
		t.Fatalf("zero length with nonzero crc should be invalid, got %v", err)
	}
}

// 5. 长度溢出报错。
func TestLengthOverflow(t *testing.T) {
	big := Segment{Length: math.MaxInt64, CRC: 0xDEADBEEF}
	one := Segment{Length: 1, CRC: 0xCAFEBABE}
	if _, err := Combine(IEEE, big, one); !errors.Is(err, ErrLengthOverflow) {
		t.Fatalf("Combine overflow: %v", err)
	}
	c, err := NewChain(IEEE)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Append(big); err != nil {
		t.Fatal(err)
	}
	if err := c.Append(one); !errors.Is(err, ErrLengthOverflow) {
		t.Fatalf("Chain overflow: %v", err)
	}
	if got := c.Len(); got != 1 {
		t.Fatalf("chain mutated after failed append: %d", got)
	}
}

// 6. 相同摘要、不同长度不能混用（替换必须等长）。
func TestReplaceRequiresEqualLength(t *testing.T) {
	c, _ := NewChain(IEEE)
	a, _ := SegmentFromData(IEEE, []byte("AAAA"))
	b, _ := SegmentFromData(IEEE, []byte("BBBBBBBB"))
	if err := c.Append(a); err != nil {
		t.Fatal(err)
	}
	if err := c.Append(b); err != nil {
		t.Fatal(err)
	}
	// 即使构造出相同摘要，长度不同也必须拒绝。
	sameCRCdifferentLen := Segment{Length: 9, CRC: a.CRC}
	err := c.ReplaceAt(0, sameCRCdifferentLen)
	wantErrIs(t, err, ErrLengthMismatch)

	// 越界。
	wantErrIs(t, c.ReplaceAt(5, a), ErrIndexOutOfRange)
	wantErrIs(t, c.ReplaceAt(-1, a), ErrIndexOutOfRange)

	// 等长替换成功。
	repl, _ := SegmentFromData(IEEE, []byte("CCCC"))
	if err := c.ReplaceAt(0, repl); err != nil {
		t.Fatal(err)
	}
}

// 7. 子段替换后的整体验证：不重新扫描未变数据。
func TestReplaceAndVerifyWithoutRescan(t *testing.T) {
	parts := [][]byte{
		[]byte("header-"),
		bytes.Repeat([]byte("x"), 10000),
		[]byte("-tail"),
	}
	full := bytes.Join(parts, nil)
	whole, _ := IEEE.Checksum(full)

	c, _ := NewChain(IEEE)
	var segs []Segment
	for _, part := range parts {
		s, _ := SegmentFromData(IEEE, part)
		segs = append(segs, s)
		if err := c.Append(s); err != nil {
			t.Fatal(err)
		}
	}
	if !c.Verify(Segment{Length: int64(len(full)), CRC: whole}) {
		t.Fatal("original chain does not verify")
	}

	// 替换中间段为等长的新内容，整体摘要必须等于“新全文”一次计算的结果。
	newMid := bytes.Repeat([]byte("y"), 10000)
	parts[1] = newMid
	full2 := bytes.Join(parts, nil)
	whole2, _ := IEEE.Checksum(full2)

	newSeg, _ := SegmentFromData(IEEE, newMid)
	if err := c.ReplaceAt(1, newSeg); err != nil {
		t.Fatal(err)
	}
	// 只用到了新段 10000 字节 + 纯摘要合成；未触碰其它段字节。
	if !c.Verify(Segment{Length: int64(len(full2)), CRC: whole2}) {
		t.Fatalf("replaced chain verifies: total=%+v want crc %08x", c.Segment(), whole2)
	}
	if c.Verify(Segment{Length: int64(len(full)), CRC: whole}) {
		t.Fatal("old checksum must no longer verify")
	}
}

// 8. 帧解析区分缺数据与坏数据；缺失/坏帧不消耗额度。
func TestParseFrameKinds(t *testing.T) {
	b, err := ParseFrame(append([]byte{TagOK}, []byte("hello")...))
	if err != nil || b.Kind != BlockOK || string(b.Data) != "hello" {
		t.Fatalf("ok frame: %+v %v", b, err)
	}
	b, err = ParseFrame([]byte{TagOK})
	if err != nil || b.Kind != BlockOK || len(b.Data) != 0 {
		t.Fatalf("empty ok frame: %+v %v", b, err)
	}
	b, err = ParseFrame([]byte{TagMissing})
	wantErrIs(t, err, ErrMissingData)
	if b.Kind != BlockMissing {
		t.Fatal("missing kind")
	}
	b, err = ParseFrame([]byte{TagBad})
	wantErrIs(t, err, ErrBadData)
	if b.Kind != BlockBad {
		t.Fatal("bad kind")
	}
	wantErrIs(t, ParseFrameSilent([]byte{TagMissing, 'x'}), ErrBadData)
	wantErrIs(t, ParseFrameSilent([]byte{TagBad, 'x'}), ErrBadData)
	wantErrIs(t, ParseFrameSilent([]byte{0x7F}), ErrBadData)
	wantErrIs(t, ParseFrameSilent(nil), ErrBadData)
}

func ParseFrameSilent(f []byte) error {
	_, err := ParseFrame(f)
	return err
}

// 9. 长度额度：分配前检查；坏/缺帧、超额帧都不改变已收状态。
func TestCollectorQuota(t *testing.T) {
	c, err := NewCollector(IEEE, 10)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := c.Write([]byte("01234")); err != nil || n != 5 {
		t.Fatalf("first write: n=%d err=%v", n, err)
	}
	// 恰好等于额度可以。
	if _, err := c.Write([]byte("56789")); err != nil {
		t.Fatalf("at quota: %v", err)
	}
	// 超出：在吸收前失败。
	if _, err := c.Write([]byte("!")); !errors.Is(err, ErrQuota) {
		t.Fatalf("over quota: %v", err)
	}
	if c.Len() != 10 {
		t.Fatalf("state changed by rejected write: %d", c.Len())
	}
	// 坏帧与缺帧不消耗额度、不改变摘要。
	before := c.Segment()
	if err := c.IngestFrame([]byte{TagMissing}); !errors.Is(err, ErrMissingData) {
		t.Fatalf("missing frame: %v", err)
	}
	if err := c.IngestFrame([]byte{TagBad}); !errors.Is(err, ErrBadData) {
		t.Fatalf("bad frame: %v", err)
	}
	if c.Segment() != before {
		t.Fatal("missing/bad frame changed collector state")
	}
	// 一帧合法数据会先做额度检查：超额的完整帧也不被吸收。
	if err := c.IngestFrame(append([]byte{TagOK}, 'x')); !errors.Is(err, ErrQuota) {
		t.Fatalf("ok frame over quota: %v", err)
	}
	if c.Segment() != before {
		t.Fatal("over-quota ok frame changed collector state")
	}
}

// 10. 收帧端到端：有序流中遇到缺帧/坏帧先跳过，按序重传补齐后
// 最终摘要等于一次计算。
func TestCollectorFramesEndToEnd(t *testing.T) {
	whole := []byte("frame-by-frame ingestion payload 0123456789")
	chunks := [][]byte{whole[0:7], whole[7:19], whole[19:25], whole[25:33], whole[33:]}
	want, _ := IEEE.Checksum(whole)

	// 每个位置先用问题帧（缺/坏）试探一次，再用正确帧补齐：
	// 收集器严格按喂入顺序累积 CRC。
	c, _ := NewCollector(IEEE, int64(len(whole)))
	for i, chunk := range chunks {
		// 先发一个会失败的帧（奇数块缺失、偶数块损坏）。
		var badFrame []byte
		if i%2 == 0 {
			badFrame = []byte{TagBad}
		} else {
			badFrame = []byte{TagMissing}
		}
		err := c.IngestFrame(badFrame)
		if i%2 == 0 {
			wantErrIs(t, err, ErrBadData)
		} else {
			wantErrIs(t, err, ErrMissingData)
		}
		// 问题帧不得消耗额度。
		if c.Len() != int64(chunkOffset(chunks, i)) {
			t.Fatalf("bad frame at %d consumed quota: %d", i, c.Len())
		}
		if err := c.IngestFrame(append([]byte{TagOK}, chunk...)); err != nil {
			t.Fatal(err)
		}
	}
	got := c.Segment()
	if got.Length != int64(len(whole)) || got.CRC != want {
		t.Fatalf("seg=%+v want crc=%08x", got, want)
	}
	c.Reset()
	if c.Len() != 0 || c.Segment().CRC != IEEE.Empty() {
		t.Fatal("reset failed")
	}
}

func chunkOffset(chunks [][]byte, i int) int {
	n := 0
	for _, c := range chunks[:i] {
		n += len(c)
	}
	return n
}

// 11. 非标准多项式/初末异或：合成结果与直接计算逐长度一致。
func TestCustomParamsCombine(t *testing.T) {
	p := Params{Polynomial: 0x12345678, Init: 0x00000000, FinalXOR: 0xFFFFFFFF}
	data := make([]byte, 200)
	rng := rand.New(rand.NewSource(99))
	rng.Read(data)
	whole, err := p.Checksum(data)
	if err != nil {
		t.Fatal(err)
	}
	for cut := 0; cut <= len(data); cut++ {
		a, _ := SegmentFromData(p, data[:cut])
		b, _ := SegmentFromData(p, data[cut:])
		ab, err := Combine(p, a, b)
		if err != nil {
			t.Fatal(err)
		}
		if ab.Length != int64(len(data)) || ab.CRC != whole {
			t.Fatalf("cut %d: combined=%08x whole=%08x", cut, ab.CRC, whole)
		}
	}
	// 三段链 + 中段等长替换。
	c, _ := NewChain(p)
	s1, _ := SegmentFromData(p, data[:50])
	s2, _ := SegmentFromData(p, data[50:150])
	s3, _ := SegmentFromData(p, data[150:])
	for _, s := range []Segment{s1, s2, s3} {
		if err := c.Append(s); err != nil {
			t.Fatal(err)
		}
	}
	if !c.Verify(Segment{Length: 200, CRC: whole}) {
		t.Fatal("custom-param chain verify failed")
	}
	rng.Read(data[50:150])
	whole2, _ := p.Checksum(data)
	s2b, _ := SegmentFromData(p, data[50:150])
	if err := c.ReplaceAt(1, s2b); err != nil {
		t.Fatal(err)
	}
	if !c.Verify(Segment{Length: 200, CRC: whole2}) {
		t.Fatal("custom-param replace verify failed")
	}
}

// 12. Hasher 行为符合标准库 hash.Hash32 语义（Sum32/Sum/Reset/Size）。
func TestHasherStdlibSemantics(t *testing.T) {
	data := bytes.Repeat([]byte("abc123"), 100)
	h, err := NewHasher(IEEE)
	if err != nil {
		t.Fatal(err)
	}
	if h.Size() != 4 || h.BlockSize() != 1 {
		t.Fatal("size/blocksize")
	}
	var _ hash32 = h
	if n, err := h.Write(data); err != nil || n != len(data) {
		t.Fatalf("write n=%d err=%v", n, err)
	}
	want, _ := IEEE.Checksum(data)
	if h.Sum32() != want {
		t.Fatalf("sum32 %08x want %08x", h.Sum32(), want)
	}
	sum := h.Sum(nil)
	if len(sum) != 4 {
		t.Fatalf("sum len %d", len(sum))
	}
	if be := uint32(sum[0])<<24 | uint32(sum[1])<<16 | uint32(sum[2])<<8 | uint32(sum[3]); be != want {
		t.Fatalf("sum bytes %08x want %08x", be, want)
	}
	prefixed := h.Sum([]byte{0xAA})
	if len(prefixed) != 5 || prefixed[0] != 0xAA {
		t.Fatal("Sum prefix not preserved")
	}
	h.Reset()
	if h.Sum32() != IEEE.Empty() || h.Segment().Length != 0 {
		t.Fatal("reset did not restore initial state")
	}
}

type hash32 interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
	Reset()
	Size() int
	BlockSize() int
	Sum32() uint32
}

// countingReader 记录读取过的总字节数。
type countingReader struct {
	data []byte
	read int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.data)
	c.data = c.data[n:]
	c.read += int64(n)
	return n, nil
}

// 13. 子段替换只扫描新段数据，不触碰未变段。
func TestReplaceDoesNotRescanUnchanged(t *testing.T) {
	orig := [][]byte{
		[]byte("head"),
		bytes.Repeat([]byte("Z"), 4096),
		[]byte("tail"),
	}
	c, _ := NewChain(IEEE)
	var totalInitialScanned int64
	for _, part := range orig {
		r := &countingReader{data: part}
		h, _ := NewHasher(IEEE)
		for {
			buf := make([]byte, 333)
			n, err := r.Read(buf)
			if n > 0 {
				if _, werr := h.Write(buf[:n]); werr != nil {
					t.Fatal(werr)
				}
			}
			if err != nil {
				break
			}
		}
		totalInitialScanned += r.read
		if err := c.Append(h.Segment()); err != nil {
			t.Fatal(err)
		}
	}

	// 替换中段：只读取新中段的 4096 字节。
	newMid := bytes.Repeat([]byte("Q"), 4096)
	r := &countingReader{data: newMid}
	h, _ := NewHasher(IEEE)
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := h.Write(buf[:n]); werr != nil {
				t.Fatal(werr)
			}
		}
		if err != nil {
			break
		}
	}
	if r.read != int64(len(newMid)) {
		t.Fatalf("replacement scanned %d bytes, want %d", r.read, len(newMid))
	}
	if err := c.ReplaceAt(1, h.Segment()); err != nil {
		t.Fatal(err)
	}

	full := bytes.Join([][]byte{orig[0], newMid, orig[2]}, nil)
	want, _ := IEEE.Checksum(full)
	if !c.Verify(Segment{Length: int64(len(full)), CRC: want}) {
		t.Fatal("verify failed after replacement")
	}
	// 初始扫描总量 == 全文长度；替换阶段增量扫描仅 4096，且没有任何
	// 机制去重新读取 head/tail（Chain 只持有 Segment 元数据）。
	if totalInitialScanned != int64(len(full)) {
		t.Fatalf("initial scan %d", totalInitialScanned)
	}
}

// 14. 边界与错误分支补充。
func TestEdgeBranches(t *testing.T) {
	if _, err := NewHasher(Params{Polynomial: 0}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("NewHasher bad params: %v", err)
	}
	h, _ := NewHasher(IEEE)
	if h.Params() != IEEE {
		t.Fatal("Hasher.Params")
	}
	if h.BlockSize() != 1 {
		t.Fatal("block size")
	}

	if _, err := EmptySegment(Params{Polynomial: 0}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("EmptySegment bad params: %v", err)
	}
	if _, err := NewChain(Params{Polynomial: 0}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("NewChain bad params: %v", err)
	}
	if _, err := SegmentFromData(Params{Polynomial: 0}, nil); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("SegmentFromData bad params: %v", err)
	}
	if _, err := IEEE.Checksum(nil); err != nil {
		t.Fatalf("checksum nil: %v", err)
	}

	// 负长度段非法。
	if _, err := Combine(IEEE, Segment{Length: -1}, Segment{}); !errors.Is(err, ErrInvalidSegment) {
		t.Fatalf("negative length: %v", err)
	}

	// 收集器：负额度、负预留、未知 block kind、直接 IngestBlock OK。
	if _, err := NewCollector(IEEE, -2); err == nil {
		t.Fatal("negative quota should fail")
	}
	col, _ := NewCollector(IEEE, -1) // 不限额度
	if err := col.Reserve(-1); err == nil {
		t.Fatal("negative reservation should fail")
	}
	if err := col.IngestBlock(Block{Kind: BlockOK, Data: []byte("abc")}); err != nil {
		t.Fatalf("ingest ok block: %v", err)
	}
	if err := col.IngestBlock(Block{Kind: BlockMissing}); !errors.Is(err, ErrMissingData) {
		t.Fatalf("ingest missing block: %v", err)
	}
	if err := col.IngestBlock(Block{Kind: BlockBad}); !errors.Is(err, ErrBadData) {
		t.Fatalf("ingest bad block: %v", err)
	}
	if err := col.IngestBlock(Block{Kind: BlockKind(99)}); !errors.Is(err, ErrBadData) {
		t.Fatalf("ingest unknown kind: %v", err)
	}
	if col.Len() != 3 {
		t.Fatalf("only ok block counts: %d", col.Len())
	}

	// Chain 访问器与非法 Append。
	c, _ := NewChain(IEEE)
	if c.Params() != IEEE {
		t.Fatal("chain params")
	}
	if c.Segment() != (Segment{Length: 0, CRC: 0}) {
		t.Fatalf("empty chain total %+v", c.Segment())
	}
	if _, err := c.SegmentAt(0); !errors.Is(err, ErrIndexOutOfRange) {
		t.Fatalf("segment at on empty: %v", err)
	}
	if err := c.Append(Segment{Length: 0, CRC: 9}); !errors.Is(err, ErrInvalidSegment) {
		t.Fatalf("append bogus empty: %v", err)
	}
	s, _ := SegmentFromData(IEEE, []byte("xy"))
	if err := c.Append(s); err != nil {
		t.Fatal(err)
	}
	got, err := c.SegmentAt(0)
	if err != nil || got != s {
		t.Fatalf("segment at: %+v %v", got, err)
	}
	if err := c.Append(s); err != nil {
		t.Fatal(err)
	}
	// ReplaceAt 传非法段。
	if err := c.ReplaceAt(0, Segment{Length: 1, CRC: 0}); err == nil {
		t.Fatal("invalid replacement accepted")
	}
}
