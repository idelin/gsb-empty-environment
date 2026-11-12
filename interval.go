package taskpkg

// Interval 是对某份字节切片的半开区间 [Start, End)。
//
// 所有区间都相对于传入 Parser 的那份字节切片：基线评估时相对于原始请求体，
// 变异评估时相对于变异后的请求体。引擎不跨字节版本平移区间，而是让两侧解析器
// 在同一份字节上各自独立产出区间。
type Interval struct {
	Start int
	End   int
}

// DuplicatePolicy 描述解析器对同名字段（含同名字段名或重复字段）的取舍策略。
type DuplicatePolicy int

const (
	// PolicyUnknown 表示解析器未声明重复项取舍。
	PolicyUnknown DuplicatePolicy = iota
	// PolicyFirst 表示保留首次出现的字段，忽略后续重复项。
	PolicyFirst
	// PolicyLast 表示保留最后一次出现的字段，覆盖先前重复项。
	PolicyLast
	// PolicyAll 表示保留全部重复项（例如合并为数组或逐一处理）。
	PolicyAll
)

// String 返回重复项取舍策略的可读名称。
func (p DuplicatePolicy) String() string {
	switch p {
	case PolicyFirst:
		return "first"
	case PolicyLast:
		return "last"
	case PolicyAll:
		return "all"
	default:
		return "unknown"
	}
}

// FieldOccurrence 记录一次字段出现的解析结果。
type FieldOccurrence struct {
	// Name 是字段名（表单字段名或部件名）。
	Name string
	// Raw 是该字段出现（含取值/附件内容）在当前字节切片中的原始区间。
	Raw Interval
	// KeptOrdinal 是该字段名第几次出现（0 起）；与 Policy 配合说明重复项取舍。
	KeptOrdinal int
	// Kept 表示按本侧策略该出现是否被实际保留/使用。
	Kept bool
}

// Entry 是一侧解析器实际关注的执行入口或审核入口。
type Entry struct {
	// Name 是入口关联的字段名（例如被审核/被执行的附件字段名）。
	Name string
	// Policy 是本侧对该字段重复项的取舍策略。
	Policy DuplicatePolicy
	// Chosen 是入口选中的字段出现；Raw 为其原始区间。
	Chosen FieldOccurrence
	// DependOn 是入口最终行为所依赖的全部原始区间。
	//
	// 后端侧通常就是被执行附件的字节区间；网关侧就是实际送审的字节区间。
	// 两侧都通过该字段表达覆盖关系，以便引擎做统一的独立展开与覆盖比较。
	DependOn []Interval
}

// Report 是一侧解析器对一份请求字节的完整解析回报。
type Report struct {
	// Fields 是全部字段出现，按解析顺序给出，包含被重复项策略丢弃的出现。
	Fields []FieldOccurrence
	// Entries 是本侧实际使用的入口（网关为审核入口，后端为执行入口）。
	Entries []Entry
	// Audited 是网关侧实际送审字节的原始区间；后端侧留空即可。
	Audited []Interval
	// Rejected 为 true 时表示本侧按 fail-closed 拒绝该请求（不构成绕过）。
	Rejected bool
}

// Parser 是测试宿主提供的单侧解析回调。
//
// body 是当前评估使用的完整请求字节（基线或变异后的样本）。返回的所有区间都必须
// 指向 body 的合法半开区间。回调返回非 nil error 表示该侧无法评估，整个样本判定
// 为出错而不是绕过。
type Parser func(body []byte) (*Report, error)

// ExpandIntervals 独立展开并归并一组半开区间。
//
// 该函数不依赖任何另一侧的结果或共享缓存：传入哪个侧的区间就只处理哪个侧。
// 重叠与相邻（[0,5) 与 [5,9)）的区间会合并为单个区间，输出按 Start 升序、互不重叠。
// 输入为空时返回 nil；非法区间（Start<0、End<Start）返回错误。
func ExpandIntervals(ivs []Interval) ([]Interval, error) {
	if len(ivs) == 0 {
		return nil, nil
	}
	work := make([]Interval, len(ivs))
	copy(work, ivs)
	for i := range work {
		if work[i].Start < 0 || work[i].End < work[i].Start {
			return nil, &IntervalError{Bad: work[i]}
		}
	}
	sortIntervals(work)

	merged := make([]Interval, 0, len(work))
	cur := work[0]
	for _, next := range work[1:] {
		if next.Start <= cur.End {
			if next.End > cur.End {
				cur.End = next.End
			}
			continue
		}
		merged = append(merged, cur)
		cur = next
	}
	merged = append(merged, cur)
	return merged, nil
}

// IntervalError 表示回调返回了指向字节切片之外或方向非法的区间。
type IntervalError struct {
	Bad Interval
}

func (e *IntervalError) Error() string {
	return "taskpkg: illegal interval [" + itoa(e.Bad.Start) + "," + itoa(e.Bad.End) + ")"
}

// CoverGap 返回 target 中没有被 cover 完整覆盖的残余区间（两者均视为已归并区间集）。
//
// cover 与 target 都会先各自独立归并，再做双指针扫描。返回 nil 表示 cover 完整覆盖
// target；返回非 nil 即存在未覆盖字节，残余区间按升序给出且互不重叠。
// 该判定只关心字节覆盖，不关心字段出现顺序，因此普通字段排序差异不会产生缺口。
func CoverGap(cover, target []Interval) ([]Interval, error) {
	coverM, err := ExpandIntervals(cover)
	if err != nil {
		return nil, err
	}
	targetM, err := ExpandIntervals(target)
	if err != nil {
		return nil, err
	}

	var gaps []Interval
	ci := 0
	for _, t := range targetM {
		pos := t.Start
		for pos < t.End {
			for ci < len(coverM) && coverM[ci].End <= pos {
				ci++
			}
			if ci >= len(coverM) || coverM[ci].Start > pos {
				gapEnd := t.End
				if ci < len(coverM) && coverM[ci].Start < gapEnd {
					gapEnd = coverM[ci].Start
				}
				gaps = append(gaps, Interval{Start: pos, End: gapEnd})
				pos = gapEnd
				continue
			}
			pos = coverM[ci].End
		}
	}
	return gaps, nil
}

// ContainsAll 报告 cover 是否完整覆盖 target。
func ContainsAll(cover, target []Interval) (bool, error) {
	gaps, err := CoverGap(cover, target)
	if err != nil {
		return false, err
	}
	return len(gaps) == 0, nil
}

func sortIntervals(ivs []Interval) {
	// 区间数量很小（单次解析的字段/入口数量级），插入排序足够且无额外依赖。
	for i := 1; i < len(ivs); i++ {
		for j := i; j > 0 && (ivs[j].Start < ivs[j-1].Start ||
			(ivs[j].Start == ivs[j-1].Start && ivs[j].End < ivs[j-1].End)); j-- {
			ivs[j], ivs[j-1] = ivs[j-1], ivs[j]
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
