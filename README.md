# DFA 最小化与分区证书工具

纯 Python 3.11 标准库实现（不使用任何正则/自动机库）。字母表与转移结构显式输入，转移可以部分缺失。

## 功能

- **规范化** `normalize`：先删除不可达状态（`trim`），再把缺失转移统一补到唯一的死状态 `DEAD`（`complete`）。
- **最小化** `minimize`：划分细化。接受/非接受状态从初始划分起就分开，永不合并；语言等价的冗余状态合并。
- **旧→新状态映射**：`result.mapping`，新状态名为规范名 `q0, q1, ...`（从起点按字母序 BFS 命名，重命名输入状态不改变规范结果，`canonical_key` 可验证）。
- **语言等价分区证书**：`result.certificate`，对任意两个不同商状态给出最短区分串（积空间 BFS 保证最短）。
- **增量分区修复** `repair_partition`：局部转移修改后，先对旧划分做细化（拆分），再对商自动机求最粗稳定划分（合并），两阶段都正确；报告触及状态对数 `pairs_touched`。
- **独立乘积搜索验证** `verify_partition`：对所有状态对做积空间 BFS，独立确认划分正确，并报告检查的状态对数。
- **反例独立执行**：`counterexample.py` 可单独运行，对“两状态等价”的断言给出具体反例串。
- 空字母表可处理（无转移需补，接受/非接受各自成块）。

## 文件

- `dfa.py` — 核心库
- `demo.py` — 运行样例（`python3.11 demo.py`）
- `counterexample.py` — 可独立执行的反例生成器（`python3.11 counterexample.py`）
- `test_dfa.py` — unittest 测试（`python3.11 -m unittest test_dfa -v`）

## 运行样例（`python3.11 demo.py`）

样例自动机：`s5` 不可达；`(s3,"b")` 缺失 → 死状态；`s1` 与 `s2` 冗余。

```
== Minimization ==
normalized states: ["'s0'", "'s1'", "'s2'", "'s3'", "'s4'", '<dead>']
blocks: [["'s4'"], ['<dead>'], ["'s0'"], ["'s1'", "'s2'"], ["'s3'"]]
old -> new mapping:
    's0' -> q0
    's1' -> q1
    's2' -> q1
    's3' -> q2
    's4' -> q3
  <dead> -> q4
quotient transitions:
  (q0, a) -> q1
  (q0, b) -> q1
  (q1, a) -> q1
  (q1, b) -> q2
  (q2, a) -> q3
  (q2, b) -> q4
  (q3, a) -> q3
  (q3, b) -> q3
  (q4, a) -> q4
  (q4, b) -> q4
accepting: ['q3']
certificate (shortest distinguishing string per quotient-state pair):
  q0 vs q1: 'ba'
  q0 vs q2: 'a'
  q0 vs q3: ''
  q0 vs q4: 'aba'
  q1 vs q2: 'a'
  q1 vs q3: ''
  q1 vs q4: 'ba'
  q2 vs q3: ''
  q2 vs q4: 'a'
  q3 vs q4: ''
pairs touched: 9

== Independent product-space verification ==
ok=True, pairs checked=15, counterexample=None

== Relabeling invariance ==
canonical keys equal: True

== Incremental repair after local change (s2,b)->s4 ==
partition: [["'s4'"], ['<dead>'], ["'s0'"], ["'s1'"], ["'s2'"], ["'s3'"]]
splits=1, merges=0, pairs touched=11
matches full re-minimization: True

== Incremental repair after local changes (s1,a)->s4, (s1,b) removed ==
partition: [["'s4'"], ["'s2'"], ["'s0'"], ['<dead>'], ["'s1'", "'s3'"]]
splits=1, merges=1, pairs touched=10
matches full re-minimization: True
```

反例脚本（`python3.11 counterexample.py`）：

```
counterexample for ('s0', 's1'): 'b'
  from s0: ends at s2 (accepting=False)
  from s1: ends at s3 (accepting=True)
```

## 测试结果（`python3.11 -m unittest test_dfa`）

24 个测试全部通过，覆盖：不可达删除、死状态补全、接受/非接受不合并、
冗余合并、证书见证串真实可区分且最短、空字母表、增量拆分、增量合并、
无变化时划分稳定、触及状态对数报告、乘积验证（含检测错误划分）、
重命名不变性、反例脚本独立执行。

```
Ran 24 tests in 2.266s

OK
```
