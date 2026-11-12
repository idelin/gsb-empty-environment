package timeparse

import (
	"regexp"
	"sort"
	"strings"
)

// 数字捕获组布局：
//
//	reFull:   (年)(月)(日)
//	reNumMD:  (月)(日)
//	reTime24: (时)(分)(秒?)
//	reTime12: (时段?)(时)(冒号分?)(冒号秒?)(点/时)(点后分?)(点后秒?)
var (
	reFull = regexp.MustCompile(
		`(\d{4})\s*[年\-/.]\s*(\d{1,2})\s*[月\-/.]\s*(\d{1,2})\s*日?`)

	reNumMD = regexp.MustCompile(
		`(?:^|[^\d])(\d{1,2})\s*[月\-/.]\s*(\d{1,2})\s*日?`)

	reTime24 = regexp.MustCompile(
		`(?:^|[^\d])(\d{1,2})\s*[:：]\s*(\d{1,2})(?:\s*[:：]\s*(\d{1,2}))?\s*(?:点|时)?整?`)

	reTime12 = regexp.MustCompile(
		`(凌晨|早上|上午|中午|下午|晚上|晚间)?\s*(\d{1,2})\s*(?:[:：]\s*(\d{1,2})\s*分?)?\s*(?:[:：]\s*(\d{1,2})\s*秒?)?\s*(点|时)\s*(?:(\d{1,2})\s*分)?\s*(?:(\d{1,2})\s*秒)?\s*整?`)
)

var periodAM = map[string]bool{
	"凌晨": true,
	"早上": true,
	"上午": true,
}

