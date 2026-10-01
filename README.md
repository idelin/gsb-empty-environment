# phyprop — 物理属性传递与强制（Sort / Order）

自底向上跟踪并强制数据物理排序特性的查询优化器组件，仅使用 Go 标准库。

## 设计

- `property.go` — `Order` 物理属性与 `SatisfiedBy` 判定（需求是交付的前缀即满足）。
- `plan.go` — 物理算子：`SeqScan`、`IndexScan`、`Sort`（强制器）、`StreamAgg`、`HashAgg`、`MergeJoin`、`HashJoin`、`Project`、`Limit`。每个算子声明对子节点的 `RequiredProps()` 并向上交付 `DeliveredProps()`。
- `optimizer.go` — `Enforce(node, required)` 自底向上递归：先强制子节点满足本算子的需求，再检查本算子交付是否满足父节点需求，不满足则插入 `Sort`。

## 验收边界

`ORDER BY u.id` + 底层 `IndexScan(u.id)`：优化器识别索引已交付所需顺序，省去物理 Sort 算子，避免外部排序。见 `TestOrderBySatisfiedByIndexScan`。

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 样例
```
