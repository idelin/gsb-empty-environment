# Temporal Partial-Order Engine（闭区间必然先后偏序）

Python 3.11，仅标准库（`fractions`、`dataclasses`、`itertools`、`unittest`）。

事件的时间是**闭区间** `[lo, hi]` 而非点。系统联合区间约束与显式因果边，
推导**必然先后偏序**（`a << b` 当且仅当所有可行赋时都满足 `t(a) < t(b)`），
输出确定顺序层、不可比较组、紧凑表示（传递约简 / Hasse 边）、矛盾环及其
原始约束证据、以及一个可行时间见证。**绝不按区间中点伪造全序**，也**不把
不可比较关系当成传递等价类**（不可比较组是真正的反链，而非等价类）。

## 模型与算法

- 所有时间用 `Fraction` 精确表示，无浮点误差；并区分三类概念：
  `WallClockTime`（墙钟读数）、`TimePoint`（绝对时刻）、`Duration`（持续时长）。
  墙钟读数之间不能直接相减，必须经 `OffsetTable`（合成偏移转换表）换算。
- 约束编译为简单时序网络（STN），权重在字典序群 `Q × Z` 上：`(r, k)` 表示
  `r + kε`（ε 为无穷小正量），从而严格小于可被精确推理：
  - `t(v) ≥ lo` → 边 `v → 0`，权 `(-lo, 0)`；`t(v) ≤ hi` → 边 `0 → v`，权 `(hi, 0)`
  - 因果边 `c << e` → 边 `e → c`，权 `(0, -1)`（即 `t(c) - t(e) ≤ -ε`）
- 对该图做 Floyd–Warshall 最短路：`a << b` 必然成立 ⟺ `d(b, a) < 0`；
  负环 ⟺ 矛盾，环上每条边回溯到原始约束 id 作为证据。
- 每条推导出的先后关系都携带**支持集**（原始约束 id）。`retract(id)` 撤回
  约束后重新推导，失去支持的关系自动消失，其余保留。
- 可行时间见证：参考点最短路给出 `(r, k)` 形式解，再取满足全部约束的
  最大 ε 实例化为精确 `Fraction`。
- 小整数域枚举核验：枚举所有满足闭区间与严格因果边的整数赋时，验证
  “所有可行赋时中都成立”的先后关系与符号推导完全一致。

## 语义示例

- `A=[0,2]`、`B=[3,4]`：`A << B` 必然成立（证据 `interval:A, interval:B`）。
- `A=[0,2]` 与 `C=[1,3]`：不可比较（即使中点 1 < 2 也不排序）。
- 端点相接的闭区间（如 `[0,2]` 与 `[2,4]`）不可比较——可能同时取到端点 2。
- 输入顺序不影响偏序边（测试对 24 种输入排列断言同一紧凑表示）。

## 运行

```bash
python3.11 demo.py                          # 运行样例
python3.11 -m unittest discover -s tests    # 测试
```

## 文件

- `temporal/clocks.py` — 墙钟时间 / 时刻 / 时长三类精确类型 + 合成偏移转换表
- `temporal/network.py` — STN 推导引擎：偏序、约简、分层、矛盾环、见证、撤回
- `temporal/enumeration.py` — 小整数域枚举赋时核验
- `tests/` — 39 个 unittest 用例
- `demo.py` — 运行样例

## 运行样例（节选）

```
== intervals only ==
必然先后关系 necessary-before (含原始约束证据):
  A << B   evidence: ['interval:A', 'interval:B']
紧凑表示 transitive reduction (Hasse edges):
  A << B
确定顺序层 layers (同层两两不可比较):
  L0: ['A', 'C']
  L1: ['B']
可行时间见证 feasible witness (exact Fractions):
  A -> 2
  B -> 4
  C -> 3
  A 必然先于 B? True
  A 与 C 不可比较? True  (中点 1<2 也不排序)
  是否全序? False

4. 矛盾环检测与原始约束证据
  consistent? False
  contradiction cycle [A -> B -> epoch0 -> A] weight=(Fraction(-1, 1), -1) evidence=['causal:B->A', 'interval:A', 'interval:B']

5. 撤回约束: 失去支持的先后关系被删除, 其余保留
  撤回前: [('A', 'B'), ('A', 'C'), ('B', 'C')]
  撤回 interval:B 后: [('A', 'C')]
  A<<C 仍成立, 证据: ['interval:A', 'interval:C']

6. 输入顺序不影响偏序边
  24 种输入排列产生的不同紧凑表示数量: 1

7. 小整数域枚举赋时核验
  [intervals A,B,C] 18 feasible assignments over (0, 1, 2, 3, 4); derived=[('A', 'B')] enumerated=[('A', 'B')] -> AGREE
  [intervals + causal] 33 feasible assignments ... derived=[('A', 'B'), ('A', 'C'), ('B', 'C')] enumerated=[...] -> AGREE
  [after retraction] 20 feasible assignments ... derived=[] enumerated=[] -> AGREE
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests
Ran 39 tests in 0.006s
OK
```

覆盖：三类时间概念的类型安全与精确换算；`A<<B` 必然性；`A` 与 `[1,3]`
不可比较且拒绝中点全序；端点相接不可比较；传递闭包与传递约简；顺序层为
反链；不可比较非传递（`A∥C`、`C∥B` 但 `A<<B`）；因果边与区间复合推导；
矛盾环检测与证据；不可能因果（两个点区间 + 严格因果）成环；自因果矛盾；
撤回区间/因果边后关系增删与一致性恢复；区间更正；24 种输入排列不变性；
见证满足全部区间与严格因果；三组场景枚举核验一致；撤回后再次核验一致。
