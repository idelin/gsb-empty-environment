# proofpack — 可复算的聚合证明包

为 **sum / count / 加权平均** 生成可复算证明包。证明包同时证明两件事，
而不是只给一个无法核对的摘要：

1. **贡献成员完整性**：哪些记录进入了聚合，且没有遗漏、没有重复；
2. **聚合算术**：sum、count、加权平均的分子/分母可由贡献列表直接重算。

纯 Python 3.11+ 标准库，测试用 unittest。样例为合成数据，证明包完全确定
（不含时间戳/随机数），证据可重放。

## 设计

- **有序承诺树**：原始数据按 key 有序排列，逐叶 SHA-256 构造 Merkle 树
  （`proofpack/commitments.py`），树根即数据集承诺。
- **过滤规则**：整数区间 `key ∈ [lo, hi]`（`FilterRule`，带 `rule_id` + `version`）。
  数据有序 ⇒ 命中记录是连续下标区间 `[i, j)`。
- **范围完整性证据**：区间外的排除区域用**覆盖子树摘要**（最小子树集）表示；
  边界记录 `i-1` 与 `j` 原样揭示。验证器检查边界 key 严格落在区间外、
  揭示 key 序列单调、贡献下标连续唯一，并用「摘要 + 揭示叶」重建树根——
  重建成功即无遗漏、无重复。
- **聚合算术**：`sum`、`count`、`wavg_numerator = Σv·w`、
  `wavg_denominator = Σw` 全部显式给出；加权平均用 `Fraction` 精确表示，
  **总权重为 0 时标记 `UNDEFINED`**。绝不用各批均值的平均代替加权均值
  （测试 `test_mean_of_batch_means_rejected` 专门拒绝这种错误包）。
- **贡献列表摘要**：有序滚动哈希（count + digest），删除一条记录必然改变。
- **稳定 ID 与版本**：事实（`rid` + `fact_version`）、规则
  （`rule_id` + `version`）、步骤（`step_id` + `version`）、包
  （`package_id` + `version`）。
- **可重放步骤链**：`select → aggregate → retract...` 每步哈希链接自
  genesis，验证器从创世哈希重放整条链，末哈希须等于 `package_digest`。
- **增量撤回**：`retract_contribution` 追加一个 retract 步骤，同步更新
  分子、分母、sum、count 与贡献摘要（范围证据仍证明原始选择，撤回作用于
  其之上）。验证器同时做两个对照：(a) 对剩余贡献**直接重算**；
  (b) 增量 delta 应用到上一版本聚合结果须等于新结果
  （`retraction_vs_previous`）。

## 运行

```bash
python3 run_sample.py            # 端到端样例（合成数据）
python3 -m unittest discover -s tests -v   # 21 个测试
```

## 运行样例（实际输出）

```
dataset root : 5d4f09267b92af321cfefff32c52624164cb8c66928d07a4c665c8d840d2adb7
rule         : {"field": "key", "hi": 60, "lo": 20, "rule_id": "rule:key-range", "version": 1}
contributions: 14 of 40 records
aggregates   : {"count": 14, "sum": 325, "wavg": "423/19", "wavg_denominator": 38, "wavg_numerator": 846}

weighted mean (correct)      : 423/19
mean of batch means (WRONG)  : 1466/65   <- rejected: not the weighted mean

== verify v1 (fresh package) ==           => overall: PASS (10/10 checks)

retracted F0017 (value=12, weight=5)
  sum: 325  ->  313
  count: 14  ->  13
  wavg_numerator: 846  ->  786
  wavg_denominator: 38  ->  33
  wavg: 423/19  ->  262/11
  summary digest changed: True

== verify v2 (after retraction, cross-checked vs v1) ==  => overall: PASS (11/11)

zero-denominator package:
  aggregates: {"count": 3, "sum": 20, "wavg": "UNDEFINED", "wavg_denominator": 0, "wavg_numerator": 0}
== verify zero-denominator package ==      => overall: PASS
```

## 测试结果

```
Ran 21 tests in 0.008s
OK
```

覆盖：正常包验证、算术直接重算对照、精确分数加权平均、批均值陷阱拒绝、
零分母 `UNDEFINED`（含撤回至零分母）、空选择、遗漏检测、重复检测、
隐藏匹配记录（缩小范围作弊）检测、摘要/叶/步骤链篡改检测、增量撤回的
分子分母摘要同步更新、多次撤回、伪造 delta 检测、过期摘要检测。

## 文件

- `proofpack/commitments.py` — 有序 Merkle 承诺树、覆盖子树摘要、树根重建
- `proofpack/model.py` — 事实/规则/聚合模型（稳定 ID + 版本，Fraction 精确均值）
- `proofpack/prover.py` — 证明包构建、增量撤回、哈希步骤链
- `proofpack/verifier.py` — 11 项检查的验证器（重放 + 直接重算对照）
- `run_sample.py` — 合成数据端到端样例
- `tests/test_proofpack.py` — 21 个 unittest 用例
