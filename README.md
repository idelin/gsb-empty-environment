# DOM 阅读顺序

`taskpkg` 是供 Go 抽取宿主调用的纯 Go 阅读顺序包。它接收可见文本块的页面矩形、显式 DOM 祖先关系和写作方向，在多栏页面中返回稳定的列优先阅读顺序及冲突诊断。

## 用法

```go
result, err := taskpkg.BuildReadingOrder(
    []taskpkg.Block{
        {NodeID: "title", Heading: true, Rect: taskpkg.Rect{X: 0, Y: 0, Width: 800, Height: 40}},
        {NodeID: "p1", Rect: taskpkg.Rect{Left: 0, Top: 60, Right: 380, Bottom: 100}},
        {NodeID: "p2", Rect: taskpkg.Rect{X: 420, Y: 60, Width: 380, Height: 100}},
    },
    []float64{400},
    taskpkg.LTR,
)
```

`Rect` 同时接受 `X/Y/Width/Height` 和 `Left/Top/Right/Bottom`；设置宽或高时优先使用前者。`Block.ID` 与 `Block.NodeID` 均可使用整数或字符串，二者同时设置时以 `NodeID` 为准。`Ancestors`（或 `AncestorIDs`）可传 `[]int`、`[]string`、`[]any` 或单个 ID，填写显式 DOM 祖先节点 ID。

## 规则

- 最多 30 个文本块、6 条候选分割线；超限返回 `ErrTooManyBlocks` / `ErrTooManyDividers`。
- 枚举所有有效候选线子集。有效线左右都至少有一个完整文本块；每一列都必须有文本块，否则该分区无效。
- 分区主成本是穿过选中栏线的**去重非标题块数**。标题块显式标记为 `Heading` 或 `IsHeading`，跨栏时只产生诊断，不计入成本。
- 平局先选更多分割线，再按从左到右的候选线下标字典序选择，保证结果确定。
- `LTR` 从左栏到右栏，`RTL` 从右栏到左栏。跨栏标题进入其跨越范围在阅读方向上的第一栏。
- 栏内按矩形 `Top` 升序，再按原节点 ID 排序；数字 ID 按数值比较。
- 显式祖先关系是硬约束，祖先必须位于后代之前。不会因为矩形邻近而推测祖先或分组。
- `Result.Conflicts` 按返回顺序列出每个块的冲突原因：`nonheading-crosses-divider`、`heading-spans-divider`、`ancestor-reordered`、`ancestor-crosses-column`、`ancestor-cycle`。祖先链中不对应任何可见文本块的容器 ID 会被忽略。

`Result` 还提供实际选中的 `SelectedDividers`、对应的原始下标 `DividerIndices`、未选候选线 `UnusedDividers`、总穿越块数 `CrossingCount` 和枚举分区数 `EvaluatedPartitions`。

## 测试

```sh
go test ./...
```

测试使用小页面覆盖全子集枚举、跨栏标题、悬浮注释的显式祖先约束、RTL 栏序、节点 ID 平局和输入上限。
