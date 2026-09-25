# mergeagg — 有序多流归并聚合（支持 checkpoint / 恢复）

纯 Python 3.11 标准库实现，不依赖任何现成查询引擎。对**按归并键排序**的多个
输入流做 k 路归并，按键分组聚合，并支持在任意时刻（包括一个组横跨多条流的
中途）暂停、保存 checkpoint、从 checkpoint 恢复，且**不会重复输出已提交的组**。

## 语义

- **批次输入**：表以 `Batch(columns, types, rows)` 形式进入，列类型为
  `INT / FLOAT / STRING`，行在构造时即校验。
- **NULL ≠ NaN**：`NULL` 是单例哨兵，与 `float("nan")` 严格区分。
  所有聚合跳过 `NULL`；`NaN` 是真实值：`COUNT` 计入，`SUM/MIN/MAX` 结果为 `NaN`。
- **整数精确计算**：整数求和始终停留在 `int`，任意精度（如 `2**70`）无舍入。
- **临时无数据 ≠ EOF**：`poll()` 返回 `NOT_READY` 时归并器等待重试
  （`max_not_ready` 次后抛 `StreamNotReadyError`），只有 `EOF_SIGNAL` 才结束流。
- **游标版本校验**：恢复时流的 `version` 与 checkpoint 中不一致即抛
  `CursorVersionMismatchError`；流集合不一致抛 `CursorError`。

## Checkpoint 内容

- `cursors`：每条流的 `(stream_id, version, offset)`（offset = 已消费行数）。
- `pending`：未完成的组（键、各聚合函数的部分状态、已为该键耗尽的流 id 列表）。
- `committed_upto`：已提交键水位线，恢复后任何 ≤ 水位线的组都不会再次输出。

序列化为 JSON 时所有值带类型标签（`null` / `nan` / `int` / `float` / `str`），
整数按十进制字符串编码以保证精度。

## 用法

```python
from mergeagg import AggSpec, ListStream, MergeAggregator, Checkpoint

merger = MergeAggregator(streams, "k", [AggSpec("v", "sum")])
merger.run(pause_after_rows=100)        # 在任意行边界暂停（可位于组中途）
payload = merger.checkpoint().to_json() # 保存 checkpoint

restored = MergeAggregator.restore(     # 恢复：校验版本、seek 各流、继续归并
    Checkpoint.from_json(payload), fresh_streams, "k", [AggSpec("v", "sum")],
)
restored.run()
```

## 运行

```bash
python3.11 run_sample.py                 # 端到端样例（暂停/恢复/版本错配演示）
python3.11 -m unittest discover -s tests -v
```

## 结构

- `mergeagg/types.py` — `NULL` 哨兵、`ColumnType`、`Batch`、值标签编解码
- `mergeagg/aggregator.py` — `SUM/COUNT/MIN/MAX` 可序列化聚合状态
- `mergeagg/streams.py` — `Stream` 抽象与 `ListStream`（`NOT_READY`/`EOF` 信号）
- `mergeagg/checkpoint.py` — `Checkpoint` / `CursorState` / `PendingGroup`
- `mergeagg/merger.py` — `MergeAggregator`（归并、暂停、checkpoint、恢复）
- `tests/test_mergeagg.py` — unittest 测试
