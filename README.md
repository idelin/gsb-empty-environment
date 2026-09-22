# relprune —— 反向列裁剪与本地关系执行

纯 Python 3.11 标准库实现，不调用任何 SQL 引擎。从输出列出发，沿计划
（Project / Filter / Join / Sort / Aggregate / Scan）反向推导每个算子的
必需列，保留表达式的隐式依赖与行重数，并输出逐算子的裁剪证据。

## 语义约定

- 行是 `dict[限定列名, 值]`，表是行的 `list`：多重集合，重复行保留；
- 同名列按来源区分：一律使用限定名 `users.id` / `orders.id`；
- NULL 用 `None`：比较/算术传播 UNKNOWN，过滤与连接仅保留谓词为 `True` 的行；
- 排序稳定，升序 NULLS LAST、降序 NULLS FIRST；分组时 NULL 键归入同组；
- 裁剪到零列的扫描仍逐行产生空字典 —— 行重数不变，`COUNT(*)` 不会变空表；
- 排序键即使不在输出中，也保留到排序完成后才裁掉。

## 运行

```bash
python3 run_sample.py            # 三个样例查询：裁剪证据 + 执行结果
python3 -m unittest discover -s tests -v   # 15 个单元测试
```

## 文件

- `exprs.py` —— 表达式 AST：`refs()` 显式暴露列引用（隐式依赖来源），三值逻辑求值
- `plan.py` —— 算子、`prune()` 反向裁剪、`Evidence` 裁剪证据、本地执行
- `data.py` —— 合成关系表（含 NULL 与完全重复行）
- `run_sample.py` —— 样例：连接+过滤+排序、`COUNT(*)` 零列裁剪、分组聚合
- `tests/test_relprune.py` —— unittest 测试套件
