package migrate

import "fmt"

// contextSize 是断点前后各取的邻域语句数量。
const contextSize = 3

// Anchor 描述一个断点的定位锚点：目标语句本身 + 前后邻域语句。
// 文件编辑后，依靠目标语句内容与邻域结构在新文件中重新定位。
type Anchor struct {
	Target     Statement   // 断点所在语句
	Before     []Statement // 之前的邻域语句，由近及远
	After      []Statement // 之后的邻域语句，由近及远
	SourceLine int         // 断点原始行号（1 起）
}

// BuildAnchor 在旧文件的语句序列中，为给定行号构造锚点。
// 若该行是空行（无语句），返回错误。
func BuildAnchor(stmts []Statement, line int) (Anchor, error) {
	idx := -1
	for i, s := range stmts {
		if s.Line == line {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Anchor{}, fmt.Errorf("第 %d 行没有可定位的语句（空行或超出范围）", line)
	}
	a := Anchor{Target: stmts[idx], SourceLine: line}
	for i := idx - 1; i >= 0 && len(a.Before) < contextSize; i-- {
		a.Before = append(a.Before, stmts[i])
	}
	for i := idx + 1; i < len(stmts) && len(a.After) < contextSize; i++ {
		a.After = append(a.After, stmts[i])
	}
	return a, nil
}
