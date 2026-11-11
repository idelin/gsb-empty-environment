package tsdemux

// crc32MPEG2 计算 ISO/IEC 13818-1 定义的 CRC-32（多项式 0x04C11DB7，
// 初始值 0xFFFFFFFF，输入输出均不取反），用于 PAT/PMT 整段校验。
func crc32MPEG2(data []byte) uint32 {
	var crc uint32 = 0xFFFFFFFF
	for _, b := range data {
		crc ^= uint32(b) << 24
		for bit := 0; bit < 8; bit++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// sectionAssembler 按 PID 重组 PSI（PAT/PMT）section。
//
// 带 payload_unit_start_indicator 的包以 1 字节 pointer_field 开头，
// 其后是新 section 的起点；同一包尾部可能带有上一 section 的最后字节。
// section 的总长度由 section_length 字段给出，据此跨包拼接，收齐后整段
// 交给调用方做 CRC 校验与语义解析。
type sectionAssembler struct {
	buf     []byte
	started bool
}

// push 送入一个 TS 包的去填充 payload，返回本次恰好收齐的完整 section
// （可能为 nil）。返回的切片在下次 push 前有效。
func (a *sectionAssembler) push(payload []byte, pusi bool) []byte {
	if pusi {
		if len(payload) == 0 {
			return nil
		}
		// pointer_field：本包开头属于上一 section 的字节数。
		pointer := int(payload[0])
		rest := payload[1:]
		if pointer > len(rest) {
			pointer = len(rest)
		}
		if a.started && pointer > 0 {
			// 补齐上一 section 的尾部；长度合法则交还给调用方校验，
			// 否则随新 section 开始而被丢弃。
			a.buf = append(a.buf, rest[:pointer]...)
		}
		// pointer_field 本身不属于任何 section，新 section 从其后开始。
		rest = payload[1+pointer:]

		var completed []byte
		if total := sectionTotalLen(a.buf); a.started && total > 0 && len(a.buf) >= total {
			completed = a.finishSized(total)
		} else {
			// 上一 section 无法收齐（通常由丢包引起），直接丢弃。
			a.reset()
		}

		if len(rest) > 0 {
			a.started = true
			a.buf = append(a.buf, rest...)
		}
		if total := sectionTotalLen(a.buf); a.started && total > 0 && len(a.buf) >= total {
			completed = a.finishSized(total)
		}
		return completed
	}

	if !a.started || len(payload) == 0 {
		return nil
	}
	a.buf = append(a.buf, payload...)
	if total := sectionTotalLen(a.buf); total > 0 && len(a.buf) >= total {
		return a.finishSized(total)
	}
	return nil
}

// sectionTotalLen 根据 section 头三字节返回整个 section 的总长度；
// 头部不完整时返回 0。
func sectionTotalLen(buf []byte) int {
	if len(buf) < 3 {
		return 0
	}
	// section_length 为该字段之后的字节数，加回 3 字节头。
	sectionLength := int(buf[1]&0x0F)<<8 | int(buf[2])
	return 3 + sectionLength
}

// finishSized 按 section_length 精确截取完整 section，丢弃同一包内的 TS 填充。
func (a *sectionAssembler) finishSized(total int) []byte {
	complete := make([]byte, total)
	copy(complete, a.buf[:total])
	remainder := a.buf[total:]
	a.reset()
	// 正常 PSI 包的填充只允许 0xFF；若剩余的是新 section 的起始，
	// 留待下一个 pointer_field 处理，这里不做拼接以保持实现简单。
	for _, b := range remainder {
		if b != 0xFF {
			return complete
		}
	}
	return complete
}

func (a *sectionAssembler) reset() {
	a.buf = nil
	a.started = false
}
