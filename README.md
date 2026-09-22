# Pull 式算子管线（Python 3.11，仅标准库）

从空仓库实现的火山模型（pull-based）算子管线：有界批次、提前 `LIMIT` 停止、
协作式取消、异常沿管线关闭，所有资源随 `close()` 级联释放。合成关系表在本地
生成（CSV），不调用任何 SQL 引擎。

## 结构

- `pipeline/core.py` — `Operator` 基类（`open` / `next_batch` / `close`）、
  `UnaryOperator`（带背压缓冲的 `_pull_rows`）、`CancelToken` / `CancelledError`、
  驱动器 `run(root, sink, batch_size)`。
- `pipeline/operators.py` — `CsvScan`、`Filter`、`Project`、`Limit`、`HashJoin`，
  以及三值逻辑比较助手 `sql_eq` 等。
- `make_data.py` — 生成合成表 `data/users.csv`、`data/orders.csv`
  （约 10% NULL、2% 重复行）。
- `demo.py` — 端到端示例：Scan ⋈ Scan → Filter → Project → Limit 3。
- `tests/test_pipeline.py` — 14 个 unittest 用例。

## 契约

- `next_batch(max_rows)` 返回至多 `max_rows` 行的批次；**EOF 用 `None` 表示，
  空批次 `[]` 不是 EOF**，调用方必须继续拉取。
- `close()` 幂等（重复调用安全），并沿管线级联关闭子算子、释放文件句柄。
- 任何 `next_batch` 抛错时，该算子先关闭自身及全部上游再向上传播；
  `run()` 在成功 / 取消 / 异常三种路径上都恰好关闭根算子一次。
- `Limit` 达到上限立即关闭上游（提前停止）；`CancelToken.cancel()` 后下一个
  批次边界抛出 `CancelledError`。
- NULL 语义：`None` 即 NULL，比较产生 UNKNOWN（`None`），`Filter` 只保留
  谓词为真的行；NULL 连接键永不匹配（NULL ≠ NULL）。
- 多重集合（bag）语义：重复行在 Scan/Filter/Project 中保留；`HashJoin` 对
  左 m 次、右 n 次的键产生 m×n 行。

## 运行样例

```
$ python3 make_data.py && python3 demo.py
pipeline: CsvScan(users) JOIN CsvScan(orders) WHERE amount>400 PROJECT (name, amount) LIMIT 3
  row: ('bob', 466.51)
  row: ('bob', 426.49)
  row: ('dave', 481.84)
delivered 3 rows
users rows read    : 2
orders rows read   : 5000 (hash-join build side, fully materialized by design)
users probe stopped after 2 rows thanks to LIMIT 3
all operators closed: users=True, orders=True, join=True, limit=True
users file closed  : True
```

`LIMIT 3` 使命探侧（users）只读了 2 行即停止并关闭文件；build 侧按哈希连接
的语义物化后同样被释放。

## 测试结果

```
$ python3 -m unittest discover -s tests -v
test_batches_never_exceed_max_rows ... ok
test_scan_reads_in_bounded_batches ... ok
test_cancel_stops_pipeline_and_releases_resources ... ok
test_close_propagates_along_pipeline ... ok
test_empty_batch_is_not_eof ... ok
test_empty_batch_passes_through_filter ... ok
test_repeated_close_is_safe ... ok
test_operator_error_closes_its_children ... ok
test_sink_error_closes_whole_pipeline ... ok
test_limit_1_does_not_scan_million_rows ... ok   # 100 万行文件，LIMIT 1 实际读 ≤64 行
test_duplicates_preserved_through_filter_and_project ... ok
test_join_is_bag_product ... ok
test_null_comparison_is_unknown_and_filtered_out ... ok
test_null_never_joins ... ok

Ran 14 tests in 0.647s
OK
```
