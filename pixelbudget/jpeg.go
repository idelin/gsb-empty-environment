package pixelbudget

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// JPEG 以 0xFF 后跟标记字节组织段结构。
const (
	jpegMarkerPrefix = 0xFF
	jpegSOI          = 0xD8 // 图像起始
	jpegEOI          = 0xD9 // 图像结束
	jpegTEM          = 0x01 // 独立标记，无长度字段
	jpegSOS          = 0xDA // 扫描数据起始，其后是熵编码数据
)

// isStandaloneMarker 判断标记是否不带 2 字节长度字段。
// RSTn（0xD0-0xD7）、SOI、EOI、TEM 均为独立标记。
func isStandaloneMarker(marker byte) bool {
	return marker == jpegSOI || marker == jpegEOI || marker == jpegTEM ||
		(marker >= 0xD0 && marker <= 0xD7)
}

// isSOFMarker 判断标记是否为帧起始（Start Of Frame），尺寸只在这类段中声明。
func isSOFMarker(marker byte) bool {
	// SOF0..SOF15，但去掉交错的 DHT(0xC4)、JPG(0xC8)、DAC(0xCC)。
	if marker < 0xC0 || marker > 0xCF {
		return false
	}
	switch marker {
	case 0xC4, 0xC8, 0xCC:
		return false
	default:
		return true
	}
}

// inspectJPEG 扫描 JPEG 标记段，在第一个 SOF 段取得宽高，
// 到达 SOS（实际熵编码扫描数据）即停止，绝不解码像素。
func inspectJPEG(r *bufio.Reader) (Header, error) {
	first, err := r.ReadByte()
	if err != nil {
		return Header{}, fmt.Errorf("%w: missing JPEG SOI: %v", ErrHeader, err)
	}
	second, err := r.ReadByte()
	if err != nil {
		return Header{}, fmt.Errorf("%w: truncated JPEG SOI: %v", ErrHeader, err)
	}
	if first != jpegMarkerPrefix || second != jpegSOI {
		return Header{}, fmt.Errorf("%w: bad JPEG SOI marker", ErrHeader)
	}

	for {
		marker, err := readMarker(r)
		if err != nil {
			return Header{}, err
		}

		if isStandaloneMarker(marker) {
			if marker == jpegEOI {
				return Header{}, fmt.Errorf("%w: EOI before SOF dimension segment", ErrHeader)
			}
			continue
		}

		segment, err := readSegment(r)
		if err != nil {
			return Header{}, err
		}

		if isSOFMarker(marker) {
			if len(segment) < 5 {
				return Header{}, fmt.Errorf("%w: SOF segment too short", ErrHeader)
			}
			// 段布局：精度(1) + 高度(2) + 宽度(2) + 组件信息...
			height := binary.BigEndian.Uint16(segment[1:3])
			width := binary.BigEndian.Uint16(segment[3:5])
			if width == 0 || height == 0 {
				return Header{}, ErrZeroDimension
			}
			h := Header{
				Format: FormatJPEG,
				Width:  uint32(width),
				Height: uint32(height),
				Frames: 1, // JPEG 无多帧动画概念。
			}
			return finalizeHeader(h)
		}

		if marker == jpegSOS {
			// 进入扫描数据前仍未见到 SOF，属于结构损坏。
			return Header{}, fmt.Errorf("%w: SOS before SOF dimension segment", ErrHeader)
		}
	}
}

// readMarker 读取一个标记字节，跳过填充用的连续 0xFF。
func readMarker(r *bufio.Reader) (byte, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return 0, fmt.Errorf("%w: expected marker prefix: %v", ErrHeader, err)
	}
	if prefix != jpegMarkerPrefix {
		return 0, fmt.Errorf("%w: expected 0xFF marker prefix, got 0x%02x", ErrHeader, prefix)
	}
	for {
		marker, err := r.ReadByte()
		if err != nil {
			return 0, fmt.Errorf("%w: truncated marker byte: %v", ErrHeader, err)
		}
		if marker == jpegMarkerPrefix {
			continue // 填充字节
		}
		if marker == 0 {
			return 0, fmt.Errorf("%w: invalid marker byte 0x00", ErrHeader)
		}
		return marker, nil
	}
}

// readSegment 读取带长度字段的标记段，返回去掉长度字段本身后的载荷。
func readSegment(r *bufio.Reader) ([]byte, error) {
	lenBuf := make([]byte, 2)
	if _, err := io.ReadFull(r, lenBuf); err != nil {
		return nil, fmt.Errorf("%w: truncated segment length: %v", ErrHeader, err)
	}
	length := binary.BigEndian.Uint16(lenBuf)
	if length < 2 {
		return nil, fmt.Errorf("%w: invalid segment length %d", ErrHeader, length)
	}
	payload := make([]byte, length-2)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("%w: truncated segment payload: %v", ErrHeader, err)
	}
	return payload, nil
}
