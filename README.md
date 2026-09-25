# 类型化谓词树、三值逻辑与统计块裁剪

本项目只使用 Python 3.11 标准库实现，不调用 SQL、Pandas、Polars 或其他现成查询引擎。

## 文件

- `src/predicate_tree.py`：列类型、类型化谓词节点、三值逻辑、逐行解释器和批统计。
- `src/block_pruning.py`：块级抽象结果集合、安全跳过判定和跳过理由。
- `src/query.py`：带列类型的批次输入、逐行参考扫描、裁剪扫描和一致性审计。
- `tests/test_predicate_pruning.py`：`unittest` 单元测试和随机对照测试。
- `demo.py`：可直接运行的端到端样例。

## 语义

- 支持列类型：`INTEGER`、`FLOAT`、`BOOLEAN`、`TEXT`。
- 整数使用 Python `int`，大整数不转浮点，比较和相等判断保持精确。
- SQL `NULL` 与浮点 `NaN` 是不同值；`NaN IS NULL` 为 `false`。
- 比较中遇到 `NULL` 得到 `unknown`。
- 浮点 `NaN` 遵守 IEEE 754 比较：普通序比较为 `false`，`NaN <> NaN` 为 `true`。
- 三值逻辑中 `NOT unknown = unknown`。
- 块统计包含 `null_count`、`nan_count`、有限非 NaN 值的 `value_count`、`minimum`、`maximum`。
- 只有块的抽象可能结果不含 `true` 时才跳过块；跳过决策返回 `reason_code` 和中文理由。
- 范围统计对 `=` 采取保守策略：只要字面量位于 `[minimum, maximum]` 内就继续扫描，即使离散整数列也不假设有值域索引。
- `<>` 在统计上可证明无匹配的条件是：没有 NULL/NaN，且所有有限非 NaN 值都等于字面量。

## 运行样例

```bash
python3.11 demo.py
```

样例覆盖：

- 全 NULL 块：`x > 2` 可跳过，`x IS NULL` 不可跳过。
- `[0, 2]` 整数块：`x > 2` 由最大值证明无 true。
- 含 NaN 的浮点块：`x > 2`、`NOT (x > 2)` 与逐行解释器结果审计一致。
- `NOT` 对 NaN false 分支求反为 true，同时对 NULL unknown 仍保留 unknown。

## 运行测试

```bash
python3.11 -m unittest discover -s tests -v
```

当前验证结果：

```text
Ran 22 tests in 0.471s

OK
```

## 最小用法

```python
from predicate_tree import Column, ColumnType, Comparison, ComparisonOp, Literal
from query import typed_batch, scan_row_by_row, scan_with_pruning, audit_pruning

x = Column("x", ColumnType.INTEGER)
batches = [typed_batch([x], [{"x": None}, {"x": None}])]
predicate = Comparison(x, ComparisonOp.GT, Literal(2))

reference = scan_row_by_row(batches, predicate)
pruned = scan_with_pruning(batches, predicate)
report = audit_pruning(reference, pruned)

assert report.ok
assert pruned[0].skipped
assert "全为 NULL" in pruned[0].decision.reason
```
