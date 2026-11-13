package netsec

import (
	"errors"
	"fmt"
)

// Kind 标识一个模拟 Luau 值的具体类型。
type Kind int

const (
	KindNil Kind = iota
	KindBool
	KindNumber
	KindString
	KindTable
	KindFunc
)

// Value 是模拟 Luau 值的带类型容器。零值即为 Luau nil。
// Value 的所有字段都是可比较类型，因此 Value 可以作为 Go map 的键，
// 这使得 Table.Fields 可以直接用 Value 做键。
type Value struct {
	Kind  Kind
	Bool  bool
	Num   float64
	Str   string
	Table *Table
	Func  *Func
}

// 常用值的便捷构造器。
func NilValue() Value             { return Value{Kind: KindNil} }
func BoolValue(b bool) Value      { return Value{Kind: KindBool, Bool: b} }
func NumberValue(n float64) Value { return Value{Kind: KindNumber, Num: n} }
func StringValue(s string) Value  { return Value{Kind: KindString, Str: s} }
func TableValue(t *Table) Value   { return Value{Kind: KindTable, Table: t} }
func FuncValue(f *Func) Value     { return Value{Kind: KindFunc, Func: f} }

// IsNil 报告该值是否为 Luau nil（含零值 Value）。
func (v Value) IsNil() bool { return v.Kind == KindNil }

// Func 模拟一个 Luau 函数。
//
// Builtin 为 true 时表示宿主注入的内置函数（如 string.format）。
// 内置函数在真实 Luau 中闭包于基础环境之上，getfenv 若直接放行就会把
// 基础环境交还给不可信脚本，因此模拟层对内置函数的 getfenv 一律拒绝。
//
// Env 是脚本闭包的函数环境；内置函数的 Env 保留 nil，绝不暴露。
type Func struct {
	Name    string
	Builtin bool
	Env     *Table
}

// Table 模拟一个 Luau 表，同时承载普通表、只读共享表和线程全局代理表。
//
// 权限标记：
//   - ReadOnly：构造期标记的标准库表 / 共享元表，任何 rawset、赋值、
//     setmetatable 都被拒绝；
//   - Frozen：脚本调用 table.freeze 后进入的冻结状态，冻结后的写同样被拒绝；
//   - Shared：宿主级共享表。只读表天然是共享的；可变共享表一旦被改动，
//     会出现在最终的变更报告里。
//
// 代理关系：
//   - ProxyTarget != nil 表示该表是某线程全局表对基础环境的代理。
//     代理自身的 Fields 是线程私有存储，写入只落在这里；
//     读取时先查私有字段，未命中再穿透到 ProxyTarget。
type Table struct {
	Name        string
	Fields      map[Value]Value
	Metatable   *Table
	ReadOnly    bool
	Frozen      bool
	Shared      bool
	ProxyTarget *Table
}

// NewTable 创建一个普通可变表。
func NewTable(name string) *Table {
	return &Table{Name: name, Fields: map[Value]Value{}}
}

// AsReadOnly 把表标记为只读共享表。标准库表与共享元表在构造期使用。
func (t *Table) AsReadOnly() *Table {
	t.ReadOnly = true
	t.Shared = true
	return t
}

// AsShared 把表标记为共享表（可变，变更会进入报告）。
func (t *Table) AsShared() *Table {
	t.Shared = true
	return t
}

// IsProxy 报告该表是否为线程全局代理。
func (t *Table) IsProxy() bool { return t.ProxyTarget != nil }

func (t *Table) String() string {
	if t == nil {
		return "<nil table>"
	}
	return fmt.Sprintf("table(%s)", t.Name)
}

// 写隔离在各个拦截点返回的哨兵错误，可用 errors.Is 精确判别。
var (
	// ErrReadOnly 表示写入目标是构造期只读的标准库表或共享元表。
	ErrReadOnly = errors.New("target table is read-only")
	// ErrFrozen 表示写入目标已被 table.freeze 冻结。
	ErrFrozen = errors.New("frozen table cannot be modified")
	// ErrSharedWrite 表示写入穿透代理、直接落在宿主共享表上。
	ErrSharedWrite = errors.New("write to shared host table is forbidden")
	// ErrBuiltinEnv 表示试图通过 getfenv/setfenv 取得或改写内置函数的基础环境。
	ErrBuiltinEnv = errors.New("builtin function environment is not accessible")
	// ErrInvalidTarget 表示操作目标无法解析（线程/路径不存在等）。
	ErrInvalidTarget = errors.New("invalid operation target")
)

// EventType 记录一次写访问尝试的处置结果，便于沙箱构造期审计。
type EventType string

