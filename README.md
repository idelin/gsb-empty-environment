# strategysolver

有限世界信号博弈的确定性策略求解器（Go，精确有理数算术）。

## 模型

- 有限个**世界**，各带有理数**概率**（和必须为 1），并产生一个可观察**信号**。
- **策略**是 `信号 -> 动作` 的映射：同一信号必须选同一动作（不可预知规则，
  非预期性 / non-anticipation）。
- 每个 (世界, 动作) 对给出有理数**损失**。
- **目标按字典序**：先最小化最坏情形损失 `max_w loss(w)`，
  再最小化期望损失 `sum_w p_w * loss(w)`。
- 支持**动作总容量约束**（多条、共享）：每条约束形如
  `sum_w p_w * weight(w, a(signal(w))) <= limit`，权重可按 (世界, 动作) 任意指定。

## 方法

枚举全部 `|actions|^|signals|` 个确定性策略，逐一：

1. 核对不可预知规则（策略定义在信号上，逐世界校验一致性）；
2. 用 `math/big.Rat` 精确累加并核对每条共享容量约束；
3. 计算最坏损失与期望损失，按字典序保留最优。

## 用法

```sh
go run . solve examples/weather.json              # 求最优策略
go run . list  examples/weather.json              # 列出全部可行策略（按目标排序）
go run . check examples/weather.json "clear=tent,cloudy=tent"   # 核对给定策略
go test ./...                                     # 运行测试
```

## 输入格式（JSON）

```json
{
  "worlds":  [{"id": "w1", "prob": "1/2", "signal": "s1"}],
  "actions": ["a", "b"],
  "losses":  {"w1": {"a": "0", "b": "10"}},
  "capacities": [
    {"name": "capA", "limit": "1/2", "actions": ["a"], "weight": "1"},
    {"name": "capB", "limit": "3/4",
     "entries": [{"world": "w1", "action": "a", "weight": "2"}]}
  ]
}
```

- 有理数可写为 `"p/q"`、小数或整数。
- 容量约束二选一：`actions` 简写（列出动作在所有世界按 `weight` 计耗，缺省 1），
  或 `entries` 逐 (世界, 动作) 指定权重；未指定的权重为 0。
