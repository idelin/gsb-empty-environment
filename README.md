# bayes_tree — 有限假设的对数域序贯贝叶斯更新

纯标准库，Python 3.11，无第三方依赖。

## 满足的约束

- **对数域归一化**：先验和每条观测的似然转换到 log 域，归一化证据
  `log P(D) = logsumexp(log prior + Σ log likelihood)` 用 logsumexp 计算；
  50 个 1e-300 量级的似然也不会下溢（见
  `test_log_domain_survives_tiny_likelihoods`）。
- **每步证据值留档**：`history()` 返回每一步的 `EvidenceRecord`
  （步号、操作、log 证据、先验版本、是否冲突）。
- **似然贡献树**：`_ContributionTree` 是按观测到达顺序排列的线段树，
  叶子是每条观测的 log 似然向量，内部节点是逐假设求和（即 log 域乘积）。
  迟到观测按 id 插入、`correct_observation` 原地改叶、
  `retract_observation` 把叶重置为单位向量 0，受影响路径自底向上重算，
  复杂度 O(log n)。
- **零似然不靠除法恢复**：撤回时把叶向量重置为 0 并重算路径，从不做
  “总量 ÷ 该条似然”这种在 0 似然下不可恢复的运算；测试
  `test_zero_likelihood_retraction_is_exact_no_division` 断言撤回后后验
  与 log 证据**逐位相等**。
- **零证据 = 冲突，绝不均匀重置**：观测（和/或先验）同时排除所有假设时，
  证据为 0，`posterior()`/`log_evidence()` 抛 `ConflictError`，冲突状态
  保留以便撤回；记录中 `log_evidence=-inf`、`conflict=True`。
- **重复 id 不双更新**：重复添加抛 `DuplicateObservationError`，状态不变。
- **分批 == 连续**：`apply_batch` 逐条调用同一批原语，后验与证据历史与
  逐次调用完全一致（测试按浮点相等断言）。
- **先验版本原子更新**：`update_prior(prior, version=...)` 要求版本号
  严格递增，先校验再替换；非法先验（如全 0）不改动任何状态。假设集合在
  更新器生命周期内固定（似然向量按假设顺序对位），更换假设集合需新建
  更新器。
- **分片合并不重复计入**：`merge_shards` 按观测 id 去重，同一 id 只计
  一次；同 id 但似然互相矛盾时抛 `ConflictError` 而非静默选一份。
- **精确有理小模型对照**：`ExactRationalModel` 用 `fractions.Fraction`
  给出精确后验与证据，测试断言 float/log 模型在小数点后 12 位内一致。
- **随机源由调用方注入**：`empirical_posterior(..., rng=random.Random(seed),
  seed=..., samples=...)` 不自取随机数，结果 `EmpiricalResult` 带
  `label="empirical"`、`seed`、`samples`；解析结论用
  `updater.exact_posterior()` 返回 `label="exact"`，两者分别标注。

## 文件

- `bayes_tree.py` — 实现（贡献树、更新器、分片、精确有理模型、经验估计）。
- `test_bayes_tree.py` — 29 个 unittest 用例。
- `demo.py` — 端到端运行样例。

## 运行

```sh
python3.11 -m unittest -v test_bayes_tree
python3.11 demo.py
```
