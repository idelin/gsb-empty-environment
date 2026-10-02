# cteopt — 公共表达式识别与物化复用（CTE / Common Subexpression）

Go 实现的查询优化器组件：识别 DAG 计划树中的重叠子查询，基于成本估算
决定将其**内联（Inline）多次执行**，还是生成 **Materialize 算子**物化一次、
存入临时内存缓冲区供多处复用。

## 结构

- `plan.go` — 计划 DAG 节点（Scan/Filter/Project/Join/Aggregate/UnionAll/Materialize）与 `Explain` 打印
- `fingerprint.go` — 子树结构指纹，识别指针共享（CTE 引用）与结构等价（CSE）的重复子表达式
- `cost.go` — 成本模型：为每个节点估算行数与子树成本
- `optimize.go` — 优化器：统计每个子表达式的引用次数，比较
  `inline = refs × cost` 与 `materialize = cost + 写缓冲 + refs × 读缓冲`，
  选择更优方案并重写 DAG（重复的子树替换为单个共享 Materialize 节点）
- `exec.go` — 拉取式执行器；Materialize 首次执行时将结果写入内存缓冲区，
  后续读者直接重放；`Stats` 记录每个算子的打开次数与产出行数
- `demo.go` / `cmd/demo` — 可运行样例

## 运行

```sh
go test ./...      # 运行测试
go run ./cmd/demo  # 运行演示
```

## 演示查询

```sql
WITH big_orders AS (
  SELECT o.order_id, o.amount, u.region
  FROM orders o JOIN users u ON o.user_id = u.user_id
  WHERE o.amount > 100
)
SELECT region, COUNT(*) FROM big_orders GROUP BY region
UNION ALL
SELECT region, COUNT(*) FROM big_orders WHERE amount > 1000 GROUP BY region
```

CTE `big_orders` 被两个分支引用。优化器估算其成本（join+filter ≈ 4600）
高于物化开销，决定物化一次：执行统计中 CTE 子树只被打开 1 次
（内联方案为 2 次），重复计算行数减半，且两种方案结果完全一致。
对于成本很低的共享子表达式（如 4 行小表扫描），优化器选择保持内联。
