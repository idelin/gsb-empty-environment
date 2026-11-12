package netsec

// globMatch reports whether name matches the glob pattern, where '*'
// matches any (possibly empty) sequence of characters including '/'
// and '?' matches exactly one character.
func globMatch(pattern, name string) bool {
	return globAt(pattern, 0, name, 0)
}

func globAt(pattern string, pi int, name string, ni int) bool {
	for pi < len(pattern) {
		switch pattern[pi] {
		case '*':
			for k := ni; k <= len(name); k++ {
				if globAt(pattern, pi+1, name, k) {
					return true
				}
			}
			return false
		case '?':
			if ni >= len(name) {
				return false
			}
			pi++
			ni++
		default:
			if ni >= len(name) || pattern[pi] != name[ni] {
				return false
			}
			pi++
			ni++
		}
	}
	return ni == len(name)
}
