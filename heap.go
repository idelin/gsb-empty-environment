package netsec

// objState 是单个抽象对象的状态。
//
//   - Fields: 自身字段名 -> 抽象值（may/must 集合）；
//   - Protos: 当前可能原型的 may/must 对象集合；
//   - Multi: 该分配点是否可能表示多个具体对象（循环中重复分配时置位），
//     置位后字段/原型写入退化为弱更新以保证不漏报。
type objState struct {
	Fields map[string]Value
	Protos Value
	Multi  bool
}

func newObjState() *objState {
	return &objState{Fields: map[string]Value{}}
}

func (o *objState) copy() *objState {
	c := &objState{Multi: o.Multi}
	c.Fields = make(map[string]Value, len(o.Fields))
	for k, v := range o.Fields {
		c.Fields[k] = v.Copy()
	}
	c.Protos = o.Protos.Copy()
	return c
}

// Heap 是抽象堆：ObjID -> 对象状态。
type Heap struct {
	Objs map[ObjID]*objState
}

// NewHeap 构建初始堆，包含共享原型 P 与固定构造对象 C：
// C.prototype 初始指向 P，P 自身没有原型，C 自身也没有原型。
func NewHeap() *Heap {
	h := &Heap{Objs: map[ObjID]*objState{}}

	proto := newObjState()
	proto.Protos = BottomValue()
	h.Objs[SharedProtoID] = proto

	ctor := newObjState()
	ctor.Protos = BottomValue()
	protoRef := Value{
		MayObjs:    map[ObjID]bool{SharedProtoID: true},
		MustObjs:   map[ObjID]bool{SharedProtoID: true},
		MayStrings: map[string]bool{},
		Evidence: []Evidence{{
			NodeID: string(ConstructorID),
			Var:    StrPrototype,
			Desc:   "builtin constructor.prototype points to shared proto",
		}},
	}
	ctor.Fields[StrPrototype] = protoRef
	h.Objs[ConstructorID] = ctor

	return h
}

// Ensure 返回对象状态，不存在时按默认原型（共享原型 P）惰性创建。
func (h *Heap) Ensure(id ObjID) *objState {
	if o, ok := h.Objs[id]; ok {
		return o
	}
	o := newObjState()
	o.Protos = Value{
		MayObjs:    map[ObjID]bool{SharedProtoID: true},
		MustObjs:   map[ObjID]bool{SharedProtoID: true},
		MayStrings: map[string]bool{},
		Evidence: []Evidence{{
			NodeID: string(id),
			Var:    StrProto,
			Desc:   "default prototype is the shared proto",
		}},
	}
	h.Objs[id] = o
	return o
}

// Copy 深拷贝整个堆。
func (h *Heap) Copy() *Heap {
	c := &Heap{Objs: make(map[ObjID]*objState, len(h.Objs))}
	for id, o := range h.Objs {
		c.Objs[id] = o.copy()
	}
	return c
}

// Join 合并两个堆：对象集合取并集，字段/原型走 Value 幂集合合并。
func (h *Heap) Join(other *Heap) *Heap {
	out := &Heap{Objs: map[ObjID]*objState{}}
	for id, o := range h.Objs {
		out.Objs[id] = o.copy()
	}
	for id, ro := range other.Objs {
		lo, ok := out.Objs[id]
		if !ok {
			out.Objs[id] = ro.copy()
			continue
		}
		lo.Multi = lo.Multi || ro.Multi
		lo.Protos = lo.Protos.Join(ro.Protos)
		for k, rv := range ro.Fields {
			if lv, ok := lo.Fields[k]; ok {
				lo.Fields[k] = lv.Join(rv)
			} else {
				lo.Fields[k] = rv.Copy()
			}
		}
	}
	return out
}

// State 是某个程序点的完整抽象状态：变量环境 + 抽象堆。
type State struct {
	Env  map[string]Value
	Heap *Heap
}

// NewState 构建仅含 P/C 的初始状态。
func NewState() *State {
	return &State{Env: map[string]Value{}, Heap: NewHeap()}
}

// Lookup 读取变量；未定义变量返回 bottom。
func (s *State) Lookup(name string) Value {
	if v, ok := s.Env[name]; ok {
		return v
	}
	return BottomValue()
}

// Copy 深拷贝状态（分支分析需要独立状态）。
func (s *State) Copy() *State {
	c := &State{Env: make(map[string]Value, len(s.Env)), Heap: s.Heap.Copy()}
	for k, v := range s.Env {
		c.Env[k] = v.Copy()
	}
	return c
}

// Join 合并两个程序点状态。
func (s *State) Join(other *State) *State {
	out := &State{Env: map[string]Value{}, Heap: s.Heap.Join(other.Heap)}
	for k, v := range s.Env {
		out.Env[k] = v.Copy()
	}
	for k, rv := range other.Env {
		if lv, ok := out.Env[k]; ok {
			out.Env[k] = lv.Join(rv)
		} else {
			out.Env[k] = rv.Copy()
		}
	}
	return out
}
