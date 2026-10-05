package bpmigrate

import "strings"

// levenshtein 计算两个字符串的编辑距离。
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
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
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	if a > b {
		a = b
	}
	if a > c {
		a = c
	}
	return a
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// textSimilarity 基于归一化文本的编辑相似度，范围 [0,1]。
func textSimilarity(a, b string) float64 {
	if a == b {
		return 1
	}
	longest := maxInt(len([]rune(a)), len([]rune(b)))
	if longest == 0 {
		return 1
	}
	return 1 - float64(levenshtein(a, b))/float64(longest)
}

// tokenSet 提取标识符/数字 token 集合。
func tokenSet(s string) map[string]bool {
	toks := strings.FieldsFunc(s, func(r rune) bool {
		return !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 0x80)
	})
	set := make(map[string]bool, len(toks))
	for _, t := range toks {
		set[t] = true
	}
	return set
}

// tokenSimilarity 为 token 集合的 Jaccard 相似度。
func tokenSimilarity(a, b string) float64 {
	sa, sb := tokenSet(a), tokenSet(b)
	if len(sa) == 0 && len(sb) == 0 {
		return 1
	}
	inter := 0
	for t := range sa {
		if sb[t] {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}

// contextSimilarity 比较两条语句的结构邻域（前后语句、块头、深度）。
func contextSimilarity(a, b statement) float64 {
	var total, weight float64
	add := func(x, y string, w float64) {
		if x == "" && y == "" {
			return // 双方都无该邻域，不提供信息
		}
		s := textSimilarity(x, y)
		total += s * w
		weight += w
	}
	add(a.Prev, b.Prev, 1)
	add(a.Next, b.Next, 1)
	add(a.Block, b.Block, 1.5)
	if weight == 0 {
		return 0.5 // 无邻域可比，置中性
	}
	score := total / weight
	if a.Depth == b.Depth {
		score += 0.1
		if score > 1 {
			score = 1
		}
	}
	return score
}
