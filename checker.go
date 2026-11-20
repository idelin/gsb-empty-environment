package cgocheck

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"unsafe"
)

const (
	wordSize       = uintptr(unsafe.Sizeof(uintptr(0)))
	maxWalkedElems = uintptr(1 << 16)
)

// GoObject describes a Go allocation registered with a Checker.
type GoObject struct {
	ID   uint64
	Addr uintptr
	Size uintptr
	Type reflect.Type
	Name string

	ptr unsafe.Pointer
}

// CBlock describes a registered block of C-allocated memory.
type CBlock struct {
	ID     uint64
	Addr   uintptr
	Size   uintptr
	Name   string
	Fields Layout

	ptr unsafe.Pointer
}

// HeldPointer is one entry in the table of pointers C retained.
type HeldPointer struct {
	Address uintptr
	Pointer unsafe.Pointer
	Label   string
}

// Checker tracks Go allocations, C blocks, and pointers retained by C.
//
// A Checker is safe for concurrent use.
type Checker struct {
	mu      sync.RWMutex
	nextID  uint64
	objects []*GoObject
	ranges  []*goRange
	cblocks []*CBlock
	holds   map[uintptr]HeldPointer
	lastErr error
}

// Default is a process-wide Checker used by the package-level helpers.
var Default = New()

// New returns an empty Checker.
func New() *Checker {
	return &Checker{holds: make(map[uintptr]HeldPointer)}
}

type rangeKind uint8

const (
	rangeObject rangeKind = iota
	rangeField
)

type goRange struct {
	object *GoObject
	offset uintptr
	size   uintptr
	typ    reflect.Type
	kind   rangeKind
	name   string
	parent *goRange
	depth  int

	array    bool
	elemType reflect.Type
	elemSize uintptr
	count    uintptr
}

func (r *goRange) contains(addr uintptr) bool {
	if r == nil || r.size == 0 {
		return false
	}
	start := r.start()
	return addr >= start && addr-start < r.size
}

func (r *goRange) start() uintptr {
	return uintptr(r.startPtr())
}

func (r *goRange) startPtr() unsafe.Pointer {
	if r == nil || r.object == nil || r.object.ptr == nil {
		return nil
	}
	return unsafe.Add(r.object.ptr, r.offset)
}

type visitKey struct {
	addr  uintptr
	typ   reflect.Type
	array bool
	count uintptr
}

type sliceHeader struct {
	Data unsafe.Pointer
	Len  int
	Cap  int
}

type stringHeader struct {
	Data unsafe.Pointer
	Len  int
}

