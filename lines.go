// Package linediff computes minimal line-based edit scripts between two
// sequences of lines, with deterministic tie-breaking, and provides an
// applier that applies a script back to the old lines.
package linediff

import "strings"

// SplitLines splits s into lines, each keeping its trailing "\n".
// The final line may lack the trailing newline; an empty string yields
// zero lines, so "" and "a" (no trailing newline) stay distinguishable
// from "a\n".
func SplitLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// JoinLines concatenates lines back into a single string. It is the exact
// inverse of SplitLines: JoinLines(SplitLines(s)) == s.
func JoinLines(lines []string) string {
	return strings.Join(lines, "")
}
