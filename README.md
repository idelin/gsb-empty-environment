# session-windows

增量会话窗口库（Python 3.11+）。把乱序、迟到、可撤回的遥测事件维护成会话窗口，
并以 **retract/upsert changelog** 的形式把每一次变化交给下游，使消费方可以无歧义地
更新自己的物化结果。

## 数据模型

- `Event(source, seq, event_ms, value)`：原事件，由 `(source, seq)` 唯一标识。
- `RetractEvent(source, seq)`：独立撤回操作，指向同一标识。
- 同一 `source` 内，相邻事件时间差 `<= gap_ms` 即属同一会话窗口。

## 接口

```python
from session_windows import SessionWindowIndex, Event, RetractEvent

idx = SessionWindowIndex(gap_ms=5, allowed_lateness_ms=10)

disposition, changes = idx.apply(Event("s", 1, 0))          # 逐条
results = idx.apply_all([Event("s", 2, 9), RetractEvent("s", 1)])  # 批量
close_changes = idx.advance_watermark(1000)                  # 推进水位线并回收
snapshot = idx.windows()                                     # 当前存活窗口（调试用）
```

`apply` 返回 `(Disposition, list[Change])`；`apply_all` 返回
`[(op, Disposition, list[Change]), ...]`。

### Changelog 三种变更

| 变更 | 含义 | 下游动作 |
| --- | --- | --- |
| `Upsert(window)` | 新建或内容变化 | 按 `window.id` 覆盖 |
| `RetractWindow(window_id, source, last)` | 窗口被合并走或删空 | 删除该 id（`last` 为消失前内容） |
| `CloseWindow(window)` | 窗口闭合（终态），状态已回收 | 落终态存储，从活跃集合移除 |

### 窗口 ID 延续规则

- 新窗口分配单调递增、永不复用的 id。
- **合并**：一条事件桥接 k 个窗口时，**覆盖最早时间戳的窗口保留其 id**，
  其余 k-1 个 id 发 `RetractWindow`，幸存 id 发 `Upsert`（合并后成员）。
- **拆分**：撤回导致窗口断裂时，**含最早事件的分片保留原 id**，其余分片分配新 id，
  所有分片各发一条 `Upsert`。
- **闭合**：`watermark - allowed_lateness >= end_ms + gap_ms` 时窗口不可能再被
  任何可接受的事件扩展，发 `CloseWindow` 后回收其全部状态。

### 处置结果（Disposition）

| 值 | 条件 |
| --- | --- |
| `APPLIED` | 事件已插入 |
| `DUPLICATE` | `(source, seq)` 已存在，幂等忽略 |
| `REJECTED_LATE` | `event_ms <= watermark - allowed_lateness`，超出允许迟到范围 |
| `RETRACTED` | 撤回成功 |
| `RETRACTION_UNKNOWN` | 事件不存在或状态已被回收 |

## 示例（gap=5）

收到 `t=0`、`t=9` 两条 → 两个窗口；补到 `t=4` → 桥接合并为一个窗口
（保留 `t=0` 窗口的 id，另一 id 被 retract）；撤回 `t=4` → 重新拆成两个窗口
（含 `t=0` 的分片保留合并时的 id，含 `t=9` 的分片得到新 id）。
`{0, 4, 9}` 的全部 6 种到达顺序产生相同的最终成员（见
`tests/test_session_windows.py::MergeSplitTest`）。

## 索引结构与每次修订的开销

按 `source` 分片，每个来源维护：

| 索引 | 结构 | 服务的操作 |
| --- | --- | --- |
| `events` | `dict: seq -> (event_ms, value)` | O(1) 判重、撤回定位 |
| `starts` / `windows` | 按 `start_ms` 有序的窗口列表（窗口互不相交，end 亦有序） | `bisect` O(log w) 定位受影响邻域 |
| `seq_window` | `dict: seq -> window_id` | O(1) 找到被撤回事件所属窗口 |
| `by_id` | `dict: id -> window` | O(1) 取窗口 |

每次修订更新的索引：

- **插入事件**：`bisect` 定位 `[t-gap, t+gap]`；窗口间隔 `> gap` ⇒ 至多触及
  **2 个**既有窗口。合并时更新 `starts/windows` 的常数长度切片、被吸收窗口成员的
  `seq_window` 条目、`events` 一条。总开销 O(log w + 被合并成员数)。
- **撤回事件**：`seq_window` O(1) 定位窗口；只扫描**该窗口的成员**做断点重分组；
  分片原地拼接回 `starts/windows`（有序性由窗口不相交保证）。开销 O(窗口成员数)。
- **推进水位线**：可闭合窗口恰为有序列表的**前缀**（`end + gap <= horizon`），
  从头弹出即可，开销 O(#闭合窗口)，不扫描保留的历史。

任何操作都不会全量扫描保留历史。

## 对照实现与测试

`session_windows/reference.py` 是独立的朴素实现：保存全部存活事件，每次查询
重新排序、从头分组，与增量实现零共享代码。测试包括：

- 合并/拆分示例与 ID 延续规则；6 种到达顺序最终成员一致；
- 窗口闭合边界（`end+gap == horizon` 才闭合）、迟到拒绝边界（`event_ms == horizon` 拒绝）；
- 重复投递幂等、撤回不存在/已回收事件；
- 批量接口与逐条接口结果一致；
- 随机操作序列（含重复、撤回、水位线）下：逐条 disposition 与对照实现一致，
  changelog 在下游重放后始终等于当前状态，最终成员与对照实现一致。

运行：

```bash
python -m pytest tests/ -q        # 或 python -m unittest discover -s tests -v
```
