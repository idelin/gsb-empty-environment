# Exact Alias Sampler (Python 3.11, stdlib only)

常数步（O(1)）有放回抽样：Vose alias 方法，构造与采样全程使用
`fractions.Fraction` 精确有理数运算，无浮点舍入、无归一化漂移。

## 设计要点

- **精确构造**：`AliasTable(weights)` 接受 `int` / `Fraction` / 有限 `float`，
  容量分摊（Vose 小/大worklist配对）全部以有理数进行。
- **无偏采样**：调用方注入随机源（任何带 `randrange` 的对象，如
  `random.Random`）；每步抽一列，再用 `randrange(分母) < 分子` 做无偏阈值比较。
- **可重放**：表可序列化为 JSON（分子/分母字符串）；会话记录 `seed` 与
  `draws`，`session.replay()` 精确复现样本序列。
- **双版本发布**：`AliasTablePublisher.publish_update()` 批量更新权重并原子换表；
  旧会话持有旧表引用，继续读旧分布。
- **校验**：负权、非有限权（inf/nan）、全零、空输入均拒绝；单正权项必选；
  零权项概率恒为 0。
- **结论标注**：返回 `Fraction` 的为 [EXACT] 精确结论；返回 float 的频率
  统计为 [EMPIRICAL] 经验估计。

## 文件

- `alias_sampler/exact_alias.py` — 精确 alias 表（构造/采样/序列化/概率重建）
- `alias_sampler/publisher.py` — 双版本表发布 + 可重放抽样会话
- `alias_sampler/analysis.py` — 全结果枚举（精确证明）与经验频率估计
- `tests/test_alias_sampler.py` — 21 个 unittest 用例
- `demo.py` — 运行样例

## 运行

```bash
python3.11 -m unittest discover -s tests -v
python3.11 demo.py
```

## 测试结果

```
Ran 21 tests in 0.191s
OK
```

覆盖：负权/非有限权拒绝、单正权必选、零权不选中、极小权重（1e-15、1e-30
量级）精确概率、归一化无误差（概率和恰为 1）、小权重集全枚举证明、
序列化往返无偏差、反序列化后重放一致、双版本发布后旧会话读旧分布、
seed/样本量记录、经验频率与精确概率一致（固定种子）。

## 运行样例（`python3.11 demo.py` 节选）

```
=== 1. Exact construction with tiny / zero weights ===
weights:            ['1/1000000000000', '2', '0', '3', '1/7']
[EXACT] table reconstruction == normalized weights: True
[EXACT] probabilities sum to exactly 1: True

=== 2. Exact enumeration proof on a small weight set ===
weights [1, 2, 3], enumerated outcomes: 4 equiprobable (column, draw) pairs
[EXACT] enumerated distribution: ['1/6', '1/3', '1/2']
[EXACT] equals normalized weights: True

=== 3. Empirical sampling (caller-injected RNG) ===
seed=20260927, draws=100000
[EMPIRICAL] frequencies: [0.16587, 0.33351, 0.50062]
[EXACT]     probabilities: [0.16666666666666666, 0.3333333333333333, 0.5]

=== 4. Serialization round-trip + replay ===
[EXACT] restored table identical: True
[EXACT] restored distribution unbiased: True
session state: {'version': 0, 'seed': 42, 'draws': 10}
replay matches produced sequence: True

=== 5. Dual-version publishing (batch weight update) ===
[EXACT] old session v0 distribution: ['7/36000000000007', ...]
[EXACT] new session v1 distribution: ['100/101', '1/101', '0', '0', '0']

=== 6. Edge cases ===
[EXACT] single positive weight -> always item 2: True
rejected [1, -1]: negative weight rejected: -1
rejected [1.0, inf]: non-finite weight rejected: inf
rejected [nan]: non-finite weight rejected: nan
```
