Go 与 C 交接内存时，传给 C 的 Go 指针所指内存不得再含 Go 指针，C 也不得在调用返回后保留 Go 指针。本包为 Go 测试宿主和 cgo 包装层提供可登记的指针规则检查器：

- 自动遍历并登记 Go 对象图，包括结构体字段、指针目标、切片后备数组和字符串数据。
- 登记 C 分配块及其字段布局，用于区分 C 指针和发现 C 端遗留的 Go 指针。
- 调用 C 前检查参数指向的 Go 内存。指向结构体字段时只扫描该字段，指向数组元素时扫描整个数组。
- 调用返回后检查显式持有表和已登记 C 块中的指针字段。仍指向 Go 内存即报告违规。

## 使用方式

```go
package host

import (
	"unsafe"

	"prescreen.local/ddd-reczz28zozloar1t"
)

func callC(checker *cgocheck.Checker, value *Payload, cBuffer unsafe.Pointer) error {
	checker.RegisterCBlock(cBuffer, 64,
		cgocheck.PtrField("next", 0),
		cgocheck.DataField("length", 8, 8),
	)
	if err := checker.RegisterGo(value); err != nil {
		return err
	}

	call, err := checker.BeginCall(unsafe.Pointer(&value.Data[0]))
	if err != nil {
		return err // do not enter C
	}

	// C callback: savePointer is called with a Go pointer.
	savePointer := func(p unsafe.Pointer) {
		call.HoldLabel(p, "callback stored pointer")
	}
	_ = savePointer

	// Invoke the actual cgo wrapper here.
	return call.End()
}
```

## 布局

`Field` 可以直接描述指针字段，也可以携带 Go 镜像类型，由包递归找出其中的指针槽：

```go
import "reflect"

type mirror struct {
	Next  unsafe.Pointer
	Value int64
}

checker.RegisterCBlock(ptr, 128, cgocheck.TypeLayout(reflect.TypeOf(mirror{})))
```

`PtrField`/`PointerArrayField` 适合没有 Go 镜像类型的 C 结构。`DataField` 标记非指针字段，主要用于表达偏移和块大小。

## 规则

- 指针落在已登记 C 块内时视为 C 指针。
- 指针槽指向已登记 Go 内存时，调用前检查失败。
- 指针槽指向未登记内存时也报告，因为宿主无法证明它不是 Go 指针；C 分配块应显式登记。
- 若 Go 对象图中含 C 指针，请先登记对应 C 块，再登记 Go 对象图。
- `uintptr` 字段不按指针扫描；需要遵守该规则时请使用 `unsafe.Pointer` 或具体指针类型。
- 指针指向结构体字段起始地址时，只检查该字段；指向数组元素（或切片元素）时，检查整个数组/后备数组。
