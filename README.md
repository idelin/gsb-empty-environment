# keyrng — 显式键驱动的随机张量库（纯标准库，Python 3.11）

不依赖任何自动微分或张量库。所有随机流都是键的纯函数：没有全局随机状态，
图节点持有自己的键，批处理按稳定样本 ID 派生子流，checkpoint 重算复用原 mask。

## 结构

- `keyrng/prng.py` — 键 `PRNGKey(a, b)`（两个 64 位字）、`split`、`fold_in`、
  无状态计数器位流 `random_bits`、严格键冲突检测 `KeyRegistry`
- `keyrng/tensor.py` — 极简 CPU 张量（扁平 float 列表 + shape）
- `keyrng/distributions.py` — 重参数化采样：`uniform` / `normal` / `bernoulli`
- `keyrng/layers.py` — dropout、checkpoint 重算 token、重参数化正态的精确梯度
- `keyrng/graph.py` — 图执行器：节点键 = `fold_in(root, 节点名)`，支持缓存恢复
- `keyrng/batch.py` — 批处理与分块执行：样本键 = `fold_in(base, 样本ID)`

## 公开的分布公式

位混合（SplitMix64 finaliser，全部运算 mod 2^64）：

    mix64(x): x ^= x>>30; x *= 0xBF58476D1CE4E5B9
              x ^= x>>27; x *= 0x94D049BB133111EB
              x ^= x>>31
    random_bits(key, i) = mix64(key.a ^ mix64(key.b + i * GOLDEN))
    GOLDEN = 0x9E3779B97F4A7C15

- **uniform**：取每个 64 位字的高 53 位 `w`，`u = w * 2^-53 ∈ [0, 1)`
- **normal**（Box-Muller，两个输出都使用）：对均匀对 `(u1, u2)`（u1 下限 2^-53），
  `r = sqrt(-2 ln u1)`，`z0 = r cos(2π u2)`，`z1 = r sin(2π u2)`；
  返回 `mean + std * z`。重参数化梯度：`d/dmean = 1`，`d/dstd = z`
- **bernoulli(p)**：`u < p` 时为 1.0 否则 0.0
- **dropout（inverted）**：`keep = 1 - rate`，mask 取 `1/keep` 或 0；
  前向 `y = x * mask`，反向 `dx = dy * mask`

字符串/整数标识符（节点名、样本 ID）用 64 位 FNV-1a 哈希，跨进程稳定。

## 运行测试

    python3.11 -m unittest discover -v

## 实际运行结果（已记录于 RESULTS.txt）

    Ran 53 tests in ~0.2–0.9s — OK（Python 3.11.15，macOS）

独立数值验证（测试内独立重实现逐位比对 + 统计量对照理论值）：

    uniform  n=20000  mean=0.497734 (理论 0.5)   var=0.083027 (理论 0.083333)
    normal   n=40000  mean=0.003398 (理论 0.0)   var=0.992889 (理论 1.0)
    bernoulli(0.25) n=40000 freq=0.249775 (理论 0.25)
    dropout(rate=0.4) n=20000 keep-fraction=0.594800 (理论 0.6)

## 覆盖的关键性质

- 同键同形状结果一致；交错/乱序消费不改变独立子流（`test_prng.py`）
- checkpoint 重算的 mask 与原 mask 逐位相同，梯度只经原 mask 传播
  （`test_dropout_checkpoint.py`）
- 批次切分/乱序不改变相同样本 ID 的随机值与梯度（`test_batch.py`、`test_integration.py`）
- 部分节点完成后恢复不重复消耗键；严格模式检测键冲突；空张量与非法采样参数
  （`test_graph.py`、`test_prng.py`）
