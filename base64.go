package netsec

// stdBase64Alphabet 是 RFC 2045 使用的标准 Base64 字母表。
const stdBase64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var base64Decode [256]int8

func init() {
	for i := range base64Decode {
		base64Decode[i] = -1
	}
	for i := 0; i < len(stdBase64Alphabet); i++ {
		base64Decode[stdBase64Alphabet[i]] = int8(i)
	}
}

// scanBase64 实现 Base64 解码并记录逐字节依赖。
//
// 依赖粒度是完整的 4 字符编码量子：量子内每个解码字节都依赖该量子
// 全部原始字符（含填充 '='）。因此量子中任一字符未认证，其产出的
// 1~3 个解码字节全部不可信——跨编码单元的半覆盖不可能产生部分可信。
// CR/LF 仅为传输折行，不属于任何量子的依赖；空格/制表符等非法字符
// 按标准库视为损坏。结尾不完整的量子尽力输出但强制不可信并记录截断。
func (s *scanner) scanBase64(raw []byte) {
	var symPos [4]int64
	var dbuf [4]byte
	j := 0

	for i := 0; i < len(raw); {
		b := raw[i]
		pos := int64(i)
		i++

		if b == '\r' || b == '\n' {
			continue
		}

		if v := base64Decode[b]; v >= 0 {
			symPos[j] = pos
			dbuf[j] = byte(v)
			j++
			if j == 4 {
				s.emitQuantum(dbuf, 3, s.deps(symPos[0], symPos[1], symPos[2], symPos[3]), false)
				j = 0
			}
			continue
		}

		if b != '=' {
			// 非法 Base64 字符（含空格、制表符）：停止解码，保留此前输出。
			s.issue(IssueMalformed, pos, pos+1)
			return
		}

		// 填充处理，规则与 encoding/base64 的 decodeQuantum 对齐。
		if j == 0 || j == 1 {
			s.issue(IssueMalformed, pos, pos+1)
			return
		}
		firstPad := pos
		if j == 2 {
			k := i
			for k < len(raw) && (raw[k] == '\r' || raw[k] == '\n') {
				k++
			}
			if k == len(raw) {
				s.issue(IssueMalformed, pos, int64(len(raw)))
				return
			}
			if raw[k] != '=' {
				s.issue(IssueMalformed, int64(k), int64(k)+1)
				return
			}
			s.emitQuantum(dbuf, 1, s.deps(symPos[0], symPos[1], firstPad, int64(k)), false)
			i = k + 1
		} else {
			s.emitQuantum(dbuf, 2, s.deps(symPos[0], symPos[1], symPos[2], firstPad), false)
		}
		j = 0

		// 填充量子之后只允许 CR/LF，否则为尾随垃圾。
		k := i
		for k < len(raw) && (raw[k] == '\r' || raw[k] == '\n') {
			k++
		}
		if k < len(raw) {
			s.issue(IssueMalformed, int64(k), int64(k)+1)
		}
		return
	}

	switch j {
	case 0:
	case 1:
		// 单个悬空符号无法产出任何字节，与标准库一样视为损坏。
		s.issue(IssueMalformed, symPos[0], symPos[0]+1)
	case 2:
		s.issue(IssueTruncated, symPos[0], symPos[1]+1)
		s.emitQuantum(dbuf, 1, s.deps(symPos[0], symPos[1]), true)
	case 3:
		s.issue(IssueTruncated, symPos[0], symPos[2]+1)
		s.emitQuantum(dbuf, 2, s.deps(symPos[0], symPos[1], symPos[2]), true)
	}
}

// emitQuantum 输出一个量子解码出的 outLen 个字节。
// forceUntrusted 用于截断量子：即使现有符号全部已认证也强制不可信。
func (s *scanner) emitQuantum(dbuf [4]byte, outLen int, deps []int64, forceUntrusted bool) {
	val := uint(dbuf[0])<<18 | uint(dbuf[1])<<12 | uint(dbuf[2])<<6 | uint(dbuf[3])
	decoded := [3]byte{byte(val >> 16), byte(val >> 8), byte(val)}
	for k := 0; k < outLen; k++ {
		s.emit(decoded[k], deps)
		if forceUntrusted {
			s.m.Bytes[len(s.m.Bytes)-1].Trusted = false
		}
	}
}
