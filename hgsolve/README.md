# hgsolve — 超图推导求解器

## 模型

- 超边 `Edge{From, To, Cost}`：**全部**前置就绪后产生目标 `To`；同一目标可由多条超边产生。
- 初始事实免费（成本 0）。
- 目标的证明是一个以事实为根的**超边 DAG**；成本 = DAG 中**不同超边**的成本之和——
  被多个后继共享的证明节点只收费一次。

## 为什么逐点最短路不适用

逐点最短路（`TreeCost`）按 `dist(to) = Σ dist(premises) + cost` 递推，共享子证明会被
重复计费，因此只是最优值的**上界**。例如 `examples/shared.json` 中 `f` 的树成本为 13，
而共享 `c` 的证明 DAG 成本只有 8。精确求解需要对"事实集合"状态做搜索。

## 算法

| 功能 | 实现 | 性质 |
| --- | --- | --- |
| 可推出集合 | `Graph.Derivable` 前向链 | 多项式；无事实接地的循环边永不触发，循环自证被排除 |
| 精确最小成本证明 | `Graph.SolveExact` | 在单调事实集状态空间上做 Dijkstra；边沿路径至多触发一次，故路径成本恰为去重后的 DAG 成本 |
| 上界 | `Graph.GreedyProof` / `Graph.TreeCost` | 贪心证明经反向剪枝后是合法证明；用于剪枝精确搜索 |
| 下界 | `Graph.OrLower` | 把超边松弛为"任一单前置即可"的普通最短路，可采纳 |
| 小规模核对 | `Graph.BruteForce` | 枚举全部边子集（≤ 24 条边），闭包验证良基性，与精确解对拍 |
| 证明校验 | `Graph.VerifyProof` | 检查良基性（拒绝循环自证）与成本一致性 |

精确搜索是指数级的（状态为事实子集），属于"有限精确搜索"：贪心/树上界用于剪枝，
`ExactResult.Expanded` 报告实际展开的状态数。

## 用法

```sh
go run ./cmd/hgsolve -input examples/shared.json   # 或从 stdin 读入
go test ./...
```

输入 JSON：`{"facts": [...], "edges": [{"id","from","to","cost"}], "targets": [...]}`；
`targets` 省略时对全部可推出节点求解。输出每个目标的可达性、精确最小成本、
证明边集合、上下界与搜索展开数。

## 测试

- 循环自证（`a→b, b→a` 无事实时不可推出，有事实后正常）；
- 共享前置只计一次（树成本 13 vs DAG 成本 8）；
- 同一目标多生产者取最优；
- 300 组随机小超图上 `SolveExact` 与 `BruteForce` 对拍，并校验上下界夹逼与证明合法性。
