package migrate

import (
	"fmt"
	"sort"
	"strings"
)

const (
	// 内容相似度低于该值的语句不进入候选集。
	minContentScore = 0.5
	// 综合得分权重：内容为主，邻域用于在相同文本间消歧。
	contentWeight = 0.65
	contextWeight = 0.35
	// 邻域匹配允许的漂移窗口（前后各 1 条语句）。
	contextDrift = 1
	// 语句拆分检测的最大行数。
	maxSplitSpan = 3
)

// Candidate 是新文件中一个可能对应断点的位置。
type Candidate struct {
	Line         int      `json:"line"`          // 新文件行号（1 起）；拆分时为拆分块首行
	Span         int      `json:"span"`          // 占用的语句条数，>1 表示原语句被拆分为多行
	ContentScore float64  `json:"content_score"` // 内容相似度
	ContextScore float64  `json:"context_score"` // 邻域匹配度
	Score        float64  `json:"score"`         // 综合置信度
	Reasons      []string `json:"reasons"`       // 人类可读的判定理由
}

// FindCandidates 在新文件语句序列中为锚点寻找候选位置，按置信度降序返回。
func FindCandidates(anchor Anchor, stmts []Statement) []Candidate {
	var cands []Candidate
	targetFlat := stripSpace(anchor.Target.Norm)

	for i, s := range stmts {
		// 1) 单语句内容匹配（精确 / 仅空白差异 / 模糊相似）。
		cs, exact, reason := scoreContent(anchor.Target.Norm, s.Norm)
		if cs >= minContentScore {
			c := Candidate{
				Line:         s.Line,
				Span:         1,
				ContentScore: cs,
				Reasons:      []string{reason},
			}
			if exact && s.Line != anchor.SourceLine {
				c.Reasons = append(c.Reasons,
					fmt.Sprintf("语句从第 %d 行移动到第 %d 行", anchor.SourceLine, s.Line))
			}
			cands = append(cands, c)
		}

		// 2) 拆分检测：连续多条语句拼接后与目标一致，说明原语句被拆行。
		//    扁平化后仅用编辑距离相似度，避免词元比较忽略括号/逗号造成误判。
		for span := 2; span <= maxSplitSpan && i+span <= len(stmts); span++ {
			var b strings.Builder
			for k := 0; k < span; k++ {
				b.WriteString(stmts[i+k].Norm)
			}
			flat := stripSpace(b.String())
			endLine := stmts[i+span-1].Line
			switch {
			case flat == targetFlat:
				cands = append(cands, Candidate{
					Line:         s.Line,
					Span:         span,
					ContentScore: 0.98,
					Reasons: []string{fmt.Sprintf(
						"原语句在新文件中被拆分为 %d 行（第 %d-%d 行）",
						span, s.Line, endLine)},
				})
			default:
				if sim := levSimilarity(targetFlat, flat); sim >= 0.9 {
					cands = append(cands, Candidate{
						Line:         s.Line,
						Span:         span,
						ContentScore: 0.9 * sim,
						Reasons: []string{fmt.Sprintf(
							"原语句疑似被拆分为 %d 行（第 %d-%d 行），拼接相似度 %.2f",
							span, s.Line, endLine, sim)},
					})
				}
			}
		}
	}

	// 3) 邻域打分与综合置信度。拆分候选的后侧邻域从跨行块末尾之后开始。
	for i := range cands {
		idx := stmtIndexAtLine(stmts, cands[i].Line)
		endIdx := idx + cands[i].Span - 1
		cands[i].ContextScore, cands[i].Reasons = scoreContext(anchor, stmts, idx, endIdx, cands[i].Reasons)
		cands[i].Score = contentWeight*cands[i].ContentScore + contextWeight*cands[i].ContextScore
	}

	// 4) 同一行可能同时产生单语句与拆分候选，只保留最优的一个。
	cands = dedupeByLine(cands)

	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Score != cands[j].Score {
			return cands[i].Score > cands[j].Score
		}
		return cands[i].Line < cands[j].Line
	})
	return cands
}

func scoreContent(target, norm string) (score float64, exact bool, reason string) {
	switch {
	case target == norm:
		return 1, true, "语句内容完全一致"
	case stripSpace(target) == stripSpace(norm):
		return 0.97, false, "仅空白/缩进存在差异"
	default:
		sim := contentSimilarity(target, norm)
		return sim, false, fmt.Sprintf("语句内容相似度 %.2f", sim)
	}
}

// scoreContext 比较锚点邻域与候选位置邻域，返回匹配比例 [0,1]。
// idx 是候选首条语句下标，endIdx 是候选末条语句下标（拆分时大于 idx）。
// 邻域语句允许 ±contextDrift 的漂移，以容忍局部增删行。
func scoreContext(anchor Anchor, stmts []Statement, idx, endIdx int, reasons []string) (float64, []string) {
	total := len(anchor.Before) + len(anchor.After)
	if total == 0 {
		return 0.5, append(reasons, "断点邻域为空，无法利用上下文消歧")
	}
	matched := 0
	matched += matchSide(anchor.Before, stmts, idx, idx, endIdx, -1)
	matched += matchSide(anchor.After, stmts, endIdx, idx, endIdx, +1)
	score := float64(matched) / float64(total)
	return score, append(reasons,
		fmt.Sprintf("邻域匹配 %d/%d 条上下文语句", matched, total))
}

// matchSide 统计一侧邻域中能在候选对应位置（±漂移）找到的语句条数。
// base 是该侧的起算下标；dir 为 -1 表示向前（文件头方向），+1 表示向后。
// 落在候选自身占用区间 [lo,hi] 内的下标不参与匹配。
func matchSide(ctx []Statement, stmts []Statement, base, lo, hi, dir int) int {
	matched := 0
	for k, want := range ctx {
		center := base + dir*(k+1)
		for d := -contextDrift; d <= contextDrift; d++ {
			j := center + d
			if j < 0 || j >= len(stmts) || (j >= lo && j <= hi) {
				continue
			}
			if stmts[j].Norm == want.Norm {
				matched++
				break
			}
		}
	}
	return matched
}

func dedupeByLine(cands []Candidate) []Candidate {
	best := map[int]int{}
	var out []Candidate
	for _, c := range cands {
		if i, ok := best[c.Line]; ok {
			if c.Score > out[i].Score {
				out[i] = c
			}
			continue
		}
		best[c.Line] = len(out)
		out = append(out, c)
	}
	return out
}

func stmtIndexAtLine(stmts []Statement, line int) int {
	for i, s := range stmts {
		if s.Line == line {
			return i
		}
	}
	return -1
}
