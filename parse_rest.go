package netsec

import "unicode/utf8"

// parseRest 处理精度、动词前索引与动词，并完成参数消费登记。
// 返回新的扫描位置；返回 -1 表示整个格式串扫描结束。
func (r *Result) parseRest(format string, i, end, seq int, verbIndex int, hasVerbIndex, afterIndex bool, widthExplicit int, widthStar bool) (int, int) {
	// 精度：.([n]* | * | 数字)
	precExplicit, precStar, precPresent := -1, false, false
	if i < end && format[i] == '.' {
		if afterIndex {
			r.diag(DiagBadIndex, "显式索引后不能直接接精度")
		}
		i++
		precPresent = true
		switch {
		case i < end && format[i] == '[':
			idx, found, _, proceed := r.tryIndex(format, &i, end)
			if !proceed {
				return -1, seq
			}
			if !found {
				return resync(format, i), seq
			}
			precExplicit = idx
			if i >= end || format[i] != '*' {
				r.diag(DiagMalformed, "显式精度索引后缺少 *")
				return resync(format, i), seq
			}
			i++
			precStar = true
			afterIndex = false
		case i < end && format[i] == '*':
			i++
			precStar = true
			afterIndex = false
		default:
			for i < end && format[i] >= '0' && format[i] <= '9' {
				i++
			}
		}
	}

	// 无前置索引时，动词前仍可出现 [n]。
	if !afterIndex {
		idx, found, after, proceed := r.tryIndex(format, &i, end)
		if !proceed {
			return -1, seq
		}
		if found {
			verbIndex = idx
			hasVerbIndex = true
			afterIndex = after
		}
	}

	if i >= end {
		r.diag(DiagMalformed, "转换说明符缺少动词")
		return -1, seq
	}
	verb, size := utf8.DecodeRuneInString(format[i:])
	i += size

	if verb == '%' {
		// 带宽度/精度标记的 %% 不消费参数。
		return i, seq
	}

	valueKind := verbConstraint(verb)
	truncating := precPresent && valueKind == KindString

	// 动态宽度先于精度、值参数消费。
	if widthStar {
		if widthExplicit >= 0 {
			r.consume(widthExplicit, true, RoleWidth, verb, KindInt, false)
		} else {
			r.consume(seq, false, RoleWidth, verb, KindInt, false)
			seq++
		}
	}
	if precStar {
		if precExplicit >= 0 {
			r.consume(precExplicit, true, RolePrecision, verb, KindInt, false)
		} else {
			r.consume(seq, false, RolePrecision, verb, KindInt, false)
			seq++
		}
	}
	if hasVerbIndex {
		r.consume(verbIndex, true, RoleValue, verb, valueKind, truncating)
	} else {
		r.consume(seq, false, RoleValue, verb, valueKind, truncating)
		seq++
	}
	return i, seq
}
