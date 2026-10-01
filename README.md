# minicore：有限域冲突约束集的核分析

纯 Python 3.11 标准库实现（不调用任何 SAT/SMT 库）。从冲突约束集中删除冗余项，
每次删除都调用**内部求解器**验证剩余集合仍冲突，输出**子集最小核**与完整**检查记录**；
另用**击中集搜索**求**最小基数核**并给出**下界证据**。所有求解在显式节点预算下进行，
预算耗尽传播为 **UNKNOWN（未决）**，绝不当作 SAT 或 UNSAT 继续缩核。

## 关键概念区分

| 概念 | 含义 | 本仓库的实现 |
|---|---|---|
| 子集最小核 (subset-minimal / MUS) | 删掉**任何**剩余项后即可满足（每项都有删除见证） | `deletion_based_core`：逐项试删，UNSAT 才删，SAT 保留（见证经独立检查器验证），UNKNOWN 保留并标记未决 |
| 最小基数核 (minimum-cardinality) | 基数最小的冲突子集；子集最小 ≠ 最小基数 | `minimum_cardinality_core`：已证 SAT 子集的补集构成" blocker "，任何核必须击中全部 blocker；最小击中集大小即**下界证据**，候选 UNSAT 时下界闭合 |
| 恢复一致性 (recovery consistency) | 删除核后剩余约束恢复可满足 | `verify_core` 通过全子集枚举独立核验（注意：最小基数核不保证恢复一致性） |

## 模块

- `minicore/solver.py` — 有限域回溯求解器，三值结果 SAT / UNSAT / UNKNOWN（节点预算耗尽）。
- `minicore/checker.py` — 独立检查器：对原始约束重新求值验证 SAT 声称的见证。
- `minicore/cache.py` — `CachedSolver`：缓存键绑定假设集版本 `(version, frozenset(假设集))`，
  `invalidate()` 升版本失效；每个 SAT 结果先经独立检查器验证再入缓存。
- `minicore/core.py` — 删除法子集最小核、击中集法最小基数核；共享总预算；UNKNOWN 传播为未决。
- `minicore/enumeration.py` — 小实例全子集枚举，独立核验核的类别、逐项删除见证与恢复一致性。
- `minicore/demo.py` / `run_demo.py` — 演示实例与端到端运行样例。
- `tests/test_minicore.py` — 26 个 unittest 用例。

## 运行

```bash
python3.11 run_demo.py                  # 端到端演示
python3.11 -m unittest discover -s tests -v   # 测试
```

## 运行样例（节选）

演示实例：`a,b ∈ {0,1}`，约束池 `k1:a==1, k2:b==0, k3:a==0, k4:b==1, k5:a==b`。
其 MUS 为 `{k1,k3}`、`{k2,k4}`（基数 2）与 `{k1,k2,k5}`、`{k3,k4,k5}`（基数 3）。

```
1) Deletion-based subset-minimal core (pool order)
  kind=subset_minimal  core=['k3: a==0', 'k4: b==1', 'k5: a==b']  undecided=False
    #01 [initial     ] tested={k1,k2,k3,k4,k5} -> UNSAT   check_full_set
    #02 [deletion    ] tested={k2,k3,k4,k5}    -> UNSAT   removed  -- item 0 redundant
    #03 [deletion    ] tested={k3,k4,k5}       -> UNSAT   removed  -- item 1 redundant
    #04 [deletion    ] tested={k4,k5}          -> SAT     kept_sat -- item 2 necessary; deletion witness verified
    #05 [deletion    ] tested={k3,k5}          -> SAT     kept_sat -- item 3 necessary; deletion witness verified
    #06 [deletion    ] tested={k3,k4}          -> SAT     kept_sat -- item 4 necessary; deletion witness verified
    #07 [final_verify] tested={k3,k4,k5}       -> UNSAT   verify_core

2) Minimum-cardinality core via hitting-set search (lower bounds)
  kind=minimum_cardinality  core=['k1: a==1', 'k3: a==0']  certified_lower_bound=2
    ... 6 个单例候选均 SAT（各自补集成为 blocker，下界升至 2）...
    #08 [hitting_set ] tested={k1,k3} -> UNSAT  core_found -- lower bound 2 met
  => subset-minimal core has size 3, minimum-cardinality core has size 2

3) Independent verification by exhaustive subset enumeration
  deletion core (2, 3, 4): subset-minimal=True  minimum-cardinality=False (min size = 2)
                           recovery consistent=True
  min-card core (0, 2):    subset-minimal=True  minimum-cardinality=True
                           recovery consistent=False   # 最小基数核不保证恢复一致性

4) Budget exhaustion propagates as UNDECIDED (never SAT/UNSAT)
  kind=undecided  undecided=True  candidate_core_size=5
    #02..#06 [deletion] -> UNKNOWN  kept_unknown -- budget exhausted, cannot conclude
  OK: no item was removed on UNKNOWN; no minimality was claimed.
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests -v
...
Ran 26 tests in 1.2s
OK
```

覆盖：求解器三值语义与预算耗尽、独立检查器（越域/未赋值/违约束）、
缓存命中与版本失效、SAT 声称被独立检查器拒绝时抛 `InternalSolverError`、
删除法核与击中集核在演示实例上的精确结果、子集最小 ≠ 最小基数、
UNKNOWN 不当 UNSAT（不删项）也不当 SAT（不算删除见证）、
击中集下界证据闭合，以及 12+ 个随机小实例上两种算法与全子集枚举的交叉核验
（核类别、逐项删除见证、恢复一致性）。
