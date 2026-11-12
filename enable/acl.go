package enable

import "unicode/utf8"

func isValidUTF8(s string) bool { return utf8.ValidString(s) }

// ACL 是按解码后的规范 UTF-8 邮箱名保存的授权表。
// 任何模式下都不得拿线上原始字符串直接比较。
type ACL struct {
	allow map[string]bool
}

// NewACL 以规范 UTF-8 名构建授权表。
func NewACL(canonicalNames ...string) *ACL {
	a := &ACL{allow: make(map[string]bool, len(canonicalNames))}
	for _, n := range canonicalNames {
		a.allow[n] = true
	}
	return a
}

// AllowsCanonical 判定规范名是否被授权。
func (a *ACL) AllowsCanonical(canonical string) bool { return a.allow[canonical] }

// Authorize 把线上原始名按当前会话模式解码为规范名后判定授权。
func (s *Session) Authorize(a *ACL, rawName string) (bool, error) {
	canonical, err := s.CanonicalMailboxName(rawName)
	if err != nil {
		return false, err
	}
	return a.AllowsCanonical(canonical), nil
}
