# cgo指针传递规则校验

Go 与 C 交接内存时，传给 C 的 Go 指针所指内存不得再含 Go 指针，C 也不得在调用返回后保留 Go 指针。为Go测试宿主实现供 cgo 包装层调用的指针规则检查包：登记 Go 分配的对象图、C 分配块和字段布局；调用 C 前检查被传入指针指向的内存不含 Go 指针，指向结构字段时只检查该字段占用的内存，指向数组元素时检查整个数组；返回后 C 端持有表中若仍引用 Go 对象，报告违规。用嵌套切片、C 回调保存指针、只含 C 指针的结构和纯数据数组验证。

## 用法

```go
chk := netsec.NewChecker()
chk.RegisterGo("obj", &obj)          // 登记 Go 对象图（递归可达对象、字段布局）
chk.RegisterCBlock(cptr, size, "C.malloc") // 登记 C 分配块

if err := chk.CheckArg(ptr); err != nil { /* 调用前检查 */ }
cCall(ptr)                            // C 侧保存指针时经回调进入 CRetain
if errs := chk.CheckRetained(); len(errs) > 0 { /* 返回后检查持有表 */ }
```

## 文件

- `cgocheck.go`：检查器核心（纯 Go，保守扫描已登记对象图）。
- `cgocheck_test.go`：嵌套切片、字段范围、数组元素、C 指针结构、纯数据数组、持有表。
- `cgo_bridge.go` / `cgo_export.go` / `cgo_bridge_test.go`：真实 cgo 链路验证（C 回调保存指针、`C.malloc` 块），仅在启用 cgo 时构建。

运行测试：`go test ./...`
