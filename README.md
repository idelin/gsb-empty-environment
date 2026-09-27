# 含维护窗口的单机工单排程

单机、非抢占式工单排程。工单有**释放时间** `release`、**截止时间** `due`、
**整数持续时长** `duration`、**整数权重** `weight` 和**前置依赖** `predecessors`
（完成到开始：前置工单完成后才能开始）。机器任一时刻至多处理一单，且在
**维护窗口**内不可用。

- 所有时间点均为整数，区间一律按**半开区间** `[start, end)` 处理：
  - 维护 `[3,5)` 时，时长 3 的工单可以 `[0,3)`（3 结束，接触不冲突），
    但不能 `[1,4)`（与 `[3,5)` 在 3 处重叠）；
  - 两工单 `[0,3)` 与 `[3,6)` 不冲突；
  - 工单在截止时刻完成算按时：`end <= due`。

## 优化目标（字典序）

1. **最大化**按时完成（被安排）工单的总权重；
2. 在总权重最优的所有方案中，**最小化总完成时间**
   （各被安排工单完成时刻之和，未安排工单不计）。

无法按时安排的工单一律不安排，并给出原因。

## 算法与最优性边界

| 场景 | 方法 | 最优性 |
| --- | --- | --- |
| 工单数 `n <= 14`（可调阈值） | 子集 DP + Pareto 前沿 | **已证最优**（两个目标都最优） |
| `n > 14`（自动回退） | 4 条优先规则构造启发式（EDD、加权密度、权重优先、释放/截止） | 一般**未证最优** |

精确 DP 对每个已安排工单集合保存 `(机器空闲时刻, 总完成时间)` 的 Pareto 前沿，
按集合大小递推，状态数 `2^n`；在本机 n=14 压力用例约 2.4 秒。该问题属
NP 难（加权在机数量/加权吞吐类），故大规模实例不做伪精确声称：

- 输出 `status = "optimal"` 表示由精确算法求得并经内置校验；
- `status = "feasible_not_proven_optimal"` 表示启发式可行解；
  若启发式恰好安排了**所有正权重工单**，`statistics.proven_optimal_weight`
  为 `true`（权重达到全局上界，权重目标已证最优），第二目标仍未证明；
- 测试中对 n≤7 的合成实例用**独立编写的暴力枚举**（显式枚举每个整数开始
  时刻、独立的区间重叠判定）与精确 DP 交叉核验，全部一致。

未安排原因代码：

- `no_feasible_slot`：即使独占机器（忽略其他工单）也无法在释放后、截止前
  找到不穿越维护窗口的连续整数时段；
- `predecessor_unscheduled`：存在未安排的前置工单；
- `capacity_conflict`：本身可独占排下，但在保持最优权重集合的前提下无法
  同时排入（产能/维护窗口/截止冲突），加入它必须挤出其他工单；
- `excluded_by_secondary_objective`：权重为 0，加入后权重不变但总完成时间
  增加，按第二目标排除（仅精确解可能给出）。

## 运行环境

Python 3.11，仅使用标准库，无第三方依赖。

## 命令行

```bash
python3.11 scheduler.py examples/acceptance.json          # 文件输入
cat examples/acceptance.json | python3.11 scheduler.py    # 标准输入
python3.11 scheduler.py examples/large_demo.json --mode heuristic
python3.11 scheduler.py instance.json --mode exact        # 强制精确
python3.11 scheduler.py instance.json --threshold 12      # 调整自动阈值
```

退出码：`0` 正常；`2` 输入非法（错误信息以 JSON 写到 stderr，
`status="invalid_instance"`）。

## 输入格式（JSON）

```json
{
  "maintenance": [[3, 5]],
  "jobs": [
    {"id": "A", "release": 0, "due": 3, "duration": 3, "weight": 1,
     "predecessors": []}
  ]
}
```

- `jobs`（必填，非空）中每个对象：
  - `id`：字符串，可省略，默认 `J1, J2, ...`；必须唯一；
  - `release`：非负整数；
  - `due`：整数，`due >= release`；完成于 `due` 时刻算按时；
  - `duration`：正整数；
  - `weight`：非负整数，默认 `1`；
  - `predecessors`：字符串数组，默认 `[]`；前置工单 id 必须存在，
    依赖图不允许有环。
- `maintenance`：`[start, end]` 整数对数组（半开、`0 <= start < end`），
  默认 `[]`；重叠/相邻窗口会被自动合并。

## 输出格式（JSON）

```json
{
  "status": "optimal",
  "maintenance": [[3, 5]],
  "scheduled": [
    {"id": "A", "start": 0, "end": 3, "release": 0, "due": 3,
     "duration": 3, "weight": 1}
  ],
  "unscheduled": [
    {"id": "B", "reason": "no_feasible_slot", "detail": "..."}
  ],
  "statistics": {
    "method": "exact_subset_dp",
    "proven_optimal": true,
    "subset_states": 4,
    "feasible_subsets": 2,
    "elapsed_seconds": 0.0001,
    "total_weight": 1,
    "total_completion_time": 3,
    "scheduled_count": 1,
    "job_count": 2
  },
  "validation": {
    "valid": true,
    "violations": [],
    "interval_convention": "half-open [start, end); touching does not conflict"
  }
}
```

- `scheduled` 按开始时刻升序；
- 启发式结果中 `statistics.proven_optimal` 为 `false`，并带
  `proven_optimal_weight` / `proven_optimal_secondary` 与中文 `note`；
- `validation.valid` 表示输出方案已通过独立的后置校验（释放、截止、
  维护、机器产能、前置依赖、半开接触规则）。

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

覆盖：验收边界（`[0,3)` 可行 / `[1,4)` 不可行 / 截止时刻完成算按时 /
接触不冲突）、权重优先与总完成时间次级目标、前置依赖与环检测、
四类未安排原因、非法输入、14 工单精确解、20 工单启发式、CLI，
以及 n≤7 共 84 个随机合成实例与独立暴力枚举的逐例最优性比对。

所有示例与测试数据均为合成数据。
