# rowselect — 可组合行选择位图

纯标准库实现（Python 3.11+），不依赖任何现成查询引擎。

## 结构

- `rowselect/bitmap.py` — 分块压缩位图：每 512 行为一个段，稀疏全零段存 `0`、
  密集全一段存哨兵 `_FULL`、部分填充段存位掩码 int；AND/OR/XOR/NOT 按段运算并
  重新归一化，稀疏与密集区域都保持压缩。
- `rowselect/table.py` — 带列类型（`int`/`bool`/`float`/`str`）的 `Schema`、
  按批次输入的 `Batch`/`Table`，以及产生位图的谓词表达式（`col(...)`）。
- `tests/` — unittest 测试套件；`demo.py` — 运行样例。

## 语义要点

- **组合**：`&` `|` `^` `~`；长度不同的位图组合抛出 `ValueError`。
- **NOT**：只翻转有效行范围，尾部填充位被掩码清除，绝不泄漏。
- **rank/select**：`rank(i)` 为 `[0, i)` 中选中行数；`select(k)` 返回第 k 个
  （0 起始）选中行；行 i 被选中时 `select(rank(i)) == i`。
- **空位图**：长度 0 时 `rank(0) == 0`、`select(0)` 抛 `IndexError`、
  `~empty` 仍为空。
- **null vs NaN**：`None` 是 null，比较按三值逻辑不选中；NaN 是普通浮点值，
  与一切（含自身）比较不等；`is_null()` / `is_nan()` / `is_not_null()` 可区分。
- **整数精确**：整数始终保持 Python int，任意大小比较不经过浮点舍入。

## 运行样例（`python3 demo.py`）

```
table: 6 rows in 2 batches

price < 20.0        -> [0, 4] (null/NaN excluded)
tag == 'a'          -> [0, 2]
AND                 -> [0]
OR                  -> [0, 2, 4]
NOT cheap           -> [1, 2, 3, 5]
price IS NULL       -> [1, 5]
price IS NaN        -> [2]
fetch(tag == 'b')   -> [(2, None, 'b'), (5, 7.25, 'b')]

-- rank/select --
selection: [0, 2, 3, 4]
  rank(0) = 0, select(0) = 0
  rank(2) = 1, select(1) = 2
  rank(3) = 2, select(2) = 3
  rank(4) = 3, select(3) = 4

-- NOT tail padding (length 5, chunk 512) --
~zeros(5): [0, 1, 2, 3, 4] popcount = 5

-- length mismatch --
ValueError: bitmap length mismatch: 6 != 7
```

## 测试结果（`python3 -m unittest discover -s tests`）

```
Ran 55 tests in 0.711s

OK
```
