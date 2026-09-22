# 安全局部 TopK 与可证明关系重写

这是一个只使用 Python 3.11 标准库实现的本地关系执行器与计划优化器，不调用 SQL 引擎。

## 语义

- 表是带模式的多重集合：重复行保留，比较按整行值计数，不使用主键去重。
- `NULL` 是三值逻辑中的未知值：普通比较返回未知，只有 `IS NULL` 为真。
- 行带 `(partition_id, local_index)` 来源标记。排序键完全相同时，先按分区号、再按分区内原始序号稳定排列。
- `ORDER BY ... OFFSET o LIMIT n` 的每个分区本地候选必须保留前 `o+n` 行；全局归并后再跳过 `o` 行并取 `n` 行。
- 外连接未匹配侧补 `NULL`；未匹配组上 `COUNT(*)=1`、`COUNT(column)=0`、`SUM/AVG/MIN/MAX=NULL`。

## 重写规则与前提

### 局部 TopK + 全局归并

安全前提：

1. 根计划是单表扫描上的排序加有限 `LIMIT`，中间没有过滤、连接或聚合。
2. 每个本地分区按相同排序键稳定排序。
3. 全局前 `offset+limit` 名之外的行不可能进入答案，因此每分区保留 `offset+limit` 行足够。
4. 全局归并使用相同的键与稳定 tie-breaker，然后应用全局 `OFFSET` 与 `LIMIT`。

`OFFSET` 没有 `LIMIT` 时不能选择有限本地前缀，因此保留原计划。

### 外连接过滤谓词

- 左外连接的纯左侧谓词可下推到左侧，因为它不会删除未匹配的左行。
- 左外连接的纯右侧、可结构化证明为 null-rejecting 的谓词可下推，并把左外连接转成内连接。
- `IS NULL`、包含两侧列、或不能证明 null-rejecting 的右侧谓词保留在连接上方。
- 保守策略要求整个顶层合取谓词可处理；存在残余谓词时保留原计划。

### 聚合下推

只对可分配聚合（`COUNT`、`COUNT(*)`、`SUM`、`AVG`、`MIN`、`MAX`）生成 partial/final 两阶段计划。安全前提包括：

1. 源是无额外非等值谓词的左外等值连接。
2. 聚合表达式只引用右输入列。
3. 左外连接的左连接键唯一，并且该键保留在 `GROUP BY` 中。
4. 部分聚合按右连接键分组；最终聚合按原始分组键合并。
5. 非分配聚合（示例中的 `MEDIAN`）、非唯一保留侧键、内连接、全/右外连接等情形保留原计划。

## 运行

```bash
python3.11 demo.py
python3.11 -m unittest discover -s tests -v
```

## 文件

- `relational/relations.py`：`NULL`、行、模式、表和多重集合比较。
- `relational/expressions.py`：三值逻辑表达式与 null-rejection 判定。
- `relational/operators.py`：扫描、过滤、连接、稳定排序、局部 TopK 和归并。
- `relational/aggregates.py` 与 `relational/aggregate_node.py`：聚合状态和 complete/partial/final 执行。
- `relational/planner.py`：带前提记录的保守计划重写器。
- `relational/data.py`：本地合成关系表。
- `tests/test_relational.py`：unittest 测试。
