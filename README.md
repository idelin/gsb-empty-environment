# 稀疏区间冲突修复（sparse-intervals）

纯 Python 3.11 标准库实现，无第三方依赖。所有示例数据均为合成数据。

## 语义

### 一维时间线（`sparse_intervals.core1d`）

- 配置：`id`、`key`、`priority`、`version`、半开区间 `[start, end)`、输出值 `value`。
- 同一 `key` 上多条配置重叠时，生效者选择顺序：**priority 高 → version 高 → id 小（字典序）**。
- 规范时间线按**输出值**合并相邻原子区间：同值相邻段必合并（即使生效配置不同），
  无覆盖区间不输出。该合并对“值函数”而言段数最少（每个值变化点都必须有边界）。
- `Timeline.revoke(id)` 仅在被撤销配置自身区间内做增量重算并返回受影响区间
  （`AffectedInterval`，含旧值/新值，`None` 表示无覆盖）；区间外的缓存段不动，
  受影响范围内不保留过期段。
- `Timeline.explain(key)` 输出每段的生效配置与被遮蔽规则列表（按强弱排序），
  用于解释为何生效。

### 二维矩形分区（`sparse_intervals.rect2d`）

- 配置同时受业务时间 `[t0, t1)` 与系统版本时间 `[v0, v1)` 约束；查询为有限矩形
  `[qt0, qt1) × [qv0, qv1)`。规则选择顺序与一维相同。
- 输出覆盖整个查询矩形的规范、不重叠矩形分区（含 `value=null` 的无覆盖区域）。
- **贪心分区**（`greedy_partition`）：行内条带 + 与上一行同键条带的垂直合并，
  是局部贪心，**不保证、也不宣称全局最少矩形数**。
- **精确最小覆盖**（`exact_min_partition`）：小网格状态压缩 DP，返回精确最少矩形数
  及矩形列表；`budget` 限制展开状态数，预算耗尽时返回 `optimal=false` 及
  `[lower_bound, upper_bound]` 中断界（下界 = 单元数 / 最大单色矩形面积，为弱下界；
  上界 = 贪心解）。
- **独立验证器**（`verify_partition`）：不复用生成器代码，逐单元校验不重叠、
  单色、恰好覆盖一次、边界对齐网格线。
- `RectTimeline.revoke(id)` 仅失效与被撤销矩形相交的网格单元并返回受影响区域；
  `audit()` 以全量网格枚举逐单元中心、逐端点（半开语义）对照暴力重算。

## 命令行

```bash
python3 -m sparse_intervals timeline INPUT.json [--explain]
python3 -m sparse_intervals revoke INPUT.json --config-id a [--config-id b ...]
python3 -m sparse_intervals rect INPUT.json --solver greedy|exact|both [--budget N]
python3 -m sparse_intervals rect-revoke INPUT.json --config-id a [...]
```

`INPUT` 为 JSON 文件路径，`-` 表示从 stdin 读取；输出均为 JSON（stdout）。

### 一维输入格式

```json
{
  "configs": [
    {"id": "a", "key": "rate", "priority": 1, "version": 1,
     "start": 0, "end": 5, "value": "x"}
  ]
}
```

`priority`/`version` 缺省为 0。输出：

- `timeline`：`{"segments": {key: [{"start","end","value"}...]}, "explain": {...}?}`
- `revoke`：`{"operations": [{"revoked", "affected": [{"key","start","end","old_value","new_value"}]}], "segments": {...}}`

### 二维输入格式

```json
{
  "query": {"t0": 0, "t1": 10, "v0": 0, "v1": 10},
  "configs": [
    {"id": "base", "priority": 1, "version": 1,
     "t0": 0, "t1": 10, "v0": 0, "v1": 10, "value": "base"}
  ]
}
```

输出：

- `rect`：`{"audit_errors": [], "greedy": {"count","rects","verify_errors"},
  "exact": {"optimal","count","lower_bound","upper_bound","nodes","rects"?,"verify_errors"?}}`
- `rect-revoke`：`{"operations": [{"revoked","affected":[{"x0","x1","y0","y1","old_value","new_value"}]}],
  "audit_errors": [], "partition": [...]}`

示例数据见 `examples/sample1d.json`、`examples/sample2d.json`（均为合成数据）。

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

覆盖：验收边界（同值 `[0,5)`+`[5,10)` 合并为一段；撤销 a 后仅剩 `[5,10)`）、
半开边界、优先级/版本/ID 次序、增量撤销受影响区间、被遮蔽列表、
精确解 vs 贪心（含贪心次优的已知实例）、预算中断界、独立验证器正反例、
随机撤销后与全量网格枚举逐单元逐端点对照。

## 最优性说明

- 一维规范时间线的段数对值函数是最少的（相邻同值必合并，值变化点必有边界）。
- 二维贪心分区**未证明、也不宣称**全局最少；精确最小值仅对小网格由
  `exact_min_partition` 在预算内给出（`optimal=true`）；预算中断时只给出
  `[lower_bound, upper_bound]` 区间，不声称最优。
