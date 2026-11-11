package netsec

// scanQP 实现 quoted-printable 解码并记录逐字节依赖。
//
// 依赖粒度：
//   - 普通可打印字符、原样放行的高位字节与制表符：单个原始位置；
//   - "=XX" 十六进制转义：'=' 及两个十六进制位共三个原始位置；
//   - 硬换行（行末 CRLF/LF）：换行自身的原始位置；
//   - 软换行 "=CRLF"/"=LF"：不产生任何解码字节，"=" 与换行位置
//     都不出现在任何依赖中（纯传输框架，不可被高亮为正文）；
//   - 行尾在换行前被剥离的空格/制表符同样不产生输出。
func (s *scanner) scanQP(raw []byte) {
	i := 0
	for i < len(raw) {
		// 取到下一个 LF 为止的一行（含 LF）。
		lineStart := i
		j := i
		for j < len(raw) && raw[j] != '\n' {
			j++
		}
		hasLF := j < len(raw)
		if hasLF {
			j++ // 行包含 LF
		}
		line := raw[lineStart:j]

		// 与标准库一致：剥掉行尾的 CR/LF/空格/制表符，再判断软换行。
		contentEnd := len(line)
		for contentEnd > 0 && isQPDiscardWS(line[contentEnd-1]) {
			contentEnd--
		}

		soft := contentEnd > 0 && line[contentEnd-1] == '='
		if soft {
			contentEnd--
		}

		absEnd := lineStart + contentEnd
		absPos := lineStart
		for absPos < absEnd {
			b := raw[absPos]
			switch {
			case b == '=':
				if absPos+2 < absEnd && isHex(raw[absPos+1]) && isHex(raw[absPos+2]) {
					decoded := fromHex(raw[absPos+1])<<4 | fromHex(raw[absPos+2])
					s.emit(decoded, s.deps(int64(absPos), int64(absPos+1), int64(absPos+2)))
					absPos += 3
					continue
				}
				if absPos+1 < lineStart+len(line) && raw[absPos+1] != '\r' && raw[absPos+1] != '\n' {
					// 标准库行为：后面不是换行也不是合法十六进制时，
					// '=' 按字面量处理。
					s.issue(IssueMalformed, int64(absPos), int64(absPos+1))
					s.emit('=', s.deps(int64(absPos)))
					absPos++
					continue
				}
				// 行尾悬空的 "="：软换行的残缺形式，不产生输出。
				s.issue(IssueTruncated, int64(absPos), int64(absPos+1))
				absPos++
			case b == '\t' || b == '\r' || b == '\n':
				s.emit(b, s.deps(int64(absPos)))
				absPos++
			case b >= 0x80:
				// 非标准但常见：高位字节原样放行。
				s.emit(b, s.deps(int64(absPos)))
				absPos++
			case b < ' ' || b > '~':
				// 非法的未转义控制字符：尽力预览，标记问题且不可信。
				s.issue(IssueMalformed, int64(absPos), int64(absPos+1))
				s.emit(b, s.deps(int64(absPos)))
				absPos++
			default:
				s.emit(b, s.deps(int64(absPos)))
				absPos++
			}
		}

		if soft {
			// 软换行：'=' 与换行全部是传输框架，跳过整行剩余部分。
			i = j
			continue
		}

		if hasLF {
			// 硬换行：输出 CRLF 或单个 LF，每个字节各记各的原始位置。
			lfPos := j - 1
			if lfPos-1 >= lineStart && raw[lfPos-1] == '\r' {
				s.emit('\r', s.deps(int64(lfPos-1)))
			}
			s.emit('\n', s.deps(int64(lfPos)))
		}
		i = j
	}
}

func isQPDiscardWS(b byte) bool {
	return b == '\n' || b == '\r' || b == ' ' || b == '\t'
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'A' && b <= 'F') || (b >= 'a' && b <= 'f')
}
func fromHex(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10
	default:
		return b - 'a' + 10
	}
}
