# minirel — 可去关联化子查询的小型关系执行器

Python 3.11 标准库实现。表是本地合成多重集合（行的列表），字段为整数或
`None`（null）。同一查询子集提供两种执行方式，并在空值与重复行上证明等价：

- **逐外行解释器**（`minirel/interpreter.py`）：每条外行完整扫描内表，
  作为参考语义。
- **优化执行器**（`minirel/optimized.py`）：把支持的子查询去关联化为
  半连接（EXISTS）、反连接（NOT EXISTS）或带 TRUE/FALSE/UNKNOWN 标记的
  连接（IN / NOT IN），每条外部物理行独立探测，保留重数。

## 语义

- 行值相等按三值 AND 计算：任一分量确定不等则整行 FALSE（因此
  `(1,2)` vs `(3,null)` 是 FALSE，vs `(1,null)` 是 UNKNOWN）。
- IN 是对所有候选内行相等结果的三值 OR，空候选集为 FALSE；NOT IN 再取 NOT。
- WHERE 只保留 TRUE；投影表达式保留 UNKNOWN（输出为 `None`）。
- 关联条件支持多外部列的等值或 null 安全等值（`<=>`），内表可附加过滤。

## 去关联化结构

`minirel/subindex.py` 为每个子查询建立

```
corr_key -> { null_pattern -> { 非null值元组: 重数 } }
```

IN 探测每外行每分区只需 O(2^k) 次字典查找（k 为比较列数），不再扫描整个
内表。null 模式是键结构的一部分，所以内表任意位置存在 null 不会把所有
外行判成 UNKNOWN。超出内存额度（缓冲行数）时把内存索引溢写为分区文件；
每个分区完整保存其行的 null 模式信息，跨分区结果用三值 OR 合并。

## 错误与清理

取消（`cancel_check`）与磁盘写入失败（可注入 `open_fn`）都会传播异常，
并删除全部溢写分区与部分输出；输出文件先写 `.tmp` 再原子改名，错误不会
留下可被当成完整结果的输出。

## 运行

```
python3.11 demo.py                          # 计划解释 / 执行计数 / 清理验证
python3.11 -m unittest discover -s tests -t .   # 全部测试
```

测试覆盖：域 {null,0,1} 上小表（含重复行与空表）的穷举对照、二列 NOT IN
的空值边界、关联子表为空、分页输入、极低内存、倾斜关联键、磁盘写失败与
取消。
