package migrate

import "fmt"

// Status 是断点迁移的结论类型。
type Status string

const (
	// StatusUnique 表示新文件中存在唯一可信位置。
	StatusUnique Status = "unique"
	// StatusAmbiguous 表示存在多个无法区分的候选，绝不随意挑选，
	// 全部候选连同理由一并返回，交由用户裁决。
	StatusAmbiguous Status = "ambiguous"
	// StatusDeleted 表示断点所在语句在新文件中已不存在。
	StatusDeleted Status = "deleted"
)

const (
	// 综合置信度达到该值且与次优候选拉开 margin 差距时，判定为唯一位置。
	uniqueThreshold = 0.8
	// 判定唯一位置所需的最小领先幅度。
	uniqueMargin = 0.12
	// 最高内容相似度低于该值时，认为原语句已被删除。
	deleteContentThreshold = 0.5
	// 冲突候选的收录窗口：与最优候选分差在此范围内的一并返回。
	conflictWindow = 0.12
	// 返回的候选数量上限。
	maxCandidates = 5
)

// Result 是单个断点的迁移结果。
type Result struct {
	Status     Status      `json:"status"`
	SourceLine int         `json:"source_line"`
	Line       int         `json:"line,omitempty"` // StatusUnique 时的新行号
	Span       int         `json:"span,omitempty"` // 语句被拆分时占用的行数
	Confidence float64     `json:"confidence,omitempty"`
	Candidates []Candidate `json:"candidates,omitempty"` // StatusAmbiguous 时的候选列表
	Reasons    []string    `json:"reasons"`
}

// Migrate 把旧文件第 line 行的断点迁移到新文件。
func Migrate(oldSrc, newSrc string, line int) (Result, error) {
	oldStmts := ParseStatements(oldSrc)
	anchor, err := BuildAnchor(oldStmts, line)
	if err != nil {
		return Result{}, err
	}
	newStmts := ParseStatements(newSrc)
	cands := FindCandidates(anchor, newStmts)
	return decide(anchor, cands), nil
}

// decide 依据置信度与候选间差距做唯一/冲突/删除裁决。
func decide(anchor Anchor, cands []Candidate) Result {
	base := Result{SourceLine: anchor.SourceLine}

	// 删除：没有任何候选的内容与原语句足够相似。
	if len(cands) == 0 || cands[0].ContentScore < deleteContentThreshold {
		base.Status = StatusDeleted
		base.Reasons = []string{fmt.Sprintf(
			"原语句 %q 在新文件中不存在足够相似的内容，判定为已删除",
			anchor.Target.Norm)}
		return base
	}

	top := cands[0]
	second := 0.0
	if len(cands) > 1 {
		second = cands[1].Score
	}

	// 唯一精确匹配：内容完全一致且不存在其他近似一致的候选时，
	// 即使邻域变化较大（如语句被移动到其他函数），也可唯一定位。
	soleExact := top.ContentScore == 1 && (len(cands) == 1 || cands[1].ContentScore < 0.97)
	if soleExact && top.Score-second >= uniqueMargin {
		base.Status = StatusUnique
		base.Line = top.Line
		base.Span = top.Span
		base.Confidence = round2(top.Score)
		base.Reasons = append([]string{"内容在新文件中唯一精确匹配"}, top.Reasons...)
		return base
	}

	// 唯一：置信度足够高且明显领先次优候选。
	if top.Score >= uniqueThreshold && top.Score-second >= uniqueMargin {
		base.Status = StatusUnique
		base.Line = top.Line
		base.Span = top.Span
		base.Confidence = round2(top.Score)
		base.Reasons = top.Reasons
		return base
	}

	// 冲突：收录与最优候选分差在窗口内的全部候选，绝不随意挑选。
	base.Status = StatusAmbiguous
	var tied []Candidate
	for _, c := range cands {
		if top.Score-c.Score > conflictWindow || len(tied) >= maxCandidates {
			break
		}
		tied = append(tied, c)
	}
	base.Candidates = tied
	base.Confidence = round2(top.Score)

	switch {
	case top.Score < uniqueThreshold:
		base.Reasons = append(base.Reasons, fmt.Sprintf(
			"最高置信度 %.2f 低于唯一判定阈值 %.2f", top.Score, uniqueThreshold))
	}
	if len(tied) > 1 && tied[0].ContentScore >= 0.97 && tied[1].ContentScore >= 0.97 {
		base.Reasons = append(base.Reasons,
			"相同（或仅空白不同的）文本在新文件中出现多次，且邻域不足以区分，不随意选择")
	} else if len(tied) > 1 {
		base.Reasons = append(base.Reasons, fmt.Sprintf(
			"前 %d 个候选置信度接近（分差 < %.2f），不随意选择", len(tied), uniqueMargin))
	}
	return base
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