// RegisterGo registers one or more roots of the current Go object graph.
//
// Pointers and slices are the usual roots.  A pointer registers the object it
// points to; a slice registers its backing array.  Arrays and structs passed
// by value must be addressable, so pass a pointer to them.
func (c *Checker) RegisterGo(values ...any) error {
	if c == nil {
		return fmt.Errorf("cgocheck: nil Checker")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, value := range values {
		if _, err := c.registerGoValueLocked(value, ""); err != nil {
			c.lastErr = err
			return err
		}
	}
	c.lastErr = nil
	return nil
}

// TrackGo is an alias for RegisterGo.
func (c *Checker) TrackGo(values ...any) error {
	return c.RegisterGo(values...)
}

// RegisterGoObject registers a statically typed object at addr.
func (c *Checker) RegisterGoObject(addr unsafe.Pointer, t reflect.Type) (*GoObject, error) {
	if c == nil {
		return nil, fmt.Errorf("cgocheck: nil Checker")
	}
	if t == nil {
		return nil, fmt.Errorf("cgocheck: nil Go object type")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	obj, err := c.registerGoTypeLocked(t, addr, "", make(map[visitKey]bool))
	c.lastErr = err
	return obj, err
}

// RegisterGoAt is an alias for RegisterGoObject.
func (c *Checker) RegisterGoAt(addr unsafe.Pointer, t reflect.Type) (*GoObject, error) {
	return c.RegisterGoObject(addr, t)
}

func (c *Checker) registerGoValueLocked(value any, name string) (*GoObject, error) {
	if value == nil {
		return nil, nil
	}
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return nil, nil
	}

	switch rv.Kind() {
	case reflect.Ptr:
		if rv.IsNil() {
			return nil, nil
		}
		return c.registerGoTypeLocked(rv.Type().Elem(), rv.UnsafePointer(), name, make(map[visitKey]bool))

	case reflect.Slice:
		elem := rv.Type().Elem()
		count := rv.Cap()
		if count == 0 || rv.IsNil() {
			return nil, nil
		}
		return c.registerSyntheticArrayLocked(elem, rv.UnsafePointer(), uintptr(count), name, make(map[visitKey]bool))

	case reflect.String:
		s := rv.String()
		if len(s) == 0 {
			return nil, nil
		}
		byteType := reflect.TypeOf(byte(0))
		return c.registerSyntheticArrayLocked(byteType, unsafe.Pointer(unsafe.StringData(s)), uintptr(len(s)), name, make(map[visitKey]bool))

	case reflect.Array, reflect.Struct, reflect.Interface:
		return nil, fmt.Errorf("cgocheck: %s value is not addressable; pass a pointer", rv.Type())

	case reflect.UnsafePointer:
		return nil, fmt.Errorf("cgocheck: cannot infer the type of an unsafe.Pointer; use RegisterGoObject")

	default:
		return nil, fmt.Errorf("cgocheck: cannot register non-addressable %s value; pass a pointer", rv.Type())
	}
}

func (c *Checker) registerGoTypeLocked(t reflect.Type, addr unsafe.Pointer, name string, visited map[visitKey]bool) (*GoObject, error) {
	if t == nil {
		return nil, fmt.Errorf("cgocheck: nil Go object type")
	}
	if addr == nil {
		return nil, nil
	}
	addrValue := uintptr(addr)
	if c.inCBlockLocked(addrValue) != nil {
		return nil, nil
	}
	if c.findGoRangeLocked(addrValue) != nil {
		return nil, nil
	}

	key := visitKey{addr: addrValue, typ: t}
	if visited[key] {
		return nil, nil
	}

	c.nextID++
	obj := &GoObject{
		ID:   c.nextID,
		Addr: addrValue,
		Size: t.Size(),
		Type: t,
		Name: objectName(name, addrValue),
		ptr:  addr,
	}
	c.objects = append(c.objects, obj)

	root := c.addRangeLocked(obj, addr, obj.Size, t, rangeObject, name, nil)
	if root != nil {
		c.walkNodeLocked(t, addr, root, obj, visited)
	}
	return obj, nil
}

func (c *Checker) registerSyntheticArrayLocked(elem reflect.Type, addr unsafe.Pointer, count uintptr, name string, visited map[visitKey]bool) (*GoObject, error) {
	if elem == nil || addr == nil || count == 0 {
		return nil, nil
	}
	addrValue := uintptr(addr)
	if c.inCBlockLocked(addrValue) != nil {
		return nil, nil
	}
	if c.findGoRangeLocked(addrValue) != nil {
		return nil, nil
	}

	// Keep the synthetic allocation key distinct from the key used for its
	// first element.  With a slice such as [][]byte, both addresses can be the
	// same address as seen by a nested walk, and sharing the key would skip the
	// first element entirely.
	key := visitKey{addr: addrValue, array: true, count: count}
	if visited[key] {
		return nil, nil
	}
	visited[key] = true

	elemSize := elem.Size()
	if elemSize != 0 && count > ^uintptr(0)/elemSize {
		return nil, fmt.Errorf("cgocheck: synthetic array size overflows uintptr")
	}
	size := elemSize * count

	c.nextID++
	obj := &GoObject{
		ID:   c.nextID,
		Addr: addrValue,
		Size: size,
		Name: objectName(name, addrValue),
		ptr:  addr,
	}
	c.objects = append(c.objects, obj)

	root := c.addRangeLocked(obj, addr, size, nil, rangeObject, name, nil)
	if root == nil {
		return obj, nil
	}
	root.array = true
	root.elemType = elem
	root.elemSize = elemSize
	root.count = count
	c.walkArrayElementsLocked(elem, addr, root, obj, visited, count)
	return obj, nil
}

func (c *Checker) addRangeLocked(obj *GoObject, addr unsafe.Pointer, size uintptr, t reflect.Type, kind rangeKind, name string, parent *goRange) *goRange {
	if size == 0 || addr == nil {
		return nil
	}
	r := &goRange{
		object: obj,
		offset: uintptr(addr) - obj.Addr,
		size:   size,
		typ:    t,
		kind:   kind,
		name:   name,
		parent: parent,
	}
	if parent != nil {
		r.depth = parent.depth + 1
	}
	if t != nil && t.Kind() == reflect.Array {
		r.array = true
		r.elemType = t.Elem()
		r.elemSize = r.elemType.Size()
		r.count = uintptr(t.Len())
	}
	c.ranges = append(c.ranges, r)
	return r
}

func (c *Checker) walkNodeLocked(t reflect.Type, addr unsafe.Pointer, r *goRange, obj *GoObject, visited map[visitKey]bool) {
	if t == nil || addr == nil {
		return
	}
	key := visitKey{addr: uintptr(addr), typ: t, array: r != nil && r.array, count: rCount(r)}
	if visited[key] {
		return
	}
	visited[key] = true

	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			fieldAddr := unsafe.Add(addr, field.Offset)
			fieldRange := c.addRangeLocked(obj, fieldAddr, field.Type.Size(), field.Type, rangeField, field.Name, r)
			c.walkNodeLocked(field.Type, fieldAddr, fieldRange, obj, visited)
		}

	case reflect.Array:
		c.walkArrayElementsLocked(t.Elem(), addr, r, obj, visited, uintptr(t.Len()))

	case reflect.Ptr:
		c.followPointerLocked(t.Elem(), addr, obj, visited)

	case reflect.Slice:
		c.followSliceLocked(t.Elem(), addr, obj, visited)

	case reflect.String:
		c.followStringLocked(addr, obj, visited)
	}
}

