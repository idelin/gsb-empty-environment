# migration — 带权分区迁移规划与执行

纯 Python 3.11 标准库实现，无第三方依赖，不调用任何现成查询引擎。

## 组成

- `migration/batch.py` — 带列类型的批次输入（`INT64` / `FLOAT64` / `STRING` / `BOOL`）。
  null（`None`）与 NaN 是不同的值；NaN 只能出现在 `FLOAT64` 列；整数为任意精度精确计算。
- `migration/model.py` — 问题模型：带权分区（不可分割）、固定数量执行槽（各自容量）、
  亲和（必须同槽）/反亲和（必须不同槽）约束、本轮移动预算（字节）。由批次构建问题。
- `migration/solver.py` — 小规模精确求解器（分支定界）。目标字典序：先最小化最大槽负载，
  再最小化迁移字节。联合满足槽容量、亲和/反亲和与移动预算。输出：
  - 独立可验的分配（`verify()` 为独立检查器，返回违规列表）；
  - 已证下界（对剩余容量取 `sum(min(cap, T)) >= 剩余权重` 的精确二分下界）；
  - 未决 gap（节点限额截断时状态为 `UNDECIDED`，给出下界与 `max_load - lower_bound`）。
  - 单个超大分区不假装拆分、总容量不足时返回 `INFEASIBLE` 与未满足字节量 `unmet_bytes`。
  - 计划确定性：分区按 `(-weight, pid)`、槽按 `sid` 排序，移动按 `(pid, src, dst)` 排序。
- `migration/executor.py` — 分阶段执行器：对全部移动依次执行
  目标预留（RESERVE）→ 拷贝（COPY）→ 所有权切换（SWITCH）→ 释放预留（RELEASE）。
  - 失败时按相反顺序逐步回滚元数据；任意时刻分区恰好归属一个槽（无双重拥有）；
  - 回滚精确退还预算（无预算泄漏）；
  - 旧完成的回包按计划代际（generation）拒绝（`StaleAckError`）；
  - 同一计划重放为幂等空操作，不重复占用目标槽、不重复扣预算。

## 运行

```sh
python3.11 demo.py                 # 端到端样例
python3.11 -m unittest discover -s tests -v   # 测试
```

## 运行样例（`python3.11 demo.py` 摘要）

```
before:  s0: 90/120  s1: 65/120  s2: 45/120   max load: 90
solver: status=OPTIMAL nodes=156
  max_load=75 moved_bytes=65
  proven lower_bound=67 gap=0
  independent verification: OK
after (planned):  s0: 75/120  s1: 65/120  s2: 60/120   max load: 75
deterministic plan plan-1 (generation 1):
  move p1: s0 -> s2 (40 bytes)
  move p4: s2 -> s0 (25 bytes)
execute: success=True rolled_back=False   budget spent=65 remaining=135
replay same plan (idempotent): success=True budget spent now=65 (all skipped)
stale ack rejection: rejected: stale ack for p1: generation 0 != 1
failure + step-by-step rollback: budget after rollback: spent=0 remaining=200 (no leak)
oversized partition cannot be split: status=INFEASIBLE unmet_bytes=50
```

## 测试结果

`python3.11 -m unittest discover -s tests`：**Ran 28 tests — OK**

覆盖：null/NaN 区分与列类型校验、大整数精确计算、最优性与字典序目标、
亲和/反亲和、预算约束、超大分区与总容量不足的未满足量、UNDECIDED 的下界与 gap、
计划确定性、独立校验器、分阶段顺序、失败逐步回滚、代际拒绝、幂等重放、失败后重试。
