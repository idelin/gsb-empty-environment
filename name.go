package netsec

import (
	"unicode/utf8"
)

// MaxScriptNameBytes 是 RFC 5804 规定的脚本名最大字节数（按 UTF-8 计）。
const MaxScriptNameBytes = 128

// ValidateScriptName 校验 PUTSCRIPT 的脚本名。
//
// 规则：
//   - 非空；
//   - 按 UTF-8 编码不超过 128 字节；
//   - 不含任何控制字符（0x00-0x1F 与 0x7F），因此名称中的 CR/LF/TAB
//     无法被用来改写或拼接命令。
//
// 名称中的引号、分号等可打印字符在协议层只允许出现在带引号字符串里，
// 这里对已解码的名称做内容校验。
func ValidateScriptName(name string) error {
	if name == "" {
		return ErrNameEmpty
	}
	if len(name) > MaxScriptNameBytes {
		return ErrNameTooLong
	}
	if !utf8.ValidString(name) {
		// 非合法 UTF-8 时按控制字符类问题拒绝，避免字节走私。
		return ErrNameControlChar
	}
	for i := 0; i < len(name); i++ {
		b := name[i]
		if b < 0x20 || b == 0x7F {
			return ErrNameControlChar
		}
	}
	return nil
}
