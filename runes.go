package netsec

// RuneSpan 是解码后正文上一段可信性一致的高亮区间。
// 区间为半开 [Start, End)，单位是 Decoded 的字节偏移；
// 多字节 UTF-8 字符不会被拆到两个区间里。
type RuneSpan struct {
	Start   int64
	End     int64
	Trusted bool
}

// RuneRanges 把逐字节的 ByteTrust 聚合为界面可直接消费的连续区间。
//
// 一个 rune 只有在其全部 UTF-8 字节都可信时才标为可信；跨在认证
// 边界上的 rune 整体降级为不可信，避免把半个字符渲染成“已签名”。
// 非法 UTF-8 字节按 RuneError 的语义逐个作为单字节 rune 处理，
// 同样只有本身可信时才进入可信区间。
func (m *Mapping) RuneRanges() []RuneSpan {
	if len(m.Bytes) == 0 {
		return nil
	}

	var spans []RuneSpan
	for i := 0; i < len(m.Bytes); {
		size := utf8RuneSize(m.Decoded, i)
		end := i + size

		trusted := true
		for k := i; k < end; k++ {
			if !m.Bytes[k].Trusted {
				trusted = false
				break
			}
		}

		if last := len(spans) - 1; last >= 0 && spans[last].Trusted == trusted && spans[last].End == int64(i) {
			spans[last].End = int64(end)
		} else {
			spans = append(spans, RuneSpan{Start: int64(i), End: int64(end), Trusted: trusted})
		}
		i = end
	}
	return spans
}

// utf8RuneSize 返回 Decoded[i:] 起始处 rune 的 UTF-8 字节宽度，
// 非法或截断序列按 1 字节处理（与 unicode/utf8.DecodeRune 一致）。
func utf8RuneSize(decoded []byte, i int) int {
	b := decoded[i]
	switch {
	case b < 0x80:
		return 1
	case b < 0xC2:
		return 1 // 孤立的续字节或非法前导字节
	case b < 0xE0:
		if i+1 < len(decoded) && isContinuation(decoded[i+1]) {
			return 2
		}
	case b < 0xF0:
		if i+2 < len(decoded) && isContinuation(decoded[i+1]) && isContinuation(decoded[i+2]) {
			return 3
		}
	default:
		if b < 0xF5 && i+3 < len(decoded) &&
			isContinuation(decoded[i+1]) && isContinuation(decoded[i+2]) && isContinuation(decoded[i+3]) {
			return 4
		}
	}
	return 1
}

func isContinuation(b byte) bool { return b&0xC0 == 0x80 }
