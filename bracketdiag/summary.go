// Package bracketdiag 为可插入、删除、撤销的文本缓冲提供增量括号诊断。
//
// 设计要点：
//   - 文本按行切分为块，每行针对所有可能的词法进入状态预计算一份摘要，
//     因此字符串/注释/转义可以自然地跨块传播（转义跨块由 SEsc* 进入状态表达）。
//   - 每份摘要是一个可合并的 monoid（未匹配闭括号、未匹配开括号、不匹配错误事件），
//     查询时沿行折叠合并即可得到全文诊断。
//   - 摘要中的位置均为相对行首的偏移，编辑只重算内容变化的行，
//     后续行的摘要无需触碰。
package bracketdiag

// State 是块边界上的词法状态。
type State uint8

const (
	SNormal       State = iota // 普通代码
	SDouble                    // "..." 字符串内
	SSingle                    // '...' 字符串内
	SBacktick                  // `...` 字符串内
	SLineComment               // // 行注释内
	SBlockComment              // /* ... */ 块注释内
	SEscDouble                 // 字符串 " 内转义待续（下一字符被转义，可跨块）
	SEscSingle                 // 字符串 ' 内转义待续
	SEscBacktick               // 字符串 ` 内转义待续
	numStates
)

// DiagError 描述一条括号诊断。
type DiagError struct {
	Pos      int    // 错误的字节位置
	Expected string // 期待的符号（"" 表示无特定期待，如多余的闭括号）
	Found    string // 实际遇到的符号（"" 表示输入结束）
}

// bracketInfo 记录一个括号的位置与字符。
type bracketInfo struct {
	pos  int
	char byte
}

// monoid 是一段文本的可合并括号摘要。
// 不变式：close、open、errs 各自按位置升序。
type monoid struct {
	close []bracketInfo // 局部空栈时遇到的闭括号（多余闭括号候选）
	open  []bracketInfo // 段末仍留在栈上的开括号
	errs  []DiagError   // 闭括号与栈顶不匹配的错误事件
}

func closeOf(c byte) byte {
	switch c {
	case '(':
		return ')'
	case '[':
		return ']'
	case '{':
		return '}'
	}
	return 0
}

func isOpen(c byte) bool  { return c == '(' || c == '[' || c == '{' }
func isClose(c byte) bool { return c == ')' || c == ']' || c == '}' }

// mergeMonoid 合并两个相邻段的摘要（a 紧跟在 b 之前）。
// 不匹配恢复策略：记录错误，不弹栈、丢弃该闭括号，与朴素扫描保持一致。
func mergeMonoid(a, b monoid) monoid {
	res := monoid{
		open:  append([]bracketInfo(nil), a.open...),
		close: append([]bracketInfo(nil), a.close...),
		errs:  append([]DiagError(nil), a.errs...),
	}
	var cross []DiagError // b.close 与 a.open 栈顶的跨界不匹配，位置递增
	for _, c := range b.close {
		if n := len(res.open); n > 0 {
			top := res.open[n-1]
			if closeOf(top.char) == c.char {
				res.open = res.open[:n-1]
			} else {
				cross = append(cross, DiagError{
					Pos:      c.pos,
					Expected: string(closeOf(top.char)),
					Found:    string(c.char),
				})
			}
		} else {
			res.close = append(res.close, c)
		}
	}
	res.open = append(res.open, b.open...)
	res.errs = append(res.errs, mergeErrLists(cross, b.errs)...)
	return res
}

// mergeErrLists 按位置归并两个各自升序的错误列表。
func mergeErrLists(a, b []DiagError) []DiagError {
	out := make([]DiagError, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i].Pos <= b[j].Pos {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

// offsetMonoid 把段内相对位置平移 delta，得到绝对位置。
func offsetMonoid(m monoid, delta int) monoid {
	out := monoid{
		close: make([]bracketInfo, len(m.close)),
		open:  make([]bracketInfo, len(m.open)),
		errs:  make([]DiagError, len(m.errs)),
	}
	for i, c := range m.close {
		out.close[i] = bracketInfo{pos: c.pos + delta, char: c.char}
	}
	for i, o := range m.open {
		out.open[i] = bracketInfo{pos: o.pos + delta, char: o.char}
	}
	for i, e := range m.errs {
		e.Pos += delta
		out.errs[i] = e
	}
	return out
}

// segResult 是以某一进入状态扫描一个段的结果。
type segResult struct {
	exit    State  // 段末词法状态
	m       monoid // 括号摘要（位置相对段首）
	openPos int    // 退出状态所属构造（字符串/块注释）在段内的起始位置；-1 表示继承自前块或退出为普通态
}

// scanSegment 以 entry 为进入状态扫描 seg，产出退出状态与括号摘要。
// 这是唯一的词法扫描实现，行摘要与（测试中的）整体扫描共享同一语义。
func scanSegment(seg string, entry State) segResult {
	state := entry
	var m monoid
	var stack []bracketInfo
	constructStart := -1
	inherited := entry != SNormal // 进入时是否已处于某个跨块构造中

	for i := 0; i < len(seg); i++ {
		c := seg[i]
		switch state {
		case SNormal:
			switch {
			case c == '"':
				state, constructStart, inherited = SDouble, i, false
			case c == '\'':
				state, constructStart, inherited = SSingle, i, false
			case c == '`':
				state, constructStart, inherited = SBacktick, i, false
			case c == '/' && i+1 < len(seg) && seg[i+1] == '/':
				state = SLineComment
				i++
			case c == '/' && i+1 < len(seg) && seg[i+1] == '*':
				state, constructStart, inherited = SBlockComment, i, false
				i++
			case isOpen(c):
				stack = append(stack, bracketInfo{pos: i, char: c})
			case isClose(c):
				if len(stack) == 0 {
					m.close = append(m.close, bracketInfo{pos: i, char: c})
				} else if top := stack[len(stack)-1]; closeOf(top.char) == c {
					stack = stack[:len(stack)-1]
				} else {
					m.errs = append(m.errs, DiagError{
						Pos:      i,
						Expected: string(closeOf(top.char)),
						Found:    string(c),
					})
				}
			}
		case SDouble:
			if c == '\\' {
				state = SEscDouble
			} else if c == '"' {
				state = SNormal
			}
		case SSingle:
			if c == '\\' {
				state = SEscSingle
			} else if c == '\'' {
				state = SNormal
			}
		case SBacktick:
			if c == '\\' {
				state = SEscBacktick
			} else if c == '`' {
				state = SNormal
			}
		case SEscDouble: // 消费一个被转义的字符（可以是换行，即转义跨块）
			state = SDouble
		case SEscSingle:
			state = SSingle
		case SEscBacktick:
			state = SBacktick
		case SLineComment:
			if c == '\n' {
				state = SNormal
			}
		case SBlockComment:
			if c == '*' && i+1 < len(seg) && seg[i+1] == '/' {
				state = SNormal
				i++
			}
		}
	}

	openPos := -1
	if state != SNormal && state != SLineComment && !inherited {
		openPos = constructStart
	}
	return segResult{exit: state, m: monoid{close: m.close, open: stack, errs: m.errs}, openPos: openPos}
}

// lineSummary 是一行针对全部进入状态的预计算摘要表。
type lineSummary [numStates]segResult

// computeLineSummary 计算一行的完整摘要表。
func computeLineSummary(seg string) lineSummary {
	var ls lineSummary
	for st := State(0); st < numStates; st++ {
		ls[st] = scanSegment(seg, st)
	}
	return ls
}
