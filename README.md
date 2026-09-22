# plan-diff: minimal counterexamples for query-plan pairs

在有限小数据域上枚举合成关系表，寻找使两个查询计划的**结果多重集合**
不等的最小反例。纯 Python 3.11 标准库实现，不调用任何 SQL 引擎。

## 语义约定

- **NULL**：单例哨兵 `NULL`。任何涉及 NULL 的比较结果为 `UNKNOWN`
  （SQL 三值逻辑）；`SELECT` 只保留谓词为 `TRUE` 的行（UNKNOWN 被丢弃）。
- **多重集合（bag）**：关系是行的多重集合，投影保留重复行；
  比较结果时忽略行顺序，但重复行数量不同即判不等。
- **连接**：等值连接，NULL 永不匹配；支持 inner / left / right / full
  外连接，未匹配侧以 NULL 填充。
- **最小性**：按数据库总行数升序枚举，首个反例即在总行数意义下最小。
- **无证明**：预算内未发现反例 **不等于** 等价证明，输出会明确标注。

## 文件

- `relalg.py` — NULL/三值逻辑、关系与计划算子（select/project/join）、求值器
- `counterexample.py` — 数据库枚举器与反例搜索（含 `max_rows`/`max_dbs` 预算）
- `main.py` — 四个演示（3VL 陷阱、内连接 vs 左外连接、重复计数、等价对）
- `test_relalg.py` / `test_counterexample.py` — unittest 测试

## 运行

```sh
python3.11 main.py                 # 默认 domain={0,1,NULL}, 每关系 <=2 行
python3.11 main.py --max-rows 3 --domain 0,1,NULL
python3.11 -m unittest discover -v # 27 个测试
```

## 作为库使用

```python
from relalg import Scan, Select, Project, Join, col, lit, eq
from counterexample import find_counterexample

plan_a = Select(eq(col("A"), lit(1)), Scan("R"))
plan_b = Scan("R")
result = find_counterexample(plan_a, plan_b, {"R": ("A",)}, max_rows=2)
if result.found:
    print(result.db, result.output_a, result.output_b)
else:
    print("预算内无反例 —— 并非等价证明")
```
