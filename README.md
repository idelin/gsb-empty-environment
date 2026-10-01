# 不确定时间事件因果验真

每个事件的发生时间是闭区间内的未知量，因果依赖要求下游事件至少比上游事件晚
整数 `d`。本工具判定全部差分约束（含区间上下界）是否可同时满足：

- **可行**：输出一组满足全部约束的时间赋值（已校验）；
- **不可行**：输出负环证据（沿环一周要求 `0 >= K, K > 0`，矛盾）；
- **增量添加**：约束逐条添加，导致不可行的约束自动**回滚**，原始时间区间永不被修改。

纯 Python 3.11 标准库实现（兼容 3.9+），无第三方依赖。

## 原理

约束 `x_v - x_u >= d` 化为最短路形式 `x_u <= x_v - d`；区间
`lo <= x_i <= hi` 通过虚拟源点化为两条边。用 Bellman-Ford 检测负环：
无负环时距离即为一组合法赋值；有负环时沿前驱指针提取环作为冲突证据。
增量添加在失败时移除该约束并重新求解，保证系统始终处于可行状态。

## 使用方法

```bash
python3 temporal_constraints.py [输入.json]   # 省略文件路径时从标准输入读取
python3 -m unittest test_temporal_constraints -v   # 运行测试
```

退出码：`0` 最终系统可行；`1` 最终系统不可行；`2` 输入格式错误。

## 输入格式（JSON）

```json
{
  "events": {"A": [0, 2], "B": [1, 3]},
  "constraints": [
    {"upstream": "A", "downstream": "B", "min_delay": 2}
  ]
}
```

- `events`：事件名 → `[下界, 上界]`（闭区间，数值）；
- `constraints`：按顺序**增量添加**的约束列表，`min_delay` 为整数，
  含义为 `x_downstream - x_upstream >= min_delay`。

## 输出格式（JSON）

```json
{
  "steps": [
    {"index": 0, "constraint": {...}, "accepted": true},
    {"index": 1, "constraint": {...}, "accepted": false, "rolled_back": true,
     "evidence": {"type": "negative_cycle",
                  "chain": ["A - B >= 1 ...", "B - A >= 2 ..."],
                  "contradiction": "沿环一周要求同一事件比自身晚 3，即 0 >= 3，矛盾"}}
  ],
  "feasible": true,
  "active_constraints": [...],
  "intervals": {"A": [0, 2], "B": [1, 3]},
  "assignment": {"A": 0, "B": 2},
  "assignment_verified": true
}
```

- `steps`：每条约束的受理结果；被拒绝的约束带有负环证据并已回滚；
- `active_constraints`：回滚后实际生效的约束；
- `assignment` / `assignment_verified`：最终可行时的一组赋值及其校验结果；
- `intervals`：原始区间（只读，求解过程不修改）。

## 示例（均为合成数据）

```bash
python3 temporal_constraints.py examples/feasible.json          # 验收边界：可行
python3 temporal_constraints.py examples/infeasible_cycle.json  # 正时差环：第 2 条回滚
python3 temporal_constraints.py examples/chain.json             # 链式依赖
```

## 说明与限制

- 采用 Bellman-Ford，复杂度 O(事件数 × 约束数)；增量添加为失败后整体重解，
  未做增量式最短路优化，大规模场景未验证性能最优性。
- 赋值是众多可行解之一（最短路解），不保证任何最优性（如最早/最晚时间）。
