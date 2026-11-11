# controlflow

供 Go 二进制审查宿主使用的控制流恢复包。包本身不触碰字节，指令解码
完全由调用方注入；恢复过程从一组可信入口展开，并保留同一字节区间的全部
竞争解释。

## 注入解码器

实现 `controlflow.Decoder`（或用 `DecoderFunc` 适配普通函数），对每个地址
返回：

- `Length`：指令字节数，必须为正；为零时引擎记录 `ErrZeroLength`。
- `Successors`：已解析后继，后继类型用 `EdgeDirect` / `EdgeIndirect` 区分。
- `Terminates`：为 `true` 时不合成落到；为 `false` 时引擎自动加入
  `Address+Length` 的 `EdgeFallthrough` 后继。
- `UnknownIndirect`：无法解析的间接转移，引擎不展开，单列在报告里。
- `Err`：解码失败（如短读，可用 `ErrShortRead`）。失败地址仍建节点并进入
  `Report.Errors`，不会静默截断图。

## 恢复语义

- 从可信入口集合展开，重复入口自动去重。
- 同一地址只解码一次；后续到达只合并来源路径、入边和入口根，**后到不覆盖
  先到**。
- 两条指令区间部分重叠（含跳入指令中段）时，两种解释都保留，并在
  `Report.Conflicts` 记录先到/后到区间与共享字节范围；完全相同的区间视为
  同一解释，只做合并。
- 后继地址已在当前来源路径上时，边同时记入 `Report.BackEdges`（回边），
  不会重新解码，因此循环可终止。
- `MaxInstructions` 限制 `Decode` 调用次数；耗尽后置 `BudgetExceeded` 并
  停止展开，已恢复部分照常返回。
- 落在 `[Base, Base+Size)` 之外的后继不调用解码器，记入
  `Report.UnmappedTargets`。

## 最小用法

```go
engine := &controlflow.Engine{
    Decoder:         controlflow.DecoderFunc(myDecoder),
    Base:            0x400000,
    Size:            imageSize,
    MaxInstructions: 100000,
}
report, err := engine.Recover([]uint64{entryA, entryB})
```

`Report` 提供 `Nodes`、`Edges`、`BackEdges`、`Conflicts`、
`UnknownIndirect`、`UnmappedTargets`、`Errors` 与预算统计；每个 `Node`
保留 `Incoming` 入边、`EntryRoots` 可达入口和去重后的 `Paths` 来源链
（受 `MaxPathsPerNode` 上限保护）。

## 测试

`recover_test.go` 使用脚本化合成解码器覆盖：回边循环、跳入指令中段的区间
冲突、短读与零长度、未知间接目标、预算耗尽、越界目标与隐式落到、以及
“不覆盖只合并”语义。

```sh
go test ./... -race
```
