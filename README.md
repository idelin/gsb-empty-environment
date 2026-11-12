# 敏感联合体活动成员

二进制消息通过联合体解释同一内存时，未清理的旧成员字节可能被新成员发送出去。实现供Go插桩宿主调用的对象监视包，输入明确布局、活动成员切换、按字节写入与发送事件；成员切换不自动初始化共享外的字节，发送只能使用当前成员已初始化范围。输出残留字节来源，测试不同大小成员、嵌套联合体和部分字段覆盖。

## 设计

`netsec` 包不接触真实内存，而是用显式布局重建一个被监视对象，逐字节记录：

- **来源（provenance）**：每个字节最后一次写入它的字段，以及写入瞬间各层联合体的活动成员快照。
- **活动成员（active member）**：联合体同一时刻只有一个成员可达，由 `Switch` 选择；切换只改选择，不清空、不初始化任何字节。
- **发送校验（send）**：发送路径上的每个联合体都必须选中对应成员；只有来源成员与当前选择一致、且确实写过的字节才算干净。

残留字节分成两类（`Reason`）：

- `never-written`：该字节从未被任何写事件初始化。
- `stale-union-member`：字节由当前已失效的联合体成员写入，切换后作为旧值残留，`Residual.Source` 给出原始字段路径。

## API

- `NewMonitor(layout Field) (*Monitor, error)`：校验并建立对象，所有字节初始为未写状态。
- `(*Monitor).Switch(unionPath, memberPath string) error`：切换联合体活动成员，无字节副作用。
- `(*Monitor).Write(fieldPath string, offset, length int) error`：按字节写入并登记来源与成员快照。
- `(*Monitor).Send(fieldPath string, offset, length int) (Report, error)`：校验发送，返回 `Report.Clean` 与连续残留区间 `Residuals`。
- `(*Monitor).InitializedSpan(...)`、`Events()`、`Size()`：辅助查询已写字节数、事件日志与对象大小。

字段路径同时支持相对根路径（`body.u32v`）与全限定路径（`msg.body.u32v`）。

## 示例

```go
mon, _ := netsec.NewMonitor(netsec.Field{
    Name: "msg", Kind: netsec.Struct, Offset: 0, Size: 12,
    Children: []netsec.Field{
        {Name: "hdr", Kind: netsec.Leaf, Offset: 0, Size: 4},
        {
            Name: "body", Kind: netsec.Union, Offset: 4, Size: 8,
            Children: []netsec.Field{
                {Name: "u32v", Kind: netsec.Leaf, Offset: 4, Size: 4},
                {Name: "u64v", Kind: netsec.Leaf, Offset: 4, Size: 8},
            },
        },
    },
})

mon.Switch("body", "body.u32v")
mon.Write("body.u32v", 0, 4)
mon.Switch("body", "body.u64v")
mon.Write("body.u64v", 4, 4)

report, _ := mon.Send("body.u64v", 0, 8)
// report.Clean == false
// report.Residuals == [{Offset:4 Length:4 Reason:stale-union-member Source:"msg.body.u32v"}]
```

## 测试

`go test ./...` 覆盖不同大小成员互切、嵌套联合体（内层/外层切换）、部分字段覆盖、整对象与结构体发送、未切换/非活动成员拒绝、布局校验及并发访问（`-race`）。
