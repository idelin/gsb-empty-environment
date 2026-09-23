# Python 表分区裁剪与跳过证明

本项目只使用 Python 3.11 标准库和 `unittest`，不调用 SQL 引擎。代码在内存中构造合成关系表，按整数范围和有限枚举进行叶子分区；查询阶段先分析布尔谓词的可能三值状态 `TRUE / FALSE / UNKNOWN`，只扫描状态可能为 `TRUE` 的分区。

## 文件

- `partition_pruning.py`：分区模型、谓词 AST、SQL 三值逻辑、抽象域分析、跳过证明、本地关系表扫描。
- `demo.py`：整数边界、OR 并集、枚举 `IN/NOT`、NULL、未绑定参数和绑定参数的运行样例。
- `test_partition_pruning.py`：21 个 `unittest` 测试。
- `sample_output.txt`：`python3.11 demo.py` 的已保存输出。
- `test_results.txt`：`python3.11 -m unittest -v` 的已保存输出。

## 语义

- 整数范围把开闭边界规范化为整数闭区间，例如 `[0,10)` 变成 `[0,9]`，校验时拒绝空洞和重叠，并要求负无穷到正无穷完整覆盖。
- 每个分区键可以有一个独立 `NULL` 片段；多个分区键的普通片段和 NULL 片段做笛卡尔叶子分区。
- 普通比较遇到 `NULL` 为 `UNKNOWN`；`IS NULL` 单独处理。NULL 分区不会被 `x = :p` 之类的普通谓词选中，即使参数未绑定。
- `IN` / `NOT IN` 按 SQL 三值逻辑处理列表中的 `NULL`；`NOT` 交换 `TRUE/FALSE` 并保留 `UNKNOWN`。
- OR 的两个分支只要任一分支可能为真就保留分区，因此跨范围、跨枚举集合天然取并集。
- 未绑定参数的谓词根节点保守返回 `{TRUE, FALSE, UNKNOWN}` 并保留全部叶子分区；参数绑定后重新分析。
- 关系表是多重集合：不去重，保留插入顺序；投影中重复行会重复出现。
- 非分区键谓词无法证明可跳过时保留分区，再由逐行求值过滤。

跳过证明是递归树。叶子节点记录左右抽象域或集合域以及比较可能值；AND/OR/NOT 节点记录三值真值表推导。根状态不含 `TRUE` 才可以跳过该分区。

## 运行

```sh
python3.11 demo.py
python3.11 -m unittest -v
```

## 已验证结果

样例命令和测试命令均已在 Python 3.11 下运行：

- `python3.11 demo.py`：成功，输出见 `sample_output.txt`。
- `python3.11 -m unittest -v`：21 个测试全部通过，输出见 `test_results.txt`。
