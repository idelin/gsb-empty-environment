# 多自动机交集：区间计数 / 第 k 串 / 排名 / 无限性证据

Python 3.11 标准库实现（仅用 `unittest` 做测试，不使用任何正则或自动机库）。
在若干个**显式字母表、显式转移表**的 DFA 交集中支持：

- 长度区间 `[low, high]` 计数；
- 词典序第 k 串（0-based）与串的排名，互为逆运算；
- **精确大整数**与**模计数**两种模式严格分开；
- 按需（惰性）构造积自动机状态 + 可复用的二次幂转移块处理大长度；
- DFA 修改后版本号递增，派生缓存自动失效；checkpoint 绑定版本与排序字母表；
- 无限语言判断输出「可达且能通向接受态」的循环证据 `(prefix, cycle, suffix)`；
- 输出过长时返回可续生成器；预算耗尽**不**报「不存在」；
- 空串计数由**全部**约束共同决定；
- 模计数不能用于第 k 选择（`ModCounter` 上该操作直接拒绝）；
- 反例/证据可用仅依赖原始转移表的独立模拟验证。

## 目录结构

```
dfa_intersection/
  dfa.py          # 显式 DFA、版本号、独立模拟 accepts/walk、accepts_all
  engine.py       # 惰性积状态、稀疏幂块 M_i / 前缀和块 S_i、计数/排名/环检测
  counters.py     # ExactCounter（大整数）与 ModCounter（模计数，两模式分开）
  enumeration.py  # StringEnumerator + JSON checkpoint
  witness.py      # InfiniteWitness（prefix/cycle/suffix 及独立 verify）
  errors.py
tests/
  test_intersection.py   # 26 个 unittest，含小域穷举对照
examples/
  demo.py        # 可独立执行的端到端样例
```

## 快速开始

```python
from dfa_intersection import DFA, ExactCounter, ModCounter

even_a = DFA(("a", "b"), start=0, accepting={0},
             transitions={(0, "a"): 1, (1, "a"): 0,
                          (0, "b"): 0, (1, "b"): 1})
no_bb = DFA(("a", "b"), start=0, accepting={0, 1},
            transitions={(0, "a"): 0, (0, "b"): 1, (1, "a"): 0})

exact = ExactCounter([even_a, no_bb])
exact.count_length(7)        # 精确大整数
exact.count_interval(3, 8)   # 闭区间计数
exact.kth_string(6, 10)      # 'babaaa'
exact.rank("babaaa")         # 10（排名反还原 k）
exact.kth_in_interval(4, 6, 3)

mod = ModCounter([even_a, no_bb], 1_000_000_007)
mod.count_length(100_000)    # 模计数
# mod.kth_string(...) -> UnsupportedModeError：余数无法决定第 k 串所在分支
```

### 无限语言的循环证据

```python
w = exact.infinite_witness()
# InfiniteWitness(prefix='', cycle='aa', suffix='')
w.verify([even_a, no_bb])          # True：仅用各 DFA 原始转移表独立验证
w.sample(3)                        # 'aaaaaa'，prefix + cycle*3 + suffix 恒被接受
```

证据保证：`prefix` 从初态**可达**环上状态，`cycle` 非空且在每个分量 DFA
上回到该状态，`suffix` 能到达**接受态**。不可达或无法通向接受态的
「无关环」不会导致无限结论（见测试中的 rejecting-loop 反例）。

### 大长度与幂块

长度 `n` 的路径数是起点行乘 `T**n`。惰性预建并在所有查询间复用：

```
M_i = T ** 2**i,   S_i = I + T + ... + T ** (2**i - 1)
M_{i+1} = M_i * M_i,   S_{i+1} = S_i + M_i * S_i
```

`count_interval` 用两个前缀和相减（`S(high+1) - S(low)`），复杂度对长度
是 `O(log n)`。例：一元字母表偶数长度 DFA 上，`n = 2**60` 可立即计算。

### 版本失效与可续枚举

```python
exact.count_length(1)             # 1
even_a.add_transition(0, "b", 1)  # 修改自动机 -> version += 1
exact.count_length(1)             # 缓存自动重建，结果反映修改

enum = exact.enumerator(7, budget=6)
batch = list(enum)                # 最多产生 6 个串
enum.done                         # False：预算耗尽 ≠ 枚举结束
token = enum.checkpoint()         # 绑定版本 + 排序字母表 + 下一个 k
Checkpoint.loads(token.dumps())   # 可序列化跨进程/跨调用续生成
```

## 运行

```bash
python3.11 examples/demo.py
python3.11 -m unittest discover -s tests -t . -v
```

## 设计要点（与需求对应）

- **按需积状态**：积状态是各分量状态的元组，仅从积初态 BFS/按需 `_expand`，
  缺转移按隐式死状态处理（任一分量走死则积转移死）。
