package polyglot

import "fmt"

// pngSignature 为 PNG 8 字节文件签名。
var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

type pngParser struct{}

// parse 校验 PNG 签名与块链：
// 必须以签名起始、IHDR 为首个块、IEND 为最后一个块且文件在 IEND 处结束。
func (pngParser) parse(r *boundedReader) (string, error) {
	size := r.Size()
	const sigLen = 8
	if size < sigLen {
		return "", fmt.Errorf("文件短于 PNG 签名（%d 字节）", size)
	}
	sig, err := r.bytesAt(0, sigLen)
	if err != nil {
		return "", err
	}
	for i := range pngSignature {
		if sig[i] != pngSignature[i] {
			return "", fmt.Errorf("偏移 0 处不是 PNG 签名")
		}
	}

	var (
		off     int64 = sigLen
		sawIHDR       = false
		sawIEND       = false
		chunks  int
	)
	for off < size {
		// 每个块：长度(4) + 类型(4) + 数据(长度) + CRC(4)。
		if size-off < 8 {
			return "", fmt.Errorf("偏移 %d 处块头不完整", off)
		}
		length, err := r.u32beAt(off)
		if err != nil {
			return "", err
		}
		chunkType, err := r.bytesAt(off+4, 4)
		if err != nil {
			return "", err
		}
		blockEnd := off + 12 + int64(length)
		if blockEnd > size {
			return "", fmt.Errorf("块 %q（偏移 %d，长度 %d）超出文件范围", chunkType, off, length)
		}

		switch {
		case string(chunkType) == "IHDR":
			if chunks != 0 {
				return "", fmt.Errorf("IHDR 必须是首个块，却出现在偏移 %d", off)
			}
			if length != 13 {
				return "", fmt.Errorf("IHDR 长度应为 13，实际为 %d", length)
			}
			sawIHDR = true
		case string(chunkType) == "IEND":
			if length != 0 {
				return "", fmt.Errorf("IEND 数据长度应为 0，实际为 %d", length)
			}
			sawIEND = true
		}
		chunks++
		off = blockEnd
		if sawIEND {
			break
		}
	}

	if !sawIHDR {
		return "", fmt.Errorf("缺少 IHDR 块")
	}
	if !sawIEND {
		return "", fmt.Errorf("缺少 IEND 块，块链不完整")
	}
	if off != size {
		return "", fmt.Errorf("IEND 之后存在 %d 字节尾随数据，可能是多义文件", size-off)
	}
	return fmt.Sprintf("签名+%d 个块，IEND 终止于文件末尾", chunks), nil
}
