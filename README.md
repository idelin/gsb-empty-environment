# 等价类敏感值分布 vs. 总体分布：距离增量监控（仅合成数据）

Python 3.11 标准库实现（无第三方依赖，测试用 `unittest`）。对每个等价类
（准标识符分组）维护其敏感值桶计数，与**总体基线**分布逐桶比较：

- 离散**总变差距离**（Total Variation），`Fraction` 精确有理计算；
- 有序一维**运输距离**（Earth Mover / W₁），给出最优输运流与成本验证器
  （含对偶最优性证书）；
- 一般**成本矩阵模式**：真正求解最小费用流（successive shortest paths），
  不沿用一维累计公式；小实例用**穷举全部输运方案**核对；
- 数据插入/撤回时增量维护计数与**基线版本**，基线一变所有类标记为待重评；
- 输出逐桶（或逐间隙）贡献、样本基数（类大小、基线大小）与阈值证据。

## 定义

支撑集（敏感值桶）为固定有序集合 $B=\{b_0,\dots,b_{k-1}\}$。某等价类计数
$c=(c_0,\dots,c_{k-1})$，$n_c=\sum_i c_i$，分布 $P(i)=c_i/n_c$；总体基线
计数 $g$，$n_g=\sum_i g_i$，分布 $Q(i)=g_i/n_g$。

1. **总变差**：$\mathrm{TV}(P,Q)=\frac12\sum_i |P(i)-Q(i)|$。
   逐桶贡献为 $\frac12|P(i)-Q(i)|$，总和恰为 TV。所有分子分母用
   `fractions.Fraction` 精确表示，无浮点误差。

2. **有序一维运输距离**：桶 $i$ 的位置为 $x_i$（单调不减，默认 $0,\dots,k-1$），
   地面成本 $d_{ij}=|x_i-x_j|$：
   $$\mathrm{EMD}_{1D}(P,Q)=\sum_{j=0}^{k-2} |\mathrm{CDF}_P(j)-\mathrm{CDF}_Q(j)|\,(x_{j+1}-x_j).$$
   逐**间隙**贡献 $|\mathrm{CDF}_P(j)-\mathrm{CDF}_Q(j)|\,\Delta x_j$。
   最优流由西北角贪心构造（质量沿相邻位置搬运）；`verify_emd_1d` 同时校验
   (a) 非负、行和为 $P$、列和为 $Q$；(b) 流成本等于声称距离；
   (c) 对偶势 $\phi$ 满足 1-Lipschitz 约束 $\phi_j-\phi_i\le |x_i-x_j|$，
   且对偶目标 $\sum_i(Q(i)-P(i))\phi_i$ 等于声称距离——弱对偶即证最优。

3. **一般成本矩阵模式**：给定任意整数成本矩阵 $C_{ij}$（不必是线度量，
   例如平方距离），求解运输问题
   $\min_f \sum_{ij} f_{ij} C_{ij}$ s.t. 行和 $P$、列和 $Q$、$f\ge 0$。
   实现是真正的最小费用流（SPFA 最短路逐次增广），**不是**一维累计公式。
   为保持精确有理：把双方计数缩放到同一整数总量 $n_c n_g$
   （供应量 $c_i n_g$，需求量 $g_j n_c$），整数最小费用流得到总费用后除以
   $n_c n_g$。`emd_general_exhaustive` 递归枚举全部整数输运矩阵供小实例
   交叉核对；`verify_transport_plan` 校验流的可行性与成本。

## 参数

`DistributionMonitor(support, positions=None, cost_matrix=None,
tv_threshold=None, emd_threshold=None)`

- `support`：敏感值桶标签列表（唯一、有序）；
- `positions`：一维模式下各桶位置（默认等距 $0,\dots,k-1$）；
- `cost_matrix`：提供即启用一般成本矩阵模式（最小费用流），否则用一维模式；
- `tv_threshold` / `emd_threshold`：阈值（`Fraction`/`int`/`float`，
  float 按十进制字符串转精确分数）。证据按贡献降序排列并累计，到累计值
  **严格超过**阈值的最小前缀为止，即“哪些桶足以证明越界”。

## 增量语义与边界

- `add_record(record_id, class_key, value)` /
  `withdraw_record(record_id)`：O(1) 增量更新类计数与总体计数；
  距离在 `evaluate` 时从更新后的计数 O(k) 精确重算，不重扫数据。
- 每次增删都改变总体基线，`baseline_version += 1`；`stale_classes()`
  返回所有评估版本落后的类——**总体基线版本变化使所有类必须重新评估**；
  `evaluate_all()` 重评全部。
- `Evaluation` 同时给出 `n_class`、`n_baseline`（样本基数）、基线版本、
  逐桶/逐间隙贡献、阈值证据与输运流。
- **完全相同分布**（计数成比例即可）TV 与 EMD 精确为 `0`。
- **空类不除零**：类空返回状态 `SKIPPED_EMPTY_CLASS`，总体空返回
  `SKIPPED_EMPTY_BASELINE`，距离为 `None`。

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 27 个测试
python3.11 demo.py                            # 合成数据运行样例
```

## 文件

- `distances.py`：TV、一维 EMD（流+对偶验证器）、最小费用流、穷举核对、
  一般成本矩阵 EMD 及流可行性验证器；
- `monitor.py`：`DistributionMonitor` / `Evaluation`，增量增删、基线版本、
  陈旧类检测、阈值证据、空类/空基线处理；
- `demo.py`：合成数据完整样例（建库→撤回→空类→流验证→一般成本矩阵与
  穷举一致、且与一维公式不同→随机小实例批量交叉核对）；
- `tests/`：27 个 `unittest` 用例。

## 测试结果

```
Ran 27 tests in 0.1s
OK
```

覆盖：TV 精确分数/恒等为 0/不相交为 1/空分布报错；一维 EMD 已知值、
不规则位置、恒等为 0、验证器拒绝篡改成本与非法对偶；最小费用流在 40 个
随机小实例（2×2、3×3）上与穷举最优一致，并展示平方距离成本下一维累计公式
给出错误结果（一般模式 4 vs 一维 2）；监控的增删版本、全类重评、空类跳过、
阈值证据最小越界前缀等。

## 免责声明

本工具仅处理**合成数据**，距离与阈值越界是描述性统计监控，**不构成**
差分隐私或任何形式化隐私保证：距离小不等于隐私安全，距离大也不等价于
形式化攻击结论。
