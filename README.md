# 二分图匹配（禁止边 / 必选边 / 偏好成本）

Python 3.11，仅标准库，不调用外部求解器。目标按字典序：**先最大匹配数，再最小总成本**。

## 状态语义（彼此分开）

| 状态 | 含义 |
|---|---|
| `OPTIMAL` | 达到满匹配 `min(左, 右)` 且成本最小 |
| `FEASIBLE` | 不存在满匹配，返回最大部分匹配（最小成本）与缺额 |
| `BUDGET_UNDECIDED` | 给了预算且最优成本超预算；预算内是否存在（更小的）可行匹配未作判定 |
| `INFEASIBLE` | 必选边冲突，求解前预先诊断，绝不悄悄丢弃任何一条必选边 |

## 用法

```python
from bipartite_matching import solve

res = solve(
    ["甲", "乙", "丙"], ["岗位A", "岗位B", "岗位C"],
    [("甲", "岗位A", 5), ("甲", "岗位B", 2),
     ("乙", "岗位A", 3), ("乙", "岗位C", 4),
     ("丙", "岗位B", 1), ("丙", "岗位C", 6)],
    forbidden=[("丙", "岗位A")],
    mandatory=[("乙", "岗位C")],
    budget=20,
)
print(res.status, res.matching, res.cost, res.deficit)
```

- 成本接受 `int` / `Fraction` / `"p/q"` 字符串，内部一律用 `Fraction` 精确计算。
- 必选边冲突（共享人员、共享资源、与禁止边重叠、端点未知）在 `diagnostics` 中逐条列出。
- 必选边未在 `edges` 中列出时按成本 0 补入；重复边取最小成本。

## 运行

```bash
python3.11 bipartite_matching.py          # 演示
python3.11 -m unittest test_bipartite_matching -v   # 测试
```

## 实现

最小费用最大流（单位容量 + SPFA 逐条最短路增广），天然保证字典序
（最大基数，最小成本）；必选边两两不冲突时直接固定并从残图移除端点。
