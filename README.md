# 二项序贯概率比检验（SPRT）

Python 3.11 标准库实现（无第三方依赖）。两简单假设

- H0: X ~ Bernoulli(p0)
- H1: X ~ Bernoulli(p1)

给定似然比上下界 `A > 1`、`0 <= B < 1`（或对数界 `a > 0 > b`），顺序抽样并维护
对数累计似然比 `L_n = log f1(x1..xn)/f0(x1..xn)`：

- `L_n >= a`：停止，接受 H1
- `L_n <= b`：停止，接受 H0
- 两界之间：继续；到达 `n_max` 仍未越界则判 `INCONCLUSIVE`（到达最大样本数仍未决）

边界为闭区间，命中即停，优先于 `n_max` 判断。**不使用任何 epsilon 平移边界，
也不用固定样本量 p 值替代序贯规则。**

## 目录

- `seqtest/config.py`：参数与单样本对数权重；`p=0/1` 的零似然单独处理（`log_ratio`）。
- `seqtest/evidence.py`：可重复查询的证据过程 `Evidence`，checkpoint 快照/恢复/重放。
- `seqtest/runner.py`：随机源注入接口、`SeededBernoulli`（记录 seed）、确定性脚本源、
  单次运行与样本预算中断。
- `seqtest/exact.py`：离散小样本格点 `(n,k)` 精确递推，给出两类错误概率、
  未决概率、期望停止样本数（标注 `kind="exact"`）。
- `seqtest/simulation.py`：蒙特卡洛经验估计与标准误（标注 `kind="empirical"`）。
- `seqtest/multistream.py`：多流共享停止预算（共享样本预算 + 可选族级 alpha 上界），
  覆盖保证只给预先**声明**并经精确递推核验的规则。
- `demo.py`：运行样例；`tests/test_seqtest.py`：unittest 测试。

## 关键设计约定

- **对数累计**：`L_n = k·w1 + (n-k)·w0`（整数计数累计式），在线过程与精确递推共用
  同一函数，保证逐位一致；这避免了逐项浮点求和与闭式结果之间的 1-ULP 差异
  （`2·log 1.5` 与 `log 2.25` 在浮点下并不严格相等），且不靠 epsilon 兜底。
- **零似然**：某观测在真假设下似然为 0 时权重为 `+inf/-inf`，证据立即定界；
  系数为 0 的项按 0 处理，不产生 `0·inf=NaN`。精确递推中零质量路径自然跳过。
- **checkpoint 恢复一致**：快照含配置、`(n,k,L_n)`；恢复时用充分统计量重算并校验，
  不一致即抛错；inf 用字符串哨兵支持标准 JSON 往返。
- **预算中断/恢复不重复计样本**：预算耗尽时不产生该次抽样，返回 `interrupted`；
  追加预算后沿同一证据过程继续。
- **随机源由调用方注入**：记录 seed 与样本量；确定性脚本源用于对照测试。
- **精确 vs 经验分开报告**：`ExactReport(kind="exact")` 与
  `EmpiricalReport(kind="empirical")` 字段与语义分离。
- **选择性停止覆盖**：只有 `MultiStreamStudy.declare(...)` 预先声明、
  登记时用精确递推核验 `alpha_i <= 分配额` 的规则才在覆盖范围内，
  族级保证为并集界 `sum exact_alpha_i <= family_alpha_cap`；未声明的窥视/选择性停止无任何保证。

## 运行

```bash
python3.11 demo.py
python3.11 -m unittest discover -s tests -v
```
