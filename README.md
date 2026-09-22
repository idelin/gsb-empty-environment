# occupancy-peak

从可能乱序的 start/end 记录重建设备占用的半开区间，并查询峰值并发与所有达到峰值的时间段。纯标准库实现，不接外部数据库。

## 运行

```bash
python3 demo.py                 # 连贯演示：配对 → 索引更新 → 峰值查询
python3 -m pytest tests/ -q     # 自动测试（含逐端点扫描的暴力核验）
```

## 记录语义

每个占用有独立 ID，两个端点可以任意顺序到达。所有变更返回明确的 `Result`：

| 操作 | 结果 |
| --- | --- |
| `record_start/record_end` | `RECORDED`（孤立暂存）/ `PAIRED`（配对并入索引）/ `INVALID_PAIR`（end ≤ start，只记录不入索引）/ `DUPLICATE`（同值重复，忽略）/ `CONFLICT`（异值冲突，须显式更正） |
| `correct_start/correct_end` | `CORRECTED` / `INVALID`（会产生 end ≤ start，拒绝且原状态不变）/ `NOT_FOUND` |
| `retract` | `RETRACTED`（区间贡献整体撤销）/ `NOT_FOUND` |

- 配对成功后形成**半开区间** `[start, end)`：`[0,5)` 与 `[5,9)` 在交界处不重叠。
- 孤立记录通过 `orphans()` 单列（`waiting_end` / `waiting_start` / `invalid`），**不补零、不产生虚假占用**。
- 无效 end（早于 start）绝不进入索引；对已有区间的非法更正被整体拒绝，此前状态不受影响。
- 查询 `peak(lo, hi)` 返回 `PeakResult(peak, periods)`，每个 `PeakPeriod` 携带 `occupancy_ids`，证据可回溯到贡献的占用 ID。

## 工程实现：如何避免每次更新重扫全部区间

核心是一个**差分（delta）索引**而不是区间列表扫描：

- **更新 O(log K)**（K = 不同端点时刻数）：区间建立/撤销/更正只做两次端点增量
  `delta[start] += 1, delta[end] -= 1`（撤销取反）。更正 = 原子地"撤销旧贡献 + 加入新贡献"，
  旧区间的贡献被精确冲销，不会残留。端点时刻维护在有序数组上（`bisect` 插入）。
- **查询只扫范围内的端点事件**：`peak(lo, hi)` 从 `lo` 处的初始活跃集合出发，
  只遍历 `(lo, hi)` 内发生变化的端点时刻，按"先 end 后 start"应用半开语义，
  逐段统计并发数并收集达到峰值的极大时间段。代价是 O(范围内端点数)，与总区间数无关。
- **证据回溯**：每个端点时刻维护 `time -> {占用ID}` 的映射，扫描时同步维护活跃 ID 集合，
  因此每个峰值时间段都能给出确切的贡献 ID 集合。
- **初始活跃集合**通过在按 start 排序的候选中做 stabbing 过滤得到；
  若区间规模继续增长，可替换为区间树/线段树把这一步降到 O(log n + k)，
  差分索引与查询框架无需改动。

## 正确性核验

`tests/test_occupancy.py` 中的 `brute_peak` 是独立的参考实现：把查询范围在所有端点处切开，
对每个基本段用中点做逐区间计数。随机化测试（300 个混合操作 + 50 个随机查询范围）
将工程实现与该暴力扫描逐一比对，并校验差分索引与活跃区间集合始终一致。

## 结构

- `occupancy/tracker.py` — 配对状态机、差分索引、峰值查询
- `demo.py` — 可运行样例（乱序配对、重叠/撤回、更正移出范围、无效 end、重复 end、孤立记录）
- `tests/test_occupancy.py` — 自动测试
