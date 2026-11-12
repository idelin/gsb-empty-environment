# unsafe.Pointer转换范围检查

解析器里手写的 unsafe 偏移计算会在攻击者给定的长度字段下越界读写。为Go测试宿主实现供插桩代码调用的指针转换检查包：登记每次分配的基址、大小与元素布局，校验 unsafe.Add 或切片重解释之后的整个目标类型落在同一分配内且按类型对齐；指针算术的中间值存入 uintptr 后若越过已登记的垃圾回收安全点再转回指针，一律报告。用长度字段溢出、跨相邻对象、未对齐和合法偏移验证。

## 设计

`netsec` 包维护一个分配注册表。每条记录包含：

- 基址和字节大小；
- 元素 `Layout`（元素大小与类型对齐）；
- 单调递增的分配 ID。

检查顺序为：先检查 `uintptr` 加法/乘法溢出，再查找源指针所属分配，最后确认目标区间
`[addr, addr+target.Size)` 完全落在**同一个**分配内，且 `addr` 满足目标类型的对齐要求。
长度为零的切片/目标只允许落在分配内部或恰好位于分配末尾。

相邻对象会被建模为两个独立注册的分配。即使偏移结果落进第二个对象，只要算术起点在
第一个对象内，检查仍会报告 `cross_allocation`。

## API

创建隔离的检查器，或使用包级 `Default` 便捷函数：

```go
checker := netsec.NewChecker()

base := unsafe.Pointer(&buf[0])
if _, err := checker.Register(base, uintptr(len(buf)), netsec.LayoutOf[byte]()); err != nil {
    // 登记失败
}
```

校验并执行 `unsafe.Add` / `unsafe.Slice` 重解释：

```go
p, err := checker.Add(base, headerOffset, netsec.LayoutOf[uint32]())
if err != nil {
    // 越界、跨对象、未对齐或长度溢出
}

seconds, err := netsec.ReinterpretSlice[uint16](checker, p, count)
if err != nil {
    // count*2 溢出，或目标不在同一分配内
}
```

`Add`、`Slice`、`SliceAt`、`ReinterpretSlice` 在成功时返回校验过的值；
`CheckAdd`、`CheckSlice`、`CheckSliceAt`、`CheckUintptrToPointer` 只返回 `error`。
所有被拒绝的转换也会追加到 `Checker.Violations()`。

来自线报文的 `uint64` 长度/偏移可直接使用 `AddUint64`、`SliceUint64`、
`SliceAtUint64`；若值无法安全表示为 `int`/`uintptr`，会在任何乘法或指针算术前
报告 `length_overflow`。

## uintptr 与 GC 安全点

插桩代码必须通过检查器完成“指针 -> uintptr -> 指针”的桥接：

```go
u := checker.PointerToUintptr(p) // 记录当前 GC epoch 和所属分配

// 若需要在 uintptr 上做偏移，使用保留 provenance 的版本：
u2, err := checker.AddUintptr(u, 8)

// 插桩代码在真实 GC safe point 处调用：
checker.SafePoint()

_, err = checker.UintptrToPointer(u2, netsec.LayoutOf[uint32]())
// 必然报告 stale_uintptr，即使整数地址仍然有效
```

安全点之后重新调用 `PointerToUintptr` 会建立新的、有效的 epoch 记录。未经
`PointerToUintptr` 登记而直接转回指针的整数会被报告为 `untracked_uintptr`，避免
插桩被绕过。

## 验证

`checker_test.go` 覆盖：

- 攻击者长度字段导致的 `uintptr` 乘法/加法溢出与负长度；
- 相邻注册对象之间的越界与跨对象算术；
- 非对齐的目标类型；
- 合法对齐偏移、末尾空切片；
- 跨 GC 安全点的 `uintptr` 回转换，以及同 epoch 合法回转换。

运行：

```sh
go test ./...
go test -race ./...
go vet ./...
```
