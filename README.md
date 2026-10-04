# fsplanner — 有限状态规划器

以布尔命题描述状态，动作包含前置条件（正/负）、增删效果与非负成本，
使用 Dijkstra（一致成本搜索）求达到目标的最低成本计划。

## 特性

- 状态为命题位掩码，最多 20 个命题（加载时校验）
- 动作成本非负（负成本在加载时拒绝）；仅当发现严格更低成本时才更新距离，
  零成本环不会导致无限搜索
- 目标不可达时输出已探索状态摘要：状态数、各目标条件的满足统计、
  按到达成本排序的状态列表
- 独立计划回放器 `replay`：逐步检查前置条件、应用效果、验证目标与成本

## 用法

```sh
go build -o fsplanner .
./fsplanner plan   examples/logistics.json plan-out.json   # 求解并保存计划
./fsplanner replay examples/logistics.json plan-out.json   # 独立回放校验
go test ./...                                               # 运行测试
```

问题与计划的 JSON 格式见 `fsplanner` 帮助输出及 `examples/` 目录。
