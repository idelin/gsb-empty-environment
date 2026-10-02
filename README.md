# cteopt — 公共表达式识别与物化复用（CTE / Common Subexpression）

纯 Go 标准库实现：识别 DAG 计划树中的重叠子查询，基于估算成本智能决策
**内联（Inline）多次执行** 还是生成 **Materialize 算子** 物化单次复用。

## 组件

| 文件 | 职责 |
| --- | --- |
| `plan.go` | 计划 DAG 节点（Scan/Filter/Project/Join/Rename/Materialize/CTERef）与打印 |
| `cost.go` | 成本模型：行数/代价估算、谓词选择率、spool 写/读/固定开销 |
| `optimize.go` | CSE 识别（结构哈希分组 + 引用计数）与 Inline vs Materialize 成本决策 |
| `exec.go` | 执行器：Materialize 将子计划算一次存入内存 buffer，CTERef 复用；统计各节点执行次数 |
| `sql.go` | 迷你 SQL 解析器：`WITH cte AS (...) SELECT ... JOIN/WHERE ...`，CTE 多处引用共享同一子计划指针（DAG） |

## 决策规则

对引用次数 `n >= 2` 的公共子表达式 `S`：

- 内联代价：`n * cost(S)`
- 物化代价：`cost(S) + rows(S)*write + fix + n * rows(S)*read`

物化代价更低则生成 `Materialize` spool，所有引用点替换为 `CTERef`；
否则保持内联重复执行。

## 运行

```sh
go test ./...    # 测试（含验收：WITH cte 被两处分支引用 -> 物化一次）
go run ./cmd/demo
```

## 验收点（见 `cteopt_test.go`）

- `TestOptimizeMaterializesExpensiveCTE`：昂贵 CTE 被两处引用 → 计划含 1 个
  Materialize + 2 个 CTERef；底层 orders 扫描执行次数从 2 降为 1；结果与内联执行完全一致。
- `TestOptimizeInlinesCheapCTE`：廉价 CTE 保持内联（扫描执行 2 次，无 Materialize）。
- `TestCommonSubexpressionDistinctPointers`：非共享指针但结构相同的子树同样被识别并物化一次。