func (c *Checker) walkArrayElementsLocked(elem reflect.Type, addr unsafe.Pointer, parent *goRange, obj *GoObject, visited map[visitKey]bool, count uintptr) {
	if elem == nil {
		return
	}
	elemSize := elem.Size()
	if elemSize == 0 {
		return
	}
	limit := count
	if limit > maxWalkedElems {
		limit = maxWalkedElems
	}
	for i := uintptr(0); i < limit; i++ {
		c.walkNodeLocked(elem, unsafe.Add(addr, i*elemSize), parent, obj, visited)
	}
}

func (c *Checker) followPointerLocked(elem reflect.Type, slotAddr unsafe.Pointer, obj *GoObject, visited map[visitKey]bool) {
	p := *(*unsafe.Pointer)(slotAddr)
	if p == nil {
		return
	}
	target := uintptr(p)
	if c.inCBlockLocked(target) != nil || c.findGoRangeLocked(target) != nil {
		return
	}
	_, _ = c.registerGoTypeLocked(elem, p, "", visited)
}

func (c *Checker) followSliceLocked(elem reflect.Type, slotAddr unsafe.Pointer, obj *GoObject, visited map[visitKey]bool) {
	h := (*sliceHeader)(slotAddr)
	if h.Data == nil || h.Cap == 0 {
		return
	}
	target := uintptr(h.Data)
	if c.inCBlockLocked(target) != nil || c.findGoRangeLocked(target) != nil {
		return
	}
	_, _ = c.registerSyntheticArrayLocked(elem, h.Data, uintptr(h.Cap), "", visited)
}

