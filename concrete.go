package netsec

import "sort"

// 以下具体解释器用于验证静态分析的可靠性：它在真实对象语义下穷举
// 所有分支与外部字符串取值，记录每条路径上实际命中共享原型的写节点。

const noObj = -1

type cVal struct {
	defined bool
	isStr   bool
	str     string
	objID   int
}

func cStr(s string) cVal { return cVal{defined: true, isStr: true, str: s} }
func cObjVal(id int) cVal {
	return cVal{defined: true, objID: id}
}

var cBottom = cVal{}

type cObj struct {
	id     int
	fields map[string]cVal
	proto  int
}

func newCObj(id int, proto int) *cObj {
	return &cObj{id: id, fields: map[string]cVal{}, proto: proto}
}

// ExploreResult 是具体穷举执行的结果。
type ExploreResult struct {
	// Paths 是实际探索到的具体路径数（外部取值与条件分支的组合）。
	Paths int
	// DangerNodes 是至少一条具体路径上真正写入共享原型的 Store 节点。
	DangerNodes []string
	// Truncated 为 true 时表示路径数达到上限被截断，结论仅覆盖已探索路径。
	Truncated bool
}

type explorer struct {
	pathCap int
	paths   int
	danger  map[string]bool
	trunc   bool
}

// ExploreConcrete 对程序做具体穷举执行。pathCap 限制探索路径上限，
// 传入 <=0 时使用默认上限 50000。
func ExploreConcrete(prog *Program, pathCap int) (*ExploreResult, error) {
	if err := prog.Validate(); err != nil {
		return nil, err
	}
	if pathCap <= 0 {
		pathCap = 50000
	}
	e := &explorer{pathCap: pathCap, danger: map[string]bool{}}
	s := newCState()
	e.execCont(prog.Body, s)
	res := &ExploreResult{
		Paths:     e.paths,
		Truncated: e.trunc,
	}
	for id := range e.danger {
		res.DangerNodes = append(res.DangerNodes, id)
	}
	sort.Strings(res.DangerNodes)
	return res, nil
}

type cState struct {
	env    map[string]cVal
	objs   map[int]*cObj
	nextID int
	pID    int
	ctorID int
}

func newCState() *cState {
	s := &cState{
		env:    map[string]cVal{},
		objs:   map[int]*cObj{},
		nextID: 2,
		pID:    0,
		ctorID: 1,
	}
	p := newCObj(0, noObj)
	ctor := newCObj(1, noObj)
	ctor.fields[StrPrototype] = cObjVal(0)
	s.objs[0] = p
	s.objs[1] = ctor
	return s
}

func (s *cState) clone() *cState {
	c := &cState{
		env:    map[string]cVal{},
		objs:   map[int]*cObj{},
		nextID: s.nextID,
		pID:    s.pID,
		ctorID: s.ctorID,
	}
	for k, v := range s.env {
		c.env[k] = v
	}
	for id, o := range s.objs {
		co := &cObj{id: o.id, fields: map[string]cVal{}, proto: o.proto}
		for fk, fv := range o.fields {
			co.fields[fk] = fv
		}
		c.objs[id] = co
	}
	return c
}

func (s *cState) lookup(name string) cVal {
	if v, ok := s.env[name]; ok {
		return v
	}
	return cBottom
}

// execCont 执行 block 后继续执行 cont。External/If 会为每种取值/每个
// 分支克隆状态，并在克隆上递归调用 execCont，从而穷举全部具体路径。
func (e *explorer) execCont(block []Node, s *cState) {
	if e.trunc {
		return
	}
	if len(block) == 0 {
		e.paths++
		return
	}
	n, rest := block[0], block[1:]
	switch t := n.(type) {
	case *Alloc:
		id := s.nextID
		s.nextID++
		s.objs[id] = newCObj(id, s.pID)
		s.env[t.Dest] = cObjVal(id)
		e.execCont(rest, s)

	case *Const:
		s.env[t.Dest] = cStr(t.Value)
		e.execCont(rest, s)

	case *External:
		// 外部字符串穷举五种取值，各自在独立状态上继续。
		for _, choice := range AllowedStrings {
			if e.paths >= e.pathCap {
				e.trunc = true
				return
			}
			fork := s.clone()
			fork.env[t.Dest] = cStr(choice)
			e.execCont(rest, fork)
		}

	case *Alias:
		s.env[t.Dest] = s.lookup(t.Src)
		e.execCont(rest, s)

	case *Load:
		s.env[t.Dest] = e.evalLoad(s, s.lookup(t.Obj), s.lookup(t.Key))
		e.execCont(rest, s)

	case *Store:
		e.evalStore(t, s)
		e.execCont(rest, s)

	case *If:
		// 谓词未知：两个分支分别在克隆状态上穷举，空分支保留当前状态。
		branches := [][]Node{t.Then}
		if len(t.Else) > 0 {
			branches = append(branches, t.Else)
		}
		for _, branch := range branches {
			if e.paths >= e.pathCap {
				e.trunc = true
				return
			}
			fork := s.clone()
			branchCopy := append([]Node(nil), branch...)
			branchCopy = append(branchCopy, rest...)
			e.execCont(branchCopy, fork)
		}

	case *Loop:
		for i := 0; i < t.Count; i++ {
			// 循环体在同一具体路径上顺序执行；体内 External/If 仍会分叉。
			e.execCont(t.Body, s)
			if e.trunc {
				return
			}
		}
		e.execCont(rest, s)
	}
}

func (e *explorer) evalLoad(s *cState, recv, key cVal) cVal {
	if !recv.defined || recv.isStr || !key.defined || !key.isStr {
		return cBottom
	}
	return e.resolve(s, recv.objID, key.str, map[int]bool{})
}

func (e *explorer) resolve(s *cState, objID int, key string, visited map[int]bool) cVal {
	if objID == noObj || visited[objID] {
		return cBottom
	}
	visited[objID] = true
	o := s.objs[objID]

	if key == StrProto {
		if o.proto == noObj {
			return cBottom
		}
		return cObjVal(o.proto)
	}
	if fv, ok := o.fields[key]; ok {
		return fv
	}
	if key == StrConstructor {
		return cObjVal(s.ctorID)
	}
	if o.proto == noObj {
		return cBottom
	}
	return e.resolve(s, o.proto, key, visited)
}

func (e *explorer) evalStore(n *Store, s *cState) {
	recv := s.lookup(n.Obj)
	key := s.lookup(n.Key)
	val := s.lookup(n.Val)
	if !recv.defined || recv.isStr {
		return
	}
	o := s.objs[recv.objID]

	if !key.defined || !key.isStr {
		return
	}
	if key.str == StrProto {
		// __proto__ 写：赋入对象为共享原型 P 时构成原型污染。
		if val.defined && !val.isStr && val.objID == s.pID {
			e.danger[n.ID] = true
		}
		// 仅对象值会改变原型；字符串值与 JS 一样被忽略。
		if val.defined && !val.isStr {
			o.proto = val.objID
		}
		return
	}
	if recv.objID == s.pID {
		e.danger[n.ID] = true
	}
	if recv.objID == s.ctorID && key.str == StrPrototype {
		e.danger[n.ID] = true
	}
	o.fields[key.str] = val
}
