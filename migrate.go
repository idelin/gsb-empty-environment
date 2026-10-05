// Package bpmigrate 实现断点迁移：以语句结构和邻域定位旧文件中的断点
// 在编辑后的新文件中的对应位置。匹配采用候选 + 置信度仲裁，
// 对复制产生的相同代码不会随意选择，而是返回候选列表及理由。
package bpmigrate

import (
	"fmt"
	"sort"
	"strings"
)

// Status 描述断点迁移结果类别。
type Status string

const (
	StatusUnchanged Status = "unchanged" // 位置未变
	StatusMoved     Status = "moved"     // 唯一匹配，行号变化
	StatusSplit     Status = "split"     // 语句被拆分到多行
	StatusDeleted   Status = "deleted"   // 语句已删除，无可信候选
	StatusAmbiguous Status = "ambiguous" // 多个候选置信度接近，无法唯一确定
)

// Breakpoint 是旧文件中的一个断点（1-based 行号）。
type Breakpoint struct {
	ID   int
	Line int
}

// Candidate 是新文件中的一个候选位置。
type Candidate struct {
	Line       int      `json:"line"`
	EndLine    int      `json:"end_line,omitempty"` // 拆分时覆盖的末行
	Text       string   `json:"text"`
	Confidence float64  `json:"confidence"`
	Reasons    []string `json:"reasons"`
}

// Result 是单个断点的迁移结果。
type Result struct {
	Breakpoint Breakpoint  `json:"breakpoint"`
	Status     Status      `json:"status"`
	Line       int         `json:"line,omitempty"` // 唯一匹配时的新行号
	Candidates []Candidate `json:"candidates,omitempty"`
	Reason     string      `json:"reason"`
}

// 置信度仲裁参数。
const (
	deleteThreshold = 0.45 // 低于此置信度视为已删除
	uniqueMargin    = 0.12 // 前两名差距大于此值才判定唯一
	maxCandidates   = 5
)

// Migrate 将旧文件中的断点迁移到新文件。
func Migrate(oldSrc, newSrc string, bps []Breakpoint) []Result {
	oldStmts := parseStatements(oldSrc)
	newStmts := parseStatements(newSrc)

	results := make([]Result, 0, len(bps))
	for _, bp := range bps {
		anchor := findStatement(oldStmts, bp.Line)
		if anchor == nil {
			results = append(results, Result{
				Breakpoint: bp,
				Status:     StatusDeleted,
				Reason:     "旧文件中该行没有可定位的语句",
			})
			continue
		}
		results = append(results, migrateOne(bp, *anchor, newStmts, newSrc))
	}
	return results
}

func migrateOne(bp Breakpoint, anchor statement, newStmts []statement, newSrc string) Result {
	cands := buildCandidates(anchor, newStmts)

	// 无单行候选时，检测语句是否被拆分到多行。
	if len(cands) == 0 || cands[0].Confidence < deleteThreshold {
		if sp, ok := detectSplit(anchor, newSrc); ok {
			return Result{
				Breakpoint: bp,
				Status:     StatusSplit,
				Line:       sp.Line,
				Candidates: []Candidate{sp},
				Reason:     "原语句未在任何单行完整出现，但其内容连续分布在新文件的多行中，判定为语句被拆分",
			}
		}
	}

	if len(cands) == 0 {
		return Result{
			Breakpoint: bp,
			Status:     StatusDeleted,
			Reason:     "新文件中找不到文本或结构相似的语句，判定为已删除",
		}
	}

	best := cands[0]
	if best.Confidence < deleteThreshold {
		return Result{
			Breakpoint: bp,
			Status:     StatusDeleted,
			Candidates: cands,
			Reason: fmt.Sprintf("最佳候选（第 %d 行）置信度 %.2f 低于阈值 %.2f，判定为已删除；附最接近的候选供人工确认",
				best.Line, best.Confidence, deleteThreshold),
		}
	}

	// 冲突仲裁：与第一名差距在 uniqueMargin 内的候选构成冲突组。
	conflicts := []Candidate{best}
	for _, c := range cands[1:] {
		if best.Confidence-c.Confidence <= uniqueMargin {
			conflicts = append(conflicts, c)
		}
	}

	if len(conflicts) > 1 {
		lines := make([]string, 0, len(conflicts))
		for _, c := range conflicts {
			lines = append(lines, fmt.Sprintf("%d(%.2f)", c.Line, c.Confidence))
		}
		return Result{
			Breakpoint: bp,
			Status:     StatusAmbiguous,
			Candidates: conflicts,
			Reason: fmt.Sprintf("存在 %d 个置信度接近的候选（%s），语句文本相同且邻域无法区分，不随意选择，需人工确认",
				len(conflicts), strings.Join(lines, ", ")),
		}
	}

	status := StatusMoved
	reason := fmt.Sprintf("唯一匹配到第 %d 行（置信度 %.2f）", best.Line, best.Confidence)
	if best.Line == bp.Line {
		status = StatusUnchanged
		reason = fmt.Sprintf("语句位置未变（第 %d 行，置信度 %.2f）", best.Line, best.Confidence)
	}
	return Result{
		Breakpoint: bp,
		Status:     status,
		Line:       best.Line,
		Candidates: []Candidate{best},
		Reason:     reason,
	}
}

