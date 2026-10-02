package tagunion

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"reflect"
	"testing"
)

// splitFrames 是独立解析器：不依赖 Registry 与 schema，
// 仅按帧格式切分并校验 CRC，用于交叉验证主解析器。
func splitFrames(t *testing.T, data []byte) [][]byte {
	t.Helper()
	var frames [][]byte
	pos := 0
	for pos < len(data) {
		start := pos
		readU := func() uint64 {
			v, n := binary.Uvarint(data[pos:])
			if n <= 0 {
				t.Fatalf("independent parser: bad varint at %d", pos)
			}
			pos += n
			return v
		}
		readU() // tag
		readU() // version
		length := readU()
		end := pos + int(length) + 4
		if end > len(data) {
			t.Fatalf("independent parser: truncated frame at %d", start)
		}
		stored := binary.LittleEndian.Uint32(data[end-4 : end])
		if crc32.ChecksumIEEE(data[start:end-4]) != stored {
			t.Fatalf("independent parser: crc mismatch at %d", start)
		}
		frames = append(frames, data[start:end])
		pos = end
	}
	return frames
}

// TestRoundTripMatrix 构造多标签 × 多版本的编码往返矩阵：
// 每个版本编码 -> 解码（显式迁移到最新）-> 重编码 -> 再解码，字段必须一致。
func TestRoundTripMatrix(t *testing.T) {
	r := testRegistry(t)
	type encodeSpec struct {
		tag     uint64
		version uint64
		fields  map[string]any
	}
	matrix := []encodeSpec{
		{tagTransfer, 1, map[string]any{"amount": uint64(1)}},
		{tagTransfer, 1, map[string]any{"amount": uint64(1<<63 + 7)}},
		{tagTransfer, 2, map[string]any{"amount": uint64(0), "currency": ""}},
		{tagTransfer, 2, map[string]any{"amount": uint64(99), "currency": "EUR"}},
		{tagPing, 1, map[string]any{}},
		{tagProfile, 1, map[string]any{"name": "", "age": uint64(0), "score": int64(0), "admin": false, "avatar": []byte{}}},
		{tagProfile, 1, map[string]any{"name": "bob", "age": uint64(41), "score": int64(-1 << 40), "admin": true, "avatar": []byte{1, 2, 3}}},
	}
	for _, spec := range matrix {
		name := "tag" + itoa(spec.tag) + "-v" + itoa(spec.version)
		t.Run(name, func(t *testing.T) {
			f1, err := r.Encode(spec.tag, spec.version, spec.fields)
			if err != nil {
				t.Fatal(err)
			}
			v1, n1, err := r.Decode(f1)
			if err != nil {
				t.Fatal(err)
			}
			if n1 != len(f1) {
				t.Fatalf("consumed %d of %d bytes", n1, len(f1))
			}
			f2, err := r.Marshal(v1)
			if err != nil {
				t.Fatal(err)
			}
			v2, _, err := r.Decode(f2)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(v1, v2) && !reflect.DeepEqual(v1.Fields, v2.Fields) {
				t.Fatalf("round trip mismatch:\n first  %+v\n second %+v", v1, v2)
			}
			// 独立解析器交叉验证重编码输出是单个合法帧。
			frames := splitFrames(t, f2)
			if len(frames) != 1 || !bytes.Equal(frames[0], f2) {
				t.Fatalf("independent parser disagrees on re-encoded frame")
			}
		})
	}
}

// TestIndependentParserUnknownRewrite 独立解析器验证：
// 未知标签的载荷被改写为“看似已知字段”的内容后，透传仍逐字节保留。
func TestIndependentParserUnknownRewrite(t *testing.T) {
	r := testRegistry(t)
	// 载荷恰好是合法 transfer v2 编码（amount=7, currency="USD"）。
	knownPayload, err := r.Encode(tagTransfer, 2, map[string]any{"amount": uint64(7), "currency": "USD"})
	if err != nil {
		t.Fatal(err)
	}
	// 取已知帧的载荷部分，作为未知标签的载荷（“改写已知字段”）。
	_, n := binary.Uvarint(knownPayload) // tag
	pos := n
	_, n = binary.Uvarint(knownPayload[pos:]) // version
	pos += n
	l, n := binary.Uvarint(knownPayload[pos:]) // length
	pos += n
	payload := knownPayload[pos : pos+int(l)]

	fu, err := EncodeUnknown(555, 3, payload)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := EncodeBatch(fu)
	if err != nil {
		t.Fatal(err)
	}

	// 主解析器：必须识别为未知并逐字节透传。
	vals, err := r.DecodeBatch(batch)
	if err != nil {
		t.Fatal(err)
	}
	if vals[0].Kind != KindUnknown {
		t.Fatalf("must stay unknown, got %+v", vals[0])
	}
	re, err := r.Marshal(vals[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(re, fu) {
		t.Fatalf("unknown frame not byte-preserved:\n got %x\nwant %x", re, fu)
	}

	// 独立解析器：切分批量缓冲区，逐字节比对未知帧。
	frames := splitFrames(t, batch[1:]) // 跳过批量计数
	if len(frames) != 1 || !bytes.Equal(frames[0], fu) {
		t.Fatalf("independent parser: unknown frame altered")
	}
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