- **精确/模两模式分开**：`ExactCounter` / `ModCounter` 是两个类；底层共用
  稀疏矩阵结构但注入不同算术（Python 大整数 vs 每步取模）。第 k 串、排名、
  枚举只存在于精确模式。
- **第 k 串只用精确计数**：每一步对排序字母表逐符号累加精确分支大小来
  定位；排名是反向累加。测试对小域全部接受串验证 `rank(kth(k)) == k`。
- **空串**：`count_length(0)` 直接等于「积初态是否全部分量接受」。
- **无限性判定**：先求能到达接受态的状态集合（反向 BFS），再在该集合内做
  环检测；环上状态同时满足可达 + 生产性，并构造最短 `prefix/cycle/suffix`。
- **checkpoint**：JSON 中固化排序字母表、各 DFA version、长度、下一个索引；
  恢复时任一不匹配抛 `CheckpointError`。`budget=0` 时 `done` 仍为 `False`。
- **独立可执行反例**：`DFA.accepts/walk` 与 `InfiniteWitness.verify` 只依赖
  原始转移表；测试中还会把 DFA 用其规格重建后再验证证据。
- **穷举对照**：随机 DFA × 长度 0–9 的 `itertools.product` 穷举结果与精确
  计数、区间计数、第 k 串顺序、排名、模计数（大长度闭式解）逐一对照。

## 附：本机运行结果

### `python3.11 -m unittest discover -s tests -t .`

```
Ran 26 tests in 0.5s

OK
```

测试覆盖（类 / 主要用例）：

- `CountingTests`：12 组随机 DFA 对长度 0–8 穷举对照；全部闭区间组合计数；
  空串在单约束接受/拒绝、双约束缺一、修改后恢复四种情形。
- `LexicographicTests`：第 k 串顺序与穷举序列完全一致；每个接受串满足
  `rank(kth(k)) == k` 与 `kth(rank(s)) == s`；拒绝串返回 `None`；越界异常；
  区间内按（长度，词典序）排序取第 k。
- `ModularModeTests`：模结果 == 精确结果取模；长度 50,000 与闭式解
  `2**(n-1)` 一致；模模式拒绝 kth/rank/enumerator。
- `PowerBlockTests`：一元偶数 DFA 在 `n = 2**60` 上的精确计数与区间计数；
  版本修改后幂块重建并给出新结果。
- `InfinitudeTests`：无限语言证据可独立验证且泵出串长度两两不同；用重建
  的 DFA 复验证据；可达但不可达接受态的拒绝环判有限；空语言判有限；
  环必须在每个分量上同时闭合。
- `VersionAndCheckpointTests`：增删转移/翻转接受态后计数更新；checkpoint
  分页（budget=5）拼接结果 == 穷举列表；`budget=0` 不报完成；版本/字母表/
  长度不一致的 checkpoint 被拒绝。
- `ConstructionTests`：字母表元素必须是单字符且唯一；DFA 字母表必须一致；
  模数必须 ≥ 2。

### `python3.11 examples/demo.py`（节选）

```
============ exact counting (big integers) ============
sorted alphabet: ab
|L|, lengths 0..8: [1, 1, 1, 3, 4, 6, 11, 17, 27]
|L|, lengths 3..8: 68

============ k-th string and rank (0-based, exact counts only) ============
11th accepted string of length 6: babaaa
its rank: 10
independent simulation accepts it: True
rank -> string round trip: babaaa
4th in length interval [4, 6]: baba

============ modular counting is a separate mode ============
|L_100000| mod 1000000007: 939526867
matches exact % mod: True
mod.kth_string refused: UnsupportedModeError - modular counts cannot select the k-th string; use ExactCounter

============ large length handled by reusable power blocks ============
even unary lengths only; n = 2^60 = 1152921504606846976
count(n) = 1  count(n+1) = 0
count over [0, n] = 576460752303423489 (= 2^59 + 1)

============ infinitude witness: reachable cycle that reaches acceptance ============
witness: InfiniteWitness(prefix='', cycle='aa', suffix='')
witness.verify(constraints): True
  prefix + cycle^0 + suffix = '' accepted: True
  prefix + cycle^1 + suffix = 'aa' accepted: True
  prefix + cycle^2 + suffix = 'aaaa' accepted: True
  prefix + cycle^3 + suffix = 'aaaaaa' accepted: True
reachable rejecting loop with no path to acceptance -> infinite? False

============ resumable enumeration with an output budget ============
page 1: produced=6 total=17 done=False batch=['aaaaaab', 'aaaaaba', 'aaaabaa', 'aaabaaa', 'aabaaaa', 'aababab']
page 2: produced=12 total=17 done=False batch=['abaaaaa', 'abaabab', 'ababaab', 'abababa', 'baaaaaa', 'baaabab']
page 3: produced=17 total=17 done=True batch=['baabaab', 'baababa', 'babaaab', 'babaaba', 'bababaa']
count matches: True
```
