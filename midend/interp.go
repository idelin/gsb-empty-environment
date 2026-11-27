package midend

import (
	"fmt"
	"sort"
	"strings"
)

const (
	ptrSize        = 8
	maxInterpSteps = 50_000_000
)

// Pointer is a runtime address into an object.
type Pointer struct {
	Obj    int // object id; 0 is the null object
	Offset int
}

// Value is a runtime SSA value: either an integer or a pointer.
type Value struct {
	Int int64
	Ptr *Pointer
}

func intVal(i int64) Value { return Value{Int: i} }

func ptrVal(p *Pointer) Value {
	if p == nil {
		return Value{}
	}
	return Value{Ptr: p}
}

// IsPtr reports whether the value carries a non-null pointer.
func (v Value) IsPtr() bool { return v.Ptr != nil }

// Object is a byte-addressed runtime object (heap, stack or global).
type Object struct {
	ID     int
	Global bool
	Stack  bool
	Size   int
	mem    []byte
	ptrAt  map[int]*Pointer
}

type frame struct {
	fn   *Func
	regs map[string]Value
}

// Interpreter executes a Program over heap/stack/global storage.
type Interpreter struct {
	prog    *Program
	objs    map[int]*Object
	frames  []*frame
	globals map[string]*Object
	nextID  int

	phiPred string

	// Output holds every printed integer in execution order.
	Output []string

	// result is the most recent top-level return value, used as a GC root.
	result Value

	steps     int
	extFuncs  map[string]func([]Value) Value
	typeCache map[*Func]map[string]Type
}

// NewInterpreter creates an interpreter with zero-initialized globals.
func NewInterpreter(prog *Program) *Interpreter {
	in := &Interpreter{
		prog:      prog,
		objs:      map[int]*Object{},
		globals:   map[string]*Object{},
		extFuncs:  map[string]func([]Value) Value{},
		typeCache: map[*Func]map[string]Type{},
	}
	for _, g := range prog.Globals {
		in.globals[g.Name] = in.newObject(g.Ty.size(), false, true)
	}
	return in
}

// RegisterExtFunc registers an opaque external function implementation.
// When unregistered, external calls return the zero value of their type.
func (in *Interpreter) RegisterExtFunc(name string, fn func([]Value) Value) {
	in.extFuncs[name] = fn
}

func (in *Interpreter) newObject(size int, stack, global bool) *Object {
	in.nextID++
	o := &Object{
		ID:     in.nextID,
		Global: global,
		Stack:  stack,
		Size:   size,
		mem:    make([]byte, size),
		ptrAt:  map[int]*Pointer{},
	}
	in.objs[o.ID] = o
	return o
}

func (in *Interpreter) cur() *frame { return in.frames[len(in.frames)-1] }

func (in *Interpreter) setReg(name string, v Value) { in.cur().regs[name] = v }

func (in *Interpreter) getReg(name string) Value {
	for i := len(in.frames) - 1; i >= 0; i-- {
		if v, ok := in.frames[i].regs[name]; ok {
			return v
		}
	}
	panic("midend: undefined register %" + name)
}

func (in *Interpreter) eval(o Operand) Value {
	if o.IsConst() {
		return Value{Int: o.Int}
	}
	return in.getReg(o.Reg)
}

func zeroVal(ty Type) Value {
	if _, ok := ty.(*PtrType); ok {
		return Value{}
	}
	return intVal(0)
}

// Run executes the entry function with the given integer arguments.
func (in *Interpreter) Run(args ...int64) Value {
	f := in.prog.Func(in.prog.Entry)
	if f == nil {
		panic("midend: entry function " + in.prog.Entry + " missing")
	}
	argv := make([]Value, len(args))
	for i, a := range args {
		argv[i] = intVal(a)
	}
	in.result = in.call(f, argv)
	return in.result
}

func (in *Interpreter) call(f *Func, args []Value) Value {
	if len(args) != len(f.Params) {
		panic(fmt.Sprintf("midend: arity mismatch calling %s", f.Name))
	}
	fr := &frame{fn: f, regs: map[string]Value{}}
	for i, p := range f.Params {
		fr.regs[p.Name] = args[i]
	}
	stackObjs := map[int]*Object{}
	in.frames = append(in.frames, fr)
	res := in.execute(f, stackObjs)
	in.frames = in.frames[:len(in.frames)-1]
	for id := range stackObjs {
		delete(in.objs, id)
	}
	return res
}

