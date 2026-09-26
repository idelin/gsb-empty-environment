# 单机工单排程（含维护窗口）

Python 3.11+ 标准库实现，无第三方依赖。所有区间均为**半开区间** `[start, end)`：
端点接触不算冲突（维护 `[3,5)` 时，时长 3 的工单可在 0 开始 3 结束，但不能在 1
开始 4 结束）；工单在截止时刻恰好完成算按时（`end <= deadline`）。

## 问题定义

- 工单：释放时间、截止时间、整数持续时长、权重、前置依赖；不可抢占。
- 机器：每时刻至多执行一个工单；维护窗口内不可用。
- 目标（字典序）：先**最大化完成工单的总权重**，再**最小化总完成时间**。
- 求解器为精确算法（拓扑序枚举 + 最早开工贪心 + 权重上界剪枝），适合中小
  规模算例；另附独立暴力枚举核验器，可在小算例上交叉验证最优性。

## 运行

```bash
python3 scheduler.py example.json          # 从文件读取
cat example.json | python3 scheduler.py    # 从标准输入读取
python3 scheduler.py example.json --no-verify        # 关闭最优性核验
python3 scheduler.py example.json --max-combos 5000000
python3 -m unittest test_scheduler.py -v   # 运行测试
```

退出码：`0` 成功；`2` 输入非法（JSON 错误、时长 < 1、前置不存在、依赖环等）。

## 输入格式（JSON）

```json
{
  "jobs": [
    {"id": "A", "release": 0, "deadline": 12, "duration": 3,
     "weight": 4, "predecessors": ["B"]}
  ],
  "maintenance": [[3, 5], [9, 10]]
}
```

| 字段 | 类型 | 必填 | 默认 | 说明 |
|---|---|---|---|---|
| `jobs[].id` | string | 是 | — | 工单唯一标识 |
| `jobs[].release` | int | 否 | `0` | 释放时间（最早可开工时刻） |
| `jobs[].deadline` | int | 是 | — | 截止时间（`end <= deadline` 即按时） |
| `jobs[].duration` | int | 是 | — | 整数持续时长，`>= 1` |
| `jobs[].weight` | int | 否 | `1` | 完成权重 |
| `jobs[].predecessors` | array | 否 | `[]` | 前置工单 id，须先完成 |
| `maintenance` | array | 否 | `[]` | 维护窗口 `[start, end)` 列表，可重叠（自动合并） |

## 输出格式（JSON）

```json
{
  "objective": {"total_weight": 15, "total_completion": 24},
  "schedule": [{"id": "A", "start": 0, "end": 3}],
  "unscheduled": [{"id": "X", "reason_code": "no_feasible_slot", "reason": "..."}],
  "verification": {"status": "optimal_verified", "combinations": 1234,
                   "solver_objective": {}, "brute_force_objective": {}}
}
```

- `schedule`：按开工时刻排序，`[start, end)` 半开区间。
- `unscheduled[].reason_code`：
  - `no_feasible_slot`：释放-截止之间不存在避开维护窗口的完整时段；
  - `infeasible_precedence_chain`：前置依赖链无法全部按时完成；
  - `resource_contention`：本身可行，但为最大化权重/最小化完成时间被舍弃。
- `verification.status`：
  - `optimal_verified`：独立暴力枚举确认目标值一致（仅小算例）；
  - `skipped`：组合数超 `--max-combos` 上限，未核验（结果最优性未证）；
  - `disabled`：通过 `--no-verify` 关闭；
  - `mismatch`：求解器与暴力核验不一致（不应出现，出现即为 bug）。

## 文件

- `scheduler.py`：求解器、核验器、CLI。
- `test_scheduler.py`：unittest 测试（含验收边界用例）。
- `example.json`：合成示例输入。