// buildCandidates 为锚点语句在新文件中构建候选列表并打分排序。
func buildCandidates(anchor statement, newStmts []statement) []Candidate {
	var cands []Candidate
	for _, ns := range newStmts {
		textSim := textSimilarity(anchor.Norm, ns.Norm)
		tokSim := tokenSimilarity(anchor.Norm, ns.Norm)
		if textSim < 0.4 && tokSim < 0.4 {
			continue // 明显不相关，剪枝
		}
		ctxSim := contextSimilarity(anchor, ns)
		conf := 0.45*textSim + 0.25*tokSim + 0.30*ctxSim

		var reasons []string
		if anchor.Norm == ns.Norm {
			reasons = append(reasons, "语句文本完全一致")
		} else {
			reasons = append(reasons, fmt.Sprintf("文本相似度 %.2f", textSim))
		}
		if anchor.Prev != "" && anchor.Prev == ns.Prev {
			reasons = append(reasons, "前一条语句一致")
		}
		if anchor.Next != "" && anchor.Next == ns.Next {
			reasons = append(reasons, "后一条语句一致")
		}
		if anchor.Block != "" && anchor.Block == ns.Block {
			reasons = append(reasons, "外层块头一致")
		}
		if anchor.Depth == ns.Depth {
			reasons = append(reasons, "嵌套深度一致")
		}
		cands = append(cands, Candidate{
			Line:       ns.Line,
			Text:       strings.TrimSpace(ns.Raw),
			Confidence: conf,
			Reasons:    reasons,
		})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Confidence != cands[j].Confidence {
			return cands[i].Confidence > cands[j].Confidence
		}
		return cands[i].Line < cands[j].Line
	})
	if len(cands) > maxCandidates {
		cands = cands[:maxCandidates]
	}
	return cands
}

// detectSplit 检测锚点语句是否被拆分为新文件中连续的多行：
// 锚点的 token 序列按顺序出现在某个连续行窗口内（允许拆行引入的
// 逗号、括号等标点差异），且窗口跨多行，即判定为拆分。
func detectSplit(anchor statement, newSrc string) (Candidate, bool) {
	needle := tokenSeq(anchor.Norm)
	if len(needle) < 3 {
		return Candidate{}, false // token 太少时拆分检测不可靠
	}
	lines := strings.Split(newSrc, "\n")
	const maxWindow = 10
	for i := 0; i < len(lines); i++ {
		// 窗口首行必须包含锚点的首个 token，避免窗口从无关位置开始。
		if !containsToken(tokenSeq(lines[i]), needle[0]) {
			continue
		}
		var window []string
		for j := i; j < len(lines) && j < i+maxWindow; j++ {
			window = append(window, tokenSeq(lines[j])...)
			if j == i || !isSubseq(needle, window) {
				continue
			}
			// 窗口不能明显比锚点大，避免把整段代码误判为拆分。
			if len(window) > len(needle)*2+4 {
				break
			}
			// 把紧随的语句收尾行（如 ")"）并入拆分范围；
			// 不含块定界符 {}，避免吞掉外层块的结束行。
			end := j
			for end+1 < len(lines) && isStatementTail(lines[end+1]) {
				end++
			}
			return Candidate{
				Line:       i + 1,
				EndLine:    end + 1,
				Text:       strings.TrimSpace(lines[i]),
				Confidence: 0.9,
				Reasons: []string{
					fmt.Sprintf("原语句的 token 序列连续分布在新文件第 %d-%d 行", i+1, j+1),
					"判定为语句被拆分为多行",
				},
			}, true
		}
	}
	return Candidate{}, false
}

// tokenSeq 提取有序 token 序列。
func tokenSeq(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 0x80)
	})
}

// isStatementTail 判断一行是否为语句拆分的收尾行：仅由括号、逗号、
// 分号等标点组成，不含块定界符。
func isStatementTail(line string) bool {
	c := compact(line)
	if c == "" {
		return false
	}
	for _, r := range c {
		switch r {
		case '(', ')', '[', ']', ',', ';', '.':
		default:
			return false
		}
	}
	return true
}

func containsToken(toks []string, want string) bool {
	for _, t := range toks {
		if t == want {
			return true
		}
	}
	return false
}

// isSubseq 判断 needle 是否为 hay 的子序列。
func isSubseq(needle, hay []string) bool {
	if len(needle) > len(hay) {
		return false
	}
	k := 0
	for _, t := range hay {
		if t == needle[k] {
			k++
			if k == len(needle) {
				return true
			}
		}
	}
	return false
}
