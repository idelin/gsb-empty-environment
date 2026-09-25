# 带权分区迁移（固定执行槽 + 每轮字节预算）

Python 3.11，仅标准库。表按带列类型的批次输入，不调用任何现成查询引擎。

## 模块

- `typed_batch.py` — 带列类型的批次输入。`NULL` 是独立哨兵，与 `NaN` 不同；
  INT 列用 Python 整数精确求和（无 64 位溢出）；`concat` 校验 schema；
  `group_sum_int` / `sum_float` 为手写聚合（NULL 跳过、NaN 传播）。
- `model.py` — 槽（容量）、分区（精确整数权重、亲和槽集合）、反亲和对、
  当前放置。超大分区永不拆分。
- `solver.py` — 小规模精确分支定界。字典序目标：先最小化最大槽负载，
  再最小化迁移字节。联合满足槽容量、亲和/反亲和。输出可独立校验的分配
  （`verify_assignment`）、已证下界与未决 gap；搜索树耗尽时 `gap == 0`
  （已证最优），节点上限触发时返回最优可行解 + 下界 + 未闭合 gap。
  放不下的分区计入 `unmet`（未满足量），绝不假装拆分。
- `planner.py` — 把当前放置到目标分配的迁移按每轮字节预算装箱成确定性
  轮次；`plan_id` 为内容哈希，同输入同 id；单分区超过预算列入
  `unschedulable`，不强行调度。
- `executor.py` — 四阶段执行 `RESERVE -> COPY -> SWITCH -> RELEASE`。
  所有权切换是带代际检查的 CAS（不会双重拥有）；旧代际的完成回包与
  过期 SWITCH 被拒绝；预留计费到轮次预算，释放/回滚精确退还（无预算
  泄漏）；失败按逆序逐步回滚元数据；每个阶段幂等，同计划重放不会
  重复占用目标槽。`consistency_report` 重算全部安全不变量。

## 运行

```bash
python3.11 demo.py                          # 端到端样例
python3.11 -m unittest test_partition_migration -v   # 19 个测试
```

## 运行样例（`python3.11 demo.py`，节选）

```
=== 1. typed batch input (NULL != NaN, exact ints) ===
merged batches: 5 + 7 = 12 rows
load_factor sum is NaN (NaN != NULL, NaN propagates): True
exact integer weights: {'p1': 500, ..., 'p_huge': 5000}

=== 2. exact solve (min max-load, then min moved bytes) ===
before:  s0: 1250  s1: 550  s2: 350  s3: 240   (capacity 1300)
after:   s0:  600  s1: 590  s2: 600  s3: 600
max load: 600  proved lower bound: 600  gap: 0
moved bytes: 1390 (lower bound 1390)  status: proven optimal
UNMET demand (never split, fits no slot): {'p_huge': 5000} total=5000 bytes
independent verification: ok=True errors=[]

=== 3. deterministic plan under per-round budget ===
plan_id=65cfe0ccc30622bc generation=7 budget/round=600
  round 0: 550 bytes  [p2:s0->s2(400), p7:s2->s1(150)]
  round 1: 600 bytes  [p3:s0->s3(350), p5:s1->s3(250)]
  round 2: 240 bytes  [p8:s3->s0(100), p9:s3->s1(80), p10:s3->s1(60)]

=== 5. failure injection -> staged metadata rollback ===
caught: migration of p2 failed at copy: injected failure at copy
ownership of p2 still: s0
reservations on target: {}
budget ledger after rollback: {0: 0}
consistency: {'ok': True, 'errors': []}

=== 6. generation fencing + idempotent replay ===
stale ack rejected: ack for p1 from generation 6 rejected; current generation is 7
replayed plan 65cfe0ccc30622bc: 7/7 steps skipped
consistency after replay: {'ok': True, 'errors': []}
```

## 测试结果

```
Ran 19 tests in 0.005s
OK
```

覆盖：NULL≠NaN、超 64 位精确整数求和、schema 校验；均衡最优与 gap=0、
亲和/反亲和、不可行报错、超大分区 unmet、节点上限下的下界与未决 gap；
轮次预算装箱、超预算移动不可调度、plan_id 确定性；四阶段提交、
COPY/SWITCH 失败回滚（无双重拥有、预算归零）、旧代际回包拒绝、
同计划重放幂等、预留容量约束。
