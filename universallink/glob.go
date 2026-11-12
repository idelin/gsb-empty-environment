package universallink

// globMatch reports whether name matches pattern. In pattern, '*' matches any
// (possibly empty) sequence of characters and '?' matches exactly one
// character; every other rune matches itself. Matching is rune-aware and
// anchored at both ends.
func globMatch(pattern, name string) bool {
	pat := []rune(pattern)
	txt := []rune(name)

	// dp[i][j] is true when the first i pattern runes match the first j text
	// runes.
	dp := make([][]bool, len(pat)+1)
	for i := range dp {
		dp[i] = make([]bool, len(txt)+1)
	}
	dp[0][0] = true

	for i := 1; i <= len(pat); i++ {
		if pat[i-1] == '*' {
			dp[i][0] = dp[i-1][0]
		}
	}

	for i := 1; i <= len(pat); i++ {
		for j := 1; j <= len(txt); j++ {
			switch pat[i-1] {
			case '*':
				dp[i][j] = dp[i-1][j] || dp[i][j-1]
			case '?':
				dp[i][j] = dp[i-1][j-1]
			default:
				dp[i][j] = dp[i-1][j-1] && pat[i-1] == txt[j-1]
			}
		}
	}

	return dp[len(pat)][len(txt)]
}
