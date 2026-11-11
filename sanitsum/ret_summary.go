package sanitsum

// RetSummary 是单个返回值下的必然净化集合。
// 映射值表示“到达该返回值时，对应槽位上已经被必然清除的污染类别”。
type RetSummary struct {
	ParamClean  map[string]CatSet // 入参变量 -> 已净化类别
	ResultClean map[string]CatSet // 结果变量 -> 已净化类别
	FieldClean  map[Ref]CatSet    // 副作用字段 -> 已净化类别
}

// Summary 是库函数在所有返回值上的净化摘要。
type Summary struct {
	Func       string
	Categories []Category
	Params     []string
	Results    []string
	Returns    map[RetVal]*RetSummary
}

// Equal 比较两份摘要是否逐返回值、逐槽位一致（类别位集直接按位比较）。
func (m *Summary) Equal(o *Summary) bool {
	if m == nil || o == nil {
		return m == o
	}
	if m.Func != o.Func || len(m.Returns) != len(o.Returns) {
		return false
	}
	if !sameCategories(m.Categories, o.Categories) {
		return false
	}
	for rv, rm := range m.Returns {
		om, ok := o.Returns[rv]
		if !ok || !rm.equal(om) {
			return false
		}
	}
	return true
}

func (r *RetSummary) equal(o *RetSummary) bool {
	return catMapEqual(r.ParamClean, o.ParamClean) &&
		catMapEqual(r.ResultClean, o.ResultClean) &&
		fieldMapEqual(r.FieldClean, o.FieldClean)
}

func catMapEqual(a, b map[string]CatSet) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || !va.Equals(vb) {
			return false
		}
	}
	return true
}

func fieldMapEqual(a, b map[Ref]CatSet) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || !va.Equals(vb) {
			return false
		}
	}
	return true
}

func sameCategories(a, b []Category) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