const (
	EventPrivateWrite    EventType = "private_write"    // 落入线程私有表（代理或脚本自建表）
	EventBlockedReadonly EventType = "blocked_readonly" // 只读拦截
	EventBlockedFrozen   EventType = "blocked_frozen"   // 冻结拦截
	EventBlockedShared   EventType = "blocked_shared"   // 共享穿透拦截
	EventFreeze          EventType = "freeze"           // 冻结操作
)

// Event 是一次写相关操作的审计记录。
type Event struct {
	Thread string
	Op     string
	Target string
	Key    Value
	Type   EventType
	Err    error
}

// SharedChange 汇总一张共享表被改动的情况。
type SharedChange struct {
	Table   *Table
	Name    string
	Freezes int
}

// OpType 枚举脚本可执行的操作种类。
type OpType string

const (
	OpAssign      OpType = "assign"
	OpRawSet      OpType = "rawset"
	OpSetMeta     OpType = "setmetatable"
	OpGetFenv     OpType = "getfenv"
	OpSetFenv     OpType = "setfenv"
	OpTableFreeze OpType = "table.freeze"
)

// Path 描述从某线程全局表出发的解析路径：
// 依次按字符串/数字键穿透，读取遵循代理回退规则，
// 因此 ["string","format"] 可以解析到共享标准库里的内置函数。
type Path struct {
	Keys []Value
}

// Target 指定操作对象。Table 直接引用一张已构造的表/代理；
// Path 则从 Thread 的全局代理出发解析。两者二选一。
type Target struct {
	Table *Table
	Path  *Path
}

// Op 是脚本操作序列中的单条操作。
//
// 各字段按 OpType 取用：
//   - assign/rawset：Target+Key+Value；
//   - setmetatable：Target+Meta；
//   - getfenv：Func（nil 表示当前线程环境）；
//   - setfenv：Func+Env；
//   - table.freeze：Target。
type Op struct {
	Type   OpType
	Thread string
	Target Target
	Key    Value
	Value  Value
	Meta   *Table
	Func   *Func
	Env    *Table
}

// OpResult 是单条操作的执行结果。
type OpResult struct {
	Index int
	Op    Op
	Value Value // getfenv 的返回值
	Err   error
}

// Report 汇总整个操作序列的执行结果。
type Report struct {
	Results []OpResult
	Events  []Event
	Changes map[*Table]*SharedChange
}

// ChangedTables 返回被实际改动的共享表。字段/元表写入在权限闸门处即被
// 拒绝，因此这里只会出现被 table.freeze 改变状态的共享表。
func (r *Report) ChangedTables() []*Table {
	out := make([]*Table, 0, len(r.Changes))
	for _, c := range r.Changes {
		out = append(out, c.Table)
	}
	return out
}

// ThreadConfig 描述一个脚本线程：它的全局表是代理到 Target 的私有代理。
type ThreadConfig struct {
	Name   string
	Target *Table
}

// Config 是沙箱构造输入：只读标记随 Table 本身携带，这里补充线程代理关系。
type Config struct {
	Threads []ThreadConfig
}

// Sandbox 是写隔离模拟器，由宿主的沙箱构造步骤创建并复用。
type Sandbox struct {
	threads map[string]*Table
	events  []Event
	changes map[*Table]*SharedChange
}

// New 构造模拟器：为每个线程建立一张代理到基础环境的私有全局表。
func New(cfg Config) (*Sandbox, error) {
	sb := &Sandbox{
		threads: map[string]*Table{},
		changes: map[*Table]*SharedChange{},
	}
	for _, tc := range cfg.Threads {
		if tc.Name == "" || tc.Target == nil {
			return nil, fmt.Errorf("%w: thread %q missing name or proxy target", ErrInvalidTarget, tc.Name)
		}
		if _, dup := sb.threads[tc.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate thread %q", ErrInvalidTarget, tc.Name)
		}
		proxy := &Table{
			Name:        "global(" + tc.Name + ")",
			Fields:      map[Value]Value{},
			ProxyTarget: tc.Target,
		}
		sb.threads[tc.Name] = proxy
	}
	return sb, nil
}

// Thread 返回某线程的全局代理表，未知名线程返回 nil。
func (sb *Sandbox) Thread(name string) *Table { return sb.threads[name] }

// Execute 按顺序执行整个脚本操作序列。任何单条操作失败都不会中断序列，
// 错误记录在对应的 OpResult 与审计事件中，最终返回完整报告。
func (sb *Sandbox) Execute(ops []Op) *Report {
	report := &Report{Changes: sb.changes}
	for i, op := range ops {
		res := OpResult{Index: i, Op: op}
		switch op.Type {
		case OpAssign:
			res.Err = sb.write(op, false)
		case OpRawSet:
			res.Err = sb.write(op, true)
		case OpSetMeta:
			res.Err = sb.setMetatable(op)
		case OpGetFenv:
			res.Value, res.Err = sb.getFenv(op)
		case OpSetFenv:
			res.Err = sb.setFenv(op)
		case OpTableFreeze:
			res.Err = sb.freeze(op)
		default:
			res.Err = fmt.Errorf("%w: unknown op %q", ErrInvalidTarget, op.Type)
		}
		report.Results = append(report.Results, res)
	}
	report.Events = append(report.Events, sb.events...)
	return report
}

