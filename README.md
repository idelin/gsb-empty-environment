# releng：带共享预算的内存关系执行引擎

纯 Python 3.11 标准库实现（不调用任何 SQL 引擎）。合成关系表在本地以元组
列表提供（`releng/tables.py`），算子以火山模型逐行拉取执行，整个算子树共享
同一份行数 / 内存 / 步骤预算。

## 目录结构

- `releng/core.py` — `Budget`、`TerminationReason`、`ExecutionContext`、`ResourceRegistry`、三值逻辑比较器
- `releng/operators.py` — `Scan / Filter / Project / Limit / HashJoin / NestedLoopJoin / HashAggregate / Sort`
- `releng/executor.py` — `execute()` 驱动算子树，统一收尾并返回 `QueryResult`
- `releng/tables.py` — 本地合成表 `users` / `orders`（含 NULL 与重复行）
- `main.py` — 运行样例（`python3 main.py`）
- `tests/test_engine.py` — 26 个 unittest 用例

## 预算模型

`Budget(max_rows, max_memory, max_steps)` 由一次查询的所有算子共享：

- **行数**：每次“行操作”（从表或子算子消费一行）计 1。
- **内存**：算子为缓冲区（哈希表、分组表、排序缓冲）显式 `mem_alloc` /
  `mem_free`，当前占用超限即中断；记录峰值。
- **步骤**：每次行处理或连接探测计 1。

预算是硬上限：越界的那次操作先记账再抛 `BudgetExhausted`，因此部分统计
不漏记。`budget.cancel()` 后下一个记账点抛 `QueryCancelled`。

## 结果契约

`execute()` 返回 `QueryResult`：

- `complete`：仅当 `termination ∈ {EXHAUSTED, LIMIT}` 时为 `True`。
- `termination`：`EXHAUSTED`（输入耗尽）/ `LIMIT`（正常提前结束）/
  `BUDGET_ROWS` / `BUDGET_MEMORY` / `BUDGET_STEPS` / `CANCELLED`。
- `partial_aggregates`：聚合算子未消费完全部输入时为 `True` —— 此时返回的
  是累加器中间值，**绝不冒充最终值**。
- `stats`：行操作数、步骤数、内存峰值、创建的临时文件数（部分结果同样带
  完整统计）。

`Limit` 取够 n 行后会再探测一次输入：有剩余则标记 `LIMIT`（正常提前结束），
输入恰好耗尽则为 `EXHAUSTED` —— 以此与预算中断明确区分。

## 重复子计划共享预算

`NestedLoopJoin` 的内层子计划对每一行外层行重新执行一次，所有重复执行都向
同一个 `Budget` 记账。例如 3×4 的嵌套循环连接：行操作数 =
3（外 Scan）+ 3（外循环）+ 12（内 Scan ×3）+ 12（内循环）= 30，一步不漏。

## 取消与临时资源

`Sort` 缓冲区达到 `spill_bytes` 即排序落盘为临时 run 文件（登记在
`ResourceRegistry`），最后归并输出。无论正常结束、预算中断还是取消，
执行器在 `finally` 中统一 `release_all()` 删除临时文件；各算子的缓冲区内存
也在 `finally` 中归还预算（可用 `budget.used_memory == 0` 验证）。

## NULL 与多重集合语义

- 比较走三值逻辑（`sql_eq/lt/gt/le/ge`）：任一操作数为 NULL 则结果为
  UNKNOWN；`Filter` 只保留谓词为 `True` 的行。
- 等值连接中 NULL 键永不匹配（NULL ≠ NULL）。
- 聚合忽略 NULL 输入；`count(*)` 统计全部行，`count(col)` 跳过 NULL；
  全 NULL 组的 `sum/avg/min/max` 为 None。
- `GROUP BY` 把 NULL 键归入同一组。
- 多重集合（bag）语义：重复行原样保留，`count(*)` 计入重复，连接产生
  |左匹配| × |右匹配| 行。

## 运行样例（python3 main.py）

```
== 合成表 ==
users: 8 行（含 NULL 与重复行）
orders: 12 行（含 NULL 与重复行）

== 1. 完整聚合：各城市已支付订单 ==
complete=True termination=EXHAUSTED partial_aggregates=False
stats: rows=57 steps=65 peak_mem=3040B temp_files=0
  {'city': 'shanghai', 'n_orders': 4, 'total': 384.5, 'n_amount': 4}
  {'city': 'beijing', 'n_orders': 5, 'total': 104.0, 'n_amount': 5}

== 2. 步骤预算=50 -> 部分聚合（非最终值） ==
complete=False termination=BUDGET_STEPS partial_aggregates=True
stats: rows=47 steps=51 peak_mem=3040B temp_files=0
  {'city': 'shanghai', 'n_orders': 4, 'total': 384.5, 'n_amount': 4}
  {'city': 'beijing', 'n_orders': 1, 'total': 44.0, 'n_amount': 1}

== 3. LIMIT 3（正常提前结束） ==
complete=True termination=LIMIT partial_aggregates=False
stats: rows=7 steps=7 peak_mem=0B temp_files=0
  {'user_id': 1, 'name': 'alice', 'age': 34, 'city': 'shanghai'}
  {'user_id': 2, 'name': 'bob', 'age': 28, 'city': 'beijing'}
  {'user_id': 3, 'name': 'carol', 'age': None, 'city': 'shanghai'}

== 3b. 行数预算=3（预算中断，对照） ==
complete=False termination=BUDGET_ROWS partial_aggregates=False
stats: rows=4 steps=3 peak_mem=0B temp_files=0
  {'user_id': 1, 'name': 'alice', 'age': 34, 'city': 'shanghai'}
  {'user_id': 2, 'name': 'bob', 'age': 28, 'city': 'beijing'}
  {'user_id': 3, 'name': 'carol', 'age': None, 'city': 'shanghai'}

== 4. 排序落盘（spill_bytes=4096） ==
complete=True termination=EXHAUSTED partial_aggregates=False
stats: rows=600 steps=600 peak_mem=4172B temp_files=21
  {'id': 0, 'tag': 'k00', 'v': 0}
  {'id': 11, 'tag': 'k07', 'v': 1}
  ... 共 300 行
排序后剩余临时文件: []

== 5. 执行到第 120 步被取消 ==
complete=False termination=CANCELLED partial_aggregates=False
stats: rows=120 steps=120 peak_mem=4172B temp_files=4
取消后剩余临时文件: []

== 6. 嵌套循环连接 3x4（内层重复执行 3 次） ==
complete=True termination=EXHAUSTED partial_aggregates=False
stats: rows=30 steps=30 peak_mem=0B temp_files=0
  {'x': 1, 'y': 10}
  {'x': 1, 'y': 20}
  ... 共 12 行
```

样例 2 中 `beijing` 的 `total=44.0` 只是已处理部分的累加值，
`partial_aggregates=True` 明确标示它不是最终值（完整结果应为 104.0）。

## 测试结果（python3 -m unittest discover -s tests）

```
Ran 26 tests in 0.037s

OK
```

覆盖：三类预算中断与部分统计、部分聚合标记、LIMIT 与预算中断的区分、
重复子计划共享预算记账（精确到行/步数）、取消与中断后临时文件释放、
排序落盘正确性、三值逻辑过滤、NULL 连接键、聚合忽略 NULL、
GROUP BY NULL 分组、多重集合语义（重复保留 / count(*) 计重复 / 连接乘积）。
