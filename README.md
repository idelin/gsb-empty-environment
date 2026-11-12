# 范围检查与别名失效

安全代码在检查共享长度后，另一别名写入可能使后续读取越界。为Go审查服务实现可导入的检查有效性分析包，输入字段敏感的对象图与控制流，检查事实绑定对象字段版本，调用摘要给出可能写集合；写入任一别名使相关事实失效。求循环不动点并返回缺口路径，用不同字段写入和递归别名反例核对精度。

## 包结构

`netsec` 是可导入的纯库（无第三方依赖），入口为 `Analyze(*Program) *Result`。

- `model.go`：`ObjectGraph`、`Node`/`CFG`、`CallSummary`、`Target`、`Gap`、`Result`。
- `resolve.go`：根 + 字段路径到具体 `(对象, 字段后缀)` 目标的字段敏感解析，含递归/环去重，以及字段路径前缀关系。
- `analyze.go`：版本格（标签 + top）、检查事实、状态连接与 `mayDef` 精度控制。
- `engine.go`：worklist 不动点迭代与各节点传递函数（check/read/write/call）。
- `result.go`：缺口判定与 entry→read 见证路径回溯。

## 核心语义

- 检查事实绑定到具体对象字段的**版本标签**；只有被实际定义（检查/写入）的字段才会在控制流连接点参与版本合并，未被触碰的字段保持原版本。
- 写入通过任一别名命中同一对象字段即令相关事实失效；失效判定使用字段路径前缀（写父字段影响子字段，反之不然），因此写不同字段不会误报。
- 调用节点依据 `CallSummary.MayWrites` 与参数绑定解析可能写集合，按 may-write 保守失效。
- 版本在有限格（有限标签 + top）上单调上升、事实集合单调收缩，worklist 必然到达不动点，循环/自环/不可归约 CFG 均终止。
- 每个无法证明仍有效的读取产出一个 `Gap`，含 `Kind`（`invalidated`/`unchecked`）、`Invalidator` 与完整见证 `Path`。

## 用法

```go
prog := &netsec.Program{
    Graph: netsec.ObjectGraph{Roots: map[string]string{"a": "B", "other": "B"}},
    Nodes: []netsec.Node{
        {ID: "e", Kind: netsec.NodeEntry},
        {ID: "c", Kind: netsec.NodeCheck, Root: "a", Field: "len"},
        {ID: "w", Kind: netsec.NodeWrite, Root: "other", Field: "len"},
        {ID: "r", Kind: netsec.NodeRead, Root: "a", Field: "len"},
    },
    CFG: netsec.CFG{Entry: "e", Successors: map[string][]string{
        "e": {"c"}, "c": {"w"}, "w": {"r"},
    }},
}
res := netsec.Analyze(prog) // res.Gaps 含 r 处被 w 失效的缺口及见证路径
```

## 精度反例（测试）

- 同对象不同字段写入不误报；经别名写同字段必报（`TestDifferentFieldPrecision`）。
- 递归/自引用别名的字段共享与区分（`TestRecursiveAlias`、`TestResolveCycleFinite`）。
- 循环不动点：体内写无关字段时事实存活，写同字段时回边路径被完整重建（`TestLoopFixpoint`、`TestSelfLoopTermination`）。
- 调用摘要 may-write 经参数绑定失效（`TestCallSummary`）；无支配检查的读取报 `unchecked`（`TestUncheckedRead`）。