// write 处理 assign 与 rawset。raw 仅影响元方法语义，不改变权限判定：
// 代理写入始终落私有表；对只读/冻结/共享底层表的写入一律拒绝。
func (sb *Sandbox) write(op Op, raw bool) error {
	target, key, err := sb.resolveWrite(op.Thread, op.Target, op.Key)
	if err != nil {
		return err
	}
	opName := string(OpAssign)
	if raw {
		opName = string(OpRawSet)
	}
	if err := sb.checkWritable(target); err != nil {
		sb.recordBlock(op.Thread, opName, target, key, err)
		return err
	}
	target.Fields[key] = op.Value
	if target.IsProxy() {
		sb.events = append(sb.events, Event{
			Thread: op.Thread, Op: opName, Target: target.Name,
			Key: key, Type: EventPrivateWrite,
		})
		return nil
	}
	sb.events = append(sb.events, Event{
		Thread: op.Thread, Op: opName, Target: target.Name,
		Key: key, Type: EventPrivateWrite,
	})
	return nil
}

// setMetatable 处理 setmetatable：只读表连元表也不允许替换/追加，
// 冻结表与共享表同样拒绝。
func (sb *Sandbox) setMetatable(op Op) error {
	target, err := sb.resolve(op.Thread, op.Target)
	if err != nil {
		return err
	}
	if err := sb.checkWritable(target); err != nil {
		sb.recordBlock(op.Thread, string(OpSetMeta), target, NilValue(), err)
		return err
	}
	target.Metatable = op.Meta
	if target.IsProxy() {
		sb.events = append(sb.events, Event{
			Thread: op.Thread, Op: string(OpSetMeta), Target: target.Name,
			Type: EventPrivateWrite,
		})
		return nil
	}
	sb.events = append(sb.events, Event{
		Thread: op.Thread, Op: string(OpSetMeta), Target: target.Name,
		Type: EventPrivateWrite,
	})
	return nil
}

// getFenv 处理 getfenv。内置函数不返回其闭包所依赖的基础环境；
// 显式给出的脚本闭包返回自身环境；不给函数时返回当前线程的全局代理。
func (sb *Sandbox) getFenv(op Op) (Value, error) {
	if op.Func == nil {
		proxy, ok := sb.threads[op.Thread]
		if !ok {
			return NilValue(), fmt.Errorf("%w: unknown thread %q", ErrInvalidTarget, op.Thread)
		}
		return TableValue(proxy), nil
	}
	if op.Func.Builtin {
		err := fmt.Errorf("%w: %s", ErrBuiltinEnv, op.Func.Name)
		sb.events = append(sb.events, Event{
			Thread: op.Thread, Op: string(OpGetFenv), Target: op.Func.Name,
			Type: EventBlockedReadonly, Err: err,
		})
		return NilValue(), err
	}
	if op.Func.Env == nil {
		return NilValue(), nil
	}
	return TableValue(op.Func.Env), nil
}

// setFenv 处理 setfenv。内置函数的环境不可替换；脚本闭包的环境被
// 改写为调用方提供的表（调用方只能传入自己持有的代理/私有表）。
func (sb *Sandbox) setFenv(op Op) error {
	if op.Func == nil {
		return fmt.Errorf("%w: setfenv requires an explicit function", ErrInvalidTarget)
	}
	if op.Func.Builtin {
		err := fmt.Errorf("%w: %s", ErrBuiltinEnv, op.Func.Name)
		sb.events = append(sb.events, Event{
			Thread: op.Thread, Op: string(OpSetFenv), Target: op.Func.Name,
			Type: EventBlockedReadonly, Err: err,
		})
		return err
	}
	op.Func.Env = op.Env
	return nil
}

// freeze 处理 table.freeze。只读表无需也不允许再冻结；冻结后该表
// 进入与只读等效的拒绝写入状态。
func (sb *Sandbox) freeze(op Op) error {
	target, err := sb.resolve(op.Thread, op.Target)
	if err != nil {
		return err
	}
	if target.ReadOnly {
		err := fmt.Errorf("%w: %s", ErrReadOnly, target.Name)
		sb.recordBlock(op.Thread, string(OpTableFreeze), target, NilValue(), err)
		return err
	}
	target.Frozen = true
	sb.events = append(sb.events, Event{
		Thread: op.Thread, Op: string(OpTableFreeze), Target: target.Name,
		Type: EventFreeze,
	})
	if !target.IsProxy() && target.Shared {
		ch := sb.changeEntry(target)
		ch.Freezes++
	}
	return nil
}

