# automata — 多自动机交集的计数、选串与排名

纯 Python 3.11 标准库实现（无正则 / 自动机库），字母表与转移结构显式输入。

## 功能

- **多自动机交集约束**：`ProductAutomaton` 按需（lazy）展开积状态，不可达组合从不实体化。
- **长度区间计数**：`Counter.count(lo, hi)` / `count_length(n)` / `count_upto(n)`。
- **两种计数模式严格分离**：
  - `exact`：精确大整数，支持 `kth` / `rank` / 枚举；
  - `mod`：所有运算取模，仅用于计数——模计数不能驱动第 k 选择（调用即 `ValueError`）。
- **大长度**：可复用幂块缓存 `M^(2^i)` 与前缀和块 `I+M+...+M^(2^i-1)`，
  单次查询 O(log n) 次稀疏矩阵-向量乘；自动机被修改（`version` 变化）后缓存自动失效重建。
- **词典序第 k 串与排名**：`kth(k, lo, hi)`、`rank(word, lo, hi)`，互为逆运算；
  空串仅当**全部**约束接受时计入。
- **无限语言判定**：`find_infinite_evidence` 输出循环证据 `(prefix, loop, suffix)`，
  保证 `prefix + loop**i + suffix` 对所有 `i >= 0` 被接受；`verify_evidence` 只用自动机公开接口，
  可独立执行。无法通向接受态的"无关环"不会得出无限结论。
- **可续生成器**：`LexEnumerator.take(budget)` 分批输出；预算耗尽返回 `done=False`
  （不谎报"不存在"）；`checkpoint()` 绑定自动机版本与排序字母表，
  版本或字母表不匹配时 `resume` 抛 `ValueError`。

## 快速开始

```python
from automata import Automaton, Counter, ProductAutomaton

even_a = Automaton(
    alphabet="ab", states=["even", "odd"], initial="even", accepting={"even"},
    transitions={("even", "a"): "odd", ("odd", "a"): "even",
                 ("even", "b"): "even", ("odd", "b"): "odd"})
product = ProductAutomaton([even_a])          # 可放多个约束

counter = Counter(product)                    # 精确大整数模式
counter.count(1, 8)                           # 长度区间 [1, 8] 的串数
counter.kth(100, 1, 8)                        # 词典序第 100 串（0 起）
counter.rank("bbaabaab", 1, 8)                # -> 100

mod = Counter(product, mode="mod", modulus=10**9 + 7)
mod.count_length(10**12)                      # 大长度模计数
```

## 运行样例

```
$ python3 examples/demo.py
== exact big-integer counting ==
words with length in [1, 8]: 128
words of exact length 40: 274877906944

== modular counting (separate mode) ==
words of length 10**12 (mod 1000000007): 739841546

== k-th word and ranking (lexicographic) ==
word #100: 'bbaabaab'
rank('bbaabaab') = 100  (round-trips to k)

== infinitude with cycle evidence ==
evidence: prefix='' loop='aa' suffix='b'
independently verified: True
witness with 3 loops: 'aaaaaab' accepted=True

== budgeted, resumable enumeration ==
first batch (budget 5): ['aab', 'b', 'bb', 'bbb'], exhausted=True
checkpoint: versions=(0, 0) alphabet=('a', 'b') emitted=4
resumed batch: [], exhausted=True

== cache invalidation on mutation ==
count before mutation: 128, after: 251 (cache rebuilt)
```

## 测试

```
$ python3 -m unittest discover
.......................................
----------------------------------------------------------------------
Ran 31 tests in 0.392s

OK
```

测试要点（`tests/`）：

- 随机小自动机（≤3 个约束、≤4 状态、字母表 ab）与穷举结果对照：区间计数、
  每个 k 的第 k 串、每个串的排名、生成串反排名还原 k；
- 大长度：禁 `11` 串计数对照斐波那契（精确 n=500，模 n=10^15），幂块复用；
- 缓存失效：修改转移 / 接受态后计数立即与新构造计数器一致；
- 空串语义：仅当全部约束接受空串时 `count(0,0)==1`；
- 无限性：可验证循环证据、无关环 / 空语言 / 有限语言均判有限；
- 枚举器：分批 + checkpoint 恢复等于全量、预算耗尽不报不存在、
  checkpoint 绑定版本与字母表、模模式禁止枚举。

## 目录结构

```
automata/
  core.py        # Automaton（带版本号）、ProductAutomaton（按需积）
  counting.py    # 幂块、Counter（exact/mod）、kth、rank
  infinite.py    # 无限性判定 + 循环证据 + 独立验证
  enumerate.py   # LexEnumerator（预算、checkpoint、恢复）
examples/demo.py # 运行样例
tests/           # unittest 测试（含穷举对照）
```
