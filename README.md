# transducer — 最低成本有限状态转导 + 前 K 个不同输出枚举

纯 Python 3.11 标准库实现（不使用任何正则/自动机库）。字母表、状态集与
转移结构全部由调用方显式给出；转移消耗一个输入符号（或 epsilon）、产生
一个输出字符串并带有非负成本。

## 模型

```python
from transducer import FST, Transition

fst = FST(
    states={"q0"},
    alphabet={"a", "b"},
    transitions=[
        Transition(1, "q0", "q0", "a", "4", 1),   # edge_id, src, dst, in, out, cost
        Transition(2, "q0", "q0", "a", "a", 2),
        Transition(3, "q0", "q0", "b", "8", 1),
        Transition(5, "q0", "q0", None, "!", 5),  # None = epsilon（不消耗输入）
    ],
    start="q0",
    accepts={"q0"},
)
```

构造期校验：边 ID 唯一、端点状态已知、符号属于字母表、成本为非负实数；
查询时校验输入串符号属于字母表。

## 决胜顺序

候选路径按元组键全序排列：

```
(总成本, 输出长度, 输出字符串, 边 ID 序列)
```

即先比总成本，再比输出长度，再按字典序比输出，最后按边 ID 序列决胜。
先比长度再比字符串是关键：它保证"对两条路径追加同一条边不会改变其相对
顺序"（字典序在等长字符串上才比较，追加共同后缀保持序），从而使最佳优先
弹出（best-first）正确。

## 终止性论证（epsilon 环与零成本环）

乘积图 `(state, pos)` 有限（|Q| × (n+1) 个节点）。若一条路径以相同的输出
长度重新访问同一 `(pos, state)`，则中间段是一个**静默环**（不消耗输入、
不产生输出）；删除该段得到输出相同、成本不增的合法路径，因此带静默环的
路径既不可能贡献新的不同输出，也不可能成为所需的最小值。枚举器据此剪枝
（`search.py` 中 `mark in cur.marks`）。

剪枝后，路径上每条边要么消耗输入（至多 n 次），要么严格增长输出。因此在
任意键上界之下候选路径只有有限条（同一输入位置内，输出长度 ≤ L 时至多访
问 |Q|·(L+1) 个节点）。于是：

- 最小值必然存在——零成本静默环是无限严格下降键序列的唯一来源，已被排除
  （否则 `(1,2,3) > (1,2,1,2,3) > ...` 这样的链使最小字典序解不存在）；
- 第 k 个不同输出在有限次弹出内到达；
- 若静默环自由路径总数有限，优先队列自然排空，搜索自行停止。

零成本但**有输出**的环不剪枝：它产生真正不同的输出（`""`, `"a"`, `"aa"`,
…），每个都按键序弹出，任意有限的 k 仍可到达。

## API

- `min_cost_transduction(fst, text, budget=...)` → `Transduction`
  （`ok/cost/output/steps/pops/reason`；无接受路径时 `ok=False`）。
- `k_best_outputs(fst, text, k, budget=..., frontier_size=...)` → `KBestResult`
  前 k 个**不同**输出（多路径同输出只计一次，证据取最优路径）；预算耗尽时
  `budget_exhausted=True` 且 `frontier` 保留待处理的最好键。
- `brute_force_best(fst, text, max_steps)` → 小图穷举对照器（独立 DFS 实现，
  与枚举器不共享代码），返回排序后的 `(key, output, steps)` 列表。
- `FST.find_zero_cost_silent_cycle()` → 诊断零成本静默环（返回闭合边 ID 序列
  或 `None`）。

证据（`steps`）逐边记录：边 ID、起止状态、消耗的输入符号（epsilon 为 `""`）、
产生的输出、成本与输入位置变化，可完整重放校验（测试中 `replay()` 即如此）。

## 运行样例

```
$ python3 examples/run_samples.py
== sample 1: weighted 'leet' encoder, input 'ab' ==
  min: cost=2 output='48' (pops=4)
    edge 1: q0 -> q0  in 'a'  out '4'  cost 1  (pos 0->1)
    edge 3: q0 -> q0  in 'b'  out '8'  cost 1  (pos 1->2)
  top-6 distinct outputs:
    #1 cost=2 output='48' edges=[1, 3]
    #2 cost=3 output='a8' edges=[2, 3]
    #3 cost=4 output='4b' edges=[1, 4]
    #4 cost=5 output='ab' edges=[2, 4]
    #5 cost=7 output='!48' edges=[5, 1, 3]
    #6 cost=7 output='4!8' edges=[1, 5, 3]

== sample 2: zero-cost silent cycle + zero-cost output loop ==
  zero-cost silent cycle diagnostic: [1, 2]
  min: cost=0 output='xxx' (pops=6)
  (the silent cycle is normalised away; the search terminates)

== sample 3: duplicate outputs counted once; budget keeps frontier ==
  diamond: distinct outputs = 1 (paths agree on 'x')
    #1 cost=1 output='x' edges=[1, 3]
  zero-cost output loop, k=10 budget=5: got 5, budget_exhausted=True
  frontier (pending keys): [(0, 5, 'aaaaa', (1, 1, 1, 1, 1))]

== sample 4: no accepting path -> failure ==
  input 'ab': ok=False reason='no accepting path consumes the whole input'
```

## 反例（可独立执行）

```
$ python3 examples/counterexamples.py
...
all counterexamples behaved as documented
```

涵盖：零成本静默环使朴素最小值不存在、零成本输出环产生无穷多不同输出、
朴素 k 最短路重复计数同输出、静默环使朴素枚举不终止、无接受路径返回失败。

## 测试结果

```
$ python3 -m unittest discover -s tests -v
...
Ran 26 tests in 0.169s

OK
```

包含：模型校验、全部决胜规则、epsilon/零成本环/空输入/失败路径、证据重放、
K-best 去重与预算前沿，以及 200 组随机小图（epsilon 子图无环，保证穷举界
完备）与独立穷举对照器的全序一致性交叉验证。

## 目录

```
transducer/
  model.py    # Transition / FST：显式字母表与转移、校验、环诊断
  search.py   # 最佳优先枚举引擎、min_cost_transduction、k_best_outputs
  brute.py    # 穷举对照器（独立实现）
examples/
  run_samples.py      # 运行样例
  counterexamples.py  # 可独立执行的反例
tests/
  test_transducer.py  # unittest 套件
```
