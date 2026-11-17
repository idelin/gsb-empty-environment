package netsec

import "sort"

// Certainty 区分危险写的确定程度。
type Certainty string

const (
	// Definite 表示该节点只要执行，接收对象在所有路径上都是共享原型 P。
	Definite Certainty = "definite"
	// Possible 是合并/弱更新下的保守结论：存在一条路径使接收对象为 P，
	// 但别名集合不能在所有路径上唯一确定为 P，可能是误报。
	Possible Certainty = "possible"
)

// DangerousWrite 是一个可达的共享原型写报告。
type DangerousWrite struct {
	// NodeID 是触发危险写的 Store 节点 ID。
	NodeID string `json:"node_id"`
	// Certainty 为 definite 或 possible。
	Certainty Certainty `json:"certainty"`
	// PossibleKeys 是键变量可能取到的全部字符串。
	PossibleKeys []string `json:"possible_keys"`
	// ReceiverTrace 是接收对象的别名数据流证据。
	ReceiverTrace []Evidence `json:"receiver_trace"`
	// KeyTrace 是键变量的字符串集合数据流证据。
	KeyTrace []Evidence `json:"key_trace"`
	// Reason 是人读的判定说明。
	Reason string `json:"reason"`
}

// Result 是 Analyze 的输出。
type Result struct {
	DangerousWrites []DangerousWrite `json:"dangerous_writes"`
}

type analyzer struct {
	prog    *Program
	reports map[string]*DangerousWrite
}

// Analyze 在执行前对受限脚本做原型污染静态分析：
// 传播变量别名（may/must 对象集合）与字符串集合，报告所有可达的
// 共享原型危险写。分支合并产生的保守结论标记为 possible。
func Analyze(prog *Program) (*Result, error) {
	if err := prog.Validate(); err != nil {
		return nil, err
	}
	a := &analyzer{prog: prog, reports: map[string]*DangerousWrite{}}
	st := NewState()
	a.execBlock(prog.Body, st)

	res := &Result{DangerousWrites: make([]DangerousWrite, 0, len(a.reports))}
	for _, r := range a.reports {
		res.DangerousWrites = append(res.DangerousWrites, *r)
	}
	sort.Slice(res.DangerousWrites, func(i, j int) bool {
		return res.DangerousWrites[i].NodeID < res.DangerousWrites[j].NodeID
	})
	return res, nil
}

func (a *analyzer) execBlock(block []Node, st *State) {
	for _, n := range block {
		a.exec(n, st)
	}
}

func (a *analyzer) exec(n Node, st *State) {
	switch t := n.(type) {
	case *Alloc:
		id := ObjID("alloc:" + t.ID)
		_, existed := st.Heap.Objs[id]
		o := st.Heap.Ensure(id)
		if existed {
			// 同一分配点在一条路径上重复执行（循环），抽象对象代表多个具体对象。
			o.Multi = true
		}
		st.Env[t.Dest] = singletonObj(id, Evidence{
			NodeID: t.ID, Var: t.Dest, Desc: "object allocation (default proto is shared proto)",
		})

	case *Const:
		v := Value{
			MayObjs:    map[ObjID]bool{},
			MustObjs:   map[ObjID]bool{},
			MayStrings: map[string]bool{t.Value: true},
		}
		st.Env[t.Dest] = v.WithEvidence(Evidence{
			NodeID: t.ID, Var: t.Dest, Desc: "string constant \"" + t.Value + "\"",
		})

	case *External:
		v := Value{MayObjs: map[ObjID]bool{}, MustObjs: map[ObjID]bool{}, MayStrings: map[string]bool{}}
		for _, s := range AllowedStrings {
			v.MayStrings[s] = true
		}
		st.Env[t.Dest] = v.WithEvidence(Evidence{
			NodeID: t.ID, Var: t.Dest, Desc: "external string may be any of the five allowed strings",
		})

	case *Alias:
		st.Env[t.Dest] = st.Lookup(t.Src).WithEvidence(Evidence{
			NodeID: t.ID, Var: t.Dest, Desc: "alias of " + t.Src,
		})

	case *Load:
		st.Env[t.Dest] = a.execLoad(t, st)

	case *Store:
		a.execStore(t, st)

	case *If:
		thenSt := st.Copy()
		a.execBlock(t.Then, thenSt)
		if len(t.Else) == 0 {
			// 条件未知：合并"未进入分支"的零次路径。
			merged := st.Join(thenSt)
			*st = *merged
		} else {
			elseSt := st.Copy()
			a.execBlock(t.Else, elseSt)
			merged := thenSt.Join(elseSt)
			*st = *merged
		}

	case *Loop:
		// Count 是精确有界值（<=16），逐次精确展开即可；
		// 循环内重复命中的分配点会置 Multi，其字段/原型写入退化为弱更新。
		for i := 0; i < t.Count; i++ {
			a.execBlock(t.Body, st)
		}
	}
}

func singletonObj(id ObjID, ev Evidence) Value {
	return Value{
		MayObjs:    map[ObjID]bool{id: true},
		MustObjs:   map[ObjID]bool{id: true},
		MayStrings: map[string]bool{},
		Evidence:   []Evidence{ev},
	}
}

func (a *analyzer) execLoad(n *Load, st *State) Value {
	recv := st.Lookup(n.Obj)
	keyV := st.Lookup(n.Key)
	out := BottomValue()
	first := true
	for id := range recv.MayObjs {
		for key := range keyV.MayStrings {
			v := a.resolveOne(st, id, key, map[ObjID]bool{})
			if first {
				out = v
				first = false
			} else {
				out = out.Join(v)
			}
		}
	}
	return out.WithEvidence(Evidence{
		NodeID: n.ID,
		Var:    n.Dest,
		Desc:   "field load from " + n.Obj + " keyed by " + n.Key,
	})
}

