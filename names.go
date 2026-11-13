package netsec

import (
	"strings"
	"unicode/utf8"
)

// ValidateName 校验明文名：有效 UTF-8、非空、不含斜杠与 NUL、不是点段（"." 或 ".."）。
func ValidateName(name string) error {
	if name == "" || name == "." || name == ".." {
		return ErrInvalidName
	}
	if !utf8.ValidString(name) {
		return ErrInvalidName
	}
	if strings.IndexByte(name, '/') >= 0 || strings.IndexByte(name, 0x00) >= 0 {
		return ErrInvalidName
	}
	return nil
}

// Canonicalize 按目录大小写模式求规范名。
// Sensitive 返回原始 UTF-8；FoldASCII 仅把 A-Z 映射为 a-z，
// 其余字节（含全部非 ASCII 字节）保持不变，折叠不改变字节长度。
func Canonicalize(name string, mode CaseMode) string {
	if mode != CaseFoldASCII {
		return name
	}
	b := []byte(name)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
