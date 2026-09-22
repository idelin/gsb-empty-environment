# session-windows

增量会话窗口库（Python 3.11，零依赖）。事件可以乱序、迟到、重复、被撤回；
库不只维护当前窗口，还向下游输出 **retract/upsert 变更流**，并保证窗口 ID
在合并/拆分时的延续规则，使消费方能无歧义地更新已有结果。

## 数据模型

- `Event(source, seq, event_ms, value)`：`(source, seq)` 是事件的持久标识。
- `Retract(source, seq)`：单独操作，按标识撤回已受理的事件。
- 会话定义：同一 `source` 内，按 `event_ms` 排序后相邻事件间隔 `<= gap_ms`
  的事件属于同一会话。会话成员只取决于存活事件集合，与到达顺序无关。

## 接口

```python
from session_windows import SessionWindowStore, Event, Retract

store = SessionWindowStore(gap_ms=5, allowed_lateness_ms=10)

res = store.apply(Event("a", seq=1, event_ms=0))     # 逐条
res = store.apply(Retract("a", seq=1))
batch = store.apply_batch([...])                     # 批量，等价于按序逐条
closes = store.advance_watermark("a", watermark_ms)  # 推进水位线，回收历史
store.snapshot()   # 当前全部存活窗口
store.windows("a") # 单 source 的窗口
store.stats()      # 各 source 保留状态规模（验证回收）
```

每个 `apply` 返回 `ApplyResult(disposition, changes)`；`apply_batch` 返回
`BatchResult`，其 `.changes` 是按应用顺序拼接的全部变更。

## 变更流与窗口 ID 延续规则

下游以 `window.id` 为键维护结果表，按顺序应用变更：

| 变更 | 含义 | 下游动作 |
| --- | --- | --- |
| `Upsert(window)` | 窗口新建或被修改 | 按 `window.id` 覆盖 |
| `RetractWindow(window_id, ...)` | 窗口消失（被合并或成员清空） | 删除该 id |
| `CloseWindow(window)` | 窗口超出允许迟到范围，被回收定型 | 删除并存档最终快照 |

ID 延续规则（保证消费方无歧义）：

- **原地扩展**：窗口吸收新事件，id 不变，发 `Upsert`。
- **合并**：一条记录桥接两个窗口时，合并结果保留 **`start_ms` 较小窗口的 id**，
  另一个 id 发 `RetractWindow`。
- **拆分**：撤回导致窗口分裂时，**包含原 `start_ms` 的片段保留原 id**，
  其余片段分配全新 id，各发 `Upsert`（无需 retract，因为原 id 仍存续）。
- id 单调分配、绝不复用。

示例（gap=5）：先到 0 和 9 形成两个窗口；补 4 时桥接合并——保留 0 所在窗口
的 id，retract 9 所在窗口的 id；撤回 4 后拆分——[0] 保留合并时的 id，[9]
获得新 id。任意到达顺序（0/4/9 的全部排列）最终成员一致，由
`test_final_membership_is_order_independent` 验证。

## 处置结果（Disposition）

每条记录都有明确处置：

- `APPLIED` / `RETRACTED`：受理，附带变更。
- `DUPLICATE`：同 `(source, seq)` 同内容重复投递，幂等忽略。
- `CONFLICT`：同标识不同内容，拒绝（先写胜出）。
- `LATE_REJECTED`：`event_ms < watermark - allowed_lateness`，超出允许迟到范围。
- `RETRACT_UNKNOWN`：撤回不存在、已撤回或已被回收的事件。

## 水位线与状态回收

水位线按 source 单调推进（回退/重复推进被忽略）。记
`horizon = watermark - allowed_lateness`：

- `event_ms < horizon` 的记录被拒绝（`LATE_REJECTED`）。
- 满足 `end_ms + gap_ms < horizon` 的会话不可能再被任何可受理记录触及，
  被回收并发出 `CloseWindow`（最终快照），其事件从索引中清除。
  注意 `end_ms + gap_ms == horizon` 时仍保留——边界上的迟到事件恰好还能并入。

## 每次修订更新的索引

每个 source 维护三个索引，单条事件只触及常数个会话，**不扫描全部保留历史**：

| 索引 | 结构 | 插入 | 撤回 |
| --- | --- | --- | --- |
| 去重/撤回表 `events` | `dict: seq -> (event_ms, value)` | O(1) 写入 | O(1) 删除 |
| 时间索引 `times` | 有序数组 `(event_ms, seq)` | 二分定位 O(log n) + 数组插入 | 二分定位 O(log n) + 删除 |
| 会话列表 `sessions` | 按 `start_ms` 有序、互不相交 | 二分定位后**至多触及相邻 2 个会话**（扩展或合并） | 二分定位**恰好 1 个会话**，仅对该会话成员重分段 O(m) |

- 合并：拼接两个会话的成员列表（二者本就相邻有序）。
- 拆分：只遍历被撤回事件所在会话的成员，与其余会话无关。
- 回收：会话按 `start_ms` 有序且互不相交，过期会话构成列表前缀，
  每次水位线推进只弹出前缀 O(k)（k 为回收个数），其成员同样是时间索引的前缀。
- 数组可用平衡树/跳表替换以把插入删除降为 O(log n)；接口语义不变。

## 对照实现与测试

`session_windows.reference.compute_windows` 是独立的小规模对照实现：每次把
全部事件重新排序、重新切分，不含任何增量状态。测试（`tests/`，14 个用例）：

- 规范示例：迟到记录合并两窗口、撤回后拆分，及全部到达顺序的最终成员一致性；
- 窗口闭合：水位线推进回收历史、边界条件（`end+gap == horizon` 不回收）、
  回收后撤回按 `RETRACT_UNKNOWN` 处置；
- 重复投递（`DUPLICATE`/`CONFLICT`）、撤回不存在事件（`RETRACT_UNKNOWN`）；
- 批量与逐条等价、多 source 隔离、三路拆分；
- 随机化测试：与对照实现逐事件比对最终窗口，并用一个**只消费变更流的
  下游镜像**逐步重放，验证 retract/upsert 流足以精确重建当前状态。

```bash
python3 -m pytest tests/
```
