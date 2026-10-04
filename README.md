# hmm — 有限状态隐马尔可夫模型的精确与经验推断

纯 Python 3.11 标准库实现，无第三方依赖；测试基于 `unittest`。

## 功能概览

- **精确推断**（`hmm/infer.py`）：forward-backward 与 Viterbi，全程对数空间，
  任意长序列不会下溢；另提供 Rabiner 缩放实现 `forward_backward_scaled`
  作为独立交叉验证（测试中对拍）。输出状态后验、边缘对数证据与最优路径。
- **参数校验**（`hmm/model.py`）：构造 `HMM` 时验证初始/转移/发射均为合法
  概率分布（非负、行和为 1、维度匹配），非法参数抛 `HMMValidationError`。
- **缺测观测**：`hmm.MISSING`（即 `None`）表示缺测，发射概率恒为 1（无信息）。
- **受限状态路径**：`constraints=` 按时刻给定允许状态集合。
- **分段转移**：`transitions=` 传入 `T-1` 个转移矩阵，支持分段动力学。
- **Top-K 路径**（`hmm/topk.py`）：`rank_topk_paths` 用 K-best DP 精确排序；
  `accumulate_path_probabilities` 独立重算每条路径概率并累加质量——
  **路径排序与概率累加是分开的两个阶段**。
- **增量修订**（`hmm/incremental.py`）：`VersionedInference.revise(t, obs)`
  只重算受影响的前向/后向消息行，产生新的不可变版本；旧版本查询结果保留。
- **采样**（`hmm/sampling.py`）：随机源由调用方注入（`random.Random`），
  结果记录 seed 与样本量，并标注 `kind="empirical"`；精确结果标注
  `kind="exact"`，两者明确区分。
- **全路径枚举**（`hmm/brute.py`）：小模型暴力枚举，作为测试的 ground truth。

## 约定

- **零概率序列**：明确不可行——`feasible=False`、`log_evidence=-inf`、
  `posteriors=None`、`viterbi_path=None`。绝不会把全零消息归一化成伪后验。
- **不可能观测**：任何状态都无法发射的观测 ⇒ 无路径（`viterbi_path=None`）。
- **空序列**：证据为 1（对数证据 0），后验为空元组，Viterbi 路径为 `()`。
- **逐点 MAP ≠ Viterbi**：逐时刻取后验最大的状态拼接，一般不等于联合最优
  路径（`tests/test_infer.py::TestMapVsViterbi` 给出确定性反例）。

## 目录结构

```
hmm/
  model.py        # HMM 定义与概率校验
  infer.py        # 对数空间 forward-backward / Viterbi + 缩放交叉验证
  topk.py         # Top-K 路径排序；概率累加（分离）
  incremental.py  # 局部观测修订的增量、版本化消息更新
  sampling.py     # 注入式随机源采样与经验估计
  brute.py        # 全路径枚举（小模型核验）
tests/            # 50 个 unittest 用例；test_results.txt 为测试结果
examples/
  demo.py         # 运行样例（10 个场景）
  demo_output.txt # 样例输出
```

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 测试
python3.11 examples/demo.py                    # 样例
```

测试结果：`Ran 50 tests ... OK`（见 `tests/test_results.txt`）。

## 快速示例

```python
from hmm import HMM, MISSING, forward_backward, rank_topk_paths, accumulate_path_probabilities

m = HMM(
    n_states=2,
    initial=(0.6, 0.4),
    transition=((0.7, 0.3), (0.4, 0.6)),
    emission=({"walk": 0.6, "shop": 0.3, "clean": 0.1},
              {"walk": 0.1, "shop": 0.4, "clean": 0.5}),
)
res = forward_backward(m, ("walk", MISSING, "clean"))
res.log_evidence      # 边缘对数证据（exact）
res.posteriors        # 各时刻状态后验
res.viterbi_path      # 最优路径

ranked = rank_topk_paths(m, ("walk", MISSING, "clean"), k=3)   # 排序
acc = accumulate_path_probabilities(m, ("walk", MISSING, "clean"), ranked)  # 累加
acc.covered_conditional_mass, acc.residual_conditional_mass
```
