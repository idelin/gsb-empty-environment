# EDF Scheduler（可抢占单处理器，事件驱动）

Python 3.11，仅标准库；测试用 `unittest`。不使用任何求解器，所有算术基于
`fractions.Fraction`，对合成整数/有理数数据精确计算。

## 语义

- 任务 `Task(id, release, work, deadline)`：释放时间、工作量、截止期。
- 事件驱动模拟：时间只在「释放」与「完成/抢占」事件间跳跃，绝不逐 tick 推进。
- 同时到达且截止期相同 → 按任务 ID 稳定排序（堆键为 `(deadline, release, id)`）。
- 零工作量任务在释放时刻立即完成，不产生执行片段。
- 未到释放时间的任务绝不运行；CPU 空闲时直接跳到下一个释放事件。
- 结果三态分离：
  - `FEASIBLE`：全部截止期满足。抢占式单处理器上 EDF 最优，故此结果即最优性证书。
  - `INFEASIBLE`：有任务错过截止期，并附**不可行时间窗证据**（处理器需求准则）：
    存在区间 `[a, b]`，其中释放不早于 `a` 且截止不晚于 `b` 的任务总需求
    `demand > b - a`，任何调度器都无法满足。返回超出量最大的窗口。
  - `UNDECIDED`：事件预算（`event_budget`）耗尽，返回已算出的部分调度。

## 运行样例

```console
$ python3.11 edf_scheduler.py
=== 1) Feasible (and optimal, since preemptive EDF is optimal) ===
status: feasible (optimal: True)
  task 1 runs [0, 1)
  task 2 runs [1, 2)
  task 2 runs [2, 3)
  task 3 runs [3, 7/2)
  task 1 runs [7/2, 11/2)
  task 1 completes at 11/2
  task 2 completes at 3
  task 3 completes at 7/2

=== 2) Infeasible with overload-window evidence ===
status: infeasible, missed: [2]
evidence: overload window [0, 3]: demand 4 > capacity 3 (excess 1), tasks [1, 2]

=== 3) Undecided: event budget exhausted ===
status: undecided after 3 events (completed: [1, 2, 3])
```

## 用法

```python
from fractions import Fraction
from edf_scheduler import Task, simulate, find_overload_window, Status

tasks = [
    Task(1, release=0, work=3, deadline=7),
    Task(2, release=1, work=2, deadline=4),
    Task(3, release=2, work=Fraction(1, 2), deadline=6),
]
result = simulate(tasks, event_budget=10_000)  # budget 可选
assert result.status is Status.FEASIBLE
```

## 测试

```console
$ python3.11 -m unittest test_edf_scheduler -v
...
----------------------------------------------------------------------
Ran 22 tests in 0.226s

OK
```

测试按关注点分类：

- `TestFeasible`：抢占、空闲跳跃、有理数工作量、释放时间约束。
- `TestOptimality`：随机合成实例上「EDF 可行 ⇔ 无超载窗口」交叉验证（300 例）。
- `TestInfeasibleEvidence`：超载窗口的区间、需求、容量、涉及任务与最大超出量。
- `TestBudgetUndecided`：预算耗尽 → `UNDECIDED`，预算充足 → `FEASIBLE`。
- `TestEdgeCases`：零工作量立即完成、同释放同截止按 ID 稳定、事件计数非逐 tick、
  空任务集、非法参数拒绝。
