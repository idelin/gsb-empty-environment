package netsec

import (
	"strings"
	"testing"
	"unsafe"
)

func mustViolation(t *testing.T, err error, rule string) *Violation {
	t.Helper()
	if err == nil {
		t.Fatalf("期望违规 %s，实际通过", rule)
	}
	v, ok := err.(*Violation)
	if !ok {
		t.Fatalf("期望 *Violation，实际 %T: %v", err, err)
	}
	if v.Rule != rule {
		t.Fatalf("期望规则 %s，实际 %s: %v", rule, v.Rule, v)
	}
	return v
}

// 嵌套切片：结构体含 [][]int32，整体与切片字段违规，纯数据字段与内层数据通过。
func TestNestedSlices(t *testing.T) {
	chk := NewChecker()
	type Packet struct {
		Payload  [][]int32
		Checksum uint64
	}
	p := &Packet{Payload: [][]int32{{1, 2, 3}, {4, 5}}, Checksum: 99}
	chk.RegisterGo("packet", p)

	// 整体传给 C：结构体内含切片头（指向 Go 底层数组）→ 违规。
	mustViolation(t, chk.CheckArg(unsafe.Pointer(p)), RuleGoPointerInArg)

	// 只传纯数据字段：只检查该字段，Payload 的 Go 指针不影响 → 通过。
	if err := chk.CheckArg(unsafe.Pointer(&p.Checksum)); err != nil {
		t.Fatalf("纯数据字段应通过: %v", err)
	}

	// 指向外层切片元素：检查整个外层数组，元素为切片头 → 违规。
	mustViolation(t, chk.CheckArg(unsafe.Pointer(&p.Payload[0])), RuleGoPointerInArg)

	// 内层切片底层数组为纯数据 → 通过。
	inner := p.Payload[0]
	if err := chk.CheckArg(unsafe.Pointer(&inner[0])); err != nil {
		t.Fatalf("内层纯数据数组应通过: %v", err)
	}
}

// 结构字段范围：指向字段时只检查该字段占用的内存。
func TestStructFieldScope(t *testing.T) {
	chk := NewChecker()
	type Rec struct {
		Name *string
		ID   uint64
	}
	s := "hello"
	r := &Rec{Name: &s, ID: 7}
	chk.RegisterGo("rec", r)

	// 整体与指针字段：含 Go 指针 → 违规。
	mustViolation(t, chk.CheckArg(unsafe.Pointer(r)), RuleGoPointerInArg)
	mustViolation(t, chk.CheckArg(unsafe.Pointer(&r.Name)), RuleGoPointerInArg)

	// 纯数据字段：只检查 ID 的 8 字节，Name 的 Go 指针不影响 → 通过。
	if err := chk.CheckArg(unsafe.Pointer(&r.ID)); err != nil {
		t.Fatalf("纯数据字段应通过: %v", err)
	}
}

// 数组元素：指向元素时检查整个数组。
func TestArrayElement(t *testing.T) {
	chk := NewChecker()

	arr := [4]uint32{10, 20, 30, 40}
	chk.RegisterGo("arr", &arr)
	if err := chk.CheckArg(unsafe.Pointer(&arr[2])); err != nil {
		t.Fatalf("纯数据数组元素应通过: %v", err)
	}

	x, y := 1, 2
	parr := [2]*int{&x, &y}
	chk.RegisterGo("parr", &parr)
	// 即使 &parr[1] 之后的元素含 Go 指针，也要检查整个数组。
	mustViolation(t, chk.CheckArg(unsafe.Pointer(&parr[1])), RuleGoPointerInArg)
}

// 只含 C 指针的结构：字段均指向已登记 C 块 → 通过。
func TestCOnlyStruct(t *testing.T) {
	chk := NewChecker()
	cbuf := make([]byte, 64) // 测试宿主模拟 C.malloc 分配的块
	chk.RegisterCBlock(unsafe.Pointer(&cbuf[0]), 64, "c-block")

	type COnly struct {
		Buf unsafe.Pointer
		Out unsafe.Pointer
		N   uint64
	}
	s := &COnly{
		Buf: unsafe.Pointer(&cbuf[0]),
		Out: unsafe.Pointer(&cbuf[32]),
		N:   3,
	}
	chk.RegisterGo("conly", s)

	if err := chk.CheckArg(unsafe.Pointer(s)); err != nil {
		t.Fatalf("只含 C 指针的结构应通过: %v", err)
	}
	if err := chk.CheckArg(unsafe.Pointer(&cbuf[16])); err != nil {
		t.Fatalf("C 块内部指针应通过: %v", err)
	}
}

// 纯数据数组：byte/float64 切片任意元素均通过。
func TestPureDataArray(t *testing.T) {
	chk := NewChecker()
	data := []float64{1.5, 2.5, 3.5, 4.5}
	chk.RegisterGo("data", data)
	for i := range data {
		if err := chk.CheckArg(unsafe.Pointer(&data[i])); err != nil {
			t.Fatalf("纯数据数组元素 %d 应通过: %v", i, err)
		}
	}
}

// C 端持有表：返回后仍引用 Go 对象 → 违规；释放后或通过 C 块 → 通过。
func TestRetainedTable(t *testing.T) {
	chk := NewChecker()
	obj := &struct{ V uint64 }{42}
	chk.RegisterGo("obj", obj)
	cbuf := make([]byte, 16)
	chk.RegisterCBlock(unsafe.Pointer(&cbuf[0]), 16, "c-block")

	// C 回调保存 Go 指针。
	chk.CRetain(unsafe.Pointer(obj), "c-callback")
	// C 保存自己的块指针：不算违规。
	chk.CRetain(unsafe.Pointer(&cbuf[0]), "c-cache")

	errs := chk.CheckRetained()
	if len(errs) != 1 {
		t.Fatalf("期望 1 个违规，实际 %d: %v", len(errs), errs)
	}
	v := mustViolation(t, errs[0], RuleCRetainsGo)
	if !strings.Contains(v.Detail, "c-callback") {
		t.Fatalf("违规详情应含来源: %v", v)
	}

	chk.CRelease(unsafe.Pointer(obj))
	if errs := chk.CheckRetained(); len(errs) != 0 {
		t.Fatalf("释放后不应有违规: %v", errs)
	}
}

// 未登记指针：既非 Go 对象也非 C 块 → 违规。
func TestUnregisteredPointer(t *testing.T) {
	chk := NewChecker()
	var x uint64
	mustViolation(t, chk.CheckArg(unsafe.Pointer(&x)), RuleUnregisteredPtr)
}
