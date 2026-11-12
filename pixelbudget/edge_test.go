package pixelbudget

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"
)

// TestFramePixelOverflow 用 PNG 32 位宽高制造单帧乘法溢出：
// 乘积超过 math.MaxInt64，必须在纯整数运算阶段被拒绝。
func TestFramePixelOverflow(t *testing.T) {
	// 4294967295 * 4294967295 远大于 math.MaxInt64。
	data := minimalPNGWithIHDR(math.MaxUint32, math.MaxUint32)
	h, _, err := Inspect(bytes.NewReader(data))
	if !errors.Is(err, ErrSingleFrame) {
		t.Fatalf("err = %v, want ErrSingleFrame", err)
	}
	if h.FramePixels != 0 {
		t.Fatalf("overflow must not report a product, got %d", h.FramePixels)
	}
}

// TestTotalPixelOverflow 用 APNG 帧数放大制造总像素乘法溢出，
// 单帧乘积合法但乘以帧数后超过 math.MaxInt64。
func TestTotalPixelOverflow(t *testing.T) {
	// 单帧 65535*65535 = 4294836225；帧数取 2^32-1，总乘积必然溢出。
	data := minimalPNGWithIHDR(65535, 65535)
	withACTL := insertACTL(data, math.MaxUint32)

	h, _, err := Inspect(bytes.NewReader(withACTL))
	if !errors.Is(err, ErrTotalPixels) {
		t.Fatalf("err = %v, want ErrTotalPixels", err)
	}
	if h.TotalPixels != 0 {
		t.Fatalf("overflow must not report a total, got %d", h.TotalPixels)
	}
}

// insertACTL 在给定 PNG 的 IHDR 之后插入 acTL chunk。
func insertACTL(pngData []byte, frames uint32) []byte {
	// 8 字节签名 + IHDR(长度4+类型4+13+CRC4=25)，其后即插入点。
	const afterIHDR = 8 + 25
	out := make([]byte, 0, len(pngData)+20)
	out = append(out, pngData[:afterIHDR]...)
	out = append(out, buildChunk("acTL", actlPayload(frames))...)
	out = append(out, pngData[afterIHDR:]...)
	return out
}

// TestOversizedChunkLength 验证荒谬的 chunk 长度被合理性检查拦下，
// 不会触发大跨度跳过或分配。
func TestOversizedChunkLength(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(pngSignature)
	buf.Write(buildChunk("IHDR", ihdrPayload(4, 3)))
	head := make([]byte, 8)
	binary.BigEndian.PutUint32(head[0:4], maxChunkPayload+1)
	copy(head[4:8], "tEXt")
	buf.Write(head)

	if _, _, err := Inspect(&buf); !errors.Is(err, ErrHeader) {
		t.Fatalf("err = %v, want ErrHeader", err)
	}
}

// TestJPEGBadMarkers 覆盖 JPEG 标记解析的异常分支。
func TestJPEGBadMarkers(t *testing.T) {
	t.Run("missing marker prefix", func(t *testing.T) {
		// SOI 之后直接来普通字节而不是 0xFF。
		data := []byte{0xFF, 0xD8, 0x10, 0x20}
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})

	t.Run("marker byte zero", func(t *testing.T) {
		// 0xFF 0x00 是转义字节，不是合法标记。
		data := []byte{0xFF, 0xD8, 0xFF, 0x00}
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})

	t.Run("eoi before sof", func(t *testing.T) {
		data := []byte{0xFF, 0xD8, 0xFF, 0xD9}
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})

	t.Run("bad segment length", func(t *testing.T) {
		// APP0 段声明长度 1（小于最小值 2）。
		data := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x01}
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})

	t.Run("truncated sof payload", func(t *testing.T) {
		// SOI 后直接给 SOF0 标记，声明段长 9 字节，但只提供 4 字节载荷。
		data := []byte{0xFF, 0xD8, 0xFF, 0xC0, 0x00, 0x09, 0x08, 0x00, 0x10, 0x00}
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})
}

// TestNonSOFMarkersSkipped 验证 RSTn 等独立标记与普通段被正确跳过，
// 最终仍能从后续 SOF 取得尺寸。
func TestNonSOFMarkersSkipped(t *testing.T) {
	buf := bytes.NewBuffer(nil)
	buf.Write([]byte{0xFF, 0xD8})
	buf.Write([]byte{0xFF, 0xD0, 0xFF, 0xD7}) // RST0、RST7：独立标记
	buf.Write(minimalJPEGWithSOF(320, 240)[2:])

	h, _, err := Inspect(buf)
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}
	if h.Width != 320 || h.Height != 240 {
		t.Fatalf("dims = %dx%d, want 320x240", h.Width, h.Height)
	}
}

// TestDecodeRewindError 验证不可 Seek 的读取器传给 Decode 时返回明确错误。
func TestDecodeRewindError(t *testing.T) {
	data := validSmallPNG(2, 2)
	_, _, err := Decode(bytes.NewReader(data), Options{
		Budget: Limits{MaxFramePixels: 1 << 20},
	})
	if err != nil {
		t.Fatalf("bytes.Reader should be seekable: %v", err)
	}

	// brokenSeeker 满足 ReadSeeker 类型约束但 Seek 永远失败，
	// 用于验证 Decode 的回绕错误被显式包装返回。
	_, _, err = Decode(&brokenSeeker{data: data}, Options{
		Budget: Limits{MaxFramePixels: 1 << 20},
	})
	if err == nil {
		t.Fatal("failing Seek must surface as an error from Decode")
	}
}

// brokenSeeker 实现 ReadSeeker，但 Seek 始终返回错误。
type brokenSeeker struct {
	data []byte
	pos  int
}

func (b *brokenSeeker) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	return n, nil
}

func (b *brokenSeeker) Seek(int64, int) (int64, error) {
	return 0, errors.New("seek not supported")
}
