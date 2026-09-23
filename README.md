# minirel

一个纯标准库的迷你关系执行引擎（Python 3.11+，无 SQL 引擎依赖）。
关系表是本地合成的 Python 数据；算子采用 Volcano 风格的 `open/next/close`
拉取接口，演示共享资源预算、NULL / 多重集合语义、limit 与预算中断的
区分、重复子计划共享预算，以及取消后临时资源的确定性释放。

## 目录结构

- `minirel/budget.py` — `Budget` / `BudgetTracker`：行数、内存（字节）、
  步骤三类预算的唯一记账点；`BudgetExhausted` 与 `Cancelled` 异常。
- `minirel/table.py` — 合成关系表（bag）、`NULL` 哨兵与三值逻辑
  （`sql_and/or/not/eq/lt`）。
- `minirel/operators.py` — `Scan / Filter / Project / HashJoin /
  Aggregate / Limit / Sort / SharedSubplan`。
- `minirel/executor.py` — 驱动算子树，返回 `Result(status, rows,
  stats, partial)`。
- `examples/run_sample.py` — 6 个端到端场景。
- `tests/` — 24 个 `unittest` 用例。

## 关键设计

**共享预算，不漏记。** 一个查询只有一个 `BudgetTracker`，所有算子向它
记账。行计数只在 `Scan.next()` 收取（重复行、连接产生的行不重复计费）；
HashJoin build 侧、Aggregate 缓冲、Sort 缓冲与 SharedSubplan 缓存按
估算字节（`ROW_BYTES=64`）计内存，`close()` 时归还。超预算的那一拍
也会留下计数（`used > limit` 的过冲量体现在部分统计中），然后立即抛出
`BudgetExhausted`。

**四种结束状态严格区分。**

- `complete`：子管道自然耗尽；
- `limit`：`Limit` 主动停止（正常提前结束，无异常、无 partial）；
- `budget`：预算中断，返回部分统计（行列数、步数、峰值内存、命中的预算
  维度与用量）和部分聚合；
- `cancelled`：算子在检查点观察到取消标志。

**部分聚合不冒充最终值。** `Aggregate` 是阻塞算子，中途被打断时只能通过
`Result.partial` 取得 `PartialAggregate(rows, complete=False)`；该类型禁止
构造 `complete=True`，正常结束时 `partial` 为 `None`。

**NULL 语义。** `NULL` 是独立哨兵而非 `None`，`bool(NULL)` 直接报错以防
误用；谓词产生 TRUE/FALSE/UNKNOWN（UNKNOWN 用 NULL 表示），UNKNOWN 行
被过滤；连接键为 NULL 永不匹配；分组键 NULL 同组；`COUNT(*)` 计数所有
行，`COUNT(col)` 与 `SUM/AVG/MIN/MAX` 跳过 NULL，全 NULL 聚合为 NULL。

**多重集合（bag）语义。** 表保留重复行，无隐式 DISTINCT；内连接按匹配
的叉积产生重复（2×3 → 6 行）。

**重复子计划共享。** `SharedSubplan` 只执行一次子节点，多个 consumer
读同一份缓存；行/步骤预算只在实际扫描时收取一次，consumer 读缓存不再
计费（缓存字节计入共享内存预算）。

**取消与临时资源。** `Sort` 超阈值溢写到本地临时文件；`Executor.run()`
在 `finally` 中递归 `close()`，预算中断与取消路径都会删除溢写文件并归还
内存（样例与测试均断言临时目录为空、`tracker.memory == 0`）。

## 运行

```bash
python3 examples/run_sample.py
python3 -m unittest discover -s tests -v
```

## 样例输出

```
=== 1. full aggregation (COUNT(*), COUNT(salary), SUM, AVG per dept) ===
status      : complete
rows        : [(10, 2, 1, 90000, 90000.0), (20, 3, 3, 240000, 80000.0), (30, 1, 1, 60000, 60000.0), (NULL, 1, 1, 70000, 70000.0)]
stats       : rows=7 steps=8 peak_mem=0B

=== 2. same query, row budget = 4 -> partial stats, incomplete flag ===
status      : budget
message     : rows budget exhausted: used 5, limit 4
rows        : []
stats       : rows=5 steps=5 peak_mem=0B
budget hit  : rows (5/4)
partial agg : complete=False rows=[(10, 2, 1, 90000, 90000.0), (20, 1, 1, 80000, 80000.0), (NULL, 1, 1, 70000, 70000.0)]

=== 3. join with NULL keys + bag semantics, LIMIT 3 (normal early stop) ===
status      : limit
rows        : [('brian', 'eng'), ('ada', 'eng'), ('eli', 'ops')]
stats       : rows=9 steps=10 peak_mem=384B

=== 4. same join without limit but step budget = 12 (budget interrupt) ===
status      : budget
message     : steps budget exhausted: used 13, limit 12
rows        : [('brian', 'eng'), ('ada', 'eng'), ('eli', 'ops'), ('eli', 'ops'), ('chen', 'ops'), ('eli', 'ops'), ('eli', 'ops'), ('chen', 'ops'), ('fang', 'hr')]
stats       : rows=11 steps=13 peak_mem=384B
budget hit  : steps (13/12)

=== 5. duplicate subplan shares one execution and one budget ===
status      : complete
rows        : [('brian',... 'chen', ...), ...]   # 6 行 = 2 x 3 多重集合
stats       : rows=7 steps=8 peak_mem=576B
note        : table has 7 rows, budget charged rows=7 (read once)

=== 6. cancel a spilling sort -> temp files released ===
status      : cancelled
message     : query cancelled
stats       : rows=54001 steps=54001 peak_mem=128000B
temp files left: []
```

## 测试结果

```
Ran 24 tests in 0.007s

OK
```

覆盖：行/步/内存预算中断与部分统计、部分聚合不可标记为完成、三值逻辑、
NULL 过滤与连接、COUNT/SUM/AVG 的 NULL 处理、bag 重复与连接重数、
limit 与 budget 胜负判定、共享子计划单次计费（含预算下不漏记）、
取消后溢写文件删除与内存归还。
