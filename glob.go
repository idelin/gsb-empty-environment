package netsec

// matchGlob 按 AppArmor 风格匹配路径 glob：
//   - '*' 匹配任意不含 '/' 的字符序列（不跨目录）
//   - '**' 匹配任意字符序列（可跨目录）
//   - 其余字符按字面值匹配
func matchGlob(pattern, path string) bool {
	for len(pattern) > 0 {
		if pattern[0] != '*' {
			if len(path) == 0 || path[0] != pattern[0] {
				return false
			}
			pattern = pattern[1:]
			path = path[1:]
			continue
		}
		if len(pattern) > 1 && pattern[1] == '*' {
			rest := pattern
			for len(rest) > 0 && rest[0] == '*' {
				rest = rest[1:]
			}
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(path); i++ {
				if matchGlob(rest, path[i:]) {
					return true
				}
			}
			return false
		}
		rest := pattern[1:]
		for i := 0; ; i++ {
			if matchGlob(rest, path[i:]) {
				return true
			}
			if i >= len(path) || path[i] == '/' {
				return false
			}
		}
	}
	return len(path) == 0
}
