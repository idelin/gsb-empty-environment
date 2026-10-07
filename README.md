# perflib

Go 性能事件分析库：输入嵌套调用的 start/end 事件流，构建调用树并计算
包含时间（inclusive）与独占时间（exclusive），跨线程异步重叠按时间片
占用模型求并集而非直接相加。

## 模型

- **严格栈配对**：同一线程内事件必须 LIFO 嵌套；异步子任务在自己的
  线程上记录，线程间事件可任意交错。
- **输入顺序**：事件按输入顺序处理，同一线程时间戳需非递减（同刻事件
  合法），回退报 `DiagOutOfOrder`。
- **包含/独占**：节点 inclusive = end - start；exclusive = inclusive -
  直接子节点 inclusive 之和（即该帧位于线程栈顶的时间片）。
- **时间片占用模型**：扫描线把区间切成恒定占用的片段，`UnionDuration`
  统计占用 ≥1 的片长——异步重叠只计一次。`WallTime`/`FuncWallTime`
  基于此；`Stats` 的 Inclusive 是逐次调用之和，跨线程重叠场景请勿
  直接相加，改用 `FuncWallTime`。

## 诊断

`Build` 不中断于坏数据，恢复后继续并收集 `Diagnostic`：

| 诊断 | 含义 |
| --- | --- |
| `DiagEndWithoutStart` | 栈空时收到 end |
| `DiagNameMismatch` | end 的名字不在当前打开的帧中 |
| `DiagUnclosedFrame` | 帧未被配对关闭（被祖先 end 跳过或流结束），在最后时刻强制关闭 |
| `DiagNegativeDuration` | end 早于 start，钳制为零时长 |
| `DiagOutOfOrder` | 同线程时间戳回退 |

## 用法

```go
tr := perflib.Build(events)        // events: []perflib.Event
stats := tr.Stats()                // map[string]*perflib.FuncStat（按名聚合）
wall := tr.WallTime()              // 全局占用并集
perFunc := tr.FuncWallTime()       // 每个函数的占用并集
for _, d := range tr.Diagnostics { // 坏栈诊断
    fmt.Println(d)
}
```

测试：`go test ./...`（覆盖递归、同刻事件、异步重叠、坏栈恢复，
并用占用模型交叉核对逐帧独占时间）。