func (c *Checker) followStringLocked(slotAddr unsafe.Pointer, obj *GoObject, visited map[visitKey]bool) {
	h := (*stringHeader)(slotAddr)
	if h.Data == nil || h.Len == 0 {
		return
	}
	target := uintptr(h.Data)
	if c.inCBlockLocked(target) != nil || c.findGoRangeLocked(target) != nil {
		return
	}
	byteType := reflect.TypeOf(byte(0))
	_, _ = c.registerSyntheticArrayLocked(byteType, h.Data, uintptr(h.Len), "", visited)
}

func rCount(r *goRange) uintptr {
	if r == nil {
		return 0
	}
	return r.count
}

func objectName(name string, addr uintptr) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("Go allocation 0x%x", addr)
}

// RegisterCBlock registers a C-allocated block and its field layout.
//
// Layout arguments may be Field values, a Layout, a []Field, or a reflect.Type
// describing a Go mirror of the C type.
func (c *Checker) RegisterCBlock(ptr unsafe.Pointer, size uintptr, layout ...any) *CBlock {
	if c == nil || ptr == nil {
		return nil
	}
	fields, err := normalizeLayout(layout)
	if err != nil {
		c.mu.Lock()
		c.lastErr = err
		c.mu.Unlock()
		return nil
	}
	if size == 0 {
		size = inferredBlockSize(fields)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	addr := uintptr(ptr)
	block := &CBlock{
		ID:     c.nextID,
		Addr:   addr,
		Size:   size,
		Name:   fmt.Sprintf("C block 0x%x", addr),
		Fields: fields,
		ptr:    ptr,
	}
	c.cblocks = append(c.cblocks, block)
	c.lastErr = nil
	return block
}

// RegisterC is a typed convenience wrapper around RegisterCBlock.
func (c *Checker) RegisterC(ptr unsafe.Pointer, size uintptr, fields ...Field) *CBlock {
	items := make([]any, len(fields))
	for i, field := range fields {
		items[i] = field
	}
	return c.RegisterCBlock(ptr, size, items...)
}

// RegisterCBlockNamed registers a C block with a useful diagnostic name.
func (c *Checker) RegisterCBlockNamed(name string, ptr unsafe.Pointer, size uintptr, layout ...any) *CBlock {
	block := c.RegisterCBlock(ptr, size, layout...)
	if block != nil {
		c.mu.Lock()
		block.Name = name
		c.mu.Unlock()
	}
	return block
}

func inferredBlockSize(fields []Field) uintptr {
	var size uintptr
	for _, field := range fields {
		end := field.Offset + field.Size
		if field.Type != nil {
			if typeSize := field.Type.Size(); end < field.Offset+typeSize {
				end = field.Offset + typeSize
			}
		}
		if field.Pointer || field.HasPointer || field.IsPointer {
			count := field.Count
			if count == 0 {
				count = 1
			}
			if ptrEnd := field.Offset + count*wordSize; ptrEnd > end {
				end = ptrEnd
			}
		}
		if end > size {
			size = end
		}
	}
	return size
}

// CheckBefore checks every argument before it is handed to C.
func (c *Checker) CheckBefore(ptrs ...unsafe.Pointer) error {
	return ruleError(c.InspectBefore(ptrs...))
}

// ValidateBefore is an alias for CheckBefore.
func (c *Checker) ValidateBefore(ptrs ...unsafe.Pointer) error {
	return c.CheckBefore(ptrs...)
}

// InspectBefore returns all violations for the supplied argument pointers.
func (c *Checker) InspectBefore(ptrs ...unsafe.Pointer) []Violation {
	if c == nil {
		return []Violation{{Kind: ViolationUnregisteredArgument, Message: "nil Checker"}}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	var violations []Violation
	seen := make(map[string]bool)
	for _, ptr := range ptrs {
		if ptr == nil {
			continue
		}
		for _, v := range c.inspectPointerLocked(uintptr(ptr)) {
			key := fmt.Sprintf("%s:%x:%x:%s", v.Kind, v.Pointer, v.Target, v.Field)
			if !seen[key] {
				seen[key] = true
				violations = append(violations, v)
			}
		}
	}
	return violations
}

func (c *Checker) inspectPointerLocked(addr uintptr) []Violation {
	if c.inCBlockLocked(addr) != nil {
		return nil
	}
	scope := c.scopeForLocked(addr)
	if scope == nil {
		return []Violation{{
			Kind:    ViolationUnregisteredArgument,
			Pointer: addr,
			Message: "pointer is not in a registered Go allocation or C block",
		}}
	}
	return c.scanScopeLocked(scope)
}

func (c *Checker) scanScopeLocked(scope *goRange) []Violation {
	if scope == nil {
		return nil
	}
	var violations []Violation
	emit := func(slotAddr unsafe.Pointer, path string, force bool) {
		p := *(*unsafe.Pointer)(slotAddr)
		if p == nil {
			return
		}
		target := uintptr(p)
		if !force && c.inCBlockLocked(target) != nil {
			return
		}
		kind := ViolationUnknownPointer
		message := "pointer word refers to unregistered memory"
		if force {
			kind = ViolationGoPointerInGoMemory
			message = "Go runtime/interface state is stored in memory passed to C"
		} else if c.findGoRangeLocked(target) != nil {
			kind = ViolationGoPointerInGoMemory
			message = "Go memory passed to C contains another Go pointer"
		}
		violations = append(violations, Violation{
			Kind:    kind,
			Pointer: uintptr(slotAddr),
			Target:  target,
			Object:  scope.objectName(),
			Field:   path,
			Message: message,
		})
	}

	if scope.array && scope.elemType != nil {
		if !typeHasPointers(scope.elemType) || scope.elemSize == 0 {
			return nil
		}
		for i := uintptr(0); i < scope.count; i++ {
			c.collectTypeSlotsLocked(scope.elemType, unsafe.Add(scope.startPtr(), i*scope.elemSize), fmt.Sprintf("%s[%d]", scope.name, i), emit)
		}
		return violations
	}

	if scope.typ != nil && typeHasPointers(scope.typ) {
		c.collectTypeSlotsLocked(scope.typ, scope.startPtr(), scope.name, emit)
	}
	return violations
}

func (r *goRange) objectName() string {
	if r == nil || r.object == nil {
		return ""
	}
	return r.object.Name
}

// CheckAfter checks pointers C retained after a call returned.
func (c *Checker) CheckAfter() error {
	return ruleError(c.InspectAfter())
}

// ValidateAfter is an alias for CheckAfter.
func (c *Checker) ValidateAfter() error {
	return c.CheckAfter()
}

// InspectAfter returns all retained-Go-pointer violations.
func (c *Checker) InspectAfter() []Violation {
	if c == nil {
		return []Violation{{Kind: ViolationRetainedGoPointer, Message: "nil Checker"}}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	var violations []Violation
	seen := make(map[string]bool)
	add := func(v Violation) {
		key := fmt.Sprintf("%s:%x:%x:%s", v.Kind, v.Pointer, v.Target, v.Field)
		if seen[key] {
			return
		}
		seen[key] = true
		violations = append(violations, v)
	}

	for _, held := range c.holds {
		addr := uintptr(held.Pointer)
		if c.findGoRangeLocked(addr) == nil {
			continue
		}
		add(Violation{
			Kind:    ViolationRetainedGoPointer,
			Pointer: addr,
			Target:  addr,
			Object:  c.goObjectNameLocked(addr),
			Message: held.Label,
		})
	}

	for _, block := range c.cblocks {
		c.collectCBlockSlotsLocked(block, func(slotAddr unsafe.Pointer, path string) {
			p := *(*unsafe.Pointer)(slotAddr)
			if p == nil {
				return
			}
			target := uintptr(p)
			if c.inCBlockLocked(target) != nil {
				return
			}
			if c.findGoRangeLocked(target) == nil {
				return
			}
			add(Violation{
				Kind:    ViolationRetainedGoPointer,
				Pointer: uintptr(slotAddr),
				Target:  target,
				Object:  block.Name,
				Field:   path,
				Message: "C block field still refers into Go memory",
			})
		})
	}
	return violations
}

// CollectRetained is an alias for InspectAfter.
func (c *Checker) CollectRetained() []Violation {
	return c.InspectAfter()
}

func (c *Checker) goObjectNameLocked(addr uintptr) string {
	scope := c.scopeForLocked(addr)
	if scope != nil {
		return scope.objectName()
	}
	return ""
}

// Hold records a pointer retained by C, typically from a C callback.
func (c *Checker) Hold(ptr unsafe.Pointer) {
	c.HoldLabel(ptr, "")
}

// HoldLabel records a pointer retained by C together with a diagnostic label.
func (c *Checker) HoldLabel(ptr unsafe.Pointer, label string) {
	if c == nil || ptr == nil {
		return
	}
	addr := uintptr(ptr)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.holds[addr] = HeldPointer{Address: addr, Pointer: ptr, Label: label}
}

// CStored is an alias for Hold, matching wrapper terminology.
func (c *Checker) CStored(ptr unsafe.Pointer) {
	c.Hold(ptr)
}

// Release removes one pointer from C's retained-pointer table.
func (c *Checker) Release(ptr unsafe.Pointer) {
	if c == nil || ptr == nil {
		return
	}
	c.mu.Lock()
	for key, held := range c.holds {
		if uintptr(held.Pointer) == uintptr(ptr) {
			delete(c.holds, key)
		}
	}
	c.mu.Unlock()
}

// ClearHolds removes every entry from C's retained-pointer table.
func (c *Checker) ClearHolds() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.holds = make(map[uintptr]HeldPointer)
	c.mu.Unlock()
}

// Held returns a stable snapshot of C's retained-pointer table.
func (c *Checker) Held() []HeldPointer {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]HeldPointer, 0, len(c.holds))
	for _, held := range c.holds {
		out = append(out, held)
	}
	sort.Slice(out, func(i, j int) bool { return uintptr(out[i].Pointer) < uintptr(out[j].Pointer) })
	return out
}

