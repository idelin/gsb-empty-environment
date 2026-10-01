# unbiased-shuffle

显式 PRNG 的无偏 Fisher-Yates 洗牌工具包（Python 3.11+，仅标准库，unittest 测试）。

## 功能与设计

- **显式随机源，调用方注入**：任何提供 `getrandbits(k)` 的对象均可注入；
  `CountingSource` 包装注入源并记录 seed、抽样次数（draws）与消耗比特数（bits）。
- **无偏范围抽样**：`randbelow(source, n)` 用拒绝采样（取 `bit_length(n-1)` 个比特，
  拒绝 `>= n` 的值），避免取模偏差；`n` 支持任意大整数。抽样只依赖逻辑步，
  与底层比特流的块大小、I/O 读写重试完全无关（重试均为幂等绝对写，不消耗随机性）。
- **可暂停 Fisher-Yates**：`ShuffleState` 维护交换前沿 `frontier`（自 n-1 递减），
  每步 `j = randbelow(frontier+1)` 保证索引范围 `[0, frontier]` 正确；
  检查点同时保存交换前沿、随机源状态与已发布位置（`> frontier` 的位置不再被触碰）。
- **按排名定位（部分）排列**：factoradic/Lehmer 编码，`unrank_partial(n, k, rank)`
  是 `[0, P(n,k))` 到 k-部分排列的双射；借助虚拟有序集合，`n` 可达 10**60
  而不物化候选列表，排名为大整数。
- **可恢复外存洗牌**：`ExternalShuffle` 对定长记录文件原地洗牌。
  每步持久化顺序：预写日志（绝对写集合 + 抽后随机状态）→ 数据写 → 原子替换检查点
  → 删日志。恢复时重放日志中的幂等绝对写并推进检查点，绝不重新抽取已提交的随机数，
  因此不重复交换、不重复元素、不缺失元素；撕裂/陈旧日志被安全丢弃。

## 目录结构

```
unbiased_shuffle/
  prng.py       # CountingSource（记录 seed/样本量）、randbelow（拒绝采样）
  shuffle.py    # ShuffleState：可暂停 Fisher-Yates + 检查点
  ranking.py    # rank/unrank 全排列与部分排列（大整数）
  external.py   # ExternalShuffle：日志 + 检查点的可恢复外存洗牌
tests/          # unittest 测试（精确结论与经验估计分别标注）
examples/run_demo.py
```

## 运行

```bash
python3 -m unittest discover -s tests -t . -v   # 运行测试
python3 examples/run_demo.py                     # 运行样例
```

## 精确结论 vs 经验估计

- **【精确结论】**（`tests/test_enumeration.py`、`tests/test_randbelow.py`、
  `tests/test_ranking.py`）：
  - 小 n 枚举全部 n! 个随机分支，与 n! 个排列一一对应（每个恰出现一次），
    故 j 均匀 ⟹ 排列严格均匀；
  - `randbelow` 单轮接受：每个 `r < n` 在 k 比特空间中恰有一个原像，
    接受概率均为 `2**-k`，条件分布严格均匀，且接受概率 `n/2**k > 1/2`；
  - rank/unrank 经全量验证为双射（n ≤ 7 全排列、多组部分排列）。
- **【经验估计】**（`tests/test_enumeration.py::TestEmpiricalUniformity`）：
  固定 seed 跑 96000 次 n=4 洗牌，各排列频率接近期望 4000（容差约 7.7σ），
  仅为统计佐证，非严格证明。

## 崩溃恢复一致性

`tests/test_recovery.py` 在洗牌阶段**每一个持久化操作边界**注入崩溃
（日志写、两次记录写、检查点写、日志删除），恢复后要求：

- 最终数据与连续运行**逐项一致**；
- 样本量（draws/bits）与连续运行一致（未重复抽取）；
- 续跑的 `(frontier, j)` 步骤序列恰为连续运行序列的后缀（恢复不重复交换）；
- 结果为原序列的排列（无重复、无缺失）；
- 多次连续崩溃、撕裂日志、真实 fsync 场景均覆盖。

## 运行样例输出（`python3 examples/run_demo.py`）

```
=== 1. 注入随机源的无偏洗牌（记录 seed 与样本量） ===
洗牌结果: [1, 0, 9, 7, 8, 4, 6, 2, 5, 3]
元数据  : {'seed': 20261001, 'draws': 9, 'bits': 25}

=== 2. 可暂停状态：中途检查点 -> 恢复 -> 与连续运行一致 ===
暂停时检查点: frontier = 4 , published = [5, 6, 7] , seed = 42 , draws = 3
恢复后继续  : [1, 4, 3, 7, 2, 6, 0, 5]
连续运行    : [1, 4, 3, 7, 2, 6, 0, 5] -> 一致: True

=== 3. 按排名定位排列（含大整数索引） ===
n=5, rank=  0 -> [0, 1, 2, 3, 4]
n=5, rank=  1 -> [0, 1, 2, 4, 3]
n=5, rank= 60 -> [2, 3, 0, 1, 4]
n=5, rank=119 -> [4, 3, 2, 1, 0]
n=1000, rank=2**2500+987654321 -> 前 5 项: [0, 1, 2, 3, 4]
n=10**60 的 4-部分排列, rank=P(n,4)-1 -> [999...999, 999...998, 999...997, 999...996]

=== 4. 可恢复外存洗牌：崩溃 -> 恢复 -> 与连续运行逐项一致 ===
连续运行结果: [13, 11, 1, 2, 10, 15, 0, 6, 8, 12, 4, 9, 5, 14, 7, 3] meta: {'seed': 777, 'draws': 20, 'bits': 66}
注入崩溃    : injected crash before op #9
崩溃时检查点: frontier = 14 , published = [15] , draws = 1
恢复后结果  : [13, 11, 1, 2, 10, 15, 0, 6, 8, 12, 4, 9, 5, 14, 7, 3] meta: {'seed': 777, 'draws': 20, 'bits': 66}
逐项一致    : True | 无重复无缺失: True

=== 5. 均匀性：精确结论 vs 经验估计 ===
[精确结论] n=4: 24 个分支 -> 24 个排列，每个恰出现 {1} 次
[经验估计] n=3, 24000 次试验: 每排列期望 4000 次, 实测 [3948, 4039]
```

## 测试结果

```
$ python3 -m unittest discover -s tests -t . -v
...（29 个测试，详见下方清单）
Ran 29 tests in 12.6s
OK
```

覆盖：randbelow 精确枚举/大整数/块大小无关、可暂停洗牌检查点与已发布位置、
排名双射与大整数往返、小 n 全分支枚举均匀性（精确）与频率检验（经验）、
外存洗牌确定性与重试无关性、全操作边界崩溃恢复逐项一致。
