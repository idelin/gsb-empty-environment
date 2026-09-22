# pullpipe：Pull 式有界批次算子管线（纯 Python 3.11 标准库）

一个从零实现的迷你关系执行引擎：算子以 **pull** 方式逐批拉取数据（`next_batch()`），
支持提前 `LIMIT` 停止、外部取消、异常时沿管线释放全部资源。不调用任何 SQL 引擎，
合成关系表在本地提供；仅使用标准库，测试使用 `unittest`。

## 运行

```bash
python3.11 examples/demo.py          # 运行样例（默认百万行大表）
python3.11 -m unittest discover -s tests -v   # 56 个测试
```

## 核心设计

**批次协议（`pullpipe/batch.py`）**
- `next_batch()` 返回：非空 `Batch` / 空 `Batch`（合法心跳）/ `None`（EOF）。
- **空批次 ≠ EOF**：`bool(Batch()) is True`，消费方只能用 `batch is None` 判结束。

**NULL 与三值逻辑（`pullpipe/null.py`）**
- `Null` 单例：任何含 NULL 的比较/算术结果仍是 NULL；逻辑为 True/False/NULL 三态。
- WHERE 判定（`is_true`）：只有 True 通过，False 与 Unknown 都不通过。
- `NULL = NULL` 为 Unknown；但 `NULL IS DISTINCT FROM NULL` 为 False。
- GROUP BY 中所有 NULL 归入同一组；连接键为 NULL 的行永不匹配（内连接）。
- `bool(Null)` 直接抛 `TypeError`，强制显式三值判定，杜绝隐式歧义。

**多重集合语义**
- 关系是袋子不是集合：`UnionAll` 保留重复；HashJoin 对重复键做笛卡尔展开；
  聚合不去重（除非另加算子）。

**算子（`pullpipe/operator.py`）**
- `TableScan / Filter / Project / Limit / HashJoin(inner) / HashAggregate / Sort / UnionAll`。
- 批次大小全程有上界（scan 按 `batch_size` 切片，join/agg 输出也按批切）。
- `HashAggregate` 全量消费后才输出，故在构建循环内逐批检查取消。

**提前停止 / 取消 / 异常关闭（资源沿管线释放）**
- `Limit` 取满（或 n=0）立即 `close()` 上游：百万行表的 LIMIT 1 只产 1 行
  （batch_size=1 时）或至多一个批次，游标生成器随即被 `close()`。
- `CancellationToken` 下发到每个算子，批次边界抛 `CancelledError`。
- 算子内部错误包装为 `OperatorError`（保留原异常）；根算子 `close()` 按
  **先子后己**递归释放，因此下游出错/被取消时上游所有句柄都会关闭（连接两侧同关）。
- `close()` 幂等，重复调用安全；生成器被 GC 回收时其 `finally` 同样关闭整树。
- `Pipeline.batches()` / `iter_rows()` / `run()` / 上下文管理器均保证关闭。

**本地表（`pullpipe/table.py`）**
- `ListTable`（内存）、`GeneratedTable`（惰性合成百万行，构造零成本）、
  `CsvTable`（本地 CSV，文件句柄挂在游标上）。

## 代码结构

```
pullpipe/
  errors.py          PipelineError / OperatorError / CancelledError
  null.py            Null 单例与 3VL（and3/or3/not3/cmp3/arith3/is_distinct）
  batch.py           Batch（空批为真）与 EOF=None
  table.py           Schema、List/Generated/Csv 表与行游标
  expr.py            Col/Lit/BinOp（比较、算术、AND/OR/NOT，自动 3VL）
  operator.py        全部算子 + CancellationToken
  pipeline.py        Pipeline：驱动、取消、关闭
  sample_tables.py   合成关系表（百万订单、含 NULL/重复键的客户与事件表）
examples/demo.py     7 个可运行样例
tests/               unittest 测试（56 个）
```
