# unbiased-shuffle

Python 3.11，仅标准库，测试使用 `unittest`。实现无偏 Fisher-Yates 洗牌（显式
PRNG、可暂停状态）、无取模偏差的范围抽样（支持大整数索引）、按排名定位部分排
列，以及可崩溃恢复的外存洗牌。

## 设计要点

- **随机源由调用方注入**：所有算法接收一个 `rng` 对象（提供
  `getrandbits(k)` / `randbelow(n)` / `getstate()` / `setstate()`），自身不创
  建随机源。`SplitMix64Source` 记录 `seed` 与样本量（`draws` = 已消耗的 64 位
  随机字数；洗牌器另记录 `samples` = 每次交换一次的抽样次数）。
- **无偏范围抽样**（`rangesample.randbelow`）：拒绝采样。取 `L = n.bit_length()`
  位，拒绝 `>= n` 的值，不用取模，支持任意大整数 `n`。
- **可暂停洗牌**（`PausableShuffle`）：状态 = 数组 + 交换前沿 `frontier` +
  rng 状态 + 轨迹；`snapshot()` / `restore()` 随时暂停与恢复。
- **部分排列按排名定位**（`unrank_partial` / `rank_partial`）：按字典序把
  `P(n,k)` 个 k-排列与排名 `[0, P(n,k))` 一一对应，大整数精确运算。
- **可恢复外存洗牌**（`ExternalShuffler`）：对定长记录文件做原位
  Fisher-Yates。每步协议：
  1. 抽取 `j ~ Uniform[0, i]`（每步恰好一次抽样）；
  2. 原子写日志 `journal.json`（前沿 `i`、`j`、两条记录的完整前像、抽样后的
     rng 状态）；
  3. 应用交换并 flush + fsync；
  4. 原子写检查点 `checkpoint.json`（交换前沿、rng 状态、已发布位置
     `published_from = frontier + 1`、seed、样本量、轨迹）；
  5. 删除日志。

  恢复规则（比较日志与检查点的前沿）：相等 → 日志为待完成交换，用完整前像
  幂等重放（重复写相同镜像无副作用）；日志前沿 = 检查点前沿 + 1 → 陈旧日志，
  丢弃；其他 → 报 `CorruptionError`。因此崩溃不会造成重复或缺元素，恢复后不重
  复交换、不重抽样，与连续运行逐项一致。
- **随机整数抽样不受块大小和读写重试影响**：抽样只发生在每步一次，与 I/O 块
  大小无关；底层读写失败按次数重试，重试不消耗随机数。

## 精确结论 vs 经验估计

- **[EXACT]** 小 n 枚举全部 `∏(i+1) = n!` 条随机分支，每个排列恰好出现一次
  （`tests/test_shuffle.py::ExactEnumerationTest`）；拒绝采样每个值恰好一个原
  像（`tests/test_rangesample.py::ExactUniformityTest`）；unrank/rank 对全部
  小 `(n,k)` 是双射（`tests/test_unrank.py`）。
- **[EMPIRICAL]** 卡方拟合（n=3 排列频率、randbelow 频率），固定种子、确定性
  通过，仅作经验估计（`EmpiricalUniformityTest` 与 `demo.py` 第 5 节）。

## 运行

```sh
python3.11 demo.py                    # 运行样例
python3.11 -m unittest discover -v    # 测试（24 个）
```

完整运行样例与测试结果见 `sample_output.txt`（本仓库实际运行的输出）。

## 文件

- `unbiased_shuffle/prng.py` — 显式可序列化 PRNG（SplitMix64 / random 适配器）
- `unbiased_shuffle/rangesample.py` — 无取模偏差范围抽样（大整数）
- `unbiased_shuffle/shuffle.py` — 可暂停 Fisher-Yates
- `unbiased_shuffle/unrank.py` — 部分排列 rank/unrank
- `unbiased_shuffle/external.py` — 可恢复外存洗牌（日志 + 检查点 + 重试）
- `tests/` — unittest 测试（精确枚举与经验估计分别标注）
- `demo.py` — 运行样例
