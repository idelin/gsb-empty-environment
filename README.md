# 二分图匹配：禁止边 / 必选边 / 偏好成本

在二分图（人 ↔ 资源）上求匹配，规则如下：

- **一个资源不能匹配两个人，一个人也不能匹配两个资源**（匹配定义保证）。
- **禁止边**：任何解都不得使用。
- **必选边**：必须出现在解中；必选边之间若共享人或资源、或本身非法，
  在求解前一次性**预诊断**并拒绝，绝不静默丢弃其中任何一条。
- **偏好成本**：先求**最大基数匹配**，再在所有最大匹配中取**总成本最小**者。
- **预算三态分开**：给定预算且最优成本不超预算 → `within_budget`（可行）；
  超过 → `exceeds_budget`；不给预算 → `undecided`（不判定）。
- **无满匹配时**仍返回**最大部分匹配**，并给出缺额
  `deficit = min(|人|, |资源|) - 匹配数`，以及两侧未匹配顶点。
- 成本为合成**整数或有理数**，统一用 `fractions.Fraction` 精确运算，
  也接受负偏好成本；**不调用任何求解器**，仅用 Python 3.11 标准库。

## 文件

| 文件 | 说明 |
| --- | --- |
| `matching.py` | 核心模块：必选边诊断 + 最小费用最大流 |
| `demo.py` | 运行样例（最优/预算、冲突诊断、部分匹配缺额） |
| `test_matching.py` | 25 个 unittest 用例 |

## 算法

1. 校验边的顶点，剔除禁止边，重复边取最小费用。
2. 必选边预诊断：`shared_person` / `shared_resource` / `forbidden` /
   `unknown_edge` / `unknown_vertex`，全部收集后抛 `MandatoryConflictError`。
3. 固定必选边，删除其占用的顶点，费用计入总额。
4. 残图建模为单位容量网络 `source → 人 → 资源 → sink`，用
   **逐次最短路最小费用最大流**（每轮 Bellman-Ford，支持残量负边）求解：
   增广路耗尽时基数最大，且每一步最短路保证该流量下费用最小。
5. 回读匹配，合并必选边，计算缺额、未匹配顶点和预算状态。

## 用法

```python
from fractions import Fraction
from matching import solve, MandatoryConflictError

result = solve(
    people=["alice", "bob"],
    resources=["x", "y"],
    edges=[("alice", "x", 2), ("alice", "y", 5),
           ("bob", "x", 6), ("bob", "y", 1)],
    mandatory={("bob", "y")},   # 必选边
    forbidden=set(),            # 也可传 {("alice", "x")} 等禁止边
    budget=10,
)
print(result.matching)       # {'alice': 'x', 'bob': 'y'}
print(result.total_cost)     # 3 (Fraction)
print(result.deficit)        # 0 表示满匹配
print(result.budget_status)  # within_budget / exceeds_budget / undecided

# 必选边冲突时捕获诊断：
try:
    solve(..., mandatory={("alice", "x"), ("bob", "x")})
except MandatoryConflictError as exc:
    for d in exc.diagnostics:  # 所有冲突一次性报出
        print(d.kind, d.message)
```

## 运行样例

```
$ python3.11 demo.py
```

输出：

```
================================================================
样例 1：最大匹配 + 最小成本（预算=8）
----------------------------------------------------------------
匹配 (4 对): bob->y, alice->z, carol->w, dave->x
必选边        : {'bob': 'y'}
总成本        : 6
满匹配规模    : 4，缺额: 0（满匹配）
未匹配的人    : 无
未匹配的资源  : 无
预算          : 8 -> 可行（最优成本 <= 预算）

================================================================
样例 1b：同一问题，预算=4（低于最优成本）
----------------------------------------------------------------
匹配 (4 对): bob->y, alice->z, carol->w, dave->x
必选边        : {'bob': 'y'}
总成本        : 6
满匹配规模    : 4，缺额: 0（满匹配）
未匹配的人    : 无
未匹配的资源  : 无
预算          : 4 -> 不可行（最优成本 > 预算）

================================================================
样例 1c：同一问题，不给预算
----------------------------------------------------------------
匹配 (4 对): bob->y, alice->z, carol->w, dave->x
必选边        : {'bob': 'y'}
总成本        : 6
满匹配规模    : 4，缺额: 0（满匹配）
未匹配的人    : 无
未匹配的资源  : 无
预算          : None -> 未决（未提供预算，不作判定）

================================================================
样例 2：必选边冲突预诊断（不静默丢弃任何一条）
----------------------------------------------------------------
[shared_resource] mandatory edges ('bob', 'x') and ('alice', 'x') share resource 'x'; a resource cannot be matched twice
[shared_person] mandatory edges ('alice', 'x') and ('alice', 'z') share person 'alice'; a person cannot be matched twice

----------------------------------------------------------------
[forbidden] mandatory edge ('alice', 'x') is also forbidden

================================================================
样例 3：4 人 / 2 资源（成本为有理数，预算=1）
----------------------------------------------------------------
匹配 (2 对): p2->r1, p3->r2
必选边        : 无
总成本        : 5/6
满匹配规模    : 2，缺额: 0（满匹配）
未匹配的人    : ['p1', 'p4']
未匹配的资源  : 无
预算          : 1 -> 可行（最优成本 <= 预算）

================================================================
样例 4：3 人 / 3 资源但结构上无法满匹配
----------------------------------------------------------------
匹配 (2 对): a->m, b->n
必选边        : 无
总成本        : 4
满匹配规模    : 3，缺额: 1（部分匹配）
未匹配的人    : ['c']
未匹配的资源  : ['q']
预算          : None -> 未决（未提供预算，不作判定）

```

