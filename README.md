# 结构差分包

`taskpkg` 是供 Go 内容采集宿主调用的块树结构差分库。它把页面更新表示成
`match` / `move` / `delete` / `insert` 编辑计划，避免把广告轮换一类噪声区域
误判成正文全文变化。

## 输入模型

```go
type Block struct {
    ID       string   // 可选；两版都提供时用于稳定识别父关系
    Role     string   // 稳定角色/类型，例如 paragraph、heading、section
    Text     string   // 块正文
    Children []*Block // 子块树
    Ignore   bool     // true 表示该块及整棵子树为调用方声明的可忽略区域
}
```

`Ignore` 区域不会参与匹配，也不会产生删除/插入操作，其文本不会进入任何
编辑操作。广告容器应标记在容器块上，整棵子树都会被忽略。

## 成本模型

`Diff(oldRoot, newRoot, Options)` 返回最低总代价计划：

- 未匹配的旧块：`DeleteCost`
- 未匹配的新块：`InsertCost`
- 匹配块：`RolePenalty`（角色不同时） + 文本的 Unicode rune 级
  Levenshtein 距离
- 匹配块父关系改变：额外 `MoveCost`，并输出为 `OpMove`，不计成
  一删一增

父关系是否保持由匹配本身判定：旧块的父块必须匹配到新块的父块。根块的
父关系为“无父”，两个根块互相匹配时不产生移动。

```go
opts := taskpkg.Options{
    DeleteCost:  10,
    InsertCost:  10,
    RolePenalty: 2,
    MoveCost:    4,
    Budget:      2_000_000,
}
plan, err := taskpkg.Diff(oldRoot, newRoot, opts)
```

输入总块数上限为 40（包括被忽略块）。`Options.MaxBlocks` 只能下调该上限。
`Budget` 是分支定界搜索状态数上限；耗尽时仍返回当前最优可行计划，但
`plan.Optimal == false`、`plan.BudgetExhausted == true`，并给出 `Reason`。

## 计划与原文区间

`Plan.Operations` 中旧块相关操作按旧树前序排列，插入操作随后按新树前序
排列。`OldOrder` / `NewOrder` 是非忽略块的前序索引，缺失一侧为 `-1`。

`TextSpan` 同时给出 rune 和 byte 半开区间。坐标基于
`PlainText(root)`：所有块的 `Text` 按前序直接拼接（包括被忽略块、无分隔符）。
这样删除广告区域不会改变其他块在原文中的位置。

- `Operation.OldSpan` / `NewSpan`：整块区间
- `Operation.OriginalIntervals`：该操作在原文中删除/替换的区间；匹配块
  纯插入文本时给出零宽插入位置；删除操作给出整块区间
- `Operation.NewIntervals`：新文本中的对应变化区间

```go
for _, op := range plan.Operations {
    if op.Kind == taskpkg.OpMove {
        fmt.Println("moved:", op.OldID, "-> new parent", op.NewParentOrder)
    }
    for _, s := range op.OriginalIntervals {
        fmt.Println(s.ByteStart, s.ByteEnd)
    }
}
```

## 测试

`go test ./...` 包含独立穷举 oracle：对随机小树枚举所有单射匹配与删增补全，
逐项核对最低成本；同时覆盖重复段落、跨父移动、无 ID 父关系、忽略广告区、
非忽略正文替换、纯插入区间和预算耗尽标记。
