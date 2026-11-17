package netsec

import "sort"

// ObjID 标识抽象堆对象。
type ObjID string

const (
	// SharedProtoID 是所有对象默认共享的原型对象。
	// 向它的任意字段写入都是原型污染。
	SharedProtoID ObjID = "<shared-proto>"
	// ConstructorID 是固定构造对象；其 prototype 字段初始指回共享原型。
	ConstructorID ObjID = "<constructor>"
)

// Evidence 是一条数据流证据，记录某个变量值在节点 NodeID 处的来源。
// 同一值携带的证据按来源链顺序排列，用于向调用方解释别名与字符串集合
// 是如何传播到危险写接收对象/键上的。
type Evidence struct {
	NodeID string `json:"node_id"`
	Var    string `json:"var"`
	Desc   string `json:"desc"`
}

// Value 是变量或字段的抽象值：
//   - MayObjs: 变量可能指向的抽象对象（may 别名集合，幂集域）；
//   - MustObjs: 变量在所有到达当前程序点的路径上都指向的对象，
//     用于区分"确定危险"与"可能危险"；
//   - MayStrings: 变量可能取到的字符串集合；
//   - Evidence: 支撑上述集合的数据流证据。
//
// 对象与字符串可以同时出现（外部输入、未知字段等场景下值的类型不确定）。
type Value struct {
	MayObjs    map[ObjID]bool
	MustObjs   map[ObjID]bool
	MayStrings map[string]bool
	Evidence   []Evidence
}

// BottomValue 是空值（未定义变量 / 读取不存在的字段）。
func BottomValue() Value {
	return Value{
		MayObjs:    map[ObjID]bool{},
		MustObjs:   map[ObjID]bool{},
		MayStrings: map[string]bool{},
	}
}

// IsBottom 报告值是否既不含对象也不含字符串。
func (v Value) IsBottom() bool {
	return len(v.MayObjs) == 0 && len(v.MayStrings) == 0
}

// Copy 返回深拷贝，避免分析中不同程序点共享可变 map。
func (v Value) Copy() Value {
	c := Value{
		MayObjs:    make(map[ObjID]bool, len(v.MayObjs)),
		MustObjs:   make(map[ObjID]bool, len(v.MustObjs)),
		MayStrings: make(map[string]bool, len(v.MayStrings)),
		Evidence:   append([]Evidence(nil), v.Evidence...),
	}
	for id := range v.MayObjs {
		c.MayObjs[id] = true
	}
	for id := range v.MustObjs {
		c.MustObjs[id] = true
	}
	for s := range v.MayStrings {
		c.MayStrings[s] = true
	}
	return c
}

// Join 是幂集域的合并：may 取并集，must 取交集。
// bottom 是合并单位元。证据去重后按 (节点,变量,描述) 稳定排序。
func (v Value) Join(other Value) Value {
	out := BottomValue()
	for id := range v.MayObjs {
		out.MayObjs[id] = true
	}
	for id := range other.MayObjs {
		out.MayObjs[id] = true
	}
	for id := range v.MustObjs {
		if other.MustObjs[id] {
			out.MustObjs[id] = true
		}
	}
	for s := range v.MayStrings {
		out.MayStrings[s] = true
	}
	for s := range other.MayStrings {
		out.MayStrings[s] = true
	}
	out.Evidence = mergeEvidence(v.Evidence, other.Evidence)
	return out
}

// WithEvidence 返回追加单条证据的值（证据在该值来源链的最前面）。
func (v Value) WithEvidence(e Evidence) Value {
	c := v.Copy()
	c.Evidence = append([]Evidence{e}, c.Evidence...)
	c.Evidence = dedupEvidence(c.Evidence)
	return c
}

func mergeEvidence(a, b []Evidence) []Evidence {
	return dedupEvidence(append(append([]Evidence(nil), a...), b...))
}

func dedupEvidence(in []Evidence) []Evidence {
	type key struct {
		n, v, d string
	}
	seen := map[key]bool{}
	out := make([]Evidence, 0, len(in))
	for _, e := range in {
		k := key{e.NodeID, e.Var, e.Desc}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
		if len(out) >= 32 {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID < out[j].NodeID
		}
		if out[i].Var != out[j].Var {
			return out[i].Var < out[j].Var
		}
		return out[i].Desc < out[j].Desc
	})
	return out
}
