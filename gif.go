package polyglot

import "fmt"

var (
	gif87a = []byte("GIF87a")
	gif89a = []byte("GIF89a")
)

type gifParser struct{}

// parse 校验 GIF 版本头、逻辑屏幕描述符、全局/局部颜色表、
// 图像数据与扩展块链，直到块终止符 0x3B；终止符后不允许尾随字节。
func (gifParser) parse(r *boundedReader) (string, error) {
	size := r.Size()
	if size < 13 {
		return "", fmt.Errorf("文件短于 GIF 头部（%d 字节）", size)
	}
	header, err := r.bytesAt(0, 6)
	if err != nil {
		return "", err
	}
	if !equalBytes(header, gif87a) && !equalBytes(header, gif89a) {
		return "", fmt.Errorf("偏移 0 处不是 GIF87a/GIF89a 版本头")
	}

	// 逻辑屏幕描述符位于偏移 6..12，打包字段末 3 位为全局颜色表尺寸指数。
	packed, err := r.byteAt(10)
	if err != nil {
		return "", err
	}
	off := int64(13)
	if packed&0x80 != 0 {
		entries := 1 << ((packed & 0x07) + 1)
		off += int64(entries) * 3
		if off > size {
			return "", fmt.Errorf("全局颜色表超出文件范围")
		}
	}

	var images int
	for off < size {
		label, err := r.byteAt(off)
		if err != nil {
			return "", err
		}
		switch label {
		case 0x3B: // GIF Trailer，逻辑文件必须在此结束。
			off++
			if off != size {
				return "", fmt.Errorf("GIF 终止符 0x3B 之后存在 %d 字节尾随数据，可能是多义文件", size-off)
			}
			if images == 0 {
				return "", fmt.Errorf("块链完整但不包含任何图像")
			}
			return fmt.Sprintf("版本 %s，%d 个图像，终止符位于文件末尾", string(header), images), nil
		case 0x21: // Extension Introducer。
			off, err = r.skipGIFExtension(off)
			if err != nil {
				return "", err
			}
		case 0x2C: // Image Descriptor。
			off, err = r.skipGIFImage(off)
			if err != nil {
				return "", err
			}
			images++
		default:
			return "", fmt.Errorf("偏移 %d 处出现非法块引入符 0x%02X", off, label)
		}
	}
	return "", fmt.Errorf("块链在到达终止符 0x3B 前结束")
}

// skipGIFExtension 跳过扩展块：0x21 + 标签(1) + 若干子块 + 块终止符。
func (r *boundedReader) skipGIFExtension(off int64) (int64, error) {
	size := r.Size()
	if size-off < 2 {
		return 0, fmt.Errorf("偏移 %d 处扩展块不完整", off)
	}
	off += 2 // 跳过 0x21 与扩展标签。
	return r.skipGIFSubBlocks(off)
}

// skipGIFImage 跳过图像描述符、可选局部颜色表与 LZW 数据子块。
func (r *boundedReader) skipGIFImage(off int64) (int64, error) {
	size := r.Size()
	// 图像描述符固定 10 字节：0x2C + 左/上/宽/高(各2) + 打包字段。
	if size-off < 10 {
		return 0, fmt.Errorf("偏移 %d 处图像描述符不完整", off)
	}
	packed, err := r.byteAt(off + 9)
	if err != nil {
		return 0, err
	}
	off += 10
	if packed&0x80 != 0 { // Local Color Table Flag。
		entries := 1 << ((packed & 0x07) + 1)
		off += int64(entries) * 3
		if off > size {
			return 0, fmt.Errorf("局部颜色表超出文件范围")
		}
	}
	if off >= size {
		return 0, fmt.Errorf("缺少 LZW 最小代码尺寸")
	}
	off++ // LZW Minimum Code Size。
	return r.skipGIFSubBlocks(off)
}

// skipGIFSubBlocks 跳过一连串“长度前缀 + 数据”的子块，直到 0 长度终止符。
func (r *boundedReader) skipGIFSubBlocks(off int64) (int64, error) {
	size := r.Size()
	for off < size {
		length, err := r.byteAt(off)
		if err != nil {
			return 0, err
		}
		off++
		if length == 0 {
			return off, nil
		}
		off += int64(length)
		if off > size {
			return 0, fmt.Errorf("子块数据超出文件范围")
		}
	}
	return 0, fmt.Errorf("子块链缺少 0 长度终止符")
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
