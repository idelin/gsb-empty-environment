# 对账键冲突解释器 (Reconciliation Key-Conflict Interpreter)

纯 Python 3.11 标准库实现，无第三方依赖。包含两个引擎：

1. **onetoone** — 严格一对一匹配（二分图）。
2. **splitmerge** — 一对多 / 多对一拆并账匹配（精确集合打包，**不是**普通二分图匹配）。

所有示例数据（`examples/`）均为合成数据。

## 运行

```bash
python3.11 -m recon onetoone   examples/onetoone_key_conflict.json --pretty
python3.11 -m recon splitmerge examples/splitmerge_demo.json --pretty
python3.11 -m recon splitmerge INPUT.json --budget 1000   # 节点预算中断
python3.11 -m unittest discover -s tests -v               # 运行测试
```

## 输入格式（JSON）

```json
{
  "window": 300,
  "left":  [{"id": "a", "keys": ["x", "y"], "amount": "100.00", "time": 1000}],
  "right": [{"id": "r1", "keys": ["x"], "amount": "100.00", "time": 1010}]
}
```

- `window`：一对一匹配允许的最大时差（秒，整数）。拆并账引擎不用 window 约束组的可行性，时差只进入目标函数。
- `id`：同一侧内唯一。`keys`：候选业务键集合。`amount`：字符串形式的十进制数（内部用 `Decimal` 精确比较）。`time`：整数时间戳。

## 一对一引擎（onetoone）

**可行性**：左右行共享至少一个候选键、金额精确相等、`|Δt| ≤ window`；每行最多使用一次。

**目标（字典序）**：① 最大化匹配对数；② 最小化总时差；③ 配对 ID 列表字典序最小。

**输出**（JSON）：

- `objective`：`matched_count`、`total_time_diff`、`pair_ids`。
- `pairs`：每对的左右 ID、金额、时差、共享键。
- `unmatched.left/right`：每个未匹配行：
  - `reason: "no_candidates"` — 没有任何可行对手行；
  - `reason: "competition"` — 有候选但被全局竞争挤掉，附增广路径证据：
    从该行出发的交错搜索到达的两侧行集合与 Hall 亏数
    （`hall_deficiency ≥ 1`），`augmenting_path_found: false`。
- `optimality`：
  - `max_cardinality`：Berge 引理 —— 从所有自由行出发的交错搜索均无增广路径，附 Hall 见证集；
  - `min_cost`：对偶可行性 —— 所有残余边在返回势函数下约化成本非负（`dual_feasible`）；
  - `lexicographic`：按 ID 顺序贪心强制，独立重算一致（`recomputed_equal`）。

**验收边界**：`examples/onetoone_key_conflict.json` 中左 a 候选 {x, y}、左 b 只候选 {x}，
最优为 `a↔r_y, b↔r_x`；若 a 贪心选 `r_x`，b 将无配（测试 `test_greedy_would_lose_b` 验证）。

## 拆并账引擎（splitmerge）

**组可行性**：1v1、1v2 或 2v1（每组最多 3 行，两侧均至少一行）；组内所有行共享至少一个业务键；
左侧金额之和与右侧金额之和**严格相等**（Decimal 精确比较）；每行最多出现在一个组。

**目标（字典序）**：① 最大化匹配金额绝对值总和（按行计）；② 最大化匹配行数；
③ 最小化各组组内最大时差之和；④ 规范组 ID（`L:..|R:..`，行 ID 排序）列表字典序最小。

**求解**：枚举全部可行组后做精确分支定界集合打包；上界取"剩余组后缀和"与
"剩余未用行自身金额和"的较小者。

- `--budget N`：节点预算。耗尽时输出 `search.status = "budget_exceeded"`、
  `proven_optimal = false`，并给出 `lower_bound`（当前最优可行解）与
  `upper_bound`（搜索前沿最乐观界）。完成时 `status = "proven_optimal"`。
- `cross_check`：独立穷举所有不交集组合的暴力对照，`objective_agrees` /
  `solution_agrees` 双确认（可用 `--no-cross-check` 关闭）。
- `unmatched.*[].forced_inclusion`：对每个未匹配行，强制纳入后的最优目标损失：
  - `status: "infeasible"` — 该行不属于任何可行组（完全不可行）；
  - `status: "equal_alternative"` — 存在同优替代（损失为零）；
  - `status: "loss"` — 附 `amount_abs_loss` / `row_loss` / `spread_increase`；
  - `status: "unverified"` — 主搜索被预算中断，无已证最优可对照
    （此时 `verified_against_proven_optimum: false`）。

## 项目结构

- `recon/model.py` — 数据模型与输入加载
- `recon/onetoone.py` — 一对一引擎（最小费用最大流 + 字典序强制 + 最优性证据）
- `recon/splitmerge.py` — 拆并账引擎（分支定界集合打包 + 预算中断 + 穷举对照 + 强制纳入分析）
- `recon/cli.py`、`recon/__main__.py` — 命令行入口
- `tests/` — unittest 测试（27 个）
- `examples/` — 合成示例数据
