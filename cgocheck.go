package netsec

import (
	"fmt"
	"reflect"
	"sync"
	"unsafe"
)

// Field 描述结构体字段布局，供“指向字段时只检查该字段”判定使用。
type Field struct {
	Name   string
	Offset uintptr
	Size   uintptr
}

// 违规规则标识。
const (
	// RuleGoPointerInArg 表示传给 C 的内存中仍含 Go 指针。
	RuleGoPointerInArg = "arg-contains-go-pointer"
	// RuleCRetainsGo 表示调用返回后 C 端持有表仍引用 Go 对象。
	RuleCRetainsGo = "c-retains-go-pointer"
	// RuleUnregisteredPtr 表示传入指针既不在已登记 Go 对象中也不在 C 块中。
	RuleUnregisteredPtr = "unregistered-pointer"
	// RuleInteriorPtr 表示指针落在对象内部非边界位置（填充或元素内部）。
	RuleInteriorPtr = "interior-pointer"
)

// Violation 描述一次 cgo 指针规则违规。
type Violation struct {
	Rule   string
	Ptr    uintptr
	Detail string
}

func (v *Violation) Error() string {
	return fmt.Sprintf("cgo 指针规则违规[%s]: ptr=%#x %s", v.Rule, v.Ptr, v.Detail)
}

type spanKind uint8

const (
	spanGo spanKind = iota
	spanC
)

// memSpan 描述一段已登记内存：Go 分配的对象（含字段布局/数组信息）或 C 分配块。
type memSpan struct {
	base, size uintptr
	kind       spanKind
	desc       string
	fields     []Field // 结构体字段布局；非结构体为 nil
	isArray    bool
	elemSize   uintptr
}

func (s *memSpan) contains(addr uintptr) bool {
	return addr >= s.base && addr < s.base+s.size
}

// Checker 是供 cgo 包装层调用的指针规则检查器。
//
// 使用约定：
//  1. RegisterGo 登记可能传给 C 的 Go 对象图（递归登记可达对象）；
//  2. RegisterCBlock 登记 C 分配块（如 C.malloc 返回值）；
//  3. 调用 C 前对每个指针实参调用 CheckArg；
//  4. C 侧（含回调）保存指针时经包装层调用 CRetain，释放时调用 CRelease；
//  5. 调用返回后用 CheckRetained 检查 C 端持有表。
//
// 对 Go 堆指针的判定基于已登记对象图，属于保守扫描：
// 未登记的 Go 对象不会被识别为 Go 指针目标。
type Checker struct {
	mu       sync.Mutex
	spans    []*memSpan
	retained map[uintptr]string // C 端持有表：指针 -> 来源描述
}

// NewChecker 返回空的检查器。
func NewChecker() *Checker {
	return &Checker{retained: make(map[uintptr]string)}
}

// RegisterCBlock 登记一块 C 分配内存。C 块内容不参与 Go 指针扫描，
// 指向 C 块的指针一律允许传给 C。
func (c *Checker) RegisterCBlock(p unsafe.Pointer, size uintptr, desc string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addSpan(&memSpan{base: uintptr(p), size: size, kind: spanC, desc: desc})
}

// RegisterGo 以 v 为根登记 Go 分配的对象图。
// v 通常为指针或切片；递归登记所有可达的指针目标与切片底层数组，
// 并为结构体记录字段布局、为数组/切片底层数组记录元素尺寸。
// map/chan/func 无法保守扫描，不予登记。
func (c *Checker) RegisterGo(desc string, v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.walk(reflect.ValueOf(v), desc, make(map[visitKey]bool))
}

// CheckArg 在调用 C 前检查指针实参 p：
//   - p 指向 C 块：允许；
//   - p 指向结构体某字段：只检查该字段占用的内存；
//   - p 指向数组/切片底层数组的元素：检查整个数组；
//   - p 指向对象起始：检查整个对象；
//   - 被检查内存中含任何指向已登记 Go 对象的指针：违规。
//
// 返回 nil 表示通过，否则返回 *Violation。
func (c *Checker) CheckArg(p unsafe.Pointer) error {
	if p == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	addr := uintptr(p)
	sp := c.findSpan(addr)
	if sp == nil {
		return &Violation{Rule: RuleUnregisteredPtr, Ptr: addr,
			Detail: "指针未登记为 Go 对象或 C 块"}
	}
	if sp.kind == spanC {
		return nil
	}
	start, end, err := c.regionFor(sp, addr)
	if err != nil {
		return err
	}
	return c.scan(start, end)
}

// CheckArgs 依次检查多个指针实参，返回首个违规。
func (c *Checker) CheckArgs(ptrs ...unsafe.Pointer) error {
	for _, p := range ptrs {
		if err := c.CheckArg(p); err != nil {
			return err
		}
	}
	return nil
}

// CRetain 登记 C 侧（含回调）保存的指针，where 描述保存位置。
func (c *Checker) CRetain(p unsafe.Pointer, where string) {
	if p == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.retained[uintptr(p)] = where
}

// CRelease 从 C 端持有表中移除指针。
func (c *Checker) CRelease(p unsafe.Pointer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.retained, uintptr(p))
}

// CheckRetained 在调用返回后检查 C 端持有表：
// 仍引用已登记 Go 对象的每一项构成一个违规。
func (c *Checker) CheckRetained() []error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []error
	for ptr, where := range c.retained {
		if sp := c.findSpan(ptr); sp != nil && sp.kind == spanGo {
			out = append(out, &Violation{Rule: RuleCRetainsGo, Ptr: ptr,
				Detail: fmt.Sprintf("调用返回后 C 仍持有 Go 指针（%s，指向 %s）", where, sp.desc)})
		}
	}
	return out
}

