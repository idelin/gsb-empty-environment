# kanonym：合成数据上的 k-匿名试算工具

Python 3.11，仅标准库，测试用 `unittest`。

> **适用范围与免责声明**
> - 仅处理**合成数据**；不应用于真实个人信息。
> - 本工具只评价下文**声明的指标**（k-匿名约束 + NCP/LM 信息损失），
>   **不宣称绝对匿名**，也**不宣称等同完整隐私保障**（不覆盖同质性攻击、
>   背景知识攻击、组合释放等问题）。
> - 所有统计实验结果**不作为形式化隐私证明**。

## 定义与参数

- **准标识符（QI）**：参与分组的属性，每个 QI 配一棵泛化层级树
  （`Hierarchy`，用 `child -> parent` 映射描述，根的父指向自身）。
- **泛化向量**：每个 QI 一个层级值的元组，如 `(1, 0, 2)`；
  level 表示从叶子向上爬的层数，到根为止。
- **等价类**：泛化向量下所有 QI 泛化值相同的记录集合。
  泛化后**总是从原始记录重新分组、重新计数**，不沿用旧计数。
- **类大小（k 的计数口径）**：类内**不同主体数**。同一主体有多条记录时，
  按题面约定**按主体计数**（只计一次），不按记录数。
- **不足 k 记录**：所在等价类主体数 < k 的记录（`below_k_records`）。
- **空值**：`None` 单独成类（标签 `∅(空值)`），任何泛化层级下都不与其他
  类别合并，NCP 权重恒为 `1 / (叶子数 + 1)`。
- **抑制**：删除记录。给定泛化向量下，最小抑制集 = 所有不足 k 类的全部
  记录（抑制只会缩小类，部分抑制无法使不足 k 的类达标）。
  参数 `sup_budget` 为允许抑制的最大记录数。
- **信息损失**（总量，越小越好）：
  `L = Σ_{保留记录} record_loss + sup_cost × 抑制记录数`
  - `metric="ncp"`：record_loss = 各 QI「泛化节点覆盖叶子数 / 总类别数」的均值；
  - `metric="lm"`：record_loss = 各 QI「level / max_level」的均值；
  - `sup_cost` 默认 1.0（≥ 任何单记录权重，保证下界成立）。

## 求解与最优性

- `solve_exact`：对泛化向量网格做**最佳优先精确枚举**（按下界堆排序）。
  小域上完整枚举即得**全局最优**（`status="optimal"`），并由测试与
  暴力枚举交叉核验。
- **预算中断**：`max_evaluations` 限制评估次数；超限时返回
  `status="budget_exceeded"` 及最优损失的**上下界**
  `[lower_bound, upper_bound]`。下界来自「不抑制时的泛化损失 ≤ 真实损失」，
  上界为已找到的最优可行解损失（尚未找到可行解时上界为 `None`）。
- 完整枚举仍无可行解时返回 `status="infeasible"`（已证明预算内无解）。

## 每记录归属证据

`build_evidence` 对**每条记录**给出：所属等价类的泛化键、类主体计数、
类记录数、是否满足 k、是否被抑制。

## 增量修复

`Anonymizer.add_records / remove_records` 后自动修复：
先用旧向量对**全量**数据重新评估，再整体重解。
所有等价类都重新分组、重新计数、重新校验 k-约束——
**不因局部组已达 k 而忽略其他组**（含未变更的组）。

## 运行

```bash
python3 examples/sample_run.py        # 运行样例（合成数据）
python3 -m unittest discover -s tests # 运行测试（23 个用例）
```

## 文件结构

- `kanonym/hierarchy.py`：泛化层级树、空值类别、NCP 权重
- `kanonym/dataset.py`：`Record` / `Dataset`
- `kanonym/solver.py`：分组、损失、评估、精确枚举与上下界、证据
- `kanonym/incremental.py`：`Anonymizer` 增量修复
- `examples/sample_run.py`：运行样例
- `tests/test_kanonym.py`：unittest 测试
