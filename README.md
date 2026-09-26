# keyrand：显式键驱动随机张量库（纯标准库，Python 3.11）

无全局随机状态：所有随机性由不可变的 128 位 `Key` 显式驱动。图节点保存
键而非全局状态；checkpoint 保存键而非中间激活，重算时重放同一键。

## 模块

- `keyrand/prng.py` — `Key`、`root_key`、`split`、`fold_in`、计数器式 `random_bits`
- `keyrand/tensor.py` — CPU 小张量（扁平 float 列表 + shape，无自动微分/张量库）
- `keyrand/random.py` — uniform / normal / bernoulli 张量采样
- `keyrand/nn.py` — dropout（前向+反向）、高斯重参数化（采样+反向）、`Checkpoint`
- `keyrand/graph.py` — 节点持键的计算图：分块执行、断点续跑、严格键冲突检测
- `keyrand/batch.py` — 按稳定样本 ID 分配随机流的批处理变换

## 分布公式（公开）

- 位混合（SplitMix64 最终混淆器）：
  `z=(x+0x9E3779B97F4A7C15) mod 2^64; z=(z^(z>>30))*0xBF58476D1CE4E5B9; z=(z^(z>>27))*0x94D049BB133111EB; out=z^(z>>31)`
- Uniform[0,1)：`u = bits(key,i)[高53位] / 2^53`
- Normal(0,1)（Box-Muller）：`u1=1-u(key,2p) ∈ (0,1]`，`u2=u(key,2p+1)`，
  `z_{2p}=sqrt(-2 ln u1)·cos(2π u2)`，`z_{2p+1}=sqrt(-2 ln u1)·sin(2π u2)`
- Bernoulli(p)：`b = 1[u < p]`
- 反向 dropout：`y = x ⊙ m`，`m ~ Bernoulli(1-p)/(1-p)`；`dL/dx = dL/dy ⊙ m`
- 高斯重参数化：`z = μ + σ·ε`，`σ=exp(log_σ)`，`ε~N(0,I)`；
  `dL/dμ = dL/dz`，`dL/dlog_σ = dL/dz · ε · σ`

## 关键性质

- 同键同形状 → 逐位一致；`split`/`fold_in` 子流与求值顺序无关。
- checkpoint 重算复用原 dropout mask，梯度按原 mask 传播。
- 批处理中样本随机流 = `fold_in(root, 稳定样本ID)`，与分块大小无关。
- 图执行器断点续跑：已完成节点从 cache 跳过，不重复消耗键
  （`Node.compute_count` 恒为 1）；`strict=True` 时键冲突抛 `KeyConflictError`。

## 运行测试

    python3.11 -m unittest discover -s tests -v

## 实际运行结果（Python 3.11.15, macOS arm64, 2026-09-26）

    Ran 18 tests in 0.208s
    OK

全部 18 项通过，覆盖：

- 同键同形状一致、不同执行顺序不改独立子流（`TestDeterminism`）
- 重算 dropout 复用原 mask、梯度按原 mask 传播（`TestDropoutCheckpoint`）
- 改变批次切分后相同样本 ID 保持随机值（`TestBatchChunking`）
- 部分节点完成后恢复不重复消耗键、键冲突检测（`TestGraphResume`）
- 空张量与非法采样参数（`TestEdgeCases`）
- 独立数值验证：正态矩（|mean|<0.03, |var-1|<0.06, n=20000）、
  均匀分布卡方（χ²<27.88, df=9）、Bernoulli 频率（5σ 容差）、
  重参数化梯度有限差分（误差 <1e-4）（`TestNumericalVerification`）
