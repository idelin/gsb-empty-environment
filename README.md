# cardian — 带可核验证书的基数分析器

纯 Python 3.11 标准库实现，不调用数据库或求解器。对有限整数域上的
**精确整数计数统计**，为查询结果行数给出**数学上可证明的上下界**，
并为每个界输出一个达到该界的具体关系实例（见证），附带独立验证器。

## 输入格式（本地 JSON）

```json
{
  "stats": {
    "tables": {
      "T": {
        "row_count": 4,
        "columns": {
          "a": {"domain": [0, 1], "null_count": 0, "histogram": {"0": 2, "1": 2}},
          "b": {"domain": [0, 1], "null_count": 0, "histogram": {"0": 2, "1": 2}}
        },
        "joint_counts": [{"columns": ["a", "b"], "counts": {"0,0": 2, "1,1": 2}}]
      }
    }
  },
  "query": {"select": {"table": "T", "where": {"and": [{"eq": ["a", 0]}, {"eq": ["b", 0]}]}}},
  "config": {"max_tuple_types": 512, "max_nodes": 200000},
  "budget": 100000
}
```

统计语义（全部为**精确**整数计数约束）：

- `row_count`：精确行数；
- `histogram`：该列**完整**直方图，未列出的域值计数为 0；
- `null_count`：该列精确的 NULL 数；
- `joint_counts`（可选）：两列**完整**联合计数，键为 `"va,vb"`，`null` 表示 NULL，
  未列出的值对计数为 0。

查询子集：

- 谓词：`{"eq": [col, v]}`、`{"range": [col, lo, hi]}`（闭区间）、
  `{"and": [...]}`、`{"or": [...]}`、`{"not": p}`；常量可以是 `null`；
- `{"select": {"table": T, "where": p}}`；
- `{"join": {"left": {"table": L, "where": p}, "right": {"table": R, "where": q},
  "on": [lk, rk]}}`（二表等值内连接）。

语义要点：NULL 采用 SQL 三值逻辑，`WHERE` 只保留 **TRUE**；连接中 NULL 键
永不匹配；重复行与连接重数（左行数 × 右行数）完整保留。

## 输出

```json
{
  "consistent": true,
  "exact": true,
  "min": {"lower": 0, "upper": 0, "witness": {...}},
  "max": {"lower": 2, "upper": 2, "witness": {...}},
  "estimate": {"method": "column-independence assumption", "value": 1.0, "is_bound": false},
  "search": {"nodes": 5, "leaves": 3, "pruned_infeasible": 0, "pruned_bound": 0, "budget": 200000}
}
```

- 真实最小值 ∈ `[min.lower, min.upper]`，真实最大值 ∈ `[max.lower, max.upper]`。
  `exact=true` 时区间退化为精确答案；`exact=false`（预算中断）时界限仍然
  **覆盖所有未搜索分支**，但不得当作精确答案。
- `witness` 是达到当前最优可行值的具体多重集关系实例（含重复行），
  可用 `cardian.verify_witness` 独立核验。
- `estimate` 是独立性假设下的**点估计**，明确标注 `is_bound: false`；
  本工具不产出经验置信区间，更不会把它当成数学界限。
- `search` 记录实际探索节点数、叶子数、不可行剪枝数与界限剪枝数。

统计不相容时返回：

```json
{"consistent": false, "conflict": ["T:hist[b=0]", "T:joint[a=0,b=0]", ...]}
```

`conflict` 是删除极小的冲突子集：子集整体不相容，**删除其中任意一条后即相容**
（由 `tests/test_consistency.py` 程序化验证）。

## 方法

1. 每个元组类型一个计数变量；每条统计变成一个线性等式约束；
2. 统计约束传播（界收紧至不动点）+ 分支定界搜索所有满足统计的联合分布；
3. 目标：单表查询为线性目标，连接查询为 `sum_k A_k * B_k`（按连接键分组的
   过滤后计数乘积），松弛界对非负变量取逐键上下界乘积；
4. 预算中断时，已证明界 = 当前可行最优 与  frontier 全部节点松弛界 的合并，
   因此覆盖未探索分支；当前可行见证始终保留；
5. 搜索状态（frontier + 现任最优 + 计数器）可序列化为 JSON 并恢复；
   状态带有统计+查询的指纹，**统计被修改后旧状态/旧证明会被拒绝或重新验证**，
   绝不沿用失效证明（见 `cardian.analyze.Analyzer`）。

## 使用

```bash
python3.11 -m cardian examples/correlation.json            # 完整分析
python3.11 -m cardian examples/resumable.json --budget 5 --save-state s.json
python3.11 -m cardian examples/resumable.json --state s.json   # 恢复搜索
python3.11 examples/run_examples.py                        # 运行全部样例并核验见证
python3.11 -m unittest discover -s tests -v                # 测试套件
```

Python API：`cardian.analyze(stats, query, config=None, budget=None, state=None)`、
`cardian.Analyzer`（带失效重验证的缓存外观）、`cardian.verify_witness(...)`。

## 目录

- `cardian/model.py` — 统计/查询数据模型、SQL 三值逻辑、查询计数
- `cardian/propagate.py` — 整数计数约束、传播、可行性检查、删除极小冲突子集（IIS）
- `cardian/search.py` — 分支定界、已证明界、可恢复状态、节点/剪枝计数
- `cardian/analyze.py` — 编排：建约束、相容性检查、搜索、见证、`Analyzer`
- `cardian/estimate.py` — 独立性点估计（明确标注非界限）
- `cardian/witness.py` — 见证验证器（重算全部统计与查询行数）
- `reference/enumerator.py` — **独立**参考枚举器，不调用生产搜索器，
  用于交叉核验（`tests/test_fuzz.py` 做随机化对拍）
- `examples/` — 相反相关性同边缘、重复键连接、互斥谓词、全 NULL 列、
  不相容联合统计、可恢复搜索等样例
- `tests/` — 28 个 unittest：界限正确性、IIS 性质、预算中断不声称精确、
  恢复前后最优界限一致、统计修改后见证重验证、见证验证器抗篡改、随机对拍
