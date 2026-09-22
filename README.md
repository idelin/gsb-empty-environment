# session-windows

增量会话窗口库（Python 3.11+，零依赖）。把迟到事件、撤回和水位线
翻译成一条 **retract / upsert / finalize** 变更流，让下游可以无歧义地
维护物化结果，而不必每次拿全量窗口列表重算。

## 数据模型与语义

- 事件 `Event(source, seq, event_ms, value)`；`(source, seq)` 是事件的
  稳定标识，撤回 `Retract(source, seq)` 指向该标识。
- 同一 `source` 内，相邻事件间隔 `<= gap_ms` 即属于同一会话窗口。
- 同一 `(source, seq)` 重复投递且负载一致 → `DUPLICATE`（无变更）；
  负载不同 → `UPDATED`（等价于撤回旧记录再插入新记录）。
- 窗口快照 `Window` 是不可变值：`(source, window_id, start_ms, end_ms,
  members)`，成员按 `(event_ms, seq)` 排序。

## 窗口 ID 的延续规则

`window_id = "{source}:{首个成员的 event_ms}:{首个成员的 seq}"`，由内容
推导，与到达顺序无关：

- **合并**：合并后的窗口沿用“拥有全局最早成员”的那个输入窗口（或触发
  事件）的 id；被吞并窗口的 id 各发一条 `RETRACT`（携带最后快照）。
- **拆分**：最左一段保留原 id（其首成员未变），其余各段用各自首成员
  生成新 id；若被撤回的恰是首成员，则旧 id 消亡（`RETRACT`），各段都
  用新 id。
- 因此最终事件集合相同 ⇒ 窗口 id 与成员完全相同，与到达顺序无关
  （`tests/test_tracker.py::test_arrival_orders_converge` 验证全部 6 种
  排列）。

## 变更流契约

每个操作返回 `OpResult(disposition, changes)`；`changes` 是该操作的
**净变更**：每个 window_id 至多一条，按 id 排序。

- `UPSERT(window)`：该 id 的窗口现在的完整内容（新建或替换）。
- `RETRACT(window)`：该 id 已消失，携带最后快照，下游按键删除即可。
- `FINALIZE(window)`：水位线越过 `end_ms + gap + allowed_lateness`，
  窗口不再可能变化，状态已回收，携带最终快照。

`Disposition` 给出每条记录的明确处置：`APPLIED` / `UPDATED` /
`DUPLICATE` / `TOO_LATE`（落在 `watermark - allowed_lateness` 之外，
被拒绝）/ `NOT_FOUND`（撤回不存在或已回收的事件）/
`WATERMARK_ADVANCED` / `WATERMARK_IGNORED`（水位线回退，忽略）。

## API

```python
from session_windows import (
    SessionTracker, Event, Upsert, Retract, AdvanceWatermark,
)

t = SessionTracker(gap_ms=5, allowed_lateness_ms=10)

# 逐条接口
r = t.apply(Upsert(Event("src", seq=1, event_ms=0, value="x")))
r = t.apply(Retract("src", 1))
r = t.apply(AdvanceWatermark("src", 1000))

# 批量接口：逐条 apply 的便捷封装，结果与逐条调用完全一致
batch = t.apply_all([...])   # BatchResult(results, changes)

t.active_windows()           # 当前活跃窗口快照（可选按 source 过滤）
```

## 索引与每次修订的更新范围

每个 source 独立维护两个索引，任何操作都不扫描其它 source 或未触及的
窗口：

| 索引 | 结构 | 用途 |
| --- | --- | --- |
| `members` | `dict: seq -> Member` | O(1) 去重、撤回定位 |
| `windows` | 按 `start_ms` 排序的 `list[Window]` | 二分查找邻居/所属窗口 |

- **upsert**：1 次 `members` 插入；在 `windows` 上二分定位 `start <=
  event_ms + gap` 的最后一个窗口，只需检查它及其前一个（窗口两两间隔
  `> gap`，一个事件至多触及 2 个窗口）；切片替换这 ≤2 个条目为合并后
  的窗口，仅对被触及窗口的成员重排序。复杂度
  `O(log W + m)`，`m` 为被触及窗口的成员数。
- **retract**：1 次 `members` 删除；二分定位所属窗口，只重分组该窗口
  自己的成员，切片替换 1 → 0/1/2 个条目。复杂度 `O(log W + m)`。
- **watermark**：窗口按起点有序则终点也有序，可回收窗口构成列表前缀，
  一次切片弹出 `k` 个并从 `members` 删除其成员。复杂度
  `O(W + 回收成员数)`，摊还 O(1)/窗口。
- 每个操作的 changelog 通过 `_Delta` 记录“触及窗口 id 的前后快照”
  计算净变更，与全量重算的 diff 严格一致，但代价只与触及的窗口数有关。

## 状态回收

水位线按 source 单调推进（回退返回 `WATERMARK_IGNORED`）。视界
`horizon = watermark - allowed_lateness`：

- `event_ms < horizon` 的新事件 → `TOO_LATE`，不入索引；
- `end_ms + gap < horizon` 的窗口已不可能再被任何可接纳事件触及 →
  发 `FINALIZE` 并回收其全部成员状态（此后撤回其成员得到
  `NOT_FOUND`）。

## 对照实现与验证

`session_windows/reference.py` 是独立的暴力实现：保留全部事件，每个
操作后对该 source 全量排序重算会话，再 diff 前后窗口集得到变更流。
`tests/test_parity.py` 用随机操作序列（多 source、重复投递、负载更新、
撤回、水位线推进，多组 gap/lateness 参数 × 25 个种子 × 400 步）逐步
断言增量实现与对照实现的处置结果、净变更集合和活跃窗口完全一致。

## 运行测试

```bash
python3 -m pytest
```