// checkWritable 是所有写入口共享的权限闸门，顺序为只读、冻结、共享。
// 代理表是线程私有的，任何共享/只读标记都不会落到它身上，因此永远可写。
func (sb *Sandbox) checkWritable(t *Table) error {
	if t.IsProxy() {
		return nil
	}
	if t.ReadOnly {
		return fmt.Errorf("%w: %s", ErrReadOnly, t.Name)
	}
	if t.Frozen {
		return fmt.Errorf("%w: %s", ErrFrozen, t.Name)
	}
	if t.Shared {
		return fmt.Errorf("%w: %s", ErrSharedWrite, t.Name)
	}
	return nil
}

func (sb *Sandbox) recordBlock(thread, opName string, t *Table, key Value, err error) {
	evType := EventBlockedShared
	switch {
	case t.ReadOnly:
		evType = EventBlockedReadonly
	case t.Frozen:
		evType = EventBlockedFrozen
	}
	sb.events = append(sb.events, Event{
		Thread: thread, Op: opName, Target: t.Name, Key: key, Type: evType, Err: err,
	})
}

func (sb *Sandbox) changeEntry(t *Table) *SharedChange {
	ch, ok := sb.changes[t]
	if !ok {
		ch = &SharedChange{Table: t, Name: t.Name}
		sb.changes[t] = ch
	}
	return ch
}

// resolve 把操作目标解析为最终落写的表：
// 直接引用原样返回；路径从线程全局代理出发按代理回退规则穿透。
func (sb *Sandbox) resolve(thread string, target Target) (*Table, error) {
	if target.Table != nil {
		return target.Table, nil
	}
	proxy, ok := sb.threads[thread]
	if !ok {
		return nil, fmt.Errorf("%w: unknown thread %q", ErrInvalidTarget, thread)
	}
	if target.Path == nil || len(target.Path.Keys) == 0 {
		return proxy, nil
	}
	cur := proxy
	for _, key := range target.Path.Keys {
		next, ok := tableGet(cur, key)
		if !ok || next.Kind != KindTable || next.Table == nil {
			return nil, fmt.Errorf("%w: path key %v did not resolve to a table", ErrInvalidTarget, key)
		}
		cur = next.Table
	}
	return cur, nil
}

// resolveWrite 解析写操作的父表与键。直接表引用使用调用方给出的 Key；
// 路径的最后一个键作为写字段（因此 string.format 解析为：向 string 表写 format），
// 其余键按代理回退规则穿透到父表。
func (sb *Sandbox) resolveWrite(thread string, target Target, key Value) (*Table, Value, error) {
	if target.Table != nil || target.Path == nil || len(target.Path.Keys) == 0 {
		t, err := sb.resolve(thread, target)
		return t, key, err
	}
	parent := Target{Path: &Path{Keys: target.Path.Keys[:len(target.Path.Keys)-1]}}
	t, err := sb.resolve(thread, parent)
	if err != nil {
		return nil, NilValue(), err
	}
	return t, target.Path.Keys[len(target.Path.Keys)-1], nil
}

// tableGet 模拟一次带代理回退的索引读取：先查本表私有字段，
// 代理未命中再穿透到 ProxyTarget。
func tableGet(t *Table, key Value) (Value, bool) {
	if v, ok := t.Fields[key]; ok {
		return v, true
	}
	if t.ProxyTarget != nil {
		return tableGet(t.ProxyTarget, key)
	}
	return NilValue(), false
}

// Get 返回某线程沿路径读到的值（带代理回退），供宿主构造步骤自检。
func (sb *Sandbox) Get(thread string, path Path) (Value, error) {
	proxy, ok := sb.threads[thread]
	if !ok {
		return NilValue(), fmt.Errorf("%w: unknown thread %q", ErrInvalidTarget, thread)
	}
	if len(path.Keys) == 0 {
		return TableValue(proxy), nil
	}
	var cur Value = TableValue(proxy)
	for _, key := range path.Keys {
		if cur.Kind != KindTable || cur.Table == nil {
			return NilValue(), fmt.Errorf("%w: intermediate value is not a table", ErrInvalidTarget)
		}
		v, ok := tableGet(cur.Table, key)
		if !ok {
			return NilValue(), nil
		}
		cur = v
	}
	return cur, nil
}

// RawGet 只查指定表自身字段，不做代理回退，对应 Lua rawget。
func RawGet(t *Table, key Value) Value {
	if t == nil {
		return NilValue()
	}
	return t.Fields[key]
}
