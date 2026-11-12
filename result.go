package netsec

// Analyze 分析 format 的参数消费。tainted 为 true 时表示格式串来自不可信
// 数据源，此时仍做尽力解析并返回消费记录，但 Result.Guaranteed 为 false。
func Analyze(format string, tainted bool) *Result {
	r := &Result{
		Guaranteed:   true,
		maxExplicit:  -1,
		seenExplicit: map[int]bool{},
	}
	if tainted {
		r.Tainted = true
		r.Guaranteed = false
		r.diag(DiagTainted, "格式串来源不可信，无法静态保证参数消费结论")
	}
	r.parse(format)
	for _, d := range r.Diagnostics {
		if d.Code == DiagMalformed || d.Code == DiagBadIndex {
			r.Guaranteed = false
			break
		}
	}
	return r
}

// AnalyzeFormat 是 Analyze(format, false) 的便捷入口，用于可信字面量格式串。
func AnalyzeFormat(format string) *Result {
	return Analyze(format, false)
}

func (r *Result) diag(code, msg string) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{Code: code, Msg: msg})
}

// ArgCount 返回静态消费所覆盖的参数个数。顺序模式为顺序消费总数；显式
// 模式为最大索引加一（含被跳过的位置）。
func (r *Result) ArgCount() int {
	if r.ExplicitMode {
		return r.maxExplicit + 1
	}
	return r.sequentialCount
}

// Constraint 返回参数位置 index 上聚合后的类型约束；位置未被消费时
// 返回 KindUnknown。
func (r *Result) Constraint(index int) Kind {
	if index < 0 || index >= len(r.constraints) {
		return KindUnknown
	}
	return r.constraints[index]
}

// RepeatedPositions 返回被重复消费的显式位置（零基、升序去重）。
func (r *Result) RepeatedPositions() []int {
	seen := map[int]bool{}
	var out []int
	for _, c := range r.Consumptions {
		if c.Explicit && c.Repeated && !seen[c.Index] {
			seen[c.Index] = true
			out = append(out, c.Index)
		}
	}
	return out
}

// TruncatingPositions 返回带精度截断的字符串转换所消费的参数位置
// （零基、按首次出现次序去重）。
func (r *Result) TruncatingPositions() []int {
	seen := map[int]bool{}
	var out []int
	for _, c := range r.Consumptions {
		if c.Truncating && !seen[c.Index] {
			seen[c.Index] = true
			out = append(out, c.Index)
		}
	}
	return out
}

// TypeMismatch 对照宿主提供的实际参数类型 actual（零基对齐），返回首个
// 不满足约束的参数位置；全部满足时返回 -1。
func (r *Result) TypeMismatch(actual []Kind) int {
	for index, want := range r.constraints {
		if index >= len(actual) {
			break
		}
		if !compatible(want, actual[index]) {
			return index
		}
	}
	return -1
}
