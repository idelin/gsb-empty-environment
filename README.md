# bounded_sampler：精确有界推导采样（Python 3.11 标准库）

对带非负有理权重的上下文无关文法，在「终结串长度 ≤ L、产生式应用次数 ≤ D」
的有限域上，对**完整推导树**按权重乘积做精确归一化采样。每次产生式应用
（包括 epsilon 产生式与 `A→A` 自循环）都消耗一步预算，因此循环文法、epsilon
循环在该有限域内只有有限多棵推导树，动态规划是良定义的（第 d 层只依赖 < d 层）。

全程不引入浮点：有理权重先通分为整数（`w_p = 整数 / Q`，Q 为所有分母的最小公倍数），
配分函数用大整数计算；`d` 步树的精确值为 `Z / Q**d`。随机性只通过调用方提供的
均匀随机整数源 `randbelow(n)` 注入，每次决策用一次均匀抽取与大整数质量比较完成，
无拒绝采样，因此不存在无限等待。

## 目标分布

- 一棵完整推导树的权重 = 所用产生式权重之乘积（`Fraction` 精确值）。
- 只保留 yield 长度 ≤ L 且步数 ≤ D 的树，按总权重归一化。
- 同一字符串由多棵树生成时，字符串概率 = 各树权重**之和**（见
  `string_mass`）；采样按树进行、不去重，自然与该和一致，不声称均匀字符串采样。
- 组条件（"某组产生式至少使用一次"）通过 DP 状态中的组位掩码（精确并集语义）
  进入配分函数与采样，条件分布直接采样，不做拒绝采样。

## 文件

- `bounded_sampler/grammar.py` — `Production`（lhs/rhs/weight/group）、`Grammar`
- `bounded_sampler/dp.py` — `PartitionTable`：精确配分量 `Z[nt, len, steps, mask]`，
  任意右部长度的预算卷积（逐产生式前缀表 + 精确步数切片），共享子问题；
  `ResourceBudget` / `BudgetExhaustedError`
- `bounded_sampler/sampler.py` — `BoundedSampler`：无偏精确采样；无可行树返回 `None`
- `bounded_sampler/parser.py` — `string_mass`：给定终结串的所有有界推导权重和（CYK 式）
- `bounded_sampler/enumerator.py` — 小文法全枚举参考实现（测试对照用）
- `bounded_sampler/validator.py` — 独立结构验证器（重算长度/步数/权重/组/产出，
  不信任树上缓存字段）
- `tests/test_exact.py` — 19 个 unittest；`demo.py` — 确定 seed 演示

## 用法

```python
import random
from bounded_sampler import (Grammar, Production, PartitionTable, BoundedSampler,
                             randbelow_from_random, string_mass, validate_tree)

g = Grammar.create("S", (
    Production("S", ("S", "S"), 2, group="struct"),
    Production("S", ("a",), 3, group="leaf"),
    Production("S", (), 1, group="leaf"),          # epsilon 产生式
))
table = PartitionTable(g, max_length=6, max_steps=9)
sampler = BoundedSampler(table, randbelow_from_random(random.Random(42)))

s = sampler.sample(length=3, require_groups=("struct",))  # 无可行树时为 None
s.tree, s.terminals, s.steps, s.probability               # 精确 Fraction 概率
validate_tree(s.tree, g, 6, 9).ok                         # 独立验证

table.total_mass()                                        # 总配分量（精确）
table.joint_partition()                                   # 各 (长度, 步数) 配分量
table.total_mass(require_mask=table.group_bit["struct"])  # 组条件总质量
string_mass(table, "aaa")                                 # 字符串概率质量（树权重求和）
```

运行：`python3.11 demo.py`；测试：`python3.11 -m unittest discover -s tests -v`

## 资源开销

- **状态数**：`O(|NT| · (L+1) · D · 2^G)`，G 为跟踪的组数（默认全部有标签的组；
  可用 `PartitionTable(..., groups=...)` 子集降低）。组条件以精确并集语义进入
  掩码，故条件查询不增加渐近状态数，但 G 本身指数于组数。
- **建表时间**：每层 d 对每个产生式做右部序列卷积；单条产生式（右部长 k）
  每层约 `O(k · D · S_pref · S_nt)` 次松弛（`S_pref`、`S_nt` 为前缀/子问题状态数），
  总计约 `O(Σ_p k_p · D² · L² · 4^G)` 量级——L、D、G 是主要成本轴。
- **大整数位长**：`d` 步树的整数质量约为 `Q^d × Πw_p`，位长随 `O(D · (log Q + max log w))`
  线性增长；大整数乘法使实际开销再乘上近乎线性的位长因子。
- **采样**：每棵树 `O(树节点数 × (候选产生式 × 合法预算切分数))`；切分枚举
  最坏 `O((L·D·2^G)^{k})`（k 为右部非终结符数），仅覆盖 DP 中非零状态。
- **额度不足即失败**：`ResourceBudget(max_ops=..., max_int_bits=...)` 超限抛出
  `BudgetExhaustedError`，计算整体中止——绝不返回截断后的部分分布冒充原分布。

## 验证方式

- 极小文法全枚举（`enumerator.py`，独立实现）对照 DP 的联合/条件配分量与逐掩码单元；
  覆盖 `A→A`、`A→AA`、`A→ε`、零权产生式、不可达非终结符、有理权重。
- 每个采样树由独立验证器结构校验，且 `probability == 树权重 / 精确总质量`。
- 歧义文法的字符串质量对照枚举逐字符串求和（如 `S→SS|a` 的 Catalan 数 1/2/5）。
- 统计频率（3000 次采样）仅作补充 sanity check，不作为分布正确性依据。
