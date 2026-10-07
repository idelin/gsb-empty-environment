package diff

import "strings"

// SplitLines splits s into lines, each keeping its trailing "\n".
//
// The final line keeps whatever bytes remain, so it may lack a newline:
// "a" and "a\n" split into different lines. An empty string yields no
// lines at all, which keeps an empty file distinct from a file holding a
// single empty line ("\n").
func SplitLines(s string) []string {
	var lines []string
	for len(s) > 0 {
		if k := strings.IndexByte(s, '\n'); k >= 0 {
			lines = append(lines, s[:k+1])
			s = s[k+1:]
		} else {
			lines = append(lines, s)
			break
		}
	}
	return lines
}

// JoinLines is the inverse of SplitLines: it concatenates the lines.
func JoinLines(lines []string) string {
	return strings.Join(lines, "")
}

// DiffText splits both texts with SplitLines and diffs the lines.
func DiffText(oldText, newText string) []Edit {
	return Diff(SplitLines(oldText), SplitLines(newText))
}
