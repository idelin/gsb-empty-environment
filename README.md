# 采集器读取多栏网页时，仅按DOM顺序会把正文与侧栏混在一起

采集器读取多栏网页时，仅按DOM顺序会把正文与侧栏混在一起。写供Go抽取宿主调用的阅读顺序包，输入可见文本块的矩形、DOM祖先关系和写作方向；在给定候选栏分割线中选择使被栏线穿过的非标题块数量最少的分区，标题块由输入显式标记，再按栏内纵向与明确祖先先后排序。至多30块、6条候选线，返回顺序与每个冲突原因，平局按原节点ID。用小页面全分区枚举核对跨栏标题、悬浮注释和RTL栏序，不以坐标近邻替代结构约束。

## Go API

`Plan` 枚举最多 `2^6=64` 个候选线子集，返回完整阅读顺序、视觉栏、选中线、全部分区评分和结构化冲突原因：

```go
result, err := taskpkg.Plan(taskpkg.Request{
    Blocks: []taskpkg.Block{
        {
            ID:        "title", // 原节点 ID，也是最终平局键
            Heading:   true,    // 标题必须显式标记
            Rect:      taskpkg.RectFromLTRB(20, 10, 580, 50),
            Ancestors: []string{"html", "body", "header"},
        },
        {
            ID:        "article-1",
            Rect:      taskpkg.RectFromLTRB(20, 80, 260, 160),
            Ancestors: []string{"html", "body", "main"},
            DOMOrder:  1,
        },
    },
    CandidateLines: []taskpkg.CandidateLine{
        {ID: "gutter-1", X: 300},
    },
    Direction: taskpkg.DirectionRTL, // 栏序从右向左；LTR 为默认值
})
if err != nil {
    return err
}

_ = result.Order     // 完整 ID 顺序，每个输入块恰好出现一次
_ = result.Conflicts // 稳定代码 + 中文原因
_ = result.Reasons   // 纯文本原因
```

也可以用 `Parents map[string]string` 提供父指针，或用 `NodeOrder map[string]int`
提供祖先/兄弟节点的明确先后。`EnumeratePartitions` 会返回完整穷举结果，便于
宿主审计或测试独立核对。

## 定序规则

- 候选分区：穷举候选线子集；惩罚为被任意选中线穿过内部的不同非标题块数。
- 标题豁免：显式 `Heading`/`IsHeading`/`Title` 被穿线不计惩罚，但返回 `cross_column_heading` 信息。
- 栏顺序：LTR 从左栏到右栏，RTL 从右栏到左栏。
- 栏内顺序：先按互不相交的纵向区间分组；相交/悬浮区间内优先使用明确 DOM 祖先或节点次序，再使用纵向坐标，最后按原节点 ID 平局。
- 跨线块：作为跨栏带处理且只输出一次；页中跨栏标题会分隔其上下的栏内容。
- 结构审计：穿线、纵向顺序与 DOM 顺序冲突、悬浮重叠、ID 平局都会进入 `Conflicts`。

运行 `go test -v ./...` 可查看跨栏标题、页中跨栏带、悬浮注释、RTL、ID 平局、父指针和随机页面排列不变量测试。
