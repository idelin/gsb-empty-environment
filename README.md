# equivstats：等价类敏感值不同数与熵指标

在**合成数据**上按等价类计算两种敏感性指标（不同数、熵），支持显式 null
策略、阈值判定（含浮点误差区间与未决态）、增量删除、可撤回的层级合并、
独立重算校验与最少抑制记录搜索。

> **免责声明**：本项目仅用于合成数据上的统计实验与算法演示，所有输出
> **不构成任何形式化隐私证明**（非差分隐私、非形式化 l-多样性证明）。

## 定义

- **等价类**：准标识符 `qid` 元组相同的记录集合。
- **不同数（distinct count）**：等价类内按 null 策略过滤后的敏感值类别数。
  同一值出现任意多次（如 10 次）仍只计 **1** 种。
- **熵（entropy）**：`H = -Σ p_i · log_b(p_i)`，其中 `p_i = c_i / n`，
  `b` 为配置的对数底 `log_base`；空类（过滤后无值）熵定义为 0。
- **null 策略（显式配置，`NullPolicy`）**：
  - `IGNORE`：`None` 不计入类别，也不计入熵的总数 `n`；
  - `AS_CATEGORY`：`None` 视为独立类别参与不同数与熵。
  - 全 null 的类是否计为类别完全由该配置决定。
- **阈值判定（`Verdict`）**：要求 `不同数 ≥ min_distinct` 且 `H ≥ min_entropy`。
  熵用 `decimal` 在两种精度下计算并加保守 slack 得到误差区间 `[lo, hi]`：
  - `lo ≥ 阈值` → `PASS`；`hi < 阈值` → `FAIL`；否则 → `UNDECIDED`（未决）。
  - 未决时精度可加倍提升（可加精度），达到 `max_precision` 仍未决则返回
    `UNDECIDED`。该误差区间是工程数值界，非形式化证明。
- **层级合并与撤回**：`Dataset.generalize(mapping)` 合并敏感值计数，原始
  贡献压入历史栈；`Dataset.rollback()` 逐条恢复原贡献。
- **抑制目标**：删除最少记录使所有等价类达标。删除不会增加不同数，故不同数
  不达标的类只能整类抑制；熵不达标可通过删除主导值记录改善。判定中
  `UNDECIDED` 按不达标处理（保守）。

## 参数

| 参数 | 含义 | 默认 |
|---|---|---|
| `null_policy` | `IGNORE` / `AS_CATEGORY` | `IGNORE` |
| `log_base` | 熵的对数底 | `2` |
| `min_distinct` | 不同数阈值（含） | 调用方指定 |
| `min_entropy` | 熵阈值（含） | 调用方指定 |
| `precision` / `max_precision` | 熵区间起始/最高十进制精度 | 28 / 120 |
| `max_nodes` | 枚举抑制搜索的节点上限 | 500000 |

## 模块

- `equivstats/core.py` — 记录、等价类计数、增量删除、层级合并与撤回
- `equivstats/metrics.py` — 不同数、熵误差区间、阈值判定、失败类报告（含各项贡献）
- `equivstats/hierarchy.py` — 敏感值层级（child→parent）映射生成
- `equivstats/suppression.py` — 小域枚举最少抑制 + 贪心启发式
- `equivstats/verify.py` — 独立重算校验：增量计数、重复值、null、分片合并

## 运行

```sh
python3.11 demo.py                        # 运行样例（合成数据）
python3.11 -m unittest discover -s tests  # 测试（31 例）
```

仅依赖 Python 3.11 标准库与 unittest。
