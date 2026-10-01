# 跨日班次最小人员覆盖（shift_cover）

仅使用 Python 3.11 标准库实现的分支限界求解器：在一周循环（10080 分钟，
分钟 0 = 周一 00:00）上，给定分钟级人员需求区间与候选班次（可跨日、跨周末，
时长 ≤ 1440 分钟），为每个班次选择非负整数人数，使所有时段每分钟在岗人数
满足需求，且总成本最低。

## 运行

```bash
python3 shift_cover.py instance.json            # 从文件读取
cat instance.json | python3 shift_cover.py      # 从标准输入读取
python3 shift_cover.py instance.json --max-nodes 5000 --max-seconds 10 --pretty
python3 -m unittest test_shift_cover -v         # 运行测试
```

退出码：`0` 求得最优或可行解；`1` 不可行或未知；`2` 输入不合法。

## 输入格式（JSON）

```json
{
  "demands": [{"start": 0, "end": 120, "required": 2}],
  "shifts":  [{"id": "A", "start": 0, "duration": 120, "cost": 3}]
}
```

- `demands[].start/end`：一周内的分钟，`0 <= start < 10080`，`0 < end <= 10080`，
  区间为左闭右开 `[start, end)`；`end < start` 表示跨周末回绕；`end == start` 非法。
  多个需求区间重叠时，每分钟需求取各区间 `required` 的最大值。
- `shifts[].start`：班次开始分钟（`0 <= start < 10080`），按模 10080 循环覆盖，
  因此天然支持跨日、跨周末。
- `shifts[].duration`：整数分钟，`1 <= duration <= 1440`（最长一天）。
- `shifts[].cost`：非负数字（每安排 1 人的成本，每人计 1 单位覆盖）。
- 班次 `id` 为非空唯一字符串。

## 输出格式（JSON）

- `status`：`optimal`（已证明最优）/ `feasible`（预算耗尽，仅有可行解）/
  `infeasible` / `unknown`。
- `proven_optimal`：只有为 `true` 时结果才是被证明的最优解。
- `cost`、`total_count`：当前最好解的成本与总人数。
- `assignment`：班次 ID -> 人数（仅非零）；`count_vector`：按 `shift_order`
  （班次 ID 升序）排列的完整人数向量。
- `cost_lower_bound` / `cost_upper_bound`：成本合法下界 / 可行上界。
  预算中断时两者不一定相等，且下界保证不超过真实最优值。
- `nodes_expanded`、`budget_exhausted`、`elapsed_seconds`、`note`。

## 目标与平局规则

1. 总成本最小；2. 成本相同取总人数最少；3. 仍相同取按班次 ID 升序的
   人数向量**字典序最小**者（确定性规则）。

## 方法说明

- 分支限界（best-first）：按剩余需求最大的分钟选择分支班次，枚举其人数
  `0..R`（`R` 为该班覆盖分钟的最大剩余需求，超过必被支配）。
- 下界：覆盖 LP 对偶可行解 `y_t = min_i(cost_i / 时长_i)`，用 `Fraction`
  精确计算，对任意剩余需求给出合法成本下界；人数下界为最大剩余需求。
- 上界：根节点及周期性贪心补全 + 反向删除得到的可行解。
- 预算（`--max-nodes` / `--max-seconds`）耗尽时输出当前上、下界，
  `proven_optimal=false`，绝不把未证明最优的结果标记为最优。

## 数据说明

本仓库所有示例与测试数据均为合成数据。
