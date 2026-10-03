# sheetengine — 动态公式依赖图增量拓扑求值器

类电子表格的公式依赖图计算引擎，纯 Go 标准库实现。

## 能力

- **单元格引用公式**：`A1=B1+C1`，支持 `+ - * /`、括号、一元负号、数字字面量；
- **增量拓扑重算**：单点数值变更时，仅沿 `dependents` 反向边收集受影响下游子图，
  用 Kahn 算法做拓扑排序后按序重算，无关单元格零开销；
- **循环引用即时拒绝**：`SetFormula` 时在现有图上做可达性检查（BFS），
  检测到直接或间接循环即返回 `*CycleError`，携带完整循环路径
  （如 `A1 -> B1 -> C1 -> A1`），且图状态完全不被修改（隔离）。

## 结构

- `engine.go` — 依赖图、`SetValue`/`SetFormula`、循环检测、增量拓扑求值；
- `formula.go` — 公式词法/语法分析（递归下降）与求值；
- `engine_test.go` — 验收测试；
- `cmd/demo/main.go` — 运行样例。

## 运行

```sh
go test ./... -v        # 测试
go run cmd/demo/main.go # 演示
```

## 验收边界对应测试

- `TestIncrementalRecomputeOnlyAffectedSubgraph`：构建 `A1->B1->C1`、`D1->C1`，
  变更 `A1` 后断言 `B1`、`C1` 各恰好重算 1 次，`D1` 重算 0 次且值不变；
- `TestIndirectCycleRejectedWithFullPath`：尝试 `A1 = C1 + 1` 被立即拒绝，
  错误路径为 `[A1 B1 C1 A1]`，并验证原图边与数值均未泄漏变更。
