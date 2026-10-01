# k_anonymizer

有限泛化树上的 k-匿名全局层级组合搜索。**仅处理合成数据**；本仓库中的统计实验
结果用于演示算法行为，**不构成任何形式化隐私证明**（详见文末免责声明）。

仅依赖 Python 3.11 标准库，测试使用 `unittest`。

## 定义

- **字段层级（有限泛化树/DAG）**：每个准标识符字段一棵有限层级。节点以字符串
  唯一命名；`parents` 边允许构成 DAG（一个节点可有多个父节点）。叶子为原始取值，
  层级 0；内部节点层级 = 1 + 子节点最大层级。`generalize(value, L)` 返回 `value`
  在层级 `L` 的唯一祖先；**不同泛化路径收敛到同一节点时按集合去重**，若某层仍
  存在多个不同祖先（歧义）则构造时直接报错。
- **层级组合**：向量 `(l_1, ..., l_d)`，`l_i ∈ [0, max_level_i]`，表示第 `i` 个
  字段统一泛化到层级 `l_i`（full-domain generalization）。
- **等价类**：泛化后准标识符元组相同的记录集合。
- **k 约束**：每个等价类大小 `≥ k`。
- **损失（归一化 LM）**：单字段单记录
  `field_loss = (leaves_under(node) - 1) / (total_leaves - 1)`（叶子为 0，根为 1）；
  组合损失为全部记录、全部字段的算术平均。损失关于层级单调不减。

## 搜索模式与状态语义

- `exact`：穷举全部层级组合，小规模精确求解。
- `beam`：从全原始层 `(0,...,0)` 出发按损失升序扩展，受 `--beam-width`（前沿
  宽度）与 `--budget`（状态评估预算）截断；不同扩展路径收敛到同一状态时用
  `visited` 集合去重；利用损失单调性做下界剪枝。

| 状态 | 含义 |
|---|---|
| `OPTIMAL` | 已搜完全空间，返回全局最优可行解 |
| `INFEASIBLE` | 已搜完全空间，**不存在**可行解 |
| `FEASIBLE_TRUNCATED` | 预算截断：找到可行解但未搜完，不保证最优 |
| `NO_SOLUTION_TRUNCATED` | 预算截断：未找到可行解且未搜完（**不等于不存在**） |

“找不到”（`INFEASIBLE`）与“未搜完”（`*_TRUNCATED`）严格分开；`beam` 模式
若实际评估数覆盖全空间，会自动升级为 `OPTIMAL` / `INFEASIBLE`。

## 参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `--n` | 200 | 合成记录数 |
| `--k` | 5 | k-匿名约束 |
| `--seed` | 42 | 合成数据随机种子（同种子结果确定） |
| `--mode` | exact | `exact` 精确穷举 / `beam` 预算截断 |
| `--beam-width` | 4 | beam 前沿宽度 |
| `--budget` | 10000 | beam 状态评估预算 |

## 输出

JSON 报告包含：状态、最优层级组合、损失、已评估状态数 / 全空间大小、是否搜完，
以及**可验证的等价类计数**（各类大小列表、类数、最小类大小、总和、是否满足 k）。
`verify_report` 会从原始记录独立重算等价类并核对报告（CLI 输出中的
`"verified": true` 即该校验通过）。

## 运行

```bash
python3.11 -m k_anonymizer --mode exact --n 200 --k 5 --seed 42
python3.11 -m k_anonymizer --mode beam --n 200 --k 5 --seed 42 --beam-width 2 --budget 30
```

## 运行样例

精确模式（`--mode exact --n 200 --k 5 --seed 42`）：

```json
{
  "status": "OPTIMAL",
  "levels": [3, 1, 1, 0],
  "loss": 0.5775862068965542,
  "explored_states": 120,
  "search_space_size": 120,
  "search_exhausted": true,
  "equivalence_classes": {
    "num_classes": 12,
    "sizes": [25, 23, 19, 19, 17, 17, 16, 13, 13, 13, 13, 12],
    "min_size": 12,
    "sum_sizes": 200,
    "k_satisfied": true
  },
  "verified": true
}
```

截断模式（`--mode beam --beam-width 2 --budget 30`）：`FEASIBLE_TRUNCATED`，
评估 19/120 状态，损失 0.75（劣于精确解 0.578，符合“截断不保证最优”）。
极小预算（`--budget 3`）：`NO_SOLUTION_TRUNCATED`。`k > n`：`INFEASIBLE`。

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

结果：`Ran 20 tests ... OK`（20/20 通过）。覆盖：树/DAG 层级泛化、收敛路径
去重、歧义与环的拒绝、损失边界、手工可验证的最优解、不可行判定、beam 与
exact 一致性、截断状态、报告可验证性与篡改检测、合成数据确定性。

## 目录结构

```
k_anonymizer/
  hierarchy.py   # 有限泛化层级（DAG、收敛去重、歧义校验）
  loss.py        # 归一化 LM 损失
  dataset.py     # 合成数据与演示层级（含 DAG 钻石结构示例）
  search.py      # exact 穷举 + beam 预算截断搜索、等价类计算
  report.py      # 可验证报告构建与独立核验
  __main__.py    # CLI
tests/           # unittest 测试
```

## 免责声明

本工具仅用于算法演示与教学：所有数据均为随机合成，不对应任何真实个人；
k-匿名本身不能防御同质性攻击、背景知识攻击等；在合成数据上的统计实验
**不构成形式化隐私证明**，不得作为真实数据发布的隐私保证依据。