## 测试结果

```
$ python3.11 -m unittest -v
test_all_conflicts_reported_at_once (test_matching.MatchingTests.test_all_conflicts_reported_at_once) ... ok
test_budget_exceeded (test_matching.MatchingTests.test_budget_exceeded) ... ok
test_budget_undecided_when_absent (test_matching.MatchingTests.test_budget_undecided_when_absent) ... ok
test_budget_within (test_matching.MatchingTests.test_budget_within) ... ok
test_cheaper_non_perfect_is_not_chosen (test_matching.MatchingTests.test_cheaper_non_perfect_is_not_chosen) ... ok
test_duplicate_edge_takes_minimum_cost (test_matching.MatchingTests.test_duplicate_edge_takes_minimum_cost) ... ok
test_empty_graph (test_matching.MatchingTests.test_empty_graph) ... ok
test_forbidden_edge_never_used (test_matching.MatchingTests.test_forbidden_edge_never_used) ... ok
test_mandatory_conflict_shared_person_diagnosed (test_matching.MatchingTests.test_mandatory_conflict_shared_person_diagnosed) ... ok
test_mandatory_conflict_shared_resource_diagnosed (test_matching.MatchingTests.test_mandatory_conflict_shared_resource_diagnosed) ... ok
test_mandatory_edge_blocks_cheaper_global_assignment (test_matching.MatchingTests.test_mandatory_edge_blocks_cheaper_global_assignment) ... ok
test_mandatory_edge_forced_even_if_expensive (test_matching.MatchingTests.test_mandatory_edge_forced_even_if_expensive) ... ok
test_mandatory_forbidden_edge (test_matching.MatchingTests.test_mandatory_forbidden_edge) ... ok
test_mandatory_unknown_edge (test_matching.MatchingTests.test_mandatory_unknown_edge) ... ok
test_mandatory_unknown_vertex (test_matching.MatchingTests.test_mandatory_unknown_vertex) ... ok
test_minimum_cost_among_maximum_matchings (test_matching.MatchingTests.test_minimum_cost_among_maximum_matchings) ... ok
test_more_people_than_resources (test_matching.MatchingTests.test_more_people_than_resources) ... ok
test_negative_preference_costs (test_matching.MatchingTests.test_negative_preference_costs) ... ok
test_partial_matching_with_deficit (test_matching.MatchingTests.test_partial_matching_with_deficit) ... ok
test_rational_budget_comparison_exact (test_matching.MatchingTests.test_rational_budget_comparison_exact) ... ok
test_rational_cost_from_string (test_matching.MatchingTests.test_rational_cost_from_string) ... ok
test_rational_costs_exact (test_matching.MatchingTests.test_rational_costs_exact) ... ok
test_resource_never_matched_twice (test_matching.MatchingTests.test_resource_never_matched_twice) ... ok
test_simple_perfect_matching (test_matching.MatchingTests.test_simple_perfect_matching) ... ok
test_unknown_vertex_in_edge_rejected (test_matching.MatchingTests.test_unknown_vertex_in_edge_rejected) ... ok

----------------------------------------------------------------------
Ran 25 tests in 0.004s

OK
```

覆盖点：最大基数优先于成本、最大匹配中取最小成本、禁止边永不出现、
必选边强制（即便更贵、即便降低基数）、共享人/共享资源冲突一次性诊断、
必选边为禁止边/不存在边/未知顶点、有理数精确比较、负成本、部分匹配缺额、
人多于资源、空图、预算三态、重复边取最小费用。
