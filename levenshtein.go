package taskpkg

// Levenshtein returns the rune-level Levenshtein edit distance between a and b.
//
// The implementation uses two dynamic-programming rows and trims common
// prefixes/suffixes first, so memory is O(min(len(a), len(b))).
func Levenshtein(a, b string) int {
	return levenshteinRunes([]rune(a), []rune(b))
}

func levenshteinRunes(a, b []rune) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	if prefix == len(a) && prefix == len(b) {
		return 0
	}

	aEnd, bEnd := len(a), len(b)
	for aEnd > prefix && bEnd > prefix && a[aEnd-1] == b[bEnd-1] {
		aEnd--
		bEnd--
	}
	a = a[prefix:aEnd]
	b = b[prefix:bEnd]
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Keep the inner dimension as small as possible.
	if len(a) > len(b) {
		a, b = b, a
	}
	prev := make([]int, len(a)+1)
	curr := make([]int, len(a)+1)
	for i := range prev {
		prev[i] = i
	}
	for j := 1; j <= len(b); j++ {
		curr[0] = j
		bj := b[j-1]
		for i := 1; i <= len(a); i++ {
			sub := prev[i-1]
			if a[i-1] != bj {
				sub++
			}
			del := prev[i] + 1
			ins := curr[i-1] + 1
			m := sub
			if del < m {
				m = del
			}
			if ins < m {
				m = ins
			}
			curr[i] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(a)]
}
