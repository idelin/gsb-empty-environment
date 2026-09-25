# eqjoin — 等值连接（内 / 左 / 全外）

纯标准库（Python 3.11+）实现的等值连接，不依赖任何现成查询引擎。
表以**带列类型的批次**（`RecordBatch`）输入，支持小内存下的**哈希分区溢写**，
并提供**可定义的输出次序**。

## 语义

- **连接类型**：`how="inner" | "left" | "full"`。
- **重复键**：按笛卡尔重数展开 —— 左 2 条、右 3 条同键行输出 6 条。
- **NULL**：单例哨兵 `NULL`，默认不参与等值匹配；左/全外连接中按外连接
  语义保留（补另一侧 NULL）。`NULL` 与 `NaN` 不同：`NaN` 是普通 FLOAT 值
  （IEEE 语义，NaN ≠ NaN，故 NaN 键也互不匹配），输出中原样保留。
- **整数精确**：`INT` 列为 Python 任意精度整数，拒绝 bool/float 混入；
  连接键两侧列类型必须完全一致（INT 不会与 FLOAT 的 1.0 误匹配）。

## 小内存执行（grace hash join）

1. 两侧按连接键的稳定哈希（blake2b，跨进程确定）流入 `num_partitions`
   个分区；分区缓冲超过阈值即溢写临时文件（pickle 序列化）。
   含 NULL/NaN 的键进入独立的"不可匹配"分区。
2. 逐对分区连接：右（build）侧能放进 `max_rows_in_memory` 就建哈希表、
   流式扫左侧；否则**递归细分区**。
3. **防无限递归**：若细分区毫无进展（如所有键相同，全部落回同一个子
   分区）或超过 `max_partition_depth`，对该分区回退**块嵌套循环连接**
   （左側按预算分块、右侧从磁盘流式重扫），保证终止。

## 输出次序（`order` 参数）

- `"left"`（默认）：匹配行/左保留行按左输入序（同行多匹配按右输入序）；
  全外连接的未匹配右行按右输入序附后。需对输出做一次排序缓冲。
- `"partition"`：流式友好的确定序 —— 分区 0..N-1，再不可匹配分区，
  最后（全外）未匹配右行。

## 用法

```python
from eqjoin import NULL, ColType, RecordBatch, join

left  = [RecordBatch((("k", ColType.INT), ("lv", ColType.TEXT)),
                     [(1, "a"), (1, "b"), (NULL, "c")])]
right = [RecordBatch((("k", ColType.INT), ("rv", ColType.TEXT")),
                     [(1, "x"), (1, "y"), (1, "z"), (NULL, "w")])]

res = join(left, right, left_on="k", right_on="k", how="full",
           num_partitions=4, max_rows_in_memory=1000, order="left")
for row in res:          # 2x3=6 条匹配 + 左右 NULL 键各 1 条
    print(row)
print(res.stats)         # 溢写分区数 / 细分区轮次 / BNL 回退次数
```

## 运行

```bash
python3 examples/demo.py                 # 运行样例
python3 -m unittest discover -s tests -v # 测试（17 个用例）
```

## 文件

- `eqjoin/types.py` — `ColType`、`NULL` 哨兵、类型校验
- `eqjoin/batch.py` — `RecordBatch`（schema + 类型检查的行）
- `eqjoin/spill.py` — 可溢写分区（内存缓冲 + pickle 落盘、可重复读）
- `eqjoin/core.py` — 连接主流程（分区、哈希探测、递归细分、BNL 回退）
- `tests/test_eqjoin.py` — unittest 套件（含朴素嵌套循环参照实现对照）
- `examples/demo.py` — 运行样例

## 说明与取舍

- 输出结果物化在内存中（`order="left"` 本就需要整体排序）；溢写针对的
  是连接的中间状态。全外连接的"已匹配右行"集合也驻留内存。
- 溢写文件用 pickle，仅适用于可信数据。
