# streamsum

流式浮点求和库（Go）：返回结果的同时给出**明确的、数据相关的误差估计**，
支持按块摄入、合并部分和、撤回最近一块。

## 算法与误差界

- 块内：朴素递归求和，严格误差界 `(m-1)·u·Σ|xᵢ|`（m 为块大小，u = 2⁻⁵³）。
- 块间：Neumaier 补偿求和，一阶误差界 `2·u·Σ|块和|`。
- 总估计：`ErrorBound = Σ块 (m_b - 1)·u·Σ|x| + 2·u·Σ|块和|`。

因此**块越大，误差界越保守**；逐元素摄入（块大小 1）时块内项为零，
界只来自全局补偿项。

## 特殊值公开规则

- 任一输入为 NaN → `Result()` 为 NaN。
- 同时摄入过 +Inf 与 -Inf → `Result()` 为 NaN。
- 仅一种符号的无穷 → `Result()` 为该符号的无穷。
- 有限累加溢出为无穷 → `Result()` 反映该无穷。
- 只要结果非有限，`ErrorBound()` 一律返回 **+Inf**——绝不对无穷结果
  给出伪有限误差界。

## API

```go
s := streamsum.New()
s.Add(x)              // 单值（块大小 1）
s.AddBlock(xs)        // 按块摄入
s.Merge(other)        // 合并另一个 Summer（作为一块）
s.Retract() bool      // 撤回最近一次 Add/AddBlock/Merge
s.Result() float64    // 当前和
s.ErrorBound() float64// 误差估计（非有限结果时为 +Inf）
s.Count() int64       // 已摄入的有限值个数
```

每次 `Add`/`AddBlock`/`Merge` 压入一个快照，`Retract` 弹出；
内存随未撤回的块数线性增长。

## 测试

`go test ./...` 使用 `math/big.Rat` 以二进制浮点值的**精确有理值**复算，
验证：小例精确对照、大数抵消（`1e16+1+1-1e16`）、小增量丢失、
不同块大小对误差界的影响、NaN/±Inf 规则、撤回与合并。