// resolveOne 在单个接收对象上解析一次字段读取，沿原型链回溯。
// visited 防止 __proto__ 改写后出现的原型环导致无限递归。
func (a *analyzer) resolveOne(st *State, id ObjID, key string, visited map[ObjID]bool) Value {
	if visited[id] {
		return BottomValue()
	}
	visited[id] = true
	o := st.Heap.Ensure(id)

	if key == StrProto {
		return o.Protos.WithEvidence(Evidence{
			NodeID: string(id), Var: StrProto, Desc: "__proto__ read",
		})
	}
	if fv, ok := o.Fields[key]; ok {
		return fv.WithEvidence(Evidence{
			NodeID: string(id), Var: key, Desc: "own field read",
		})
	}
	if key == StrConstructor {
		// 自身无 constructor 字段时，读到固定构造对象 C。
		return singletonObj(ConstructorID, Evidence{
			NodeID: string(id), Var: StrConstructor, Desc: "missing field resolves to fixed constructor",
		})
	}
	// 自身未定义：沿可能原型集合继续查找（may 并集 / must 交集）。
	out := BottomValue()
	first := true
	for pid := range o.Protos.MayObjs {
		v := a.resolveOne(st, pid, key, visited)
		if first {
			out = v
			first = false
		} else {
			out = out.Join(v)
		}
	}
	return out
}

func (a *analyzer) execStore(n *Store, st *State) {
	recv := st.Lookup(n.Obj)
	keyV := st.Lookup(n.Key)
	val := st.Lookup(n.Val)
	if len(recv.MayObjs) == 0 || len(keyV.MayStrings) == 0 {
		return
	}
	keys := make([]string, 0, len(keyV.MayStrings))
	for k := range keyV.MayStrings {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	_, protoPossible := keyV.MayStrings[StrProto]
	normalKeys := make([]string, 0, len(keys))
	for _, k := range keys {
		if k != StrProto {
			normalKeys = append(normalKeys, k)
		}
	}

	for id := range recv.MayObjs {
		o := st.Heap.Ensure(id)
		singleRecv := len(recv.MayObjs) == 1

		if protoPossible {
			// __proto__ 写：赋入对象可能为 P 即危险（P 是真实接收对象）。
			if val.MayObjs[SharedProtoID] {
				cert := Possible
				if len(val.MustObjs) == 1 && val.MustObjs[SharedProtoID] {
					cert = Definite
				}
				a.report(n, cert, keys, recv, keyV)
			}
			strong := singleRecv && !o.Multi && len(keys) == 1
			a.writeProto(o, val, strong, n.ID)
		}
		for _, key := range normalKeys {
			// 普通字段写只有接收对象本身为 P 才危险。独立对象（含原型
			// 指向 P 的对象）缺失字段写在自身建立字段，不沿链污染。
			if id == SharedProtoID {
				cert := Possible
				if len(recv.MustObjs) == 1 && recv.MustObjs[SharedProtoID] {
					cert = Definite
				}
				a.report(n, cert, keys, recv, keyV)
			}
			// 写固定构造对象的 prototype 字段等价改写共享原型引用，按危险处理。
			if id == ConstructorID && key == StrPrototype {
				cert := Possible
				if len(recv.MustObjs) == 1 && recv.MustObjs[ConstructorID] {
					cert = Definite
				}
				a.report(n, cert, keys, recv, keyV)
			}
			strong := singleRecv && !o.Multi && len(keys) == 1
			a.writeField(o, key, val, strong, n.ID)
		}
	}
}

func (a *analyzer) writeField(o *objState, key string, val Value, strong bool, nodeID string) {
	v := val.WithEvidence(Evidence{
		NodeID: nodeID, Var: key, Desc: "field write",
	})
	if strong {
		o.Fields[key] = v
		return
	}
	if old, ok := o.Fields[key]; ok {
		o.Fields[key] = old.Join(v)
	} else {
		o.Fields[key] = v
	}
}

func (a *analyzer) writeProto(o *objState, val Value, strong bool, nodeID string) {
	target := Value{
		MayObjs:    map[ObjID]bool{},
		MustObjs:   map[ObjID]bool{},
		MayStrings: map[string]bool{},
		Evidence:   append([]Evidence(nil), val.Evidence...),
	}
	for id := range val.MayObjs {
		target.MayObjs[id] = true
	}
	for id := range val.MustObjs {
		target.MustObjs[id] = true
	}
	target = target.WithEvidence(Evidence{
		NodeID: nodeID, Var: StrProto, Desc: "__proto__ write changes receiver prototype",
	})
	if strong {
		o.Protos = target
	} else {
		o.Protos = o.Protos.Join(target)
	}
}

func (a *analyzer) report(n *Store, cert Certainty, keys []string, recv, keyV Value) {
	r, ok := a.reports[n.ID]
	if !ok {
		r = &DangerousWrite{
			NodeID:        n.ID,
			Certainty:     cert,
			PossibleKeys:  append([]string(nil), keys...),
			ReceiverTrace: append([]Evidence(nil), recv.Evidence...),
			KeyTrace:      append([]Evidence(nil), keyV.Evidence...),
			Reason:        "reachable write whose receiver may be the shared prototype",
		}
		a.reports[n.ID] = r
		return
	}
	if cert == Definite {
		r.Certainty = Definite
	}
}
