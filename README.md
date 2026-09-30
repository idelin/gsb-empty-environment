# muskit — 预算感知的不可满足核提取（纯标准库，无 SAT/SMT 求解器）

对冲突约束集做冗余项删除：每删一项都调用**带预算的内部求解器**验证剩余集合仍冲突，
输出**子集最小核**（subset-minimal core）与完整检查记录；另用**击中集搜索**求
**最小基数核**（minimum-cardinality core）并给出下界证据。变量域有限，回溯求解器
为自实现，不调用任何 SAT/SMT 库。

## 关键语义

- **子集最小 ≠ 最小基数**：子集最小核附带逐项删除见证（删掉该项后剩余集合可满足，
  见证赋值由独立检查器对原约束复核）；最小基数核由击中集搜索求得，下界证据为
  两两不相交的修正集（correction set）打包数。
- **预算耗尽传播为未决**：内部求解返回 UNKNOWN 时，该项保留、整体状态为
  UNDECIDED；UNKNOWN 绝不当作 SAT 或 UNSAT 继续缩核。
- **删任何剩余项后可满足才称子集最小**：仅当所有幸存项都有当前有效的删除见证时
  才报告 SUBSET_MINIMAL。
- **缓存绑定假设集版本**：键为 (问题修订号, 预算, 假设集 frozenset)；约束池变更
  使旧条目失效；UNKNOWN 条目按预算隔离，不会污染更高预算的查询。
- **小实例全子集枚举核验**：枚举全部 2^n 子集作为基准，核验核的类别
  （子集最小 / 最小基数）与恢复一致性（核是提取的不动点、原集合仍冲突）。
- **声称有解必验**：求解器或日志声称 SAT 时，一律用独立检查器对照原约束复核。

## 结构

| 文件 | 职责 |
| --- | --- |
| `muskit/constraints.py` | 有限域变量 / 约束 / 问题池（带修订号） |
| `muskit/solver.py` | 预算回溯求解器，SAT / UNSAT / UNKNOWN |
| `muskit/checker.py` | 独立赋值检查器（与求解器无共享搜索代码） |
| `muskit/cache.py` | 绑定假设集版本的求解缓存 |
| `muskit/core.py` | 删法式子集最小核提取 + 检查记录 + 删除见证 |
| `muskit/hitting.py` | 击中集搜索求最小基数核 + 不相交修正集下界 |
| `muskit/exhaustive.py` | 小实例全子集枚举核验类别与恢复一致性 |
| `muskit/demo.py` / `main.py` | 运行样例 |
| `tests/` | unittest 测试（30 个） |

## 运行

```sh
python3.11 main.py                       # 运行样例
python3.11 -m unittest discover -s tests # 运行测试
```

## 运行样例（python3.11 main.py）

样例问题：x∈{0,1}, y∈{0,1,2}；a: x==0，b: x==1，c/d/e: y≠0/1/2。
{a,b} 是唯一最小基数核（大小 2），{c,d,e} 是大小 3 的子集最小核。

```

====================================================================
Problem
====================================================================
  var x in [0, 1]
  var y in [0, 1, 2]
  a: x == 0
  b: x == 1
  c: y != 0
  d: y != 1
  e: y != 2

====================================================================
1. Deletion-based subset-minimal core (unlimited budget)
====================================================================
  try removing a: solve {b,c,d,e}      -> UNSAT   (5 nodes) => removed
  try removing b: solve {c,d,e}        -> UNSAT   (3 nodes) => removed
  try removing c: solve {d,e}          -> SAT     (1 nodes) => kept-sat
  try removing d: solve {c,e}          -> SAT     (2 nodes) => kept-sat
  try removing e: solve {c,d}          -> SAT     (3 nodes) => kept-sat
  core = ['c', 'd', 'e']  status = SUBSET_MINIMAL
  deletion witness for c: core\{c} satisfied by {'y': 0}
  deletion witness for d: core\{d} satisfied by {'y': 1}
  deletion witness for e: core\{e} satisfied by {'y': 2}

====================================================================
2. Minimum-cardinality core via hitting-set search
====================================================================
  SAT seed [] -> grew to ['a', 'c', 'd'], correction set ['b', 'e']
  SAT seed ['b'] -> grew to ['b', 'c', 'd'], correction set ['a', 'e']
  SAT seed ['e'] -> grew to ['a', 'c', 'e'], correction set ['b', 'd']
  UNSAT seed ['a', 'b'] -> core ['a', 'b'] (size 2)
  lower bound 2 meets best core size 2: proven optimal
  best core = ['a', 'b']  status = OPTIMAL
  lower bound (disjoint correction sets) = 2, upper bound = 2, proven = True
  correction sets found: [['b', 'e'], ['a', 'e'], ['b', 'd']]
  => subset-minimal core ['c', 'd', 'e'] (size 3) is NOT minimum-cardinality; optimum is ['a', 'b'] (size 2)

====================================================================
3. Budget exhaustion propagates as UNDECIDED
====================================================================
  try removing a: solve {b,c,d,e}      -> UNKNOWN (3 nodes) => kept-undecided
  try removing b: solve {a,c,d,e}      -> UNKNOWN (3 nodes) => kept-undecided
  try removing c: solve {a,b,d,e}      -> UNSAT   (2 nodes) => removed
  try removing d: solve {a,b,e}        -> UNSAT   (2 nodes) => removed
  try removing e: solve {a,b}          -> UNSAT   (2 nodes) => removed
  budget_per_check=3 -> status = UNDECIDED (core kept as ['a', 'b'])
  note: kept-undecided items block the SUBSET_MINIMAL claim;
        UNKNOWN was not treated as UNSAT (no item removed on UNKNOWN)
  SAT seed [] -> grew to ['a', 'c', 'e'], correction set ['b', 'd']
  SAT seed ['b'] -> grew to ['b', 'd', 'e'], correction set ['a', 'c']
  candidate budget 2 exhausted
  hitting search, candidate_budget=2 -> status = UNDECIDED, proven = False, best = None

====================================================================
4. Cache bound to assumption-set version
====================================================================
  hits/misses before: (8, 34), after two repeated solves: hits=10, misses=34
  constraint pool mutated (revision -> 6); same ids solved again: misses 34 -> 35

====================================================================
5. Exhaustive verification (all 2^5 subsets)
====================================================================
  subsets: 11 UNSAT, 21 SAT, 0 unknown
  subset-minimal UNSAT sets: [['a', 'b'], ['c', 'd', 'e']]
  minimum-cardinality cores: [['a', 'b']]
  [OK] ['c', 'd', 'e'] subset-minimal: core is UNSAT and deleting any single item breaks the conflict
  [FAIL] ['c', 'd', 'e'] minimum-cardinality: core size 3 != exhaustive minimum 2
  [OK] ['a', 'b'] minimum-cardinality: core size matches exhaustive minimum 2
  [OK] recovery consistency: core is a fixed point of extraction and the original set is still UNSAT
```

## 测试结果

```
----------------------------------------------------------------------
Ran 30 tests in 0.002s

OK
```