func (in *Interpreter) execute(f *Func, stackObjs map[int]*Object) Value {
	block := f.Blocks[0]
	for {
		in.steps++
		if in.steps > maxInterpSteps {
			panic("midend: interpreter step limit exceeded")
		}
		types := in.funcTypes(f)
		for _, ins := range block.Insts {
			in.execInst(ins, stackObjs, types)
		}

		switch t := block.Term.(type) {
		case *Jump:
			in.phiPred = block.Name
			block = in.prog.Block(f, t.To)
		case *Branch:
			in.phiPred = block.Name
			if in.eval(t.Cond).Int != 0 {
				block = in.prog.Block(f, t.T)
			} else {
				block = in.prog.Block(f, t.F)
			}
		case *Return:
			return in.eval(t.Val)
		default:
			panic("midend: block without terminator")
		}
	}
}

func (in *Interpreter) funcTypes(f *Func) map[string]Type {
	if tc, ok := in.typeCache[f]; ok {
		return tc
	}
	tc := regTypes(f)
	in.typeCache[f] = tc
	return tc
}

func (in *Interpreter) execInst(ins Inst, stackObjs map[int]*Object, types map[string]Type) {
	switch x := ins.(type) {
	case *Alloc:
		o := in.newObject(x.Ty.size(), !x.Heap, false)
		if !x.Heap {
			stackObjs[o.ID] = o
		}
		in.setReg(x.Name, ptrVal(&Pointer{Obj: o.ID}))
	case *GlobalAddr:
		o := in.globals[x.G.Name]
		in.setReg(x.Name, ptrVal(&Pointer{Obj: o.ID}))
	case *FieldAddr:
		p := in.deref(in.eval(x.Ptr), "fieldaddr")
		st := pointeeStruct(types, x)
		in.setReg(x.Name, ptrVal(&Pointer{Obj: p.Obj, Offset: p.Offset + st.Offset(x.Field)}))
	case *AddrAdd:
		p := in.deref(in.eval(x.Ptr), "addradd")
		off := in.eval(x.Off).Int
		in.setReg(x.Name, ptrVal(&Pointer{Obj: p.Obj, Offset: p.Offset + int(off)}))
	case *Load:
		p := in.deref(in.eval(x.Ptr), "load")
		in.setReg(x.Name, in.read(p, x.Ty))
	case *Store:
		p := in.deref(in.eval(x.Ptr), "store")
		in.write(p, in.typeOf(x.Val), in.eval(x.Val))
	case *Phi:
		var v Value
		for _, e := range x.Incoming {
			if e.Block == in.phiPred {
				v = in.eval(e.Val)
				break
			}
		}
		in.setReg(x.Name, v)
	case *BinOp:
		l := in.eval(x.X).Int
		r := in.eval(x.Y).Int
		in.setReg(x.Name, intVal(evalBin(x.Op, l, r)))
	case *Call:
		callee := in.prog.Func(x.Callee)
		argv := make([]Value, len(x.Args))
		for i, a := range x.Args {
			argv[i] = in.eval(a)
		}
		in.setReg(x.Name, in.call(callee, argv))
	case *ExtCall:
		argv := make([]Value, len(x.Args))
		for i, a := range x.Args {
			argv[i] = in.eval(a)
		}
		fn := in.extFuncs[x.Callee]
		if fn == nil {
			in.setReg(x.Name, zeroVal(x.Ty))
		} else {
			in.setReg(x.Name, fn(argv))
		}
	case *PrintInst:
		v := in.eval(x.Val)
		in.Output = append(in.Output, fmt.Sprintf("%d", v.Int))
	default:
		panic(fmt.Sprintf("midend: unsupported inst %T", ins))
	}
}

