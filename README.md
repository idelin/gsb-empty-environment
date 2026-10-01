# DFA 最小化与语言等价分区证书

纯 Python 3.11 标准库实现（只用 `unittest` 做测试），不使用任何正则或自动机库：
字母表与转移结构以显式数据（JSON / Python 字典）输入。

## 功能

- 去除不可达状态后最小化确定自动机（划分精化）。
- 缺失转移统一补到新增的死状态 `__dead__`（非接受、全自环）。
- 输出旧→新状态映射；商自动机采用 BFS 规范命名（`q0, q1, ...`），
  输入状态重命名不影响规范结果。
- 输出完整的语言等价分区证书：每个块及其成员，以及任意两个不同商状态之间的
  最短区分串（对pair图做反向 BFS 求得）。
- 接受与不接受状态永不合并；同语言的冗余状态合并；空字母表也可处理。
- 局部转移修改后的增量分区修复：既能拆分也能合并，并报告触及状态对数。
- 小自动机用独立的乘积空间搜索（BFS）验证原自动机与商自动机等价，报告探索的状态对数。
- 不等价时给出最短反例串，并可生成可独立执行的反例脚本。

## 输入格式

```json
{
  "alphabet": ["0", "1"],
  "states": ["A", "B", "C", "D", "E"],
  "start": "A",
  "accepting": ["B", "C"],
  "transitions": {
    "A": {"0": "E", "1": "B"},
    "B": {"0": "B", "1": "B"},
    "C": {"0": "C", "1": "B"},
    "D": {"0": "A", "1": "A"},
    "E": {"1": "C"}
  }
}
```

转移可以缺失（自动补死状态），状态可以不可达（自动删除）。

## 运行样例

### 最小化 + 证书

```
$ python3 main.py minimize examples/sample_dfa.json
Alphabet: {0, 1}
Missing transitions completed with dead state: __dead__
Removed unreachable states: D
Prepared DFA states: 5

Old -> new state mapping:
  A -> q0
  B -> q2
  C -> q2
  E -> q1
  __dead__ -> q3

Quotient DFA (canonical naming):
  start: q0
  accepting: q2
  q0: 0 -> q1, 1 -> q2
  q1: 0 -> q3, 1 -> q2
  q2: 0 -> q2, 1 -> q2
  q3: 0 -> q3, 1 -> q3

Language-equivalence partition certificate:
  block q0: {A}
  block q1: {E}
  block q2: {B, C}
  block q3: {__dead__}
  Shortest distinguishing strings for every pair of quotient states:
    q0 vs q1: '01'
    q0 vs q2: ''
    q0 vs q3: '1'
    q1 vs q2: ''
    q1 vs q3: '1'
    q2 vs q3: ''

Independent product-search verification (prepared DFA vs quotient):
  state pairs explored: 5
  result: EQUIVALENT
```

### 等价性验证 + 可独立执行的反例

```
$ python3 main.py verify examples/sample_dfa.json examples/wrong_minimized.json -o counterexample.py
state pairs explored: 5
result: NOT EQUIVALENT, shortest witness '01'
standalone counterexample script written to: counterexample.py

$ python3 counterexample.py        # 反例脚本完全自包含，可独立执行
witness: '01'
examples/sample_dfa.json accepts: True
examples/wrong_minimized.json accepts: False
counterexample confirmed: the automata disagree
```

### 增量修复演示

```
$ python3 examples/incremental_demo.py
Initial mapping (after full minimization):
  A -> q0
  B -> q2
  C -> q2
  E -> q1
  __dead__ -> q3

Edit 1: E --0--> E
  removed as unreachable: ('__dead__',)
  merges: [(('E',), ('A',))]
  splits: (none)
  touched state pairs: 2
  mapping after repair:
  A -> q0
  B -> q1
  C -> q1
  E -> q0

Edit 2: A --1--> A
  merges: (none)
  splits: [(('A',), ('E',))]
  touched state pairs: 3
  mapping after repair:
  A -> q0
  B -> q2
  C -> q2
  E -> q1

Cross-check: incremental result == from-scratch minimization. OK
```

## 测试结果

```
$ python3 -m unittest discover -s tests
.................................
----------------------------------------------------------------------
Ran 34 tests in 0.741s

OK
```

覆盖：补死状态、去不可达、等价合并/不等价不合并、接受性约束、见证串有效性
与最短性（暴力枚举对拍）、证书覆盖所有商状态对、空字母表、空语言、
重命名不变性、乘积搜索验证、反例脚本独立执行（子进程）、增量拆分/合并/
合并后再合并、触及状态对有界、随机编辑序列与从头最小化对拍、CLI 端到端。

## 设计说明

- `dfa_min/core.py`：DFA 数据结构、补全/裁剪、划分精化、最短区分串
  （pair 图反向 BFS）、商自动机构造、BFS 规范重命名、`minimize` 证书。
- `dfa_min/incremental.py`：增量修复。拆分阶段以含被改状态的块为种子做
  worklist 精化；合并阶段只对“受影响块”（可达被改转移的块 + 拆分新块）
  做代表元等价性 BFS 并用并查集合并——未受影响的块对等价性不变，跳过是
   sound 的。触及状态对数 = 拆分阶段检查的块内对数 + 合并阶段 BFS 访问的
  状态对数。
- `dfa_min/verify.py`：独立乘积搜索（BFS，含见证串回溯）、单自动机状态
  等价判定、自包含反例脚本生成。
- `dfa_min/cli.py` / `main.py`：命令行入口（`minimize` / `verify`）。
