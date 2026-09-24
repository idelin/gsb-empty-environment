# ival — 点 / 半开区间连接（纯 Python 标准库）

在空仓库中用 **Python 3.11 + 标准库 + unittest** 实现，不调用任何现成查询
引擎（无 DuckDB / pandas / sqlite / pyarrow）。

## 功能

- **点-区间连接**：点 `p` 匹配半开区间 `[lo, hi)` 当且仅当 `lo <= p < hi`。
  - 点 `5` 不属于 `[0,5)`，但属于 `[5,8)`。
- **区间-区间连接**：两列半开区间，输出来源 ID 与重叠长度
  `max(0, min(hi1,hi2) - max(lo1,lo2))`。
  - `[0,7)` 与 `[3,5)` 交长 `2`；相邻的 `[0,5)` 与 `[5,8)` 不匹配。
- **零长 / 空区间不匹配**：`lo >= hi`（含 `[x,x)` 与反向区间）不入索引，
  计入统计；点连接的命中行 `overlap_len` 恒为 `0`（点是零长度对象，语义
  与区间连接的“正长度才匹配”区分开）。
- 支持**重复端点、重复 ID、嵌套区间、负端点、空表、多批次**输入。
- 表是**带列类型的列式批次**（`int64 / float64 / bool / string`），
  追加批次时严格校验：
  - `None` = SQL null；`float('nan')` = NaN，**null 与 NaN 不同**
    （NaN 也不等于 NaN），分别统计跳过行；
  - 精确类型：`int64` 列不接受 `bool`/`float`，`float64` 列不接受 `int`
    （不做隐式提升）；两侧数值域不同（int vs float）拒绝连接；
  - **整数精确计算**：int 端点的重叠长度始终是 `int`，对 `10**20` 量级
    也无浮点误差；不相交时返回整数 `0`。

## 避免全笛卡尔扫描的索引

`ival/index.py` 实现**居中区间树（centered interval tree）**：

- 建树 O(n log n)：每层取 lo 中位数作中心，把区间分到左 / 右 / 覆盖组，
  覆盖组按 lo、hi 各排序一次（取 lo 中位数保证每层都有区间被吸收，
  全部区间相同也能终止）；
- 点刺查 `stab` 与重叠查 `overlaps` 均为 O(log n + k)，用 `bisect`
  在有序端点数组上截断，只沿可能命中的分支递归、只产出真命中区间；
- 连接结果含统计：`naive_pairs`（朴素 n×m 比较数）对比
  `examined_pairs`（索引实际枚举的候选数）。见测试中
  “空隙查询 1000 条区间零枚举”的用例。

`ival/joins.py` 另给一套**扫描线**等价实现用于对账：事件序
`结束(0) < 开始(1) < 点(2)`，同一坐标上先移除到期区间再加入新区间，
精确表达半开边界（点 5 先离开 `[0,5)` 再进入 `[5,8)`）。

## 目录

```
ival/values.py   标量类型、null/NaN 语义、精确校验
ival/table.py    Schema / Field / RecordBatch / Table（带类型的列式批次）
ival/index.py    Interval / IntervalIndex（居中区间树）
ival/joins.py    点-区间、区间-区间连接（索引 + 扫描线两套实现）
demo.py          运行样例（输出见 demo_output.txt）
tests/           unittest 测试（含随机暴力三方对账）
```

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 测试
python3.11 demo.py                             # 运行样例
```

## API 速览

```python
from ival import (Schema, RecordBatch, Table,
                  point_interval_join, interval_interval_join)

schema = Schema.from_pairs([("id", "int64"), ("lo", "int64"), ("hi", "int64")])
table = Table(schema, [RecordBatch(schema, {"id": [1], "lo": [0], "hi": [5]})])

res = point_interval_join(point_table, "id", "p",
                          interval_table, "id", "lo", "hi")
# res.matches: [Match(left_id, right_id, left_row, right_row, overlap_len), ...]
# res.left_stats / res.right_stats: 参与、null、NaN、空区间行数
# res.naive_pairs, res.examined_pairs: 笛卡尔 vs 实际比较量
```
