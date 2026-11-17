# 多栏网页阅读顺序

`taskpkg` 供 Go 抽取宿主计算多栏网页的确定性阅读顺序。调用方提供可见文本块的矩形、显式标题标记、DOM 祖先 ID 和候选栏分割线；包会枚举所有非退化候选线子集，优先减少穿过非标题块的栏线，再按栏内纵向和祖先先后排序。

```go
result, err := taskpkg.Order(taskpkg.Input{
    Blocks: []taskpkg.Block{
        {ID: "title", Rect: taskpkg.NewRect(0, 0, 220, 24), Heading: true},
        {ID: "body", Rect: taskpkg.NewRect(0, 40, 100, 80)},
        {ID: "aside", Rect: taskpkg.NewRect(140, 40, 80, 40)},
    },
    Dividers:  []float64{120},
    Direction: taskpkg.LTR,
})
```

约束与行为：

- 最多 `MaxBlocks = 30` 个块、`MaxDividers = 6` 条候选线；超限或非法输入返回错误。
- 标题必须由抽取器显式设置 `Heading`，不能靠字号或坐标推断。标题穿过栏线会记录冲突，但不计入 `Result.Crossings`。
- 候选线按完整子集枚举，只有每一栏都至少有一个结构根块中心时才视为有效分区。同一穿越数下优先保留更多栏线，再按候选线字典序决定。
- 同一 DOM 子树继承结构根节点的栏归属；悬浮注释不会因为坐标更近而被挪到别栏。祖先始终排在后代之前。
- 同等栏、纵坐标下按原始节点 ID 字典序排序。`Result.Conflicts` 给出栏线穿越、祖先改栏、祖先压过纵向顺序和异常约束环等原因。
