# cardest — 带可核验证书的基数分析器

纯 Python 3.11 标准库实现，不调用数据库、不调用求解器。
当统计信息不完整时，明确区分三种答案：

1. **已证明界限（proven bounds）**：搜索所有满足统计的联合分布后得到的
   数学上成立的查询行数区间 `[lower, upper]`，每个端点附带达到该值的
   具体关系实例（见证）。
2. **预算中断时的已证明界限**：界限仍覆盖所有尚未搜索的分支（由 DFS
   栈上每个开放节点的子树界限合并而来），并保留当前可行见证；此时
   `exact=false`，绝不声称已找到精确答案。
3. **启发式点估计**：基于独立性/均匀性假设的猜测，单独标注
   `is_proven_bound=false`。任何经验置信区间都不是数学界限。

## 输入规格（JSON）

```json
{
  "relations": {"R": {"columns": {"a": {"domain": [1, 2]},
                                  "b": {"domain": [1, 2]}}}},
  "statistics": [
    {"id": "R_rows",    "type": "row_count",  "relation": "R", "count": 4},
    {"id": "R_a_hist",  "type": "histogram",  "relation": "R", "column": "a",
     "counts": {"1": 2, "2": 2}},
    {"id": "R_b_nulls", "type": "null_count", "relation": "R", "column": "b", "count": 0},
    {"id": "R_ab_joint","type": "joint_count", "relation": "R", "columns": ["a", "b"],
     "counts": [{"values": [1, 1], "count": 2}, {"values": [2, 2], "count": 2}]}
  ],
  "query": {"type": "select", "from": "R",
            "where": {"op": "and", "args": [{"op": "eq", "column": "a", "value": 1},
                                            {"op": "eq", "column": "b", "value": 1}]}},
  "config": {"max_cells": 512, "node_budget": 200000}
}
```

- 每列是有限整数域；`null`（NULL）是每列隐含的额外取值。
- `histogram` 对域完整：未列出的域值计数为 0（NULL 由 `null_count` 单独给出）。
- `joint_count` 对所列列的非空值组合完整：未列出的非空组合计数为 0；
  含 NULL 的组合只在显式列出时受约束。
- 每条统计都是整数计数约束（单元格重数之和 == 目标值）。

## 查询子集

- `select`：单表 + `where`。
- `join`：二表等值内连接，`"on": [["R列", "S列"], ...]`（NULL 键永不匹配，
  重复键的连接重数完整保留：3 行 × 4 行 = 12 行）。
- 谓词：`eq`（等值）、`range`（闭区间，`low`/`high` 可省略表示无界）、
  `and` / `or` / `not`。NULL 采用 SQL 三值逻辑，`WHERE` 只保留 `TRUE`。
- join 查询的谓词列名用 `R.a` 形式限定。

## 用法

```bash
python3 -m cardest run examples/marginals_only.json            # 完整分析
python3 -m cardest run spec.json --budget 1000 --save-state s.json --out r.json
python3 -m cardest run spec.json --resume s.json --budget 100000   # 断点续搜
python3 -m cardest verify spec.json r.json                     # 核验证书
python3 -m cardest estimate spec.json                          # 启发式点估计
python3 -m cardest consistency spec.json                       # 只查相容性
python3 -m unittest discover -s tests -v                       # 测试
```

## 结果与证书

`run` 输出 JSON：

- `status`: `exact` | `budget_exhausted` | `inconsistent` | `domain_too_large`
  | `consistency_unknown`
- `bounds`: `{lower, upper, kind: "proven"}` —— 已证明界限
- `min` / `max`: 达到界限的见证（具体关系实例，单元格重数展开）
- `search`: `{nodes_explored, nodes_pruned}` —— 实际探索节点数与剪枝数
- `spec_fingerprint`: 规格哈希，证书锚点
- `conflict`: 不相容时的最小冲突子集（MUS），含“删除任意成员后即相容”
  的逐条核验记录
- `resume_state`: 预算耗尽时的可恢复搜索状态

**证书的失效**：`spec_fingerprint` 覆盖全部统计、域与查询。修改任何一项
统计后，`verify` 会报告 `stale`，`--resume` 会拒绝旧状态
（`StaleStateError`）——旧见证与旧界限必须重新推导，不能沿用失效证明。

## 架构

| 模块 | 职责 |
|---|---|
| `cardest/spec.py` | 规格加载/校验、规格指纹 |
| `cardest/query.py` | 谓词解析与 SQL 三值逻辑求值 |
| `cardest/constraints.py` | 统计 → 整数计数约束；相容性检查；MUS（删除法） |
| `cardest/search.py` | 分支定界、约束传播、可序列化/可恢复 DFS |
| `cardest/engine.py` | 组装分析流程，产出带证书的结果 |
| `cardest/witness.py` | 独立见证验证器（不导入搜索引擎） |
| `cardest/reference.py` | 独立暴力枚举器（不导入搜索引擎），用于交叉核验 |
| `cardest/estimate.py` | 启发式点估计（明确标注非界限） |
| `cardest/cli.py` | 命令行入口 |

### 搜索说明

- 单元格 = 关系列域（含 NULL）的笛卡尔积；每个单元格一个非负整数重数。
- 传播：约束剩余量为 0 时未赋值单元格归零；只剩一个未赋值单元格时
  强制取值；每个单元格的上界取其所有约束的最小剩余量。
- 目标界限：select 是线性目标（逐单元格精确上下界）；join 是双线性目标
  （下界只计完全确定的配对，上界忽略谓词取各键的重数上界乘积——两者
  都保守且单调，叶节点处收敛到精确值）。
- 剪枝：子树既不能改进当前最小值也不能改进当前最大值时剪掉，计入
  `nodes_pruned`。
- 预算中断时，已证明下界 = DFS 栈上所有开放帧的子树下界最小值，
  已证明上界 = 子树上界最大值，因此覆盖全部未搜索分支。

### 独立性

`witness.py`（见证验证器）与 `reference.py`（参考枚举器）都不导入
`cardest.search` / `cardest.engine` / `cardest.constraints`，各自独立
重新计数统计、独立实现三值逻辑求值。测试
`TestReferenceCrossCheck` 断言二者在所有样例上与生产搜索器结果一致，
并有专门测试防止参考程序意外引入对生产搜索器的依赖。

## 样例（examples/）

- `correlated.json` / `anti_correlated.json`：边缘直方图完全相同、列相关性
  相反的两张表；同一查询分别得到精确值 2 与 0。
- `marginals_only.json`：同样的边缘、去掉联合计数后只能证明 `[0, 2]`。
- `dup_key_join.json`：重复键连接，重数 3×4=12。
- `exclusive_predicates.json`：互斥谓词 `a=1 AND a=2` → 0。
- `all_null.json`：全 NULL 列，`b=1` 恒为 UNKNOWN → 0。
- `inconsistent.json`：行数 5 与直方图和 6 不相容 → 返回最小冲突子集。
- `join_filtered.json`：带过滤的连接，可证明区间 `[1, 3]`。
