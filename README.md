# 有序可溢写流 Merge Join

纯 Python 3.11 标准库实现（不调用任何 SQL 引擎），对两个按键有序、
可溢写的行流执行 merge join，支持左外连接、有界暂存溢写与协作式取消。

## 语义约定

- 每行是首元素为键的元组 `(key, *cols)`；两个输入流都必须按键
  **非递减序**提供。
- **多重集合（multiset）语义**：左键组 m 行、右键组 n 行时输出 `m*n` 行，
  完全相同的重复行也保留。
- **NULL 语义（SQL 风格）**：`None` 视为 NULL。NULL 排序最小、彼此相邻成组，
  但 NULL ≠ NULL，永不参与匹配；左外连接中左 NULL 行以 NULL 填充右侧。
- 左外连接未匹配左行的填充宽度取自 `right_arity` 参数，缺省由已见到的
  右行宽度自动推断。
- 每个键组独立使用一个**有界暂存区**（按行数限内存）：超限后整组溢写到
  临时文件（pickle 追加写），之后可反复从头迭代，因此
  “超大组 × 超大组”也能完成；正常结束或取消都会清理临时文件。
- 输入一旦出现键回退，立即抛 `OrderViolationError`（发生在产出任何相关
  结果之前），不会静默产出错误结果。
- 取消通过 `CancellationToken`（线程安全）实现，在键组读取和逐行产出等
  检查点协作式中断，抛 `JoinCancelled`。

## 文件

- `mergejoin/spool.py` — `BoundedSpool`：有界、可重绕、可溢写的暂存区
- `mergejoin/stream.py` — `OrderedStream`：惰性有序流 + 顺序违约检测
- `mergejoin/join.py` — `merge_join`：核心算法（inner / left）
- `mergejoin/errors.py` — 异常类型与取消令牌
- `mergejoin/relations.py` — 本地合成关系表（小表 + 惰性大表生成器）
- `sample_run.py` — 运行样例
- `tests/test_mergejoin.py` — 30 个 unittest 用例

## 用法

```python
from mergejoin import merge_join

# 内连接：2×3 重复组 -> 6 行
list(merge_join([(2, "a"), (2, "b")],
                [(2, "x"), (2, "y"), (2, "z")], how="inner"))

# 左外连接，键组内存上限 1000 行，超出自动溢写磁盘
for lrow, rrow in merge_join(left_stream, right_stream,
                             how="left", memory_limit=1000, token=token):
    ...
```

输入可以是列表、生成器或 `BoundedSpool.rows()` 等任何行元组可迭代对象；
输出是 `(left_row, right_row_or_None)` 的惰性生成器。

## 运行

```sh
python3 sample_run.py
python3 -m unittest discover -s tests -v
```

## 结果摘要

- 2×3 重复组输出 6 行；合成表内连接 8 行、左外连接 10 行。
- 两侧各 1000 行的 key=2 大组（内存上限 8 行）双侧溢写，完整产出
  1,000,002 行；单侧大组溢写的左外连接同样完成。
- 顺序违约抛 `OrderViolationError`；置位令牌后抛 `JoinCancelled` 且
  临时文件全部清理。
- 测试：30 个用例全部通过（见 `test_results.txt`）。