// LastError returns the last registration/layout error.
func (c *Checker) LastError() error {
	if c == nil {
		return fmt.Errorf("cgocheck: nil Checker")
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastErr
}

func (c *Checker) inCBlockLocked(addr uintptr) *CBlock {
	for _, block := range c.cblocks {
		start := uintptr(block.ptr)
		if block.Size == 0 {
			if addr == start {
				return block
			}
			continue
		}
		if addr >= start && addr-start < block.Size {
			return block
		}
	}
	return nil
}

func (c *Checker) findGoRangeLocked(addr uintptr) *goRange {
	var found *goRange
	for _, r := range c.ranges {
		if !r.contains(addr) {
			continue
		}
		if found == nil || r.depth > found.depth || (r.depth == found.depth && r.size < found.size) {
			found = r
		}
	}
	return found
}

func (c *Checker) scopeForLocked(addr uintptr) *goRange {
	var best *goRange
	var array *goRange
	for _, r := range c.ranges {
		if !r.contains(addr) {
			continue
		}
		if best == nil || r.depth > best.depth || (r.depth == best.depth && r.size < best.size) {
			best = r
		}
		start := r.start()
		if r.array && r.elemSize != 0 && (addr-start)%r.elemSize == 0 {
			if array == nil || r.depth < array.depth || (r.depth == array.depth && r.size > array.size) {
				array = r
			}
		}
	}
	if array != nil {
		return array
	}
	return best
}

func (c *Checker) collectTypeSlotsLocked(t reflect.Type, base unsafe.Pointer, path string, emit func(unsafe.Pointer, string, bool)) {
	if t == nil {
		return
	}
	switch t.Kind() {
	case reflect.Ptr, reflect.UnsafePointer:
		emit(base, path, false)

	case reflect.Slice:
		emit(base, path+".data", false)

	case reflect.String:
		emit(base, path+".data", false)

	case reflect.Interface:
		emit(base, path+".type", true)
		emit(unsafe.Add(base, wordSize), path+".data", true)

	case reflect.Map, reflect.Chan, reflect.Func:
		emit(base, path, true)

	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			fieldPath := field.Name
			if path != "" {
				fieldPath = path + "." + field.Name
			}
			c.collectTypeSlotsLocked(field.Type, unsafe.Add(base, field.Offset), fieldPath, emit)
		}

	case reflect.Array:
		elem := t.Elem()
		if !typeHasPointers(elem) || elem.Size() == 0 {
			return
		}
		for i := 0; i < t.Len(); i++ {
			elemPath := fmt.Sprintf("%s[%d]", path, i)
			c.collectTypeSlotsLocked(elem, unsafe.Add(base, uintptr(i)*elem.Size()), elemPath, emit)
		}
	}
}

