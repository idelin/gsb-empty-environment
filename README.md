# 对象污点与原型写入

## `netsec` 原型污染静态分析包

`Analyze(*Program) (*Result, error)` 在受限脚本执行前做抽象解释，报告所有可达的共享原型危险写。

### 语言模型

- 节点：`Alloc`、`Const`、`External`、`Alias`、`Load`、`Store`、`If`、`Loop`，节点 ID 全程序唯一。
- 限制：分配点最多 32 个，循环精确展开 0–16 次；字符串取值域固定为 `a`、`b`、`__proto__`、`constructor`、`prototype`，`External` 按五值全集处理。
- 堆语义：每个新对象默认原型为共享原型 `P`；读取缺失的 `constructor` 字段得到固定构造对象 `C`，`C.prototype` 初始指回 `P`；`__proto__` 写改变接收对象的原型（仅对象值生效）。

### 分析域

- 每个变量/字段携带 may/must 抽象对象集合（别名分析）与 may 字符串集合，`If` 分支按 may 并集、must 交集合并。
- 循环内重复命中的分配点标记 `Multi`，其字段/原型写入退化为弱更新，保证不漏报。
- 原型链解析带环保护；`__proto__`/`constructor.prototype` 路径同样参与传播。

### 危险写判定

- 接收对象（may 集合）包含共享原型 `P` 的 `Store` 即报告：
  - `definite`：must 集合唯一为 `P`（或 `__proto__` 赋入值 must 唯一为 `P`、写 `C.prototype` 且 must 唯一为 `C`）；
  - `possible`：分支合并/弱更新下只能证明某条路径命中 `P`，属于允许的保守误报。
- 独立对象的自身字段写（含原型指向 `P` 的对象）不报告——缺失字段写在自身建立字段，不沿原型链污染。
- 报告含节点 ID、可能键集合、接收者与键的数据流证据链（`Evidence`）。

### 验证

- `ExploreConcrete` 是真实对象语义下的路径穷举解释器（外部字符串五值 × 条件分支全展开）。
- `TestExhaustiveNoFalseNegative` 对 1000 个随机小程序同时运行抽象分析与具体穷举，断言具体执行中的每个危险写都被静态分析覆盖（无漏报），并统计保守误报数量。

```go
res, err := netsec.Analyze(prog)
for _, dw := range res.DangerousWrites {
    // dw.NodeID / dw.Certainty (definite|possible) / dw.ReceiverTrace / dw.KeyTrace
}
```
