package blockdict

// 自实现的 LZ77 变体，不调用任何压缩库。
//
// 令牌流编码：
//   tag 0x00..0xFE: 匹配，长度 = tag + minMatch，后跟 2 字节大端回退距离
//   tag 0xFF:       字面量，后跟 1 个原始字节
//
// 窗口 = 字典（只读前缀）+ 本块已解码输出，因此每个块独立可解，
// 不依赖前一块的任何状态。

const (
	minMatch  = 4
	maxMatch  = 254 + minMatch
	maxOffset = 1 << 16
	hashBits  = 16
)

func hash4(b []byte, i int) uint32 {
	v := uint32(b[i]) | uint32(b[i+1])<<8 | uint32(b[i+2])<<16 | uint32(b[i+3])<<24
	return (v * 2654435761) >> (32 - hashBits)
}

// windowByte 按绝对位置取窗口字节：位置 [0,len(dict)) 落在字典，
// 之后落在 src（编码）或 out（解码）。
func windowByte(dict, tail []byte, pos int) byte {
	if pos < len(dict) {
		return dict[pos]
	}
	return tail[pos-len(dict)]
}

func compress(dict, src []byte) []byte {
	out := make([]byte, 0, len(src)/2+16)
	table := make([]int, 1<<hashBits)
	for i := range table {
		table[i] = -1
	}
	// 用字典播种哈希表，使匹配可以引用字典内容。
	for i := 0; i+minMatch <= len(dict); i++ {
		table[hash4(dict, i)] = i
	}

	base := len(dict)
	for i := 0; i < len(src); {
		bestLen, bestOff := 0, 0
		if i+minMatch <= len(src) {
			h := hash4(src, i)
			cand := table[h]
			table[h] = base + i
			if cand >= 0 {
				off := base + i - cand
				if off > 0 && off < maxOffset {
					l := 0
					for i+l < len(src) && l < maxMatch &&
						windowByte(dict, src, cand+l) == src[i+l] {
						l++
					}
					if l >= minMatch {
						bestLen, bestOff = l, off
					}
				}
			}
		}
		if bestLen >= minMatch {
			out = append(out, byte(bestLen-minMatch),
				byte(bestOff>>8), byte(bestOff))
			// 为匹配内部的位置补充哈希项，提高后续命中率。
			for k := 1; k < bestLen && i+k+minMatch <= len(src); k++ {
				table[hash4(src, i+k)] = base + i + k
			}
			i += bestLen
		} else {
			out = append(out, 0xFF, src[i])
			i++
		}
	}
	return out
}

// decompress 解码令牌流，输出字节数严格不超过 limit。
func decompress(dict, comp []byte, limit int) ([]byte, error) {
	out := make([]byte, 0, 256)
	i := 0
	for i < len(comp) {
		tag := comp[i]
		i++
		if tag == 0xFF {
			if i >= len(comp) {
				return nil, ErrCorrupt
			}
			out = append(out, comp[i])
			i++
		} else {
			if i+2 > len(comp) {
				return nil, ErrCorrupt
			}
			off := int(comp[i])<<8 | int(comp[i+1])
			i += 2
			l := int(tag) + minMatch
			if off <= 0 || off > len(dict)+len(out) {
				return nil, ErrCorrupt
			}
			for k := 0; k < l; k++ {
				out = append(out, windowByte(dict, out, len(dict)+len(out)-off))
				if len(out) > limit {
					return nil, ErrOutputLimit
				}
			}
		}
		if len(out) > limit {
			return nil, ErrOutputLimit
		}
	}
	return out, nil
}
