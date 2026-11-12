package pixelbudget

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// pngSignature 是 PNG 文件开头的 8 字节魔数，APNG 与其完全相同。
var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

// maxChunkPayload 限制单个 chunk 声明长度的合理性，避免损坏的长度字段
// 导致跳过荒谬的字节数。合法头部 chunk 远小于此值。
const maxChunkPayload = 1 << 30 // 1 GiB

// chunk 类型标签（4 字节 ASCII）。
var (
	ihdrType = []byte("IHDR")
	actlType = []byte("acTL")
	idatType = []byte("IDAT")
	iendType = []byte("IEND")
)

// inspectPNG 解析 PNG/APNG 头部：读取 IHDR 的宽高与 acTL 的帧数，
// 遇到第一个 IDAT 即停止——像素数据在预算通过前完全不会被触碰。
func inspectPNG(r *bufio.Reader) (Header, error) {
	sig := make([]byte, len(pngSignature))
	if _, err := io.ReadFull(r, sig); err != nil {
		return Header{}, fmt.Errorf("%w: missing PNG signature: %v", ErrHeader, err)
	}
	if !bytes.Equal(sig, pngSignature) {
		return Header{}, fmt.Errorf("%w: bad PNG signature", ErrHeader)
	}

	h := Header{Format: FormatPNG, Frames: 1}
	var sawIHDR bool

	for {
		length, typ, err := readChunkHeader(r)
		if err != nil {
			return Header{}, err
		}
		// 每个 chunk 的载荷之后都紧跟 4 字节 CRC；读完载荷后统一跳过。
		skipCRC := func() error {
			if err := discardN(r, 4); err != nil {
				return fmt.Errorf("%w: truncated CRC of chunk %q: %v", ErrHeader, typ, err)
			}
			return nil
		}

		switch {
		case bytes.Equal(typ, ihdrType):
			if sawIHDR {
				return Header{}, fmt.Errorf("%w: duplicate IHDR chunk", ErrHeader)
			}
			if length != 13 {
				return Header{}, fmt.Errorf("%w: IHDR payload length %d is not 13", ErrHeader, length)
			}
			payload := make([]byte, length)
			if _, err := io.ReadFull(r, payload); err != nil {
				return Header{}, fmt.Errorf("%w: truncated IHDR: %v", ErrHeader, err)
			}
			width := binary.BigEndian.Uint32(payload[0:4])
			height := binary.BigEndian.Uint32(payload[4:8])
			if width == 0 || height == 0 {
				return Header{}, ErrZeroDimension
			}
			h.Width, h.Height = width, height
			sawIHDR = true
			if err := skipCRC(); err != nil {
				return Header{}, err
			}

		case bytes.Equal(typ, actlType):
			// acTL 必须出现在第一个 IDAT 之前才有效；这里能读到即满足顺序要求。
			if length < 4 {
				return Header{}, fmt.Errorf("%w: acTL payload too short", ErrHeader)
			}
			payload := make([]byte, length)
			if _, err := io.ReadFull(r, payload); err != nil {
				return Header{}, fmt.Errorf("%w: truncated acTL: %v", ErrHeader, err)
			}
			frames := binary.BigEndian.Uint32(payload[0:4])
			if frames == 0 {
				return Header{}, fmt.Errorf("%w: acTL declares zero frames", ErrHeader)
			}
			h.Frames = frames
			if err := skipCRC(); err != nil {
				return Header{}, err
			}

		case bytes.Equal(typ, idatType):
			// 到达图像数据起点，头部信息已经齐备；IDAT 载荷原样留给后续解码器。
			if !sawIHDR {
				return Header{}, fmt.Errorf("%w: IDAT before IHDR", ErrHeader)
			}
			return finalizeHeader(h)

		case bytes.Equal(typ, iendType):
			// 没有任何 IDAT 的文件不是合法图像，但头部尺寸仍可核对。
			if !sawIHDR {
				return Header{}, fmt.Errorf("%w: IEND without IHDR", ErrHeader)
			}
			return finalizeHeader(h)

		default:
			// 跳过不关心的 chunk 载荷，再跳过它的 CRC。
			if err := discardN(r, int64(length)); err != nil {
				return Header{}, fmt.Errorf("%w: truncated chunk %q: %v", ErrHeader, typ, err)
			}
			if err := skipCRC(); err != nil {
				return Header{}, err
			}
		}
	}
}

// readChunkHeader 读取 4 字节长度与 4 字节类型，并对长度做合理性校验。
func readChunkHeader(r *bufio.Reader) (uint32, []byte, error) {
	head := make([]byte, 8)
	if _, err := io.ReadFull(r, head); err != nil {
		return 0, nil, fmt.Errorf("%w: truncated chunk header: %v", ErrHeader, err)
	}
	length := binary.BigEndian.Uint32(head[0:4])
	if length > maxChunkPayload {
		return 0, nil, fmt.Errorf("%w: chunk length %d exceeds sanity bound", ErrHeader, length)
	}
	return length, head[4:8], nil
}

// discardN 跳过 n 个字节，遇到提前 EOF 时返回错误。
func discardN(r *bufio.Reader, n int64) error {
	got, err := io.CopyN(io.Discard, r, n)
	if err != nil {
		return err
	}
	if got != n {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// finalizeHeader 用溢出安全的乘法计算单帧与全部帧像素数。
func finalizeHeader(h Header) (Header, error) {
	framePixels, overflow := mulOverflowSafe(uint64(h.Width), uint64(h.Height))
	if overflow {
		return Header{}, fmt.Errorf("%w: %d*%d overflows", ErrSingleFrame, h.Width, h.Height)
	}
	totalPixels, overflow := mulOverflowSafe(framePixels, uint64(h.Frames))
	if overflow {
		return Header{}, fmt.Errorf("%w: %d pixels * %d frames overflows",
			ErrTotalPixels, framePixels, h.Frames)
	}
	h.FramePixels, h.TotalPixels = framePixels, totalPixels
	return h, nil
}
