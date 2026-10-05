package bpmigrate

import "strings"

// statement 表示源文件中的一条语句（非空行）及其结构邻域。
// 邻域信息用于在文本相同的多个候选之间消歧：
// 复制出的相同代码不能只凭文本随意匹配。
type statement struct {
	Line  int    // 1-based 行号
	Raw   string // 原始行文本
	Norm  string // 归一化文本（压缩空白）
	Prev  string // 上一条非空语句的归一化文本
	Next  string // 下一条非空语句的归一化文本
	Block string // 最近外层块头（如函数签名、if/for 行）
	Depth int    // 花括号嵌套深度
}

func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// compact 去除全部空白，用于跨行（拆分）匹配。
func compact(s string) string {
	return strings.Join(strings.Fields(s), "")
}

func isBlank(s string) bool {
	return strings.TrimSpace(s) == ""
}

// parseStatements 将源码解析为语句序列，并填充结构邻域。
func parseStatements(src string) []statement {
	lines := strings.Split(src, "\n")
	type rawLine struct {
		no   int
		text string
	}
	var raws []rawLine
	for i, l := range lines {
		if isBlank(l) {
			continue
		}
		raws = append(raws, rawLine{no: i + 1, text: l})
	}

	stmts := make([]statement, 0, len(raws))
	depth := 0
	headers := map[int]string{} // depth -> 打开该深度的块头文本
	for i, r := range raws {
		norm := normalize(r.text)
		st := statement{Line: r.no, Raw: r.text, Norm: norm}
		if i > 0 {
			st.Prev = normalize(raws[i-1].text)
		}
		if i < len(raws)-1 {
			st.Next = normalize(raws[i+1].text)
		}

		opens := strings.Count(norm, "{")
		closes := strings.Count(norm, "}")
		// 以 } 开头的行（如 "} else {"）属于外层块。
		effDepth := depth
		if strings.HasPrefix(norm, "}") && effDepth > 0 {
			effDepth--
		}
		st.Depth = effDepth
		st.Block = headers[effDepth]
		if opens > closes {
			headers[effDepth+1] = norm
		}
		depth += opens - closes
		if depth < 0 {
			depth = 0
		}
		stmts = append(stmts, st)
	}
	return stmts
}

// findStatement 返回覆盖指定行号的语句；若该行是空行，取最近的非空语句。
func findStatement(stmts []statement, line int) *statement {
	var best *statement
	bestDist := 1 << 30
	for i := range stmts {
		d := stmts[i].Line - line
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			bestDist = d
			best = &stmts[i]
		}
	}
	return best
}
