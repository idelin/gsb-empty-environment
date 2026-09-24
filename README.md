# cse-agg: 多聚合查询公共子表达式去重执行器

纯 Python 3.11 标准库实现, 不依赖任何查询引擎。

## 特性
- 多个聚合查询共享一个执行 DAG: 结构 + 类型相同的中间表达式只计算一次
- 类型语义: `sum(x:int)` 与 `sum(cast(x->float))` 是不同节点, 不会错误合并
- 副作用函数 (`Func(..., volatile=True)`) 禁止复用, 每次出现独立计算
- 表按 `(schema, rows)` 批次输入, schema 携带列类型并做校验
- `None` 表示 null, `float('nan')` 表示 NaN, 二者语义不同
  (count 跳过 null 但计 NaN; sum 跳过 null、NaN 传播)
- 整数精确计算 (任意精度 int, avg 返回 `Fraction`); 浮点用 `math.fsum`
- 聚合 FILTER 条件不同的同一表达式不合并; filter 结果为 null 视为 false
- 输出执行依赖图 (`Engine.graph()`) 与每节点实际计算次数 (`run` 返回的 counts)

## 运行
```sh
python3.11 demo.py            # 运行样例: 打印依赖图、结果、计算次数
python3.11 -m unittest -v     # 运行测试
```

## 文件
- `cse_agg.py` — 表达式 AST、计划器 (CSE)、批式执行器
- `demo.py` — 运行样例 (sum(x) 三处引用只算一次等)
- `test_cse_agg.py` — 15 个 unittest 用例
