package netsec

// parse 按 Go fmt 的说明符结构逐段扫描格式串。
func (r *Result) parse(format string) {
	end := len(format)
	seq := 0 // 顺序模式下的下一个参数序号

	for i := 0; i < end; {
		// 跳过普通文本。
		for i < end && format[i] != '%' {
			i++
		}
		if i >= end {
			return
		}
		i++ // 跳过 '%'
		if i >= end {
			r.diag(DiagMalformed, "格式串以悬空的 % 结尾，缺少转换说明符")
			return
		}
		if format[i] == '%' {
			i++ // 字面量 %%，不消费参数
			continue
		}

		// 标志位：# 0 + - 空格
		for i < end {
			switch format[i] {
			case '#', '0', '+', '-', ' ':
				i++
			default:
				goto afterFlags
			}
		}
	afterFlags:

		// 与 fmt 一致：进入宽度前先尝试一次索引解析（如 %[1]*）。
		verbIndex, hasVerbIndex, afterIndex, proceed := r.tryIndex(format, &i, end)
		if !proceed {
			return
		}

		// 宽度：[n]* | * | 数字。入口 argNumber 解析出的 [n] 若紧跟 *，
		// 按 fmt 语义属于动态宽度而非动词索引。
		widthExplicit, widthStar := -1, false
		if i < end && format[i] == '*' {
			if afterIndex {
				// 入口解析到的 [n] 与这个 * 配对，属于动态宽度。
				widthExplicit = verbIndex
				hasVerbIndex = false
			}
			i++
			widthStar = true
			afterIndex = false
		} else if i < end && format[i] == '[' {
			idx, found, _, proceed := r.tryIndex(format, &i, end)
			if !proceed {
				return
			}
			if !found || i >= end || format[i] != '*' {
				r.diag(DiagMalformed, "显式宽度索引后缺少 *")
				i = resync(format, i)
				continue
			}
			widthExplicit = idx
			i++
			widthStar = true
		} else {
			numStart := i
			for i < end && format[i] >= '0' && format[i] <= '9' {
				i++
			}
			if afterIndex && i > numStart {
				r.diag(DiagBadIndex, "显式索引后不能再跟数字宽度")
			}
		}

		i, seq = r.parseRest(format, i, end, seq, verbIndex, hasVerbIndex, afterIndex, widthExplicit, widthStar)
		if i < 0 {
			return
		}
	}
}
