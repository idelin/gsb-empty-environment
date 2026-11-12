package netsec

import (
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestRecordRoundTrip(t *testing.T) {
	rec := Record{Fields: []Field{
		{ID: 1, Type: FieldUint64, Data: uint64Bytes(0xdeadbeef)},
		{ID: 2, Type: FieldBytes, Data: []byte{0, 1, 2, 3, 255}},
		{ID: 0xffffff, Type: FieldString, Data: []byte("监控事件")},
	}}

	payload, err := rec.Encoded()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	parsed, err := ParseRecord(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(parsed.Fields) != 3 {
		t.Fatalf("field count = %d", len(parsed.Fields))
	}
	for i, f := range parsed.Fields {
		if f.ID != rec.Fields[i].ID || f.Type != rec.Fields[i].Type ||
			string(f.Data) != string(rec.Fields[i].Data) {
			t.Fatalf("field %d mismatch: %+v vs %+v", i, f, rec.Fields[i])
		}
	}
}

func TestParseRecordRejectsFramingErrors(t *testing.T) {
	good, err := (Record{Fields: []Field{{ID: 1, Type: FieldBytes, Data: []byte("abc")}}}).Encoded()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	cases := map[string][]byte{
		"truncated header":          good[:len(good)-2],
		"partial count":             {0x01, 0x00},
		"trailing bytes":            append(append([]byte{}, good...), 0x99),
		"zero field id":             buildFrame(t, 0, FieldBytes, []byte("x")),
		"unknown type":              buildFrame(t, 5, 99, []byte("x")),
		"uint64 wrong width":        buildFrame(t, 5, FieldUint64, []byte{1, 2, 3}),
		"declared length overflows": buildOverflowFrame(t),
	}

	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRecord(payload)
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("expected ErrMalformed, got %v", err)
			}
		})
	}
}

func buildFrame(t *testing.T, id uint32, typ uint8, data []byte) []byte {
	t.Helper()
	buf := make([]byte, 4+fieldHeaderSize+len(data))
	binary.LittleEndian.PutUint32(buf[:4], 1)
	buf[4] = byte(id)
	buf[5] = byte(id >> 8)
	buf[6] = byte(id >> 16)
	buf[7] = typ
	binary.LittleEndian.PutUint32(buf[8:12], uint32(len(data)))
	copy(buf[12:], data)
	return buf
}

// buildOverflowFrame 声明一个远超实际载荷的字段长度。
func buildOverflowFrame(t *testing.T) []byte {
	t.Helper()
	buf := make([]byte, 4+fieldHeaderSize)
	binary.LittleEndian.PutUint32(buf[:4], 1)
	buf[4], buf[5], buf[6] = 1, 0, 0
	buf[7] = FieldBytes
	binary.LittleEndian.PutUint32(buf[8:12], 0x7fffffff)
	return buf
}

// scriptedPage 按 ReadAt 调用序号返回预先编排的字节，用于确定性地制造
// “窗口前半读到版本 A、后半读到版本 B”的撕裂场景。
type scriptedPage struct {
	versions [][]byte // 每个元素是一整页
	reads    int
}

func (p *scriptedPage) ReadAt(b []byte, off int64) (int, error) {
	v := p.versions[(p.reads)%len(p.versions)]
	p.reads++
	if off < 0 || uint64(off)+uint64(len(b)) > uint64(len(v)) {
		return 0, io.EOF
	}
	return copy(b, v[off:]), nil
}

func (p *scriptedPage) WriteAt([]byte, int64) (int, error) { return 0, io.ErrClosedPipe }
func (p *scriptedPage) Size() int64                        { return int64(len(p.versions[0])) }

func buildPageBytes(t *testing.T, gen, seq uint64, rec Record) []byte {
	t.Helper()
	const capacity = 256
	page := make([]byte, PageSize(capacity))
	payload, err := rec.Encoded()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	le.PutUint32(page[offMagic:], magic)
	le.PutUint32(page[offVersion:], version)
	le.PutUint64(page[offGen:], gen)
	le.PutUint64(page[offSeq:], seq)
	le.PutUint32(page[offCapacity:], capacity)
	le.PutUint32(page[offLength:], uint32(len(payload)))
	sum := digestPayload(payload)
	copy(page[offDigest:offDigest+digestSize], sum[:])
	copy(page[headerSize:], payload)
	return page
}

