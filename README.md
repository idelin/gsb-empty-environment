# migsched — 非交换迁移变更排序

数据库迁移以**抽象读集合、写集合、显式前置依赖**表示。两个迁移的读写或
写写集合相交（任一方向）时，必须保持其原始相对顺序；其余情况可并行。
所有迁移单位工时，每轮至多 `k` 项，目标是生成满足约束的**最少轮次**计划。

- 精确小例解：状态空间 BFS 精确搜索（利用“无空闲支配”剪枝），证明最优。
- 可行调度证据：内置校验器逐项检查“恰好调度一次 / 容量 / 前置约束”，
  结果随计划一并输出。
- 预算中断上下界：精确搜索超出状态扩展预算时，报告已证明的下界
  （已完整排除的 BFS 层数 + 1，与静态下界取大）与启发式可行调度给出的上界。

仅使用 Python 3.11 标准库，所有示例数据均为合成数据。

## 运行环境

Python 3.11（`python3.11`）。无第三方依赖。

## 命令行用法

```bash
# 运行验收示例（A 写 x，B 读 x，C 写 y，原序 A B C，k=2）
python3.11 -m migsched demo

# 从文件生成计划（k 可来自文件或用 --k 覆盖）
python3.11 -m migsched plan examples/acceptance.json
python3.11 -m migsched plan spec.json --k 4 --exact-limit 16 --budget 200000
cat spec.json | python3.11 -m migsched plan - --k 2

# 校验一个已有计划（退出码 0 可行 / 1 不可行 / 2 输入错误）
python3.11 -m migsched verify spec.json plan.json
```

## 输入格式（plan / verify 的第一个参数）

```json
{
  "k": 2,
  "migrations": [
    {"name": "A", "reads": [], "writes": ["x"]},
    {"name": "B", "reads": ["x"], "writes": [], "deps": ["A"]}
  ]
}
```

- `migrations` 数组顺序即**原始相对顺序**。
- `reads` / `writes`：抽象键集合（字符串数组），可省略，默认空。
- `deps`：显式前置依赖（迁移名数组），可省略；被依赖者必须排在更早的轮次。
- `k`：每轮容量上限；命令行 `--k` 优先于文件中的值。

冲突规则：对原始顺序中 `i` 在 `j` 之前的两个迁移，若
`writes(i) ∩ (reads(j) ∪ writes(j)) ≠ ∅` 或
`writes(j) ∩ (reads(i) ∪ writes(i)) ≠ ∅`，则 `i` 必须排在 `j` 之前
（读读相交不冲突）。显式依赖与冲突边合并后若构成循环，报错退出（码 2）。

## 输出格式（plan）

```json
{
  "status": "optimal",
  "optimal": true,
  "k": 2,
  "num_migrations": 3,
  "rounds": [["A", "C"], ["B"]],
  "num_rounds": 2,
  "lower_bound": 2,
  "upper_bound": 2,
  "gap": 0,
  "states_expanded": 4,
  "conflict_edges": [["A", "B"]],
  "explicit_edges": [],
  "critical_path_rounds": 2,
  "verification": {"ok": true, "errors": [], "checked_precedence_edges": 1,
                   "scheduled": 3, "total": 3},
  "notes": ["精确搜索完成，轮次数已证明最优。"]
}
```

- `status`：`optimal`（精确证明）/ `interrupted`（预算中断，未证最优）/
  `heuristic`（规模超限走启发式，未证最优）。
- `rounds`：第 r 轮执行的迁移名列表；同轮内可并行，跨轮严格先后。
- `lower_bound` / `upper_bound`：轮次数的已证明下界与可行上界；
  `gap = upper_bound - lower_bound`。
- `verification`：可行调度证据（恰好一次、容量、全部前置边逐条检查）。

`verify` 子命令接受 `plan` 的输出（或任何含 `rounds` 字段的 JSON），
输出校验证据并以退出码报告结论。

## 算法与界

1. **前置图**：显式依赖 ∪ 冲突顺序边，拓扑检测循环。
2. **静态下界**：`max(ceil(n/k), 关键路径长度)`。
3. **精确搜索**：以“已完成集合”为状态做按轮 BFS；利用无空闲支配性质，
   每轮只枚举 `min(k, 就绪数)` 大小的子集。首个到达全集的层数即最优轮次。
   该问题等价于 `P|prec, p_j=1|C_max`，一般情形 NP-hard，故精确求解默认
   限 `n ≤ 16` 且状态扩展预算 200000（均可调）。
4. **预算中断**：中断时已完整排除 `L` 层 ⇒ 下界 `L+1`（与静态下界取大）；
   上界由关键路径优先列表调度（同轮容量 `k`，就绪者优先长链）给出。
5. **间接最优**：若启发式上界与下界吻合，则轮次数同样被证明最优，
   输出中 `optimal` 为 `true` 并在 `notes` 说明。

## 验收边界示例

A 写 x，B 读 x，C 写 y，原序 A B C，k=2：

- A 与 B 读写相交于 x ⇒ A 必须早于 B，**不得同轮**；
- C 与 A、B 均不相交 ⇒ 可与 A 同轮；
- 最优计划：第一轮 `A C`，第二轮 `B`（共 2 轮，下界=关键路径 2，最优得证）。

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

覆盖：验收边界、四类读写冲突组合、显式依赖、循环检测、容量边界、
精确解与启发式在随机小例上的一致性、预算中断上下界、CLI 端到端。