func evalBin(op string, x, y int64) int64 {
	switch op {
	case "add":
		return x + y
	case "sub":
		return x - y
	case "mul":
		return x * y
	case "eq":
		return b2i(x == y)
	case "ne":
		return b2i(x != y)
	case "lt":
		return b2i(x < y)
	default:
		panic("midend: unsupported binop " + op)
	}
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (in *Interpreter) deref(v Value, where string) *Pointer {
	if !v.IsPtr() {
		panic("midend: nil pointer dereference in " + where)
	}
	if _, ok := in.objs[v.Ptr.Obj]; !ok {
		panic("midend: dangling pointer in " + where)
	}
	return v.Ptr
}

func (in *Interpreter) read(p *Pointer, ty Type) Value {
	o := in.objs[p.Obj]
	end := p.Offset + ty.size()
	if p.Offset < 0 || end > o.Size {
		panic("midend: out-of-bounds read")
	}
	if _, ok := ty.(*PtrType); ok {
		return ptrVal(o.ptrAt[p.Offset])
	}
	var n int64
	for i := 0; i < ty.size(); i++ {
		n |= int64(o.mem[p.Offset+i]) << (8 * i)
	}
	return intVal(n)
}

func (in *Interpreter) write(p *Pointer, ty Type, v Value) {
	o := in.objs[p.Obj]
	end := p.Offset + ty.size()
	if p.Offset < 0 || end > o.Size {
		panic("midend: out-of-bounds write")
	}
	if _, ok := ty.(*PtrType); ok {
		if v.IsPtr() {
			o.ptrAt[p.Offset] = v.Ptr
		} else {
			delete(o.ptrAt, p.Offset)
		}
		return
	}
	delete(o.ptrAt, p.Offset)
	n := v.Int
	for i := 0; i < ty.size(); i++ {
		o.mem[p.Offset+i] = byte(n >> (8 * i))
	}
}

// HeapSnapshot returns a deterministic, identity-insensitive rendering of all
// live heap objects reachable from globals and active frame registers. Object
// ids are canonicalized by BFS discovery order, so two executions can be
// compared even when raw allocation ids differ.
func (in *Interpreter) HeapSnapshot() string {
	roots := in.collectRoots()
	seen := map[int]bool{}
	var order []int
	queue := append([]*Pointer{}, roots...)
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if p == nil || p.Obj == 0 {
			continue
		}
		o, ok := in.objs[p.Obj]
		if !ok || o.Stack || o.Global || seen[o.ID] {
			continue
		}
		seen[o.ID] = true
		order = append(order, o.ID)
		queue = append(queue, in.outgoingPtrs(o)...)
	}

	canon := map[int]int{}
	for i, id := range order {
		canon[id] = i + 1
	}

	var b strings.Builder
	for idx, id := range order {
		o := in.objs[id]
		fmt.Fprintf(&b, "obj%d(size=%d", idx+1, o.Size)
		ptrOffs := make([]int, 0, len(o.ptrAt))
		for off := range o.ptrAt {
			ptrOffs = append(ptrOffs, off)
		}
		sort.Ints(ptrOffs)
		for _, off := range ptrOffs {
			target := 0
			if tp := o.ptrAt[off]; tp != nil {
				if c, ok := canon[tp.Obj]; ok {
					target = c
				}
			}
			fmt.Fprintf(&b, ",p@%d->obj%d", off, target)
		}
		var bytes []string
		for off := 0; off < o.Size; off++ {
			if _, isPtr := o.ptrAt[off]; isPtr {
				continue
			}
			bytes = append(bytes, fmt.Sprintf("%d=%d", off, o.mem[off]))
		}
		if len(bytes) > 0 {
			b.WriteString(",bytes[" + strings.Join(bytes, ",") + "]")
		}
		b.WriteString(");")
	}
	return b.String()
}

func (in *Interpreter) outgoingPtrs(o *Object) []*Pointer {
	offs := make([]int, 0, len(o.ptrAt))
	for off := range o.ptrAt {
		offs = append(offs, off)
	}
	sort.Ints(offs)
	ps := make([]*Pointer, 0, len(offs))
	for _, off := range offs {
		ps = append(ps, o.ptrAt[off])
	}
	return ps
}

func (in *Interpreter) collectRoots() []*Pointer {
	var roots []*Pointer
	gnames := make([]string, 0, len(in.globals))
	for name := range in.globals {
		gnames = append(gnames, name)
	}
	sort.Strings(gnames)
	for _, name := range gnames {
		gobj := in.globals[name]
		roots = append(roots, in.outgoingPtrs(gobj)...)
	}
	for _, fr := range in.frames {
		names := make([]string, 0, len(fr.regs))
		for name := range fr.regs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if v := fr.regs[name]; v.IsPtr() {
				roots = append(roots, v.Ptr)
			}
		}
	}
	if in.result.IsPtr() {
		roots = append(roots, in.result.Ptr)
	}
	return roots
}
