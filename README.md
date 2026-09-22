# mdawg — 在线最小无环词典自动机（Python 3.11，仅标准库）

在空仓库中实现一个**在线**维护的最小无环确定有限自动机（minimal DAFSA /
directed acyclic word graph），支持任意顺序的增删、前缀查询，并把词频元数据
与自动机结构完全分离。不依赖任何现成的正则 / 自动机库；字母表与转移结构
显式输入、显式表示。

## 文件

- `mdawg.py` — 核心实现：`MinimalAcyclicAutomaton`（结构）+ `Lexicon`（词频外观）
- `test_mdawg.py` — 20 个 `unittest` 用例，含对集合模型的随机化对拍
- `demo.py` — 运行样例
- `counterexamples/` — 三个可独立执行的反例脚本（见下）

## 设计

- **显式字母表**：构造时必须传入非空字符集合；任何含表外符号的词在
  `add`/`remove`/`Lexicon.add` 时抛出 `AlphabetError`。
- **显式转移结构**：状态是整数 id，转移是 `dict[str, int]`，存放在平行数组
  `_final` / `_trans` 中；没有隐藏的正则编译或库自动机。
- **哈希 consing 寄存器**：`_register` 把签名 `(final, sorted(transitions))`
  映射到唯一状态 id。因为自动机无环，"签名相同 ⟺ 右语言相同"（对深度归纳，
  子状态已典范化），所以可达部分始终是当前语言的最小 DFA。
- **写时复制（COW）**：更新从不改写已有状态。`add`/`remove` 沿词的路径
  自底向上重新典范化（replace-or-register）；未触及的共享后缀仍指向旧状态，
  其他词不受污染。
- **词频分离**：`Lexicon` 用普通 `dict` 存计数，自动机内不放任何元数据——
  最小自动机的终止状态被大量词共享，计数放在状态里必然错误（见反例 3）。

## 正确性论证（概要）

**引理 1（典范性）** 不变式：寄存器中每个签名至多一个状态；任意状态的每个
子状态都是典范的。初始空自动机显然成立；`add`/`remove` 只在 `_canonical`
中创建状态，且自底向上处理保证入参子状态已典范，故不变式保持。

**引理 2（最小性）** 无环自动机中，两状态右语言相同 ⟺ 签名相同（对状态
高度归纳：终态性相同且每个符号的转移目标右语言相同 ⟺ 目标签名相同 ⟺
目标为同一典范状态）。由引理 1，可达状态两两签名不同，故两两右语言不同，
即自动机是最小 DFA。

**定理（增删正确性）** `add(w)`：沿 `w` 的最长已存在前缀走，路径上的状态
可能被共享，因此全部以 COW 方式重建：先为剩余后缀建链（末态 final），再自
底向上把路径上每个父状态的相应转移改指新子状态并重新典范化。新语言 =
旧语言 ∪ {w}，且由引理 2 保持最小。`remove(w)`：终端态清 final；若无子
状态则死亡，父状态删转移，死亡向上级联但遇 final 或分支即止——这恰好只
移除 `w` 本身：若 `w` 是更长词的前缀，终端态有子状态，仅清 final，更长词
保留。归纳即得新语言 = 旧语言 ∖ {w} 且最小性保持。

## 重建策略（有证明的兜底 / 压缩）

在线维护保证任意时刻可达部分最小；但寄存器会滞留不可达状态（垃圾）。
策略：**当 `total_states() > 2 · reachable_states()` 时调用 `compact()`**
（真 GC：只复制可达状态并重映射 id），或随时用
`MinimalAcyclicAutomaton.rebuild(words, alphabet)` 从零重建。

- **正确性**：`compact` 逐条复制每个可达转移与 final 标志，语言不变；
  `rebuild` 重放在线插入，由上述定理得到同一最小 DFA（测试
  `test_rebuild_matches_online` 对拍验证）。
- **均摊代价**：设可达状态数 n。每次更新最多新增 O(|w|) 个状态；两次压缩
  之间垃圾超过 n 才触发，压缩代价 O(n)，均摊到产生这些垃圾的 Ω(n) 次更新
  上为每次更新 O(1) 额外代价。

## 复杂度

| 操作 | 代价 |
|---|---|
| `add` / `remove` | O(\|w\|) 次哈希查表（字母表大小视为常数） |
| `w in automaton` / `has_prefix` | O(\|w\|) |
| `iter_words(prefix)` | O(\|prefix\| + 输出规模 × 词长) |
| `compact` / `rebuild` | O(状态数) |

## 运行

```console
$ python3 demo.py
alphabet: aelprst

-- add words (repeated adds count) --
words: [('ape', 2), ('app', 3), ('apple', 1), ('apples', 1), ('pet', 1)]
reachable states: 9 | minimal: True

-- prefix queries (metadata joined from the separate store) --
has_prefix('appl'): True
words_with_prefix('app'): [('app', 3), ('apple', 1), ('apples', 1)]

-- delete a prefix word; longer words survive --
'app' in lexicon: False | 'apple' in lexicon: True
words: [('ape', 2), ('apple', 1), ('apples', 1), ('pet', 1)]

-- shared suffix copy-on-write --
'pet' count: 4 | states still: True
after removing all 'pet': [('ape', 3), ('apple', 1), ('apples', 1)]
minimal: True | reachable: 7 | allocated: 27

-- compaction (proven rebuild strategy) --
after compact: allocated: 7 | language preserved: True
```

## 测试结果

```console
$ python3 -m unittest
....................
----------------------------------------------------------------------
Ran 20 tests in 0.019s

OK
```

覆盖：显式字母表校验、增删、删前缀词保留更长词（双向）、共享前缀/后缀的
写时复制、级联删除在 final 处停止、空前缀/空词、前缀查询与词频分离、同词
多次添加计数、重建与压缩、以及 400 步随机操作对集合模型的对拍（每步校验
语言相等与最小性）。

## 反例（均可独立执行）

```console
$ python3 counterexamples/ce1_prefix_delete.py
naive trie:  'app' deleted -> 'apple' present? False
mdawg:       'app' deleted -> 'apple' present? True, 'app' present? False
OK: naive prefix-delete is broken; mdawg handles it correctly.

$ python3 counterexamples/ce2_shared_suffix_mutation.py
in-place:    'dog' deleted -> 'log' present? False
mdawg (COW): 'dog' deleted -> 'log' present? True, 'dog' present? False
OK: in-place mutation pollutes shared suffixes; mdawg COW is safe.

$ python3 counterexamples/ce3_frequency_in_states.py
'bad' and 'dad' share terminal state? True
state-embedded counter for 'bad' reads 9 (expected 7)
Lexicon: frequency('bad')=7, frequency('dad')=2
OK: frequencies belong outside the automaton; Lexicon separates them.
```

1. **ce1**：朴素"trie 删除 = 摘掉终端子树"会把 `apple` 一起删掉；
   mdawg 只清 final 标志。
2. **ce2**：共享后缀状态下原地修改会污染 `log`；mdawg 写时复制不受影响。
3. **ce3**：终止状态被 `bad`/`dad` 共享，词频放进状态必然串计数；
   `Lexicon` 把词频放在自动机外。
