# minijoin — 从零实现的分区溢写等值连接引擎

纯 Python 3.11 标准库实现（不调用任何现成查询引擎），支持
**INNER / LEFT / FULL OUTER** 等值连接，表以**带列类型的批次（Batch）**流式输入，
在小内存预算下通过 **GRACE 式哈希分区 + 磁盘溢写**完成连接，并保持**可定义的输出次序**。

## 语义约定

- **重复键按笛卡尔重数展开**：左键出现 `m` 次、右键出现 `n` 次 → 输出 `m × n` 行
  （左 2 条、右 3 条 → 6 条）。
- **NULL 默认不匹配**：`NULL` 不等于任何值（包括 `NULL` 自己）；LEFT/FULL 按外连接
  语义把含 NULL 键的行作为未匹配行保留。
- **NULL 与 NaN 不同**：`NULL` 是独立单例哨兵；`NaN` 是合法的 FLOAT 值，
  `NaN` 与 `NaN` 可以匹配（哈希前归一化为同一 token）。
- **整数精确计算**：键使用 Python 任意精度 `int`，`2**70+7` 这类大整数精确匹配；
  `BOOL` 不会被当作 `INT`。
- **键列类型必须一致**，支持多列复合键。

## 执行模型（小内存分区溢写）

1. 两侧输入按批次流式读入，按 `hash(key) % num_partitions` 分区；分区缓冲超过
   冲刷阈值（由 `memory_limit_rows` 推导）即 pickle 溢写到临时目录。含 NULL 键的
   行进入专用分区（永不匹配）。
2. 同序号分区对独立连接；放不进 `memory_limit_rows` 的分区对用加盐哈希**递归再分区**。
   终止保证：分区对放得下、达到 `max_depth`、或再分区**毫无进展**（例如所有键相同）
   时直接内存连接兜底 —— **全键相同也不会无限递归**。
3. 输出行携带排序键，默认 `OutputOrder.LEFT`：匹配/未匹配左行按**左输入次序**输出
   （同一左行的多条匹配按右输入次序），FULL 的未匹配右行按右输入次序追加在最后；
   因此溢写与否输出完全一致。`OutputOrder.PARTITION` 则按分区处理次序输出（更省，
   但仅对固定 hash seed 确定）。

## 代码结构

```
minijoin/
  types.py    # NULL 哨兵、ColType、Schema、Batch（带类型校验的批次输入）
  joiner.py   # Joiner：分区、溢写、递归再分区、内存哈希连接、次序控制
tests/test_join.py   # 19 个 unittest 用例
examples/demo.py     # 运行样例
```

## 用法

```python
from minijoin import NULL, Batch, ColType, Joiner, JoinType, Schema

emp  = Schema([("id", ColType.INT), ("dept", ColType.INT), ("name", ColType.STR)])
dept = Schema([("dept", ColType.INT), ("building", ColType.STR)])

joiner = Joiner(emp, dept,
                left_keys=["dept"], right_keys=["dept"],
                join_type=JoinType.FULL,
                memory_limit_rows=4,   # 极小内存预算 -> 触发分区溢写
                num_partitions=2)
rows = joiner.collect([Batch(emp,  [(1, 10, "alice"), (2, NULL, "dave")])],
                      [Batch(dept, [(10, "hq"), (NULL, "unassigned")])])
```

## 运行样例

```
$ python3.11 examples/demo.py

== inner join (memory_limit_rows=4, spilled) (7 rows) ==
id | dept | name | right.dept | building
1 | 10 | 'alice' | 10 | 'hq-a'
1 | 10 | 'alice' | 10 | 'hq-b'
1 | 10 | 'alice' | 10 | 'hq-c'
2 | 10 | 'bob' | 10 | 'hq-a'
2 | 10 | 'bob' | 10 | 'hq-b'
2 | 10 | 'bob' | 10 | 'hq-c'
5 | 1180591620717411303431 | 'eve' | 1180591620717411303431 | 'skunkworks'

== left join (memory_limit_rows=4, spilled) (9 rows) ==
id | dept | name | right.dept | building
1 | 10 | 'alice' | 10 | 'hq-a'
1 | 10 | 'alice' | 10 | 'hq-b'
1 | 10 | 'alice' | 10 | 'hq-c'
2 | 10 | 'bob' | 10 | 'hq-a'
2 | 10 | 'bob' | 10 | 'hq-b'
2 | 10 | 'bob' | 10 | 'hq-c'
3 | 20 | 'carol' | NULL | NULL
4 | NULL | 'dave' | NULL | NULL
5 | 1180591620717411303431 | 'eve' | 1180591620717411303431 | 'skunkworks'

== full join (memory_limit_rows=4, spilled) (11 rows) ==
id | dept | name | right.dept | building
1 | 10 | 'alice' | 10 | 'hq-a'
1 | 10 | 'alice' | 10 | 'hq-b'
1 | 10 | 'alice' | 10 | 'hq-c'
2 | 10 | 'bob' | 10 | 'hq-a'
2 | 10 | 'bob' | 10 | 'hq-b'
2 | 10 | 'bob' | 10 | 'hq-c'
3 | 20 | 'carol' | NULL | NULL
4 | NULL | 'dave' | NULL | NULL
5 | 1180591620717411303431 | 'eve' | 1180591620717411303431 | 'skunkworks'
NULL | NULL | NULL | NULL | 'unassigned'
NULL | NULL | NULL | 30 | 'annex'
```

要点：dept=10 左 2 条 × 右 3 条 = 6 行；两侧的 NULL 键行互不匹配、
按外连接语义各自保留；`2**70+7` 大整数键精确匹配。

## 测试结果

```
$ python3.11 -m unittest discover -s tests -v
test_many_small_batches_equal_one_big_batch ... ok
test_multi_column_keys ... ok
test_output_schema_renames_collisions ... ok
test_type_validation ... ok
test_big_ints_match_exactly ... ok
test_bool_is_not_int_key ... ok
test_cartesian_multiplicity_2x3_gives_6 ... ok
test_empty_inputs ... ok
test_inner_drops_null_keys_and_unmatched ... ok
test_nan_is_not_null_and_matches_nan ... ok
test_null_does_not_match_nan ... ok
test_full_join_keeps_both_sides_nulls_included ... ok
test_left_join_keeps_unmatched_and_null_left ... ok
test_null_never_matches_null ... ok
test_all_keys_identical_full_outer ... ok
test_all_keys_identical_no_infinite_recursion ... ok
test_left_order_preserved_under_spill ... ok
test_spill_files_are_actually_written ... ok
test_spill_results_identical_to_in_memory ... ok

----------------------------------------------------------------------
Ran 19 tests in 0.929s

OK
```

测试覆盖：2×3→6 笛卡尔重数、NULL 不匹配且被外连接保留、NaN≠NULL 且
NaN 可匹配、大整数精确键、溢写与全内存结果逐行一致、溢写下左序保持、
溢写文件真实落盘、全键相同不无限递归（60×40=2400 行）、多列复合键、
批次大小无关性、类型校验与输出列重命名。

## 限制

- 达到 `max_depth` 或再分区无进展时，退化分区对在内存中兜底连接
  （病态数据下可能超出内存预算，但保证终止）。
- `OutputOrder.LEFT` 需要在内存中缓冲输出行用于最终排序。
