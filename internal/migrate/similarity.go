package migrate

import (
	"strings"
	"unicode"
)

// contentSimilarity 返回两条语句规范化文本的相似度 [0,1]，
// 取编辑距离相似度与词元 Jaccard 相似度的较大者。
func contentSimilarity(a, b string) float64 {
	if a == b {
		return 1
	}
	lev := 1 - float64(levenshtein(a, b))/float64(max(len(a), len(b)))
	jac := tokenJaccard(a, b)
	return max(lev, jac)
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 0
			if ra[i-1] != rb[j-1] {
				cost = 1
			}
			cur[j] = min(min(cur[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func tokenJaccard(a, b string) float64 {
	ta, tb := tokenSet(a), tokenSet(b)
	if len(ta) == 0 && len(tb) == 0 {
		return 1
	}
	inter := 0
	for t := range ta {
		if tb[t] {
			inter++
		}
	}
	union := len(ta) + len(tb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func tokenSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		set[tok] = true
	}
	return set
}

// levSimilarity 仅基于编辑距离的相似度 [0,1]，用于扁平化后的拼接比较。
func levSimilarity(a, b string) float64 {
	if a == b {
		return 1
	}
	m := max(len(a), len(b))
	if m == 0 {
		return 1
	}
	return 1 - float64(levenshtein(a, b))/float64(m)
}