func (c *Checker) collectCBlockSlotsLocked(block *CBlock, emit func(unsafe.Pointer, string)) {
	if block == nil || block.Size == 0 {
		return
	}
	seen := make(map[uintptr]bool)
	start := block.ptr
	add := func(addr unsafe.Pointer, path string) {
		addrValue := uintptr(addr)
		startValue := uintptr(start)
		if addrValue < startValue || addrValue-startValue >= block.Size || seen[addrValue] {
			return
		}
		seen[addrValue] = true
		emit(addr, path)
	}
	for _, field := range block.Fields {
		base := unsafe.Add(start, field.Offset)
		if field.Type != nil {
			c.collectTypeSlotsLocked(field.Type, base, field.Name, func(addr unsafe.Pointer, path string, _ bool) {
				add(addr, path)
			})
		}
		if field.Pointer || field.HasPointer || field.IsPointer {
			count := field.Count
			if count == 0 {
				count = 1
			}
			for i := uintptr(0); i < count; i++ {
				path := field.Name
				if count > 1 {
					path = fmt.Sprintf("%s[%d]", field.Name, i)
				}
				add(unsafe.Add(base, i*wordSize), path)
			}
		}
	}
}

var pointerTypeCache sync.Map // map[reflect.Type]bool

func typeHasPointers(t reflect.Type) bool {
	if t == nil {
		return false
	}
	if cached, ok := pointerTypeCache.Load(t); ok {
		return cached.(bool)
	}
	result := false
	switch t.Kind() {
	case reflect.Ptr, reflect.UnsafePointer, reflect.Slice, reflect.String,
		reflect.Interface, reflect.Map, reflect.Chan, reflect.Func:
		result = true
	case reflect.Struct:
		for i := 0; i < t.NumField() && !result; i++ {
			result = typeHasPointers(t.Field(i).Type)
		}
	case reflect.Array:
		result = typeHasPointers(t.Elem())
	}
	pointerTypeCache.Store(t, result)
	return result
}

// Package-level helpers use Default.

func RegisterGo(values ...any) error { return Default.RegisterGo(values...) }

func TrackGo(values ...any) error { return Default.RegisterGo(values...) }

func RegisterGoObject(addr unsafe.Pointer, t reflect.Type) (*GoObject, error) {
	return Default.RegisterGoObject(addr, t)
}

func RegisterCBlock(ptr unsafe.Pointer, size uintptr, layout ...any) *CBlock {
	return Default.RegisterCBlock(ptr, size, layout...)
}

func RegisterC(ptr unsafe.Pointer, size uintptr, fields ...Field) *CBlock {
	return Default.RegisterC(ptr, size, fields...)
}

func CheckBefore(ptrs ...unsafe.Pointer) error { return Default.CheckBefore(ptrs...) }

func CheckAfter() error { return Default.CheckAfter() }

func Hold(ptr unsafe.Pointer) { Default.Hold(ptr) }

func Release(ptr unsafe.Pointer) { Default.Release(ptr) }

func ClearHolds() { Default.ClearHolds() }
