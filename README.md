# Bounded Petri Net Verifier

Python 3.11+（仅标准库）实现的有界容量 Petri 网执行、可达性搜索与不变式验证工具包。

## 特性

- **有界容量语义**：所有库所必须声明有限正整数容量；点火后超容量的转移被禁用。
  状态空间因此有限，可达性为**精确**结果——绝不把有界网近似成无界网的
  coverability 结论（`SearchResult.semantics == "exact-bounded-reachability"`）。
- **三类弧分别定义**：输入弧（消耗，重数需足量 token）、输出弧（生产）、
  读弧（需足量 token 但不消耗）、抑制弧（token 数低于阈值才启用）。
- **原子点火与自环**：消耗与生产一步完成；自环按净效应一起判定容量。
- **可达搜索**：BFS 输出标记路径（`trace_to`）；`max_states` 截断时结果保持
  `PENDING`，不下结论（验证对象仅限给定有限状态模型）。
- **不变式检查**：`check_invariant` 对全部可达标记求值；违反时给出**可重放**
  反例（转移序列 + 真实库所 ID 标记，`Counterexample.replay` 验证）。
- **不变式证书**：权重向量 `y` 满足 `y·C = 0`（仅计消耗/生产流量；读弧、抑制弧
  不移动 token），注册时全量校验，之后 `y·M = y·M0` 可逐标记 O(places) 检查。
- **增量重验**：`modify_transition` 修改转移后，`CertificateCache.revalidate()`
  只重算变更转移的流量列；证书可能保持 `VALID`、变为 `INVALID`，修复后可恢复。
- **对称归约**：精确网自同构（保持容量与四类弧模式多重集的库所置换）下取轨道
  字典序最小为典范形；归约保持启用条件（`preserves_enabling` 可验证）。
  每个典范状态保存真实可达代表标记，反例直接还原到真实库所 ID。
  搜索空间超限时安全回退为恒等（无归约）。
- **独立对照**：`cross_check` 用无归约搜索作基准，验证归约搜索的代表均可达、
  且覆盖全部可达典范类（适用于小网）。
- **终态校验**：`verify_terminal_token_totals` 对每个死锁标记验证证书不变量
  （全 1 权重即 token 总量守恒）。

## 运行

```bash
python3 examples/demo.py     # 运行样例
python3 -m unittest discover -s tests -v   # 运行测试（21 个用例）
```

## 样例输出（节选）

```
==================================================================
1. Bounded producer/consumer: execution, paths, invariant check
==================================================================
semantics        : exact-bounded-reachability (no coverability approximation)
status           : COMPLETE
reachable states : 3
invariant 'buffer <= 1'      : holds=False (COMPLETE)
  counterexample trace   : ['produce', 'produce']
  counterexample marking : {'buffer': 2, 'c_ready': 1, 'p_ready': 1}  (real place IDs)
  replayed marking       : {'buffer': 2, 'c_ready': 1, 'p_ready': 1}  (replay OK)

2. Truncated search stays PENDING (no verdict)
max_states=2 -> status=PENDING, holds=None

4. Invariant certificates + incremental revalidation
certificate 'total': weights={'a': 1, 'b': 1, 'c': 1}, constant=1, status=VALID
after adding read arc to t1 (rev 1):
  revalidate -> {'total': 'VALID'}  (rechecked only: ['t1'])
after t2 produces 2 tokens (rev 2):
  revalidate -> {'total': 'INVALID'}  (violations: ['t2'])
after repairing t2 (rev 3):
  revalidate -> {'total': 'VALID'}

6. Symmetry reduction + independent plain-search cross-check
automorphisms found: 6
plain reachable states   : 8
reduced reachable states : 4 (canonical classes)
cross-check (reduced vs. independent plain search): ok=True - reduced and plain searches agree
enabling preserved under canonicalisation on all states: True
```

## 测试结果

```
$ python3 -m unittest discover -s tests
Ran 21 tests in 0.272s
OK
```

覆盖：输入弧重数足量判定、超容量禁用、自环净效应、读弧不消耗、抑制弧阈值、
有限容量精确可达（非 coverability）、标记路径提取、截断未决、反例重放、
证书注册/拒绝/增量重验（读弧修改保持有效、流量破坏判 INVALID、修复恢复）、
终态 token 总量守恒、对称归约启用保持、无归约对照、归约反例真实库所 ID 还原、
恒等回退精确性。

## 代码结构

- `petri/model.py` — 网模型：容量、四类弧、启用/点火语义、增量修改
- `petri/engine.py` — 可达搜索、不变式检查、反例重放、无归约对照
- `petri/certificates.py` — 不变式证书缓存与增量重验、终态总量校验
- `petri/symmetry.py` — 自同构计算、典范形、启用保持验证
- `tests/test_petri.py` — unittest 测试套件
- `examples/demo.py` — 端到端运行样例
