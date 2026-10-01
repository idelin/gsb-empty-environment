# porcheck — 基于读写集合的部分序归约模型检查器

纯 Python 3.11 标准库实现（测试用 `unittest`）。对给定的有限状态模型做
深度优先搜索，用**读写集合**识别独立动作，归约等价交错，同时保留死锁与
指定安全性质（不变式），并输出每一步归约选择的证据。

## 设计要点

- **独立性**（`porcheck/independence.py`）：两个不同动作独立，当且仅当任何
  一方写的变量都不在另一方的读/写集合中。写同一变量（写-写）与写-读冲突
  都不独立；读-读共享无害。
- **归约条件**（`porcheck/reducer.py`）：状态 s 上仅当某 enabled 动作 a 满足
  1. 持久性：a 与模型中**所有**其他动作静态独立（保守但可靠）；
  2. 不可见性：a 不写任何不变式读取的变量；
  才选择单例集 {a}，否则全展开。
- **循环保护（cycle proviso）**：若单例后继已在 DFS 栈上，则改为全展开，
  防止某动作在环上被长期忽略（`cycle_ignoring` 示例演示关掉保护会漏报）。
- **死锁与安全性**：enabled 集为空且 `deadlock_is_error` 时报死锁；每个访问
  状态检查全部不变式。持久集归约保留死锁；不可见性 + 循环保护保留不变式
  违例。
- **未决语义**：`max_states` 预算耗尽时返回 `pending`，不下结论。
- **可重放反例**：反例为 `(states, actions)` 轨迹，`replay` 从初始状态逐步
  重放并校验守卫与后继，`validate_counterexample` 确认轨迹末端确实违例。
- **证据**：每个展开状态记录 `ReductionDecision`（enabled/chosen/理由/是否
  触发循环保护），`evidence_summary()` 汇总。
- **对照**：`compare_with_full` 在同一模型上跑归约搜索与全枚举，比较判定
  与状态数。

## 运行

```bash
python3.11 run_examples.py          # 演示：归约 vs 全枚举、反例重放、证据
python3.11 -m unittest discover -v  # 21 个单元测试
```

## 运行样例（节选）

```
== independent_counters(3,4) ==
  POR : verdict=safe   states=   8 transitions=   7 evidence={... 'singleton_reductions': 3, ...}
  FULL: verdict=safe   states=  20 transitions=  31
  verdicts agree: True

== lock_deadlock ==
  POR : verdict=unsafe states=   3 ...   FULL: verdict=unsafe ...
  counterexample (deadlock: no enabled actions):
  s0 = {la=0, lb=0, pc1=0, pc2=0}
  --p1_take_a--> s1 = {la=1, lb=0, pc1=1, pc2=0}
  --p2_take_b--> s2 = {la=1, lb=2, pc1=1, pc2=1}
  replay: OK, counterexample re-executes to the violation

== cycle proviso guard (model: cycle_ignoring) ==
  without proviso: verdict=safe (violation missed!)
  with proviso   : verdict=unsafe (invariant 'y_zero' violated)
  full enum      : verdict=unsafe

== pending when the budget runs out (model: independent_counters(50,50)) ==
  max_states=100 -> verdict=pending (explored 100 states, no conclusive claim)

== reduction evidence for cycle_ignoring (first 4 decisions) ==
  state {x=0, y=0}: enabled=['flip', 'set_y'] chosen=['flip'] ('flip' is enabled, invisible ...)
  state {x=1, y=0}: enabled=['flip', 'set_y'] chosen=['flip', 'set_y'] (cycle proviso: successor
      of 'flip' is on the DFS stack; full expansion to protect ignored actions)
```

## 测试结果

```
Ran 21 tests in 0.003s
OK
```

覆盖：独立性（写-写/写-读冲突不独立、读-读独立）、归约生效与不生效、证据
记录、不变式违例发现与重放、篡改轨迹拒绝、两种死锁保留、循环保护开/关、
预算耗尽保持 pending、9 个小模型上归约与全枚举判定一致。

## 代码结构

- `porcheck/model.py` — 状态、动作（守卫/效果 + 读写声明）、不变式、模型
- `porcheck/independence.py` — 读写集合独立性判定
- `porcheck/reducer.py` — ample 集选择、循环保护、归约证据
- `porcheck/checker.py` — DFS 检查器、轨迹重放、全枚举对照
- `examples/models.py` — 示例模型（独立计数器、共享计数器、不变式违例、
  锁序死锁、环上忽略）
- `run_examples.py` — 演示脚本；`tests/test_por.py` — 单元测试