// extract 在文本中收集互不重叠的片段。完整日期优先于裸月日，
// 时钟表达式之间按起始位置先到先得，避免同一串数字被两个时钟正则重复命中。
func extract(text string, rules Rules) []Fragment {
	type span struct{ start, end int }
	covered := map[span]bool{}

	overlaps := func(start, end int) bool {
		for s := range covered {
			if start < s.end && s.start < end {
				return true
			}
		}
		return false
	}

	var fragments []Fragment

	// 1. 带年份的完整日期，优先级最高。
	for _, m := range reFull.FindAllStringSubmatchIndex(text, -1) {
		frag := Fragment{
			Kind:  KindFullDate,
			Text:  text[m[0]:m[1]],
			Start: m[0],
			End:   m[1],
			Year:  atoi(text[m[2]:m[3]]),
			Month: atoi(text[m[4]:m[5]]),
			Day:   atoi(text[m[6]:m[7]]),
		}
		if frag.Month < 1 || frag.Month > 12 || frag.Day < 1 || frag.Day > 31 {
			continue
		}
		covered[span{frag.Start, frag.End}] = true
		fragments = append(fragments, frag)
	}

	// 2. 裸月日，跳过与完整日期重叠的命中。
	for _, m := range reNumMD.FindAllStringSubmatchIndex(text, -1) {
		// m[0]:m[1] 含前导非数字字符与可选的末尾“日”，
		// 片段本体从月份分组 m[2] 开始，到整体匹配结尾 m[1] 结束。
		start, end := m[2], m[1]
		for end > start && text[end-1] == ' ' {
			end--
		}
		frag := Fragment{
			Kind:  KindMonthDay,
			Start: start,
			End:   end,
			Month: atoi(text[m[2]:m[3]]),
			Day:   atoi(text[m[4]:m[5]]),
		}
		frag.Text = text[start:end]
		if frag.Month < 1 || frag.Month > 12 || frag.Day < 1 || frag.Day > 31 {
			continue
		}
		if overlaps(start, end) {
			continue
		}
		covered[span{start, end}] = true
		fragments = append(fragments, frag)
	}

	// 3. 时钟表达式，24 小时制与 12 小时制统一走时钟归一化。
	for _, m := range reTime24.FindAllStringSubmatchIndex(text, -1) {
		start, end := m[2], m[1]
		if overlaps(start, end) {
			continue
		}
		frag := Fragment{
			Kind:   KindTime,
			Text:   text[start:end],
			Start:  start,
			End:    end,
			Hour:   atoi(text[m[2]:m[3]]),
			Minute: groupOrZero(text, m, 4),
			Second: groupOrZero(text, m, 6),
		}
		if frag.Hour > 23 || frag.Minute > 59 || frag.Second > 59 {
			continue
		}
		covered[span{start, end}] = true
		fragments = append(fragments, frag)
	}

	for _, m := range reTime12.FindAllStringSubmatchIndex(text, -1) {
		start, end := m[2], m[1]
		if overlaps(start, end) {
			continue
		}
		frag := Fragment{
			Kind:   KindTime,
			Text:   text[start:end],
			Start:  start,
			End:    end,
			Period: groupOrEmpty(text, m, 2),
			Hour:   atoi(text[m[4]:m[5]]),
		}
		// 冒号形式（3点:30）与“X点Y分Z秒”形式二选一，冒号分组优先。
		frag.Minute = groupOrZero(text, m, 6)
		if m[6] == -1 {
			frag.Minute = groupOrZero(text, m, 12)
		}
		frag.Second = groupOrZero(text, m, 8)
		if m[8] == -1 {
			frag.Second = groupOrZero(text, m, 14)
		}
		period := frag.Period
		if m[10] != -1 {
			period = text[m[10]:m[11]]
		}
		frag.Period = normalizePeriod(frag.Period, period)
		hour, ok := normalizeHour(frag.Hour, frag.Period)
		if !ok || frag.Minute > 59 || frag.Second > 59 {
			continue
		}
		frag.Hour = hour
		covered[span{start, end}] = true
		fragments = append(fragments, frag)
	}

	// 4. 相对日期词与瞬间词，只接受未被数字片段覆盖的命中。
	wordOffset := map[string]int{}
	for word, off := range rules.relativeWords() {
		wordOffset[word] = off
	}

	emitWord := func(word string, instant bool, off int) {
		for _, idx := range allIndex(text, word) {
			start, end := idx, idx+len(word)
			if overlaps(start, end) {
				continue
			}
			frag := Fragment{
				Kind:    KindRelative,
				Text:    word,
				Start:   start,
				End:     end,
				Offset:  off,
				Instant: instant,
			}
			covered[span{start, end}] = true
			fragments = append(fragments, frag)
		}
	}

	for _, word := range rules.instantWords() {
		if _, ok := wordOffset[word]; ok {
			continue
		}
		emitWord(word, true, 0)
	}

	// 相对词按长度降序，确保“昨日”与任何更长词的包含关系都先匹配长词。
	words := make([]string, 0, len(wordOffset))
	for word := range wordOffset {
		words = append(words, word)
	}
	sort.Slice(words, func(i, j int) bool { return len(words[i]) > len(words[j]) })
	for _, word := range words {
		emitWord(word, false, wordOffset[word])
	}

	sort.Slice(fragments, func(i, j int) bool {
		if fragments[i].Start != fragments[j].Start {
			return fragments[i].Start < fragments[j].Start
		}
		return fragments[i].End > fragments[j].End
	})
	return fragments
}

// normalizeHour 把 12 小时制的钟点结合中文时段换算为 24 小时制。
func normalizeHour(hour int, period string) (int, bool) {
	if hour < 0 || hour > 12 {
		return 0, false
	}
	switch {
	case periodAM[period]:
		if hour == 12 {
			hour = 0
		}
	case period == "下午" || period == "晚上" || period == "晚间":
		if hour != 12 {
			hour += 12
		}
	case period == "中午":
		if hour < 6 {
			hour += 12
		}
	}
	if hour < 0 || hour > 23 {
		return 0, false
	}
	return hour, true
}

// normalizePeriod 合并前缀时段（下午/晚上等）与后缀“午”字信息。
func normalizePeriod(prefix, suffix string) string {
	if prefix != "" {
		return prefix
	}
	if strings.Contains(suffix, "下") {
		return "下午"
	}
	return prefix
}

func allIndex(text, sub string) []int {
	if sub == "" {
		return nil
	}
	var out []int
	for start := 0; start+len(sub) <= len(text); {
		idx := strings.Index(text[start:], sub)
		if idx < 0 {
			return out
		}
		out = append(out, start+idx)
		start += idx + len(sub)
	}
	return out
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			continue
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func groupOrZero(text string, m []int, pair int) int {
	if m[pair] == -1 {
		return 0
	}
	return atoi(text[m[pair]:m[pair+1]])
}

func groupOrEmpty(text string, m []int, pair int) string {
	if m[pair] == -1 {
		return ""
	}
	return text[m[pair]:m[pair+1]]
}
