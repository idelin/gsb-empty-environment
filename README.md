# mergeable_sampler

对未知长度流的等概率 k 采样，支持分片样本与长度信息的**精确**合并。
纯 Python 3.11 标准库，测试基于 `unittest`。

## 算法：bottom-k（优先键采样）

- 每个进入流的元素由**调用方注入的随机源**分配一个均匀优先键 `key ∈ [0,1)`；
- 每个分片保留键最小的 k 个元素（`Entry(origin, seq, key, item)`）及本片的
  流长度 `n`；
- 合并 = 取各分片保留集合的并集中键最小的 k 个。

全局最小的 k 个键必然落在各片最小的 k 个键的并集里，因此**合并结果与集中处理
逐次相等**（对每个随机结果都相等，不只是分布相同）。每个 k 子集等概率，
与分片大小、合并树形状无关。样本携带的优先键就是可验证的合并证据。

**为什么不用简单拼接**：若每片各采 k 个再拼接/等权抽取，大小为 m 的分片不论
m 大小都贡献 k 个候选，小分片元素被过度代表（本仓库的经验测试
`test_naive_concatenation_is_biased` 展示了该偏倚）。bottom-k 无此偏倚。

## 合并语义

- **幂等重试**：条目以 `(origin, seq)` 为唯一标识，合并按 `shard_id` 整体替换；
  同一份快照重试合并不会重复计数（长度与样本均不变）。
- **可继续追加**：分片追加元素后重发新快照，旧贡献被替换而非累加。
- **层级合并**：`Merger.snapshot(id)` 把子树打包成快照供父合并器合并，
  语义与单层一致。

## 长度更正与撤回的能力边界

- **支持**：源分片重建出自洽状态（长度与保留条目一致）后整体替换重发；
  更正可沿合并树逐级重传。
- **不支持**：合并器单方面删除单个元素或凭空修改长度——bottom-k 的淘汰
  不可逆，被逐出的条目已丢失；只改长度而不重建条目的"更正"会被
  `ShardSnapshot` 校验拒绝（`ValueError`）。

## 随机源与审计

`RecordedRandom(seed)` 记录 seed 与抽取次数（每元素一次）；同 seed 结果可复现。
`ScriptedRandom(keys)` 用于注入精确键值做枚举核验。

## 精确结论 vs 经验估计

- **EXACT**：`verify.check_exact_uniformity` 枚举全部 n! 种键序（bottom-k 只
  依赖键的相对次序，且各次序等可能），验证每个 k 子集恰好出现 n!/C(n,k) 次；
  并验证任意分片与合并树对每个键序都与集中处理结果一致。
- **EMPIRICAL**：`verify.empirical_subset_counts` 为带 seed 的蒙特卡洛估计，
  输出均标注为估计值。

## 边界行为

- 流短于 k：返回全部元素；
- 分片大小不同：每项仍等概率（精确核验见测试）；
- `k=0`：只计数 `n`，不保存任何数据。

## 运行

```bash
python3.11 -m unittest discover -s tests -t . -v   # 18 个测试
python3.11 examples/demo.py                        # 运行样例
```

## 样例输出（python3.11 examples/demo.py）

```
== EXACT: every k-subset is equally likely (full enumeration) ==
stream n=6, k=3: enumerated 720 equally likely key orderings
centralized : 20 subsets x 36 each -> uniform: True
shards 1/2/3: 20 subsets x 36 each -> uniform: True, identical to centralized: True

== EXACT: merge-tree shape does not change the result ==
checked flat and hierarchical ((s0+s1)+s2) trees over all 720 orderings: mismatches vs centralized = 0

== EMPIRICAL: seeded run over an uneven 3-shard stream ==
seed=20260927, k=10, shard sizes=[1000, 9000, 40000], merged n=50000
rng draws per shard: [1000, 9000, 40000]
sample (global item ids): [444, 12062, 12419, 13020, 17136, 26878, 31093, 34692, 40173, 42675]

== EMPIRICAL: subset frequencies, seeded Monte-Carlo ==
n=6, k=3, trials=20000, seed=123: 20 subsets, expected ~1000 each, observed min=941 max=1085 (estimate, not a proof)

== Merge semantics: retry, append, correction, k=0, short stream ==
retry-safe: merged n=10 (not 20)
append-friendly: merged n=15 (not 25)
length correction by replacement: merged n=12
k=0: n=1000, retained entries=0
stream shorter than k: n=2, sample=['b', 'a']
```

## 测试结果（python3.11 -m unittest discover -s tests -t .）

```
Ran 18 tests in 7.028s
OK
```

覆盖：短流全返回、k=0 不存数据、集中/分片/层级合并的精确等概率枚举核验、
重试幂等、追加后替换、长度更正与撤回（含不一致更正拒绝）、seed 与抽样量
记录、经验均匀性、朴素拼接偏倚对照。

## 文件结构

- `mergeable_sampler/core.py` — `Entry` / `ShardSnapshot` / `Shard` / `Merger`
- `mergeable_sampler/rng.py` — `RecordedRandom` / `ScriptedRandom`
- `mergeable_sampler/verify.py` — 精确枚举与经验估计核验
- `tests/test_mergeable_sampler.py` — 18 个 unittest 用例
- `examples/demo.py` — 运行样例
