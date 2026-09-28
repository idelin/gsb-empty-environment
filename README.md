# 合成数据差分隐私查询引擎（纯标准库）

Python 3.11，仅使用标准库；测试使用 `unittest`。**只处理合成数据**，非
`SyntheticData` 输入一律拒绝。

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 测试
python3.11 demo.py                            # 端到端运行样例
```

## 差分隐私定义与参数

* **相邻数据集**：差/增恰好一条记录的两个数据集 `d, d'`。
* **机制**：Laplace 机制 `M(x) = x + Lap(b)`，尺度 `b = Δf / ε`；
  对任意输出集合 `S` 满足
  `Pr[M(d) ∈ S] / Pr[M(d') ∈ S] ≤ exp(ε)`（纯 ε-DP，δ=0）。
* **敏感度 Δf**：
  * `count`：Δ = 1；
  * 有界 `sum`（先将字段裁剪到 `[lower_bound, upper_bound]`）：
    Δ = max(|lower|, |upper|)；
  * `histogram`：一条记录只落入一个桶，每桶独立加噪，每桶 Δ = 1。
* **组合记账**：每个数据版本一个 ε 账本，按顺序组合累加（无并行组合、
  无 δ）。每个新机制发布扣 `ε`；缓存命中扣 `0`。
* **隐私参数**：`epsilon > 0` 为每次查询参数；`total_epsilon_per_version`
  为每版本总预算，超限抛 `BudgetExceededError`，该次机制不运行、不写缓存，
  可换更小 ε 重试。
* **阈值参数 `threshold`**：不改变真实答案，仅作为查询语义的一部分；
  改变阈值得到不同规范查询，**不得复用**旧输出，必须重新记账加噪。
* 随机源为带种子 `random.Random`（可复现），加锁串行化抽样。

## 缓存与记账规则（需求映射）

| 情形 | 缓存键 | 是否扣费 |
|---|---|---|
| 完全相同规范查询 + 同一数据版本重复 | 命中，返回已发布的同一个带噪输出 | 否（记 `reuse`，ε=0） |
| 查询语义变化（谓词、字段、裁剪界、ε、`threshold`、查询种类） | 未命中，重新运行机制 | 是（记 `charge`） |
| 数据版本变化（记录内容指纹不同） | 不同版本独立缓存与账本 | 是 |
| 并发的相同查询 | single-flight 合并，只发布一次机制输出，只扣一次费 | 仅一次 |
| 预算用尽后的新查询 | 拒绝 | 不扣费 |
| 预算用尽后重复已发布查询 | 仍可免费读取缓存 | 否 |

* 规范键 = `(data_version, canonical_query_id)`；数据版本为全部记录
  键序无关的 SHA-256 指纹；查询经 JSON 规范序列化（条件排序、`in`
  集合去重排序），因此**字段/条件书写顺序不影响语义**。
* 记账为审计追加日志（`charge` / `reuse` 两种条目）。

## 文件

- `dp_synth/synthetic_data.py` — 合成数据容器、确定性生成器、版本指纹
- `dp_synth/queries.py` — 谓词、count/sum/histogram 查询、规范序列化
- `dp_synth/mechanism.py` — 线程安全种子随机源、Laplace 机制
- `dp_synth/budget.py` — 每版本 ε 账本（顺序组合）
- `dp_synth/engine.py` — 缓存、single-flight 并发合并、预算检查
- `dp_synth/experiments.py` — 统计诊断（偏差/尺度/覆盖率）
- `demo.py` — 8 个场景的运行样例
- `tests/` — 20 个 unittest 用例

## 重要声明

`experiments.py` 与 demo 中的蒙特卡洛统计（均值偏差、标准差、
`|noise| ≤ b` 经验覆盖率 ≈ 0.632）只是**经验健全性检查，用于教学与
实现校验，不构成形式化隐私证明**。隐私保证来自上述 Laplace 机制的
数学定义与敏感度标定；代码正确性与参数选择需另行审计。

直方图对未出现的类别不补零桶；计数类结果不做非负/取整后处理（后处理
可由调用方自行添加，不影响 DP）。