// TestTornWindowIsRejected 窗口两端版本不同（撕裂）必须重试，
// 只有完整落在同一版本上的两次采样才会被接受。
func TestTornWindowIsRejected(t *testing.T) {
	recA := Record{Fields: []Field{{ID: 1, Type: FieldString, Data: []byte("AAAA")}}}
	recB := Record{Fields: []Field{{ID: 1, Type: FieldString, Data: []byte("BBBB")}}}

	pageA := buildPageBytes(t, 1, 0, recA)

	// 一次 readSnapshot 恰好 3 次 ReadAt：版本、头部、载荷、再版本。
	// 连续编排同一页，保证两次完整快照落在同一稳定版本上。
	sp := &scriptedPage{versions: [][]byte{pageA, pageA, pageA, pageA, pageA, pageA}}

	reader := NewReader(sp, ReaderConfig{MaxAttempts: 8, MaxPayload: 256})
	rec, err := reader.Read(t.Context())
	if err != nil {
		t.Fatalf("expected stable read of recA, got %v", err)
	}
	if got := rec.Fields[0].Data; string(got) != "AAAA" {
		t.Fatalf("expected recA, got %q", got)
	}

	// 撕裂窗口：前半窗口读到 A，后半窗口读到 B（gen/seq 不同），
	// 读取器必须不断重试；预算耗尽返回不确定，绝不返回拼接记录。
	pageB := buildPageBytes(t, 2, 2, recB)
	spTorn := &scriptedPage{versions: [][]byte{pageA, pageA, pageB, pageB}}
	readerTorn := NewReader(spTorn, ReaderConfig{MaxAttempts: 6, MaxPayload: 256})
	_, err = readerTorn.Read(t.Context())
	if !errors.Is(err, ErrUncertain) {
		t.Fatalf("expected ErrUncertain on torn window, got %v", err)
	}

	// 现在制造撕裂：奇数序号页夹在偶数页之间，读取必须返回不确定。
	pageOdd := buildPageBytes(t, 2, 1, recB)
	spOdd := &scriptedPage{versions: [][]byte{pageOdd}}
	readerOdd := NewReader(spOdd, ReaderConfig{MaxAttempts: 4, MaxPayload: 256})
	_, err = readerOdd.Read(t.Context())
	if !errors.Is(err, ErrUncertain) {
		t.Fatalf("expected ErrUncertain on odd seq, got %v", err)
	}
}

// TestMixedLengthAndDigestRejected 长度来自记录 A、载荷/摘要来自记录 B 的
// 拼装页永远无法通过校验：要么结构越界，要么摘要不符，绝不会被当作新记录接受。
func TestMixedLengthAndDigestRejected(t *testing.T) {
	recA := Record{Fields: []Field{{ID: 1, Type: FieldBytes, Data: []byte("short")}}}
	recB := Record{Fields: []Field{{ID: 1, Type: FieldBytes, Data: []byte("a-much-longer-payload-here")}}}

	pageA := buildPageBytes(t, 0, 0, recA)
	pageB := buildPageBytes(t, 0, 0, recB)

	// 用 B 的载荷、A 的长度与摘要拼出一个“弗兰肯斯坦”页并保持稳定。
	mixed := append([]byte{}, pageA...)
	copy(mixed[headerSize:], pageB[headerSize:])

	sp := &scriptedPage{versions: [][]byte{mixed}}
	reader := NewReader(sp, ReaderConfig{MaxAttempts: 4, MaxPayload: 256})
	_, err := reader.Read(t.Context())
	if err == nil {
		t.Fatalf("mixed snapshot must not be accepted")
	}
	if errors.Is(err, ErrUncertain) {
		t.Fatalf("stable corruption must be a deterministic error, got uncertain")
	}
	if !errors.Is(err, ErrDigestMismatch) && !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected digest/malformed error, got %v", err)
	}
}
