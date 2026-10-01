# 非交换迁移变更排序调度器

数据库迁移用**抽象读集合、写集合、显式前置依赖**表示。两个迁移的读/写、
写/读或写/写集合相交时（读/读不相交不算冲突），必须保持原始相对顺序
（非交换）；其余迁移可并行。给定每轮并行上限 `k`，生成满足约束且
**轮次最少**（单位工时）的执行计划，并给出可行性证据与预算中断上下界。

仅依赖 Python 3.11 标准库，无第三方包。

## 快速开始

```bash
# 验收示例：A 写 x，B 读 x，C 写 y，原序 A B C，k=2
python3 migration_scheduler.py examples/acceptance.json
# => rounds: [["A", "C"], ["B"]]（第一轮 A C，第二轮 B；A、B 不同轮）

# 预算中断：只有 1 轮预算时，输出可完成迁移数的下界/上界/精确值
python3 migration_scheduler.py examples/acceptance.json --budget 1

# 从标准输入读取，并用 --k 覆盖输入中的 k
python3 migration_scheduler.py - --k 2 < examples/acceptance.json

# 运行测试
python3 -m unittest test_migration_scheduler -v
```

## 输入格式（JSON）

```json
{
  "k": 2,
  "migrations": [
    {"name": "A", "reads": [], "writes": ["x"], "deps": []},
    {"name": "B", "reads": ["x"]},
    {"name": "C", "writes": ["y"]}
  ]
}
```

| 字段 | 含义 |
| --- | --- |
| `k` | 每轮最多执行的迁移数（>=1），可被 `--k` 覆盖 |
| `name` | 迁移唯一名称（必填）；数组顺序即原始相对顺序 |
| `reads` / `writes` | 抽象读/写集合（字符串数组，可省略，默认空） |
| `deps` | 显式前置依赖名称数组（可省略，默认空） |

约束规则：对原始顺序 `i < j`，若
`writes(i) ∩ (reads(j) ∪ writes(j)) ≠ ∅` 或 `reads(i) ∩ writes(j) ≠ ∅`，
则 `i` 必须排在 `j` 之前的轮次；`deps` 中的边同样强制。依赖成环、
未知依赖、重名会以退出码 2 报错。

## 输出格式（JSON，标准输出）

| 字段 | 含义 |
| --- | --- |
| `rounds` | 每轮执行的迁移名列表，即执行计划 |
| `round_count` | 总轮次 |
| `precedence_edges` | 全部前置边（冲突保序 + 显式依赖） |
| `lower_bound` | 轮次下界 `max(ceil(n/k), 关键路径长度)` |
| `optimal` / `optimality_note` | 是否经精确搜索证明最优；超限时明确标注“未证最优” |
| `greedy_round_count` | 列表调度（贪心）轮次，作为对照 |
| `evidence` | 可行调度证据：逐项检查（每迁移恰好一次、轮规模 ≤ k、前置约束全部满足）及每轮就绪集合 |
| `budget` | 加 `--budget T` 时输出：T 轮内可完成数的 `lower_completed`（贪心达成）、`upper_completed`（容量与最早启动深度界）、`exact_max_completed`（精确最大值，超限时为 `null` 且 `exact=false`） |

## 算法说明

- **冲突图**：对每对原始顺序 `i < j` 检查读写/写读/写写相交，生成前置边，
  与显式 `deps` 合并成 DAG（拓扑排序检测环）。
- **精确解**：位掩码状态 + 记忆化搜索。单位工时下存在最优计划每轮取满
  `min(k, |ready|)` 个就绪任务（就绪未选者必在更晚轮次，提前不劣），
  故只枚举该大小的子集。默认迁移数 ≤ 20 时启用（`--exact-limit` 可调，
  `--no-exact` 关闭）。
- **贪心对照**：每轮按原始顺序取前 `k` 个就绪任务（列表调度）。
- **预算中断**：`--budget T` 时，下界为贪心 T 轮完成数；上界为
  `min(n, k·T, 深度 < T 的任务数)`；小规模另给逐轮 BFS 的精确最大值。

## 示例

- `examples/acceptance.json`：验收边界用例，k=2 得 `[["A","C"],["B"]]`。
- `examples/greedy_gap.json`：合成用例，贪心 4 轮而精确最优 3 轮，
  演示精确搜索的价值。

所有示例数据均为合成数据。
