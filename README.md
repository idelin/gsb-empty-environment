# sheetcalc

动态公式依赖图增量拓扑求值器（类电子表格计算引擎），纯 Go 标准库实现。

## 功能

- 单元格引用公式：`B1 = A1*2`、`C1 = B1+D1`，支持 `+ - * /`、括号、一元负号、数字字面量，公式可带前导 `=`。
- **增量拓扑重算**：单点数值变更时，仅对受影响的下游子图做 Kahn 拓扑排序并重算，未受影响的单元格零开销。
- **循环检测与隔离**：添加公式时若引入直接或间接循环引用，立即返回 `*CycleError`（含完整循环路径），引擎状态保持不变。

## API

```go
e := engine.New()
e.SetValue("A1", 1)          // 设置字面值（清除已有公式），并增量重算下游
e.SetFormula("B1", "A1*2")   // 解析、循环检测、接线、重算
v, _ := e.Get("B1")          // 读取当前值
n := e.EvalCount("B1")       // 公式被求值的次数（用于验证增量性）
```

循环引用示例：

```go
err := e.SetFormula("A1", "C1+1")
// err 为 *engine.CycleError:
// circular reference detected: A1 -> C1 -> B1 -> A1
```

## 运行

```sh
export GO111MODULE=on
go test ./... -v   # 运行测试
go run ./cmd/demo  # 运行验收场景样例
```

## 设计

- `parser.go`：词法分析 + 递归下降解析，公式编译为 AST 并缓存于单元格中。
- `engine.go`：
  - 每个单元格维护 `deps`（依赖，入边）与 `dependents`（被依赖，出边）双向邻接。
  - `SetFormula` 先解析并做可达性检查（新依赖 `ref` 是否能沿 depends-on 边到达目标单元格），发现环则以 DFS 路径构造完整环路并拒绝，状态零副作用。
  - `recomputeFrom` 从变更点沿 `dependents` 收集受影响子图，在诱导子图上做 Kahn 拓扑排序后按序求值，保证菱形依赖中每个单元格每次变更只求值一次。
