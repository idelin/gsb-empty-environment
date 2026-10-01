# 稀疏区间冲突修复（sparse_intervals）

Python 3.11 标准库实现，无第三方依赖。所有示例数据均为合成数据。

- 一维：多条带优先级/版本/半开时间区间的配置冲突修复，输出最少段数的规范时间线；
  支持增量撤销并返回受影响区间；可输出每段被遮蔽规则列表。
- 二维：每条配置同时受业务时间区间与系统版本时间区间约束，查询点为 (t, v)；
  输出覆盖整个有限查询矩形的规范、不重叠矩形分区；
  提供小网格精确最小矩形覆盖求解器、分区独立验证器与预算中断界；
  撤销规则仅失效相关区域，并以全量网格枚举逐单元、逐端点对照。

## 规则选择顺序（一维与二维一致）

同一位置多条配置覆盖时，生效配置为：**优先级最高 → 版本最高 → ID 最小**。

## 运行

```bash
# 一维规范时间线
python3 -m sparse_intervals timeline -i examples/rules1d.json --explain
python3 -m sparse_intervals timeline -i examples/rules1d.json --revoke c --revoke a

# 二维矩形分区
python3 -m sparse_intervals rect -i examples/rules2d.json --exact --verify
python3 -m sparse_intervals rect -i examples/rules2d.json --revoke hot2 --dump-grid

# 测试
python3 -m unittest discover -s tests -v
```

`-i/--input` 缺省时从 stdin 读取 JSON。

## 输入格式

### timeline

```json
{
  "rules": [
    {"id": "a", "priority": 1, "version": 1, "start": 0, "end": 5, "value": "X"}
  ]
}
```

- `start`/`end`：半开区间 `[start, end)`，数值（int/float）。
- `value`：任意 JSON 值；同值相邻段会合并。

### rect

```json
{
  "query": {"t0": 0, "t1": 10, "v0": 0, "v1": 4},
  "rules": [
    {"id": "r1", "priority": 2, "version": 1,
     "t0": 0, "t1": 6, "v0": 0, "v1": 3, "value": "A"}
  ]
}
```

- `query`：有限查询矩形，`t ∈ [t0, t1)`、`v ∈ [v0, v1)`。
- 每条规则同时受业务时间区间 `[t0, t1)` 与系统版本时间区间 `[v0, v1)` 约束（均半开）。

## 输出格式

### timeline

```json
{
  "segments": [{"start": 0, "end": 10, "value": "X",
                "winners": ["a", "b"], "shadowed": ["d"]}],
  "revocations": [{"rid": "a",
                   "affected": [{"start": 0, "end": 5,
                                 "old_value": "X", "new_value": null}],
                   "segments": []}]
}
```

- `segments`：规范时间线。同值相邻段已合并，段数对生效值时间线而言**最少**（可证明：
  段数 = 生效值变化次数 + 1）。无覆盖位置以 `value: null` 段出现在内部空隙。
- `winners`（`--explain`）：在该段某处胜出的规则 ID。
- `shadowed`（`--explain`）：覆盖该段某处但未在该处胜出的规则 ID（被遮蔽规则）。
- `affected`：撤销引起的生效值变化区间，含变化前后值；区间外保证无变化。
- 撤销为增量更新：仅在被撤销规则的区间内局部重算，窗外缓存段保留。

### rect

```json
{
  "rects": [{"t0": 0, "v0": 0, "t1": 10, "v1": 1, "value": "BASE"}],
  "rect_count": 7,
  "exact": {"rects": [], "rect_count": 7, "status": "optimal",
            "nodes": 22, "budget": 200000, "lower_bound_components": 4},
  "verification": {"errors": []},
  "revocations": [{"rid": "hot2",
                   "affected": [{"t0": 3, "v0": 1, "t1": 5, "v1": 3,
                                 "old_value": "HOT2", "new_value": "HOT"}],
                   "verification": {"checked_cells": 15,
                                    "checked_points": 24,
                                    "mismatches": []}}]
}
```

- `rects`：覆盖整个查询矩形的规范、不重叠矩形分区（坐标压缩 + 同值相邻合并）。
  带 `--revoke` 时输出撤销后的最终分区。
- `exact`（`--exact`）：小网格精确最小矩形覆盖。
  - `status: "optimal"`：已证明全局最少；
  - `status: "budget_exceeded"`：达到 `--budget` 搜索节点上限被中断，
    返回当前最优上界，**不保证最少**；
  - `lower_bound_components`：同值连通分量数，任何分区的矩形数下界。
- `verification`（`--verify`）：独立验证器结果，`errors` 为空即通过。
- 撤销的 `verification`：增量结果与全量网格枚举的对照，
  逐**单元**（网格中点）与逐**端点**（压缩坐标顶点）比较，`mismatches` 为空即一致。

## 最优性声明（重要）

- 一维时间线段数：可证明最少（同值相邻段全部合并）。
- 二维贪心分区（默认 `rects`）：局部贪心条带合并，**不宣称全局最少**；
  存在反例（测试 `test_greedy_not_always_optimal`：贪心 7 矩形，精确最小 6）。
- 二维精确求解器（`--exact`）：基于 Exact Cover 搜索，仅适合小网格；
  `status == "optimal"` 时矩形数可证明最少；预算中断时明确不保证最优。

## 代码结构

- `sparse_intervals/timeline1d.py`：一维规则、规范时间线、增量撤销。
- `sparse_intervals/rect2d.py`：二维规则、矩形分区引擎、增量撤销 + 全量对照。
- `sparse_intervals/solver.py`：贪心合并、精确最小覆盖、下界、两类独立验证器。
- `sparse_intervals/cli.py`：命令行入口。
- `tests/`：unittest（合成数据，固定随机种子）。
- `examples/`：合成示例输入。
