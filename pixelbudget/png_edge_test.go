package pixelbudget

import (
	"bytes"
	"errors"
	"testing"
)

// pngWithChunks 以给定 chunk 列表拼装 PNG，签名固定在最前面。
func pngWithChunks(chunks ...[]byte) []byte {
	var buf bytes.Buffer
	buf.Write(pngSignature)
	for _, c := range chunks {
		buf.Write(c)
	}
	return buf.Bytes()
}

// TestPNGMalformedChunks 覆盖 PNG 头部解析的防御性分支。
func TestPNGMalformedChunks(t *testing.T) {
	goodIHDR := buildChunk("IHDR", ihdrPayload(4, 3))

	t.Run("bad signature", func(t *testing.T) {
		data := make([]byte, len(pngSignature))
		copy(data, pngSignature)
		data[0] = 0x00
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("err = %v, want ErrUnsupported", err)
		}
	})

	t.Run("duplicate ihdr", func(t *testing.T) {
		data := pngWithChunks(goodIHDR, goodIHDR)
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})

	t.Run("bad ihdr length", func(t *testing.T) {
		data := pngWithChunks(buildChunk("IHDR", []byte{1, 2, 3}))
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})

	t.Run("actl zero frames", func(t *testing.T) {
		data := pngWithChunks(goodIHDR, buildChunk("acTL", actlPayload(0)),
			buildChunk("IDAT", []byte{0}))
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})

	t.Run("actl too short", func(t *testing.T) {
		data := pngWithChunks(goodIHDR, buildChunk("acTL", []byte{0, 1}),
			buildChunk("IDAT", []byte{0}))
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})

	t.Run("idat before ihdr", func(t *testing.T) {
		data := pngWithChunks(buildChunk("IDAT", []byte{0}))
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})

	t.Run("iend without ihdr", func(t *testing.T) {
		data := pngWithChunks(buildChunk("IEND", nil))
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})

	t.Run("truncated chunk header", func(t *testing.T) {
		data := pngWithChunks(goodIHDR, []byte{0, 1, 2})
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})

	t.Run("truncated unknown chunk payload", func(t *testing.T) {
		// tEXt 声明 10 字节载荷但完全不提供，CRC 也缺失。
		data := pngWithChunks(goodIHDR, buildChunkHeaderOnly("tEXt", 10))
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})

	t.Run("ihdr without any following chunk", func(t *testing.T) {
		data := pngWithChunks(goodIHDR)
		if _, _, err := Inspect(bytes.NewReader(data)); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})
}

// buildChunkHeaderOnly 只输出长度+类型，不附载荷与 CRC，用于构造截断输入。
func buildChunkHeaderOnly(typ string, length uint32) []byte {
	head := make([]byte, 8)
	put := head
	put[0] = byte(length >> 24)
	put[1] = byte(length >> 16)
	put[2] = byte(length >> 8)
	put[3] = byte(length)
	copy(put[4:8], typ)
	return head
}
