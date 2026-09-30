# 不确定时间事件因果验真

事件发生时间为闭区间，因果约束要求下游事件至少比上游晚整数 `d`。
本组件判定是否存在满足全部差分约束的时间赋值：可行则输出一组合法赋值；
不可行则输出正时差环（负环）作为冲突证据。支持增量添加约束，失败自动回滚，
且不修改原始时间区间。仅依赖 Python 3.11 标准库。

## 方法

把约束统一改写为 `x_v <= x_u + w` 的差分约束系统：

- 区间下界 `x_e >= lo` → 边 `e -> SOURCE`，权 `-lo`
- 区间上界 `x_e <= hi` → 边 `SOURCE -> e`，权 `hi`
- 因果约束 `x_v >= x_u + d` → 边 `v -> u`，权 `-d`

用 Bellman-Ford 检测负环：无负环时最短路距离即一组合法赋值
（归一化使参考点取 0）；有负环时沿前驱指针抽出环，
环上约束依次传递即“某事件至少比自己晚 K（K>0）”的矛盾证据。
增量添加失败时移除该条约束，系统回到添加前的可行状态。

## 命令行用法

```sh
python3.11 temporal_solver.py 输入.json     # 或省略路径，从标准输入读取
python3.11 -m unittest test_temporal_solver -v   # 运行测试
```

退出码：全部约束可行 `0`；存在被回滚的不可行约束 `1`。

## 输入格式（JSON）

```json
{
  "events": {"A": [0, 2], "B": [1, 3]},
  "constraints": [
    {"upstream": "A", "downstream": "B", "delay": 2}
  ]
}
```

- `events`：事件名 → 闭区间 `[lo, hi]`（整数，`lo <= hi`，否则报错）。
- `constraints`：按顺序增量添加；`delay` 为整数，含义
  `x_downstream >= x_upstream + delay`。

## 输出格式（JSON）

- `feasible`：是否全部约束均被接受。
- `assignment`：已提交约束下的一组合法赋值（满足全部区间与已提交约束）。
- `committed_constraints`：最终生效的约束列表。
- `steps`：每条约束的处理结果，`accepted` 或 `rolled_back`（附 `conflict`）。
- `conflict`（不可行时）：
  - `cycle`：构成正时差环的可读约束列表；
  - `min_self_delay`：环传递后要求某事件至少比自己晚的量 K（K > 0）；
  - `explanation`：矛盾说明。

## 示例（合成数据）

`examples/acceptance_feasible.json`：A∈[0,2]、B∈[1,3]、B≥A+2 →
可行，输出如 `{"A": 0, "B": 2}`，退出码 0。

`examples/acceptance_cycle.json`：再要求 A≥B+1 → 与 B≥A+2 构成正时差环
（A≥B+1≥A+3），该约束被回滚，输出 `min_self_delay: 3` 的冲突证据，退出码 1。
