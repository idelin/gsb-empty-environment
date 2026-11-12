package netsec

// parseBracket 在 format[i:] 开头解析可选的 [n]。返回零基索引 n-1、消耗
// 字节数以及是否命中；括号或数字非法时 ok 为 false。
func parseBracket(format string, i, end int) (index, wid int, ok bool) {
	if i >= end || format[i] != '[' {
		return 0, 0, false
	}
	j := i + 1
	if j >= end || format[j] < '0' || format[j] > '9' {
		return 0, 1, false
	}
	num := 0
	for j < end && format[j] >= '0' && format[j] <= '9' {
		num = num*10 + int(format[j]-'0')
		j++
	}
	if j >= end || format[j] != ']' {
		// 括号未闭合：这不是合法索引，wid 仍推进到已扫描处便于恢复。
		return 0, j - i, false
	}
	return num - 1, j - i + 1, true
}

// resync 在不合法的说明符后重新定位到下一个 '%'，保证尽力解析后续内容。
func resync(format string, i int) int {
	for i < len(format) && format[i] != '%' {
		i++
	}
	return i
}

// tryIndex 对应 fmt 的 argNumber：若当前位置是合法 [n] 则消费它并返回
// 零基索引；若是非法括号则登记诊断并通过返回的 i<0 通知调用方终止。
func (r *Result) tryIndex(format string, i *int, end int) (index int, found, after bool, ok bool) {
	if idx, wid, hit := parseBracket(format, *i, end); hit {
		if idx < 0 {
			r.diag(DiagBadIndex, "显式参数索引必须从 1 开始")
			*i = -1
			return 0, false, false, false
		}
		*i += wid
		return idx, true, true, true
	} else if *i < end && format[*i] == '[' {
		r.diag(DiagMalformed, "显式参数索引不合法或括号未闭合")
		*i = -1
		return 0, false, false, false
	}
	return -1, false, false, true
}

// consume 登记一次参数消费，并维护模式混用、重复位置与类型约束状态。
func (r *Result) consume(index int, explicit bool, role Role, verb rune, kind Kind, truncating bool) {
	repeated := false
	if explicit {
		if !r.ExplicitMode {
			if r.sequentialCount > 0 {
				r.MixedModes = true
				r.diag(DiagMixedModes, "格式串混用了顺序位置与显式位置")
			}
			r.ExplicitMode = true
		}
		repeated = r.seenExplicit[index]
		r.seenExplicit[index] = true
		if index > r.maxExplicit {
			r.maxExplicit = index
		}
	} else if r.ExplicitMode {
		r.MixedModes = true
		r.diag(DiagMixedModes, "格式串混用了显式位置与顺序位置")
	}

	if !explicit {
		r.sequentialCount++
	}

	for len(r.constraints) <= index {
		r.constraints = append(r.constraints, KindUnknown)
	}
	switch existing := r.constraints[index]; {
	case existing == KindUnknown:
		r.constraints[index] = kind
	case kind != KindUnknown && existing != kind && existing != KindAny && kind != KindAny:
		r.diag(DiagTypeConflict, "参数位置的类型约束互相冲突")
	}

	r.Consumptions = append(r.Consumptions, Consumption{
		Index:      index,
		Explicit:   explicit,
		Repeated:   repeated,
		Role:       role,
		Verb:       verb,
		Constraint: kind,
		Truncating: truncating,
	})
}
