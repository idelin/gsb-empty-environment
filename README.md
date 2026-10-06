# mcf — 小规模精确最小费用流（含分阶段流量承诺）

针对最多约 8 点 / 12 边的运输图：整数流量、容量有限、费用可为负。
输出逐边流量与总费用；流量不足时单独报告不可行并给出割证据。

## 精确性

`flow.Solve` 不是"只找最短增广路"：主循环先消去残余网络中的
**负费用环**（Bellman-Ford 全源检测），无负环时才沿最短增广路
（Bellman-Ford，兼容负费用边）推进。终止条件即最优性条件——
可行流是最小费用流当且仅当残余图无负环。自环边同样被正确处理。

## 分阶段流量承诺

- `Lower`（已执行量）：新方案中该边流量不得低于它，只允许在剩余
  容量上调整未执行流。
- `Cancelled`（取消边）：不再接受新增流量，但已执行量保留——
  该边流量被冻结在 `Lower`。
- 下界通过标准平衡变换处理：预发已执行量并修正各点净需求。

## 独立验证与可恢复搜索

`flow.Search` / `flow.ResumeSearch` 是对全部边流量的分支定界枚举
（穷举级独立验证），带平衡可达性与费用松弛两种剪枝：

- **预算终止**（`Budget{MaxNodes, MaxMillis}`）：返回
  `[LowerBound, UpperBound]` 区间与当前最优可行解，不声明最优。
- **可恢复前沿**：预算耗尽时返回 `Frontier`（完整搜索栈 + 上下界），
  可序列化保存，之后用 `ResumeSearch` 无损续搜。
- 搜索完成时若与 `Solve` 结论不一致，`flow.Verify` / CLI 会报错。

## CLI

```sh
go build ./cmd/mcf
./mcf -in problem.json   # 或从 stdin 读入
```

输入（字段均可选部分见下）：

```json
{
  "num_nodes": 4, "source": 0, "sink": 3, "demand": 5,
  "edges": [{"from":0,"to":1,"cap":4,"cost":2,"executed":1,"cancelled":false}],
  "verify": true,
  "budget": {"max_nodes": 100000, "max_millis": 1000},
  "frontier_in": "frontier.json",
  "frontier_out": "frontier.json"
}
```

输出：`status`（`optimal` / `infeasible` / `budget_exceeded`）、
逐边流量（含已执行量与新增量）、`total_cost`、不可行时的 `cut`
（S/T 两侧、缺口、跨割饱和边、未满足节点）、搜索上下界与
穷举验证结果。

## 测试

```sh
go test ./flow/
```

覆盖：负环不可忽略用例、承诺/取消语义、不可行割证据、
400 个随机实例上求解器与穷举搜索对拍、前沿保存/恢复一致性、
预算上下界有效性。
