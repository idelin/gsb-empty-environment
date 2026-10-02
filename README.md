# costmodel — 基于成本模型的 I/O 与 CPU 复合打分器（Go）

量化物理计划的执行代价：以页面读取权重构成 **I/O Cost**，以元组比较/哈希计算权重构成 **CPU Cost**，并沿计划树递归累加得到总估算成本。

## 模型

- `Weights`：可调权重（`SeqPageCost`、`RandomPageCost`、`CpuTupleCost`、`CpuIndexTupleCost`、`CpuOperatorCost`、`CpuHashCost`），默认值类 PostgreSQL。
- `TableStats`：统计信息（行数、数据页数、索引页数）。
- 计划节点（实现 `Plan` 接口，`Cost` 递归计算子树总成本，`OutputRows` 供父节点估算）：
  - `SeqScan`：`Pages*SeqPageCost + Rows*CpuTupleCost`
  - `IndexScan(sel)`：`(IndexPages + Pages*sel)*RandomPageCost + Rows*CpuIndexTupleCost + Rows*sel*CpuTupleCost`
  - `Filter(sel, child)`：`child.Cost + child.Rows*CpuOperatorCost`
  - `HashJoin(build, probe)`：子树成本 + 两侧每行一次哈希计算
- `ChooseScanPlan`：对同一查询比较 `SeqScan+Filter` 与 `IndexScan`，返回成本更低者。

## 运行

```sh
go test ./...    # 单元测试（含手工核算值与拐点切换验证）
go run ./cmd/demo
```

## 拐点示例

默认权重下，表 `rows=100000, pages=10000, indexPages=300`：

- `SeqScan+Filter = 11250`（与选择率无关）
- `IndexScan(s) = 1700 + 41000*s`
- 拐点 `s* = 9550/41000 ≈ 0.2329`：低于它选 IndexScan，高于它选 SeqScan+Filter。