type visitKey struct {
	typ reflect.Type
	ptr uintptr
}

func (c *Checker) addSpan(sp *memSpan) {
	if sp.size == 0 {
		return
	}
	for _, existing := range c.spans {
		if existing.base == sp.base && existing.size == sp.size && existing.kind == sp.kind {
			return
		}
	}
	c.spans = append(c.spans, sp)
}

func (c *Checker) findSpan(addr uintptr) *memSpan {
	for _, sp := range c.spans {
		if sp.contains(addr) {
			return sp
		}
	}
	return nil
}

// ptrOf 把已登记内存的地址转回 unsafe.Pointer。
// 检查器需要按地址读取内存（保守扫描），地址均来自登记时的真实对象。
func ptrOf(addr uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&addr))
}

// regionFor 按规则确定待检查内存区域。
func (c *Checker) regionFor(sp *memSpan, addr uintptr) (start, end uintptr, err error) {
	switch {
	case sp.fields != nil:
		if addr == sp.base {
			return sp.base, sp.base + sp.size, nil
		}
		for _, f := range sp.fields {
			fs := sp.base + f.Offset
			if f.Size > 0 && addr >= fs && addr < fs+f.Size {
				return fs, fs + f.Size, nil // 指向结构体字段：只检查该字段
			}
		}
		return 0, 0, &Violation{Rule: RuleInteriorPtr, Ptr: addr,
			Detail: fmt.Sprintf("指针落在 %s 的字段之外（填充字节）", sp.desc)}
	case sp.isArray:
		if addr == sp.base {
			return sp.base, sp.base + sp.size, nil
		}
		if off := addr - sp.base; sp.elemSize > 0 && off%sp.elemSize == 0 {
			return sp.base, sp.base + sp.size, nil // 指向数组元素：检查整个数组
		}
		return 0, 0, &Violation{Rule: RuleInteriorPtr, Ptr: addr,
			Detail: fmt.Sprintf("指针落在 %s 的数组元素内部", sp.desc)}
	default:
		if addr == sp.base {
			return sp.base, sp.base + sp.size, nil
		}
		return 0, 0, &Violation{Rule: RuleInteriorPtr, Ptr: addr,
			Detail: fmt.Sprintf("指向 %s 内部的非边界指针", sp.desc)}
	}
}

// scan 保守扫描 [start,end)：任一机器字指向已登记 Go 对象即违规。
func (c *Checker) scan(start, end uintptr) error {
	const ws = unsafe.Sizeof(uintptr(0))
	a := (start + ws - 1) &^ (ws - 1) // 指针必然字对齐
	for ; a+ws <= end; a += ws {
		w := *(*uintptr)(ptrOf(a))
		if w == 0 {
			continue
		}
		if sp := c.findSpan(w); sp != nil && sp.kind == spanGo {
			return &Violation{Rule: RuleGoPointerInArg, Ptr: a,
				Detail: fmt.Sprintf("内存 [%#x,%#x) 内含 Go 指针 %#x（指向 %s）",
					start, end, w, sp.desc)}
		}
	}
	return nil
}

// walk 递归登记对象图。
func (c *Checker) walk(rv reflect.Value, desc string, seen map[visitKey]bool) {
	switch rv.Kind() {
	case reflect.Pointer:
		if !rv.IsNil() {
			c.walkObject(rv.Type().Elem(), rv.Pointer(), desc, seen)
		}
	case reflect.Interface:
		if !rv.IsNil() {
			c.walk(rv.Elem(), desc, seen)
		}
	case reflect.Slice:
		if rv.Len() > 0 {
			c.walkBackingArray(rv.Type().Elem(), rv.Pointer(), rv.Len(), desc, seen)
		}
	case reflect.String:
		if s := rv.String(); len(s) > 0 {
			c.addSpan(&memSpan{
				base: uintptr(unsafe.Pointer(unsafe.StringData(s))),
				size: uintptr(len(s)), desc: desc + "(string数据)",
			})
		}
	case reflect.Struct:
		for i := 0; i < rv.NumField(); i++ {
			c.walk(rv.Field(i), desc, seen)
		}
	case reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			c.walk(rv.Index(i), desc, seen)
		}
	}
}

// walkObject 登记指针目标的内存跨度，并递归其内容。
func (c *Checker) walkObject(t reflect.Type, addr uintptr, desc string, seen map[visitKey]bool) {
	key := visitKey{t, addr}
	if seen[key] {
		return
	}
	seen[key] = true

	sp := &memSpan{base: addr, size: t.Size(), desc: desc}
	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			sp.fields = append(sp.fields, Field{Name: f.Name, Offset: f.Offset, Size: f.Type.Size()})
		}
	case reflect.Array:
		sp.isArray = true
		sp.elemSize = t.Elem().Size()
	}
	c.addSpan(sp)
	c.walk(reflect.NewAt(t, ptrOf(addr)).Elem(), desc, seen)
}

// walkBackingArray 登记切片底层数组并递归各元素。
func (c *Checker) walkBackingArray(et reflect.Type, addr uintptr, n int, desc string, seen map[visitKey]bool) {
	key := visitKey{reflect.ArrayOf(n, et), addr}
	if seen[key] {
		return
	}
	seen[key] = true

	c.addSpan(&memSpan{
		base: addr, size: et.Size() * uintptr(n),
		isArray: true, elemSize: et.Size(), desc: desc + "(底层数组)",
	})
	for i := 0; i < n; i++ {
		c.walk(reflect.NewAt(et, ptrOf(addr+uintptr(i)*et.Size())).Elem(), desc, seen)
	}
}
